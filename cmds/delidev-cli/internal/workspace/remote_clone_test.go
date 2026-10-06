// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManagedCloneFailedAuthenticationAndCanceledProcessCleanup(t *testing.T) {
	for _, cancelProcess := range []bool{false, true} {
		t.Run(map[bool]string{false: "authentication", true: "canceled process"}[cancelProcess], func(t *testing.T) {
			m, input, _ := managedCloneFixture(t)
			started := filepath.Join(t.TempDir(), "started")
			body := "#!/bin/sh\nprintf 'fatal: Authentication failed for private-native-content\\n' >&2\nexit 128\n"
			if cancelProcess {
				body = "#!/bin/sh\nprintf started > '" + strings.ReplaceAll(started, "'", "'\"'\"'") + "'\nexec sleep 20\n"
			}
			if err := os.WriteFile(m.Git.Executable, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			m.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			completed := make(chan error, 1)
			go func() { _, err := m.Prepare(ctx, input); completed <- err }()
			if cancelProcess {
				deadline := time.Now().Add(30 * time.Second)
				for {
					if _, err := os.Stat(started); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("owned clone process did not start")
					}
					time.Sleep(25 * time.Millisecond)
				}
				cancel()
			}
			err := <-completed
			problem := domain.SafeError(err)
			if cancelProcess && problem.Code != domain.Canceled || !cancelProcess && problem.Cause != "clone_authentication" || strings.Contains(logs.String(), "private-native-content") || strings.Contains(problem.Error(), "private-native-content") {
				t.Fatal("clone failure lost its bounded classification", problem)
			}
			if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(input.SessionID))); !os.IsNotExist(err) {
				t.Fatal("confirmed process failure retained owned files", err)
			}
		})
	}
}

func TestManagedCloneAdditionalFetchFailureNeverUsesCloneReferences(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	body, err := os.ReadFile(m.Git.Executable)
	if err != nil {
		t.Fatal(err)
	}
	// Keep successful full clone and inspection, but fail the additional fetch
	// with a native diagnostic. Existing clone refs cannot replace that failure.
	body = bytes.Replace(body, []byte("case \" $* \" in\n"), []byte("case \" $* \" in\n*' fetch '*) exit 128 ;;\n"), 1)
	if err := os.WriteFile(m.Git.Executable, body, 0700); err != nil {
		t.Fatal(err)
	}
	input.Repositories[0].AutoFetch = true
	if _, err := m.Prepare(context.Background(), input); err == nil {
		t.Fatal("failed fetch silently used retained clone refs")
	}
	if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(input.SessionID))); !os.IsNotExist(err) {
		t.Fatal("failed preparation retained clone", err)
	}
}

