package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type executionClaimState string

const (
	executionClaimActive executionClaimState = "active"
	executionClaimClosed executionClaimState = "closed"
)

type executionClaim struct {
	Version             uint32              `json:"version"`
	SessionID           domain.ID           `json:"session_id"`
	JobID               domain.ID           `json:"job_id"`
	ExecutionID         domain.ID           `json:"execution_id"`
	ManifestDigest      string              `json:"manifest_digest"`
	State               executionClaimState `json:"state"`
	WorkspaceDigest     string              `json:"workspace_digest,omitempty"`
	PreviousJobID       domain.ID           `json:"previous_job_id,omitempty"`
	PreviousExecutionID domain.ID           `json:"previous_execution_id,omitempty"`
}

// ExecutionLease retains the session lock through native process cleanup.
// The durable claim lives outside the workspace so a missing/replaced workspace
// cannot erase uncertainty after a Worker crash. It grants no server authority.
type ExecutionLease struct {
	once       sync.Once
	manager    *Manager
	claim      executionClaim
	cwd        string
	release    func() error
	closeError error
}

func (m *Manager) executionClaimPath(session domain.ID) string {
	return filepath.Join(m.Root, "execution-claims", string(session)+".json")
}

func (m *Manager) readExecutionClaim(session domain.ID) (executionClaim, error) {
	var claim executionClaim
	raw, err := security.ReadPrivate(m.executionClaimPath(session), 4096)
	if err != nil {
		return claim, err
	}
	if domain.Decode(raw, &claim) != nil || (claim.Version != 1 && claim.Version != 2) || claim.SessionID != session || domain.UniqueIDs([]domain.ID{claim.SessionID, claim.JobID, claim.ExecutionID}) != nil || len(claim.ManifestDigest) != 64 || !canonicalCommit(claim.ManifestDigest) || (claim.State != executionClaimActive && claim.State != executionClaimClosed) {
		return claim, ResultUncertain()
	}
	if claim.Version == 1 {
		if claim.WorkspaceDigest != "" || claim.PreviousJobID != "" || claim.PreviousExecutionID != "" {
			return claim, ResultUncertain()
		}
	} else {
		if len(claim.WorkspaceDigest) != 64 || !canonicalCommit(claim.WorkspaceDigest) {
			return claim, ResultUncertain()
		}
		if (claim.PreviousJobID == "") != (claim.PreviousExecutionID == "") {
			return claim, ResultUncertain()
		}
		if claim.PreviousJobID != "" {
			if err := domain.UniqueIDs([]domain.ID{claim.SessionID, claim.JobID, claim.ExecutionID, claim.PreviousJobID, claim.PreviousExecutionID}); err != nil {
				return claim, ResultUncertain()
			}
		}
	}
	return claim, nil
}

func (m *Manager) noActiveExecutionClaim(session domain.ID) error {
	claim, err := m.readExecutionClaim(session)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || claim.State != executionClaimClosed {
		return ResultUncertain()
	}
	return nil
}

// ClaimFirstExecution validates already prepared Worktree/General Chat files.
// It neither prepares/fetches nor changes HEAD. A closed first claim cannot be
// reused. Continuation requires the exact closed predecessor; its native-history
// and current account/server authority must still be checked by the caller.
func (m *Manager) ClaimFirstExecution(ctx context.Context, jobID, executionID domain.ID, input PrepareRequest, expected Manifest) (*ExecutionLease, error) {
	return m.claimExecution(ctx, jobID, executionID, nil, input, expected)
}

// ExecutionPredecessor names the original local cleanup proof to retain before
// advancing workspace ownership. It is not a native history/acceptance receipt.
type ExecutionPredecessor struct {
	JobID       domain.ID
	ExecutionID domain.ID
}

func (m *Manager) ClaimContinuation(ctx context.Context, jobID, executionID domain.ID, previous ExecutionPredecessor, input PrepareRequest, expected Manifest) (*ExecutionLease, error) {
	if err := domain.UniqueIDs([]domain.ID{input.SessionID, jobID, executionID, previous.JobID, previous.ExecutionID}); err != nil {
		return nil, err
	}
	return m.claimExecution(ctx, jobID, executionID, &previous, input, expected)
}

