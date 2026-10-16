package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// maxSubscriptionLeaseWait caps the delay between busy Take retries for one
// waiting operation.
const maxSubscriptionLeaseWait = 5 * time.Second

// subscriptionMemberNotAdmittedCause is the server's typed proof that a joined
// execution member was released before any native side effect.
const subscriptionMemberNotAdmittedCause = "subscription_member_not_admitted"

type managedSubscriptionLease struct {
	paidCredits bool
	logger      *slog.Logger
	client      delidevv1connect.SubscriptionServiceClient
	credential  Credential
	instance    domain.ID
	account     domain.ID
	response    *pb.TakeSubscriptionResponse
	finishID    domain.ID
	journalPath string
	journal     managedSubscriptionJournal
	// grokHome is the Grok home bound at materialization; Grok Finish retires the
	// credential only in that directory.
	grokHome subscription.GrokHome
	// grokRetry is set while a Grok Finish reported its private cleanup as
	// unconfirmed; the original owner's retry consumes it.
	grokRetry *grokCleanupRetry
	// recoveryCapture is set when the current attach negotiated managed ChatGPT
	// recovery: the lease then synchronizes a protected capture of the latest
	// validated bundle before deleting its transient authentication.
	recoveryCapture bool
}

type managedJournalState string

const (
	managedNotAdmitted managedJournalState = "not-admitted"
	// managedNotDelivered records the server's typed proof that a managed title
	// lease was released without any credential delivery.
	managedNotDelivered managedJournalState = "not-delivered"
	managedClaimed      managedJournalState = "claimed"
	managedClosed       managedJournalState = "closed"
	managedReported     managedJournalState = "reported"
)

type managedSubscriptionJournal struct {
	CleanupFinish   domain.ID `json:"cleanup_finish,omitempty"`
	LeaseRevision   uint64    `json:"lease_revision,omitempty"`
	Version         uint32    `json:"version"`
	NativeProfileID domain.ID `json:"native_profile_id,omitempty"`
	// GrokHome is the private $GROK_HOME a Grok execution lease materializes its
	// credential in. It is recorded before the credential is written, so a crash or
	// restart can retire the credential in exactly that directory. The path lives
	// only in this private journal and is never logged.
	GrokHome string `json:"grok_home,omitempty"`
	// OwnerLease names the shared account owner a joined execution member
	// belongs to. A founding or singleton lease omits it.
	OwnerLease                                              domain.ID `json:"owner_lease,omitempty"`
	NativeIdentity                                          string    `json:"native_identity,omitempty"`
	ExecutionStarted                                        bool      `json:"execution_started,omitempty"`
	NativeStarted                                           bool      `json:"native_started,omitempty"`
	CodeSubmissionID                                        domain.ID `json:"code_submission_id,omitempty"`
	Lease, Account, Operation, Instance, Finish, Generation domain.ID
	Action                                                  pb.SubscriptionAction
	State                                                   managedJournalState
	Cleanup, Refresh, Success                               bool
	// OwnerProfileCleaned records that recovery of a lost shared owner already
	// logged out the canonical profile. A shared owner never infers this from any
	// member's per-session Cleanup or NativeIdentity flags.
	OwnerProfileCleaned bool `json:"owner_profile_cleaned,omitempty"`
	// Installation is the exact original Claude executable evidence the native
	// profile was opened with. Historical device-owned cleanup reuses it instead
	// of re-resolving an installation from a later inventory. Journals written
	// before this field existed omit it.
	Installation *domain.Installation `json:"installation,omitempty"`
	// InstallationRetention marks a journal written by a Worker that retains
	// Installation before any native profile side effect. It is set with the
	// durable claim, before the lease is taken, so a current-schema journal
	// without Installation proves the native profile was never opened, while a
	// journal without this marker predates retention and proves nothing.
	InstallationRetention bool `json:"installation_retention,omitempty"`
	// AccountPreserved records that recovery proved the native profile was never
	// opened, so the final settlement keeps the account connected instead of
	// confirming a logout cleanup. It survives an interrupted recovery.
	AccountPreserved bool `json:"account_preserved,omitempty"`
	// Server is the stable server identity the lease was granted by. Recovery
	// accepts only evidence recorded for the same server; journals written before
	// this field existed omit it.
	Server domain.ID `json:"server,omitempty"`
	// NativeOwnerEvidence marks a journal written by a Worker that creates and
	// journals every native process owner's index (NativeOwners) before any native
	// process holding the credential can start. Only such a journal can prove that
	// an owner without a process index never started a process; a journal without
	// the marker proves nothing. It is set with the durable claim.
	NativeOwnerEvidence bool `json:"native_owner_evidence,omitempty"`
	// NativeOwners lists the process owners whose index directory was created, and
	// journaled here, immediately before the credentialed native launch of this
	// lease (the job and, for a shared owner, the owner lease itself).
	NativeOwners []domain.ID `json:"native_owners,omitempty"`
	// AuthHome is the canonical native directory the lease's transient
	// authentication was written to. It is recorded before the credential is
	// written, lives only in this private journal and is never logged.
	AuthHome string `json:"auth_home,omitempty"`
	// CaptureRequest is the durable identity of the protected recovery capture of
	// the latest validated bundle, CaptureDigest the digest of the captured bundle
	// and CaptureSynced whether the server confirmed it.
	CaptureRequest domain.ID `json:"capture_request,omitempty"`
	CaptureDigest  string    `json:"capture_digest,omitempty"`
	CaptureSynced  bool      `json:"capture_synced,omitempty"`
	// Recovery* retain the durable recovery request once the original cleanup was
	// proven, so a crash or lost response resends exactly the same request.
	RecoveryRequest domain.ID `json:"recovery_request,omitempty"`
	RecoveryCapture domain.ID `json:"recovery_capture,omitempty"`
	RecoveryReport  int32     `json:"recovery_report,omitempty"`
	RecoveryCleaned bool      `json:"recovery_cleaned,omitempty"`
}

