// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// The Worker side of the ChatGPT (managed Codex) Execute recovery lane
// (issue 3992). A negotiated Worker
//
//   - records the retained native home in the lease's durable journal before it
//     writes the transient auth.json,
//   - synchronizes a protected server-vault capture of the latest validated bundle
//     before it deletes that file, and
//   - automatically reconciles a fenced Execute lease of its own device: it joins
//     the original process owners, scans the retained history completely for the
//     latest tokens and settles one durable recovery request with the server.
//
// Secret bytes live only in memory and in the server vault. Journals, logs and
// ordinary RPCs carry opaque identifiers only.

var errRecoveryCaptureUnsupported = errors.New("recovery capture unsupported")

// writeExecutionAuthentication binds the retained native home to the lease's
// durable journal and then writes the transient auth.json. The binding precedes
// the plaintext file, so a crash can never leave an unrecorded credential.
func (l *managedSubscriptionLease) writeExecutionAuthentication(home string) error {
	if l.recoveryCapture {
		l.journal.NativeHome = home
		if err := writeJSON(l.journalPath, l.journal); err != nil {
			return subscription.Invalid()
		}
	}
	return subscription.WriteCredentialFile(filepath.Join(home, "auth.json"), l.response.Bundle)
}

// syncRecoveryCapture stores the latest validated bundle in the server vault. The
// bundle is not retained, journaled or logged here.
func (l *managedSubscriptionLease) syncRecoveryCapture(latest []byte) error {
	id := domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := l.client.SyncSubscriptionRecoveryCapture(ctx, authenticated(l.credential, &pb.SyncSubscriptionRecoveryCaptureRequest{
		Mutation: &pb.Mutation{RequestId: string(id), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision},
		LeaseId:  l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance),
		GenerationId: l.response.GenerationId, Bundle: latest,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return errRecoveryCaptureUnsupported
		}
		problem := rpc.ClientError(err)
		if domain.SafeError(problem).Code == domain.Unsupported {
			return errRecoveryCaptureUnsupported
		}
		return problem
	}
	l.journal.CaptureRequest, l.journal.CaptureConfirmed = id, true
	if err := writeJSON(l.journalPath, l.journal); err != nil && l.logger != nil {
		l.logger.Warn("codex_recovery_capture_journal_unrecorded", "account_id", l.account, "lease_id", l.response.LeaseId, "capture_id", id)
	}
	return nil
}

// cleanupCapturedExecutionAuthentication removes the transient auth.json after
// the latest validated bundle was captured in the server vault. A lease without
// the negotiated lane, or a server that does not support it, keeps the historical
// cleanup. A capture that cannot be confirmed leaves auth.json in place and fails
// the cleanup, so the retained file stays the only proof until recovery. The
// lease's transport must be open; a shared owner whose member transports were
// closed wraps the call in withFreshClient.
func cleanupCapturedExecutionAuthentication(config Config, lease *managedSubscriptionLease, home string, latest, original []byte) error {
	if err := captureBeforeCleanup(config, lease, latest); err != nil {
		return err
	}
	return cleanupExecutionAuthentication(home, latest, original)
}

// cleanupCapturedPreNativeAuthentication is the failed-startup counterpart: the
// unused original bundle is captured before its transient file is deleted, so a
// cleanup that cannot be confirmed stays recoverable.
func cleanupCapturedPreNativeAuthentication(config Config, lease *managedSubscriptionLease, home string, original []byte, helperRetained bool) (bool, error) {
	if lease != nil && lease.recoveryCapture {
		if _, err := os.Lstat(filepath.Join(home, "auth.json")); err == nil {
			if err := captureBeforeCleanup(config, lease, original); err != nil {
				return false, err
			}
		}
	}
	return cleanupPreNativeAuthentication(home, original, helperRetained)
}

