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
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type prStartupPhase string

const (
	prStartupChecking prStartupPhase = "checking"
	prStartupPassed   prStartupPhase = "passed"
	prStartupRejected prStartupPhase = "rejected"
)

// The journal establishes that this exact attempt entered only the pre-native
// phase. Missing execution logs or an arbitrary error cannot replace this fact.
type prStartupRecord struct {
	Version           uint32         `json:"version"`
	JobID             domain.ID      `json:"job_id"`
	ExecutionID       domain.ID      `json:"execution_id"`
	SessionID         domain.ID      `json:"session_id"`
	PreparationDigest string         `json:"preparation_digest"`
	ManifestDigest    string         `json:"manifest_digest"`
	TargetDigest      string         `json:"target_digest"`
	Phase             prStartupPhase `json:"phase"`
	Reason            domain.Code    `json:"reason,omitempty"`
	StartedAt         time.Time      `json:"started_at"`
	FinishedAt        *time.Time     `json:"finished_at,omitempty"`
}

func rejectedPRReason(code domain.Code) bool {
	switch code {
	case domain.Conflict, domain.MissingInput, domain.Unavailable, domain.Canceled, domain.ResourceExhausted, domain.InvalidArgument:
		return true
	default:
		return false
	}
}

func (v prStartupRecord) validate() error {
	if v.Version != 1 || domain.UniqueIDs([]domain.ID{v.JobID, v.ExecutionID, v.SessionID}) != nil || v.StartedAt.IsZero() {
		return ResultUncertain()
	}
	for _, value := range []string{v.PreparationDigest, v.ManifestDigest, v.TargetDigest} {
		if len(value) != 64 || !canonicalCommit(value) {
			return ResultUncertain()
		}
	}
	if v.Phase == prStartupChecking {
		if v.Reason != "" || v.FinishedAt != nil {
			return ResultUncertain()
		}
		return nil
	}
	if v.FinishedAt == nil || v.FinishedAt.Before(v.StartedAt) {
		return ResultUncertain()
	}
	if v.Phase == prStartupPassed && v.Reason == "" || v.Phase == prStartupRejected && rejectedPRReason(v.Reason) {
		return nil
	}
	return ResultUncertain()
}

type prStartupRejection struct{ record prStartupRecord }

func (e *prStartupRejection) Error() string { return e.Unwrap().Error() }
func (e *prStartupRejection) Unwrap() error {
	return domain.Fail(e.record.Reason, "The original PR startup was rejected before native execution.", "Inspect the retained preflight rejection and prepare a new authorized attempt; this execution identity cannot be replayed.")
}

type prStartupGate struct {
	manager *Manager
	path    string
	record  prStartupRecord
	raw     []byte
}

func (m *Manager) prStartupPath(session, execution domain.ID) string {
	return filepath.Join(m.Root, "pr-startup", string(session), string(execution)+".json")
}

func (m *Manager) beginPRStartup(job, execution domain.ID, input PrepareRequest, manifestDigest string) (*prStartupGate, error) {
	var target *domain.PRGitTarget
	for _, spec := range input.Repositories {
		if spec.PRTarget != nil {
			if target != nil {
				return nil, ResultUncertain()
			}
			target = spec.PRTarget
		}
	}
	if target == nil {
		return nil, nil
	}
	rawTarget, err := json.Marshal(target)
	if err != nil {
		return nil, ResultUncertain()
	}
	digest := sha256.Sum256(rawTarget)
	value := prStartupRecord{Version: 1, JobID: job, ExecutionID: execution, SessionID: input.SessionID, PreparationDigest: preparationDigest(input), ManifestDigest: manifestDigest, TargetDigest: hex.EncodeToString(digest[:]), Phase: prStartupChecking, StartedAt: time.Now().UTC()}
	if value.validate() != nil {
		return nil, ResultUncertain()
	}
	path := m.prStartupPath(input.SessionID, execution)
	if err := security.PrivateDir(filepath.Dir(path)); err != nil {
		return nil, ResultUncertain()
	}
	if err := security.SyncParent(filepath.Dir(path)); err != nil {
		return nil, ResultUncertain()
	}
	existing, err := security.ReadPrivate(path, 4096)
	if err == nil {
		var prior prStartupRecord
		if domain.Decode(existing, &prior) != nil || prior.validate() != nil || prior.JobID != job || prior.ExecutionID != execution || prior.SessionID != input.SessionID || prior.PreparationDigest != value.PreparationDigest || prior.ManifestDigest != value.ManifestDigest || prior.TargetDigest != value.TargetDigest {
			return nil, ResultUncertain()
		}
		if prior.Phase == prStartupRejected {
			if m.requireNoPRNativeEligibility(prior) != nil {
				return nil, ResultUncertain()
			}
			return nil, &prStartupRejection{record: prior}
		}
		return nil, ResultUncertain()
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, ResultUncertain()
	}
	if err := m.requireClosedPRStartups(input.SessionID, job); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil || security.WriteAtomic(path, raw) != nil {
		return nil, ResultUncertain()
	}
	return &prStartupGate{manager: m, path: path, record: value, raw: raw}, nil
}