func subscriptionRPC(ctx context.Context, config Config, credential Credential) (delidevv1connect.SubscriptionServiceClient, func(), error) {
	// The first protected credential read can wait for native code validation or
	// OS authentication. The control-plane 15-second header bound can abandon a
	// committed lease before that read completes. Keep a bounded wait while
	// preserving the original caller cancellation and the single-delivery fence.
	httpClient, closeHTTP, err := networkHTTPClientForHeaders(ctx, config, credential, time.Minute)
	if err != nil {
		return nil, nil, err
	}
	return delidevv1connect.NewSubscriptionServiceClient(httpClient, credential.Endpoint, subscriptionRPCOptions()...), closeHTTP, nil
}

// subscriptionRPCMaxBytes bounds one subscription message in either direction.
// A recovery capture request or response carries both the original and the latest
// maximum-size bundle plus protobuf framing and the mutation and ownership
// fields, so the bound covers two maximum bundles with generous overhead.
const subscriptionRPCMaxBytes = 2*subscription.MaxBundle + 64<<10

func subscriptionRPCOptions() []connect.ClientOption {
	return []connect.ClientOption{connect.WithReadMaxBytes(subscriptionRPCMaxBytes), connect.WithSendMaxBytes(subscriptionRPCMaxBytes)}
}

func takeManagedSubscription(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account, operation domain.ID, revision uint64, action pb.SubscriptionAction) (*managedSubscriptionLease, error) {
	lease, release, err := takeManagedSubscriptionAdmitted(ctx, config, client, credential, instance, account, operation, revision, action, nil)
	release()
	return lease, err
}

// takeManagedSubscriptionAdmitted is takeManagedSubscription with an optional
// per-account admission gate. The gate is held only around each individual Take
// request and, when the Take is granted, stays held until the caller has
// registered the granted lease locally and calls the returned release. A waiting
// retry never holds it, so a finishing sibling can still retire the owner. The
// returned release is always safe to call.
type subscriptionAdmissionGate interface {
	acquire(context.Context) error
	release()
}

