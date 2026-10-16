// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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

// Automatic recovery of a fenced managed ChatGPT EXECUTE lease.
//
// Before a managed execution deletes its transient authentication, the Worker
// synchronizes a server-vault protected capture of the latest validated bundle
// and records the exact authentication home in its private journal. If cleanup
// later fails and the account is fenced, the original Worker (possibly a
// replacement instance of it) reconciles only the retained original journal:
// it joins the original process owners, removes the transient authentication,
// scans the complete retained history and reports one durable recovery request.
// No credential is redistributed, no input is replayed and no history is deleted.

const codexRecoveryPhase = "codex-recovery"

// codexRecoveryLaneBound is the number of accounts the watch lane serves at once.
const codexRecoveryLaneBound = 4

// codexLeasesInFlight tracks managed EXECUTE leases whose execution in this
// process has not yet reported its Finish, so recovery never races a live owner.
var codexLeasesInFlight sync.Map

func codexLeaseInFlight(lease domain.ID) bool {
	_, held := codexLeasesInFlight.Load(string(lease))
	return held
}

// codexRecoveryDelay is the retry delay after failures consecutive attempts for
// one account: 1, 2, 4, 8 and 16 seconds, then 30 seconds.
func codexRecoveryDelay(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	if failures > 5 {
		return 30 * time.Second
	}
	return time.Second << (failures - 1)
}

type codexRecoveryEntry struct {
	obligation string
	failures   int
	next       time.Time
	terminal   bool
}

// codexRecoveryLane schedules recovery attempts per account. It holds no
// goroutine: an attempt is started only when the account is due, so shutdown
// only has to cancel and join the attempts the watch lane already tracks.
type codexRecoveryLane struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[domain.ID]*codexRecoveryEntry
}

func newCodexRecoveryLane() *codexRecoveryLane {
	return &codexRecoveryLane{now: time.Now, entries: map[domain.ID]*codexRecoveryEntry{}}
}

// due reports whether an attempt may start. A terminal outcome waits for a
// changed original obligation or a new lane after reconnection.
func (l *codexRecoveryLane) due(key domain.ID, obligation string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry == nil || entry.obligation != obligation {
		return true
	}
	return !entry.terminal && !l.now().Before(entry.next)
}

// success clears the account's retry state.
func (l *codexRecoveryLane) success(key domain.ID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// failure records one failed attempt and returns the delay before the next.
func (l *codexRecoveryLane) failure(key domain.ID, obligation string, terminal bool) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry == nil || entry.obligation != obligation {
		entry = &codexRecoveryEntry{obligation: obligation}
		l.entries[key] = entry
	}
	entry.failures++
	entry.terminal = terminal
	delay := codexRecoveryDelay(entry.failures)
	entry.next = l.now().Add(delay)
	return delay
}

// codexRecoveryRow reports whether the account is a managed ChatGPT account whose
// fenced EXECUTE lease, or committed recovery settlement, its original Worker
// has to finish.
func codexRecoveryRow(account domain.Account) bool {
	state := account.Subscription
	if account.Type != domain.SubscriptionAccount || account.SubscriptionService != domain.SubscriptionChatGPT || account.ProviderID != "" || state == nil {
		return false
	}
	if state.RecoverySettlement != nil {
		return true
	}
	return state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute
}

// codexRecoveryKey identifies the account's recovery lane entry: the lease the
// original obligation names.
func codexRecoveryKey(account domain.Account) domain.ID {
	if s := account.Subscription.RecoverySettlement; s != nil {
		return s.Lease
	}
	return account.Subscription.Lease.ID
}

// codexRecoveryObligation changes whenever the original obligation changes.
func codexRecoveryObligation(account domain.Account) string {
	state := account.Subscription
	if s := state.RecoverySettlement; s != nil {
		return fmt.Sprintf("settlement:%s:%s", s.Lease, s.Request)
	}
	return fmt.Sprintf("lease:%s:%d:%s:%s", state.Lease.ID, state.Lease.Revision, state.Lease.Generation, state.Lease.InstanceID)
}

// recoveryDisposition is the outcome class of one attempt.
type recoveryDisposition string

const (
	// recoverySettled means the server settled the lease.
	recoverySettled recoveryDisposition = "settled"
	// recoveryRetry means a transient condition; the lane retries with backoff.
	recoveryRetry recoveryDisposition = "retry"
	// recoveryTerminal means the evidence cannot change by itself; the account
	// stays protected until the original obligation changes or the Worker
	// reconnects.
	recoveryTerminal recoveryDisposition = "terminal"
)

