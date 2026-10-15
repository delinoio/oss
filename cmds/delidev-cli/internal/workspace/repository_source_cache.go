// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// The repository source cache is a Worker-private bare Git store per validated
// repository source identity. It only seeds the first object/ref copy of a new
// session clone. A session never depends on the cache afterwards: its clone is
// copied without hardlinks or alternates, keeps its own pinned source URL and
// performs every later fetch against that URL. The workspace contract owns the
// state table that the cell constants below implement.
const (
	repositoryCacheDirectory      = "repository-cache"
	repositoryCacheJournalName    = "journal.json"
	repositoryCacheStoreName      = "store"
	repositoryCacheStagingPrefix  = "staging-"
	repositoryCacheJournalVersion = 1
	repositoryCacheMaxJournal     = 1 << 16
	repositoryCacheLockPoll       = 20 * time.Millisecond
	repositoryCacheCleanupTimeout = 30 * time.Second
)

type repositoryCacheState string

const (
	// RepositoryCacheFilling is journaled before any native effect of a first fill.
	repositoryCacheFilling repositoryCacheState = "filling"
	// RepositoryCachePublished is a complete verified store.
	repositoryCachePublished repositoryCacheState = "published"
	// RepositoryCacheUnverified is a store whose last mutation was not proven
	// complete; it needs the full connectivity check before reuse.
	repositoryCacheUnverified repositoryCacheState = "unverified"
)

type repositoryCacheStage string

const (
	repositoryCacheStageLock    repositoryCacheStage = "lock"
	repositoryCacheStageRecover repositoryCacheStage = "recover"
	repositoryCacheStageFill    repositoryCacheStage = "fill"
	repositoryCacheStageReuse   repositoryCacheStage = "reuse"
	repositoryCacheStageUpdate  repositoryCacheStage = "update"
	repositoryCacheStageClone   repositoryCacheStage = "clone"
)

// repositoryCacheJournal is the durable ownership record of one cache entry.
// It is written and synchronized before the native effect it describes.
// RootDigest is the directory identity of the staging directory while filling
// and of the published store afterwards; a rename keeps that identity.
type repositoryCacheJournal struct {
	Version        int                  `json:"version"`
	SourceIdentity string               `json:"source_identity"`
	State          repositoryCacheState `json:"state"`
	FillID         domain.ID            `json:"fill_id"`
	RootDigest     string               `json:"root_digest,omitempty"`
}

func (j repositoryCacheJournal) valid(identity string) bool {
	if j.Version != repositoryCacheJournalVersion || j.SourceIdentity != identity || j.FillID.Validate() != nil {
		return false
	}
	if j.RootDigest != "" && !digestValid(j.RootDigest) {
		return false
	}
	switch j.State {
	case repositoryCacheFilling, repositoryCachePublished, repositoryCacheUnverified:
		return true
	}
	return false
}

// repositoryCacheOwner is the deterministic process-journal owner of every
// native effect on one cache entry. It is never a session, so session cleanup
// cannot retire or remove these processes.
func repositoryCacheOwner(identity string) domain.ID {
	digest := sha256.Sum256([]byte("delidev.repository-cache.owner.v1\x00" + identity))
	var raw [16]byte
	copy(raw[:], digest[:16])
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	return domain.ID(uuid.UUID(raw).String())
}

type repositoryCacheLease struct {
	manager  *Manager
	git      Git
	identity string
	root     string
	dir      string
	store    string
	journal  repositoryCacheJournal
	lock     *security.Lock
	logger   *slog.Logger
	released bool
}

func (c *repositoryCacheLease) journalPath() string {
	return filepath.Join(c.dir, repositoryCacheJournalName)
}
func (c *repositoryCacheLease) stagingPath(fill domain.ID) string {
	return filepath.Join(c.dir, repositoryCacheStagingPrefix+string(fill))
}

// Path is the local bare store a session clone copies from. It is valid only
// while the lease holds the per-source lock.
func (c *repositoryCacheLease) Path() string { return c.store }

