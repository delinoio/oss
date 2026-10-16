// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// grokDiagnosticPhases maps every domain phase to its wire enum member. The
// table test in grok_diagnostic_test.go asserts that no domain phase is missing.
var grokDiagnosticPhases = map[domain.GrokPhase]pb.GrokDiagnosticPhase{
	domain.GrokDiscovery: pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_DISCOVERY,
	domain.GrokVersion:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_VERSION,
	domain.GrokProfile:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_PROFILE,
	domain.GrokRuntime:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_RUNTIME,
	domain.GrokLogin:     pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN,
	domain.GrokRefresh:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH,
	domain.GrokLogout:    pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT,
	domain.GrokModels:    pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_MODELS,
	domain.GrokExecution: pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_EXECUTION,
	domain.GrokHistory:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_HISTORY,
	domain.GrokCleanup:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_CLEANUP,
}

// grokDiagnosticMessage returns only locally reconstructed, validated metadata.
// Invalid stored metadata is omitted rather than forwarded.
func grokDiagnosticMessage(d *domain.GrokDiagnostic) *pb.GrokDiagnostic {
	if d == nil || d.Validate() != nil {
		return nil
	}
	return &pb.GrokDiagnostic{DetectedVersion: d.DetectedVersion, SupportedVersion: d.SupportedVersion, Phase: grokDiagnosticPhases[d.Phase], Code: string(d.Code), Message: d.Message, Guidance: d.Guidance, CorrelationId: d.CorrelationID}
}

// grokAdmissionFailure classifies a refused Grok installation by the original
// phase and preserves the failure code that discovery or protocol validation
// already recorded. Discovery validity and the subscription version baseline
// are decided first: a release between the discovery baseline and the
// subscription baseline can retain a stored profile diagnostic from its probe,
// but admission must still report it as a version failure. Only an installation
// that is discovered and at or above the subscription baseline restores its
// stored failed-validation diagnostic unchanged, so the first native failure
// stays authoritative.
//
// Classification table (installation -> phase / code):
//
//	no Grok installation reported         -> discovery / unsupported
//	missing                               -> discovery / not_found
//	permission-denied                     -> discovery / permission_denied
//	incompatible                          -> discovery / unsupported
//	failed                                -> discovery / unavailable
//	unchecked, or detected without a
//	  resolved path, with no observation
//	  time, or with an observation time
//	  later than the admission time plus
//	  grokObservationSkew                 -> discovery / unsupported
//	detected with a recorded problem      -> discovery / the problem's code
//	version below the baseline or invalid -> version / unsupported
//	stored Grok diagnostic                -> the stored phase and code
//	protocol failed                       -> profile / unavailable
//	protocol unsupported or unverified    -> profile / unsupported
//
// The admission time is captured once by the caller and shared with the
// admission predicate (grokDiscoveryObserved), so the refusal and its
// classification can never disagree about a future-dated observation.
func grokAdmissionFailure(found *domain.Installation, now time.Time) error {
	if found == nil {
		return domain.WithGrokDiagnostic("", domain.GrokDiscovery, grokInstallationRequired())
	}
	if problem := grokDiscoveryProblem(found); problem != nil {
		return domain.WithGrokDiagnostic(found.Version, domain.GrokDiscovery, problem)
	}
	switch {
	case !grokDiscoveryObserved(found, now):
		return domain.WithGrokDiagnostic(found.Version, domain.GrokDiscovery, grokInstallationRequired())
	case !domain.GrokSubscriptionVersionAllowed(found.Version):
		return domain.GrokVersionFailure(found.Version)
	}
	if stored := found.Protocol; stored != nil && stored.GrokDiagnostic != nil && stored.GrokDiagnostic.DetectedVersion == found.Version && stored.GrokDiagnostic.Validate() == nil {
		return domain.RestoreGrokDiagnostic(*stored.GrokDiagnostic)
	}
	if found.Protocol != nil {
		if problem := domain.ProtocolProblem(found.Protocol.State); problem != nil {
			return domain.WithGrokDiagnostic(found.Version, domain.GrokProfile, problem)
		}
	}
	return domain.WithGrokDiagnostic(found.Version, domain.GrokProfile, grokInstallationRequired())
}

