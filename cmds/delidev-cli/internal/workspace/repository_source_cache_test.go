// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const cacheFixtureURL = "https://github.com/fixture/repo.git"

// syncBuffer lets concurrent preparations share one structured log sink.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type cacheEnv struct {
	t      *testing.T
	m      *Manager
	marker string
	source string
	logs   *syncBuffer
}

func newCacheEnv(t *testing.T) *cacheEnv {
	t.Helper()
	git, _, _, marker, source := cloneFixtureWithSource(t)
	e := &cacheEnv{t: t, m: manager(t), marker: marker, source: source, logs: &syncBuffer{}}
	e.m.Git.Executable = git.Executable
	// Canonicalize the Worker root up front so tests can address cache paths.
	if err := e.m.initialize(); err != nil {
		t.Fatal(err)
	}
	e.m.Logger = slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return e
}

func (e *cacheEnv) request(url string, autoFetch bool) PrepareRequest {
	repo := domain.NewID()
	return PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.Worktree, PrimaryRepository: repo, Repositories: []RepositorySpec{{ID: repo, SourceKind: RemoteCloneSource, RemoteURL: url, AutoFetch: autoFetch}}}
}

func (e *cacheEnv) prepare(request PrepareRequest) (Manifest, error) {
	return e.m.Prepare(context.Background(), request)
}

func (e *cacheEnv) mustPrepare(request PrepareRequest) Manifest {
	e.t.Helper()
	manifest, err := e.prepare(request)
	if err != nil {
		e.t.Fatal(err)
	}
	return manifest
}

func (e *cacheEnv) clones() int {
	raw, _ := os.ReadFile(e.marker)
	return strings.Count(string(raw), "invoked")
}

func (e *cacheEnv) setMode(mode string) {
	e.t.Helper()
	if mode == "" {
		os.Remove(e.marker + ".mode")
		return
	}
	if err := os.WriteFile(e.marker+".mode", []byte(mode), 0600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *cacheEnv) identity(url string) string {
	e.t.Helper()
	identity, err := domain.RepositoryCloneSourceIdentity(url)
	if err != nil {
		e.t.Fatal(err)
	}
	return identity
}

func (e *cacheEnv) entry(url string) string {
	return filepath.Join(e.m.Root, repositoryCacheDirectory, e.identity(url))
}
func (e *cacheEnv) store(url string) string {
	return filepath.Join(e.entry(url), repositoryCacheStoreName)
}
func (e *cacheEnv) journalFile(url string) string {
	return filepath.Join(e.entry(url), repositoryCacheJournalName)
}

func (e *cacheEnv) readJournal(url string) repositoryCacheJournal {
	e.t.Helper()
	raw, err := os.ReadFile(e.journalFile(url))
	if err != nil {
		e.t.Fatal(err)
	}
	var journal repositoryCacheJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		e.t.Fatal(err)
	}
	return journal
}

func (e *cacheEnv) writeJournal(url string, journal repositoryCacheJournal) {
	e.t.Helper()
	if journal.Version == 0 {
		journal.Version = repositoryCacheJournalVersion
	}
	if journal.SourceIdentity == "" {
		journal.SourceIdentity = e.identity(url)
	}
	if journal.FillID == "" {
		journal.FillID = domain.NewID()
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := security.PrivateDir(e.entry(url)); err != nil {
		e.t.Fatal(err)
	}
	if err := security.WriteAtomic(e.journalFile(url), raw); err != nil {
		e.t.Fatal(err)
	}
}

func (e *cacheEnv) digest(path string) string {
	e.t.Helper()
	digest, err := directoryIdentityDigest(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return digest
}

func (e *cacheEnv) truncatePacks(url string) {
	e.t.Helper()
	packs, _ := filepath.Glob(filepath.Join(e.store(url), "objects", "pack", "*.pack"))
	if len(packs) == 0 {
		e.t.Fatal("cache store has no pack to corrupt")
	}
	for _, pack := range packs {
		if err := os.Chmod(pack, 0600); err != nil {
			e.t.Fatal(err)
		}
		if err := os.Truncate(pack, 8); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *cacheEnv) advanceSource() string {
	e.t.Helper()
	name := filepath.Join(e.source, "advance.txt")
	if err := os.WriteFile(name, []byte(time.Now().String()), 0600); err != nil {
		e.t.Fatal(err)
	}
	gitTest(e.t, e.source, "add", "advance.txt")
	gitTest(e.t, e.source, "commit", "-m", "advance")
	return gitTest(e.t, e.source, "rev-parse", "HEAD")
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s was not retained: %v", path, err)
	}
}
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists: %v", path, err)
	}
}

