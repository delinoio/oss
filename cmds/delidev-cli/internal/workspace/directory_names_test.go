// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPortableRepositoryDirectoryConversion(t *testing.T) {
	for _, fixture := range []struct{ input, want string }{
		{"oss", "oss"}, {"  OSS  ", "OSS"}, {"a/b", "a_b"}, {"a\\b:*?", "a_b___"},
		{"CON", "_CON"}, {"lpt9.txt", "_lpt9.txt"}, {" ... \t", "repository"},
		{"name. \t", "name"}, {"e\u0301", "é"}, {"a\u0001b", "a_b"},
		{strings.Repeat("界", 100), strings.Repeat("界", 85)},
	} {
		t.Run(fixture.input, func(t *testing.T) {
			got, err := PortableRepositoryDirectory(fixture.input)
			if err != nil || got != fixture.want || len(got) > 255 || !utf8.ValidString(got) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFrozenDirectoryCollisionsRejectBeforeWorkerSideEffects(t *testing.T) {
	for _, names := range [][]string{{"oss", "OSS"}, {"a_b", "a_b"}, {"é", "é"}, {"manifest.json", "other"}} {
		first, second := domain.NewID(), domain.NewID()
		input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.Worktree, PrimaryRepository: first,
			Repositories: []RepositorySpec{{ID: first, DirectoryName: names[0], SourceKind: RemoteCloneSource, RemoteURL: "https://example.invalid/a.git"}, {ID: second, DirectoryName: names[1], SourceKind: RemoteCloneSource, RemoteURL: "https://example.invalid/b.git"}}}
		root := filepath.Join(t.TempDir(), "worker")
		manager := &Manager{Root: root, NamedDirectories: true}
		if _, err := manager.Prepare(context.Background(), input); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("collision accepted", err)
		}
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatal("collision changed the filesystem", err)
		}
	}
}

func TestNamedDirectoryRequiresNegotiationAndLegacyOmission(t *testing.T) {
	input := PrepareRequest{Repositories: []RepositorySpec{{ID: domain.NewID()}}}
	raw, _ := json.Marshal(input)
	if bytes.Contains(raw, []byte("directory_name")) || input.RequiresNamedDirectories() {
		t.Fatal("legacy request changed")
	}
	legacy, _ := json.Marshal(PreparedRepository{ID: input.Repositories[0].ID})
	if bytes.Contains(legacy, []byte("directory_name")) || RepositoryDirectory(input.Repositories[0].ID, "") != string(input.Repositories[0].ID) {
		t.Fatal("legacy result changed")
	}
	input.Repositories[0].DirectoryName = "oss"
	m := &Manager{Root: filepath.Join(t.TempDir(), "worker")}
	if err := m.requireNamedDirectories(input); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unsupported layout accepted", err)
	}
}

func TestNamedManagedWorkspaceRetainsLayoutAcrossRecoverySnapshotAndFork(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	m.NamedDirectories = true
	input.Repositories[0].DirectoryName = "oss"
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(manifest.PrimaryPath) != "oss" || manifest.Repositories[0].DirectoryName != "oss" || ValidateResult(input, manifest, runtime.GOOS) != nil {
		t.Fatal("named result mismatch")
	}
	if err := m.WithTerminalDirectory(context.Background(), input, manifest, ReadRequest{ID: domain.NewID()}, func(cwd string) error {
		if cwd != manifest.PrimaryPath {
			t.Fatal("terminal cwd changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := m.Prepare(context.Background(), input)
	if err != nil || manifestDigest(again) != manifestDigest(manifest) {
		t.Fatal("ready replay changed", err)
	}
	if recovered, err := m.Recover(context.Background(), recoveryInput(input), false); err != nil || recovered.Outcome != RecoveredReady {
		t.Fatal("named recovery failed", err)
	}
	tampered := manifest
	tampered.Repositories = append([]PreparedRepository(nil), manifest.Repositories...)
	tampered.Repositories[0].DirectoryName = "other"
	if ValidateResult(input, tampered, runtime.GOOS) == nil {
		t.Fatal("tampered name accepted")
	}
	previous := closeFirstExecution(t, m, input, manifest)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "untracked.txt"), []byte("retained bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	storage := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StoragePreview, Preparation: input, Manifest: manifest}
	preview := storageDo(t, m, storage)
	storage.Action, storage.OperationID, storage.SnapshotID, storage.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	cleaned := storageDo(t, m, storage)
	if _, err := os.Stat(filepath.Join(m.snapshotPath(storage.SnapshotID), "workspace", string(input.Repositories[0].ID), ".git")); err != nil {
		t.Fatal("snapshot lost UUID Git indexing", err)
	}
	storage.Action, storage.OperationID, storage.SnapshotDigest = StorageRestore, domain.NewID(), cleaned.Snapshot.SHA256
	storageDo(t, m, storage)
	if data, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, "untracked.txt")); err != nil || string(data) != "retained bytes" {
		t.Fatal("named live layout not restored", err)
	}
	lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest)
	if err != nil {
		t.Fatal("restored named execution failed", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	fork, err := m.ForkPreparation(context.Background(), manifest, domain.NewID(), domain.Worktree)
	if err != nil || fork.Repositories[0].DirectoryName != "oss" {
		t.Fatal("Fork lost frozen name", err)
	}
	observed, err := m.InspectForkSnapshot(context.Background(), manifest, fork)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.PrepareFork(context.Background(), fork, observed)
	if err != nil || filepath.Base(child.PrimaryPath) != "oss" || child.Repositories[0].DirectoryName != "oss" {
		t.Fatal("Fork layout changed", err)
	}
	if err := m.cleanup(context.Background(), filepath.Dir(child.PrimaryPath), child); err != nil {
		t.Fatal("named child cleanup failed", err)
	}
}

func TestForkRequiresNamedDirectoriesCoversSidechatAndIndependentSources(t *testing.T) {
	repository := domain.NewID()
	for _, test := range []struct {
		name    string
		purpose domain.ForkPurpose
		kind    domain.WorkspaceType
		frozen  string
		want    bool
	}{
		{"sidechat named worktree", domain.SidechatFork, domain.Worktree, "oss", true},
		{"sidechat unnamed worktree", domain.SidechatFork, domain.Worktree, "", false},
		{"sidechat local", domain.SidechatFork, domain.Local, "", false},
		{"independent named worktree", domain.IndependentFork, domain.Worktree, "oss", true},
		{"independent unnamed worktree", domain.IndependentFork, domain.Worktree, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			preparation, err := json.Marshal(PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: test.kind, PrimaryRepository: repository, Repositories: []RepositorySpec{{ID: repository, DirectoryName: test.frozen}}})
			if err != nil {
				t.Fatal(err)
			}
			input := domain.ForkJobInput{Purpose: test.purpose, Workspace: test.kind}
			input.SourceAssignment.Preparation = preparation
			got, err := ForkRequiresNamedDirectories(input)
			if err != nil || got != test.want {
				t.Fatalf("named requirement = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	input := domain.ForkJobInput{Purpose: domain.SidechatFork}
	input.SourceAssignment.Preparation = []byte(`{"unknown":`)
	if _, err := ForkRequiresNamedDirectories(input); err == nil {
		t.Fatal("undecodable Sidechat source preparation was accepted")
	}
}