// grokObservationSkew is the clock skew tolerated between the Runner Device
// observation time and the server's admission time.
const grokObservationSkew = time.Second

// grokDiscoveryObserved reports whether discovery produced usable evidence for
// the installation at the given admission time: it is detected, has a resolved
// path and has a recorded observation time that is not in the future beyond
// grokObservationSkew (for example after the server clock moved backward). It
// is the single predicate shared by admission and its classification.
func grokDiscoveryObserved(found *domain.Installation, now time.Time) bool {
	return found.State == domain.InstallationDetected && found.ResolvedPath != "" && found.ObservedAt != nil && !found.ObservedAt.IsZero() && !found.ObservedAt.After(now.Add(grokObservationSkew))
}

// grokDiscoveryProblem returns the safe problem that discovery recorded for the
// installation, reconstructed locally from its state so no Worker text is
// trusted. It falls back to a recorded problem on a detected installation.
func grokDiscoveryProblem(found *domain.Installation) error {
	if problem := domain.InstallationProblem(found.State); problem != nil {
		return problem
	}
	if found.Problem != nil {
		return found.Problem
	}
	return nil
}

func grokInstallationRequired() *domain.Error {
	return domain.Fail(domain.Unsupported, "Grok subscription authentication requires a verified, supported Grok Build installation.", "Discover the installed Grok Build on the selected Runner Device and install the pinned supported release yourself.")
}

// grokSettledOperationWindow bounds how long the progress presentation of a
// settled Grok operation is retained when it has no server-owned deadline.
const grokSettledOperationWindow = 15 * time.Minute

// grokActionPhase maps a Grok lifecycle action to the phase that reports a
// native failure of that action.
func grokActionPhase(action domain.SubscriptionAction) domain.GrokPhase {
	switch action {
	case domain.SubscriptionRefresh:
		return domain.GrokRefresh
	case domain.SubscriptionLogout:
		return domain.GrokLogout
	default:
		return domain.GrokLogin
	}
}

// grokLocalDiagnostic builds the closed diagnostic for a failure that the
// server itself observed. Worker-reported failures carry no native text, so the
// message and guidance are always reconstructed from phase, code and versions.
func grokLocalDiagnostic(version string, phase domain.GrokPhase, code domain.Code) *domain.GrokDiagnostic {
	return domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic(version, phase, domain.Fail(code, "The native operation did not complete.", "Check the Grok Build installation.")))
}

// grokInstalledVersion returns the valid version of the Runner Device's single
// Grok Build installation, or "" when it is absent, ambiguous or malformed. It
// is only presentation metadata and never authorizes anything.
func grokInstalledVersion(tx *store.Tx, machine domain.ID) string {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return ""
	}
	version, count := "", 0
	for i := range m.Installations {
		if m.Installations[i].Harness == domain.GrokBuild {
			version, count = m.Installations[i].Version, count+1
		}
	}
	if count != 1 || !domain.ValidInstallationVersion(version) {
		return ""
	}
	return version
}

// grokSettlement is the settled outcome of one Worker-owned Grok lifecycle
// operation, as GetSubscriptionProgress presents it.
type grokSettlement struct {
	Operation  domain.ID
	Action     domain.SubscriptionAction
	Actor      domain.Principal
	Epoch      domain.ID
	State      domain.SubscriptionLoginState
	Generation domain.ID
	Diagnostic *domain.GrokDiagnostic
}