func TestRepositorySourceCacheOwnerIsDeterministicAndNotASession(t *testing.T) {
	a, b := repositoryCacheOwner(strings.Repeat("a", 64)), repositoryCacheOwner(strings.Repeat("b", 64))
	if a.Validate() != nil || b.Validate() != nil || a == b || a != repositoryCacheOwner(strings.Repeat("a", 64)) {
		t.Fatal("cache owner is not a stable distinct valid identity", a, b)
	}
}

func TestRepositorySourceCacheOneRemoteCloneAcrossIndependentSessions(t *testing.T) {
	e := newCacheEnv(t)
	first := e.mustPrepare(e.request(cacheFixtureURL, false))
	second := e.mustPrepare(e.request(cacheFixtureURL, false))
	if e.clones() != 1 {
		t.Fatalf("remote clones = %d, want 1", e.clones())
	}
	a, b := first.Repositories[0], second.Repositories[0]
	if a.Path == b.Path || a.CloneIdentityDigest == b.CloneIdentityDigest {
		t.Fatal("sessions share a checkout")
	}
	cacheStore := e.store(cacheFixtureURL)
	for _, repo := range []PreparedRepository{a, b} {
		if gitTest(t, repo.Path, "remote", "get-url", "origin") != cacheFixtureURL {
			t.Fatal("session remote is not its pinned URL")
		}
		if gitTest(t, repo.Path, "rev-parse", "--is-shallow-repository") != "false" || gitTest(t, repo.Path, "branch", "--show-current") != "" {
			t.Fatal("session is not a detached full-history clone")
		}
		mustNotExist(t, filepath.Join(repo.Path, ".git", "objects", "info", "alternates"))
		if strings.Contains(gitTest(t, repo.Path, "config", "--get-regexp", "^remote\\."), cacheStore) {
			t.Fatal("session configuration references the cache")
		}
	}
	// No object file is shared with the cache or the other session.
	sharedFiles := func(x, y string) bool {
		found := false
		filepath.Walk(filepath.Join(x, "objects"), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			other := filepath.Join(y, "objects", strings.TrimPrefix(path, filepath.Join(x, "objects")))
			if o, err := os.Lstat(other); err == nil && os.SameFile(info, o) {
				found = true
			}
			return nil
		})
		return found
	}
	if sharedFiles(filepath.Join(a.Path, ".git"), cacheStore) || sharedFiles(filepath.Join(b.Path, ".git"), cacheStore) || sharedFiles(filepath.Join(a.Path, ".git"), filepath.Join(b.Path, ".git")) {
		t.Fatal("session Git stores share files")
	}
	if journal := e.readJournal(cacheFixtureURL); journal.State != repositoryCachePublished {
		t.Fatal("entry is not published", journal)
	}
	for _, event := range []string{"workspace_repository_cache_miss", "workspace_repository_cache_hit"} {
		if !strings.Contains(e.logs.String(), event) {
			t.Fatal("missing log event", event)
		}
	}
	if strings.Contains(e.logs.String(), "github.com") || strings.Contains(e.logs.String(), e.m.Root+string(filepath.Separator)+repositoryCacheDirectory) {
		t.Fatal("cache logs expose a source URL or cache path")
	}
	if len(second.Repositories) != 1 {
		t.Fatal("manifest shape changed")
	}
}

