package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type RecoveryAction string

const (
	InspectPreparation RecoveryAction = "inspect"
	CleanupPreparation RecoveryAction = "cleanup"
)

type RecoveryOutcome string

const (
	RecoveredReady RecoveryOutcome = "ready"
	RecoveredClean RecoveryOutcome = "clean"
)

type RecoveryRequest struct {
	JobID            domain.ID      `json:"job_id"`
	InstanceID       domain.ID      `json:"instance_id"`
	Revision         uint64         `json:"revision"`
	AssignmentDigest string         `json:"assignment_digest"`
	Preparation      PrepareRequest `json:"preparation"`
	Action           RecoveryAction `json:"action"`
}
type RecoveryResult struct {
	JobID       domain.ID       `json:"job_id"`
	InputDigest string          `json:"input_digest"`
	Outcome     RecoveryOutcome `json:"outcome"`
	Manifest    *Manifest       `json:"manifest,omitempty"`
}
type cleanupProof struct {
	Version  int       `json:"version"`
	JobID    domain.ID `json:"job_id"`
	Complete bool      `json:"complete"`
	Manifest Manifest  `json:"manifest"`
}

func preparationDigest(input PrepareRequest) string {
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (r RecoveryRequest) Validate() error {
	for _, id := range []domain.ID{r.JobID, r.InstanceID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if r.Revision == 0 || len(r.AssignmentDigest) != 64 || !canonicalCommit(r.AssignmentDigest) || (r.Action != InspectPreparation && r.Action != CleanupPreparation) {
		return ResultUncertain()
	}
	return r.Preparation.validate()
}
func ValidateRecoveryResult(input RecoveryRequest, result RecoveryResult, workerOS string) error {
	if result.JobID != input.JobID || result.InputDigest != preparationDigest(input.Preparation) {
		return ResultUncertain()
	}
	switch result.Outcome {
	case RecoveredClean:
		if result.Manifest != nil {
			return ResultUncertain()
		}
	case RecoveredReady:
		if result.Manifest == nil {
			return ResultUncertain()
		}
		return ValidateResult(input.Preparation, *result.Manifest, workerOS)
	default:
		return ResultUncertain()
	}
	return nil
}

// Recover never prepares or fetches. The caller first proves the old assignment
// against its private execution journal. completedClean is permitted only for a
// durable terminal failure/cancellation that already confirmed native cleanup.
func (m *Manager) Recover(ctx context.Context, input RecoveryRequest, completedClean bool) (RecoveryResult, error) {
	result := RecoveryResult{JobID: input.JobID, InputDigest: preparationDigest(input.Preparation)}
	if err := input.Validate(); err != nil {
		return result, err
	}
	if err := m.initialize(); err != nil {
		return result, ResultUncertain()
	}
	request := input.Preparation
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(request.SessionID)+".lock"))
	if err != nil {
		return result, ResultUncertain()
	}
	defer lock.Close()
	if err := m.noActiveExecutionClaim(request.SessionID); err != nil {
		return result, err
	}
	root := filepath.Join(m.Root, "workspaces", string(request.SessionID))
	processRoot := filepath.Join(m.Root, "processes")
	// A pre-start cancellation has no process index. Only its durable completed
	// journal can prove that this absence means no native operation was launched.
	if _, err := os.Lstat(filepath.Join(processRoot, string(request.SessionID))); err != nil {
		if !errors.Is(err, os.ErrNotExist) || !completedClean {
			return result, ResultUncertain()
		}
	} else if err := process.ReconcileOwnerContext(ctx, processRoot, request.SessionID); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	proofRoot := filepath.Join(m.Root, "workspace-recovery")
	if err := security.PrivateDir(proofRoot); err != nil {
		return result, ResultUncertain()
	}
	proofPath := filepath.Join(proofRoot, string(input.JobID)+".json")
	var proof cleanupProof
	raw, proofErr := security.ReadPrivate(proofPath, 1<<20)
	if proofErr == nil {
		if domain.Decode(raw, &proof) != nil || proof.Version != 1 || proof.JobID != input.JobID || m.validatePartial(request, proof.Manifest) != nil {
			return result, ResultUncertain()
		}
	} else if !errors.Is(proofErr, os.ErrNotExist) {
		return result, ResultUncertain()
	}
	_, rootErr := os.Lstat(root)
	if rootErr != nil && !errors.Is(rootErr, os.ErrNotExist) {
		return result, ResultUncertain()
	}
	if errors.Is(rootErr, os.ErrNotExist) {
		if completedClean || (proofErr == nil && proof.Complete) {
			result.Outcome = RecoveredClean
			return result, nil
		}
		if proofErr != nil || input.Action != CleanupPreparation {
			return result, ResultUncertain()
		}
	} else {
		manifest, err := m.Read(request.SessionID)
		if err != nil {
			return result, ResultUncertain()
		}
		if manifest.State == Ready {
			if err := m.verifyRecoveredReady(ctx, request, manifest); err != nil {
				return result, err
			}
			result.Outcome, result.Manifest = RecoveredReady, &manifest
			m.Logger.Info("workspace_recovered_ready", "session_id", request.SessionID, "job_id", input.JobID)
			return result, nil
		}
		if err := m.validatePartial(request, manifest); err != nil {
			return result, err
		}
		if input.Action != CleanupPreparation {
			return result, domain.Fail(domain.RecoveryRequired, "Workspace preparation is incomplete and requires explicit cleanup.", "Inspect the retained scope, then request workspace recovery with cleanup.")
		}
		if proofErr == nil {
			original, _ := json.Marshal(proof.Manifest)
			current, _ := json.Marshal(manifest)
			if proof.Complete || string(original) != string(current) {
				return result, ResultUncertain()
			}
		} else {
			proof = cleanupProof{Version: 1, JobID: input.JobID, Manifest: manifest}
		}
	}
	// Retain the original ownership manifest outside the removed tree. If the
	// process dies between removal and completion publication, another explicit
	// cleanup can recheck Git registrations and finish this exact attempt.
	if err := writeCleanupProof(proofPath, proof); err != nil {
		return result, err
	}
	if err := m.cleanup(ctx, root, proof.Manifest); err != nil {
		return result, err
	}
	proof.Complete = true
	if err := writeCleanupProof(proofPath, proof); err != nil {
		return result, err
	}
	result.Outcome = RecoveredClean
	m.Logger.Info("workspace_recovered_clean", "session_id", request.SessionID, "job_id", input.JobID)
	return result, nil
}
func writeCleanupProof(path string, proof cleanupProof) error {
	raw, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	return security.WriteAtomic(path, raw)
}
func (m *Manager) validatePartial(input PrepareRequest, manifest Manifest) error {
	if manifest.Version != 1 || manifest.SessionID != input.SessionID || manifest.MachineID != input.MachineID || manifest.Type != input.Type || manifest.InputDigest != preparationDigest(input) || (manifest.State != Preparing && manifest.State != CleanupPending) || len(manifest.Repositories) > len(input.Repositories) {
		return ResultUncertain()
	}
	root := filepath.Join(m.Root, "workspaces", string(input.SessionID))
	if input.Type == domain.Local {
		return domain.Fail(domain.Unsupported, "Local preparation recovery is not available through this operation.", "Preserve all original checkouts.")
	}
	if input.Type == domain.GeneralChat {
		if len(manifest.Repositories) != 0 || (manifest.PrimaryPath != "" && manifest.PrimaryPath != filepath.Join(root, "chat")) {
			return ResultUncertain()
		}
		return nil
	}
	primaryFound := manifest.PrimaryPath == ""
	for i, repo := range manifest.Repositories {
		expected := input.Repositories[i]
		if repo.ID != expected.ID || repo.Source != expected.Checkout || repo.Path != filepath.Join(root, string(repo.ID)) || !repo.Owned || repo.Source == repo.Path || !canonicalCommit(repo.StartingCommit) || !canonicalCommit(repo.BaseCommit) {
			return ResultUncertain()
		}
		if repo.ID == input.PrimaryRepository && manifest.PrimaryPath == repo.Path {
			primaryFound = true
		}
	}
	if !primaryFound {
		return ResultUncertain()
	}
	return nil
}
func (m *Manager) verifyRecoveredReady(ctx context.Context, input PrepareRequest, manifest Manifest) error {
	if err := ValidateResult(input, manifest, runtime.GOOS); err != nil {
		return err
	}
	root := filepath.Join(m.Root, "workspaces", string(input.SessionID))
	paths := []string{manifest.PrimaryPath}
	for _, repo := range manifest.Repositories {
		paths = append(paths, repo.Path)
	}
	for _, path := range paths {
		// Ready workspaces must still be at their exact owned locations. Do not
		// follow replacement symlinks or accept another Worker's matching suffix.
		if input.Type == domain.GeneralChat {
			if path != filepath.Join(root, "chat") {
				return ResultUncertain()
			}
		} else if filepath.Dir(path) != root {
			return ResultUncertain()
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ResultUncertain()
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != path {
			return ResultUncertain()
		}
	}
	git := m.Git
	git.OwnerID = input.SessionID
	for _, repo := range manifest.Repositories {
		head, err := git.run(ctx, repo.Path, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || trimGit(head) != repo.StartingCommit {
			return ResultUncertain()
		}
		branch, err := git.run(ctx, repo.Path, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || trimGit(branch) != "HEAD" {
			return ResultUncertain()
		}
		source, err := git.run(ctx, repo.Source, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return ResultUncertain()
		}
		prepared, err := git.run(ctx, repo.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || trimGit(source) != trimGit(prepared) {
			return ResultUncertain()
		}
		registered, err := git.run(ctx, repo.Source, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return ResultUncertain()
		}
		found := false
		for _, field := range strings.Split(string(registered), "\x00") {
			if path, ok := strings.CutPrefix(field, "worktree "); ok && sameNativePath(path, repo.Path) {
				found = true
			}
		}
		if !found {
			return ResultUncertain()
		}
	}
	return nil
}
