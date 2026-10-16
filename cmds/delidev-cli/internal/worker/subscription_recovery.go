package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Automatic recovery of a retained managed ChatGPT Execute lease.
//
// An original execution first synchronizes a protected capture of the latest
// validated bundle with the server, then deletes transient authentication. If
// cleanup cannot be confirmed, the server fences the lease. The original Worker
// reconciles it through the recovery lane below, using only its own journal,
// the server-held capture and independent process and filesystem evidence.

// recoveryBackoff is the retry delay sequence for transient failures: 1, 2, 4,
// 8 and 16 seconds, then 30 seconds.
func recoveryBackoff(attempt int) time.Duration {
	delay := time.Second << min(attempt, 5)
	return min(delay, 30*time.Second)
}

const (
	recoveryRPCTimeout   = 30 * time.Second
	recoveryProcessLimit = 15 * time.Second
)

// recoveryBlocked is proof that is absent or foreign. It is not retried until
// the original Worker reconnects or the original obligation changes.
type recoveryBlocked struct {
	reason domain.SubscriptionRecoveryReason
}

func (e *recoveryBlocked) Error() string {
	return "Managed account recovery is blocked: " + string(e.reason)
}

func blockedRecovery(reason domain.SubscriptionRecoveryReason) error { return &recoveryBlocked{reason} }

// recoveryTracker serializes retry timing per account for one Watch session.
type recoveryTracker struct {
	mu       sync.Mutex
	accounts map[domain.ID]*recoveryTrack
}

type recoveryTrack struct {
	attempt  int
	next     time.Time
	blocked  bool
	key      string
	reported domain.SubscriptionRecoveryReason
}

func newRecoveryTracker() *recoveryTracker {
	return &recoveryTracker{accounts: map[domain.ID]*recoveryTrack{}}
}

func recoveryObligationKey(lease *domain.SubscriptionLease) string {
	return string(lease.ID) + ":" + string(lease.Generation) + ":" + string(lease.Epoch)
}

// ready reports whether the account may be attempted now. A blocked account
// stays quiet until its original obligation changes.
func (t *recoveryTracker) ready(account domain.ID, lease *domain.SubscriptionLease, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	track := t.accounts[account]
	if track == nil {
		return true
	}
	if track.key != recoveryObligationKey(lease) {
		delete(t.accounts, account)
		return true
	}
	return !track.blocked && !now.Before(track.next)
}

func (t *recoveryTracker) record(account domain.ID, lease *domain.SubscriptionLease, err error, now time.Time) (delay time.Duration, blocked *recoveryBlocked) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		delete(t.accounts, account)
		return 0, nil
	}
	track := t.accounts[account]
	if track == nil || track.key != recoveryObligationKey(lease) {
		track = &recoveryTrack{key: recoveryObligationKey(lease)}
		t.accounts[account] = track
	}
	if errors.As(err, &blocked) {
		track.blocked = true
		return 0, blocked
	}
	delay = recoveryBackoff(track.attempt)
	track.attempt++
	track.next = now.Add(delay)
	return delay, nil
}

// markReported returns false when the same reason was already published.
func (t *recoveryTracker) markReported(account domain.ID, lease *domain.SubscriptionLease, reason domain.SubscriptionRecoveryReason) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	track := t.accounts[account]
	if track == nil || track.key != recoveryObligationKey(lease) {
		track = &recoveryTrack{key: recoveryObligationKey(lease)}
		t.accounts[account] = track
	}
	if track.reported == reason {
		return false
	}
	track.reported = reason
	return true
}

// recoveryEligible reports whether the Watch record is a fenced original
// ChatGPT Execute lease this Worker may reconcile.
func recoveryEligible(account domain.Account, machine domain.ID) bool {
	state := account.Subscription
	return account.SubscriptionService == domain.SubscriptionChatGPT && state != nil && state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute && state.Lease.MachineID == machine
}