func TestRepositorySourceCacheSessionDeletionRecoveryAndCleanupKeepCache(t *testing.T) {
	e := newCacheEnv(t)
	request := e.request(cacheFixtureURL, false)
	first := e.mustPrepare(request)
	otherRequest := e.request(cacheFixtureURL, false)
	other := e.mustPrepare(otherRequest)
	replacement := &Manager{Root: e.m.Root, Git: Git{Executable: e.m.Git.Executable}, Logger: e.m.Logger}
	if recovered, err := replacement.Recover(context.Background(), recoveryInput(request), false); err != nil || recovered.Outcome != RecoveredReady {
		t.Fatal("recovery failed", err)
	}
	mustExist(t, e.store(cacheFixtureURL))
	if err := security.PrivateDir(filepath.Join(e.m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	work := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: first.SessionID, MachineID: first.MachineID, DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 2, Digest: strings.Repeat("ab", 32), InstanceID: domain.NewID()}}, PreparationDigests: []string{first.InputDigest}}
	if err := e.m.DeleteOwnedWorkspace(context.Background(), work, false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	mustNotExist(t, first.Repositories[0].Path)
	mustExist(t, e.store(cacheFixtureURL))
	mustExist(t, other.Repositories[0].Path)
	if _, err := e.m.verifyWorkspaceIdentity(context.Background(), otherRequest, other, continuationIdentity); err != nil {
		t.Fatal("remaining session lost identity", err)
	}
	// A new session after deletion reuses the retained entry without a clone.
	e.mustPrepare(e.request(cacheFixtureURL, false))
	if e.clones() != 1 {
		t.Fatal("deletion discarded the cache", e.clones())
	}
	// Failed-preparation cleanup of a session never reaches the cache either.
	failing := e.request(cacheFixtureURL, false)
	failing.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "origin", Name: "missing"}
	if _, err := e.prepare(failing); err == nil {
		t.Fatal("missing branch prepared")
	}
	mustExist(t, e.store(cacheFixtureURL))
	mustNotExist(t, filepath.Join(e.m.Root, "workspaces", string(failing.SessionID)))
	if e.clones() != 1 {
		t.Fatal("cleanup discarded the cache", e.clones())
	}
}

func TestRepositorySourceCacheSeparateRunnerDevicesAndSourceIdentities(t *testing.T) {
	e := newCacheEnv(t)
	e.mustPrepare(e.request(cacheFixtureURL, false))
	// A second Runner Device owns a separate Manager root and cache.
	device := &Manager{Root: filepath.Join(t.TempDir(), "device"), Git: e.m.Git, Logger: e.m.Logger}
	if _, err := device.Prepare(context.Background(), e.request(cacheFixtureURL, false)); err != nil {
		t.Fatal(err)
	}
	if e.clones() != 2 {
		t.Fatalf("separate devices cloned %d times, want 2", e.clones())
	}
	// Distinct validated identities never share one entry.
	other := "https://github.com/fixture/other.git"
	e.mustPrepare(e.request(other, false))
	if e.clones() != 3 || e.entry(other) == e.entry(cacheFixtureURL) {
		t.Fatal("different source identities shared cache data")
	}
	mustExist(t, e.store(other))
	mustExist(t, e.store(cacheFixtureURL))
}

func TestRepositorySourceCacheEquivalentURLsShareEntryButKeepPinnedURLs(t *testing.T) {
	e := newCacheEnv(t)
	ssh := "git@github.com:fixture/repo.git"
	if e.identity(ssh) != e.identity(cacheFixtureURL) {
		t.Skip("source identity no longer collapses GitHub forms")
	}
	first := e.mustPrepare(e.request(cacheFixtureURL, false))
	second := e.mustPrepare(e.request(ssh, false))
	if e.clones() != 1 {
		t.Fatal("equivalent source forms cloned twice", e.clones())
	}
	if gitTest(t, first.Repositories[0].Path, "remote", "get-url", "origin") != cacheFixtureURL || gitTest(t, second.Repositories[0].Path, "remote", "get-url", "origin") != ssh {
		t.Fatal("a session did not keep its own pinned URL")
	}
}