type recoveryResult struct {
	disposition recoveryDisposition
	reason      domain.SubscriptionRecoveryBlockedReason
	code        domain.Code
}

func recoveryRetried(reason domain.SubscriptionRecoveryBlockedReason) recoveryResult {
	return recoveryResult{disposition: recoveryRetry, reason: reason}
}

func recoveryBlocked(reason domain.SubscriptionRecoveryBlockedReason) recoveryResult {
	switch reason {
	case domain.RecoveryCredentialProofUnavailable, domain.RecoveryOwnershipEvidenceInvalid:
		return recoveryResult{disposition: recoveryTerminal, reason: reason}
	}
	return recoveryResult{disposition: recoveryRetry, reason: reason}
}

func recoveryReasonProto(reason domain.SubscriptionRecoveryBlockedReason) pb.SubscriptionRecoveryBlockedReason {
	switch reason {
	case domain.RecoveryCredentialProofUnavailable:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE
	case domain.RecoveryCleanupUnconfirmed:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED
	case domain.RecoveryOwnershipEvidenceInvalid:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_OWNERSHIP_EVIDENCE_INVALID
	case domain.RecoveryMembersUnsettled:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED
	case domain.RecoveryCredentialUsersActive:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_USERS_ACTIVE
	case domain.RecoveryActiveOwner:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_ACTIVE_OWNER
	}
	return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED
}

func recoveryReasonDomain(reason pb.SubscriptionRecoveryBlockedReason) domain.SubscriptionRecoveryBlockedReason {
	switch reason {
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE:
		return domain.RecoveryCredentialProofUnavailable
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED:
		return domain.RecoveryCleanupUnconfirmed
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_OWNERSHIP_EVIDENCE_INVALID:
		return domain.RecoveryOwnershipEvidenceInvalid
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED:
		return domain.RecoveryMembersUnsettled
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_USERS_ACTIVE:
		return domain.RecoveryCredentialUsersActive
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_ACTIVE_OWNER:
		return domain.RecoveryActiveOwner
	}
	return ""
}

// recordAuthenticationHome journals the exact authentication home before any
// credential is written there, so a crash or restart can retire the credential in
// exactly that directory. The path lives only in this private journal and is
// never logged.
func (l *managedSubscriptionLease) recordAuthenticationHome(home string) error {
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || !filepath.IsAbs(canonical) {
		return subscription.Invalid()
	}
	l.journal.AuthHome = canonical
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}

// recordNativeOwner creates the durable process index of one native process owner
// and journals the owner before any credentialed native process of that owner can
// start, so recovery can tell an owner that never launched (no journaled owner)
// from one whose index was lost (journaled owner, missing index). A failure means
// no native process of the owner started.
func (l *managedSubscriptionLease) recordNativeOwner(owner domain.ID) error {
	if err := owner.Validate(); err != nil {
		return subscription.Invalid()
	}
	processes := filepath.Join(filepath.Dir(filepath.Dir(l.journalPath)), "processes")
	if err := security.PrivateDir(processes); err != nil {
		return subscription.Invalid()
	}
	if err := security.PrivateDir(filepath.Join(processes, string(owner))); err != nil {
		return subscription.Invalid()
	}
	if slices.Contains(l.journal.NativeOwners, owner) {
		return nil
	}
	l.journal.NativeOwners = append(l.journal.NativeOwners, owner)
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		l.journal.NativeOwners = l.journal.NativeOwners[:len(l.journal.NativeOwners)-1]
		return subscription.Invalid()
	}
	return nil
}

// cleanupAuthentication synchronizes the protected capture of the latest
// validated bundle and only then deletes the transient authentication and scans
// the retained history. A capture that cannot be confirmed keeps the file in
// place, so the fenced lease can still be recovered from it.
func (l *managedSubscriptionLease) cleanupAuthentication(ctx context.Context, home string, latest, original []byte) error {
	if err := l.captureBeforeCleanup(ctx, latest, original); err != nil {
		return err
	}
	return cleanupExecutionAuthentication(ctx, home, latest, original)
}