const (
	executionAuthCapture             executionAuthenticationStage  = "recovery-capture"
	executionAuthEvidenceStage       executionAuthenticationStage  = "recovery-evidence"
	executionAuthCaptureUnavailable  executionAuthenticationReason = "capture-unavailable"
	executionAuthEvidenceUnavailable executionAuthenticationReason = "evidence-unavailable"
)

// recordCleanupEvidence persists the exact native home and process scope the
// authentication cleanup acts on. Recovery uses it instead of path guesses.
func (l *managedSubscriptionLease) recordCleanupEvidence(home string, processOwner domain.ID) error {
	if l == nil || l.journalPath == "" || !l.recovery {
		return nil
	}
	if l.journal.NativeHome == home && l.journal.ProcessOwner == processOwner {
		return nil
	}
	l.journal.NativeHome, l.journal.ProcessOwner = home, processOwner
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthEvidenceStage, reason: executionAuthEvidenceUnavailable}
	}
	return nil
}

// syncRecoveryCapture protects the latest validated bundle in the server vault
// before transient authentication is deleted. A failure leaves the credential
// file in place, so the lease stays unconfirmed and recoverable.
func (l *managedSubscriptionLease) syncRecoveryCapture(latest []byte) error {
	if l == nil || !l.recovery || l.client == nil || len(latest) == 0 {
		return nil
	}
	digest := subscription.Digest(latest)
	if l.journal.CaptureID == "" || l.journal.CaptureDigest != digest {
		l.journal.CaptureID, l.journal.CaptureDigest, l.journal.CaptureSynced = domain.NewID(), digest, false
		if err := writeJSON(l.journalPath, l.journal); err != nil {
			return &executionAuthenticationFailure{stage: executionAuthCapture, reason: executionAuthCaptureUnavailable}
		}
	}
	if l.journal.CaptureSynced {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), recoveryRPCTimeout)
	defer cancel()
	request := &pb.SyncSubscriptionRecoveryCaptureRequest{Mutation: &pb.Mutation{RequestId: string(l.journal.CaptureID), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), GenerationId: l.response.GenerationId, Bundle: bytes.Clone(latest)}
	_, err := l.client.SyncSubscriptionRecoveryCapture(ctx, authenticated(l.credential, request))
	clear(request.Bundle)
	if err != nil {
		if l.logger != nil {
			l.logger.Warn("subscription_recovery_capture_failed", "account_id", l.account, "lease_id", l.response.LeaseId, "code", domain.SafeError(rpc.ClientError(err)).Code)
		}
		return &executionAuthenticationFailure{stage: executionAuthCapture, reason: executionAuthCaptureUnavailable}
	}
	l.journal.CaptureSynced = true
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return &executionAuthenticationFailure{stage: executionAuthCapture, reason: executionAuthCaptureUnavailable}
	}
	return nil
}

// beginAuthenticationCleanup records the cleanup evidence and protects the
// latest bundle. It always runs before a transient auth.json is deleted.
func (l *managedSubscriptionLease) beginAuthenticationCleanup(home string, processOwner domain.ID, latest []byte) error {
	if err := l.recordCleanupEvidence(home, processOwner); err != nil {
		return err
	}
	return l.syncRecoveryCapture(latest)
}

func (l *managedSubscriptionLease) cleanupAuthentication(home string, latest []byte) error {
	if err := l.beginAuthenticationCleanup(home, l.journal.Operation, latest); err != nil {
		return err
	}
	return cleanupExecutionAuthentication(home, latest, l.response.Bundle)
}

func (l *managedSubscriptionLease) cleanupSharedAuthentication(home string, processOwner domain.ID, latest, original []byte) error {
	if err := l.beginAuthenticationCleanup(home, processOwner, latest); err != nil {
		return err
	}
	return cleanupSharedExecutionAuthentication(home, latest, original)
}

func (l *managedSubscriptionLease) cleanupPreNative(home string, helperRetained bool) (bool, error) {
	return l.cleanupPreNativeOriginal(home, l.response.Bundle, helperRetained)
}

