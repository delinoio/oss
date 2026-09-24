package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type RepositorySpec struct {
	ID              domain.ID        `json:"id"`
	Checkout        string           `json:"checkout"`
	PreferredRemote string           `json:"preferred_remote,omitempty"`
	Base            domain.Reference `json:"base"`
	Starting        domain.Reference `json:"starting"`
	AutoFetch       bool             `json:"auto_fetch"`
}
type PrepareRequest struct {
	SessionID         domain.ID            `json:"session_id"`
	MachineID         domain.ID            `json:"machine_id"`
	OriginMachineID   domain.ID            `json:"origin_machine_id,omitempty"`
	Type              domain.WorkspaceType `json:"type"`
	Repositories      []RepositorySpec     `json:"repositories"`
	PrimaryRepository domain.ID            `json:"primary_repository,omitempty"`
}
type PreparedRepository struct {
	LocalIdentityDigest string           `json:"local_identity_digest,omitempty"`
	ID                  domain.ID        `json:"id"`
	Source              string           `json:"source"`
	Path                string           `json:"path"`
	Base                domain.Reference `json:"base"`
	Starting            domain.Reference `json:"starting"`
	BaseCommit          string           `json:"base_commit"`
	StartingCommit      string           `json:"starting_commit"`
	Owned               bool             `json:"owned"`
}
type State string

const (
	Preparing      State = "preparing"
	Ready          State = "ready"
	CleanupPending State = "cleanup-pending"
)

type Manifest struct {
	Version      int                  `json:"version"`
	SessionID    domain.ID            `json:"session_id"`
	MachineID    domain.ID            `json:"machine_id"`
	Type         domain.WorkspaceType `json:"type"`
	State        State                `json:"state"`
	InputDigest  string               `json:"input_digest"`
	PrimaryPath  string               `json:"primary_path"`
	Repositories []PreparedRepository `json:"repositories"`
	CreatedAt    time.Time            `json:"created_at"`
}
type Manager struct {
	mu          sync.Mutex
	initialized bool
	Root        string
	Git         Git
	Logger      *slog.Logger
}