func TestRepositorySourceCacheAutomaticFetchPolicy(t *testing.T) {
	e := newCacheEnv(t)
	initial := e.mustPrepare(e.request(cacheFixtureURL, true)).Repositories[0].StartingCommit
	advanced := e.advanceSource()
	disabled := e.mustPrepare(e.request(cacheFixtureURL, false)).Repositories[0]
	if disabled.StartingCommit != initial {
		t.Fatal("automatic fetch disabled did not resolve the retained refs", disabled.StartingCommit)
	}
	enabled := e.mustPrepare(e.request(cacheFixtureURL, true)).Repositories[0]
	if enabled.StartingCommit != advanced {
		t.Fatal("automatic fetch did not resolve the advanced remote", enabled.StartingCommit)
	}
	if !strings.Contains(e.logs.String(), "workspace_repository_cache_update") {
		t.Fatal("cache update was not reported")
	}
	if gitTest(t, e.store(cacheFixtureURL), "rev-parse", "refs/heads/main") != advanced {
		t.Fatal("cache did not retain the fetched branch")
	}
	// Retained refs are mirrored into the new session, so a disabled fetch sees them.
	third := e.mustPrepare(e.request(cacheFixtureURL, false)).Repositories[0]
	if third.StartingCommit != advanced {
		t.Fatal("retained cache refs were not mirrored", third.StartingCommit)
	}
	if e.clones() != 1 {
		t.Fatal("advancing remote caused another clone", e.clones())
	}
}

func TestRepositorySourceCacheRequiredFetchFailureNeverUsesStaleRefs(t *testing.T) {
	e := newCacheEnv(t)
	e.mustPrepare(e.request(cacheFixtureURL, true))
	body, err := os.ReadFile(e.m.Git.Executable)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.Replace(body, []byte("case \" $* \" in\n"), []byte("case \" $* \" in\n*' fetch '*) exit 128 ;;\n"), 1)
	if err := os.WriteFile(e.m.Git.Executable, broken, 0700); err != nil {
		t.Fatal(err)
	}
	request := e.request(cacheFixtureURL, true)
	if _, err := e.prepare(request); err == nil {
		t.Fatal("failed required fetch used stale cache refs")
	}
	mustNotExist(t, filepath.Join(e.m.Root, "workspaces", string(request.SessionID)))
	if journal := e.readJournal(cacheFixtureURL); journal.State != repositoryCachePublished {
		t.Fatal("ordinary fetch failure made the entry unusable", journal)
	}
	if !strings.Contains(e.logs.String(), "workspace_repository_cache_failed") {
		t.Fatal("cache failure was not reported")
	}
	if err := os.WriteFile(e.m.Git.Executable, body, 0700); err != nil {
		t.Fatal(err)
	}
	e.mustPrepare(e.request(cacheFixtureURL, true))
	if e.clones() != 1 {
		t.Fatal("recovery after fetch failure cloned again", e.clones())
	}
}

func TestRepositorySourceCacheFailedFirstFillIsNeverReusable(t *testing.T) {
	e := newCacheEnv(t)
	e.setMode("fail")
	request := e.request(cacheFixtureURL, false)
	_, err := e.prepare(request)
	if domain.SafeError(err).Cause != "clone_authentication" || strings.Contains(e.logs.String(), "private-native-content") {
		t.Fatal("first fill failure lost its bounded classification", err)
	}
	mustNotExist(t, e.store(cacheFixtureURL))
	entries, _ := os.ReadDir(e.entry(cacheFixtureURL))
	if len(entries) != 0 {
		t.Fatal("failed fill retained staging or journal", entries)
	}
	e.setMode("")
	e.mustPrepare(e.request(cacheFixtureURL, false))
	if e.clones() != 1 {
		t.Fatal("fill after failure did not clone exactly once", e.clones())
	}
}