func (l *managedSubscriptionLease) cleanupPreNativeOriginal(home string, original []byte, helperRetained bool) (bool, error) {
	if err := l.beginAuthenticationCleanup(home, l.journal.Operation, original); err != nil {
		return false, err
	}
	return cleanupPreNativeAuthentication(home, original, helperRetained)
}

func recoveryLogger(config Config) *slog.Logger {
	if config.Logger != nil {
		return config.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// runManagedCodexRecovery reconciles one fenced original Execute lease. It never
// takes a lease, never runs input and never reuses the failed Finish request.
func runManagedCodexRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, account domain.Account, tracker *recoveryTracker) (returned error) {
	logger := recoveryLogger(config).With("account_id", accountID)
	state := account.Subscription
	if !config.recovery || !recoveryEligible(account, credential.MachineID) {
		return blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	lease := state.Lease
	logger = logger.With("lease_id", lease.ID, "operation_id", lease.OperationID)
	logger.InfoContext(ctx, "subscription_recovery_attempt")
	defer func() {
		var blocked *recoveryBlocked
		switch {
		case returned == nil:
			logger.InfoContext(ctx, "subscription_recovery_result", "result", "settled")
		case errors.As(returned, &blocked):
			logger.WarnContext(ctx, "subscription_recovery_result", "result", "blocked", "reason", blocked.reason)
			if tracker != nil && tracker.markReported(accountID, lease, blocked.reason) && state.RecoveryReason != blocked.reason {
				reportRecoveryBlocked(ctx, client, credential, instance, accountID, lease, blocked.reason, logger)
			}
		default:
			logger.WarnContext(ctx, "subscription_recovery_result", "result", "retry", "code", domain.SafeError(returned).Code)
		}
	}()

	// Bind to the exact original lease evidence.
	journalPath := filepath.Join(config.Root, "managed-auth", string(lease.ID)+".json")
	raw, err := security.ReadPrivate(journalPath, 64<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil {
		return blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	if journal.Version != 1 || journal.Lease != lease.ID || journal.Account != accountID || journal.Operation != lease.OperationID || journal.Instance != lease.InstanceID || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE || journal.Generation != lease.Generation || journal.LeaseRevision != lease.Revision || journal.Finish.Validate() != nil {
		return blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	// One in-flight owner per account: a locally active original owner or
	// continuation keeps its own ordinary settlement path.
	if config.codexOwners != nil {
		gate := config.codexOwners.gate(accountID)
		gate.mu.Lock()
		active := gate.owner != nil
		gate.mu.Unlock()
		if active {
			return errors.New("local owner active")
		}
		select {
		case gate.slot <- struct{}{}:
		case <-ctx.Done():
			return domain.SafeError(ctx.Err())
		}
		defer gate.release()
		defer func() {
			// A settled recovery clears the local fence of the same account.
			if returned == nil {
				gate.mu.Lock()
				gate.uncertain = false
				gate.mu.Unlock()
			}
		}()
	}

	if journal.RecoveryID == "" {
		journal.RecoveryID = domain.NewID()
		if err := writeJSON(journalPath, journal); err != nil {
			return subscription.Invalid()
		}
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if journal.RecoveryOutcome == "" {
		outcome, captureID, err := reconcileOriginalExecution(recoveryCtx, config, client, credential, instance, accountID, lease, &journal, journalPath, logger)
		if err != nil {
			return err
		}
		journal.RecoveryOutcome, journal.RecoveryCapture = outcome, captureID
		if err := writeJSON(journalPath, journal); err != nil {
			return subscription.Invalid()
		}
	}
	outcome := pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_CREDENTIALS_VERIFIED
	if journal.RecoveryOutcome == domain.RecoveryNoUsableCredentials {
		outcome = pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS
	}
	reportCtx, stop := context.WithTimeout(ctx, recoveryRPCTimeout)
	defer stop()
	_, err = client.RecoverSubscription(reportCtx, authenticated(credential, &pb.RecoverSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(journal.RecoveryID), Id: string(accountID), ExpectedRevision: lease.Revision}, LeaseId: string(lease.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(lease.Generation), Outcome: outcome, CaptureId: string(journal.RecoveryCapture)}))
	if err != nil {
		return rpc.ClientError(err)
	}
	journal.State, journal.Cleanup, journal.Success = managedReported, true, false
	if err := writeJSON(journalPath, journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}

func reportRecoveryBlocked(ctx context.Context, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, lease *domain.SubscriptionLease, reason domain.SubscriptionRecoveryReason, logger *slog.Logger) {
	wire := pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_UNSPECIFIED
	switch reason {
	case domain.RecoveryCredentialProofUnavailable:
		wire = pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_CREDENTIAL_PROOF_UNAVAILABLE
	case domain.RecoveryOwnershipEvidenceUnavailable:
		wire = pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_OWNERSHIP_EVIDENCE_UNAVAILABLE
	case domain.RecoveryNativeProcessUnconfirmed:
		wire = pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_NATIVE_PROCESS_UNCONFIRMED
	case domain.RecoveryAuthenticationCleanupUnconfirmed:
		wire = pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_AUTHENTICATION_CLEANUP_UNCONFIRMED
	case domain.RecoveryCredentialValidationFailed:
		wire = pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_CREDENTIAL_VALIDATION_FAILED
	}
	ctx, cancel := context.WithTimeout(ctx, recoveryRPCTimeout)
	defer cancel()
	_, err := client.RecoverSubscription(ctx, authenticated(credential, &pb.RecoverSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(accountID), ExpectedRevision: lease.Revision}, LeaseId: string(lease.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(lease.Generation), Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, Reason: wire}))
	if err != nil {
		logger.Warn("subscription_recovery_block_report_failed", "reason", reason, "code", domain.SafeError(rpc.ClientError(err)).Code)
	}
}

// recoveryHome validates the journaled native home as a private directory
// inside this Worker's own root.
func recoveryHome(config Config, home string) (string, error) {
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	root, err := filepath.EvalSymlinks(config.Root)
	if err != nil {
		return "", blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil || !strings.HasPrefix(resolved+string(filepath.Separator), root+string(filepath.Separator)) {
		return "", blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	if err := security.CheckPrivateDir(resolved); err != nil {
		return "", blockedRecovery(domain.RecoveryOwnershipEvidenceUnavailable)
	}
	return resolved, nil
}

// reconcileOriginalExecution proves independent native, process and file
// cleanup and chooses the closed recovery outcome.
func reconcileOriginalExecution(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, lease *domain.SubscriptionLease, journal *managedSubscriptionJournal, journalPath string, logger *slog.Logger) (domain.SubscriptionRecoveryOutcome, domain.ID, error) {
	read, err := client.ReadSubscriptionRecoveryCapture(ctx, authenticated(credential, &pb.ReadSubscriptionRecoveryCaptureRequest{AccountId: string(accountID), LeaseId: string(lease.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(lease.Generation)}))
	if err != nil {
		return "", "", rpc.ClientError(err)
	}
	original, captured := read.Msg.OriginalBundle, read.Msg.LatestBundle
	defer clear(original)
	defer clear(captured)
	if _, _, err := subscription.Parse(original); err != nil {
		return "", "", blockedRecovery(domain.RecoveryCredentialProofUnavailable)
	}
	// Legacy journals predate cleanup evidence and capture receipts. They are
	// recovered only when their original proof is sufficient.
	if journal.NativeHome == "" {
		return "", "", blockedRecovery(domain.RecoveryCredentialProofUnavailable)
	}
	home, err := recoveryHome(config, journal.NativeHome)
	if err != nil {
		return "", "", err
	}
	owners := []domain.ID{journal.ProcessOwner}
	if journal.ProcessOwner == "" {
		owners = []domain.ID{lease.OperationID}
		if lease.Shared {
			owners = []domain.ID{lease.ID}
		}
	}
	for _, owner := range owners {
		bounded, stop := context.WithTimeout(ctx, recoveryProcessLimit)
		err := process.ReconcileOwnerContext(bounded, filepath.Join(config.Root, "processes"), owner)
		stop()
		if err != nil {
			logger.WarnContext(ctx, "subscription_recovery_process_unconfirmed", "process_owner_id", owner, "code", domain.SafeError(err).Code)
			if ctx.Err() == nil && domain.SafeError(err).Code == domain.RecoveryRequired {
				return "", "", blockedRecovery(domain.RecoveryNativeProcessUnconfirmed)
			}
			return "", "", err
		}
	}

	// The latest candidate is the credential file left by the original native
	// tree, or the protected capture when that file was already deleted.
	authPath := filepath.Join(home, "auth.json")
	var latest []byte
	defer func() { clear(latest) }()
	present := false
	if info, statErr := os.Lstat(authPath); statErr == nil {
		present = true
		if !info.Mode().IsRegular() {
			return "", "", blockedRecovery(domain.RecoveryAuthenticationCleanupUnconfirmed)
		}
		latest, err = security.ReadPrivate(authPath, subscription.MaxBundle)
		if err != nil {
			return "", "", err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", statErr
	}
	usable := false
	if present {
		if _, _, parseErr := subscription.Parse(latest); parseErr == nil && (subscription.Digest(latest) == subscription.Digest(original) || subscription.Refreshed(original, latest) == nil) {
			usable = true
		}
		if !usable {
			clear(latest)
			latest = nil
		}
	} else if len(captured) != 0 {
		latest, usable = bytes.Clone(captured), true
	} else if journal.CaptureID != "" {
		// A capture was synchronized but can no longer be read; never fabricate it.
		return "", "", blockedRecovery(domain.RecoveryCredentialProofUnavailable)
	}
	captureID := domain.ID(read.Msg.CaptureId)
	if usable && present && (captureID == "" || !bytes.Equal(captured, latest)) {
		// Protect the credential before deleting it. The synchronized request is
		// durable in the journal and is replayed by identity after a crash.
		synced := &managedSubscriptionLease{logger: config.Logger, client: client, credential: credential, instance: instance, account: accountID, finishID: journal.Finish, journalPath: journalPath, journal: *journal, recovery: true, response: &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), GenerationId: string(lease.Generation), LeaseRevision: lease.Revision}}
		if err := synced.syncRecoveryCapture(latest); err != nil {
			return "", "", err
		}
		*journal = synced.journal
		captureID = journal.CaptureID
	}
	scanLatest := latest
	if !usable {
		scanLatest = original
	}
	if present {
		if usable {
			if err := cleanupSharedExecutionAuthentication(home, latest, original); err != nil {
				return "", "", recoveryCleanupFailure(err)
			}
		} else {
			if err := os.Remove(authPath); err != nil {
				return "", "", err
			}
			if err := security.SyncParent(authPath); err != nil {
				return "", "", err
			}
			if err := scanExecutionAuthenticationMode(home, scanLatest, original, true); err != nil {
				return "", "", recoveryCleanupFailure(err)
			}
		}
	} else if err := scanExecutionAuthenticationMode(home, scanLatest, original, true); err != nil {
		return "", "", recoveryCleanupFailure(err)
	}
	if usable {
		logger.InfoContext(ctx, "subscription_recovery_cleanup_confirmed", "credentials", "verified", "capture_id", captureID)
		return domain.RecoveryCredentialsVerified, captureID, nil
	}
	logger.InfoContext(ctx, "subscription_recovery_cleanup_confirmed", "credentials", "unavailable")
	return domain.RecoveryNoUsableCredentials, "", nil
}

// recoveryCleanupFailure classifies a failed independent cleanup. Pending,
// special-file, remnant and identity evidence need operator action and stay
// protected; filesystem failures are retried with backoff.
func recoveryCleanupFailure(err error) error {
	var failure *executionAuthenticationFailure
	if errors.As(err, &failure) {
		switch failure.reason {
		case executionAuthPending, executionAuthSpecial, executionAuthRemnant, executionAuthIdentity:
			return blockedRecovery(domain.RecoveryAuthenticationCleanupUnconfirmed)
		}
	}
	return err
}
