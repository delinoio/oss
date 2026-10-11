// SPDX-License-Identifier: Apache-2.0
package workspace

import (
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

func TestPortableRepositoryDirectory(t *testing.T) {
	for _, test := range []struct{ input, expected string }{
		{"oss", "oss"}, {" OSS ", "OSS"}, {"a/b", "a_b"}, {"a\\b", "a_b"}, {"CON", "_CON"}, {"com1.txt", "_com1.txt"}, {"NUL", "_NUL"}, {".git", "_git"}, {".GIT", "_GIT"}, {" ... ", "repository"}, {"repo.  ", "repo"}, {"e\u0301", "é"}, {"a\x00b", "a_b"}, {strings.Repeat("界", 100), strings.Repeat("界", 85)},
	} {
		t.Run(test.input, func(t *testing.T) {
			actual, err := PortableRepositoryDirectory(test.input)
			if err != nil || actual != test.expected || len(actual) > 255 || !utf8.ValidString(actual) {
				t.Fatalf("portable conversion: %q %v", actual, err)
			}
		})
	}
}
func TestNamedDirectoryCollisionsHaveNoGitOrWorkspaceSideEffects(t *testing.T) {
	for _, names := range [][2]string{{"a_b", "a_b"}, {"OSS", "oss"}, {"é", "é"}, {"_git", "_GIT"}, {"manifest.json", "other"}} {
		t.Run(names[0]+"-"+names[1], func(t *testing.T) {
			m, input, marker := managedCloneFixture(t)
			input.Repositories[0].DirectoryName = names[0]
			second := input.Repositories[0]
			second.ID = domain.NewID()
			second.DirectoryName = names[1]
			input.Repositories = append(input.Repositories, second)
			if _, err := m.Prepare(context.Background(), input); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("collision accepted", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("collision launched Git", err)
			}
			if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(input.SessionID))); !os.IsNotExist(err) {
				t.Fatal("collision created workspace", err)
			}
		})
	}
}
func TestNamedDirectoryPrimaryReplayAndTampering(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	input.Repositories[0].DirectoryName = "first"
	second := input.Repositories[0]
	second.ID = domain.NewID()
	second.DirectoryName = "oss"
	input.Repositories = append(input.Repositories, second)
	input.PrimaryRepository = second.ID
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(m.Root, "workspaces", string(input.SessionID), "oss")
	if manifest.PrimaryPath != expected || manifest.Repositories[1].DirectoryName != "oss" {
		t.Fatal("primary did not retain name", manifest.PrimaryPath)
	}
	if err := ValidateResult(input, manifest, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	directory := ""
	err = m.WithTerminalDirectory(context.Background(), input, manifest, ReadRequest{ID: domain.NewID()}, func(path string) error { directory = path; return nil })
	if err != nil || directory != expected {
		t.Fatal("terminal cwd differs", directory, err)
	}

	jobID, executionID := domain.NewID(), domain.NewID()
	lease, err := m.ClaimFirstExecution(context.Background(), jobID, executionID, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if lease.WorkingDirectory() != expected {
		t.Fatal("agent cwd differs", lease.WorkingDirectory())
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), ExecutionPredecessor{JobID: jobID, ExecutionID: executionID}, input, manifest)
	if err != nil {
		t.Fatal("named continuation failed", err)
	}
	if next.WorkingDirectory() != expected {
		t.Fatal("continuation cwd differs")
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Repositories = append([]RepositorySpec(nil), input.Repositories...)
	changed.Repositories[1].DirectoryName = "renamed"
	if _, err := m.Prepare(context.Background(), changed); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("rename rewrote accepted layout", err)
	}
	for _, name := range []string{"renamed", "../escape", "e\u0301", ".git", "manifest.json"} {
		forged := manifest
		forged.Repositories = append([]PreparedRepository(nil), manifest.Repositories...)
		forged.Repositories[1].DirectoryName = name
		if err := ValidateResult(input, forged, runtime.GOOS); err == nil {
			t.Fatal("tampered name accepted", name)
		}
	}
}
func TestNamedDirectoriesCapabilityReadsOnlyOriginalOperands(t *testing.T) {
	input := PrepareRequest{Repositories: []RepositorySpec{{DirectoryName: "oss"}}}
	raw, _ := json.Marshal(input)
	for _, job := range []domain.Job{{Type: domain.PrepareWorkspaceJob, Input: raw}, {Type: domain.ExecuteSessionJob, Input: mustDirectoryJSON(domain.ExecutionJobInput{Preparation: raw})}, {Type: domain.ForkSessionJob, Input: mustDirectoryJSON(domain.ForkJobInput{SourceAssignment: domain.ExecutionJobInput{Preparation: raw}})}} {
		required, err := JobRequiresNamedDirectories(job)
		if err != nil || !required {
			t.Fatal("named operands not gated", job.Type, err)
		}
	}
	required, err := JobRequiresNamedDirectories(domain.Job{Type: domain.PrepareWorkspaceJob, Input: mustDirectoryJSON(PrepareRequest{})})
	if err != nil || required {
		t.Fatal("legacy request requires new capability", err)
	}
}
func mustDirectoryJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestNamedDirectoryReplacementPreservesForeignContent(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	input.Repositories[0].DirectoryName = "oss"
	original, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(foreign, "keep")
	if err := os.WriteFile(marker, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	moved := original.PrimaryPath + "-original"
	if err := os.Rename(original.PrimaryPath, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, original.PrimaryPath); err != nil {
		t.Skip("platform does not allow fixture symlink", err)
	}
	if _, err := m.Prepare(context.Background(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement replay accepted", err)
	}
	if _, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, original); err == nil {
		t.Fatal("replacement gained execution ownership")
	}
	if err := m.DeleteOwnedWorkspace(context.Background(), sidechatDeletionWork(original), false, func() error { return nil }); err == nil {
		t.Fatal("replacement gained deletion ownership")
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "foreign" {
		t.Fatal("foreign content changed", err)
	}
}
func TestOccupiedNamedDestinationFailsBeforeGit(t *testing.T) {
	m, input, marker := managedCloneFixture(t)
	input.Repositories[0].DirectoryName = "oss"
	root := filepath.Join(m.Root, "workspaces", string(input.SessionID))
	if err := os.MkdirAll(filepath.Join(root, "oss"), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "oss", "keep")
	if err := os.WriteFile(foreign, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(context.Background(), input); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("occupied destination was adopted", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("occupied destination launched Git", err)
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "foreign" {
		t.Fatal("foreign destination changed", err)
	}
}
