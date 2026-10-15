// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestFindLegacyThreadHistoryChecksNativeIdentity(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	thread := domain.NewID()
	directory := filepath.Join(home, "sessions", "2026", "10", "14")
	if err := security.PrivateDir(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fmt.Sprintf("rollout-2026-10-14T12-00-00-%s.jsonl", thread))
	write := func(id domain.ID) {
		t.Helper()
		content := fmt.Sprintf(`{"timestamp":"2026-10-14T12:00:00Z","type":"session_meta","payload":{"id":"%s"}}`+"\n", id)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(thread)
	proof, err := FindLegacyThreadHistory(context.Background(), home, thread, "")
	if err != nil || proof.Path != path || proof.ThreadID != thread {
		t.Fatalf("exact legacy history rejected: %#v, %v", proof, err)
	}
	if _, err := FindLegacyThreadHistory(context.Background(), home, thread, filepath.Join(home, "foreign.jsonl")); err == nil {
		t.Fatal("foreign checkpoint path accepted")
	}
	write(domain.NewID())
	if _, err := FindLegacyThreadHistory(context.Background(), home, thread, ""); err == nil {
		t.Fatal("foreign session metadata accepted")
	}
}