// captureBeforeCleanup synchronizes the protected capture of a negotiated lease.
// An unsupported server keeps the historical cleanup; any other failure fails
// the cleanup and leaves auth.json untouched.
func captureBeforeCleanup(config Config, lease *managedSubscriptionLease, latest []byte) error {
	if lease == nil || !lease.recoveryCapture || len(latest) == 0 {
		return nil
	}
	err := lease.syncRecoveryCapture(latest)
	switch {
	case err == nil:
		if config.Logger != nil {
			config.Logger.Info("codex_recovery_capture_synced", "account_id", lease.account, "lease_id", lease.response.LeaseId, "capture_id", lease.journal.CaptureRequest)
		}
		return nil
	case errors.Is(err, errRecoveryCaptureUnsupported):
		if config.Logger != nil {
			config.Logger.Info("codex_recovery_capture_unsupported", "account_id", lease.account, "lease_id", lease.response.LeaseId)
		}
		return nil
	default:
		if config.Logger != nil {
			config.Logger.Warn("codex_recovery_capture_unconfirmed", "account_id", lease.account, "lease_id", lease.response.LeaseId, "code", domain.SafeError(err).Code)
		}
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthCapture}
	}
}

// codexRecoveryDelays are the exponential retry delays of one account; the last
// delay is the cap.
var codexRecoveryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}

// codexRecoverySchedule serializes and paces automatic reconciliation per
// account. A transient failure waits 1, 2, 4, 8, 16 and then 30 seconds. A
// blocked account (missing, foreign or insufficient proof) is not retried until
// its original obligation changes or the Worker reconnects, which creates a new
// schedule.
type codexRecoverySchedule struct {
	mu       sync.Mutex
	now      func() time.Time
	accounts map[domain.ID]*codexRecoveryEntry
}

type codexRecoveryEntry struct {
	key      string
	failures int
	next     time.Time
	blocked  bool
}

func newCodexRecoverySchedule(now func() time.Time) *codexRecoverySchedule {
	if now == nil {
		now = time.Now
	}
	return &codexRecoverySchedule{now: now, accounts: map[domain.ID]*codexRecoveryEntry{}}
}

// ready reports whether a reconciliation attempt may start now.
func (s *codexRecoverySchedule) ready(account domain.ID, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.accounts[account]
	if entry == nil {
		return true
	}
	if entry.key != key {
		delete(s.accounts, account)
		return true
	}
	return !entry.blocked && !s.now().Before(entry.next)
}

// settled records an acknowledged settlement. The stream may still offer the
// same fenced account until the server state is observed, so that obligation is
// not repeated for the cap delay; a changed obligation is reconsidered at once.
func (s *codexRecoverySchedule) settled(account domain.ID, key string) {
	s.mu.Lock()
	s.accounts[account] = &codexRecoveryEntry{key: key, next: s.now().Add(codexRecoveryDelays[len(codexRecoveryDelays)-1])}
	s.mu.Unlock()
}

func (s *codexRecoverySchedule) block(account domain.ID, key string) {
	s.mu.Lock()
	s.accounts[account] = &codexRecoveryEntry{key: key, blocked: true}
	s.mu.Unlock()
}

// fail records a transient failure and returns the delay before the next attempt.
func (s *codexRecoverySchedule) fail(account domain.ID, key string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.accounts[account]
	if entry == nil || entry.key != key {
		entry = &codexRecoveryEntry{key: key}
		s.accounts[account] = entry
	}
	delay := codexRecoveryDelays[min(entry.failures, len(codexRecoveryDelays)-1)]
	entry.failures++
	entry.blocked = false
	entry.next = s.now().Add(delay)
	return delay
}

// codexRecoveryKey names the original obligation. A changed lease, generation or
// retained capture is a new obligation and is reconsidered immediately.
func codexRecoveryKey(state *domain.SubscriptionState) string {
	capture := domain.ID("")
	if state.RecoveryCapture != nil {
		capture = state.RecoveryCapture.ID
	}
	return string(state.Lease.ID) + "/" + string(state.Generation) + "/" + string(capture)
}

// codexRecoveryEligible reports whether the account is a fenced ChatGPT Execute
// lease the recovery lane may reconcile.
func codexRecoveryEligible(account domain.Account) bool {
	state := account.Subscription
	return account.SubscriptionService == domain.SubscriptionChatGPT && state != nil && state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute
}