// The injected executable maps a validated HTTPS address to an isolated Git
// fixture. Production transport validation stays intact; this proves ownership
// and Git history, not real network, credential-helper or platform acceptance.
func managedCloneFixture(t *testing.T) (*Manager, PrepareRequest, string) {
	t.Helper()
	git, _, clone, marker := cloneFixture(t)
	m := manager(t)
	m.Git.Executable = git.Executable
	repo := domain.NewID()
	request := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.Worktree, PrimaryRepository: repo, Repositories: []RepositorySpec{{ID: repo, SourceKind: RemoteCloneSource, RemoteURL: clone.URL}}}
	return m, request, marker
}
func TestManagedCloneDetachedFullHistoryAndRestartReplay(t *testing.T) {
	m, input, marker := managedCloneFixture(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	repo := manifest.Repositories[0]
	if repo.Source != repo.Path || repo.SourceKind != RemoteCloneSource || !repo.Owned || gitTest(t, repo.Path, "branch", "--show-current") != "" {
		t.Fatal("not an independent detached clone")
	}
	if err := ValidateResult(input, manifest, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	if gitTest(t, repo.Path, "rev-parse", "--is-shallow-repository") != "false" {
		t.Fatal("partial history")
	}
	replacement := &Manager{Root: m.Root, Git: Git{Executable: m.Git.Executable}, Logger: m.Logger}
	again, err := replacement.Prepare(context.Background(), input)
	if err != nil || manifestDigest(again) != manifestDigest(manifest) {
		t.Fatal("ready result not reused", err)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "invoked" {
		t.Fatal("duplicate clone", string(raw))
	}
	recovered, err := replacement.Recover(context.Background(), recoveryInput(input), false)
	if err != nil || recovered.Outcome != RecoveredReady {
		t.Fatal("lost response did not recover", err)
	}
	changed := input
	changed.Repositories = append([]RepositorySpec(nil), input.Repositories...)
	changed.Repositories[0].RemoteURL = "https://github.com/fixture/another.git"
	if _, err := replacement.Prepare(context.Background(), changed); err == nil {
		t.Fatal("accepted source changed")
	}
}

func TestManagedCloneProvisionsConfiguredRemoteAliases(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	input.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}
	input.Repositories[0].AutoFetch = false
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	repo := manifest.Repositories[0]
	if !strings.Contains(gitTest(t, repo.Path, "remote"), "upstream") {
		t.Fatal("configured remote alias was not provisioned")
	}
	if repo.Starting.Remote != "upstream" || repo.StartingCommit == "" {
		t.Fatalf("alias reference was not resolved from the initial clone: %+v", repo)
	}
}

func TestManagedCloneMaterializesConfiguredLocalBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix Git fixture; native Windows clone acceptance is separate")
	}
	source, destination := repository(t), filepath.Join(t.TempDir(), "clone")
	gitTest(t, source, "branch", "feature")
	gitTest(t, source, "clone", "--no-checkout", source, destination)
	gitTest(t, destination, "update-ref", "-d", "refs/heads/feature")
	git := Git{ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID()}
	spec := RepositorySpec{Base: domain.Reference{Type: domain.LocalBranch, Name: "feature"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "feature"}}
	if err := materializeManagedCloneBranches(context.Background(), git, destination, spec, "origin"); err != nil {
		t.Fatal(err)
	}
	if got, want := gitTest(t, destination, "rev-parse", "refs/heads/feature"), gitTest(t, source, "rev-parse", "refs/heads/feature"); got != want {
		t.Fatalf("materialized branch points to %s, want %s", got, want)
	}
}

