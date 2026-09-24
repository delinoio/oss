package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// ClosedExecutionInspection holds existing workspace ownership while a caller
// checks retained completion evidence. Closing only releases the lock; unlike
// an execution lease it cannot create, advance or rewrite a cleanup claim.
type ClosedExecutionInspection struct {
	once    sync.Once
	release func() error
	err     error
	cwd     string
}

func (i *ClosedExecutionInspection) WorkingDirectory() string { return i.cwd }
func (i *ClosedExecutionInspection) Close() error {
	i.once.Do(func() { i.err = i.release() })
	return i.err
}

// InspectClosedExecution never starts a harness, fetches, prepares a replacement
// workspace or repairs an absent claim. It requires the current closed owner;
// retired history and an absent process index cannot stand in for that owner.
func (m *Manager) InspectClosedExecution(ctx context.Context, expected ExecutionPredecessor, input PrepareRequest, manifest Manifest) (inspection *ClosedExecutionInspection, returned error) {
	if domain.UniqueIDs([]domain.ID{input.SessionID, expected.JobID, expected.ExecutionID}) != nil || input.validate() != nil || input.Type == domain.Local {
		return nil, ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if security.CheckPrivateDir(m.Root) != nil || m.initialize() != nil {
		return nil, ResultUncertain()
	}
	defer func() {
		if returned != nil {
			m.Logger.WarnContext(ctx, "workspace_closed_execution_inspection_failed", "session_id", input.SessionID, "job_id", expected.JobID, "execution_id", expected.ExecutionID, "code", domain.SafeError(returned).Code)
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
	claim, err := m.readExecutionClaim(input.SessionID)
	if err != nil || claim.Version != 2 || claim.State != executionClaimClosed || claim.JobID != expected.JobID || claim.ExecutionID != expected.ExecutionID {
		return nil, ResultUncertain()
	}
	retained, err := m.Read(input.SessionID)
	if err != nil {
		return nil, ResultUncertain()
	}
	actual, err := json.Marshal(retained)
	if err != nil {
		return nil, ResultUncertain()
	}
	wanted, err := json.Marshal(manifest)
	digest := sha256.Sum256(actual)
	if err != nil || !bytes.Equal(actual, wanted) || claim.ManifestDigest != hex.EncodeToString(digest[:]) {
		return nil, ResultUncertain()
	}
	// Git inspection launches its own owned read-only subprocesses. Require
	// both original owner indexes first so those launches cannot recreate a
	// missing preparation index and hide its lost process evidence.
	for _, owner := range []domain.ID{claim.JobID, input.SessionID} {
		if security.CheckPrivateDir(filepath.Join(m.Git.ProcessRoot, string(owner))) != nil {
			return nil, ResultUncertain()
		}
	}
	// Continuation identity permits retained agent commits and dirty files under
	// the same repository ownership. Recovery never resets these user results.
	identity, err := m.verifyWorkspaceIdentity(ctx, input, retained, continuationIdentity)
	if err != nil || claim.WorkspaceDigest != identity {
		return nil, ResultUncertain()
	}
	for _, owner := range []domain.ID{claim.JobID, input.SessionID} {
		if err := process.ReconcileOwnerContext(ctx, m.Git.ProcessRoot, owner); err != nil {
			return nil, err
		}
	}
	fresh, err := m.readExecutionClaim(input.SessionID)
	if err != nil || fresh != claim {
		return nil, ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	m.Logger.InfoContext(ctx, "workspace_closed_execution_inspected", "session_id", input.SessionID, "job_id", expected.JobID, "execution_id", expected.ExecutionID)
	return &ClosedExecutionInspection{release: lock.Close, cwd: retained.PrimaryPath}, nil
}