func (c *repositoryCacheLease) failure(ctx context.Context, stage repositoryCacheStage, err error) {
	safe := domain.SafeError(err)
	// Only closed classifications are logged: never a URL, credential or native text.
	c.logger.WarnContext(ctx, "workspace_repository_cache_failed", "stage", stage, "code", safe.Code, "cause", safe.Cause)
}

// lockRepositoryCache waits for one source's lock. Sources have separate lock
// files, so unrelated repositories never queue behind this key. The wait is
// bounded by the caller's repository deadline or cancellation.
func (m *Manager) lockRepositoryCache(ctx context.Context, identity string) (*security.Lock, error) {
	path := filepath.Join(m.Root, "locks", "repository-cache-"+identity+".lock")
	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		lock, err := security.TryLock(path)
		if err == nil {
			return lock, nil
		}
		if domain.SafeError(err).Code != domain.Conflict {
			return nil, ResultUncertain()
		}
		timer := time.NewTimer(repositoryCacheLockPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, domain.SafeError(ctx.Err())
		case <-timer.C:
		}
	}
}

// acquireRepositoryCache returns a lease on a verified cache entry for the
// spec's source identity, filling it on first use. The caller must release the
// lease after copying from Path.
func (m *Manager) acquireRepositoryCache(ctx context.Context, base Git, spec RepositorySpec, session domain.ID) (*repositoryCacheLease, error) {
	identity, err := domain.RepositoryCloneSourceIdentity(spec.RemoteURL)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(m.Root, repositoryCacheDirectory)
	c := &repositoryCacheLease{manager: m, identity: identity, root: root, dir: filepath.Join(root, identity)}
	c.store = filepath.Join(c.dir, repositoryCacheStoreName)
	c.logger = m.Logger.With("session_id", session, "repository_id", spec.ID, "source_identity", identity)
	lock, err := m.lockRepositoryCache(ctx, identity)
	if err != nil {
		c.failure(ctx, repositoryCacheStageLock, err)
		return nil, err
	}
	c.lock = lock
	acquired := false
	defer func() {
		if !acquired {
			c.lock.Close()
		}
	}()
	owner := repositoryCacheOwner(identity)
	base.OwnerID = owner
	if err := security.PrivateDir(c.dir); err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		return nil, ResultUncertain()
	}
	if err := security.PrivateDir(filepath.Join(base.ProcessRoot, string(owner))); err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		return nil, ResultUncertain()
	}
	if c.git, err = base.cloneProfile(ctx, root); err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		return nil, err
	}
	// Holding the source lock makes this Worker the only cache writer. Reconcile
	// proves that no native process of an earlier attempt is still running
	// before any state is interpreted or any path is rebuilt.
	if err := process.ReconcileOwnerContext(ctx, base.ProcessRoot, owner); err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		if domain.SafeError(err).Code == domain.RecoveryRequired || ctx.Err() != nil {
			return nil, err
		}
		return nil, ResultUncertain()
	}
	if err := c.open(ctx, spec.RemoteURL); err != nil {
		return nil, err
	}
	if spec.AutoFetch {
		if err := c.update(ctx, spec.RemoteURL); err != nil {
			return nil, err
		}
	}
	acquired = true
	return c, nil
}

func (c *repositoryCacheLease) readJournal() (repositoryCacheJournal, bool, error) {
	raw, err := security.ReadPrivate(c.journalPath(), repositoryCacheMaxJournal)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return repositoryCacheJournal{}, false, nil
		}
		return repositoryCacheJournal{}, false, ResultUncertain()
	}
	var journal repositoryCacheJournal
	if err := domain.DecodeBounded(raw, &journal, repositoryCacheMaxJournal); err != nil || !journal.valid(c.identity) {
		return repositoryCacheJournal{}, false, ResultUncertain()
	}
	return journal, true, nil
}

func (c *repositoryCacheLease) writeJournal(journal repositoryCacheJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return ResultUncertain()
	}
	if err := security.WriteAtomic(c.journalPath(), raw); err != nil {
		return ResultUncertain()
	}
	c.journal = journal
	return nil
}

