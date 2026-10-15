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

func TestZshPromptPreservesAccountHistoryChoice(t *testing.T) {
	// Isolate the account's startup choice from system zshrc history defaults,
	// which differ across the Unix test hosts.
	t.Run("unset history", func(t *testing.T) {
		f := newPromptShellFixture(t, "zsh")
		if err := os.WriteFile(filepath.Join(f.home, ".zshenv"), []byte("unsetopt GLOBAL_RCS\nunset HISTFILE\nSAVEHIST=20\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		rc := "if [[ -z ${HISTFILE+x} ]]; then marker=unset; else marker=set; fi\nPROMPT='%~ > '\n"
		if err := os.WriteFile(filepath.Join(f.home, ".zshrc"), []byte(rc), 0o600); err != nil {
			t.Fatal(err)
		}
		out, _ := f.run(t, "print -r -- \"history[${HISTFILE-unset}]\"\nprint -r -- \"marker[$marker]\"\nprint -r -- \"savehist[$SAVEHIST]\"\nprint -r -- \"prompt[$PROMPT]\"\nexit\n")
		for _, want := range []string{"history[unset]", "marker[unset]", "savehist[20]", "prompt[chat > ]"} {
			requireLine(t, out, want)
		}
	})
	t.Run("explicit history and ZDOTDIR", func(t *testing.T) {
		f := newPromptShellFixture(t, "zsh")
		zdot := filepath.Join(f.home, "zdot")
		if err := os.MkdirAll(zdot, 0o700); err != nil {
			t.Fatal(err)
		}
		custom := filepath.Join(f.home, "custom-history")
		env := "unsetopt GLOBAL_RCS\nZDOTDIR=" + zdot + "\nHISTFILE=" + custom + "\n"
		if err := os.WriteFile(filepath.Join(f.home, ".zshenv"), []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("PROMPT='%~ > '\nmarker=zdot\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		out, _ := f.run(t, "print -r -- \"history[$HISTFILE]\"\nprint -r -- \"marker[$marker]\"\nprint -r -- \"prompt[$PROMPT]\"\nexit\n")
		for _, want := range []string{"history[" + custom + "]", "marker[zdot]", "prompt[chat > ]"} {
			requireLine(t, out, want)
		}
	})
	t.Run("conditional rc default", func(t *testing.T) {
		f := newPromptShellFixture(t, "zsh")
		if err := os.WriteFile(filepath.Join(f.home, ".zshenv"), []byte("unsetopt GLOBAL_RCS\nunset HISTFILE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		custom := filepath.Join(f.home, "rc-history")
		rc := "if [[ -z ${HISTFILE+x} ]]; then HISTFILE=" + custom + "; marker=defaulted; fi\nPROMPT='%~ > '\n"
		if err := os.WriteFile(filepath.Join(f.home, ".zshrc"), []byte(rc), 0o600); err != nil {
			t.Fatal(err)
		}
		out, _ := f.run(t, "print -r -- \"history[$HISTFILE]\"\nprint -r -- \"marker[$marker]\"\nprint -r -- \"prompt[$PROMPT]\"\nexit\n")
		for _, want := range []string{"history[" + custom + "]", "marker[defaulted]", "prompt[chat > ]"} {
			requireLine(t, out, want)
		}
	})
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

func TestFishPromptHandoffFileIsPrivate(t *testing.T) {
	root, chat := t.TempDir(), t.TempDir()
	got := PreparePromptIntegration(root, domain.NewID(), "/usr/bin/fish", chat, []string{"-i"}, []string{"HOME=/h"})
	if got.Outcome != PromptApplied {
		t.Fatal("fish handoff was not prepared")
	}
	defer got.Cleanup()
	for _, path := range []string{got.dir, filepath.Join(got.dir, "chat-directory"), filepath.Join(got.dir, "init.fish")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("fish handoff is not private: %v", err)
		}
	}
}

func TestFishPromptStartupChildrenDoNotInheritDirectory(t *testing.T) {
	f := newPromptShellFixture(t, "fish")
	configDir := filepath.Join(f.home, ".config", "fish")
	confDir := filepath.Join(configDir, "conf.d")
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configEnv := filepath.Join(f.root, "config-child.env")
	confEnv := filepath.Join(f.root, "conf-child.env")
	laterEnv := filepath.Join(f.root, "later-child.env")
	quotedConfigEnv, ok := fishQuote(configEnv)
	if !ok {
		t.Fatal("cannot quote config capture path")
	}
	quotedConfEnv, ok := fishQuote(confEnv)
	if !ok {
		t.Fatal("cannot quote conf.d capture path")
	}
	quotedLaterEnv, ok := fishQuote(laterEnv)
	if !ok {
		t.Fatal("cannot quote later capture path")
	}
	config := "if functions -q __delidev_native_prompt_pwd\n    set -g wrapper_seen_early yes\nelse\n    set -g wrapper_seen_early no\nend\nenv > " + quotedConfigEnv + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.fish"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	childScript := filepath.Join(f.root, "capture-env.sh")
	if err := os.WriteFile(childScript, []byte("#!/bin/sh\nsleep 1\nenv > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	quotedChildScript, ok := fishQuote(childScript)
	if !ok {
		t.Fatal("cannot quote child script path")
	}
	conf := quotedChildScript + " " + quotedConfEnv + " >/dev/null 2>&1 &\n"
	if err := os.WriteFile(filepath.Join(confDir, "capture.fish"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := f.run(t, "echo \"early[$wrapper_seen_early]\"\necho \"prompt[\"(prompt_pwd)\"]\"\nenv > "+quotedLaterEnv+"\nexit\n")
	requireLine(t, out, "early[no]")
	requireLine(t, out, "prompt[chat]")
	for _, path := range []string{configEnv, confEnv, laterEnv} {
		var contents []byte
		var err error
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			contents, err = os.ReadFile(path)
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("startup child did not capture its environment: %v", err)
		}
		if strings.Contains(string(contents), ChatDirectoryEnvironment+"=") || strings.Contains(string(contents), "__delidev_chat_dir=") {
			t.Fatal("fish child inherited the private directory")
		}
	}
}
