// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestManagedUnusedAuthenticationRequiresAbsentOrIdenticalFileAndCleanHome(t *testing.T) {
	for _, mode := range []string{"absent-clean", "absent-pending-token", "absent-symlink", "published-identical", "published-changed", "missing-home"} {
		t.Run(mode, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			auth := filepath.Join(home, "auth.json")
			switch mode {
			case "absent-pending-token":
				parsed, _, err := subscription.Parse(bundle)
				if err != nil {
					t.Fatal(err)
				}
				if err := security.WriteAtomic(filepath.Join(home, ".pending-retained"), []byte(base64.RawURLEncoding.EncodeToString([]byte(parsed.Tokens.Refresh)))); err != nil {
					t.Fatal(err)
				}
			case "absent-symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("ordinary history"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(home, "history")); err != nil {
					t.Skip("symlinks unavailable")
				}
			case "published-identical", "published-changed":
				raw := bundle
				if mode == "published-changed" {
					raw = workerSubscriptionBundle("changed")
					defer clear(raw)
				}
				if err := security.WriteAtomic(auth, raw); err != nil {
					t.Fatal(err)
				}
			case "missing-home":
				if err := os.Remove(home); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "absent-clean" && cleanupExecutionAuthentication(home, bundle, bundle) == nil {
				t.Fatal("native-owned cleanup accepted an absent captured authentication file")
			}
			err := cleanupUnusedExecutionAuthentication(home, bundle)
			wantClean := mode == "absent-clean" || mode == "published-identical"
			if (err == nil) != wantClean {
				t.Fatal("unused authentication cleanup lost its original ownership or remnant barrier", err)
			}
			if wantClean {
				if _, err := os.Lstat(auth); !os.IsNotExist(err) {
					t.Fatal("confirmed cleanup retained authentication", err)
				}
			}
			if mode == "published-changed" {
				if _, err := os.Lstat(auth); err != nil {
					t.Fatal("uncertain authentication was erased", err)
				}
			}
		})
	}
}
