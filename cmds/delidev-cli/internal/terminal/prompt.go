// SPDX-License-Identifier: Apache-2.0
package terminal

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// ShellKind is the closed set of shell families whose prompt the Worker knows
// how to integrate with. Detection uses only the validated executable name.
type ShellKind string

const (
	ShellKindZsh        ShellKind = "zsh"
	ShellKindBash       ShellKind = "bash"
	ShellKindFish       ShellKind = "fish"
	ShellKindPowerShell ShellKind = "powershell"
	// cmd.exe has a static PROMPT string with no conditional or hook facility,
	// so it can never shorten the directory only at one location.
	ShellKindCmd     ShellKind = "cmd"
	ShellKindUnknown ShellKind = "unknown"
)

// PromptOutcome records whether the process-local integration was installed.
// Applied means the hook is active; a prompt whose directory segment cannot be
// identified at runtime still renders natively.
type PromptOutcome string

const (
	PromptApplied     PromptOutcome = "applied"
	PromptUnsupported PromptOutcome = "unsupported"
)

// ChatDirectoryEnvironment carries the verified original General Chat
// directory to the integration scripts, which copy it into a shell-local
// variable and unset it so it is not inherited by commands.
const ChatDirectoryEnvironment = "DELIDEV_TERMINAL_CHAT_DIRECTORY"

// ChatPromptLabel is the display alias for the General Chat directory.
const ChatPromptLabel = "chat"

const promptIntegrationDirectory = "terminal-integrations"

// DetectShellKind classifies a resolved shell executable path by file name.
func DetectShellKind(path string) ShellKind {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(path, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "zsh":
		return ShellKindZsh
	case "bash":
		return ShellKindBash
	case "fish":
		return ShellKindFish
	case "pwsh", "powershell":
		return ShellKindPowerShell
	case "cmd":
		return ShellKindCmd
	default:
		return ShellKindUnknown
	}
}

// PromptIntegration is the process-local launch change for one terminal. The
// zero Outcome-less value never occurs; unsupported integrations leave Args
// and Env equal to the inputs and own no files.
type PromptIntegration struct {
	Kind    ShellKind
	Outcome PromptOutcome
	Args    []string
	Env     []string
	dir     string
}

// Cleanup removes the per-terminal wrapper files. It is idempotent.
func (p PromptIntegration) Cleanup() error {
	if p.dir == "" {
		return nil
	}
	return os.RemoveAll(p.dir)
}

// PromptIntegrationDirectory is the per-terminal private wrapper location.
func PromptIntegrationDirectory(root string, terminal domain.ID) string {
	return filepath.Join(root, promptIntegrationDirectory, string(terminal))
}

// RemovePromptIntegration removes any wrapper left by a prior Worker process.
func RemovePromptIntegration(root string, terminal domain.ID) error {
	if terminal.Validate() != nil {
		return nil
	}
	return os.RemoveAll(PromptIntegrationDirectory(root, terminal))
}

func unsupportedPrompt(kind ShellKind, args, env []string) PromptIntegration {
	return PromptIntegration{Kind: kind, Outcome: PromptUnsupported, Args: args, Env: env}
}

// PreparePromptIntegration builds the integration for a General Chat shell.
// It never writes user dotfiles, never changes PWD and never types a startup
// command. Any failure degrades to the native prompt (Outcome unsupported).
// root is the Worker data root; chatDirectory is the verified launch cwd.
func PreparePromptIntegration(root string, terminal domain.ID, shell, chatDirectory string, args, env []string) PromptIntegration {
	kind := DetectShellKind(shell)
	if kind == ShellKindUnknown || kind == ShellKindCmd || terminal.Validate() != nil {
		return unsupportedPrompt(kind, args, env)
	}
	physical, err := filepath.EvalSymlinks(chatDirectory)
	if err != nil || !filepath.IsAbs(physical) || strings.ContainsAny(physical, "\x00\r\n") {
		return unsupportedPrompt(kind, args, env)
	}
	files := promptScripts(kind)
	if len(files) == 0 {
		return unsupportedPrompt(kind, args, env)
	}
	parent := filepath.Join(root, promptIntegrationDirectory)
	if err := security.PrivateDir(parent); err != nil {
		return unsupportedPrompt(kind, args, env)
	}
	dir := PromptIntegrationDirectory(root, terminal)
	// A stale directory can only be an earlier crashed launch of this same
	// terminal ID; the files are regenerable, so replace rather than adopt it.
	if err := os.RemoveAll(dir); err != nil {
		return unsupportedPrompt(kind, args, env)
	}
	if err := security.CreatePrivateDirExclusive(dir); err != nil {
		return unsupportedPrompt(kind, args, env)
	}
	fail := func() PromptIntegration {
		_ = os.RemoveAll(dir)
		return unsupportedPrompt(kind, args, env)
	}
	for name, contents := range files {
		if err := security.WriteAtomic(filepath.Join(dir, name), []byte(contents)); err != nil {
			return fail()
		}
	}
	launchArgs := append([]string(nil), args...)
	launchEnv := append(append([]string(nil), env...), ChatDirectoryEnvironment+"="+physical)
	switch kind {
	case ShellKindZsh:
		launchEnv = append(launchEnv, "ZDOTDIR="+dir)
	case ShellKindBash:
		launchArgs = append([]string{"--rcfile", filepath.Join(dir, "bashrc")}, launchArgs...)
	case ShellKindFish:
		quoted, ok := fishQuote(filepath.Join(dir, "init.fish"))
		if !ok {
			return fail()
		}
		launchArgs = append(launchArgs, "--init-command", "source "+quoted)
	case ShellKindPowerShell:
		quoted, ok := powerShellQuote(filepath.Join(dir, "init.ps1"))
		if !ok {
			return fail()
		}
		launchArgs = append(launchArgs, "-NoExit", "-Command", ". "+quoted)
	default:
		return fail()
	}
	return PromptIntegration{Kind: kind, Outcome: PromptApplied, Args: launchArgs, Env: launchEnv, dir: dir}
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// fishQuote single-quotes a path for fish; fish allows only \\ and \' escapes
// inside single quotes.
func fishQuote(value string) (string, bool) {
	if hasControl(value) {
		return "", false
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'", true
}

// powerShellQuote single-quotes a path as a literal PowerShell string.
func powerShellQuote(value string) (string, bool) {
	if hasControl(value) {
		return "", false
	}
	return "'" + strings.ReplaceAll(value, `'`, `''`) + "'", true
}

func promptScripts(kind ShellKind) map[string]string {
	switch kind {
	case ShellKindZsh:
		return map[string]string{".zshenv": zshEnvScript, ".zshrc": zshRCScript}
	case ShellKindBash:
		return map[string]string{"bashrc": bashRCScript}
	case ShellKindFish:
		return map[string]string{"init.fish": fishInitScript}
	case ShellKindPowerShell:
		return map[string]string{"init.ps1": powerShellInitScript}
	default:
		return nil
	}
}
