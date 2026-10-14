//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package terminal

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type promptShellFixture struct {
	shell string
	root  string
	home  string
	chat  string
	other string
}

func newPromptShellFixture(t *testing.T, name string) promptShellFixture {
	t.Helper()
	shell, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := promptShellFixture{shell: shell, root: filepath.Join(base, "root"), home: filepath.Join(base, "home"), chat: filepath.Join(base, "workspaces", "session", "chat"), other: filepath.Join(base, "other")}
	for _, dir := range []string{f.root, f.home, f.chat, f.other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// run starts the shell with the integration exactly as the Worker would and
// returns its standard output. Input is piped, so the shell is interactive
// (-i) without a PTY; prompt hooks still run before every input line.
func (f promptShellFixture) run(t *testing.T, script string) (string, PromptIntegration) {
	t.Helper()
	env := []string{"HOME=" + f.home, "PATH=" + os.Getenv("PATH"), "TERM=dumb", "LC_ALL=C"}
	integration := PreparePromptIntegration(f.root, domain.NewID(), f.shell, f.chat, []string{"-i"}, env)
	t.Cleanup(func() { _ = integration.Cleanup() })
	if integration.Outcome != PromptApplied {
		t.Fatalf("integration was not applied: %+v", integration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.shell, integration.Args...)
	cmd.Env, cmd.Dir = integration.Env, f.chat
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("shell failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), integration
}

func (f promptShellFixture) homeListing(t *testing.T) []string {
	t.Helper()
	var names []string
	_ = filepath.WalkDir(f.home, func(path string, _ os.DirEntry, err error) error {
		if err == nil {
			names = append(names, path)
		}
		return nil
	})
	return names
}

func requireLine(t *testing.T, output, want string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if line == want {
			return
		}
	}
	t.Fatalf("missing line %q in output:\n%s", want, output)
}

func TestZshPromptShortensOnlyAtOriginalDirectory(t *testing.T) {
	f := newPromptShellFixture(t, "zsh")
	rc := "PROMPT='%F{cyan}%~ %%~ ❯%f '\nUSER_RC_RAN=yes\n"
	if err := os.WriteFile(filepath.Join(f.home, ".zshrc"), []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	before := f.homeListing(t)
	out, _ := f.run(t, "print -r -- \"A[$PROMPT]\"\nprint -r -- \"B[$PROMPT]\"\nprint -r -- \"rc[$USER_RC_RAN]\"\nprint -r -- \"env[${DELIDEV_TERMINAL_CHAT_DIRECTORY-unset}]\"\npwd\ncd "+f.other+"\nprint -r -- \"C[$PROMPT]\"\ncd "+f.chat+"\nprint -r -- \"D[$PROMPT]\"\nexit\n")
	requireLine(t, out, "A[%F{cyan}chat %%~ ❯%f ]")
	requireLine(t, out, "B[%F{cyan}chat %%~ ❯%f ]")
	requireLine(t, out, "rc[yes]")
	requireLine(t, out, "env[unset]")
	requireLine(t, out, f.chat)
	requireLine(t, out, "C[%F{cyan}%~ %%~ ❯%f ]")
	requireLine(t, out, "D[%F{cyan}chat %%~ ❯%f ]")
	if after := f.homeListing(t); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the integration must not write user files: before %v after %v", before, after)
	}
}

func TestZshPromptHonorsAccountZdotdirAndUnrecognizedPrompts(t *testing.T) {
	f := newPromptShellFixture(t, "zsh")
	zdot := filepath.Join(f.home, "zdot")
	if err := os.MkdirAll(zdot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, ".zshenv"), []byte("ZDOTDIR="+zdot+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("PROMPT='%n@%m %1~ $ '\nmarker=zdot\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := f.run(t, "print -r -- \"A[$PROMPT]\"\nprint -r -- \"m[$marker]\"\nprint -r -- \"z[$ZDOTDIR]\"\nexit\n")
	requireLine(t, out, "A[%n@%m chat $ ]")
	requireLine(t, out, "m[zdot]")
	requireLine(t, out, "z["+zdot+"]")

	g := newPromptShellFixture(t, "zsh")
	if err := os.WriteFile(filepath.Join(g.home, ".zshrc"), []byte("PROMPT='custom > '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ = g.run(t, "print -r -- \"A[$PROMPT]\"\nprint -r -- \"z[${ZDOTDIR-unset}]\"\nexit\n")
	requireLine(t, out, "A[custom > ]")
	requireLine(t, out, "z[unset]")
}

func TestBashPromptShortensOnlyAtOriginalDirectory(t *testing.T) {
	f := newPromptShellFixture(t, "bash")
	rc := "unset HISTFILE\nPS1='\\[\\e[36m\\]\\w \\\\w ❯\\[\\e[0m\\] '\nUSER_RC_RAN=yes\n"
	if err := os.WriteFile(filepath.Join(f.home, ".bashrc"), []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	before := f.homeListing(t)
	out, _ := f.run(t, "printf '%s\\n' \"A[$PS1]\"\nprintf '%s\\n' \"B[$PS1]\"\nprintf '%s\\n' \"rc[$USER_RC_RAN]\"\nprintf '%s\\n' \"env[${DELIDEV_TERMINAL_CHAT_DIRECTORY-unset}]\"\npwd\ncd "+f.other+"\nprintf '%s\\n' \"C[$PS1]\"\ncd "+f.chat+"\nprintf '%s\\n' \"D[$PS1]\"\nfalse\nprintf '%s\\n' \"status[$?]\"\nexit\n")
	short := `\[\e[36m\]chat \\w ❯\[\e[0m\] `
	native := `\[\e[36m\]\w \\w ❯\[\e[0m\] `
	requireLine(t, out, "A["+short+"]")
	requireLine(t, out, "B["+short+"]")
	requireLine(t, out, "rc[yes]")
	requireLine(t, out, "env[unset]")
	requireLine(t, out, f.chat)
	requireLine(t, out, "C["+native+"]")
	requireLine(t, out, "D["+short+"]")
	requireLine(t, out, "status[1]")
	if after := f.homeListing(t); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the integration must not write user files: before %v after %v", before, after)
	}
}

func TestBashPromptKeepsExistingPromptCommandAndUnrecognizedPrompts(t *testing.T) {
	f := newPromptShellFixture(t, "bash")
	if err := os.WriteFile(filepath.Join(f.home, ".bashrc"), []byte("PS1='plain > '\nPROMPT_COMMAND='USER_HOOK=ran'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := f.run(t, "printf '%s\\n' \"A[$PS1]\"\nprintf '%s\\n' \"hook[$USER_HOOK]\"\nexit\n")
	requireLine(t, out, "A[plain > ]")
	requireLine(t, out, "hook[ran]")
}

func TestFishPromptPwdShortensOnlyAtOriginalDirectory(t *testing.T) {
	f := newPromptShellFixture(t, "fish")
	out, _ := f.run(t, "echo \"A[\"(prompt_pwd)\"]\"\nbuiltin pwd\ncd "+f.other+"\necho \"B[\"(prompt_pwd)\"]\"\ncd "+f.chat+"\necho \"C[\"(prompt_pwd)\"]\"\nexit\n")
	requireLine(t, out, "A[chat]")
	requireLine(t, out, f.chat)
	if strings.Contains(out, "B[chat]") || !strings.Contains(out, "B[") {
		t.Fatalf("a different directory must use the ordinary prompt_pwd:\n%s", out)
	}
	requireLine(t, out, "C[chat]")
}