// A different execution ID cannot bypass an interrupted pre-native phase, or
// reuse a job whose original attempt was already rejected. The session lock
// held by claimExecution serializes this bounded history with new writers.
func (m *Manager) requireClosedPRStartups(session, job domain.ID) error {
	directory := filepath.Dir(m.prStartupPath(session, job))
	if security.CheckPrivateDir(directory) != nil {
		return ResultUncertain()
	}
	index, err := os.Open(directory)
	if err != nil {
		return ResultUncertain()
	}
	defer index.Close()
	count := 0
	for {
		entries, err := index.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return ResultUncertain()
		}
		if len(entries) == 0 {
			return nil
		}
		for _, entry := range entries {
			count++
			if count >= domain.MaxPRRemediationAttempts {
				return domain.Fail(domain.ResourceExhausted, "The retained PR startup history is full.", "Preserve the original session history; do not remove records to retry an execution.")
			}
			id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
			if entry.IsDir() || entry.Name() != string(id)+".json" || id.Validate() != nil {
				return ResultUncertain()
			}
			raw, err := security.ReadPrivate(filepath.Join(directory, entry.Name()), 4096)
			var prior prStartupRecord
			if err != nil || domain.Decode(raw, &prior) != nil || prior.validate() != nil || prior.ExecutionID != id || prior.SessionID != session || prior.JobID == job || prior.Phase != prStartupRejected {
				return ResultUncertain()
			}
		}
	}
}

func (g *prStartupGate) finish(reason error) error {
	if g == nil {
		return reason
	}
	current, err := security.ReadPrivate(g.path, 4096)
	if err != nil || !bytes.Equal(current, g.raw) || g.record.Phase != prStartupChecking {
		return ResultUncertain()
	}
	if reason != nil && !rejectedPRReason(domain.SafeError(reason).Code) {
		return reason
	}
	// Cleanup belongs to the positively recorded pre-native phase. Verify the
	// original session process owner and retain checking on any uncertainty.
	bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := process.ReconcileOwnerContext(bounded, g.manager.Git.ProcessRoot, g.record.SessionID); err != nil {
		return ResultUncertain()
	}
	if g.manager.requireNoPRNativeEligibility(g.record) != nil {
		return ResultUncertain()
	}
	now := time.Now().UTC()
	value := g.record
	value.Phase = prStartupPassed
	value.FinishedAt = &now
	if reason != nil {
		value.Phase = prStartupRejected
		value.Reason = domain.SafeError(reason).Code
	}
	raw, err := json.Marshal(value)
	if err != nil || value.validate() != nil || security.WriteAtomic(g.path, raw) != nil {
		return ResultUncertain()
	}
	g.manager.Logger.Info("workspace_pr_startup_settled", "session_id", value.SessionID, "job_id", value.JobID, "execution_id", value.ExecutionID, "phase", value.Phase, "code", value.Reason)
	if reason != nil {
		return &prStartupRejection{record: value}
	}
	return nil
}

func (m *Manager) requireNoPRNativeEligibility(record prStartupRecord) error {
	for _, path := range []string{m.executionClaimPath(record.SessionID), filepath.Join(m.Git.ProcessRoot, string(record.JobID)), filepath.Join(m.Root, "runtimes", string(record.ExecutionID))} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return ResultUncertain()
		}
	}
	return nil
}