// settleGrokOperation records the outcome of a Grok operation on the account's
// ServerOperation, which is the only record GetSubscriptionProgress serves. The
// original diagnostic is attached with the operation ID as its correlation ID.
// A settled outcome is final; only a refused Take (unsupported), which leaves the
// operation claimable, is replaced. An active operation of another owner is never
// overwritten. It reports whether the state changed. The caller holds the account
// gate and persists the account.
func settleGrokOperation(st *domain.SubscriptionState, in grokSettlement, now time.Time) bool {
	o := st.ServerOperation
	if o != nil && o.ID != in.Operation {
		if o.Active() || o.NativeStarted || o.CleanupPhase != "" {
			return false
		}
		o = nil
	}
	if o != nil && o.State != domain.SubscriptionUnsupported {
		return false
	}
	var diagnostic *domain.GrokDiagnostic
	if in.Diagnostic != nil {
		value := *in.Diagnostic
		value.CorrelationID = string(in.Operation)
		if value.Validate() == nil {
			diagnostic = &value
		}
	}
	if o != nil && o.State == in.State && o.Generation == in.Generation && (o.GrokDiagnostic == nil) == (diagnostic == nil) && (diagnostic == nil || *o.GrokDiagnostic == *diagnostic) {
		return false
	}
	if o == nil {
		o = &domain.ServerSubscriptionOperation{ID: in.Operation, Action: in.Action, Epoch: in.Epoch, FinishID: domain.NewID(), Actor: in.Actor, StartedAt: now, ExpiresAt: now.Add(grokSettledOperationWindow)}
		st.ServerOperation = o
	}
	o.State, o.Generation, o.GrokDiagnostic = in.State, in.Generation, diagnostic
	return true
}

// grokRecordOutcome is what a Grok diagnostic bookkeeping mutation actually did
// to the account. A log line that says "recorded" must be backed by
// grokRecordPersisted, never by the intent to record.
type grokRecordOutcome string

const (
	// grokRecordPersisted: the mutation changed the stored operation record and
	// committed it.
	grokRecordPersisted grokRecordOutcome = "persisted"
	// grokRecordTargetChanged: the account no longer holds the exact operation
	// the mutation was meant to annotate, so it wrote nothing.
	grokRecordTargetChanged grokRecordOutcome = "target_changed"
	// grokRecordKept: the stored record already settles the operation (an
	// identical refusal, a settled outcome or another owner's active operation)
	// and stays authoritative, so the mutation wrote nothing.
	grokRecordKept grokRecordOutcome = "record_kept"
	// grokRecordInvalid: the diagnostic failed local validation and was dropped.
	grokRecordInvalid grokRecordOutcome = "diagnostic_invalid"
)

// recordGrokTakeRefusal makes the original refusal of a Take visible through
// progress. The refusal does not consume the operation: it stays queued and a
// later Take at a supported installation supersedes this record. The write is
// bookkeeping bounded and detached from the refused request, so it also
// persists when the request is canceled. The caller holds the account gate.
func (s *Service) recordGrokTakeRefusal(requestCtx context.Context, account, operation, machine domain.ID, action domain.SubscriptionAction, d *domain.GrokDiagnostic) {
	ctx, cancel := grokBookkeepingContext(requestCtx, grokBookkeepingTimeout)
	defer cancel()
	var outcome grokRecordOutcome
	_, err := s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.take-refusal", struct{ Account, Operation domain.ID }{account, operation}, func(tx *store.Tx) (any, error) {
		outcome = grokRecordTargetChanged
		r, a, err := grokSubscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		// The refusal is validated again here because the refused Take's own
		// transaction rolled back: the operation, action and Runner Device must
		// still be the account's exact queued original.
		p := grokTakeRefusalTarget(tx, a, operation, machine, action)
		if p == nil {
			return struct{}{}, nil
		}
		st := a.Subscription
		if !settleGrokOperation(st, grokSettlement{Operation: operation, Action: p.Action, Actor: p.Actor, Epoch: s.subscriptionServerEpoch(), State: domain.SubscriptionUnsupported, Diagnostic: d}, time.Now().UTC()) {
			outcome = grokRecordKept
			return struct{}{}, nil
		}
		outcome = grokRecordPersisted
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	// The event is chosen by what the mutation persisted: a failed commit is
	// never reported as recorded, and a mutation that wrote nothing says why.
	switch {
	case err != nil:
		s.logger.WarnContext(ctx, "grok_subscription_take_refusal_unrecorded", "account_id", account, "operation_id", operation, "code", domain.SafeError(err).Code)
	case outcome == grokRecordPersisted:
		s.logger.InfoContext(ctx, "grok_subscription_take_refusal_recorded", "account_id", account, "operation_id", operation, "phase", d.Phase, "code", d.Code, "installed_version", d.DetectedVersion, "supported_version", d.SupportedVersion)
	default:
		s.logger.InfoContext(ctx, "grok_subscription_take_refusal_not_recorded", "account_id", account, "operation_id", operation, "reason", outcome, "phase", d.Phase, "code", d.Code)
	}
}