func (c *repositoryCacheLease) removeJournal() error {
	if err := os.Remove(c.journalPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	if err := security.SyncParent(c.journalPath()); err != nil {
		return ResultUncertain()
	}
	c.journal = repositoryCacheJournal{}
	return nil
}

func pathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, ResultUncertain()
	}
}

// open interprets the durable entry state under the source lock and leaves a
// verified, published store at c.store, or returns without having changed any
// path whose ownership was not proven.
func (c *repositoryCacheLease) open(ctx context.Context, source string) error {
	journal, present, err := c.readJournal()
	if err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		return err
	}
	if !present {
		// An interrupted first journal write may leave only its own temporary
		// file. Any other content has no recorded owner and is never adopted.
		entries, err := os.ReadDir(c.dir)
		if err != nil {
			c.failure(ctx, repositoryCacheStageRecover, err)
			return ResultUncertain()
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".pending-") {
				err := ResultUncertain()
				c.failure(ctx, repositoryCacheStageRecover, err)
				return err
			}
		}
		return c.fill(ctx, source)
	}
	c.journal = journal
	if journal.State == repositoryCacheFilling {
		return c.recoverFilling(ctx, source)
	}
	return c.reuse(ctx, source)
}

func (c *repositoryCacheLease) recoverFilling(ctx context.Context, source string) error {
	staging := c.stagingPath(c.journal.FillID)
	stagingPresent, err := pathExists(staging)
	if err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		return err
	}
	storePresent, err := pathExists(c.store)
	if err != nil {
		c.failure(ctx, repositoryCacheStageRecover, err)
		return err
	}
	switch {
	case stagingPresent && storePresent, (stagingPresent || storePresent) && c.journal.RootDigest == "":
		// Both names, or a name whose creation was never bound to an identity,
		// cannot be attributed to the interrupted fill.
		err := ResultUncertain()
		c.failure(ctx, repositoryCacheStageRecover, err)
		return err
	case stagingPresent:
		// The fill never published. Remove only the exact directory it created.
		if err := c.removeOwned(ctx, staging, c.journal.RootDigest); err != nil {
			c.failure(ctx, repositoryCacheStageRecover, err)
			return err
		}
		return c.fill(ctx, source)
	case storePresent:
		// The rename completed but the journal still says filling. The store is
		// the filled directory only after the full check proves it.
		c.journal.State = repositoryCacheUnverified
		return c.reuse(ctx, source)
	}
	return c.fill(ctx, source)
}

func (c *repositoryCacheLease) reuse(ctx context.Context, source string) error {
	storePresent, err := pathExists(c.store)
	if err != nil {
		c.failure(ctx, repositoryCacheStageReuse, err)
		return err
	}
	if !storePresent {
		// Nothing exists to preserve or overwrite; the journal names no other path.
		return c.fill(ctx, source)
	}
	digest, err := directoryIdentityDigest(c.store)
	if err != nil || c.journal.RootDigest == "" || digest != c.journal.RootDigest {
		// A replaced directory is foreign. It is never verified, removed or reused.
		err = ResultUncertain()
		c.failure(ctx, repositoryCacheStageReuse, err)
		return err
	}
	corrupt, err := c.verifyAt(ctx, c.store, c.journal.State == repositoryCacheUnverified)
	if err != nil {
		c.failure(ctx, repositoryCacheStageReuse, err)
		return err
	}
	if corrupt {
		c.failure(ctx, repositoryCacheStageReuse, domain.Fail(domain.Unavailable, "The repository source cache entry is unusable.", "It is rebuilt after ownership is proven."))
		if err := c.removeOwned(ctx, c.store, c.journal.RootDigest); err != nil {
			c.failure(ctx, repositoryCacheStageReuse, err)
			return err
		}
		return c.fill(ctx, source)
	}
	if c.journal.State != repositoryCachePublished {
		next := c.journal
		next.State = repositoryCachePublished
		if err := c.writeJournal(next); err != nil {
			c.failure(ctx, repositoryCacheStageReuse, err)
			return err
		}
	}
	c.logger.InfoContext(ctx, "workspace_repository_cache_hit")
	return nil
}

