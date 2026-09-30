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
	m := manager(t)
	input, _ := snapshotRequest(t, m, true)
	input.Action = StorageCreate
	input.SnapshotID = domain.NewID()
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
	if _, err := os.Lstat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
		t.Fatal("partial snapshot published")
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