func takeManagedSubscriptionAdmitted(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account, operation domain.ID, revision uint64, action pb.SubscriptionAction, gate subscriptionAdmissionGate) (*managedSubscriptionLease, func(), error) {
	held := false
	release := func() {
		if held {
			held = false
			gate.release()
		}
	}
	leaseID := domain.NewID()
	root := filepath.Join(config.Root, "managed-auth")
	if err := security.PrivateDir(root); err != nil {
		return nil, release, domain.SafeError(err)
	}
	// A durable claim precedes the protected delivery and every native side
	// effect. Reconnect never regenerates this claim or replays the login.
	claim := managedSubscriptionJournal{Version: 1, Lease: leaseID, Account: account, Operation: operation, Instance: instance, Finish: domain.NewID(), Action: action, State: managedClaimed, InstallationRetention: true, NativeOwnerEvidence: true, Server: credential.ServerID}
	journalPath := filepath.Join(root, string(leaseID)+".json")
	if err := writeJSON(journalPath, claim); err != nil {
		return nil, release, subscription.Invalid()
	}
	var response *connect.Response[pb.TakeSubscriptionResponse]
	var err error
	waitingLogged := false
	// Parallel sessions can wait for one account at the same time. Back off the
	// busy retry so each waiting operation costs at most one Take per
	// maxSubscriptionLeaseWait after the first few attempts.
	wait := time.Second
	for {
		if gate != nil {
			if acquireErr := gate.acquire(ctx); acquireErr != nil {
				return nil, release, domain.SafeError(acquireErr)
			}
			held = true
		}
		response, err = client.TakeSubscription(ctx, authenticated(credential, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(leaseID), Id: string(account), ExpectedRevision: revision}, MachineId: string(credential.MachineID), InstanceId: string(instance), OperationId: string(operation), Action: action}))
		if err == nil {
			break
		}
		// A refused attempt grants nothing, so the admission gate is released
		// before any classification or wait.
		release()
		problem := rpc.ClientError(err)
		safe := domain.SafeError(problem)
		lifecycle := action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN || action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH || action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
		if lifecycle && safe.Code == domain.Canceled && safe.Cause == "subscription_take_not_admitted" {
			// This exact server proof precedes every lease/native side effect.
			// Record local retirement without fabricating a Finish acknowledgment.
			claim.State = managedNotAdmitted
			if err := writeJSON(journalPath, claim); err != nil {
				return nil, release, subscription.Invalid()
			}
			return nil, release, &managedTakeNotAdmitted{problem}
		}
		if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE && safe.Cause == "subscription_title_not_delivered" {
			// The server committed and then released this exact managed title lease
			// without returning a credential, so there is no native side effect and
			// nothing to finish. The title reports an ordinary definite failure; the
			// lease is never an uncertain execution.
			claim.State = managedNotDelivered
			if err := writeJSON(journalPath, claim); err != nil {
				return nil, release, subscription.Invalid()
			}
			if config.Logger != nil {
				config.Logger.InfoContext(ctx, "subscription_title_not_delivered", "account_id", account, "operation_id", operation)
			}
			return nil, release, &managedTakeNotDelivered{problem}
		}
		if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE && safe.Cause == subscriptionMemberNotAdmittedCause {
			// The server admitted and then released this exact member before any
			// native side effect. The owner and siblings are unchanged, so the
			// session reports an ordinary definite failure without a fence.
			claim.State = managedNotDelivered
			if err := writeJSON(journalPath, claim); err != nil {
				return nil, release, subscription.Invalid()
			}
			if config.Logger != nil {
				config.Logger.InfoContext(ctx, "subscription_member_not_admitted", "account_id", account, "operation_id", operation)
			}
			return nil, release, &managedTakeNotDelivered{problem}
		}
		if safe.Code != domain.ResourceExhausted {
			code := domain.SafeError(problem).Code
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE && (code == domain.ServerUnavailable || code == domain.Unavailable || code == domain.Canceled || code == domain.Internal || code == domain.RecoveryRequired) {
				return nil, release, &managedExecutionUncertain{problem}
			}
			return nil, release, problem
		}
		// A definite busy refusal has no delivery or native side effect. Wait on
		// the exact selected account using the unchanged claim; unknown delivery
		// never enters this retry path.
		if !waitingLogged && config.Logger != nil {
			config.Logger.InfoContext(ctx, "subscription_lease_waiting", "account_id", account, "operation_id", operation, "action", action)
			waitingLogged = true
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, release, domain.SafeError(ctx.Err())
		case <-timer.C:
		}
		wait = min(wait*2, maxSubscriptionLeaseWait)
	}
	if response.Msg.LeaseId != string(leaseID) || response.Msg.LeaseRevision == 0 {
		clear(response.Msg.Bundle)
		return nil, release, &managedExecutionUncertain{subscription.Invalid()}
	}
	claim.LeaseRevision = response.Msg.LeaseRevision
	claim.Generation = domain.ID(response.Msg.GenerationId)
	claim.NativeProfileID = domain.ID(response.Msg.NativeProfileId)
	claim.OwnerLease = domain.ID(response.Msg.OwnerLeaseId)
	if err := writeJSON(journalPath, claim); err != nil {
		clear(response.Msg.Bundle)
		return nil, release, &managedExecutionUncertain{subscription.Invalid()}
	}
	if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		codexLeasesInFlight.Store(string(leaseID), struct{}{})
	}
	return &managedSubscriptionLease{paidCredits: config.paidCredits, recoveryCapture: config.codexRecovery && action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, logger: config.Logger, client: client, credential: credential, instance: instance, account: account, response: response.Msg, finishID: claim.Finish, journalPath: journalPath, journal: claim}, release, nil
}

func (l *managedSubscriptionLease) finish(bundle []byte, cleanup, refresh, success bool) error {
	defer clear(bundle)
	codexLeasesInFlight.Delete(l.response.LeaseId)
	l.journal.State = managedClosed
	l.journal.Cleanup = cleanup
	l.journal.Refresh = refresh
	l.journal.Success = success
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := l.client.FinishSubscription(ctx, authenticated(l.credential, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(l.finishID), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, GenerationId: l.response.GenerationId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), Bundle: bundle, CleanupConfirmed: cleanup, RefreshConfirmed: refresh, Succeeded: success}))
	if err != nil {
		return rpc.ClientError(err)
	}
	l.journal.State = managedReported
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}