func (m *Manager) initialize() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initialized {
		return nil
	}
	if err := security.PrivateDir(m.Root); err != nil {
		return err
	}
	// Git records canonical paths (for example /private/var on macOS). Keep
	// ownership journals and registration comparisons in that same namespace.
	canonical, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		return err
	}
	m.Root = canonical
	for _, name := range []string{"workspaces", "locks", "empty-hooks", "processes", "execution-claims", "execution-history"} {
		if err := security.PrivateDir(filepath.Join(m.Root, name)); err != nil {
			return err
		}
	}
	m.Git.HooksDir = filepath.Join(m.Root, "empty-hooks")
	if m.Logger == nil {
		m.Logger = slog.Default()
	}
	m.Git.ProcessRoot = filepath.Join(m.Root, "processes")
	m.Git.Logger = m.Logger
	m.initialized = true
	return nil
}
func (r PrepareRequest) validate() error {
	if err := r.SessionID.Validate(); err != nil {
		return err
	}
	if err := r.MachineID.Validate(); err != nil {
		return err
	}
	if !r.Type.Valid() {
		return domain.Fail(domain.InvalidArgument, "Unknown workspace type.", "Select worktree, local, or general-chat.")
	}
	if r.Type == domain.Local && r.OriginMachineID != r.MachineID {
		return domain.Fail(domain.PermissionDenied, "Local workspaces are restricted to the originating computer.", "Choose Worktree for a remote execution machine.")
	}
	if r.Type == domain.GeneralChat {
		if len(r.Repositories) > 0 || r.PrimaryRepository != "" {
			return domain.Fail(domain.InvalidArgument, "General Chat cannot select project repositories.", "Use an isolated projectless working directory.")
		}
		return nil
	}
	if len(r.Repositories) == 0 || len(r.Repositories) > 100 {
		return domain.Fail(domain.InvalidArgument, "Invalid project repository count.", "Configure 1 through 100 repositories.")
	}
	ids := []domain.ID{}
	primary := false
	for _, repo := range r.Repositories {
		ids = append(ids, repo.ID)
		if repo.ID == r.PrimaryRepository {
			primary = true
		}
		if !filepath.IsAbs(repo.Checkout) {
			return domain.Fail(domain.InvalidArgument, "A checkout path must be absolute on this Worker.", "Use Worker repository inspection.")
		}
	}
	if err := domain.UniqueIDs(ids); err != nil {
		return err
	}
	if !primary {
		return domain.Fail(domain.InvalidArgument, "The primary repository is missing.", "Select one of the project's ordered repositories.")
	}
	return nil
}
func (m *Manager) Prepare(ctx context.Context, request PrepareRequest) (Manifest, error) {
	if err := request.validate(); err != nil {
		return Manifest{}, err
	}
	// Failure to inspect an existing scope or persist its ownership journal does
	// not prove cleanup. Only the failed() path below can certify that an attempt
	// with side effects was removed; callers must retain uncertainty otherwise.
	uncertain := func(cause error) (Manifest, error) {
		logger := m.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("workspace_preparation_uncertain", "session_id", request.SessionID, "machine_id", request.MachineID, "code", domain.SafeError(cause).Code)
		return Manifest{}, ResultUncertain()
	}
	if err := m.initialize(); err != nil {
		return uncertain(err)
	}
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(request.SessionID)+".lock"))
	if err != nil {
		return uncertain(err)
	}
	defer lock.Close()
	if err := m.noActiveExecutionClaim(request.SessionID); err != nil {
		return uncertain(err)
	}
	git := m.Git
	git.OwnerID = request.SessionID
	if err := security.PrivateDir(filepath.Join(git.ProcessRoot, string(request.SessionID))); err != nil {
		return uncertain(err)
	}
	raw, _ := json.Marshal(request)
	digestBytes := sha256.Sum256(raw)
	digest := hex.EncodeToString(digestBytes[:])
	root := filepath.Join(m.Root, "workspaces", string(request.SessionID))
	manifestPath := filepath.Join(root, "manifest.json")
	if _, err := os.Lstat(root); err == nil {
		old, err := m.Read(request.SessionID)
		if err != nil {
			return uncertain(err)
		}
		if old.InputDigest != digest {
			return Manifest{}, domain.Fail(domain.RecoveryRequired, "This session already owns a different workspace preparation.", "Inspect its recorded workspace; never overwrite an existing checkout.")
		}
		if old.State == Ready {
			if err := m.verify(old); err != nil {
				return old, err
			}
			return old, nil
		}
		return old, domain.Fail(domain.RecoveryRequired, "An earlier workspace preparation requires cleanup.", "Inspect and retry cleanup before preparing this session again.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return uncertain(err)
	}
	if err := security.PrivateDir(root); err != nil {
		return uncertain(err)
	}
	manifest := Manifest{Version: 1, SessionID: request.SessionID, MachineID: request.MachineID, Type: request.Type, State: Preparing, InputDigest: digest, Repositories: []PreparedRepository{}, CreatedAt: time.Now().UTC()}
	write := func() error {
		raw, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		return security.WriteAtomic(manifestPath, raw)
	}
	if err := write(); err != nil {
		return uncertain(err)
	}
	m.Logger.Info("workspace_preparation_started", "session_id", request.SessionID, "machine_id", request.MachineID, "workspace_type", request.Type)
	failed := func(cause error) (Manifest, error) {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		manifest.State = CleanupPending
		if err := write(); err != nil {
			m.Logger.Error("workspace_cleanup_journal_failed", "session_id", request.SessionID, "code", domain.SafeError(err).Code)
			return manifest, domain.Fail(domain.RecoveryRequired, "Workspace cleanup could not be journaled.", "Preserve the Worker workspace and retry recovery.")
		}
		if domain.SafeError(cause).Code == domain.RecoveryRequired {
			return manifest, cause
		}
		if err := m.cleanup(cleanup, root, manifest); err != nil {
			m.Logger.Warn("workspace_cleanup_pending", "session_id", request.SessionID, "code", domain.SafeError(err).Code)
			return manifest, domain.Fail(domain.RecoveryRequired, "Workspace preparation failed and cleanup remains pending.", "Retry cleanup on this Worker; existing Local checkouts were preserved.")
		}
		m.Logger.Warn("workspace_preparation_failed", "session_id", request.SessionID, "code", domain.SafeError(cause).Code)
		return Manifest{}, cause
	}
	if request.Type == domain.GeneralChat {
		manifest.PrimaryPath = filepath.Join(root, "chat")
		if err := security.PrivateDir(manifest.PrimaryPath); err != nil {
			return failed(domain.SafeError(err))
		}
	} else {
		for _, spec := range request.Repositories {
			if err := ctx.Err(); err != nil {
				return failed(domain.SafeError(err))
			}
			inspection, err := git.Inspect(ctx, spec.Checkout)
			if err != nil {
				return failed(err)
			}
			prepared := PreparedRepository{ID: spec.ID, Source: inspection.Root, Base: spec.Base, Starting: spec.Starting, Owned: request.Type == domain.Worktree}
			if request.Type == domain.Local {
				// Local means exactly the existing checkout. Do not fetch, select a new
				// starting branch, or prepare a replacement tree for any repository.
				raw, err := git.run(ctx, inspection.Root, "rev-parse", "--verify", "HEAD^{commit}")
				if err != nil {
					return failed(err)
				}
				prepared.Path = inspection.Root
				prepared.StartingCommit = trimGit(raw)
				prepared.BaseCommit = prepared.StartingCommit
				prepared.Starting = domain.Reference{Type: domain.CommitReference, Name: prepared.StartingCommit}
				prepared.LocalIdentityDigest, err = captureLocalIdentity(ctx, git, prepared.ID, prepared.Path)
				if err != nil {
					return failed(err)
				}
			} else {
				if prepared.Starting.Type == "" {
					prepared.Starting, err = DefaultStarting(inspection, spec.PreferredRemote)
					if err != nil {
						return failed(err)
					}
				}
				prepared.StartingCommit, err = git.Resolve(ctx, inspection, prepared.Starting, spec.AutoFetch)
				if err != nil {
					return failed(err)
				}
				if prepared.Base.Type == "" {
					prepared.Base = prepared.Starting
				}
				if prepared.Base == prepared.Starting {
					prepared.BaseCommit = prepared.StartingCommit
				} else {
					prepared.BaseCommit, err = git.Resolve(ctx, inspection, prepared.Base, spec.AutoFetch)
					if err != nil {
						return failed(err)
					}
				}
				prepared.Path = filepath.Join(root, string(spec.ID))
				// Journal ownership before starting Git so a crash or partial worktree-add
				// can be reconciled without touching any original checkout.
				manifest.Repositories = append(manifest.Repositories, prepared)
				if err := write(); err != nil {
					return failed(domain.SafeError(err))
				}
				if _, err = git.run(ctx, inspection.Root, "worktree", "add", "--detach", "--", prepared.Path, prepared.StartingCommit); err != nil {
					return failed(err)
				}
			}
			if !prepared.Owned {
				manifest.Repositories = append(manifest.Repositories, prepared)
			}
			if spec.ID == request.PrimaryRepository {
				manifest.PrimaryPath = prepared.Path
			}
			if err := write(); err != nil {
				return failed(domain.SafeError(err))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return failed(domain.SafeError(err))
	}
	manifest.State = Ready
	if err := write(); err != nil {
		return failed(domain.SafeError(err))
	}
	m.Logger.Info("workspace_preparation_ready", "session_id", request.SessionID, "machine_id", request.MachineID)
	return manifest, nil
}
func trimGit(raw []byte) string {
	for len(raw) > 0 && (raw[len(raw)-1] == '\n' || raw[len(raw)-1] == '\r') {
		raw = raw[:len(raw)-1]
	}
	return string(raw)
}
func (m *Manager) Read(session domain.ID) (Manifest, error) {
	if err := session.Validate(); err != nil {
		return Manifest{}, err
	}
	root := filepath.Join(m.Root, "workspaces", string(session))
	if _, err := os.Lstat(root); err != nil {
		return Manifest{}, domain.Fail(domain.RecoveryRequired, "The workspace root is unavailable.", "Restore the recorded Worker workspace.")
	}
	if err := security.CheckPrivateDir(root); err != nil {
		return Manifest{}, domain.Fail(domain.RecoveryRequired, "The workspace root is not a private owned directory.", "Inspect the owning Worker without following a replacement link.")
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "manifest.json"), 1<<20)
	if err != nil {
		return Manifest{}, domain.Fail(domain.RecoveryRequired, "The workspace manifest is unavailable.", "Inspect the owning Worker's private workspace without overwriting files.")
	}
	var manifest Manifest
	if err := domain.Decode(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Version != 1 || manifest.SessionID != session {
		return Manifest{}, domain.Fail(domain.RecoveryRequired, "The workspace manifest identity is invalid.", "Preserve the workspace and restore its matching metadata.")
	}
	return manifest, nil
}
func (m *Manager) verify(manifest Manifest) error {
	if manifest.PrimaryPath == "" {
		return domain.Fail(domain.RecoveryRequired, "The prepared workspace lacks its primary directory.", "Inspect preparation recovery before execution.")
	}
	for _, repo := range manifest.Repositories {
		info, err := os.Stat(repo.Path)
		if err != nil || !info.IsDir() {
			return domain.Fail(domain.RecoveryRequired, "A prepared repository is unavailable.", "Restore its workspace on the owning Worker before resuming.")
		}
	}
	info, err := os.Stat(manifest.PrimaryPath)
	if err != nil || !info.IsDir() {
		return domain.Fail(domain.RecoveryRequired, "The primary workspace is unavailable.", "Restore the workspace on its owning Worker.")
	}
	return nil
}
func (m *Manager) cleanup(ctx context.Context, root string, manifest Manifest) error {
	if _, err := os.Lstat(root); err == nil {
		if err := security.CheckPrivateDir(root); err != nil {
			return ResultUncertain()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	git := m.Git
	git.OwnerID = manifest.SessionID
	if err := process.ReconcileOwnerContext(ctx, git.ProcessRoot, manifest.SessionID); err != nil {
		return err
	}
	for i := len(manifest.Repositories) - 1; i >= 0; i-- {
		repo := manifest.Repositories[i]
		if !repo.Owned {
			continue
		}
		// Paths are recomputed from typed identities, never trusted from a mutable
		// manifest when removing resources. Local source checkouts are never removed.
		expected := filepath.Join(root, string(repo.ID))
		if repo.ID.Validate() != nil || repo.Path != expected || repo.Source == expected {
			return domain.Fail(domain.RecoveryRequired, "Workspace cleanup ownership could not be verified.", "Inspect the exact repository and session association.")
		}
		if info, err := os.Lstat(expected); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return ResultUncertain()
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return ResultUncertain()
		}
		registered, err := git.run(ctx, repo.Source, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return err
		}
		present := false
		for _, field := range strings.Split(string(registered), "\x00") {
			if path, ok := strings.CutPrefix(field, "worktree "); ok && sameNativePath(path, expected) {
				present = true
				break
			}
		}
		if present {
			if _, err := git.run(ctx, repo.Source, "worktree", "remove", "--force", "--", expected); err != nil {
				return err
			}
		}
	}
	// This root contains only this attempt's journal and owned General Chat data;
	// no original Local checkout is contained in or removed with this directory.
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return security.SyncParent(root)
}
func (m *Manager) RetryCleanup(ctx context.Context, session domain.ID) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if err := m.initialize(); err != nil {
		return err
	}
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(session)+".lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	manifest, err := m.Read(session)
	if err != nil {
		return err
	}
	if manifest.State != CleanupPending && manifest.State != Preparing {
		return domain.Fail(domain.Conflict, "A ready workspace cannot be deleted through preparation recovery.", "Use explicit session deletion or snapshot-backed workspace cleanup.")
	}
	return m.cleanup(ctx, filepath.Join(m.Root, "workspaces", string(session)), manifest)
}

// WorkspaceRoots preserves prepared repository order independently of the
// designated primary cwd. Call only after validating this complete manifest.
func (m Manifest) WorkspaceRoots() []string {
	if len(m.Repositories) == 0 {
		return []string{m.PrimaryPath}
	}
	roots := make([]string, len(m.Repositories))
	for i, repository := range m.Repositories {
		roots[i] = repository.Path
	}
	return roots
}