func TestRepositorySourceCacheCanceledFirstFillIsNeverReusable(t *testing.T) {
	e := newCacheEnv(t)
	e.setMode("hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.m.Prepare(ctx, e.request(cacheFixtureURL, false)); done <- err }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(e.marker + ".started"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache fill process did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	if err := <-done; domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("cancellation lost its classification", err)
	}
	mustNotExist(t, e.store(cacheFixtureURL))
	entries, _ := os.ReadDir(e.entry(cacheFixtureURL))
	if len(entries) != 0 {
		t.Fatal("canceled fill retained staging or journal", entries)
	}
	e.setMode("")
	e.mustPrepare(e.request(cacheFixtureURL, false))
	if e.clones() != 1 {
		t.Fatal("fill after cancellation did not clone exactly once", e.clones())
	}
}

func TestRepositorySourceCacheCellStateTable(t *testing.T) {
	type cell struct {
		name string
		// populate prepares one session first, leaving a published entry.
		populate bool
		arrange  func(t *testing.T, e *cacheEnv)
		// wantUncertain expects the recovery-required result and no cache mutation.
		wantUncertain bool
		wantClones    int
		retained      func(e *cacheEnv) []string
		wantState     repositoryCacheState
	}
	url := cacheFixtureURL
	cells := []cell{
		{name: "S0 absent entry directory", wantClones: 1, wantState: repositoryCachePublished},
		{name: "S0 empty entry directory", arrange: func(t *testing.T, e *cacheEnv) {
			if err := security.PrivateDir(e.entry(url)); err != nil {
				t.Fatal(err)
			}
		}, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S0 interrupted first journal write", arrange: func(t *testing.T, e *cacheEnv) {
			if err := security.PrivateDir(e.entry(url)); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(e.entry(url), ".pending-journal"), []byte("{"), 0600)
		}, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S1 foreign content without journal", arrange: func(t *testing.T, e *cacheEnv) {
			if err := security.PrivateDir(e.entry(url)); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(e.entry(url), "foreign"), []byte("x"), 0600)
		}, wantUncertain: true, retained: func(e *cacheEnv) []string { return []string{filepath.Join(e.entry(url), "foreign")} }},
		{name: "S1 store without journal", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			os.Remove(e.journalFile(url))
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string { return []string{e.store(url)} }},
		{name: "S1 undecodable journal", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			os.WriteFile(e.journalFile(url), []byte("{not json"), 0600)
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string { return []string{e.store(url)} }},
		{name: "S1 journal for another identity", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{SourceIdentity: strings.Repeat("c", 64), State: repositoryCachePublished, RootDigest: e.digest(e.store(url))})
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string { return []string{e.store(url)} }},
		{name: "S1 unknown journal state", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: "mystery", RootDigest: e.digest(e.store(url))})
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string { return []string{e.store(url)} }},
		{name: "S2 filling with nothing created", arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling})
		}, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S3 filling with owned staging", arrange: func(t *testing.T, e *cacheEnv) {
			fill := domain.NewID()
			staging := filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(fill))
			if err := security.PrivateDir(e.entry(url)); err != nil {
				t.Fatal(err)
			}
			if err := security.CreatePrivateDirExclusive(staging); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(staging, "partial"), []byte("x"), 0600)
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling, FillID: fill, RootDigest: e.digest(staging)})
		}, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S4 filling with staging that has no recorded identity", arrange: func(t *testing.T, e *cacheEnv) {
			fill := domain.NewID()
			staging := filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(fill))
			if err := security.PrivateDir(e.entry(url)); err != nil {
				t.Fatal(err)
			}
			if err := security.CreatePrivateDirExclusive(staging); err != nil {
				t.Fatal(err)
			}
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling, FillID: fill})
		}, wantUncertain: true, retained: func(e *cacheEnv) []string {
			return []string{filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(e.readJournal(url).FillID))}
		}},
		{name: "S4 filling with replaced staging", arrange: func(t *testing.T, e *cacheEnv) {
			fill := domain.NewID()
			staging := filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(fill))
			if err := security.PrivateDir(e.entry(url)); err != nil {
				t.Fatal(err)
			}
			if err := security.CreatePrivateDirExclusive(staging); err != nil {
				t.Fatal(err)
			}
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling, FillID: fill, RootDigest: strings.Repeat("d", 64)})
		}, wantUncertain: true, retained: func(e *cacheEnv) []string {
			return []string{filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(e.readJournal(url).FillID))}
		}},
		{name: "S4 filling with store that has no recorded identity", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling})
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string { return []string{e.store(url)} }},
		{name: "S6 filling with staging and store", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			fill := domain.NewID()
			if err := security.CreatePrivateDirExclusive(filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(fill))); err != nil {
				t.Fatal(err)
			}
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling, FillID: fill, RootDigest: e.digest(e.store(url))})
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string {
			return []string{e.store(url), filepath.Join(e.entry(url), repositoryCacheStagingPrefix+string(e.readJournal(url).FillID))}
		}},
		{name: "S5 publication rename done, journal still filling", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling, RootDigest: e.digest(e.store(url))})
		}, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S5 publication rename done with corrupt store", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheFilling, RootDigest: e.digest(e.store(url))})
			e.truncatePacks(url)
		}, wantClones: 2, wantState: repositoryCachePublished},
		{name: "S7 published and verified", populate: true, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S8 published but store missing", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			if err := os.RemoveAll(e.store(url)); err != nil {
				t.Fatal(err)
			}
		}, wantClones: 2, wantState: repositoryCachePublished},
		{name: "S9 published but store replaced", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			if err := os.Rename(e.store(url), e.store(url)+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := security.CreatePrivateDirExclusive(e.store(url)); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(e.store(url), "foreign"), []byte("x"), 0600)
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string {
			return []string{filepath.Join(e.store(url), "foreign"), e.store(url) + "-original"}
		}},
		{name: "S10 published but repository markers lost", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			if err := os.Remove(filepath.Join(e.store(url), "HEAD")); err != nil {
				t.Fatal(err)
			}
		}, wantClones: 2, wantState: repositoryCachePublished},
		{name: "S11 unverified and intact", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheUnverified, RootDigest: e.digest(e.store(url))})
		}, wantClones: 1, wantState: repositoryCachePublished},
		{name: "S12 unverified with damaged objects", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheUnverified, RootDigest: e.digest(e.store(url))})
			e.truncatePacks(url)
		}, wantClones: 2, wantState: repositoryCachePublished},
		{name: "S12 unverified but store replaced", populate: true, arrange: func(t *testing.T, e *cacheEnv) {
			e.writeJournal(url, repositoryCacheJournal{State: repositoryCacheUnverified, RootDigest: e.digest(e.store(url))})
			if err := os.Rename(e.store(url), e.store(url)+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := security.CreatePrivateDirExclusive(e.store(url)); err != nil {
				t.Fatal(err)
			}
		}, wantUncertain: true, wantClones: 1, retained: func(e *cacheEnv) []string { return []string{e.store(url), e.store(url) + "-original"} }},
	}
	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			e := newCacheEnv(t)
			if c.populate {
				e.mustPrepare(e.request(url, false))
			}
			if c.arrange != nil {
				c.arrange(t, e)
			}
			var retained []string
			if c.retained != nil {
				retained = c.retained(e)
			}
			request := e.request(url, false)
			manifest, err := e.prepare(request)
			if c.wantUncertain {
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("expected the recovery-required result", err)
				}
				for _, path := range retained {
					mustExist(t, path)
				}
			} else if err != nil {
				t.Fatal(err, e.logs.String())
			} else if gitTest(t, manifest.Repositories[0].Path, "rev-parse", "HEAD") == "" {
				t.Fatal("no checkout")
			}
			if e.clones() != c.wantClones {
				t.Fatalf("remote clones = %d, want %d", e.clones(), c.wantClones)
			}
			if c.wantState != "" {
				if journal := e.readJournal(url); journal.State != c.wantState || journal.RootDigest != e.digest(e.store(url)) {
					t.Fatal("entry journal does not describe the published store", journal)
				}
				entries, _ := os.ReadDir(e.entry(url))
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), repositoryCacheStagingPrefix) {
						t.Fatal("staging retained after a published fill", entry.Name())
					}
				}
			}
		})
	}
}