// A failed operation with acknowledged completion no longer owns a lease.
// It must not interrupt unrelated accounts still running on this lane.
type managedTakeNotAdmitted struct{ error }

func (e *managedTakeNotAdmitted) Unwrap() error { return e.error }

// managedTakeNotDelivered is a definite no-delivery result for a managed title
// whose server lease was already released. It is reported as an ordinary job
// problem and never fences or interrupts the primary lane.
type managedTakeNotDelivered struct{ error }

func (e *managedTakeNotDelivered) Unwrap() error { return e.error }

type managedReportedFailure struct{ error }

func (e *managedReportedFailure) Unwrap() error { return e.error }

// Protected execution completion cannot become an ordinary reported job error:
// losing its acknowledgment must close the primary lane and fence the lease.
type managedExecutionUncertain struct{ error }

func (e *managedExecutionUncertain) Unwrap() error { return e.error }

func watchSubscriptions(ctx context.Context, config Config, credential Credential, instance domain.ID) (returned error) {
	// The stream and child operations share cancellation so uncertain delivery
	// closes the server's ownership lane and triggers lost-lease reconciliation.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client, closeRPC, err := subscriptionRPC(ctx, config, credential)
	if err != nil {
		return err
	}
	defer closeRPC()
	stream, err := client.WatchSubscription(ctx, authenticated(credential, &pb.WatchSubscriptionRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		return err
	}
	defer stream.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	active := map[domain.ID]bool{}
	recoveries := newCodexRecoveryLane()
	failures := make(chan error, 1)
	defer func() {
		cancel()
		wg.Wait()
		select {
		case err := <-failures:
			returned = err
		default:
		}
	}()
	for stream.Receive() {
		r := stream.Msg().Account
		if r == nil {
			continue
		}
		var account domain.Account
		if domain.Decode(r.DocumentJson, &account) != nil || account.Subscription == nil || account.Subscription.Pending == nil && account.Subscription.Observation == nil && !(account.SubscriptionService == domain.SubscriptionClaude && account.Subscription.RecoveryRequired && account.Subscription.Lease != nil) && !grokExecutionRecovery(account) && !codexRecoveryRow(account) {
			return subscription.Invalid()
		}
		if codexRecoveryRow(account) {
			// A fenced managed ChatGPT EXECUTE lease is reconciled by its original
			// Worker only after both sides negotiated recovery. One in-flight attempt
			// per account within the lane bound; the lane retries with backoff and
			// never blocks the other accounts.
			if !config.codexRecovery {
				continue
			}
			key, obligation := codexRecoveryKey(account), codexRecoveryObligation(account)
			if !recoveries.due(key, obligation) {
				continue
			}
			mu.Lock()
			if active[key] || len(active) >= codexRecoveryLaneBound {
				mu.Unlock()
				continue
			}
			active[key] = true
			mu.Unlock()
			accountID := domain.ID(r.Id)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { mu.Lock(); delete(active, key); mu.Unlock() }()
				result := runCodexRecovery(ctx, config, client, credential, instance, accountID, account)
				if result.disposition == recoverySettled {
					recoveries.success(key)
					return
				}
				if ctx.Err() != nil {
					return
				}
				delay := recoveries.failure(key, obligation, result.disposition == recoveryTerminal)
				logCodexRecovery(config.Logger, ctx, slog.LevelInfo, "codex_recovery_retry_scheduled", "account_id", accountID, "lease_id", key, "disposition", result.disposition, "reason", result.reason, "retry_delay", delay.String())
			}()
			continue
		}
		var op domain.SubscriptionOperation
		var observation *domain.SubscriptionObservationOperation
		if account.SubscriptionService == domain.SubscriptionClaude && account.Subscription.RecoveryRequired && account.Subscription.Lease != nil || grokExecutionRecovery(account) {
			op = domain.SubscriptionOperation{ID: account.Subscription.Lease.OperationID}
		} else if account.Subscription.Observation != nil && account.Subscription.Observation.Phase == domain.SubscriptionObservationQueued {
			observation = account.Subscription.Observation
			op = domain.SubscriptionOperation{ID: observation.ID, Action: observation.Action, MachineID: observation.MachineID, Actor: observation.Actor}
		} else if account.Subscription.Pending != nil {
			op = *account.Subscription.Pending
		} else {
			continue
		}
		mu.Lock()
		if active[op.ID] || len(active) >= 4 {
			mu.Unlock()
			continue
		}
		active[op.ID] = true
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { mu.Lock(); delete(active, op.ID); mu.Unlock() }()
			run := func() error {
				if observation != nil && account.Subscription.Lease != nil {
					handled, err := config.observations.run(ctx, domain.ID(r.Id), *observation)
					if !handled {
						return nil
					}
					return err
				}
				if grokExecutionRecovery(account) {
					return runGrokRecovery(ctx, config, client, credential, domain.ID(r.Id), account)
				}
				if account.SubscriptionService == domain.SubscriptionClaude {
					if account.Subscription.RecoveryRequired {
						if account.Subscription.ManagedClaude() {
							return runManagedClaudeRecovery(ctx, config, client, credential, domain.ID(r.Id), account)
						}
						return runClaudeRecovery(ctx, config, client, credential, domain.ID(r.Id), account)
					}
					return runClaudeAccount(ctx, config, client, credential, instance, domain.ID(r.Id), r.Revision, op)
				}
				return runManagedAccount(ctx, config, client, credential, instance, domain.ID(r.Id), r.Revision, op)
			}
			if err := run(); err != nil {
				var refused *managedTakeNotAdmitted
				if errors.As(err, &refused) {
					if config.Logger != nil {
						config.Logger.InfoContext(ctx, "subscription_take_not_admitted", "account_id", r.Id, "operation_id", op.ID, "action", op.Action)
					}
					return
				}
				if config.Logger != nil {
					config.Logger.WarnContext(ctx, "managed_subscription_requires_reconciliation", "account_id", r.Id, "operation_id", op.ID, "action", op.Action, "code", domain.SafeError(err).Code)
				}
				var reported *managedReportedFailure
				if !errors.As(err, &reported) {
					select {
					case failures <- err:
					default:
					}
					cancel()
				}
			}
		}()
	}
	return stream.Err()
}

