package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Version        uint32              `json:"version"`
	SessionID      domain.ID           `json:"session_id"`
	JobID          domain.ID           `json:"job_id"`
	ExecutionID    domain.ID           `json:"execution_id"`
	ManifestDigest string              `json:"manifest_digest"`
	State          executionClaimState `json:"state"`
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
	if domain.Decode(raw, &claim) != nil || claim.Version != 1 || claim.SessionID != session || claim.JobID.Validate() != nil || claim.ExecutionID.Validate() != nil || len(claim.ManifestDigest) != 64 || !canonicalCommit(claim.ManifestDigest) || (claim.State != executionClaimActive && claim.State != executionClaimClosed) {
		return claim, ResultUncertain()
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
// reused: subsequent turns/resume need a separate native-history contract that
// permits agent commits and proves the prior execution's terminal boundary.
func (m *Manager) ClaimFirstExecution(ctx context.Context, jobID, executionID domain.ID, input PrepareRequest, expected Manifest) (lease *ExecutionLease, returned error) {
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
	if err == nil {
		if prior.State == executionClaimClosed {
			return nil, domain.Fail(domain.Conflict, "This workspace already has a retained first execution.", "Reconcile its native history before explicit Resume; never repeat the initial input.")
		}
		return nil, ResultUncertain()
	}
	if !errors.Is(err, os.ErrNotExist) {
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
	if err := m.verifyRecoveredReady(ctx, input, manifest); err != nil {
		return nil, err
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
	if err := security.PrivateDir(ownerDirectory); err != nil {
		return nil, ResultUncertain()
	}
	if err := security.SyncParent(ownerDirectory); err != nil {
		return nil, ResultUncertain()
	}
	digest := sha256.Sum256(actual)
	claim := executionClaim{Version: 1, SessionID: input.SessionID, JobID: jobID, ExecutionID: executionID, ManifestDigest: hex.EncodeToString(digest[:]), State: executionClaimActive}
	raw, err := json.Marshal(claim)
	if err != nil || security.WriteAtomic(m.executionClaimPath(input.SessionID), raw) != nil {
		return nil, ResultUncertain()
	}
	m.Logger.InfoContext(ctx, "workspace_execution_claimed", "session_id", input.SessionID, "job_id", jobID, "execution_id", executionID)
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
