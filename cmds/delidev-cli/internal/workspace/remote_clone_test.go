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

func TestManagedCloneRejectsEffectiveURLRewrite(t *testing.T) {
	m, input, marker := managedCloneFixture(t)
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[url \"https://other.invalid/\"]\n\tinsteadOf = https://github.com/fixture/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	if _, err := m.Prepare(context.Background(), input); domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatal("rewritten clone source was accepted", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("network-capable clone started before rewrite validation")
	}
}

func TestManagedCloneProfileRetainsCredentialsWithoutURLRewrites(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[url \"https://other.invalid/\"]\n\tinsteadOf = https://github.com/fixture/\n[credential]\n\thelper = fixture-helper\n[core]\n\tsshCommand = ssh -o IdentitiesOnly=yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	g := Git{ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID()}
	profile, err := g.cloneProfile(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := profile.run(context.Background(), root, "config", "--get-all", "credential.helper")
	if err != nil || strings.TrimSpace(string(helper)) != "fixture-helper" {
		t.Fatalf("credential helper was not retained: %q, %v", helper, err)
	}
	sshCommand, err := profile.run(context.Background(), root, "config", "--get", "core.sshCommand")
	if err != nil || strings.TrimSpace(string(sshCommand)) != "ssh -o IdentitiesOnly=yes" {
		t.Fatalf("SSH configuration was not retained: %q, %v", sshCommand, err)
	}
	effective, err := profile.run(context.Background(), root, "ls-remote", "--get-url", "--", "https://github.com/fixture/repo.git")
	if err != nil || strings.TrimSpace(string(effective)) != "https://github.com/fixture/repo.git" {
		t.Fatalf("URL rewrite was not isolated: %q, %v", effective, err)
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

func TestManagedCloneSymbolicHeadOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		want          domain.Code
	}{
		{"absent", "exit 1", ""},
		{"fatal", "exit 128", domain.Unavailable},
		{"foreign", "printf 'refs/remotes/other/main\\n'; exit 0", domain.RecoveryRequired},
		{"empty", "printf 'refs/remotes/origin/\\n'; exit 0", domain.RecoveryRequired},
		{"self", "printf 'refs/remotes/origin/HEAD\\n'; exit 0", domain.RecoveryRequired},
		{"multiline", "printf 'refs/remotes/origin/main\\nrefs/remotes/origin/other\\n'; exit 0", domain.RecoveryRequired},
		{"malformed", "printf 'refs/remotes/origin/bad..branch\\n'; exit 0", domain.Unavailable},
		{"valid", "printf 'refs/remotes/origin/main\\n'; exit 0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, input, marker := managedCloneFixture(t)
			input.Repositories[0].Base = domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}
			input.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "secondary", Name: "main"}
			input.Repositories[0].AutoFetch = false
			body, err := os.ReadFile(m.Git.Executable)
			if err != nil {
				t.Fatal(err)
			}
			// Only the primary read is injected; alias symbolic-ref writes and
			// all clone/ownership commands retain the real fixture executable.
			prefix := "#!/bin/sh\ncase \" $* \" in *' symbolic-ref --quiet refs/remotes/origin/HEAD '*) " + tc.command + ";; esac\n"
			if err := os.WriteFile(m.Git.Executable, []byte(prefix+strings.TrimPrefix(string(body), "#!/bin/sh\n")), 0700); err != nil {
				t.Fatal(err)
			}
			manifest, err := m.Prepare(context.Background(), input)
			if tc.want != "" {
				if err == nil || domain.SafeError(err).Code != tc.want || manifest.State == Ready {
					t.Fatal("failed symbolic HEAD published readiness", manifest, err)
				}
			} else {
				if err != nil || manifest.State != Ready {
					t.Fatal(manifest, err)
				}
				if tc.name == "valid" {
					for _, alias := range []string{"upstream", "secondary"} {
						if got := gitTest(t, manifest.Repositories[0].Path, "symbolic-ref", "refs/remotes/"+alias+"/HEAD"); got != "refs/remotes/"+alias+"/main" {
							t.Fatal("incorrect alias target", alias, got)
						}
					}
				} else {
					for _, alias := range []string{"upstream", "secondary"} {
						if got := gitTest(t, manifest.Repositories[0].Path, "for-each-ref", "--format=%(symref)", "refs/remotes/"+alias+"/HEAD"); got != "" {
							t.Fatal("absence fabricated symbolic HEAD", got)
						}
					}
				}
			}
			if raw, err := os.ReadFile(marker); err != nil || string(raw) != "invoked" {
				t.Fatal("failure replayed clone", err, string(raw))
			}
		})
	}
}