func (m *Manager) claimExecution(ctx context.Context, jobID, executionID domain.ID, previous *ExecutionPredecessor, input PrepareRequest, expected Manifest) (lease *ExecutionLease, returned error) {
	for _, id := range []domain.ID{jobID, executionID} {
		if err := id.Validate(); err != nil {
			return nil, err
		}
	}
	if err := input.validate(); err != nil {
		return nil, err
	}
	if input.Type == domain.Local {
		return nil, domain.Fail(domain.Unsupported, "Local native execution needs verified originating-machine authority.", "Keep the original checkout unchanged until that execution profile is supported.")
	}
	if jobID == executionID || jobID == input.SessionID || executionID == input.SessionID {
		return nil, ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if err := m.initialize(); err != nil {
		return nil, ResultUncertain()
	}
	defer func() {
		if returned != nil {
			m.Logger.WarnContext(ctx, "workspace_execution_claim_failed", "session_id", input.SessionID, "job_id", jobID, "execution_id", executionID, "continuation", previous != nil, "code", domain.SafeError(returned).Code)
		}
	}()
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(input.SessionID)+".lock"))
	if err != nil {
		return nil, ResultUncertain()
	}
	defer func() {
		if returned != nil {
			_ = lock.Close()
		}
	}()
	prior, err := m.readExecutionClaim(input.SessionID)
	validation := preparationIdentity
	if previous == nil {
		if err == nil {
			if prior.State == executionClaimClosed {
				return nil, domain.Fail(domain.Conflict, "This workspace already has a retained first execution.", "Reconcile its native history before explicit Resume; never repeat the initial input.")
			}
			return nil, ResultUncertain()
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, ResultUncertain()
		}
		if err := m.requireNoExecutionHistory(input.SessionID); err != nil {
			return nil, err
		}
	} else {
		if err != nil || prior.State != executionClaimClosed || prior.JobID != previous.JobID || prior.ExecutionID != previous.ExecutionID {
			return nil, ResultUncertain()
		}
		if prior.Version != 2 {
			return nil, domain.Fail(domain.RecoveryRequired, "This retained execution lacks continuation workspace identity evidence.", "Preserve its original files and native history for explicit recovery; never replay the first input.")
		}
		if err := process.ReconcileOwnerContext(ctx, m.Git.ProcessRoot, prior.JobID); err != nil {
			return nil, err
		}
		validation = continuationIdentity
	}
	// Retired execution IDs remain unusable even when a caller supplies a fresh
	// job ID. The current closed predecessor is checked separately above.
	if _, err := os.Lstat(m.executionHistoryPath(input.SessionID, executionID)); !errors.Is(err, os.ErrNotExist) {
		return nil, ResultUncertain()
	}
	manifest, err := m.Read(input.SessionID)
	if err != nil {
		return nil, ResultUncertain()
	}
	actual, err := json.Marshal(manifest)
	if err != nil {
		return nil, ResultUncertain()
	}
	accepted, err := json.Marshal(expected)
	if err != nil || !bytes.Equal(actual, accepted) {
		return nil, ResultUncertain()
	}
	digest := sha256.Sum256(actual)
	manifestDigest := hex.EncodeToString(digest[:])
	identityDigest, err := m.verifyWorkspaceIdentity(ctx, input, manifest, validation)
	if err != nil {
		return nil, err
	}
	if previous != nil && (prior.ManifestDigest != manifestDigest || prior.WorkspaceDigest != identityDigest) {
		return nil, ResultUncertain()
	}
	// Preparation's read-only Git checks have their own session process owner.
	// Prove its cleanup before creating a distinct execution-job owner scope.
	if err := process.ReconcileOwnerContext(ctx, m.Git.ProcessRoot, input.SessionID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	ownerDirectory := filepath.Join(m.Git.ProcessRoot, string(jobID))
	if _, err := os.Lstat(ownerDirectory); !errors.Is(err, os.ErrNotExist) {
		return nil, ResultUncertain()
	}
	if previous != nil {
		if err := m.retainClosedExecutionClaim(prior); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if err := security.PrivateDir(ownerDirectory); err != nil {
		return nil, ResultUncertain()
	}
	if err := security.SyncParent(ownerDirectory); err != nil {
		return nil, ResultUncertain()
	}
	claim := executionClaim{Version: 2, SessionID: input.SessionID, JobID: jobID, ExecutionID: executionID, ManifestDigest: manifestDigest, WorkspaceDigest: identityDigest, State: executionClaimActive}
	if previous != nil {
		claim.PreviousJobID, claim.PreviousExecutionID = previous.JobID, previous.ExecutionID
	}
	raw, err := json.Marshal(claim)
	if err != nil || security.WriteAtomic(m.executionClaimPath(input.SessionID), raw) != nil {
		return nil, ResultUncertain()
	}
	m.Logger.InfoContext(ctx, "workspace_execution_claimed", "session_id", input.SessionID, "job_id", jobID, "execution_id", executionID, "continuation", previous != nil)
	return &ExecutionLease{manager: m, claim: claim, cwd: manifest.PrimaryPath, release: lock.Close}, nil
}

func (l *ExecutionLease) WorkingDirectory() string { return l.cwd }

// Close must follow the native client's own Close/join. It independently checks
// every indexed process scope before recording cleanup and unlocking. Unproven
// cleanup leaves the durable claim active after the OS lock is released. A
// synchronization failure remains uncertain even if its closed write landed;
// neither case authorizes repeating the first execution.
func (l *ExecutionLease) Close() error {
	l.once.Do(func() {
		defer func() {
			if err := l.release(); err != nil {
				l.closeError = ResultUncertain()
			}
			if l.closeError != nil {
				l.manager.Logger.Warn("workspace_execution_cleanup_uncertain", "session_id", l.claim.SessionID, "job_id", l.claim.JobID, "execution_id", l.claim.ExecutionID, "code", domain.SafeError(l.closeError).Code)
			} else {
				l.manager.Logger.Info("workspace_execution_closed", "session_id", l.claim.SessionID, "job_id", l.claim.JobID, "execution_id", l.claim.ExecutionID)
			}
		}()
		retained, err := l.manager.readExecutionClaim(l.claim.SessionID)
		if err != nil || retained != l.claim {
			l.closeError = ResultUncertain()
			return
		}
		bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := process.ReconcileOwnerContext(bounded, l.manager.Git.ProcessRoot, l.claim.JobID); err != nil {
			l.closeError = ResultUncertain()
			return
		}
		closed := l.claim
		closed.State = executionClaimClosed
		raw, err := json.Marshal(closed)
		if err != nil || security.WriteAtomic(l.manager.executionClaimPath(l.claim.SessionID), raw) != nil {
			l.closeError = ResultUncertain()
		}
	})
	return l.closeError
}

func (m *Manager) executionHistoryPath(session, execution domain.ID) string {
	return filepath.Join(m.Root, "execution-history", string(session), string(execution)+".json")
}

func (m *Manager) retainClosedExecutionClaim(claim executionClaim) error {
	if claim.State != executionClaimClosed {
		return ResultUncertain()
	}
	path := m.executionHistoryPath(claim.SessionID, claim.ExecutionID)
	if err := security.PrivateDir(filepath.Dir(path)); err != nil {
		return ResultUncertain()
	}
	// Sync the new history directory itself before the current owner can move.
	if err := security.SyncParent(filepath.Dir(path)); err != nil {
		return ResultUncertain()
	}
	raw, err := json.Marshal(claim)
	if err != nil {
		return ResultUncertain()
	}
	existing, err := security.ReadPrivate(path, 4096)
	if err == nil {
		if !bytes.Equal(existing, raw) {
			return ResultUncertain()
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) || security.WriteAtomic(path, raw) != nil {
		return ResultUncertain()
	}
	return nil
}

// A lost latest claim cannot erase the fact that this workspace executed. Read
// at most one history entry, regardless of the number of retained turns.
func (m *Manager) requireNoExecutionHistory(session domain.ID) error {
	path := filepath.Join(m.Root, "execution-history", string(session))
	if err := security.CheckPrivateDir(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return ResultUncertain()
	}
	directory, err := os.Open(path)
	if err != nil {
		return ResultUncertain()
	}
	defer directory.Close()
	names, err := directory.Readdirnames(1)
	if len(names) == 0 && errors.Is(err, io.EOF) {
		return nil
	}
	return ResultUncertain()
}