// captureBeforeCleanup is the single gate every deletion of a managed lease's
// transient authentication passes: the protected capture is confirmed first, or
// the deletion does not happen.
func (l *managedSubscriptionLease) captureBeforeCleanup(ctx context.Context, latest, original []byte) error {
	if err := l.syncRecoveryCapture(ctx, latest, original); err != nil {
		if l.logger != nil {
			l.logger.Warn("codex_recovery_capture_unconfirmed", "phase", codexRecoveryPhase, "account_id", l.account, "lease_id", l.leaseID(), "code", domain.SafeError(err).Code)
		}
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthCapture}
	}
	return nil
}

// syncRecoveryCapture sends the latest validated bundle to the server vault under
// a durable capture identity. A retry after a crash or a lost response reuses the
// same identity, so the server keeps one capture of the same bundle.
func (l *managedSubscriptionLease) syncRecoveryCapture(ctx context.Context, latest, original []byte) error {
	if !l.recoveryCapture || len(latest) == 0 {
		return nil
	}
	digest := subscription.Digest(latest)
	if l.journal.CaptureSynced && l.journal.CaptureDigest == digest {
		return nil
	}
	if l.journal.CaptureRequest == "" || l.journal.CaptureDigest != digest {
		l.journal.CaptureRequest, l.journal.CaptureDigest, l.journal.CaptureSynced = domain.NewID(), digest, false
		if err := writeJSON(l.journalPath, l.journal); err != nil {
			return subscription.Invalid()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	originalCopy, latestCopy := bytes.Clone(original), bytes.Clone(latest)
	defer clear(originalCopy)
	defer clear(latestCopy)
	response, err := l.client.SyncSubscriptionRecoveryCapture(ctx, authenticated(l.credential, &pb.SyncSubscriptionRecoveryCaptureRequest{
		Mutation: &pb.Mutation{RequestId: string(l.journal.CaptureRequest), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision},
		LeaseId:  l.response.LeaseId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), GenerationId: l.response.GenerationId,
		Original: originalCopy, Latest: latestCopy,
	}))
	if err != nil {
		return rpc.ClientError(err)
	}
	if response.Msg.CaptureId != string(l.journal.CaptureRequest) {
		return subscription.Invalid()
	}
	l.journal.CaptureSynced = true
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	if l.logger != nil {
		l.logger.Info("codex_recovery_capture_synced", "phase", codexRecoveryPhase, "account_id", l.account, "lease_id", l.leaseID(), "capture_id", l.journal.CaptureRequest, "replayed", response.Msg.Replayed)
	}
	return nil
}

// recoveryEvidence is the verified original evidence of one fenced lease.
type recoveryEvidence struct {
	path    string
	journal managedSubscriptionJournal
}

// loadRecoveryEvidence reads the retained original journal and binds it to the
// server, account, lease, generation, operation and original instance the fenced
// lease names. A replacement instance of the same Worker reads exactly this
// journal and nothing else.
func loadRecoveryEvidence(config Config, credential Credential, accountID domain.ID, lease domain.SubscriptionLease) (recoveryEvidence, bool) {
	path := filepath.Join(config.Root, "managed-auth", string(lease.ID)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Lease != lease.ID || journal.Account != accountID || journal.Operation != lease.OperationID || journal.Instance != lease.InstanceID || journal.Generation != lease.Generation || journal.Finish.Validate() != nil || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		return recoveryEvidence{}, false
	}
	if journal.LeaseRevision != 0 && journal.LeaseRevision != lease.Revision || journal.Server != "" && journal.Server != credential.ServerID || journal.OwnerLease != "" {
		return recoveryEvidence{}, false
	}
	return recoveryEvidence{path: path, journal: journal}, true
}

// recoveryHome verifies the journaled authentication home: an absolute, clean
// canonical directory inside this Worker's root. The reason is empty when the
// home is usable and otherwise closes the diagnostic.
//
// A home that no longer exists holds no credential file only when the absence is
// itself bound to retained evidence: the journaled path is lexically contained
// in the Worker root, its nearest existing ancestor is a canonical directory of
// that root (never a symlinked or foreign location), and the Worker's own profile
// registry no longer registers that home. An explicit session or storage
// deletion removes the whole profile directory, credential included, and its
// registry entry. A home the registry still names but the filesystem no longer
// shows was moved away with its credential and stays unconfirmed.
func recoveryHome(config Config, machine domain.ID, home string) (canonicalHome string, missing bool, reason domain.SubscriptionRecoveryBlockedReason) {
	if home == "" {
		return "", false, domain.RecoveryCredentialProofUnavailable
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", false, domain.RecoveryOwnershipEvidenceInvalid
	}
	root, err := filepath.EvalSymlinks(config.Root)
	if err != nil {
		return "", false, domain.RecoveryOwnershipEvidenceInvalid
	}
	if !strings.HasPrefix(home, root+string(filepath.Separator)) {
		return "", false, domain.RecoveryOwnershipEvidenceInvalid
	}
	canonical, err := filepath.EvalSymlinks(home)
	if errors.Is(err, fs.ErrNotExist) {
		if !recoveryMissingAncestor(root, home) {
			return "", false, domain.RecoveryOwnershipEvidenceInvalid
		}
		registry, err := nativeProfiles(config, machine)
		if err != nil {
			return "", false, domain.RecoveryCleanupUnconfirmed
		}
		registered, err := registry.HomeRegistered(home)
		if err != nil || registered {
			return "", false, domain.RecoveryCleanupUnconfirmed
		}
		return home, true, ""
	}
	if err != nil || canonical != home || security.CheckPrivateDir(canonical) != nil {
		return "", false, domain.RecoveryOwnershipEvidenceInvalid
	}
	return canonical, false, ""
}

// recoveryMissingAncestor proves that a missing home sits below a real
// directory chain of the Worker root: its nearest existing ancestor is a
// directory strictly inside the root that resolves to itself.
func recoveryMissingAncestor(root, home string) bool {
	for ancestor := filepath.Dir(home); strings.HasPrefix(ancestor, root+string(filepath.Separator)); ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() {
			return false
		}
		canonical, err := filepath.EvalSymlinks(ancestor)
		return err == nil && canonical == ancestor
	}
	return false
}

