// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func snapshotRequest(t *testing.T, m *Manager, multi bool) (StorageRequest, []string) {
	t.Helper()
	var diagnostics bytes.Buffer
	m.Logger = slog.New(slog.NewTextHandler(&diagnostics, nil))
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(diagnostics.String())
		}
	})
	first, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(first)
	sources := []string{first}
	if multi {
		second, err := filepath.EvalSymlinks(repository(t))
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, second)
		input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: second, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
	}
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: input, Manifest: manifest}, sources
}
func storageDo(t *testing.T, m *Manager, input StorageRequest) StorageResult {
	t.Helper()
	result, err := m.Storage(context.Background(), input)
	if err != nil {
		t.Fatal(input.Action, err)
	}
	return result
}

func TestSnapshotGitAtLongPrivatePath(t *testing.T) {
	m := manager(t)
	input, sources := snapshotRequest(t, m, false)
	repo := input.Manifest.Repositories[0]
	// A source setting must not defeat the command-local Windows opt-in. The
	// independent copy needs to work with no ambient global/system configuration.
	gitTest(t, sources[0], "config", "core.longpaths", "false")
	if err := os.WriteFile(filepath.Join(repo.Path, "tracked.txt"), []byte("staged long-path snapshot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo.Path, "add", "tracked.txt")
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Windows CreateProcess limits cwd independently of Git's object paths.
	// Match snapshot staging: a supported cwd with objects beyond MAX_PATH.
	for len(target) < 230 {
		target = filepath.Join(target, strings.Repeat("x", min(60, 230-len(target))))
	}
	object := filepath.Join(target, ".git", "objects", repo.BaseCommit[:2], repo.BaseCommit[2:])
	if len(object) <= 280 {
		t.Fatal("fixture did not exceed the native Git path limit")
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.copySnapshotGit(context.Background(), input.Preparation.SessionID, repo, target); err != nil {
		t.Fatal("independent Git copy at a long path", err)
	}
	if actual := gitTest(t, target, "-c", "core.longpaths=true", "show", ":tracked.txt"); actual != "staged long-path snapshot" {
		t.Fatal("staged content was not retained", actual)
	}
	if actual := gitTest(t, sources[0], "config", "core.longpaths"); actual != "false" {
		t.Fatal("source configuration changed", actual)
	}
	if err := os.Rename(sources[0], sources[0]+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(sources[0]+"-offline", sources[0])
	if err := m.validateSnapshotGit(context.Background(), input.Preparation.SessionID, repo, target); err != nil {
		t.Fatal("long-path copy depends on original Git data", err)
	}
}

func TestWorkspaceSnapshotFaithfullyRestoresEveryRepository(t *testing.T) {
	m := manager(t)
	input, sources := snapshotRequest(t, m, true)
	type expected struct{ head, staged, unstaged string }
	facts := []expected{}
	remotes := []string{}
	for _, source := range sources {
		remote := filepath.Join(t.TempDir(), "remote.git")
		gitTest(t, source, "init", "--bare", remote)
		gitTest(t, source, "remote", "add", "snapshot-test", remote)
		remotes = append(remotes, remote)
	}
	previous := closeFirstExecution(t, m, input.Preparation, input.Manifest)
	for _, repo := range input.Manifest.Repositories {
		path := repo.Path
		os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("unpublished commit\n"), 0600)
		gitTest(t, path, "add", "tracked.txt")
		gitTest(t, path, "commit", "-m", "private unpushed commit")
		os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("staged change\n"), 0600)
		gitTest(t, path, "add", "tracked.txt")
		os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("unstaged change\n"), 0600)
		os.WriteFile(filepath.Join(path, ".gitignore"), []byte("ignored.bin\n"), 0600)
		os.WriteFile(filepath.Join(path, "ignored.bin"), []byte("private ignored bytes\x00"), 0600)
		os.WriteFile(filepath.Join(path, "untracked.txt"), []byte("untracked bytes"), 0755)
		if runtime.GOOS != "windows" {
			if err := os.Symlink("../../outside-private", filepath.Join(path, "escape")); err != nil {
				t.Fatal(err)
			}
		}
		facts = append(facts, expected{gitTest(t, path, "rev-parse", "HEAD"), gitTest(t, path, "diff", "--cached", "--binary"), gitTest(t, path, "diff", "--binary")})
	}
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.OperationID = domain.NewID()
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	if cleaned.Snapshot == nil || cleaned.RemovedSourceBytes != preview.SourceBytes || cleaned.RetainedSnapshotBytes < cleaned.Snapshot.SizeBytes {
		t.Fatal("missing complete retained-byte accounting", cleaned)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(input.Preparation.SessionID))); !os.IsNotExist(err) {
		t.Fatal("workspace was not removed", err)
	}
	// Recovery uses the independent Git object stores, even if every original
	// source checkout and its shared object/administrative store is unavailable.
	for _, source := range sources {
		if err := os.Rename(source, source+"-offline"); err != nil {
			t.Fatal(err)
		}
		defer os.Rename(source+"-offline", source)
	}
	input.Action = StorageRestore
	input.OperationID = domain.NewID()
	input.SnapshotDigest = cleaned.Snapshot.SHA256
	restored := storageDo(t, m, input)
	if !restored.CleanupVerified {
		t.Fatal("partial restoration reported")
	}
	for i, repo := range input.Manifest.Repositories {
		path := repo.Path
		if got := gitTest(t, path, "rev-parse", "HEAD"); got != facts[i].head {
			t.Fatal("unpushed commit changed", got)
		}
		if gitTest(t, path, "diff", "--cached", "--binary") != facts[i].staged || gitTest(t, path, "diff", "--binary") != facts[i].unstaged {
			t.Fatal("index/worktree changes were not preserved")
		}
		ignored, err := os.ReadFile(filepath.Join(path, "ignored.bin"))
		if err != nil || string(ignored) != "private ignored bytes\x00" {
			t.Fatal("ignored data lost", err)
		}
		untracked, err := os.ReadFile(filepath.Join(path, "untracked.txt"))
		if err != nil || string(untracked) != "untracked bytes" {
			t.Fatal("untracked data lost", err)
		}
		if runtime.GOOS != "windows" {
			link, err := os.Readlink(filepath.Join(path, "escape"))
			if err != nil || link != "../../outside-private" {
				t.Fatal("escaping link changed or dereferenced", err)
			}
			info, _ := os.Stat(filepath.Join(path, "untracked.txt"))
			if info.Mode().Perm() != 0755 {
				t.Fatal("executable mode lost")
			}
		}
		if got := gitTest(t, path, "rev-parse", "--absolute-git-dir"); !strings.HasSuffix(filepath.ToSlash(got), "/.git") {
			t.Fatal("restoration depends on shared source", got)
		}
	}
	lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input.Preparation, input.Manifest)
	if err != nil {
		t.Fatal("restored workspace lost its original logical identity", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	for _, remote := range remotes {
		if refs := gitTest(t, remote, "for-each-ref"); refs != "" {
			t.Fatal("snapshot storage pushed to a remote", refs)
		}
	}
	input.Action = StorageInspect
	input.OperationID = domain.NewID()
	storageDo(t, m, input)
	input.Action = StorageDelete
	input.OperationID = domain.NewID()
	deleted := storageDo(t, m, input)
	if !deleted.Snapshot.Deleted {
		t.Fatal("snapshot deletion unconfirmed")
	}
}
func TestSnapshotSecondRepositoryDiskFailurePreservesAllSources(t *testing.T) {
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		t.Run(string(action), func(t *testing.T) {
			m := manager(t)
			input, _ := snapshotRequest(t, m, true)
			preview := storageDo(t, m, input)
			input.Action = action
			input.OperationID = domain.NewID()
			input.SnapshotID = domain.NewID()
			if action == StorageCleanup {
				input.PreviewDigest = preview.PreviewDigest
			}
			second := string(input.Manifest.Repositories[1].ID)
			reachedSecond := false
			m.storageCopyFault = func(path string) error {
				if filepath.Base(path) == second {
					reachedSecond = true
					return snapshotDiskFullError()
				}
				return nil
			}
			if _, err := m.Storage(context.Background(), input); !reachedSecond || domain.SafeError(err).Code != domain.ResourceExhausted {
				t.Fatal("disk full did not retain a typed capacity problem", err)
			}
			for _, repo := range input.Manifest.Repositories {
				if _, err := os.ReadFile(filepath.Join(repo.Path, "tracked.txt")); err != nil {
					t.Fatal("a source was removed", err)
				}
			}
			// Compare the complete files and Git state, including both repositories,
			// so cleanup cannot silently mutate surviving sources before it fails.
			observation, err := m.storageObservation(context.Background(), input)
			if err != nil || observation.Whole.Bytes != preview.SourceBytes || observation.Digest != preview.PreviewDigest {
				t.Fatal("disk exhaustion changed original workspace or Git data", err)
			}
			if _, err := os.Lstat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
				t.Fatal("partial snapshot published")
			}
		})
	}
}
func TestSnapshotCancellationStalePreviewAndDestinationConflict(t *testing.T) {
	m := manager(t)
	input, _ := snapshotRequest(t, m, false)
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.SnapshotID = domain.NewID()
	input.OperationID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Storage(ctx, input); err == nil {
		t.Fatal("canceled cleanup succeeded")
	}
	source := input.Manifest.PrimaryPath
	os.WriteFile(filepath.Join(source, "new.txt"), []byte("later"), 0600)
	if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale preview accepted", err)
	}
	input.Action = StoragePreview
	input.OperationID = domain.NewID()
	input.SnapshotID = ""
	preview = storageDo(t, m, input)
	input.Action = StorageCleanup
	input.OperationID = domain.NewID()
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	input.Action = StorageRestore
	input.OperationID = domain.NewID()
	input.SnapshotDigest = cleaned.Snapshot.SHA256
	root := filepath.Join(m.Root, "workspaces", string(input.Preparation.SessionID))
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "foreign"), []byte("keep"), 0600)
	if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("occupied destination accepted", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "foreign")); string(raw) != "keep" {
		t.Fatal("conflicting destination overwritten")
	}
}
func TestSnapshotBlocksActiveExecutionAndPreservesLocal(t *testing.T) {
	m := manager(t)
	input, _ := snapshotRequest(t, m, false)
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input.Preparation, input.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Storage(context.Background(), input); err == nil {
		t.Fatal("active execution snapshot accepted")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	local := input
	local.Preparation.Type = domain.Local
	if _, err := m.Storage(context.Background(), local); err == nil {
		t.Fatal("Local cleanup accepted")
	}
}
func TestGeneralChatSnapshotRestoresAsOneWorkspace(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(manifest.PrimaryPath, "note"), []byte("private note"), 0600)
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.OperationID = domain.NewID()
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	input.Action = StorageRestore
	input.OperationID = domain.NewID()
	input.SnapshotDigest = cleaned.Snapshot.SHA256
	storageDo(t, m, input)
	raw, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, "note"))
	if err != nil || string(raw) != "private note" {
		t.Fatal("General Chat data lost", err)
	}
}