// codexRecoveryResult is the closed outcome of one attempt.
type codexRecoveryResult struct {
	// settled is true when the server acknowledged the recovery settlement.
	settled bool
	// blocked is the closed reason the proof is insufficient; the fence stays.
	blocked domain.SubscriptionRecoveryReason
	// err is a transient failure that is retried with backoff.
	err error
}

func transientRecovery(err error) codexRecoveryResult { return codexRecoveryResult{err: err} }

func blockedRecovery(reason domain.SubscriptionRecoveryReason) codexRecoveryResult {
	return codexRecoveryResult{blocked: reason}
}

// runCodexRecoveryAttempt runs one scheduled reconciliation and records its
// outcome. It never returns an error: a failed reconciliation must not close the
// subscription lane that serves unrelated accounts.
func runCodexRecoveryAttempt(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, account domain.Account, schedule *codexRecoverySchedule) {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	state := account.Subscription
	key := codexRecoveryKey(state)
	logger.InfoContext(ctx, "codex_recovery_attempt", "account_id", accountID, "lease_id", state.Lease.ID, "shared_owner", state.Lease.Shared, "members", len(state.Members))
	result := reconcileCodexExecution(ctx, config, client, credential, instance, accountID, account)
	switch {
	case result.settled:
		schedule.settled(accountID, key)
		logger.InfoContext(ctx, "codex_recovery_result", "account_id", accountID, "lease_id", state.Lease.ID, "result", "settled")
	case result.blocked != "":
		schedule.block(accountID, key)
		logger.WarnContext(ctx, "codex_recovery_result", "account_id", accountID, "lease_id", state.Lease.ID, "result", "blocked", "blocked_reason", result.blocked)
		if state.RecoveryReason != result.blocked {
			reportCodexRecoveryBlocked(ctx, config, client, credential, instance, accountID, account, result.blocked)
		}
	default:
		if ctx.Err() != nil {
			logger.InfoContext(ctx, "codex_recovery_result", "account_id", accountID, "lease_id", state.Lease.ID, "result", "canceled")
			return
		}
		delay := schedule.fail(accountID, key)
		logger.WarnContext(ctx, "codex_recovery_result", "account_id", accountID, "lease_id", state.Lease.ID, "result", "retry", "code", domain.SafeError(result.err).Code, "retry_delay_ms", delay.Milliseconds())
	}
}

