// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestImportCodexV1HistoryCopiesOnlyValidatedThread(t *testing.T) {
	f := newCheckpointFixture(t)
	f.input.Configuration.Subscription = true
	digest, err := f.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	f.input.ConfigurationDigest = digest
	f.bound.Effective.Provider = "openai"
	f.ref.Subscription = true
	f.ref.ConfigurationDigest = digest
	f.job.Input, _ = json.Marshal(f.input)
	f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	legacyHome := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
	directory := filepath.Join(legacyHome, "sessions", "2026", "10", "14")
	if err := security.PrivateDir(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fmt.Sprintf("rollout-2026-10-14T12-00-00-%s.jsonl", f.bound.Thread.ID))
	content := []byte(fmt.Sprintf(`{"timestamp":"2026-10-14T12:00:00Z","type":"session_meta","payload":{"id":"%s"}}`+"\n", f.bound.Thread.ID))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint, home, err := importCodexV1History(context.Background(), f.root, f.ref)
	if err != nil || checkpoint.Version != 1 {
		t.Fatalf("validated original history rejected: %#v, %v", checkpoint, err)
	}
	target := filepath.Join(home, "sessions", "2026", "10", "14", filepath.Base(path))
	imported, err := security.ReadPrivate(target, maxLegacyCodexHistoryBytes)
	if err != nil || string(imported) != string(content) {
		t.Fatalf("original bytes were not imported: %v", err)
	}
	if _, _, err := importCodexV1History(context.Background(), f.root, f.ref); err != nil {
		t.Fatalf("idempotent import failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"type":"session_meta","payload":{"id":"%s"}}`+"\n", domain.NewID())), 0600); err != nil {
		t.Fatal(err)
	}
	checkpointRecovery(t, func() error {
		_, _, importErr := importCodexV1History(context.Background(), f.root, f.ref)
		return importErr
	}())
}
