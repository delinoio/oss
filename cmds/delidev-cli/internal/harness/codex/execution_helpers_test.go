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
	// macOS temporary paths can use /var for /private/var. Native aliases
	// target the canonical executable retained by the startup proof.
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
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

func captureHelperFixture(t *testing.T) (*executionHelpers, string, string) {
	t.Helper()
	h, home, directory, _ := helperFixture(t)
	if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); err != nil {
		t.Fatal(err)
	}
	return h, home, directory
}

func requireHelperEntries(t *testing.T, directory string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(directory, name)); err != nil {
			t.Fatal("entry was removed or lost", name, err)
		}
	}
}

func TestExecutionHelpersRejectRenamedDirectory(t *testing.T) {
	h, home, directory := captureHelperFixture(t)
	renamed := directory + ".renamed"
	if err := os.Rename(directory, renamed); err != nil {
		t.Fatal(err)
	}
	if err := h.retire(home, h.owner, h.native); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("renamed captured directory was accepted as destructor removal", err)
	}
	requireHelperEntries(t, renamed, append([]string{".lock"}, executionHelperNames[:]...)...)
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cleanup created or adopted the original basename", err)
	}
	if err := h.retire(home, h.owner, h.native); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("failed cleanup result did not replay", err)
	}
}

func TestExecutionHelpersNativeRemovalWithUnrelatedParentEntries(t *testing.T) {
	h, home, directory := captureHelperFixture(t)
	unrelated := filepath.Join(filepath.Dir(directory), "unrelated-native")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := h.retire(home, h.owner, h.native); err != nil {
		t.Fatal("native destructor removal was rejected", err)
	}
	requireHelperEntries(t, filepath.Dir(directory), "unrelated-native")
}