func recoveryRequest(original StorageRequest) StorageRequest {
	claim := StorageJournalClaim{JobID: original.OperationID, InstanceID: domain.NewID(), Revision: 1, AssignmentDigest: strings.Repeat("a", 64)}
	return StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StorageRecover, Preparation: original.Preparation, Manifest: original.Manifest, SnapshotID: original.SnapshotID, PreviousState: original.PreviousState, Recovery: &StorageRecovery{Original: original, Claims: []StorageJournalClaim{claim}, InstanceID: claim.InstanceID, Revision: claim.Revision, AssignmentDigest: claim.AssignmentDigest}}
}
func TestSnapshotRecoveryInspectsLostCompletionWithoutRecreating(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600)
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.OperationID = domain.NewID()
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	output := storageDo(t, m, input)
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.RecoveredJobState != domain.JobSucceeded || recovered.WorkspaceState != domain.WorkspaceStored || recovered.Snapshot.SHA256 != output.Snapshot.SHA256 {
		t.Fatal("cleanup completion not reconciled", recovered)
	}
	input.PreviousState = domain.WorkspaceStored
	input.PreviousSnapshotID = input.SnapshotID
	input.Action = StorageRestore
	input.OperationID = domain.NewID()
	input.SnapshotDigest = output.Snapshot.SHA256
	storageDo(t, m, input)
	recovered = storageDo(t, m, recoveryRequest(input))
	if recovered.WorkspaceState != domain.WorkspacePresent || recovered.RecoveredJobState != domain.JobSucceeded {
		t.Fatal("restore completion not reconciled")
	}
	os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("later"), 0600)
	if _, err := m.Storage(context.Background(), recoveryRequest(input)); err == nil {
		t.Fatal("changed restored files accepted")
	}
	raw, _ := os.ReadFile(filepath.Join(manifest.PrimaryPath, "keep"))
	if string(raw) != "later" {
		t.Fatal("recovery repeated restoration")
	}
	input.PreviousState = domain.WorkspacePresent
	input.Action = StorageDelete
	input.OperationID = domain.NewID()
	input.SnapshotMetadata = output.Snapshot
	storageDo(t, m, input)
	recovered = storageDo(t, m, recoveryRequest(input))
	if recovered.RecoveredJobState != domain.JobSucceeded || !recovered.Snapshot.Deleted {
		t.Fatal("deletion not reconciled")
	}
}
func TestSnapshotRecoveryContinuesOnlyVerifiedClaimedRemoval(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600)
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StorageCreate, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID()}
	output := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.PreviewDigest = output.PreviewDigest
	root := filepath.Join(m.Root, "workspaces", string(prepare.SessionID))
	removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
	if err := m.retainRemovalIntent(context.Background(), input, root); err != nil {
		t.Fatal(err)
	}
	if err := renameStorage(root, removal); err != nil {
		t.Fatal(err)
	}
	// A crash after deleting one known file leaves a subset of the original
	// synchronized removal inventory. Unknown later files must retain recovery.
	os.Remove(filepath.Join(removal, "chat", "keep"))
	os.WriteFile(filepath.Join(removal, "foreign"), []byte("preserve"), 0600)
	if _, err := m.Storage(context.Background(), recoveryRequest(input)); err == nil {
		t.Fatal("foreign retained data was deleted")
	}
	if raw, _ := os.ReadFile(filepath.Join(removal, "foreign")); string(raw) != "preserve" {
		t.Fatal("foreign removal data lost")
	}
	os.Remove(filepath.Join(removal, "foreign"))
	result := storageDo(t, m, recoveryRequest(input))
	if result.WorkspaceState != domain.WorkspaceStored || !result.CleanupVerified {
		t.Fatal("partial removal not reconciled")
	}
	if _, err := os.Lstat(removal); !os.IsNotExist(err) {
		t.Fatal("removal retained", err)
	}
}
func TestSnapshotCancellationDuringSecondCopyPreservesSources(t *testing.T) {
	m := manager(t)
	input, _ := snapshotRequest(t, m, true)
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.SnapshotID = domain.NewID()
	input.OperationID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := string(input.Manifest.Repositories[1].ID)
	reachedSecond := false
	m.storageCopyFault = func(path string) error {
		if filepath.Base(path) == second {
			reachedSecond = true
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if _, err := m.Storage(ctx, input); !reachedSecond || domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("second-repository cancellation was not reached or retained", err)
	}
	for _, repo := range input.Manifest.Repositories {
		if _, err := os.ReadFile(filepath.Join(repo.Path, "tracked.txt")); err != nil {
			t.Fatal("source removed", err)
		}
	}
	if _, err := os.Lstat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
		t.Fatal("canceled snapshot published")
	}
}

