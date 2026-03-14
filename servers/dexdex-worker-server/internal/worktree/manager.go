package worktree

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// WorktreeContext holds the paths and metadata for a prepared worktree session.
type WorktreeContext struct {
	PrimaryDir   string   // First repo worktree path (agent CWD)
	AttachedDirs []string // Additional repo worktree paths for --add-dir
	SessionID    string
	SessionDir   string // Parent dir containing all worktrees for this session
}

// Manager handles git worktree lifecycle for task execution isolation.
type Manager struct {
	worktreeRoot  string
	repoCacheRoot string
	semaphore     chan struct{} // bounded concurrency
	logger        *slog.Logger
}

// NewManager creates a new worktree manager.
func NewManager(worktreeRoot, repoCacheRoot string, maxParallel int, logger *slog.Logger) *Manager {
	return &Manager{
		worktreeRoot:  worktreeRoot,
		repoCacheRoot: repoCacheRoot,
		semaphore:     make(chan struct{}, maxParallel),
		logger:        logger,
	}
}

// PrepareWorktree clones/fetches repos and creates worktrees for the given session.
// First repository in the group becomes the primary directory.
func (m *Manager) PrepareWorktree(ctx context.Context, repoGroup *dexdexv1.RepositoryGroup, sessionID string) (*WorktreeContext, error) {
	if repoGroup == nil || len(repoGroup.Members) == 0 {
		return nil, fmt.Errorf("repository_group must include at least one member")
	}

	// Acquire semaphore slot
	select {
	case m.semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	sessionDir := filepath.Join(m.worktreeRoot, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		<-m.semaphore
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	wCtx := &WorktreeContext{
		SessionID:  sessionID,
		SessionDir: sessionDir,
	}

	for i, member := range repoGroup.Members {
		if member.Repository == nil {
			m.CleanupWorktree(ctx, wCtx) //nolint:errcheck
			<-m.semaphore
			return nil, fmt.Errorf("repository_group member %d missing repository payload", i)
		}
		repositoryURL := member.Repository.RepositoryUrl
		if repositoryURL == "" {
			m.CleanupWorktree(ctx, wCtx) //nolint:errcheck
			<-m.semaphore
			return nil, fmt.Errorf("repository_group member %d missing repository_url", i)
		}

		repoName := repoNameFromURL(repositoryURL)
		cachePath := filepath.Join(m.repoCacheRoot, repoCacheKey(repositoryURL))
		worktreePath := filepath.Join(sessionDir, repoName)

		m.logger.Info("preparing worktree",
			"session_id", sessionID,
			"repo_url", repositoryURL,
			"branch", member.BranchRef,
			"index", i,
		)

		// Ensure bare clone exists and is up-to-date
		if err := m.ensureBareClone(ctx, repositoryURL, cachePath); err != nil {
			m.CleanupWorktree(ctx, wCtx) //nolint:errcheck
			<-m.semaphore
			return nil, fmt.Errorf("ensure bare clone for %s: %w", repositoryURL, err)
		}

		// Create worktree
		branch := member.BranchRef
		if branch == "" {
			branch = "HEAD"
		}
		if err := m.createWorktree(ctx, cachePath, worktreePath, branch); err != nil {
			m.CleanupWorktree(ctx, wCtx) //nolint:errcheck
			<-m.semaphore
			return nil, fmt.Errorf("create worktree for %s: %w", repositoryURL, err)
		}

		if i == 0 {
			wCtx.PrimaryDir = worktreePath
		} else {
			wCtx.AttachedDirs = append(wCtx.AttachedDirs, worktreePath)
		}
	}

	m.logger.Info("worktree prepared",
		"session_id", sessionID,
		"primary_dir", wCtx.PrimaryDir,
		"attached_count", len(wCtx.AttachedDirs),
	)

	return wCtx, nil
}

// CleanupWorktree removes worktrees and the session directory, then releases the semaphore.
func (m *Manager) CleanupWorktree(ctx context.Context, wCtx *WorktreeContext) error {
	if wCtx == nil || wCtx.SessionDir == "" {
		return nil
	}

	m.logger.Info("cleaning up worktree", "session_id", wCtx.SessionID, "session_dir", wCtx.SessionDir)

	// Remove worktrees from git
	allDirs := make([]string, 0, 1+len(wCtx.AttachedDirs))
	if wCtx.PrimaryDir != "" {
		allDirs = append(allDirs, wCtx.PrimaryDir)
	}
	allDirs = append(allDirs, wCtx.AttachedDirs...)

	for _, dir := range allDirs {
		// Find the parent bare repo for this worktree by checking .git file
		if _, err := os.Stat(dir); err == nil {
			cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				m.logger.Warn("failed to remove git worktree",
					"dir", dir, "error", err, "output", string(out))
			}
		}
	}

	// Remove session directory
	if err := os.RemoveAll(wCtx.SessionDir); err != nil {
		m.logger.Warn("failed to remove session dir", "dir", wCtx.SessionDir, "error", err)
		return err
	}

	return nil
}

// ReleaseSlot releases the concurrency semaphore slot for this worktree.
// Must be called after CleanupWorktree or when the session is done.
func (m *Manager) ReleaseSlot() {
	<-m.semaphore
}

// CleanupStale removes orphaned worktree directories older than the given timeout.
func (m *Manager) CleanupStale(ctx context.Context, maxAge time.Duration) {
	entries, err := os.ReadDir(m.worktreeRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		m.logger.Warn("failed to read worktree root for stale cleanup", "error", err)
		return
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			dir := filepath.Join(m.worktreeRoot, entry.Name())
			m.logger.Info("cleaning up stale worktree", "dir", dir, "mod_time", info.ModTime())
			if err := os.RemoveAll(dir); err != nil {
				m.logger.Warn("failed to remove stale worktree", "dir", dir, "error", err)
			}
		}
	}
}

func (m *Manager) ensureBareClone(ctx context.Context, repoURL, cachePath string) error {
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		m.logger.Info("cloning bare repo", "url", repoURL, "cache_path", cachePath)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--bare", repoURL, cachePath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone --bare: %s: %w", string(out), err)
		}
	} else {
		m.logger.Info("fetching updates for bare repo", "url", repoURL, "cache_path", cachePath)
		cmd := exec.CommandContext(ctx, "git", "-C", cachePath, "fetch", "--all", "--prune")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git fetch: %s: %w", string(out), err)
		}
	}
	return nil
}

func (m *Manager) createWorktree(ctx context.Context, barePath, worktreePath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", barePath, "worktree", "add", worktreePath, branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s: %w", string(out), err)
	}
	return nil
}

// repoCacheKey returns a stable hash-based directory name for a repository URL.
func repoCacheKey(repoURL string) string {
	h := sha256.Sum256([]byte(repoURL))
	return fmt.Sprintf("%x", h[:8])
}

// repoNameFromURL extracts a directory-safe name from a repository URL.
func repoNameFromURL(repoURL string) string {
	// Strip trailing .git
	name := strings.TrimSuffix(repoURL, ".git")
	// Take last path segment
	parts := strings.Split(name, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "repo"
}