func TestManagedCloneSymbolicHeadCancellationAndTimeout(t *testing.T) {
	for _, cancelRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "canceled"}[cancelRead], func(t *testing.T) {
			fixture, _, _, _ := cloneFixture(t)
			root := repository(t)
			gitTest(t, root, "remote", "add", "origin", "https://github.com/fixture/repo.git")
			entered := filepath.Join(t.TempDir(), "symbolic-entered")
			body, err := os.ReadFile(fixture.Executable)
			if err != nil {
				t.Fatal(err)
			}
			prefix := "#!/bin/sh\ncase \" $* \" in *' symbolic-ref --quiet refs/remotes/origin/HEAD '*) printf entered > " + fixtureShellQuote(entered) + "; exec sleep 30;; esac\n"
			if err := os.WriteFile(fixture.Executable, []byte(prefix+strings.TrimPrefix(string(body), "#!/bin/sh\n")), 0700); err != nil {
				t.Fatal(err)
			}
			fixture.Timeout = 10 * time.Second
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- provisionManagedCloneRemotes(ctx, fixture, root, "https://github.com/fixture/repo.git", RepositorySpec{Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}}, "origin")
			}()
			deadline := time.Now().Add(30 * time.Second)
			for {
				if _, err := os.Stat(entered); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("symbolic observation not reached")
				}
				time.Sleep(25 * time.Millisecond)
			}
			want := domain.Unavailable
			if cancelRead {
				want = domain.Canceled
				cancel()
			}
			if err := <-result; err == nil || domain.SafeError(err).Code != want {
				t.Fatal("original failure classification lost", err)
			}
			if refs := gitTest(t, root, "for-each-ref", "refs/remotes/upstream/"); refs != "" {
				t.Fatal("failed observation published alias refs", refs)
			}
		})
	}
}

func TestManagedCloneSymbolicHeadLaunchFailure(t *testing.T) {
	fixture, _, _, _ := cloneFixture(t)
	root := repository(t)
	gitTest(t, root, "remote", "add", "origin", "https://github.com/fixture/repo.git")
	body, err := os.ReadFile(fixture.Executable)
	if err != nil {
		t.Fatal(err)
	}
	delegate := filepath.Join(t.TempDir(), "delegate")
	if err := os.WriteFile(delegate, body, 0700); err != nil {
		t.Fatal(err)
	}
	// Remote-add succeeds, then the original executable disappears. The next
	// operation is precisely the primary HEAD read, not another clone or fetch.
	script := "#!/bin/sh\n" + fixtureShellQuote(delegate) + " \"$@\" || exit $?\ncase \" $* \" in *' remote add '*) rm -- \"$0\";; esac\n"
	if err := os.WriteFile(fixture.Executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := provisionManagedCloneRemotes(context.Background(), fixture, root, "https://github.com/fixture/repo.git", RepositorySpec{Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}}, "origin"); err == nil {
		t.Fatal("missing original executable became HEAD absence")
	}
	if refs := gitTest(t, root, "for-each-ref", "refs/remotes/upstream/"); refs != "" {
		t.Fatal("launch failure published alias refs", refs)
	}
}