func TestRepositorySourceCacheCopyFailureMarksEntryUnverifiedThenRebuilds(t *testing.T) {
	e := newCacheEnv(t)
	e.mustPrepare(e.request(cacheFixtureURL, false))
	// Objects damaged after the cheap checks cannot be seen until a copy uses them.
	e.truncatePacks(cacheFixtureURL)
	failing := e.request(cacheFixtureURL, false)
	if _, err := e.prepare(failing); err == nil {
		t.Fatal("copy from a damaged cache succeeded")
	}
	mustNotExist(t, filepath.Join(e.m.Root, "workspaces", string(failing.SessionID)))
	if journal := e.readJournal(cacheFixtureURL); journal.State != repositoryCacheUnverified {
		t.Fatal("failed copy did not mark the entry unverified", journal)
	}
	e.mustPrepare(e.request(cacheFixtureURL, false))
	if e.clones() != 2 || e.readJournal(cacheFixtureURL).State != repositoryCachePublished {
		t.Fatal("damaged entry was not rebuilt after proof", e.clones())
	}
}

func TestRepositorySourceCacheInterruptedUpdateIsVerifiedBeforeReuse(t *testing.T) {
	e := newCacheEnv(t)
	e.mustPrepare(e.request(cacheFixtureURL, false))
	e.setMode("")
	// A hanging update is canceled; the entry must be left unverified.
	body, err := os.ReadFile(e.m.Git.Executable)
	if err != nil {
		t.Fatal(err)
	}
	hanging := bytes.Replace(body, []byte("case \" $* \" in\n"), []byte("case \" $* \" in\n*' fetch '*) printf started > '"+e.marker+".fetching'; exec sleep 30 ;;\n"), 1)
	if err := os.WriteFile(e.m.Git.Executable, hanging, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.m.Prepare(ctx, e.request(cacheFixtureURL, true)); done <- err }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(e.marker + ".fetching"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("update fetch did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	if err := <-done; domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("canceled update lost its classification", err)
	}
	if journal := e.readJournal(cacheFixtureURL); journal.State != repositoryCacheUnverified {
		t.Fatal("interrupted update left the entry published", journal)
	}
	if err := os.WriteFile(e.m.Git.Executable, body, 0700); err != nil {
		t.Fatal(err)
	}
	e.mustPrepare(e.request(cacheFixtureURL, false))
	if e.readJournal(cacheFixtureURL).State != repositoryCachePublished || e.clones() != 1 {
		t.Fatal("verified entry was not reused after interruption")
	}
}

func TestRepositorySourceCacheConcurrentFirstUseSerializesToOneClone(t *testing.T) {
	e := newCacheEnv(t)
	const sessions = 6
	var group sync.WaitGroup
	results := make(chan error, sessions)
	manifests := make(chan Manifest, sessions)
	for i := 0; i < sessions; i++ {
		request := e.request(cacheFixtureURL, i%2 == 0)
		group.Add(1)
		go func() {
			defer group.Done()
			manifest, err := e.prepare(request)
			results <- err
			manifests <- manifest
		}()
	}
	group.Wait()
	close(results)
	close(manifests)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for manifest := range manifests {
		if seen[manifest.Repositories[0].Path] {
			t.Fatal("two sessions share a path")
		}
		seen[manifest.Repositories[0].Path] = true
	}
	if e.clones() != 1 {
		t.Fatalf("concurrent first use cloned %d times, want 1", e.clones())
	}
	if e.readJournal(cacheFixtureURL).State != repositoryCachePublished {
		t.Fatal("entry is not published")
	}
}

func TestRepositorySourceCacheSourceLockSerializesOnlyThatKey(t *testing.T) {
	e := newCacheEnv(t)
	if err := e.m.initialize(); err != nil {
		t.Fatal(err)
	}
	held, err := e.m.lockRepositoryCache(context.Background(), e.identity(cacheFixtureURL))
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			held.Close()
		}
	}()
	// A different source is not queued behind the held key.
	other := "https://github.com/fixture/unrelated.git"
	otherDone := make(chan error, 1)
	go func() { _, err := e.prepare(e.request(other, false)); otherDone <- err }()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("a different source waited behind the held key")
	}
	// The same source waits, then proceeds after release.
	sameDone := make(chan error, 1)
	go func() { _, err := e.prepare(e.request(cacheFixtureURL, false)); sameDone <- err }()
	select {
	case <-sameDone:
		t.Fatal("same source did not wait for its lock")
	case <-time.After(400 * time.Millisecond):
	}
	released = true
	held.Close()
	select {
	case err := <-sameDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("waiting session did not proceed after release")
	}
	if e.clones() != 2 {
		t.Fatal("each source should clone once", e.clones())
	}
}