// recordGrokCleanupFailure replaces the presentation of a succeeded or canceled
// operation whose metadata already committed with a cleanup diagnostic, because
// the account still owes removal of superseded credential generations. A native
// failure that Finish already settled keeps its own diagnostic. The account is
// fenced for recovery separately. The caller holds the account gate.
func (s *Service) recordGrokCleanupFailure(requestCtx context.Context, account, operation domain.ID, version string, cause error) {
	ctx, cancel := grokBookkeepingContext(requestCtx, grokBookkeepingTimeout)
	defer cancel()
	d := grokLocalDiagnostic(version, domain.GrokCleanup, domain.SafeError(cause).Code)
	var outcome grokRecordOutcome
	_, err := s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.cleanup-failure", struct{ Account, Operation domain.ID }{account, operation}, func(tx *store.Tx) (any, error) {
		outcome = grokRecordTargetChanged
		r, a, err := grokSubscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil {
			return struct{}{}, nil
		}
		o := a.Subscription.ServerOperation
		if o.ID != operation || o.Active() || o.NativeStarted || o.CleanupPhase != "" {
			return struct{}{}, nil
		}
		// The first failure stays authoritative: a native failure that Finish
		// already settled keeps its diagnostic, and the recovery fence reports the
		// outstanding cleanup. Only a succeeded or canceled outcome is replaced.
		if o.State == domain.SubscriptionFailed && o.GrokDiagnostic != nil {
			outcome = grokRecordKept
			return struct{}{}, nil
		}
		value := *d
		value.CorrelationID = string(operation)
		if value.Validate() != nil {
			outcome = grokRecordInvalid
			return struct{}{}, nil
		}
		o.State, o.Generation, o.GrokDiagnostic = domain.SubscriptionFailed, "", &value
		outcome = grokRecordPersisted
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	// "recorded" is logged only when the cleanup diagnostic was persisted. When an
	// earlier native diagnostic stays authoritative the cleanup failure is still
	// real: the account is fenced for recovery, so responders get a distinct event.
	switch {
	case err != nil:
		s.logger.WarnContext(ctx, "grok_subscription_cleanup_failure_unrecorded", "account_id", account, "operation_id", operation, "code", domain.SafeError(err).Code)
	case outcome == grokRecordPersisted:
		s.logger.WarnContext(ctx, "grok_subscription_cleanup_failure_recorded", "account_id", account, "operation_id", operation, "phase", d.Phase, "code", d.Code)
	default:
		s.logger.WarnContext(ctx, "grok_subscription_cleanup_failure_not_recorded", "account_id", account, "operation_id", operation, "reason", outcome, "cleanup_phase", d.Phase, "cleanup_code", d.Code)
	}
}

// grokProgress is the presentation of one Grok lifecycle operation.
type grokProgress struct {
	State      domain.SubscriptionLoginState
	Canceled   bool
	Diagnostic *domain.GrokDiagnostic
	Generation domain.ID
	// Provisional marks the one refusal that a later claim can still supersede:
	// an unsupported record whose original operation is still queued and
	// claimable. Clients keep polling a provisional response and treat every
	// other response as final. The wire carries it as `unsupported` together with
	// the Grok diagnostic, which is served only while the response is provisional.
	Provisional bool
}