// recoverAuthentication removes the transient authentication of an interrupted
// execution and proves, with a complete streaming inspection of the retained
// history, that no credential remnant survives. A missing file is already
// removed. A file that is not one of the exact captured bundles (latest or
// original) or the retained legacy bytes stays in place. Retained legacy bytes
// with no server capture are removed only after the scan has passed.
func recoverAuthentication(ctx context.Context, home string, missingHome bool, latest, original, extra []byte) error {
	if missingHome {
		return nil
	}
	path := filepath.Join(home, "auth.json")
	// keepFile marks retained legacy bytes that no server capture backs. The file
	// is then the only copy of the evidence, so it stays in place until the
	// retained-history scan has passed; a scan that fails for any reason (a
	// cancellation, an unreadable entry or a removable remnant) leaves the next
	// attempt the same evidence instead of a terminal absence of proof.
	keepFile := false
	closed, err := security.ReadPrivate(path, subscription.MaxBundle)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		clear(closed)
	case err != nil:
		clear(closed)
		return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
	default:
		// The captured original is the exact generation the Take delivered into
		// the profile; an interrupted execution that never refreshed it leaves
		// precisely those bytes, which are as authenticated as the latest bundle.
		captured := len(latest) != 0 && bytes.Equal(closed, latest) || len(original) != 0 && bytes.Equal(closed, original)
		legacy := len(extra) != 0 && bytes.Equal(closed, extra)
		clear(closed)
		if !captured && !legacy {
			return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthBundle}
		}
		if captured {
			if err := os.Remove(path); err != nil {
				return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
			}
		} else {
			keepFile = true
		}
	}
	if !keepFile {
		if err := security.SyncParent(path); err != nil {
			return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
		}
	}
	var needles [][]byte
	for _, bundle := range [][]byte{latest, original} {
		if len(bundle) != 0 {
			needles = append(needles, authenticationNeedles(bundle)...)
		}
	}
	if len(extra) != 0 {
		needles = append(needles, bytes.Clone(extra))
	}
	if len(needles) == 0 {
		// Nothing identifies a credential, but the structural scan still refuses
		// pending entries, special files and a changed root identity.
		sentinel := make([]byte, 32)
		_, _ = rand.Read(sentinel)
		needles = append(needles, sentinel)
	}
	skip := ""
	if keepFile {
		skip = "auth.json"
	}
	if err := scanRetainedAuthenticationSkipping(ctx, home, needles, skip); err != nil {
		return err
	}
	if keepFile {
		if err := os.Remove(path); err != nil {
			return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
		}
		if err := security.SyncParent(path); err != nil {
			return &executionAuthenticationFailure{stage: executionAuthFile, reason: executionAuthFilesystem}
		}
	}
	return nil
}

func logCodexRecovery(logger *slog.Logger, ctx context.Context, level slog.Level, message string, args ...any) {
	if logger == nil {
		return
	}
	logger.Log(ctx, level, message, append([]any{"phase", codexRecoveryPhase}, args...)...)
}