// grokExecutionRecovery reports whether the account holds a fenced Grok execute
// lease whose private home its original Worker still has to retire.
func grokExecutionRecovery(account domain.Account) bool {
	state := account.Subscription
	return account.Type == domain.SubscriptionAccount && account.SubscriptionService == domain.SubscriptionGrok && state != nil && state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute
}

func runManagedAccount(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account domain.ID, revision uint64, op domain.SubscriptionOperation) (returned error) {
	action := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	if op.Action == domain.SubscriptionRefresh {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	}
	if op.Action == domain.SubscriptionQuota {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA
	}
	if op.Action == domain.SubscriptionResetCredit {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT
	}
	if op.Action == domain.SubscriptionLogout {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	}
	deadline := 15 * time.Minute
	if op.Action == domain.SubscriptionQuota || op.Action == domain.SubscriptionResetCredit {
		deadline = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	lease, err := takeManagedSubscription(ctx, config, client, credential, instance, account, op.ID, revision, action)
	if err != nil {
		return err
	}
	defer clear(lease.response.Bundle)
	var latest []byte
	cleanup, refresh, success := false, false, false
	defer func() {
		if err := lease.finish(latest, cleanup, refresh, success); err != nil {
			returned = err
		} else if returned != nil {
			returned = &managedReportedFailure{returned}
		}
		clear(latest)
	}()
	var installation domain.Installation
	if domain.Decode(lease.response.InstallationJson, &installation) != nil || installation.Harness != domain.Codex || !domain.CodexVersionAllowed(installation.Version) {
		return subscription.Invalid()
	}
	executable, err := filepath.EvalSymlinks(installation.ResolvedPath)
	if err != nil || executable != installation.ResolvedPath || !filepath.IsAbs(executable) {
		return subscription.Invalid()
	}
	profiles, err := nativeProfiles(config, credential.MachineID)
	if err != nil {
		return err
	}
	profile, err := profiles.Acquire(ctx, nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeAuth, Account: account})
	if err != nil {
		return err
	}
	// The permanent account profile is retained after every outcome; only the
	// transient credential file is retired once the native tree is joined.
	defer profile.Close()
	home := profile.Root
	// A surviving credential file from an earlier operation blocks with a
	// specific cause until its reconciliation retires it.
	if present, presentErr := subscription.CredentialFilePresent(home); presentErr != nil || present {
		nativeprofile.Log(config.Logger, "native_profile_blocked", profile.Key, profile.ID, nativeprofile.ReasonCredentialPresent, "operation_id", op.ID)
		return nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "A transient credential file remains in the retained native profile.", "Reconcile the original operation before retrying.")
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return domain.SafeError(err)
	}
	nativeHome := filepath.Join(home, "codex")
	if len(lease.response.Bundle) > 0 {
		if _, _, err := subscription.Parse(lease.response.Bundle); err != nil {
			return err
		}
		if err := subscription.WriteCredentialFile(filepath.Join(nativeHome, "auth.json"), lease.response.Bundle); err != nil {
			return subscription.Invalid()
		}
	}
	native, nativeErr := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: nativeHome, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.ID(lease.response.LeaseId), Executable: executable, Cwd: home, Env: env, Logger: config.Logger}})
	if nativeErr != nil {
		// Open reports recovery-required if its independently joined cleanup
		// could not be proved. Never release that lease on an optimistic unlink.
		if domain.SafeError(nativeErr).Code != domain.RecoveryRequired {
			cleanup = retireManagedCredentials(config, profile, op.ID) == nil
		}
		return nativeErr
	}
	defer func() {
		if err := native.Close(); err != nil {
			returned = domain.SafeError(err)
			cleanup = false
			return
		}
		if success && op.Action != domain.SubscriptionLogout {
			closed, err := security.ReadPrivate(filepath.Join(nativeHome, "auth.json"), subscription.MaxBundle)
			if err != nil || !bytes.Equal(closed, latest) {
				clear(closed)
				returned = subscription.Invalid()
				success = false
			} else {
				clear(closed)
			}
		}
		cleanup = retireManagedCredentials(config, profile, op.ID) == nil
		if !cleanup {
			returned = subscription.Invalid()
		}
	}()
	var observationErr error
	if op.Action == domain.SubscriptionQuota || op.Action == domain.SubscriptionResetCredit {
		// Observation/publication uncertainty belongs to its original operation
		// key. Still collect the unchanged credential bundle and join native/file
		// cleanup before releasing this short idle credential lease.
		observationErr = lease.observe(ctx, native, domain.SubscriptionObservationOperation{ID: op.ID, Action: op.Action})
	}
	if op.Action == domain.SubscriptionLogin {
		progress, err := native.StartManagedLogin(ctx, op.DeviceCode)
		if err != nil {
			return err
		}
		loginCtx, stopLogin := context.WithCancel(ctx)
		pollDone := make(chan struct{})
		go func() {
			defer close(pollDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				report, err := client.PublishSubscriptionProgress(loginCtx, authenticated(credential, &pb.PublishSubscriptionProgressRequest{AccountId: string(account), LeaseId: lease.response.LeaseId, MachineId: string(credential.MachineID), InstanceId: string(instance), Url: progress.URL, UserCode: progress.UserCode}))
				if err != nil || report.Msg.Canceled {
					stopLogin()
					return
				}
				select {
				case <-loginCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		err = native.WaitManagedLogin(loginCtx, progress.LoginID)
		stopLogin()
		<-pollDone
		if err != nil {
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			cancelErr := native.CancelManagedLogin(bounded, progress.LoginID)
			stop()
			if cancelErr != nil {
				return subscription.Invalid()
			}
			return err
		}
	}
	if op.Action == domain.SubscriptionLogout {
		if err := native.LogoutManaged(ctx); err != nil {
			return err
		}
		success = true
		return nil
	}
	latest, err = native.ManagedBundle(ctx, op.Action == domain.SubscriptionRefresh)
	if err != nil {
		return err
	}
	refresh = op.Action == domain.SubscriptionRefresh
	success = true
	return observationErr
}

// retireManagedCredentials removes only the transient auth file of a retained
// profile after the native tree was joined. History, caches and native-created
// helper files stay so ordinary retained data never blocks a later operation.
func retireManagedCredentials(config Config, profile *nativeprofile.Lease, operation domain.ID) error {
	err := subscription.RetireCredentialFiles(profile.Root)
	reason := nativeprofile.ReasonCredentialCleaned
	if err != nil {
		reason = nativeprofile.ReasonCredentialBlocked
	}
	nativeprofile.Log(config.Logger, "native_profile_credential_cleanup", profile.Key, profile.ID, reason, "operation_id", operation)
	return err
}

// Only the caller's pre-native barrier permits an unpublished auth destination.
// A failed atomic write still needs a durable absence and the complete retained
// file scan: a leftover temporary credential file cannot release the lease.
func cleanupUnusedExecutionAuthentication(ctx context.Context, home string, original []byte) error {
	if err := security.CheckPrivateDir(home); err != nil {
		return subscription.Invalid()
	}
	path := filepath.Join(home, "auth.json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return cleanupExecutionAuthentication(ctx, home, original, original)
	}
	if err := security.SyncParent(path); err != nil {
		return subscription.Invalid()
	}
	if err := scanExecutionAuthentication(ctx, home, original, original); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return subscription.Invalid()
	}
	return nil
}

// cleanupPreNativeAuthentication runs the independent unused-original
// authentication cleanup for a failed native startup. A joined original tree
// authorizes that cleanup even when execution-helper retirement failed, but
// unretired helpers keep the credential lease unconfirmed.
func cleanupPreNativeAuthentication(ctx context.Context, home string, original []byte, helperRetained bool) (confirmed bool, err error) {
	err = cleanupUnusedExecutionAuthentication(ctx, home, original)
	return err == nil && !helperRetained, err
}

type executionAuthenticationStage string
type executionAuthenticationReason string

const (
	executionAuthFile       executionAuthenticationStage  = "auth-file"
	executionAuthScan       executionAuthenticationStage  = "retained-credential-scan"
	executionAuthVerified   executionAuthenticationReason = "verified"
	executionAuthFilesystem executionAuthenticationReason = "filesystem-failure"
	executionAuthBundle     executionAuthenticationReason = "bundle-mismatch"
	executionAuthIdentity   executionAuthenticationReason = "identity-changed"
	executionAuthPending    executionAuthenticationReason = "pending-file"
	executionAuthSpecial    executionAuthenticationReason = "non-regular-file"
	executionAuthCanceled   executionAuthenticationReason = "canceled"
	executionAuthCapture    executionAuthenticationReason = "capture-unsynchronized"
	executionAuthRemnant    executionAuthenticationReason = "credential-remnant"
)

type executionAuthenticationFailure struct {
	stage  executionAuthenticationStage
	reason executionAuthenticationReason
	count  int
	bytes  int64
}

func (e *executionAuthenticationFailure) Error() string {
	return "Native authentication cleanup could not be confirmed."
}
func (e *executionAuthenticationFailure) Unwrap() error { return subscription.Invalid() }

func logExecutionAuthenticationCleanup(logger *slog.Logger, owner domain.ID, err error) {
	if logger == nil {
		return
	}
	if err == nil {
		logger.Info("execution_authentication_cleanup", "owner_id", owner, "stage", executionAuthScan, "reason", executionAuthVerified)
		return
	}
	failure := &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	var observed *executionAuthenticationFailure
	if errors.As(err, &observed) {
		failure = observed
	}
	logger.Warn("execution_authentication_cleanup", "owner_id", owner, "stage", failure.stage, "reason", failure.reason, "file_count", failure.count, "observed_bytes", failure.bytes)
}

// cleanupExecutionAuthentication removes the transient authentication file
// after comparing it with the latest validated bundle and then proves, with a
// complete streaming inspection of the retained native history, that no
// credential remnant survives. The inspection observes ctx between files and
// chunks.
func cleanupExecutionAuthentication(ctx context.Context, home string, latest, original []byte) error {
	if err := security.CheckPrivateDir(home); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	closed, err := security.ReadPrivate(filepath.Join(home, "auth.json"), subscription.MaxBundle)
	if err != nil {
		clear(closed)
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	if len(latest) == 0 || !bytes.Equal(closed, latest) {
		clear(closed)
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthBundle}
	}
	clear(closed)
	if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	if err := security.SyncParent(filepath.Join(home, "auth.json")); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	return scanRetainedAuthentication(ctx, home, authenticationNeedles(latest, original))
}

func scanExecutionAuthentication(ctx context.Context, home string, latest, original []byte) error {
	return scanRetainedAuthentication(ctx, home, authenticationNeedles(latest, original))
}

// authenticationNeedles returns the raw and Base64 encodings of every token of
// the supplied bundles. A nil result for an unparsable bundle is reported by
// the caller as a bundle mismatch.
func authenticationNeedles(bundles ...[]byte) [][]byte {
	var needles [][]byte
	for _, raw := range bundles {
		b, _, err := subscription.Parse(raw)
		if err != nil {
			clear2D(needles)
			return nil
		}
		for _, v := range []string{b.Tokens.ID, b.Tokens.Access, b.Tokens.Refresh} {
			plain := []byte(v)
			needles = append(needles, plain)
			// Retained native files may encode credentials. Cleanup must reject
			// padded and unpadded standard/URL Base64 copies as well as raw tokens.
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				encoded := make([]byte, encoding.EncodedLen(len(plain)))
				encoding.Encode(encoded, plain)
				needles = append(needles, encoded)
			}
		}
	}
	return needles
}