func TestManagedCloneConcurrentPreparationRunsOnce(t *testing.T) {
	m, input, marker := managedCloneFixture(t)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, err := m.Prepare(context.Background(), input); results <- err }()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatal("no preparation succeeded")
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "invoked" {
		t.Fatal("concurrent clone duplicated", string(raw))
	}
}
func TestManagedCloneProjectFailureAndForeignReplacement(t *testing.T) {
	t.Run("partial project", func(t *testing.T) {
		m, input, _ := managedCloneFixture(t)
		input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), SourceKind: RemoteCloneSource, RemoteURL: input.Repositories[0].RemoteURL, Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "origin", Name: "missing"}})
		if _, err := m.Prepare(context.Background(), input); err == nil {
			t.Fatal("partial project became ready")
		}
		if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(input.SessionID))); !os.IsNotExist(err) {
			t.Fatal("owned partial clone retained", err)
		}
	})
	t.Run("replacement", func(t *testing.T) {
		m, input, _ := managedCloneFixture(t)
		manifest, err := m.Prepare(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		repo := manifest.Repositories[0]
		if err := os.Rename(repo.Path, repo.Path+"-original"); err != nil {
			t.Fatal(err)
		}
		if _, err := walkSnapshot(context.Background(), repo.Path+"-original", repo.Path, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := m.verifyWorkspaceIdentity(context.Background(), input, manifest, continuationIdentity); err == nil {
			t.Fatal("replacement adopted")
		}
		manifest.State = CleanupPending
		if err := m.cleanup(context.Background(), filepath.Dir(repo.Path), manifest); err == nil {
			t.Fatal("replacement deleted")
		}
		if _, err := os.Stat(repo.Path); err != nil {
			t.Fatal("foreign clone lost", err)
		}
	})
}
func TestManagedCloneLeaseSnapshotRestoreAndIndependentFork(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	parent := manifest.Repositories[0].Path
	if err := os.WriteFile(filepath.Join(parent, "tracked.txt"), []byte("fork dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// The public clone fixture redirects every clone. Use actual Git for the
	// explicitly bound local Fork, which must copy objects without hard links.
	m.Git.Executable = ""
	fork, err := m.ForkPreparation(context.Background(), manifest, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.InspectForkSnapshot(context.Background(), manifest, fork)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.PrepareFork(context.Background(), fork, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if child.Repositories[0].SourceKind != IndependentForkSource {
		t.Fatal("fork retains parent Git lifetime")
	}
	storage := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: input, Manifest: manifest}
	preview := storageDo(t, m, storage)
	storage.Action, storage.OperationID, storage.SnapshotID, storage.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	cleaned := storageDo(t, m, storage)
	if _, err := m.verifyWorkspaceIdentity(context.Background(), fork, child, preparationIdentity); err != nil {
		t.Fatal("parent cleanup invalidated fork", err)
	}
	raw, _ := os.ReadFile(filepath.Join(child.PrimaryPath, "tracked.txt"))
	if string(raw) != "fork dirty\n" {
		t.Fatal("dirty fork lost")
	}
	storage.Action, storage.OperationID, storage.PreviousState, storage.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, cleaned.Snapshot.SHA256
	storageDo(t, m, storage)
	if _, err := m.verifyWorkspaceIdentity(context.Background(), input, manifest, continuationIdentity); err != nil {
		t.Fatal("restored clone identity lost", err)
	}
	if !strings.Contains(gitTest(t, manifest.PrimaryPath, "log", "--oneline"), "initial") {
		t.Fatal("history lost")
	}
}

func TestManagedCloneSidechatAndPermanentDeletion(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	ctx := context.Background()
	parent, err := m.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	reference, child, err := m.PrepareSidechatReference(ctx, domain.NewID(), domain.NewID(), input, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResult(reference, child, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	if child.Repositories[0].Owned {
		t.Fatal("Sidechat acquired clone ownership")
	}
	if _, err := m.verifyWorkspaceIdentity(ctx, reference, child, continuationIdentity); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteOwnedWorkspace(ctx, sidechatDeletionWork(parent), false, func() error { return nil }); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("parent deletion crossed Sidechat", err)
	}
	if err := m.DeleteOwnedWorkspace(ctx, sidechatDeletionWork(child), false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(parent.PrimaryPath); err != nil {
		t.Fatal("child deleted parent", err)
	}
	if err := m.DeleteOwnedWorkspace(ctx, sidechatDeletionWork(parent), false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(parent.PrimaryPath); !os.IsNotExist(err) {
		t.Fatal("clone not deleted", err)
	}
}

func TestManagedCloneForkFromLocalPreservesOriginalFolder(t *testing.T) {
	m := manager(t)
	path := repository(t)
	input, _ := requestFor(path)
	input.Type, input.OriginMachineID = domain.Local, input.MachineID
	input.Repositories[0].SourceKind, input.Repositories[0].RemoteURL = LocalCheckoutSource, "https://github.com/fixture/repo.git"
	input.Repositories[0].Starting = domain.Reference{}
	gitTest(t, path, "remote", "add", "origin", input.Repositories[0].RemoteURL)
	parent, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := m.ForkPreparation(context.Background(), parent, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.Prepare(context.Background(), fork)
	if err != nil {
		t.Fatal(err)
	}
	if child.Repositories[0].SourceKind != IndependentForkSource || gitTest(t, child.PrimaryPath, "rev-parse", "--git-common-dir") != ".git" {
		t.Fatal("Fork retained original Local Git store")
	}
	child.State = CleanupPending
	if err := m.cleanup(context.Background(), filepath.Dir(child.PrimaryPath), child); err != nil {
		t.Fatal(err)
	}
	if gitTest(t, path, "rev-parse", "HEAD") != parent.Repositories[0].StartingCommit {
		t.Fatal("child deletion changed original Local folder")
	}
}
