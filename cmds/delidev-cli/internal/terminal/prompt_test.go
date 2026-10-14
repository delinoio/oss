// SPDX-License-Identifier: Apache-2.0
package terminal

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestDetectShellKind(t *testing.T) {
	cases := map[string]ShellKind{
		"/bin/zsh":                               ShellKindZsh,
		"/usr/local/bin/bash":                    ShellKindBash,
		"/opt/homebrew/bin/fish":                 ShellKindFish,
		"/usr/bin/pwsh":                          ShellKindPowerShell,
		`C:\Windows\System32\cmd.exe`:            ShellKindCmd,
		`C:\Program Files\PowerShell\7\pwsh.EXE`: ShellKindPowerShell,
		"/bin/sh":                                ShellKindUnknown,
		"/usr/bin/dash":                          ShellKindUnknown,
		"/usr/bin/nu":                            ShellKindUnknown,
	}
	for path, want := range cases {
		if got := DetectShellKind(path); got != want {
			t.Errorf("DetectShellKind(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestPreparePromptIntegrationFallsBackWithoutFiles(t *testing.T) {
	root, chat := t.TempDir(), t.TempDir()
	args, env := []string{"-i"}, []string{"HOME=/h"}
	for _, shell := range []string{"/bin/sh", `C:\Windows\System32\cmd.exe`, "/usr/bin/unknown-shell"} {
		got := PreparePromptIntegration(root, domain.NewID(), shell, chat, args, env)
		if got.Outcome != PromptUnsupported || !reflect.DeepEqual(got.Args, args) || !reflect.DeepEqual(got.Env, env) {
			t.Fatalf("%s: unexpected integration %+v", shell, got)
		}
		if err := got.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, promptIntegrationDirectory)); !os.IsNotExist(err) {
		t.Fatalf("unsupported shells must not create wrapper files: %v", err)
	}
	missing := PreparePromptIntegration(root, domain.NewID(), "/bin/zsh", filepath.Join(chat, "absent"), args, env)
	if missing.Outcome != PromptUnsupported || !reflect.DeepEqual(missing.Args, args) {
		t.Fatalf("a directory that cannot be verified must fall back: %+v", missing)
	}
}

func TestPreparePromptIntegrationLaunchShape(t *testing.T) {
	chat, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		shell string
		check func(t *testing.T, got PromptIntegration)
	}{
		{"/bin/zsh", func(t *testing.T, got PromptIntegration) {
			if !reflect.DeepEqual(got.Args, []string{"-i"}) || !containsPrefix(got.Env, "ZDOTDIR="+got.dir) {
				t.Fatalf("zsh must keep -i and gain a wrapper ZDOTDIR: %+v", got)
			}
		}},
		{"/bin/bash", func(t *testing.T, got PromptIntegration) {
			if !reflect.DeepEqual(got.Args, []string{"--rcfile", filepath.Join(got.dir, "bashrc"), "-i"}) {
				t.Fatalf("bash must read the wrapper rcfile: %+v", got.Args)
			}
		}},
		{"/usr/bin/fish", func(t *testing.T, got PromptIntegration) {
			if len(got.Args) != 3 || got.Args[0] != "-i" || got.Args[1] != "--init-command" || !strings.HasPrefix(got.Args[2], "source '") {
				t.Fatalf("fish must source the wrapper after its configuration: %+v", got.Args)
			}
		}},
		{"/usr/bin/pwsh", func(t *testing.T, got PromptIntegration) {
			if len(got.Args) != 4 || got.Args[1] != "-NoExit" || got.Args[2] != "-Command" || !strings.HasPrefix(got.Args[3], ". '") {
				t.Fatalf("PowerShell must dot-source the wrapper without exiting: %+v", got.Args)
			}
		}},
	} {
		got := PreparePromptIntegration(t.TempDir(), domain.NewID(), tc.shell, chat, []string{"-i"}, []string{"HOME=/h"})
		if got.Outcome != PromptApplied || !containsPrefix(got.Env, ChatDirectoryEnvironment+"="+chat) || !containsPrefix(got.Env, "HOME=/h") {
			t.Fatalf("%s: %+v", tc.shell, got)
		}
		tc.check(t, got)
		entries, err := os.ReadDir(got.dir)
		if err != nil || len(entries) == 0 {
			t.Fatalf("%s: wrapper files missing: %v", tc.shell, err)
		}
		if err := got.Cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(got.dir); !os.IsNotExist(err) {
			t.Fatalf("%s: cleanup must remove wrapper files: %v", tc.shell, err)
		}
		if err := got.Cleanup(); err != nil {
			t.Fatalf("cleanup must be idempotent: %v", err)
		}
	}
}

func TestPromptQuoting(t *testing.T) {
	if got, ok := fishQuote(`/a b/it's\x`); !ok || got != `'/a b/it\'s\\x'` {
		t.Fatalf("fish quote: %q %v", got, ok)
	}
	if got, ok := powerShellQuote(`/a b/it's`); !ok || got != `'/a b/it''s'` {
		t.Fatalf("PowerShell quote: %q %v", got, ok)
	}
	if _, ok := fishQuote("/a\nb"); ok {
		t.Fatal("control characters must be rejected")
	}
	if _, ok := powerShellQuote("/a\nb"); ok {
		t.Fatal("control characters must be rejected")
	}
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func TestRemovePromptIntegrationClearsCrashLeftovers(t *testing.T) {
	root, id := t.TempDir(), domain.NewID()
	dir := PromptIntegrationDirectory(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bashrc"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemovePromptIntegration(root, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("leftover wrapper must be removed: %v", err)
	}
	if err := RemovePromptIntegration(root, "../escape"); err != nil {
		t.Fatalf("invalid identifiers are ignored: %v", err)
	}
}