func clear2D(values [][]byte) {
	for _, v := range values {
		clear(v)
	}
}

// scanRetainedAuthentication inspects every regular file of the retained native
// history without a file-count, file-size or aggregate-size limit. Memory stays
// bounded because each file is streamed through a fixed window that overlaps by
// the longest needle, so a token split across chunk boundaries is still found.
// Pending entries, special files, unreadable entries, a credential remnant and a
// changed root identity all fail closed and never delete history. Native-created
// symbolic links are ordinary retained data: they are never followed or read.
func scanRetainedAuthentication(ctx context.Context, home string, needles [][]byte) error {
	return scanRetainedAuthenticationSkipping(ctx, home, needles, "")
}

// scanRetainedAuthenticationSkipping is scanRetainedAuthentication that leaves
// one regular file, named relative to the home, out of the inspection. Recovery
// uses it to prove the rest of the retained history clean while the only copy of
// a rejected legacy credential file stays in place until the scan has passed.
func scanRetainedAuthenticationSkipping(ctx context.Context, home string, needles [][]byte, skip string) error {
	count, total := 0, int64(0)
	fail := func(reason executionAuthenticationReason) error {
		return &executionAuthenticationFailure{stage: executionAuthScan, reason: reason, count: count, bytes: total}
	}
	defer clear2D(needles)
	if len(needles) == 0 {
		return fail(executionAuthBundle)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fail(executionAuthFilesystem)
	}
	defer root.Close()
	originalInfo, err := root.Stat(".")
	if err != nil {
		return fail(executionAuthFilesystem)
	}
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fail(executionAuthFilesystem)
		}
		if err := ctx.Err(); err != nil {
			return fail(executionAuthCanceled)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		// WriteAtomic owns this temporary namespace. A failed write/removal can
		// retain only a token prefix or no bytes, so token matching cannot prove
		// cleanup while any such entry remains in the owned runtime.
		if strings.HasPrefix(entry.Name(), ".pending-") {
			return fail(executionAuthPending)
		}
		if entry.IsDir() {
			return nil
		}
		if skip != "" && path == skip {
			return nil
		}
		count++
		info, err := entry.Info()
		if err != nil {
			return fail(executionAuthFilesystem)
		}
		if !info.Mode().IsRegular() {
			return fail(executionAuthSpecial)
		}
		total += info.Size()
		file, err := root.Open(path)
		if err != nil {
			return fail(executionAuthFilesystem)
		}
		found, scanErr := scanAuthenticationStream(ctx, file, needles)
		closeErr := file.Close()
		if errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
			return fail(executionAuthCanceled)
		}
		if scanErr != nil || closeErr != nil {
			return fail(executionAuthFilesystem)
		}
		if found {
			return fail(executionAuthRemnant)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if scanIdentityCheckpoint != nil {
		scanIdentityCheckpoint()
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(originalInfo, current) {
		return fail(executionAuthIdentity)
	}
	return nil
}

// scanIdentityCheckpoint is a test seam that runs after the walk and before the
// final root identity comparison.
var scanIdentityCheckpoint func()

func scanAuthenticationStream(ctx context.Context, file *os.File, needles [][]byte) (bool, error) {
	maxNeedle := 0
	for _, needle := range needles {
		maxNeedle = max(maxNeedle, len(needle))
	}
	const chunkSize = 64 << 10
	buffer := make([]byte, chunkSize+maxNeedle)
	defer clear(buffer)
	overlap := 0
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, err := file.Read(buffer[overlap : overlap+chunkSize])
		window := buffer[:overlap+n]
		for _, needle := range needles {
			if bytes.Contains(window, needle) {
				return true, nil
			}
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, io.ErrNoProgress
		}
		overlap = min(len(window), max(0, maxNeedle-1))
		copy(buffer[:overlap], window[len(window)-overlap:])
	}
}

