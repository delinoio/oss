// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestRuntimeCleanupPreservesInventoryBounds(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		files int
		bytes int64
		want  CleanupReason
	}{
		{"file-limit", RuntimeFileLimit, 0, ""},
		{"too-many-files", RuntimeFileLimit + 1, 0, CleanupFileLimit},
		{"byte-limit", 1, RuntimeByteLimit, ""},
		{"too-many-bytes", 1, RuntimeByteLimit + 1, CleanupByteLimit},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "private-runtime")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			original, err := security.StableStat(home)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < scenario.files; i++ {
				file, err := os.OpenFile(filepath.Join(home, fmt.Sprintf("file-%04d", i)), os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(scenario.bytes); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = CleanupRuntime(home, original)
			if scenario.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(home); !os.IsNotExist(err) {
					t.Fatal("bounded runtime was not removed")
				}
				return
			}
			var failure *RuntimeCleanupError
			if !errors.As(err, &failure) || failure.Stage != CleanupInventory || failure.Reason != scenario.want || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("cleanup limit lost its safe recovery classification")
			}
			if failure.FileCount != scenario.files || failure.Bytes != scenario.bytes || strings.Contains(err.Error(), home) {
				t.Fatal("cleanup diagnostic lost its bounded counters or exposed a path")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != scenario.files {
				t.Fatal("failed inventory removed unconfirmed files")
			}
		})
	}
}

func TestRuntimeCleanupRejectsSymlinkAndReplacedIdentity(t *testing.T) {
	for _, scenario := range []string{"symlink", "identity"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "runtime")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			original, err := security.StableStat(home)
			if err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(root, "retained-foreign-file")
			if err := os.WriteFile(foreign, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			want := CleanupSymlink
			if scenario == "symlink" {
				if err := os.Symlink(foreign, filepath.Join(home, "link")); err != nil {
					t.Skip("platform cannot create the temporary symlink fixture")
				}
			} else {
				want = CleanupIdentity
				if err := os.Rename(home, filepath.Join(root, "original")); err != nil {
					t.Fatal(err)
				}
				if err := security.PrivateDir(home); err != nil {
					t.Fatal(err)
				}
			}
			var failure *RuntimeCleanupError
			err = CleanupRuntime(home, original)
			if !errors.As(err, &failure) {
				t.Fatalf("unowned runtime inventory accepted: %v", err)
			}
			if failure.Reason != want {
				t.Fatalf("cleanup returned reason %q, want %q", failure.Reason, want)
			}
			if _, err := os.Stat(home); err != nil {
				t.Fatal("unconfirmed runtime removed")
			}
			if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "preserve" {
				t.Fatal("cleanup changed foreign content")
			}
		})
	}
}
