// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotWalkRejectsRootMutationAfterEnumeration(t *testing.T) {
	for _, copyFiles := range []bool{false, true} {
		for _, mutation := range []string{"entry", "mode", "mtime"} {
			name := mutation
			if copyFiles {
				name += "/copy"
			}
			t.Run(name, func(t *testing.T) {
				if mutation == "mode" && runtime.GOOS == "windows" {
					t.Skip("Windows directory permissions do not expose Unix mode changes")
				}
				source := t.TempDir()
				if err := os.WriteFile(filepath.Join(source, "enumerated"), []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(source)
				if err != nil {
					t.Fatal(err)
				}
				opened, err := os.OpenRoot(source)
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				destination := ""
				if copyFiles {
					destination = filepath.Join(t.TempDir(), "copy")
				}
				mutated := false
				_, err = walkSnapshot(context.Background(), source, destination, func(string) bool {
					if mutated {
						return false
					}
					mutated = true
					// The skip callback runs after Readdirnames. An already opened root
					// models a writer that retains access through a namespace claim.
					switch mutation {
					case "entry":
						file, err := opened.OpenFile("late", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := file.WriteString("must remain recoverable"); err != nil {
							t.Fatal(err)
						}
						if err := file.Close(); err != nil {
							t.Fatal(err)
						}
						// Make the fixture independent of filesystem timestamp granularity.
						fallthrough
					case "mtime":
						if err := os.Chtimes(source, before.ModTime(), before.ModTime().Add(2*time.Second)); err != nil {
							t.Fatal(err)
						}
					case "mode":
						if err := opened.Chmod(".", before.Mode().Perm()^0010); err != nil {
							t.Fatal(err)
						}
					}
					return false
				})
				if !mutated || domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("changed root produced a complete inventory", err)
				}
				if mutation == "entry" {
					raw, err := os.ReadFile(filepath.Join(source, "late"))
					if err != nil || string(raw) != "must remain recoverable" {
						t.Fatal("late source data changed", err)
					}
				}
			})
		}
	}
}