func managedSubscriptionCapability(resource *pb.Resource) bool {
	if resource == nil {
		return false
	}
	var m domain.Machine
	return domain.Decode(resource.DocumentJson, &m) == nil && m.Validate() == nil && slicesContainManagedCapability(m.WorkerCapabilities)
}

func slicesContainManagedCapability(values []domain.WorkerCapability) bool {
	for _, v := range values {
		if v == domain.ManagedCodexSubscriptionsV1 || v == domain.NativeClaudeSubscriptionsV1 || v == domain.ManagedClaudeExecutionV1 || v == domain.ManagedGrokSubscriptionsV1 {
			return true
		}
	}
	return false
}

// Capability probing is a read-only empty-home handshake. It neither starts a
// login nor reads host credentials and does not establish real-account support.
func verifyManagedSubscriptionProfile(ctx context.Context, config Config, machine domain.ID, executable, version string) (bool, error) {
	profiles, err := nativeProfiles(config, machine)
	if err != nil {
		return false, domain.SafeError(err)
	}
	lease, err := profiles.AcquireWait(ctx, nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeDiscovery}, 30*time.Second)
	if err != nil {
		return false, domain.SafeError(err)
	}
	defer lease.Close()
	home := lease.Root
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return false, domain.SafeError(err)
	}
	client, err := codex.Open(ctx, codex.Config{Version: version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: filepath.Join(home, "codex"), Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.NewID(), Executable: executable, Cwd: home, Env: env, Logger: config.Logger}})
	if err != nil {
		return false, err
	}
	if err := client.Close(); err != nil {
		return false, domain.SafeError(err)
	}
	// The probe profile never receives credentials; a surviving auth file would
	// contradict that and keeps the probe unsupported.
	if present, _ := subscription.CredentialFilePresent(home); present {
		return false, subscription.Invalid()
	}
	return true, nil
}