func TestRepositorySourceCacheLockWaitHonorsCancellationWithoutChangingEntry(t *testing.T) {
	e := newCacheEnv(t)
	if err := e.m.initialize(); err != nil {
		t.Fatal(err)
	}
	held, err := e.m.lockRepositoryCache(context.Background(), e.identity(cacheFixtureURL))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	request := e.request(cacheFixtureURL, false)
	if _, err := e.m.Prepare(ctx, request); err == nil || (domain.SafeError(err).Code != domain.Canceled && domain.SafeError(err).Code != domain.Unavailable) {
		t.Fatal("lock wait ignored the deadline", err)
	}
	mustNotExist(t, e.journalFile(cacheFixtureURL))
	mustNotExist(t, e.store(cacheFixtureURL))
	mustNotExist(t, filepath.Join(e.m.Root, "workspaces", string(request.SessionID)))
	if e.clones() != 0 {
		t.Fatal("canceled waiter cloned")
	}
}

func TestRepositorySourceCacheProcessesUseTheCacheOwnerNotTheSession(t *testing.T) {
	e := newCacheEnv(t)
	manifest := e.mustPrepare(e.request(cacheFixtureURL, false))
	owner := repositoryCacheOwner(e.identity(cacheFixtureURL))
	mustExist(t, filepath.Join(e.m.Root, "processes", string(owner)))
	mustExist(t, filepath.Join(e.m.Root, "processes", string(manifest.SessionID)))
	if owner == manifest.SessionID {
		t.Fatal("cache shares a session owner")
	}
}

func TestRepositorySourceCacheIgnoresLocalGeneralChatAndLegacyCheckoutPreparation(t *testing.T) {
	e := newCacheEnv(t)
	chat := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	e.mustPrepare(chat)
	checkout, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := requestFor(checkout)
	e.mustPrepare(legacy)
	entries, err := os.ReadDir(filepath.Join(e.m.Root, repositoryCacheDirectory))
	if err != nil || len(entries) != 0 {
		t.Fatal("non-remote preparation created cache entries", entries, err)
	}
}