func TestSnapshotCancellationAfterPublicationRetainsRecoveryOwnership(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600)
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action = StorageCleanup
	input.OperationID = domain.NewID()
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.storageAfterSnapshot = cancel
	result, err := m.Storage(ctx, input)
	if domain.SafeError(err).Code != domain.RecoveryRequired || result.Snapshot == nil {
		t.Fatal("published snapshot lost its recovery ownership", err)
	}
	if raw, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, "keep")); err != nil || string(raw) != "original" {
		t.Fatal("canceled cleanup removed source", err)
	}
	m.storageAfterSnapshot = nil
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.WorkspaceState != domain.WorkspacePresent || recovered.RecoveredJobState != domain.JobFailed || recovered.Snapshot == nil || recovered.Snapshot.SHA256 != result.Snapshot.SHA256 {
		t.Fatal("recovery omitted retained published snapshot", recovered)
	}
}

func TestSnapshotFailedRestoreCleansOwnedStaging(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint("canceled=", canceled), func(t *testing.T) {
			m := manager(t)
			prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
			manifest, err := m.Prepare(context.Background(), prepare)
			if err != nil {
				t.Fatal(err)
			}
			input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
			preview := storageDo(t, m, input)
			input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
			cleaned := storageDo(t, m, input)
			input.Action, input.OperationID, input.PreviousState, input.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, cleaned.Snapshot.SHA256
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.storageRestoreCopyFault = func(staging string) error {
				if err := os.WriteFile(filepath.Join(staging, "partial"), []byte("partial copied data"), 0600); err != nil {
					t.Fatal(err)
				}
				if canceled {
					cancel()
					return context.Canceled
				}
				return snapshotDiskFullError()
			}
			_, err = m.Storage(ctx, input)
			want := domain.ResourceExhausted
			if canceled {
				want = domain.Canceled
			}
			if domain.SafeError(err).Code != want {
				t.Fatal("wrong failure classification", err)
			}
			staging := filepath.Join(m.Root, "snapshot-staging", string(input.OperationID))
			if _, err := os.Lstat(staging); !os.IsNotExist(err) {
				t.Fatal("failed restore leaked scratch", err)
			}
			if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(prepare.SessionID))); !os.IsNotExist(err) {
				t.Fatal("failed restore published a workspace", err)
			}
			if _, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID); err != nil {
				t.Fatal("only recoverable copy changed", err)
			}
			m.storageRestoreCopyFault = nil
			input.OperationID = domain.NewID()
			storageDo(t, m, input)
		})
	}
}

