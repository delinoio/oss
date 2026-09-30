// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestForkRolloutProofRejectsForeignLinksChangesAndCancellation(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(home, "private-native-home")
	if security.PrivateDir(home) != nil || security.PrivateDir(filepath.Join(home, "sessions")) != nil {
		t.Fatal("private home")
	}
	path := filepath.Join(home, "sessions", "rollout.jsonl")
	if err := os.WriteFile(path, []byte("original native rollout\n"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := forkRolloutDigest(context.Background(), home, path)
	if err != nil {
		t.Fatal(err)
	}
	source := &ForkSource{home: home, path: path, fileDigest: digest}
	if source.Verify(context.Background()) != nil {
		t.Fatal("original proof rejected")
	}
	if _, err := forkRolloutDigest(context.Background(), home, filepath.Join(home, "outside.jsonl")); err == nil {
		t.Fatal("foreign rollout accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if source.Verify(canceled) == nil {
		t.Fatal("canceled inspection accepted")
	}
	if err := os.WriteFile(path, []byte("changed native rollout\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if source.Verify(context.Background()) == nil {
		t.Fatal("changed source accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "other.jsonl"), path); err != nil {
		t.Skip("native link fixture unavailable")
	}
	if source.Verify(context.Background()) == nil {
		t.Fatal("linked source accepted")
	}
}

func TestForkRejectsActiveForeignAndChildMetadata(t *testing.T) {
	id, turn := domain.NewID(), domain.NewID()
	checkpoint := ContinuationCheckpoint{ThreadID: id, SessionID: id, TurnID: turn, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: EffectiveSettings{Cwd: "/fixture", Provider: APIProvider}, Inputs: []HistoricalInput{{ID: domain.NewID(), PromptDigest: sha256.Sum256([]byte("fixture"))}}}
	wire := threadWire{ID: id, SessionID: id, Cwd: "/fixture", ModelProvider: APIProvider, Status: ThreadStatus{Type: ThreadIdle}, HistoryMode: LegacyHistory}
	if !forkableMetadata(wire, checkpoint) {
		t.Fatal("settled metadata rejected")
	}
	active := wire
	active.Status.Type = ThreadActive
	child := wire
	parent := domain.NewID()
	child.ParentThreadID = &parent
	foreign := wire
	foreign.ID = domain.NewID()
	paginated := wire
	paginated.HistoryMode = PaginatedHistory
	for _, value := range []threadWire{active, child, foreign, paginated} {
		if forkableMetadata(value, checkpoint) {
			t.Fatal("unsupported native root accepted")
		}
	}
}

func TestForkDefaultsRejectPolicyDrift(t *testing.T) {
	original := EffectiveSettings{Model: "fixture", Provider: APIProvider, Sandbox: Sandbox{Type: WorkspaceWrite}}
	changed := original
	changed.Cwd = "/child"
	changed.WorkspaceRoots = []string{"/child"}
	if !sameForkDefaults(original, changed) {
		t.Fatal("path remapping rejected")
	}
	changed.Sandbox.NetworkAccess = true
	if sameForkDefaults(original, changed) {
		t.Fatal("changed network permission accepted")
	}
	changed = original
	changed.Sandbox.ExcludeSlashTmp = true
	if sameForkDefaults(original, changed) {
		t.Fatal("changed temporary-file permission accepted")
	}
}
