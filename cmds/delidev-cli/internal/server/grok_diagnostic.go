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
// phase. Discovery validity and the subscription version baseline are decided
// first: a release between the discovery baseline and the subscription
// baseline can retain a stored profile diagnostic from its probe, but admission
// must still report it as a version failure. Only an installation that is
// discovered and at or above the subscription baseline restores its stored
// failed-validation diagnostic unchanged, so the first native failure stays
// authoritative.
func grokAdmissionFailure(found *domain.Installation) error {
	if found == nil {
		return domain.WithGrokDiagnostic("", domain.GrokDiscovery, grokInstallationRequired())
	}
	switch {
	case found.State != domain.InstallationDetected || found.ResolvedPath == "" || found.ObservedAt == nil || found.ObservedAt.IsZero():
		return domain.WithGrokDiagnostic(found.Version, domain.GrokDiscovery, grokInstallationRequired())
	case !domain.GrokSubscriptionVersionAllowed(found.Version):
		return domain.GrokVersionFailure(found.Version)
	}
	if stored := found.Protocol; stored != nil && stored.GrokDiagnostic != nil && stored.GrokDiagnostic.DetectedVersion == found.Version && stored.GrokDiagnostic.Validate() == nil {
		return domain.RestoreGrokDiagnostic(*stored.GrokDiagnostic)
	}
	return domain.WithGrokDiagnostic(found.Version, domain.GrokProfile, grokInstallationRequired())
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

// recordGrokTakeRefusal makes the original refusal of a Take visible through
// progress. The refusal does not consume the operation: it stays queued and a
// later Take at a supported installation supersedes this record. The write is
// bookkeeping bounded and detached from the refused request, so it also
// persists when the request is canceled. The caller holds the account gate.
func (s *Service) recordGrokTakeRefusal(requestCtx context.Context, account, operation, machine domain.ID, d *domain.GrokDiagnostic) {
	ctx, cancel := grokBookkeepingContext(requestCtx, grokBookkeepingTimeout)
	defer cancel()
	recorded := false
	_, err := s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.take-refusal", struct{ Account, Operation domain.ID }{account, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired || st.Lease != nil || st.Pending == nil {
			return struct{}{}, nil
		}
		p := st.Pending
		if p.ID != operation || p.MachineID != machine || p.Phase != domain.SubscriptionQueued || p.Canceled {
			return struct{}{}, nil
		}
		if !settleGrokOperation(st, grokSettlement{Operation: operation, Action: p.Action, Actor: p.Actor, Epoch: s.subscriptionServerEpoch(), State: domain.SubscriptionUnsupported, Diagnostic: d}, time.Now().UTC()) {
			return struct{}{}, nil
		}
		recorded = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_take_refusal_unrecorded", "account_id", account, "operation_id", operation, "code", domain.SafeError(err).Code)
		return
	}
	if recorded {
		s.logger.InfoContext(ctx, "grok_subscription_take_refusal_recorded", "account_id", account, "operation_id", operation, "phase", d.Phase, "code", d.Code, "installed_version", d.DetectedVersion, "supported_version", d.SupportedVersion)
	}
}

// recordGrokCleanupFailure replaces the presentation of an operation whose
// metadata already committed with a cleanup diagnostic, because the account
// still owes removal of superseded credential generations. The account is
// fenced for recovery separately. The caller holds the account gate.
func (s *Service) recordGrokCleanupFailure(requestCtx context.Context, account, operation domain.ID, version string, cause error) {
	ctx, cancel := grokBookkeepingContext(requestCtx, grokBookkeepingTimeout)
	defer cancel()
	d := grokLocalDiagnostic(version, domain.GrokCleanup, domain.SafeError(cause).Code)
	_, err := s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.cleanup-failure", struct{ Account, Operation domain.ID }{account, operation}, func(tx *store.Tx) (any, error) {
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
		value := *d
		value.CorrelationID = string(operation)
		if value.Validate() != nil {
			return struct{}{}, nil
		}
		o.State, o.Generation, o.GrokDiagnostic = domain.SubscriptionFailed, "", &value
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_cleanup_failure_unrecorded", "account_id", account, "operation_id", operation, "code", domain.SafeError(err).Code)
		return
	}
	s.logger.WarnContext(ctx, "grok_subscription_cleanup_failure_recorded", "account_id", account, "operation_id", operation, "phase", d.Phase, "code", d.Code)
}

// grokSubscriptionProgress reports the original Grok operation's state, its
// safe diagnostic and, once it succeeded, the committed generation. It never
// carries URLs, codes or identity, and the Codex
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
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || string(st.ServerOperation.ID) != req.Msg.OperationId || st.ServerOperation.Actor != actor {
			return subscriptionDenied()
		}
		o := st.ServerOperation
		if err := subscriptionActorValid(tx, o.Actor); err != nil {
			return err
		}
		state := o.State
		if st.RecoveryRequired || o.Active() && o.Epoch != s.subscriptionServerEpoch() {
			state = domain.SubscriptionRecovery
		}
		if (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) && !o.ExpiresAt.After(time.Now().UTC()) {
			state = domain.SubscriptionExpired
		}
		response.State = loginState(state)
		response.Canceled = state == domain.SubscriptionCanceled || st.Pending != nil && st.Pending.ID == o.ID && st.Pending.Canceled
		response.GrokDiagnostic = grokDiagnosticMessage(o.GrokDiagnostic)
		// A succeeded login or refresh reports the generation its Finish committed,
		// so the client can name the connected account. Grok produces no suggested
		// name, and the generation is withheld unless the account still references it.
		if state == domain.SubscriptionSucceeded && a.Connection != nil && o.Generation != "" && st.Generation == o.Generation {
			response.Generation = string(o.Generation)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_progress_observed", "operation_id", req.Msg.OperationId, "state", response.State, "diagnostic_reported", response.GrokDiagnostic != nil)
	return connect.NewResponse(response), nil
}
