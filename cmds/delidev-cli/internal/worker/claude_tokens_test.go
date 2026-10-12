// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/base64"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedClaudeTokenHistoryDoesNotRetainCredentials(t *testing.T) {
	token := []byte("synthetic-history-token")
	for _, mode := range []string{"clean", "raw", "encoded", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "runtime")
			if e := security.PrivateDir(root); e != nil {
				t.Fatal(e)
			}
			raw := []byte(`{"type":"message","text":"safe fixture history"}`)
			if mode == "raw" {
				raw = token
			}
			if mode == "encoded" {
				raw = []byte(base64.RawURLEncoding.EncodeToString(token))
			}
			if e := security.WriteAtomic(filepath.Join(root, "history.jsonl"), raw); e != nil {
				t.Fatal(e)
			}
			if mode == "symlink" {
				if e := os.Symlink("history.jsonl", filepath.Join(root, "foreign")); e != nil {
					t.Fatal(e)
				}
			}
			e := scanManagedClaudeTokenRuntime(root, token)
			if (e == nil) != (mode == "clean") {
				t.Fatal("retained history proof", mode, e)
			}
		})
	}
}