func TestExecutionHelpersRejectRenamedAlias(t *testing.T) {
	for _, nativeRemovedOthers := range []bool{false, true} {
		t.Run(strconv.FormatBool(nativeRemovedOthers), func(t *testing.T) {
			h, home, directory := captureHelperFixture(t)
			if nativeRemovedOthers {
				// Original names for the other aliases are absent, so only the
				// bounded identity scan can detect the renamed alias.
				for _, name := range executionHelperNames[1:] {
					if err := os.Remove(filepath.Join(directory, name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			renamed := "renamed-" + executionHelperNames[0]
			if err := os.Rename(filepath.Join(directory, executionHelperNames[0]), filepath.Join(directory, renamed)); err != nil {
				t.Fatal(err)
			}
			if err := h.retire(home, h.owner, h.native); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("renamed captured alias was accepted as removal", err)
			}
			requireHelperEntries(t, directory, renamed, ".lock")
			if !nativeRemovedOthers {
				requireHelperEntries(t, directory, executionHelperNames[1:]...)
			}
		})
	}
}

func TestExecutionHelpersRejectHardLinkedAlias(t *testing.T) {
	for _, original := range []string{"kept", "removed"} {
		t.Run(original, func(t *testing.T) {
			h, home, directory := captureHelperFixture(t)
			alias := filepath.Join(directory, executionHelperNames[0])
			second := filepath.Join(directory, "second-name")
			if err := os.Link(alias, second); err != nil {
				t.Skip("symlink hard links are unsupported on this platform", err)
			}
			linked, linkErr := os.Lstat(second)
			captured, captureErr := os.Lstat(alias)
			if linkErr != nil || captureErr != nil || !os.SameFile(linked, captured) {
				// Some platforms follow the symlink when linking.
				t.Skip("platform did not hard-link the symlink itself")
			}
			if original == "removed" {
				if err := os.Remove(alias); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.retire(home, h.owner, h.native); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("hard-linked captured alias was accepted as removal", err)
			}
			requireHelperEntries(t, directory, append([]string{"second-name", ".lock"}, executionHelperNames[1:]...)...)
		})
	}
}

func TestFinishFailedStartupGatesRetirementOnJoinedCapturedProof(t *testing.T) {
	original := incompatible()
	native := func(h *executionHelpers) process.Process {
		return process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}
	}
	aliasesRemain := func(directory string) bool {
		for _, name := range executionHelperNames {
			if _, err := os.Lstat(filepath.Join(directory, name)); err != nil {
				return false
			}
		}
		return true
	}
	t.Run("joined captured proof retires and keeps the original error", func(t *testing.T) {
		h, home, directory, _ := helperFixture(t)
		if err := h.capture(native(h)); err != nil {
			t.Fatal(err)
		}
		err := finishFailedStartup("", domain.CodexProfile, original, nil, h, home, h.owner, h.native)
		if err != error(original) {
			t.Fatal("successful retirement changed the original startup error", err)
		}
		if aliasesRemain(directory) || !h.closed || h.result != nil {
			t.Fatal("captured aliases were not retired")
		}
		if h.hasCapturedProof() {
			t.Fatal("retired proof can retire twice")
		}
		if err := finishFailedStartup("", domain.CodexProfile, original, nil, h, home, h.owner, h.native); err != error(original) {
			t.Fatal("repeated cleanup changed the result", err)
		}
		h.releaseUnlessRetired()
	})
	t.Run("failed join never infers retirement", func(t *testing.T) {
		h, home, directory, logs := helperFixture(t)
		if err := h.capture(native(h)); err != nil {
			t.Fatal(err)
		}
		err := finishFailedStartup("", domain.CodexProfile, original, errors.New("synthetic join failure"), h, home, h.owner, h.native)
		if domain.SafeError(err).Code != domain.RecoveryRequired || domain.CodexErrorDiagnostic(err).Code != domain.Unsupported {
			t.Fatal("failed join did not preserve both failures", err)
		}
		if !aliasesRemain(directory) || h.closed || strings.Contains(logs.String(), `"stage":"retirement"`) {
			t.Fatal("failed join attempted retirement")
		}
		if NativeJoinedHelperRetirementFailed(err) {
			t.Fatal("failed join was marked as a joined native tree", err)
		}
		h.releaseUnlessRetired()
		if !h.closed || h.result == nil {
			t.Fatal("release-only cleanup did not record an unconfirmed result")
		}
	})
	t.Run("retirement failure is joined with the original error", func(t *testing.T) {
		h, home, directory, _ := helperFixture(t)
		if err := h.capture(native(h)); err != nil {
			t.Fatal(err)
		}
		h.unlink = func(*os.Root, string) error { return errors.New("synthetic unlink failure") }
		err := finishFailedStartup("", domain.CodexProfile, original, nil, h, home, h.owner, h.native)
		if domain.SafeError(err).Code != domain.RecoveryRequired || domain.CodexErrorDiagnostic(err).Code != domain.Unsupported {
			t.Fatal("retirement failure did not preserve both failures", err)
		}
		if !errors.Is(err, error(original)) {
			t.Fatal("original startup error is no longer retained", err)
		}
		if !NativeJoinedHelperRetirementFailed(err) {
			t.Fatal("joined helper retirement failure lost its joined marker", err)
		}
		if !aliasesRemain(directory) {
			t.Fatal("failed unlink removed aliases")
		}
	})
	t.Run("pre-capture failure keeps release-only cleanup", func(t *testing.T) {
		h, home, directory, logs := helperFixture(t)
		err := finishFailedStartup("", domain.CodexProfile, original, nil, h, home, h.owner, process.Process{})
		if err != error(original) || !aliasesRemain(directory) || strings.Contains(logs.String(), `"stage":"retirement"`) {
			t.Fatal("pre-capture failure retired helpers", err)
		}
		if h.hasCapturedProof() {
			t.Fatal("uncaptured proof reports retirement authority")
		}
	})
	t.Run("no helpers", func(t *testing.T) {
		if err := finishFailedStartup("", domain.CodexProfile, original, nil, nil, "", domain.NewID(), process.Process{}); err != error(original) {
			t.Fatal("missing helpers changed the original error", err)
		}
	})
}

// useExecutionHelperProfile swaps the process-wide alias set so the Linux
// profile is exercised on every Unix host. Tests that call it must not run in
// parallel.
func useExecutionHelperProfile(t *testing.T, goos string) {
	t.Helper()
	previous := executionHelperNames
	executionHelperNames = executionHelperNamesFor(goos)
	t.Cleanup(func() { executionHelperNames = previous })
}

func TestExecutionHelperNamesMatchPinnedStartupPerOS(t *testing.T) {
	// Pinned openai/codex arg0 startup: Unix aliases everywhere, plus the Linux
	// sandbox alias on Linux only. Windows creates .bat scripts instead.
	unix := []string{"apply_patch", "applypatch", "codex-execve-wrapper"}
	for goos, want := range map[string][]string{
		"darwin":  unix,
		"freebsd": unix,
		"linux":   append(append([]string{}, unix...), "codex-linux-sandbox"),
	} {
		got := executionHelperNamesFor(goos)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s helper aliases = %v, want %v", goos, got, want)
		}
	}
	if len(executionHelperNamesFor("linux")) > helperDirectoryLimit {
		t.Fatal("alias set exceeds the bounded directory inventory")
	}
	if !isExecutionHelperName("apply_patch") || isExecutionHelperName("foreign-alias") {
		t.Fatal("alias name predicate drifted")
	}
}

func TestExecutionHelpersRetireLinuxSandboxAlias(t *testing.T) {
	useExecutionHelperProfile(t, "linux")
	for _, nativeRemoved := range []bool{false, true} {
		t.Run(strconv.FormatBool(nativeRemoved), func(t *testing.T) {
			h, home, directory := captureHelperFixture(t)
			if len(h.aliases) != 4 {
				t.Fatal("startup capture did not record the Linux sandbox alias", len(h.aliases))
			}
			requireHelperEntries(t, directory, "codex-linux-sandbox")
			if nativeRemoved {
				if err := os.RemoveAll(directory); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.retire(home, h.owner, h.native); err != nil {
				t.Fatal(err)
			}
			if !nativeRemoved {
				for _, name := range executionHelperNames {
					if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("original alias remains", name, err)
					}
				}
				requireHelperEntries(t, directory, ".lock")
			}
		})
	}
}

func TestExecutionHelpersRejectChangedLinuxSandboxAlias(t *testing.T) {
	useExecutionHelperProfile(t, "linux")
	sandbox := func(directory string) string { return filepath.Join(directory, "codex-linux-sandbox") }
	t.Run("missing-at-capture", func(t *testing.T) {
		h, _, directory, _ := helperFixture(t)
		if err := os.Remove(sandbox(directory)); err != nil {
			t.Fatal(err)
		}
		if err := h.capture(process.Process{OwnerID: h.owner, PID: 1, Birth: "synthetic", ScopeDir: filepath.Join(h.home, "original-process")}); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("Linux startup without the sandbox alias was admitted", err)
		}
	})
	t.Run("retargeted-after-capture", func(t *testing.T) {
		h, home, directory := captureHelperFixture(t)
		if err := os.Remove(sandbox(directory)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "foreign-target"), sandbox(directory)); err != nil {
			t.Fatal(err)
		}
		if err := h.retire(home, h.owner, h.native); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("replaced sandbox alias was accepted as removal", err)
		}
		// Validation precedes the first unlink, so every original remains.
		requireHelperEntries(t, directory, executionHelperNames[:]...)
	})
	t.Run("renamed-after-capture", func(t *testing.T) {
		h, home, directory := captureHelperFixture(t)
		for _, name := range executionHelperNames[:3] {
			if err := os.Remove(filepath.Join(directory, name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Rename(sandbox(directory), filepath.Join(directory, "renamed-sandbox")); err != nil {
			t.Fatal(err)
		}
		if err := h.retire(home, h.owner, h.native); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("renamed sandbox alias was accepted as removal", err)
		}
		requireHelperEntries(t, directory, "renamed-sandbox", ".lock")
	})
}