// runCodexRecovery performs one reconciliation attempt for one fenced managed
// ChatGPT account under the original obligation the account names.
func runCodexRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, account domain.Account) recoveryResult {
	state := account.Subscription
	var lease domain.SubscriptionLease
	var settlement *domain.SubscriptionRecoverySettlement
	if state.RecoverySettlement != nil {
		settlement = state.RecoverySettlement
		lease = domain.SubscriptionLease{ID: settlement.Lease, MachineID: settlement.Machine}
	} else {
		lease = *state.Lease
	}
	if lease.MachineID != credential.MachineID {
		return recoveryBlocked(domain.RecoveryOwnershipEvidenceInvalid)
	}
	logger := config.Logger
	if settlement == nil && codexLeaseInFlight(lease.ID) {
		// The live execution of this very lease still owns its home. If it ends
		// unconfirmed the next watch round runs this recovery.
		logCodexRecovery(logger, ctx, slog.LevelInfo, "codex_recovery_deferred", "account_id", accountID, "lease_id", lease.ID, "reason", domain.RecoveryActiveOwner)
		return recoveryRetried(domain.RecoveryActiveOwner)
	}
	if settlement != nil {
		// The credential decision is already committed; resend the same request so
		// the server finishes the protected-reference retirement.
		path := filepath.Join(config.Root, "managed-auth", string(settlement.Lease)+".json")
		raw, err := security.ReadPrivate(path, 16<<10)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Account != accountID || journal.RecoveryRequest != settlement.Request {
			logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", settlement.Lease, "reason", domain.RecoveryOwnershipEvidenceInvalid)
			return recoveryBlocked(domain.RecoveryOwnershipEvidenceInvalid)
		}
		return resendRecovery(ctx, config, client, credential, instance, accountID, path, journal, domain.SubscriptionLease{ID: settlement.Lease, MachineID: settlement.Machine, InstanceID: journal.Instance, Generation: journal.Generation, Revision: journal.LeaseRevision}, nil)
	}
	evidence, ok := loadRecoveryEvidence(config, credential, accountID, lease)
	if !ok {
		logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", domain.RecoveryOwnershipEvidenceInvalid)
		reportRecoveryBlock(ctx, client, credential, instance, accountID, lease, domain.RecoveryOwnershipEvidenceInvalid)
		return recoveryBlocked(domain.RecoveryOwnershipEvidenceInvalid)
	}
	journal := evidence.journal
	members := recoveryMembers(state)
	if journal.RecoveryRequest != "" && journal.RecoveryCleaned {
		return resendRecovery(ctx, config, client, credential, instance, accountID, evidence.path, journal, lease, members)
	}
	// Reconcile every original process owner through the process journals, never
	// by PID guesses, before the home is touched.
	for _, owner := range recoveryProcessOwners(lease, state) {
		// The durable process index precedes every credentialed native process and
		// is journaled before it. A missing index proves that no process ran only
		// when the owner's retained journal proves it was never launched; a lost
		// index of a journaled owner (or of a journal that proves nothing) is never
		// read as proof of completion. An index that exists is reconciled and never
		// read as proof of completion when it is damaged.
		if _, err := os.Lstat(filepath.Join(config.Root, "processes", string(owner))); errors.Is(err, fs.ErrNotExist) {
			if recoveryOwnerNeverLaunched(config, accountID, lease, state, evidence.journal, owner) {
				continue
			}
			reason := domain.RecoveryOwnershipEvidenceInvalid
			if journal.AuthHome == "" {
				reason = domain.RecoveryCredentialProofUnavailable
			}
			logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", reason, "stage", "process-index-missing")
			reportRecoveryBlock(ctx, client, credential, instance, accountID, lease, reason)
			return recoveryBlocked(reason)
		}
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), owner); err != nil {
			logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", domain.RecoveryCleanupUnconfirmed, "code", domain.SafeError(err).Code)
			return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		}
	}
	// Shared-owner members run inside the owner's joined runtime; each listed
	// member must be a member of this very owner on this Worker.
	if !recoveryMembersOwned(config, accountID, lease, members) {
		logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", domain.RecoveryOwnershipEvidenceInvalid, "members", len(members))
		reportRecoveryBlock(ctx, client, credential, instance, accountID, lease, domain.RecoveryOwnershipEvidenceInvalid)
		return recoveryBlocked(domain.RecoveryOwnershipEvidenceInvalid)
	}
	home, missingHome, reason := recoveryHome(config, credential.MachineID, journal.AuthHome)
	if reason != "" {
		logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", reason)
		reportRecoveryBlock(ctx, client, credential, instance, accountID, lease, reason)
		return recoveryBlocked(reason)
	}
	latest, original, extra, report, result := recoveryCredentials(ctx, config, client, credential, instance, accountID, lease, &evidence, home, missingHome)
	defer clear(latest)
	defer clear(original)
	defer clear(extra)
	if result != nil {
		reportRecoveryBlock(ctx, client, credential, instance, accountID, lease, result.reason)
		return *result
	}
	if err := recoverAuthentication(ctx, home, missingHome, latest, original, extra); err != nil {
		var failure *executionAuthenticationFailure
		stage, reason, count := executionAuthFile, executionAuthFilesystem, 0
		if errors.As(err, &failure) {
			stage, reason, count = failure.stage, failure.reason, failure.count
		}
		logCodexRecovery(logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", domain.RecoveryCleanupUnconfirmed, "stage", stage, "cleanup_reason", reason, "file_count", count)
		reportRecoveryBlock(ctx, client, credential, instance, accountID, lease, domain.RecoveryCleanupUnconfirmed)
		return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
	}
	journal = evidence.journal
	journal.RecoveryRequest, journal.RecoveryReport, journal.RecoveryCleaned = domain.NewID(), int32(report), true
	journal.RecoveryCapture = ""
	if report == pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE {
		journal.RecoveryCapture = evidence.journal.CaptureRequest
	}
	if err := writeJSON(evidence.path, journal); err != nil {
		return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
	}
	logCodexRecovery(logger, ctx, slog.LevelInfo, "codex_recovery_cleanup_confirmed", "account_id", accountID, "lease_id", lease.ID, "request_id", journal.RecoveryRequest, "report", report.String(), "members", len(members))
	return resendRecovery(ctx, config, client, credential, instance, accountID, evidence.path, journal, lease, members)
}