// verifyAt reports corrupt only for a definite native Git rejection of the
// store. Cancellation, timeouts, launch failures and unproven process cleanup
// are returned as errors and never authorize a rebuild.
func (c *repositoryCacheLease) verifyAt(ctx context.Context, path string, full bool) (bool, error) {
	classify := func(err error) (bool, error) {
		if ctx.Err() == nil && domain.SafeError(err).Cause == "git_exit" {
			return true, nil
		}
		return false, err
	}
	// --git-dir prevents Git from discovering an unrelated parent repository
	// when the store itself lost its repository markers.
	raw, err := c.git.run(ctx, path, "--git-dir=.", "rev-parse", "--is-bare-repository")
	if err != nil {
		return classify(err)
	}
	if strings.TrimSpace(string(raw)) != "true" {
		return true, nil
	}
	if _, err := c.git.run(ctx, path, "--git-dir=.", "for-each-ref", "--count=1", "--format=%(refname)"); err != nil {
		return classify(err)
	}
	if full {
		if _, err := c.git.run(ctx, path, "--git-dir=.", "fsck", "--connectivity-only", "--no-dangling"); err != nil {
			return classify(err)
		}
	}
	return false, nil
}

// removeOwned removes a directory only while it is still the exact object the
// journal recorded. The identity is held open from capture to removal.
func (c *repositoryCacheLease) removeOwned(ctx context.Context, path, digest string) error {
	identity, err := security.CapturePathIdentity(path)
	if err != nil {
		return ResultUncertain()
	}
	defer identity.Close()
	if !identity.Present() {
		return nil
	}
	if digest == "" {
		return ResultUncertain()
	}
	current, err := directoryIdentityDigest(path)
	if err != nil || current != digest {
		return ResultUncertain()
	}
	if err := security.RemoveOwnedTreeIdentity(ctx, c.root, path, identity); err != nil {
		return ResultUncertain()
	}
	return nil
}

