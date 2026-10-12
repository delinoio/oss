// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func directoryRefFixture() SessionDirectoryRef {
	return SessionDirectoryRef{GenerationID: NewID(), JobID: NewID(), RequestID: NewID(), ExecutionID: NewID(), RepositoryID: NewID(), RelativePath: "nested/project", CheckpointDigest: strings.Repeat("a", 64)}
}
func TestSessionDirectoryCanonicalSelection(t *testing.T) {
	for _, value := range []string{".", "nested/project", "한글/project", "folder with space"} {
		if err := ValidateSessionDirectoryPath(value); err != nil {
			t.Fatalf("valid relative path %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "/private/root", "../outside", "a/../../outside", "a/../b", "a/./b", "a//b", "a/", "C:/private", "a\\b", "a\x00b"} {
		if ValidateSessionDirectoryPath(value) == nil {
			t.Fatalf("accepted unsafe path %q", value)
		}
	}
}
func TestSessionDirectoryImmutableProofRequiresAllOwners(t *testing.T) {
	original := directoryRefFixture()
	if original.Validate() != nil {
		t.Fatal("valid generation rejected")
	}
	for name, edit := range map[string]func(*SessionDirectoryRef){
		"generation":       func(r *SessionDirectoryRef) { r.GenerationID = "" },
		"job":              func(r *SessionDirectoryRef) { r.JobID = "" },
		"request":          func(r *SessionDirectoryRef) { r.RequestID = "" },
		"execution":        func(r *SessionDirectoryRef) { r.ExecutionID = "" },
		"repository":       func(r *SessionDirectoryRef) { r.RepositoryID = "invalid" },
		"reused owner":     func(r *SessionDirectoryRef) { r.JobID = r.GenerationID },
		"previous self":    func(r *SessionDirectoryRef) { r.PreviousGenerationID = r.GenerationID },
		"invalid previous": func(r *SessionDirectoryRef) { r.PreviousGenerationID = "not-id" },
		"changed root":     func(r *SessionDirectoryRef) { r.RelativePath = "../outside" },
		"digest":           func(r *SessionDirectoryRef) { r.CheckpointDigest = strings.Repeat("A", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := original
			edit(&changed)
			if changed.Validate() == nil {
				t.Fatal("accepted invalid generation")
			}
		})
	}
}
func TestSessionDirectoryResultRequiresExactGenerationAndCleanup(t *testing.T) {
	ref := directoryRefFixture()
	result := SessionDirectoryResult{Version: 1, RequestID: ref.RequestID, GenerationID: ref.GenerationID, ExecutionID: ref.ExecutionID, Checkpoint: ref, CleanupVerified: true}
	if result.Validate() != nil {
		t.Fatal("valid result rejected")
	}
	for name, edit := range map[string]func(*SessionDirectoryResult){
		"version":    func(r *SessionDirectoryResult) { r.Version = 2 },
		"cleanup":    func(r *SessionDirectoryResult) { r.CleanupVerified = false },
		"request":    func(r *SessionDirectoryResult) { r.RequestID = NewID() },
		"generation": func(r *SessionDirectoryResult) { r.GenerationID = NewID() },
		"execution":  func(r *SessionDirectoryResult) { r.ExecutionID = NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			changed := result
			edit(&changed)
			if changed.Validate() == nil {
				t.Fatal("mixed result accepted")
			}
		})
	}
}

func TestSessionDirectorySelectionUsesOriginalPreparedRoots(t *testing.T) {
	session, machine, repo := NewID(), NewID(), NewID()
	a := ExecutionJobInput{SessionID: session, MachineID: machine}
	a.Manifest = []byte(`{"session_id":"` + string(session) + `","machine_id":"` + string(machine) + `","type":"general-chat","state":"ready","repositories":[]}`)
	if !sessionDirectoryRepository(a, "") || sessionDirectoryRepository(a, repo) {
		t.Fatal("General Chat selection acquired a repository")
	}
	a.Manifest = []byte(`{"session_id":"` + string(session) + `","machine_id":"` + string(machine) + `","type":"worktree","state":"ready","repositories":[{"id":"` + string(repo) + `"}]}`)
	if !sessionDirectoryRepository(a, repo) || sessionDirectoryRepository(a, "") || sessionDirectoryRepository(a, NewID()) {
		t.Fatal("repository selection escaped original manifest")
	}
	a.Manifest = []byte(`{"session_id":"` + string(NewID()) + `","machine_id":"` + string(machine) + `","type":"general-chat","state":"ready","repositories":[]}`)
	if sessionDirectoryRepository(a, "") {
		t.Fatal("foreign manifest adopted")
	}
}