// grokProgressView maps the account's operation records to the progress the
// original actor may read. It returns the owning actor and false when the
// operation is not the account's original one.
//
// A Grok operation is worker-owned and has no ServerOperation until it settles
// (or until a refused Take records its provisional outcome), so the record
// alone cannot describe an operation that is still queued or claimed. Every
// transition maps as follows:
//
//	queued, no record                 -> preparing
//	claimed, no record                -> waiting
//	queued or claimed, canceled flag  -> unchanged state with canceled=true
//	take refused (unsupported record) -> unsupported with the original diagnostic
//	                                     (provisional: the queued operation stays claimable)
//	finished (succeeded/failed/...)   -> the record's state and diagnostic
//	canceled record                   -> canceled, never a failure diagnostic
//	expired (record past its window)  -> expired
//	account recovery fence            -> recovery-required
func grokProgressView(st *domain.SubscriptionState, operation string, connected bool, epoch domain.ID, now time.Time) (grokProgress, domain.Principal, bool) {
	if st == nil {
		return grokProgress{}, domain.Principal{}, false
	}
	pending := st.Pending
	if pending != nil && string(pending.ID) != operation {
		pending = nil
	}
	o := st.ServerOperation
	if o == nil || string(o.ID) != operation {
		if pending == nil {
			return grokProgress{}, domain.Principal{}, false
		}
		view := grokProgress{State: domain.SubscriptionPreparing, Canceled: pending.Canceled}
		if pending.Phase == domain.SubscriptionClaimed {
			view.State = domain.SubscriptionWaiting
		}
		if st.RecoveryRequired {
			view.State = domain.SubscriptionRecovery
		}
		return view, pending.Actor, true
	}
	state := o.State
	if st.RecoveryRequired || o.Active() && o.Epoch != epoch {
		state = domain.SubscriptionRecovery
	}
	if (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) && !o.ExpiresAt.After(now) {
		state = domain.SubscriptionExpired
	}
	view := grokProgress{State: state, Canceled: state == domain.SubscriptionCanceled || pending != nil && pending.Canceled}
	// A canceled operation never carries a failure diagnostic, even if a stale
	// one survived from an earlier refused Take.
	if o.State != domain.SubscriptionCanceled {
		view.Diagnostic = o.GrokDiagnostic
	}
	// An unsupported response is provisional only while its original operation is
	// still queued, uncanceled and claimable. Any other unsupported record has no
	// claim left to supersede it, so it is served as final without the diagnostic
	// that clients read as the provisional marker.
	if state == domain.SubscriptionUnsupported {
		view.Provisional = pending != nil && pending.Phase == domain.SubscriptionQueued && !pending.Canceled
		if !view.Provisional {
			view.Diagnostic = nil
		}
	}
	// A succeeded login or refresh reports the generation its Finish committed,
	// so the client can name the connected account. Grok produces no suggested
	// name, and the generation is withheld unless the account still references it.
	if state == domain.SubscriptionSucceeded && connected && o.Generation != "" && st.Generation == o.Generation {
		view.Generation = o.Generation
	}
	return view, o.Actor, true
}

// grokSubscriptionProgress reports the original Grok operation's state, its
// safe diagnostic and, once it succeeded, the committed generation. It never
// carries URLs, codes or identity outside the transient waiting presentation
// of a server-owned login (authorization URL, or device verification URL and
// user code), and the Codex
// diagnostic (7) and native diagnostic (9) fields stay unset.
func (s *Service) grokSubscriptionProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	response := &pb.GetSubscriptionProgressResponse{}
	provisional := false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		view, owner, ok := grokProgressView(a.Subscription, req.Msg.OperationId, a.Connection != nil, s.subscriptionServerEpoch(), time.Now().UTC())
		if !ok || owner != actor {
			return subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, owner); err != nil {
			return err
		}
		response.State = loginState(view.State)
		response.Canceled = view.Canceled
		response.GrokDiagnostic = grokDiagnosticMessage(view.Diagnostic)
		response.Generation = string(view.Generation)
		provisional = view.Provisional
		// The authorization URL of a server-owned browser login is transient: it
		// is served only while its own epoch's operation is waiting and live.
		if o := a.Subscription.ServerOperation; o != nil && string(o.ID) == req.Msg.OperationId && view.State == domain.SubscriptionWaiting && !view.Canceled && o.Epoch == s.subscriptionServerEpoch() {
			progress := s.subscriptionProgress[o.ID]
			if !progress.Until.IsZero() && !time.Now().Before(progress.Until) {
				delete(s.subscriptionProgress, o.ID)
			} else {
				response.Url = progress.URL
				response.UserCode = progress.UserCode
			}
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_progress_observed", "operation_id", req.Msg.OperationId, "state", response.State, "diagnostic_reported", response.GrokDiagnostic != nil, "provisional", provisional)
	return connect.NewResponse(response), nil
}
