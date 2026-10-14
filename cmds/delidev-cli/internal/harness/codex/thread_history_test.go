// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestVerifyThreadHistoryRequiresPrivateExactBytes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	path := filepath.Join(home, "sessions", "2026", "10", "14", "rollout.jsonl")
	if err := security.PrivateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	content := []byte("private native history\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	proof := ThreadHistoryProof{ThreadID: domain.NewID(), Path: path, Digest: hex.EncodeToString(digest[:])}
	if err := VerifyThreadHistory(context.Background(), home, proof); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed native history\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyThreadHistory(context.Background(), home, proof); err == nil {
		t.Fatal("changed native bytes accepted")
	}
	proof.Path = filepath.Join(t.TempDir(), "foreign.jsonl")
	if err := VerifyThreadHistory(context.Background(), home, proof); err == nil {
		t.Fatal("foreign native path accepted")
	}
}