// reportCodexRecoveryBlocked publishes the closed blocked reason through the
// account. It changes nothing else and is best effort; the next obligation change
// or reconnection reports again.
func reportCodexRecoveryBlocked(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, account domain.Account, reason domain.SubscriptionRecoveryReason) {
	original := account.Subscription.Lease
	wire := pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED
	switch reason {
	case domain.RecoveryCredentialProofUnavailable:
		wire = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE
	case domain.RecoveryCleanupUnconfirmed:
		wire = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED
	case domain.RecoveryOwnershipUnproven:
		wire = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_OWNERSHIP_UNPROVEN
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := client.RecoverSubscription(bounded, authenticated(credential, &pb.RecoverSubscriptionRequest{
		Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(accountID), ExpectedRevision: original.Revision},
		LeaseId:  string(original.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(original.Generation),
		Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, BlockedReason: wire,
	}))
	if err != nil && config.Logger != nil {
		config.Logger.WarnContext(ctx, "codex_recovery_blocked_report_failed", "account_id", accountID, "lease_id", original.ID, "blocked_reason", reason, "code", domain.SafeError(rpc.ClientError(err)).Code)
	}
}

// reconcileCodexExecution reconciles one fenced ChatGPT Execute lease from the
// original durable evidence. It never retakes a lease, never replays input and
// never grants authentication to a new execution.
func reconcileCodexExecution(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, account domain.Account) codexRecoveryResult {
	state := account.Subscription
	if !codexRecoveryEligible(account) || state.Lease.MachineID != credential.MachineID {
		return blockedRecovery(domain.RecoveryOwnershipUnproven)
	}
	original := state.Lease
	logger := config.Logger
	phase := func(name string, args ...any) {
		if logger != nil {
			logger.InfoContext(ctx, "codex_recovery_phase", append([]any{"account_id", accountID, "lease_id", original.ID, "phase", name}, args...)...)
		}
	}
	// Concurrent active ownership of this very process stays untouched.
	if config.codexOwners != nil {
		gate := config.codexOwners.gate(accountID)
		select {
		case gate.slot <- struct{}{}:
			defer gate.release()
		default:
			return transientRecovery(domain.Fail(domain.ResourceExhausted, "The account is in use by this Worker.", "Retry once the active Execute settles."))
		}
		gate.mu.Lock()
		active := gate.owner != nil
		gate.mu.Unlock()
		if active {
			return transientRecovery(domain.Fail(domain.ResourceExhausted, "The account owner is still active in this Worker.", "Retry once the active owner retires."))
		}
	}
	phase("validate-journal")
	path := filepath.Join(config.Root, "managed-auth", string(original.ID)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Lease != original.ID || journal.Account != accountID || journal.Operation != original.OperationID || journal.Instance != original.InstanceID || journal.Generation != original.Generation || journal.Finish.Validate() != nil || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE || journal.LeaseRevision != 0 && journal.LeaseRevision != original.Revision {
		// Missing, foreign or restored original evidence is never adopted.
		if errors.Is(err, fs.ErrNotExist) || err == nil {
			return blockedRecovery(domain.RecoveryOwnershipUnproven)
		}
		return transientRecovery(domain.SafeError(err))
	}
	if journal.NativeHome == "" {
		// A journal from before the lane never recorded where the latest
		// credential lived and the file may already be deleted. No receipt is
		// fabricated and no home is guessed.
		return blockedRecovery(domain.RecoveryCredentialProofUnavailable)
	}
	home, err := recoveryNativeHome(config, original, accountID, journal.NativeHome)
	if err != nil {
		return blockedRecovery(domain.RecoveryOwnershipUnproven)
	}
	phase("process-owners")
	if blocked, err := reconcileRecoveryProcessOwners(ctx, config, original, state.Members); blocked != "" || err != nil {
		return codexRecoveryResult{blocked: blocked, err: err}
	}
	if journal.RecoveryRequest == "" {
		journal.RecoveryRequest = domain.NewID()
		if err := writeJSON(path, journal); err != nil {
			return transientRecovery(subscription.Invalid())
		}
	}
	phase("credential-proof")
	bundle, capture, result := recoveryCandidate(ctx, config, client, credential, instance, accountID, state, &journal, path, home)
	if bundle == nil {
		return result
	}
	defer clear(bundle)
	phase("retained-history-scan", "capture_id", capture)
	if err := recoveryCleanupHome(ctx, home, bundle); err != nil {
		var failure *executionAuthenticationFailure
		if errors.As(err, &failure) {
			logExecutionAuthenticationCleanup(logger, original.ID, err)
			if failure.reason != executionAuthFilesystem && failure.reason != executionAuthCanceled {
				return blockedRecovery(domain.RecoveryCleanupUnconfirmed)
			}
		}
		return transientRecovery(err)
	}
	logExecutionAuthenticationCleanup(logger, original.ID, nil)
	phase("settle", "recovery_id", journal.RecoveryRequest)
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = client.RecoverSubscription(bounded, authenticated(credential, &pb.RecoverSubscriptionRequest{
		Mutation: &pb.Mutation{RequestId: string(journal.RecoveryRequest), Id: string(accountID), ExpectedRevision: original.Revision},
		LeaseId:  string(original.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(original.Generation),
		CaptureId: string(capture), Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED,
	}))
	if err != nil {
		problem := rpc.ClientError(err)
		switch domain.SafeError(problem).Code {
		case domain.RecoveryRequired, domain.PermissionDenied, domain.Conflict, domain.Unsupported, domain.NotFound:
			// The server refused the evidence; retrying the same proof cannot help.
			return blockedRecovery(domain.RecoveryOwnershipUnproven)
		}
		return transientRecovery(problem)
	}
	journal.Recovered = true
	if err := writeJSON(path, journal); err != nil && logger != nil {
		logger.WarnContext(ctx, "codex_recovery_journal_unrecorded", "account_id", accountID, "lease_id", original.ID)
	}
	settleRecoveredMemberJournals(config, state.Members)
	if config.codexOwners != nil {
		config.codexOwners.gate(accountID).clearFence()
	}
	return codexRecoveryResult{settled: true}
}

// recoveryNativeHome validates the journaled retained home: it must be the
// canonical private directory inside this Worker's root, and a shared owner's
// home must be the account history home.
func recoveryNativeHome(config Config, original *domain.SubscriptionLease, account domain.ID, recorded string) (string, error) {
	if !filepath.IsAbs(recorded) || filepath.Clean(recorded) != recorded {
		return "", subscription.Invalid()
	}
	rel, err := filepath.Rel(config.Root, recorded)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", subscription.Invalid()
	}
	if original.Shared {
		expected, err := codexAccountHistoryHome(config.Root, account)
		if err != nil || expected != recorded {
			return "", subscription.Invalid()
		}
	}
	if resolved, err := filepath.EvalSymlinks(recorded); err != nil || resolved != recorded {
		return "", subscription.Invalid()
	}
	if err := security.CheckPrivateDir(recorded); err != nil {
		return "", subscription.Invalid()
	}
	return recorded, nil
}

// reconcileRecoveryProcessOwners joins the original process owners through the
// process owner index, never by PID guesses. The native owner of the lease must
// have its index: a missing index is never proof of completion.
func reconcileRecoveryProcessOwners(ctx context.Context, config Config, original *domain.SubscriptionLease, members []domain.SubscriptionMember) (domain.SubscriptionRecoveryReason, error) {
	root := filepath.Join(config.Root, "processes")
	nativeOwner := original.OperationID
	if original.Shared {
		nativeOwner = original.ID
	}
	owners := []domain.ID{nativeOwner}
	if original.OperationID != nativeOwner {
		owners = append(owners, original.OperationID)
	}
	for _, member := range members {
		owners = append(owners, member.OperationID)
	}
	seen := map[domain.ID]bool{}
	for _, owner := range owners {
		if seen[owner] {
			continue
		}
		seen[owner] = true
		if _, err := os.Lstat(filepath.Join(root, string(owner))); errors.Is(err, fs.ErrNotExist) {
			if owner == nativeOwner {
				return domain.RecoveryOwnershipUnproven, nil
			}
			continue
		}
		if err := process.ReconcileOwnerContext(ctx, root, owner); err != nil {
			if ctx.Err() != nil {
				return "", domain.SafeError(ctx.Err())
			}
			return domain.RecoveryCleanupUnconfirmed, nil
		}
	}
	return "", nil
}

// recoveryCandidate resolves the latest validated bundle: the retained server
// capture, or, for a lease that never synchronized one, the surviving transient
// file, which is captured first so the proof is durable before it is deleted.
// A nil bundle means the result explains why recovery cannot continue.
func recoveryCandidate(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, state *domain.SubscriptionState, journal *managedSubscriptionJournal, path, home string) ([]byte, domain.ID, codexRecoveryResult) {
	original := state.Lease
	authPath := filepath.Join(home, "auth.json")
	file, readErr := security.ReadPrivate(authPath, subscription.MaxBundle)
	present := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return nil, "", transientRecovery(domain.SafeError(readErr))
	}
	defer clear(file)
	if c := state.RecoveryCapture; c != nil {
		if c.LeaseID != original.ID || c.Generation != original.Generation {
			return nil, "", blockedRecovery(domain.RecoveryOwnershipUnproven)
		}
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		response, err := client.ReadSubscriptionRecoveryCapture(bounded, authenticated(credential, &pb.ReadSubscriptionRecoveryCaptureRequest{AccountId: string(accountID), LeaseId: string(original.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), CaptureId: string(c.ID)}))
		if err != nil {
			return nil, "", recoveryRPCFailure(err)
		}
		bundle := bytes.Clone(response.Msg.Bundle)
		clear(response.Msg.Bundle)
		if _, _, err := subscription.Parse(bundle); err != nil {
			clear(bundle)
			return nil, "", blockedRecovery(domain.RecoveryCredentialProofUnavailable)
		}
		if present && !bytes.Equal(file, bundle) {
			// A file that is not the captured bundle is uncertain filesystem
			// evidence; it is neither deleted nor trusted.
			clear(bundle)
			return nil, "", blockedRecovery(domain.RecoveryCleanupUnconfirmed)
		}
		return bundle, c.ID, codexRecoveryResult{}
	}
	if !present {
		// The latest credential was deleted before any capture could preserve it.
		return nil, "", blockedRecovery(domain.RecoveryCredentialProofUnavailable)
	}
	if _, _, err := subscription.Parse(file); err != nil {
		return nil, "", blockedRecovery(domain.RecoveryCredentialProofUnavailable)
	}
	if journal.CaptureRequest == "" {
		journal.CaptureRequest = domain.NewID()
		if err := writeJSON(path, *journal); err != nil {
			return nil, "", transientRecovery(subscription.Invalid())
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := client.SyncSubscriptionRecoveryCapture(bounded, authenticated(credential, &pb.SyncSubscriptionRecoveryCaptureRequest{
		Mutation: &pb.Mutation{RequestId: string(journal.CaptureRequest), Id: string(accountID), ExpectedRevision: original.Revision},
		LeaseId:  string(original.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(original.Generation), Bundle: bytes.Clone(file),
	}))
	if err != nil {
		return nil, "", recoveryRPCFailure(err)
	}
	return bytes.Clone(file), journal.CaptureRequest, codexRecoveryResult{}
}

// recoveryRPCFailure classifies a server refusal: definite refusals of the
// presented evidence are blocked, transport and server availability retry.
func recoveryRPCFailure(err error) codexRecoveryResult {
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		return blockedRecovery(domain.RecoveryOwnershipUnproven)
	}
	problem := rpc.ClientError(err)
	switch domain.SafeError(problem).Code {
	case domain.RecoveryRequired, domain.PermissionDenied, domain.Conflict, domain.Unsupported, domain.NotFound:
		return blockedRecovery(domain.RecoveryOwnershipUnproven)
	}
	return transientRecovery(problem)
}

// recoveryCleanupHome removes a surviving transient auth.json that equals the
// captured bundle and scans the retained history completely for the latest
// tokens. The original generation is superseded by the latest bundle, so its
// tokens are not needles unless the bundle never rotated. History, sessions,
// caches and helper links are never deleted.
func recoveryCleanupHome(ctx context.Context, home string, bundle []byte) error {
	if _, err := os.Lstat(filepath.Join(home, "auth.json")); err == nil {
		return cleanupExecutionAuthenticationContext(ctx, home, bundle, bundle)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	if err := security.CheckPrivateDir(home); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	if err := security.SyncParent(filepath.Join(home, "auth.json")); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	}
	return scanExecutionAuthenticationContext(ctx, home, bundle, bundle)
}

// settleRecoveredMemberJournals marks every member's local claim reported after
// the server released the whole owner/member set. Best effort bookkeeping.
func settleRecoveredMemberJournals(config Config, members []domain.SubscriptionMember) {
	for _, member := range members {
		path := filepath.Join(config.Root, "managed-auth", string(member.LeaseID)+".json")
		raw, err := security.ReadPrivate(path, 16<<10)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil || journal.Lease != member.LeaseID {
			continue
		}
		journal.State, journal.Cleanup, journal.Success, journal.Recovered = managedReported, true, false, true
		if err := writeJSON(path, journal); err != nil && config.Logger != nil {
			config.Logger.Warn("codex_recovery_member_journal_unsettled", "lease_id", member.LeaseID)
		}
	}
}
