//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func helperFixture(t *testing.T) (*executionHelpers, string, string, *bytes.Buffer) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "native")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	var err error
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "verified-native")
	if err := os.WriteFile(executable, []byte("synthetic executable identity"), 0700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h, err := prepareExecutionHelpers(home, process.Config{OwnerID: domain.NewID(), Executable: executable, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !h.closed {
			h.release()
		}
	})
	directory := filepath.Join(home, "tmp", "arg0", "original-helper")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range executionHelperNames {
		if err := os.Symlink(executable, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, ".lock"), []byte("synthetic lock"), 0600); err != nil {
		t.Fatal(err)
	}
	return h, home, directory, &logs
}

func TestExecutionHelpersRetireOnlyCapturedOriginals(t *testing.T) {
	for _, nativeRemoved := range []bool{false, true} {
		t.Run(strconv.FormatBool(nativeRemoved), func(t *testing.T) {
			h, home, directory, logs := helperFixture(t)
			if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); err != nil {
				t.Fatal(err)
			}
			if nativeRemoved {
				if err := os.RemoveAll(directory); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.retire(home, h.owner, h.native); err != nil {
				t.Fatal(err)
			}
			if err := h.retire(home, h.owner, h.native); err != nil {
				t.Fatal("original cleanup result did not replay", err)
			}
			for _, name := range executionHelperNames {
				if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("original helper remains", err)
				}
			}
			if _, err := os.Stat(h.executable); err != nil {
				t.Fatal("retirement followed helper target", err)
			}
			if !nativeRemoved {
				if raw, err := os.ReadFile(filepath.Join(directory, ".lock")); err != nil || string(raw) != "synthetic lock" {
					t.Fatal("retirement changed unrelated content", err)
				}
			}
			for _, value := range []string{home, directory, h.executable, "synthetic lock"} {
				if strings.Contains(logs.String(), value) {
					t.Fatal("helper diagnostic contains private native content")
				}
			}
			if !strings.Contains(logs.String(), `"stage":"retirement"`) || !strings.Contains(logs.String(), `"reason":"verified"`) {
				t.Fatal("missing closed retirement diagnostic")
			}
		})
	}
}

func TestExecutionHelpersRejectChangedOrForeignProof(t *testing.T) {
	for _, change := range []string{"target", "same-target-new-link", "directory", "ancestor", "home", "executable", "foreign-owner", "foreign-home", "foreign-process", "missing-proof", "unlink-failure", "sync-failure"} {
		t.Run(change, func(t *testing.T) {
			h, home, directory, _ := helperFixture(t)
			if change != "missing-proof" {
				if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); err != nil {
					t.Fatal(err)
				}
			}
			owner := h.owner
			native := h.native
			alias := filepath.Join(directory, executionHelperNames[0])
			switch change {
			case "target", "same-target-new-link":
				if err := os.Rename(alias, alias+".retained"); err != nil {
					t.Fatal(err)
				}
				target := h.executable
				if change == "target" {
					target = filepath.Join(t.TempDir(), "foreign-target")
				}
				if err := os.Symlink(target, alias); err != nil {
					t.Fatal(err)
				}
			case "directory", "ancestor", "home":
				replaced := directory
				if change == "ancestor" {
					replaced = filepath.Join(home, "tmp")
				}
				if change == "home" {
					replaced = home
				}
				if err := os.Rename(replaced, replaced+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "sentinel"), []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Rename(h.executable, h.executable+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(h.executable, []byte("synthetic executable identity"), 0700); err != nil {
					t.Fatal(err)
				}
			case "foreign-owner":
				owner = domain.NewID()
			case "foreign-process":
				native.Birth = "another-process"
			case "foreign-home":
				home = filepath.Join(t.TempDir(), "foreign-home")
			case "unlink-failure":
				h.unlink = func(*os.Root, string) error { return errors.New("private filesystem fault") }
			case "sync-failure":
				h.syncParent = func(*os.Root) error { return errors.New("private filesystem fault") }
			}
			if err := h.retire(home, owner, native); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("changed original helper proof granted cleanup", err)
			}
			if change == "directory" || change == "ancestor" || change == "home" {
				if raw, err := os.ReadFile(filepath.Join(directory, "sentinel")); err != nil || string(raw) != "replacement" {
					t.Fatal("replacement was touched", err)
				}
			} else if change != "sync-failure" {
				if _, err := os.Lstat(filepath.Join(directory, executionHelperNames[1])); err != nil {
					t.Fatal("failure removed an original without complete admission", err)
				}
			}
		})
	}
}

func TestExecutionHelpersBoundStartupAndNeverAdoptHistoricalAliases(t *testing.T) {
	t.Run("historical", func(t *testing.T) {
		h, home, directory, _ := helperFixture(t)
		h.before[filepath.Base(directory)] = true
		if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); err != nil {
			t.Fatal(err)
		}
		if len(h.aliases) != 0 {
			t.Fatal("historical directory supplied startup removal authority")
		}
		if err := h.retire(home, h.owner, h.native); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(directory, executionHelperNames[0])); err != nil {
			t.Fatal("historical alias was automatically cleared", err)
		}
	})
	t.Run("overflow", func(t *testing.T) {
		h, _, directory, _ := helperFixture(t)
		for i := 0; i < helperDirectoryLimit; i++ {
			if err := os.Mkdir(filepath.Join(filepath.Dir(directory), strconv.Itoa(i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); err == nil {
			t.Fatal("unbounded helper inventory was admitted")
		}
	})
	t.Run("multiple-new-owners", func(t *testing.T) {
		h, _, directory, _ := helperFixture(t)
		if err := os.Mkdir(filepath.Join(filepath.Dir(directory), "other-original"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); err == nil {
			t.Fatal("ambiguous startup helper ownership was admitted")
		}
	})
}