func TestSnapshotRejectsNestedGitAdministration(t *testing.T) {
	for _, name := range []string{".git", ".GiT"} {
		for _, directory := range []bool{false, true} {
			t.Run(fmt.Sprint(name, "/directory=", directory), func(t *testing.T) {
				m := manager(t)
				prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
				manifest, err := m.Prepare(context.Background(), prepare)
				if err != nil {
					t.Fatal(err)
				}
				nested := filepath.Join(manifest.PrimaryPath, "ignored-checkout", name)
				if err := os.MkdirAll(filepath.Dir(nested), 0700); err != nil {
					t.Fatal(err)
				}
				external := t.TempDir()
				if directory {
					if err := os.MkdirAll(filepath.Join(nested, "objects", "info"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(nested, "objects", "info", "alternates"), []byte(external+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(nested, "config"), []byte("[include]\npath = "+external+"/config\n"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(nested, []byte("gitdir: "+external+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				root := filepath.Join(m.Root, "workspaces", string(prepare.SessionID))
				before, err := walkSnapshot(context.Background(), root, "", nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, action := range []StorageAction{StoragePreview, StorageCreate, StorageCleanup} {
					input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: action, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID(), PreviewDigest: strings.Repeat("a", 64)}
					if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.Unsupported {
						t.Fatal("nested Git administration accepted", action, err)
					}
					if _, err := os.Stat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
						t.Fatal("unsupported snapshot published", err)
					}
				}
				after, err := walkSnapshot(context.Background(), root, "", nil)
				if err != nil || inventoryDigest(before) != inventoryDigest(after) {
					t.Fatal("rejected nested checkout changed", err)
				}
			})
		}
	}
}

func TestSnapshotAbsentRemovalRecoveryRequiresOriginalIntent(t *testing.T) {
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		t.Run(string(action), func(t *testing.T) {
			m := manager(t)
			prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
			manifest, err := m.Prepare(context.Background(), prepare)
			if err != nil {
				t.Fatal(err)
			}
			input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StorageCreate, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID()}
			created := storageDo(t, m, input)
			input.Action, input.PreviewDigest, input.SnapshotDigest, input.SnapshotMetadata = action, created.PreviewDigest, created.Snapshot.SHA256, created.Snapshot
			disappeared := m.snapshotPath(input.SnapshotID)
			if action == StorageCleanup {
				disappeared = filepath.Join(m.Root, "workspaces", string(prepare.SessionID))
			}
			// Model interruption before intent publication followed by external
			// loss. No private namespace claim or removal authority was retained.
			if err := os.RemoveAll(disappeared); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unproven absence settled as removal", err)
			}
		})
	}
}

func TestSnapshotCleanupNeverAdoptsRacedSourceWrites(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manifest.PrimaryPath, "keep")
	if err := os.WriteFile(path, []byte("captured original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	raced := false
	m.storageBeforeRemovalClaim = func() {
		raced = true
		if err := os.WriteFile(path, []byte("new uncaptured user bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := m.Storage(context.Background(), input)
	if !raced || domain.SafeError(err).Code != domain.RecoveryRequired || result.RemovedSourceBytes != 0 {
		t.Fatal("raced source authorized removal", result, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "new uncaptured user bytes" {
		t.Fatal("uncaptured source was not restored", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "workspace-removals", string(input.OperationID))); !os.IsNotExist(err) {
		t.Fatal("whole source claim was not restored", err)
	}
	raw, err = os.ReadFile(filepath.Join(m.snapshotPath(input.SnapshotID), "workspace", "chat", "keep"))
	if err != nil || string(raw) != "captured original" {
		t.Fatal("snapshot adopted uncaptured bytes", err)
	}
	var intent storageRemovalIntent
	raw, err = os.ReadFile(m.removalIntentPath(input.OperationID))
	if err != nil || domain.Decode(raw, &intent) != nil {
		t.Fatal("original intent missing", err)
	}
	for _, entry := range intent.Inventory.Entries {
		if entry.Path == "chat/keep" && entry.Size != uint64(len("captured original")) {
			t.Fatal("deletion authority adopted raced data")
		}
	}
	m.storageBeforeRemovalClaim = nil
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.WorkspaceState != domain.WorkspacePresent || recovered.RecoveredJobState != domain.JobFailed || recovered.Snapshot == nil {
		t.Fatal("raced cleanup did not preserve both copies", recovered)
	}
}

func TestSnapshotMaximumInventoryRemainsDeletable(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	// The workspace already contains manifest.json and the chat directory.
	for i := 0; i < MaxSnapshotEntries-2; i++ {
		if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, fmt.Sprintf("entry-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StorageCreate, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID()}
	created := storageDo(t, m, input)
	pinned, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID)
	if err != nil || len(pinned.Inventory.Entries) != MaxSnapshotEntries {
		t.Fatal("fixture did not publish maximum valid inventory", err)
	}
	input.Action, input.OperationID, input.SnapshotDigest, input.SnapshotMetadata = StorageDelete, domain.NewID(), created.Snapshot.SHA256, created.Snapshot
	deleted := storageDo(t, m, input)
	if !deleted.Snapshot.Deleted {
		t.Fatal("maximum valid snapshot was not deletable")
	}
	raw, err := os.ReadFile(m.removalIntentPath(input.OperationID))
	var intent storageRemovalIntent
	if err != nil || domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || len(intent.Inventory.Entries) != MaxSnapshotEntries+2 {
		t.Fatal("wrapper capacity missing from immutable intent", err)
	}
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.RecoveredJobState != domain.JobSucceeded || !recovered.Snapshot.Deleted {
		t.Fatal("maximum deletion lost completion could not recover")
	}
	if _, err := os.Stat(filepath.Join(manifest.PrimaryPath, "entry-0000")); err != nil {
		t.Fatal("snapshot deletion removed the live source", err)
	}
}

func TestSnapshotCancellationBeforeRemovalClaimPreservesBothCopies(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.storageBeforeRemovalClaim = cancel
	result, err := m.Storage(ctx, input)
	if domain.SafeError(err).Code != domain.RecoveryRequired || result.Snapshot == nil || result.RemovedSourceBytes != 0 {
		t.Fatal("cancellation crossed source claim", err)
	}
	if _, err := os.Stat(manifest.PrimaryPath); err != nil {
		t.Fatal("cancellation removed source", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "workspace-removals", string(input.OperationID))); !os.IsNotExist(err) {
		t.Fatal("cancellation claimed source", err)
	}
	if _, err := os.Stat(m.removalIntentPath(input.OperationID)); err != nil {
		t.Fatal("unreported intent lost", err)
	}
}