// recoveryCredentials resolves the latest credential evidence. Evidence is the
// protected capture when one was synchronized; otherwise legacy retained
// authentication that validates is captured for the first time. Retained
// authentication that does not validate, or that the server positively rejects
// as a bundle, is the proven absence of usable credentials; any other capture
// failure retains the file and retries, and no evidence at all stays protected.
func recoveryCredentials(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, lease domain.SubscriptionLease, evidence *recoveryEvidence, home string, missingHome bool) (latest, original, extra []byte, report pb.SubscriptionRecoveryReport, blocked *recoveryResult) {
	block := func(reason domain.SubscriptionRecoveryBlockedReason) *recoveryResult {
		logCodexRecovery(config.Logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", lease.ID, "reason", reason)
		r := recoveryBlocked(reason)
		return &r
	}
	read, err := client.ReadSubscriptionRecoveryCapture(ctx, authenticated(credential, &pb.ReadSubscriptionRecoveryCaptureRequest{
		AccountId: string(accountID), LeaseId: string(lease.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), OriginalInstanceId: string(lease.InstanceID), GenerationId: string(lease.Generation),
	}))
	if err == nil {
		if read.Msg.CaptureId == "" || len(read.Msg.Latest) == 0 {
			return nil, nil, nil, 0, block(domain.RecoveryOwnershipEvidenceInvalid)
		}
		evidence.journal.CaptureRequest, evidence.journal.CaptureSynced = domain.ID(read.Msg.CaptureId), true
		return read.Msg.Latest, read.Msg.Original, nil, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, nil
	}
	problem := domain.SafeError(rpc.ClientError(err))
	if problem.Cause != "subscription_recovery_capture_absent" {
		if problem.Code == domain.RecoveryRequired {
			return nil, nil, nil, 0, block(domain.RecoveryOwnershipEvidenceInvalid)
		}
		logCodexRecovery(config.Logger, ctx, slog.LevelWarn, "codex_recovery_retry", "account_id", accountID, "lease_id", lease.ID, "code", problem.Code)
		r := recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		return nil, nil, nil, 0, &r
	}
	// Legacy evidence: only the retained authentication file can supply the
	// latest bundle. Without it no equivalent proof survives.
	if missingHome {
		return nil, nil, nil, 0, block(domain.RecoveryCredentialProofUnavailable)
	}
	raw, readErr := security.ReadPrivate(filepath.Join(home, "auth.json"), subscription.MaxBundle)
	if errors.Is(readErr, fs.ErrNotExist) {
		return nil, nil, nil, 0, block(domain.RecoveryCredentialProofUnavailable)
	}
	if readErr != nil {
		clear(raw)
		r := recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		return nil, nil, nil, 0, &r
	}
	if _, _, parseErr := subscription.Parse(raw); parseErr != nil {
		// The retained file is not a usable bundle: its exact bytes are the only
		// remnant to look for, and there are no usable credentials to restore.
		return nil, nil, raw, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, nil
	}
	request := domain.NewID()
	evidence.journal.CaptureRequest, evidence.journal.CaptureDigest, evidence.journal.CaptureSynced = request, subscription.Digest(raw), false
	if err := writeJSON(evidence.path, evidence.journal); err != nil {
		clear(raw)
		r := recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		return nil, nil, nil, 0, &r
	}
	synced, syncErr := client.SyncSubscriptionRecoveryCapture(ctx, authenticated(credential, &pb.SyncSubscriptionRecoveryCaptureRequest{
		Mutation: &pb.Mutation{RequestId: string(request), Id: string(accountID), ExpectedRevision: lease.Revision},
		LeaseId:  string(lease.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), GenerationId: string(lease.Generation), Latest: bytes.Clone(raw),
	}))
	if syncErr != nil {
		problem := domain.SafeError(rpc.ClientError(syncErr))
		if problem.Cause == domain.SubscriptionRecoveryBundleRejectedCause {
			// Only the server's distinct positive rejection of the bundle itself (a
			// different identity or not a refresh of the delivered generation) makes
			// the retained file unusable evidence. Every other refusal (binding,
			// authorization, withdrawn capability, stale lease evidence) says nothing
			// about the bundle, so the only surviving credential stays in place.
			logCodexRecovery(config.Logger, ctx, slog.LevelWarn, "codex_recovery_legacy_bundle_rejected", "account_id", accountID, "lease_id", lease.ID)
			return nil, nil, raw, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, nil
		}
		logCodexRecovery(config.Logger, ctx, slog.LevelWarn, "codex_recovery_legacy_capture_retained", "account_id", accountID, "lease_id", lease.ID, "code", problem.Code)
		clear(raw)
		r := recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		return nil, nil, nil, 0, &r
	}
	evidence.journal.CaptureSynced = true
	if err := writeJSON(evidence.path, evidence.journal); err != nil {
		clear(raw)
		r := recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		return nil, nil, nil, 0, &r
	}
	logCodexRecovery(config.Logger, ctx, slog.LevelInfo, "codex_recovery_legacy_capture_synced", "account_id", accountID, "lease_id", lease.ID, "capture_id", synced.Msg.CaptureId)
	return raw, nil, nil, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, nil
}

// recoveryOwnerNeverLaunched reports whether the retained journal of the lease or
// member that governs a process owner proves that the owner never launched a
// credentialed native process: the journal was written by a Worker that journals
// every owner before its launch, and it does not name this owner. Any journal
// that cannot be read or bound proves nothing.
func recoveryOwnerNeverLaunched(config Config, accountID domain.ID, lease domain.SubscriptionLease, state *domain.SubscriptionState, original managedSubscriptionJournal, owner domain.ID) bool {
	governing := original
	if owner != lease.OperationID && owner != lease.ID {
		member, ok := domain.SubscriptionMember{}, false
		for _, candidate := range state.Members {
			if candidate.OperationID == owner {
				member, ok = candidate, true
				break
			}
		}
		if !ok {
			return false
		}
		if member.LeaseID != lease.ID {
			raw, err := security.ReadPrivate(filepath.Join(config.Root, "managed-auth", string(member.LeaseID)+".json"), 16<<10)
			governing = managedSubscriptionJournal{}
			if err != nil || domain.Decode(raw, &governing) != nil || governing.Version != 1 || governing.Account != accountID || governing.Lease != member.LeaseID || governing.Operation != member.OperationID || governing.OwnerLease != lease.ID {
				return false
			}
		}
	}
	return governing.NativeOwnerEvidence && !slices.Contains(governing.NativeOwners, owner)
}

// recoveryProcessOwners lists every process owner the original lease started:
// the job, the shared owner and each shared member's job.
func recoveryProcessOwners(lease domain.SubscriptionLease, state *domain.SubscriptionState) []domain.ID {
	owners := []domain.ID{lease.OperationID, lease.ID}
	for _, member := range state.Members {
		owners = append(owners, member.OperationID)
	}
	slices.Sort(owners)
	return slices.Compact(owners)
}

func recoveryMembers(state *domain.SubscriptionState) []domain.ID {
	members := make([]domain.ID, 0, len(state.Members))
	for _, member := range state.Members {
		members = append(members, member.LeaseID)
	}
	slices.Sort(members)
	return members
}

// recoveryMembersOwned proves that every member the server lists belongs to this
// owner on this Worker: the founding member shares the owner's lease, and every
// other member has a retained journal that names the owner.
func recoveryMembersOwned(config Config, accountID domain.ID, lease domain.SubscriptionLease, members []domain.ID) bool {
	for _, member := range members {
		if member == lease.ID {
			continue
		}
		raw, err := security.ReadPrivate(filepath.Join(config.Root, "managed-auth", string(member)+".json"), 16<<10)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Account != accountID || journal.Lease != member || journal.OwnerLease != lease.ID {
			return false
		}
	}
	return true
}

// resendRecovery sends the journaled recovery request. The request identity is
// durable, so a crash or lost response resends exactly the same request.
func resendRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, path string, journal managedSubscriptionJournal, lease domain.SubscriptionLease, members []domain.ID) recoveryResult {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, string(member))
	}
	request := &pb.RecoverSubscriptionRequest{
		Mutation: &pb.Mutation{RequestId: string(journal.RecoveryRequest), Id: string(accountID), ExpectedRevision: journal.LeaseRevision},
		LeaseId:  string(journal.Lease), MachineId: string(credential.MachineID), InstanceId: string(instance), OriginalInstanceId: string(journal.Instance), GenerationId: string(journal.Generation),
		Report: pb.SubscriptionRecoveryReport(journal.RecoveryReport), CaptureId: string(journal.RecoveryCapture), SettledMemberLeaseIds: ids,
	}
	if request.Mutation.ExpectedRevision == 0 {
		request.Mutation.ExpectedRevision = lease.Revision
	}
	response, err := client.RecoverSubscription(ctx, authenticated(credential, request))
	if err != nil {
		problem := domain.SafeError(rpc.ClientError(err))
		logCodexRecovery(config.Logger, ctx, slog.LevelWarn, "codex_recovery_report_failed", "account_id", accountID, "lease_id", journal.Lease, "request_id", journal.RecoveryRequest, "code", problem.Code)
		if problem.Code == domain.RecoveryRequired {
			return recoveryBlocked(domain.RecoveryOwnershipEvidenceInvalid)
		}
		return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
	}
	switch response.Msg.Outcome {
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED:
		journal.State, journal.Cleanup = managedReported, true
		if err := writeJSON(path, journal); err != nil {
			return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		}
		logCodexRecovery(config.Logger, ctx, slog.LevelInfo, "codex_recovery_settled", "account_id", accountID, "lease_id", journal.Lease, "request_id", journal.RecoveryRequest, "outcome", response.Msg.Outcome.String(), "replayed", response.Msg.Replayed)
		return recoveryResult{disposition: recoverySettled}
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED:
		reason := recoveryReasonDomain(response.Msg.BlockedReason)
		// The next attempt proves the cleanup again under a fresh request identity.
		journal.RecoveryRequest, journal.RecoveryCleaned, journal.RecoveryReport, journal.RecoveryCapture = "", false, 0, ""
		if err := writeJSON(path, journal); err != nil {
			return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
		}
		logCodexRecovery(config.Logger, ctx, slog.LevelWarn, "codex_recovery_blocked", "account_id", accountID, "lease_id", journal.Lease, "reason", reason, "reporter", "server")
		return recoveryBlocked(reason)
	}
	return recoveryRetried(domain.RecoveryCleanupUnconfirmed)
}

// reportRecoveryBlock tells the server why the lease stays protected so account
// observation exposes the closed reason. It is best effort and never settles
// anything.
func reportRecoveryBlock(ctx context.Context, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, accountID domain.ID, lease domain.SubscriptionLease, reason domain.SubscriptionRecoveryBlockedReason) {
	if reason == "" || lease.InstanceID == "" {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, _ = client.RecoverSubscription(bounded, authenticated(credential, &pb.RecoverSubscriptionRequest{
		Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(accountID), ExpectedRevision: lease.Revision},
		LeaseId:  string(lease.ID), MachineId: string(credential.MachineID), InstanceId: string(instance), OriginalInstanceId: string(lease.InstanceID), GenerationId: string(lease.Generation),
		Report: pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED, BlockedReason: recoveryReasonProto(reason),
	}))
}