// fill clones the pinned URL into a staging directory, verifies it and only
// then renames it to the published store name. Every earlier name is private
// to the journaled fill and removable only by its recorded identity.
func (c *repositoryCacheLease) fill(ctx context.Context, source string) error {
	if exists, err := pathExists(c.store); err != nil || exists {
		err = ResultUncertain()
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	c.logger.InfoContext(ctx, "workspace_repository_cache_miss")
	fill := repositoryCacheJournal{Version: repositoryCacheJournalVersion, SourceIdentity: c.identity, State: repositoryCacheFilling, FillID: domain.NewID()}
	if err := c.writeJournal(fill); err != nil {
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	staging := c.stagingPath(fill.FillID)
	if err := security.CreatePrivateDirExclusive(staging); err != nil {
		err = ResultUncertain()
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	digest, err := directoryIdentityDigest(staging)
	if err != nil || security.SyncParent(staging) != nil {
		err = ResultUncertain()
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	fill.RootDigest = digest
	if err := c.writeJournal(fill); err != nil {
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	if _, err := c.git.run(ctx, c.dir, repositoryCacheCloneArguments(source, staging, filepath.Join(c.manager.Root, "empty-hooks"))...); err != nil {
		return c.abandonFill(ctx, staging, err)
	}
	if after, err := directoryIdentityDigest(staging); err != nil || after != digest {
		return c.abandonFill(ctx, staging, ResultUncertain())
	}
	corrupt, err := c.verifyAt(ctx, staging, false)
	if err == nil && corrupt {
		err = domain.Fail(domain.Unavailable, "The repository source clone could not be verified.", "Retry after checking the repository source.")
	}
	if err != nil {
		return c.abandonFill(ctx, staging, err)
	}
	if err := os.Rename(staging, c.store); err != nil {
		return c.abandonFill(ctx, staging, ResultUncertain())
	}
	// From here the store name exists. Failure is uncertain, not removable
	// cleanup: the next acquisition proves it through the recorded digest.
	if err := security.SyncParent(c.store); err != nil {
		err = ResultUncertain()
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	published := fill
	published.State = repositoryCachePublished
	if err := c.writeJournal(published); err != nil {
		c.failure(ctx, repositoryCacheStageFill, err)
		return err
	}
	return nil
}

func repositoryCacheCloneArguments(source, destination, template string) []string {
	return []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always", "-c", "http.followRedirects=false", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-c", "fetch.recurseSubmodules=false",
		"clone", "--bare", "--no-recurse-submodules", "--template=" + template, "--", source, destination}
}

// abandonFill removes an unpublished staging directory after the failure and
// forgets the fill. Unproven process cleanup or unproven ownership keeps the
// journal and the directory so the next acquisition can decide.
func (c *repositoryCacheLease) abandonFill(ctx context.Context, staging string, cause error) error {
	c.failure(ctx, repositoryCacheStageFill, cause)
	if domain.SafeError(cause).Code == domain.RecoveryRequired {
		return cause
	}
	cleanup, cancel := context.WithTimeout(context.Background(), repositoryCacheCleanupTimeout)
	defer cancel()
	if err := c.removeOwned(cleanup, staging, c.journal.RootDigest); err != nil {
		c.failure(ctx, repositoryCacheStageFill, err)
		return cause
	}
	if err := c.removeJournal(); err != nil {
		c.failure(ctx, repositoryCacheStageFill, err)
	}
	return cause
}

// update refreshes the cache from the session's pinned URL before the session
// copy. It is best effort: an ordinary failed fetch leaves a verified entry and
// the session's own required fetch still fails closed. Cancellation and
// unproven process cleanup mark the entry unverified and fail the preparation.
func (c *repositoryCacheLease) update(ctx context.Context, source string) error {
	published := c.journal
	pending := published
	pending.State = repositoryCacheUnverified
	if err := c.writeJournal(pending); err != nil {
		c.failure(ctx, repositoryCacheStageUpdate, err)
		return err
	}
	_, err := c.git.run(ctx, c.store, "--git-dir=.", "fetch", "--no-tags", "--no-recurse-submodules", "--", source, "+refs/heads/*:refs/heads/*")
	if err == nil {
		if err := c.writeJournal(published); err != nil {
			c.failure(ctx, repositoryCacheStageUpdate, err)
			return err
		}
		c.logger.InfoContext(ctx, "workspace_repository_cache_update")
		return nil
	}
	c.failure(ctx, repositoryCacheStageUpdate, err)
	safe := domain.SafeError(err)
	if ctx.Err() != nil || safe.Code == domain.RecoveryRequired {
		return err
	}
	if safe.Cause == "git_exit" || safe.Cause == "clone_authentication" {
		// Git exited normally: its ref updates are atomic and nothing is partial.
		if err := c.writeJournal(published); err != nil {
			return err
		}
	}
	return nil
}

// markUnverified records that a copy from the store failed, so the next user
// runs the full check before trusting the entry.
func (c *repositoryCacheLease) markUnverified(ctx context.Context) {
	if c.journal.State != repositoryCachePublished {
		return
	}
	next := c.journal
	next.State = repositoryCacheUnverified
	if err := c.writeJournal(next); err != nil {
		c.failure(ctx, repositoryCacheStageClone, err)
	}
}

func (c *repositoryCacheLease) release(ctx context.Context, cause error) {
	if c.released {
		return
	}
	c.released = true
	if cause != nil {
		c.failure(ctx, repositoryCacheStageClone, cause)
	}
	c.lock.Close()
}

// cloneFromRepositoryCache creates the session's independent clone from the
// leased cache store, then restores the session's pinned URL as its remote.
// All later fetches, references and authentication use that pinned URL.
func (m *Manager) cloneFromRepositoryCache(ctx context.Context, clone Git, cache *repositoryCacheLease, root, destination, pinned, remote string) error {
	if _, err := clone.run(ctx, root, cloneArguments(cache.Path(), destination, filepath.Join(m.Root, "empty-hooks"), remote, true, true)...); err != nil {
		if ctx.Err() == nil && domain.SafeError(err).Cause == "git_exit" {
			cache.markUnverified(ctx)
		}
		return err
	}
	_, err := clone.run(ctx, destination, "remote", "set-url", remote, pinned)
	return err
}
