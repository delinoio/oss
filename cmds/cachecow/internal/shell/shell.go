// SPDX-License-Identifier: Apache-2.0

// Package shell generates activation code and plans confirmed, atomic profile updates.
// It never writes diagnostics to stdout or prompts without the caller's participation.
package shell

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Bash       Kind = "bash"
	Zsh        Kind = "zsh"
	PowerShell Kind = "powershell"
)

func (s Kind) Valid() bool    { return s == Bash || s == Zsh || s == PowerShell }
func quote(s string) string   { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func psquote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Init returns code only. The caller sends notices separately to stderr.
func Init(kind Kind, shimDir string) (string, error) {
	if !kind.Valid() {
		return "", fmt.Errorf("unsupported shell %q; use bash, zsh, or powershell", kind)
	}
	if !filepath.IsAbs(shimDir) || strings.ContainsAny(shimDir, "\x00\r\n") {
		return "", fmt.Errorf("shim directory must be an absolute path without NUL or line breaks")
	}
	if kind == PowerShell {
		if strings.ContainsRune(shimDir, os.PathListSeparator) {
			return "", fmt.Errorf("shim directory cannot contain the PATH separator")
		}
		return "$env:PATH = " + psquote(shimDir) + " + [IO.Path]::PathSeparator + (($env:PATH -split [regex]::Escape([string][IO.Path]::PathSeparator) | Where-Object { $_ -ne " + psquote(shimDir) + " }) -join [IO.Path]::PathSeparator)\n", nil
	}
	// Remove duplicate entries without glob matching or shell evaluation. Empty PATH
	// entries retain their meaning. Bash and Zsh both support this parameter syntax.
	code := "_ccow_dir=" + quote(shimDir) + "\n" +
		"_ccow_rest=${PATH-}\n_ccow_tail=\n_ccow_sep=\n" +
		"while :; do\n  case $_ccow_rest in\n    *:*) _ccow_part=${_ccow_rest%%:*}; _ccow_rest=${_ccow_rest#*:}; _ccow_more=1 ;;\n    *) _ccow_part=$_ccow_rest; _ccow_more=0 ;;\n  esac\n" +
		"  if [ \"$_ccow_part\" != \"$_ccow_dir\" ]; then\n    _ccow_tail=\"$_ccow_tail$_ccow_sep$_ccow_part\"\n    _ccow_sep=:\n  fi\n  [ \"$_ccow_more\" = 1 ] || break\ndone\n" +
		"PATH=\"$_ccow_dir${_ccow_sep:+:}$_ccow_tail\"\nexport PATH\nunset _ccow_dir _ccow_rest _ccow_tail _ccow_sep _ccow_part _ccow_more\n"
	if strings.Contains(shimDir, ":") {
		return "", fmt.Errorf("shim directory cannot contain the PATH separator ':'")
	}
	return code, nil
}

type Selection struct {
	File              string // Explicit --file takes precedence.
	Home              string
	ZDotDir           string
	PowerShellProfile string // Supply $PROFILE.CurrentUserCurrentHost from PowerShell 7.
}

// SelectFile does not guess between existing Bash startup files or PowerShell hosts.
func SelectFile(kind Kind, s Selection) (string, error) {
	if !kind.Valid() {
		return "", fmt.Errorf("unsupported shell %q", kind)
	}
	if s.File != "" {
		return filepath.Abs(s.File)
	}
	if kind == PowerShell {
		if s.PowerShellProfile == "" {
			return "", fmt.Errorf("PowerShell profile selection is ambiguous; pass --file with $PROFILE.CurrentUserCurrentHost from PowerShell 7")
		}
		return filepath.Abs(s.PowerShellProfile)
	}
	if s.Home == "" {
		return "", fmt.Errorf("home directory is unknown; select a configuration file with --file")
	}
	if kind == Zsh {
		dir := s.ZDotDir
		if dir == "" {
			dir = s.Home
		}
		return filepath.Abs(filepath.Join(dir, ".zshrc"))
	}
	var existing []string
	for _, n := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile"} {
		p := filepath.Join(s.Home, n)
		_, err := os.Lstat(p)
		if err == nil {
			existing = append(existing, p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if len(existing) > 1 {
		return "", fmt.Errorf("multiple Bash configuration files exist; select the intended file with --file")
	}
	if len(existing) == 1 {
		return filepath.Abs(existing[0])
	}
	return filepath.Abs(filepath.Join(s.Home, ".bashrc"))
}

type Action string

const (
	Setup  Action = "setup"
	Remove Action = "remove"
)
const begin = "# >>> CacheCow owned activation >>>"
const end = "# <<< CacheCow owned activation <<<"

// Separate markers record a separator inserted for a profile without a final
// newline. Removal can then restore the original bytes exactly.
const joinedBegin = "# >>> CacheCow owned activation (joined) >>>"

type Plan struct {
	Path     string
	Kind     Kind
	Action   Action
	Before   []byte
	After    []byte
	mode     os.FileMode
	existed  bool
	info     os.FileInfo
	security *fileSecurity
}

func (p *Plan) Changed() bool { return !bytes.Equal(p.Before, p.After) }

// CurrentSessionInstruction is a notice, not activation code.
func (p *Plan) CurrentSessionInstruction() string {
	if p.Action == Remove {
		return "This change applies to new shells. In this shell, remove the CacheCow shim directory from PATH or restart the shell."
	}
	return "This change applies to new shells. Apply `ccow shell init " + string(p.Kind) + "` output in this shell to activate now."
}

func Prepare(kind Kind, action Action, path, shimDir string) (*Plan, error) {
	if !kind.Valid() || (action != Setup && action != Remove) {
		return nil, fmt.Errorf("invalid shell or shell action")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: abs, Kind: kind, Action: action, mode: 0600}
	st, err := os.Lstat(abs)
	if err == nil {
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("configuration %s is not a regular file; use --file to select its real path", abs)
		}
		p.info = st
		p.existed = true
		p.mode = st.Mode().Perm()
		p.security, err = readSecurity(abs)
		if err != nil {
			return nil, err
		}
		p.Before, err = os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	start, stop, joined, err := block(p.Before)
	if err != nil {
		return nil, err
	}
	if action == Remove {
		p.After = bytes.Clone(p.Before)
		if start >= 0 {
			if joined {
				if start >= 2 && bytes.Equal(p.Before[start-2:start], []byte("\r\n")) {
					start -= 2
				} else if start > 0 && p.Before[start-1] == '\n' {
					start--
				}
			}
			p.After = append(bytes.Clone(p.Before[:start]), p.Before[stop:]...)
		}
		return p, nil
	}
	code, err := Init(kind, shimDir)
	if err != nil {
		return nil, err
	}
	nl := "\n"
	if bytes.Contains(p.Before, []byte("\r\n")) {
		nl = "\r\n"
	}
	marker := begin
	if start >= 0 && joined {
		marker = joinedBegin
	}
	if start < 0 && len(p.Before) > 0 && p.Before[len(p.Before)-1] != '\n' {
		marker = joinedBegin
	}
	owned := []byte(marker + nl + strings.ReplaceAll(code, "\n", nl) + end + nl)
	if start >= 0 {
		p.After = append(bytes.Clone(p.Before[:start]), owned...)
		p.After = append(p.After, p.Before[stop:]...)
	} else {
		p.After = bytes.Clone(p.Before)
		if marker == joinedBegin {
			p.After = append(p.After, []byte(nl)...)
		}
		p.After = append(p.After, owned...)
	}
	return p, nil
}

func block(b []byte) (start, stop int, joined bool, err error) {
	start = -1
	stop = -1
	offset := 0
	for _, line := range bytes.SplitAfter(b, []byte("\n")) {
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		switch text {
		case begin, joinedBegin:
			if start >= 0 {
				return -1, -1, false, fmt.Errorf("multiple or nested CacheCow blocks; repair the configuration manually")
			}
			start = offset
			joined = text == joinedBegin
		case end:
			if start < 0 || stop >= 0 {
				return -1, -1, false, fmt.Errorf("unmatched CacheCow block marker; repair the configuration manually")
			}
			stop = offset + len(line)
		}
		offset += len(line)
	}
	if start >= 0 && stop < 0 {
		err = fmt.Errorf("unfinished CacheCow block; repair the configuration manually")
	}
	return
}

type Approval struct {
	Yes         bool
	Interactive bool
	// Confirm must show the target Path and proposed Before/After change before asking.
	// It returns false on cancellation. No callback is called in noninteractive mode.
	Confirm func(*Plan) (bool, error)
}

var ErrConfirmationRequired = errors.New("shell update requires confirmation; review the proposed target/change and use --yes for noninteractive operation")
var ErrCancelled = errors.New("shell update cancelled")

func (p *Plan) Apply(a Approval) error {
	if !a.Yes {
		if !a.Interactive || a.Confirm == nil {
			return ErrConfirmationRequired
		}
		ok, err := a.Confirm(p)
		if err != nil {
			return err
		}
		if !ok {
			return ErrCancelled
		}
	}
	return p.apply(os.Rename)
}
func (p *Plan) unchanged() error {
	st, err := os.Lstat(p.Path)
	if !p.existed {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("configuration appeared after review; prepare and confirm again")
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || !os.SameFile(st, p.info) || st.Mode().Perm() != p.mode {
		return fmt.Errorf("configuration changed after review; prepare and confirm again")
	}
	same, err := p.security.matches(p.Path)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("configuration permissions changed after review; prepare and confirm again")
	}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, p.Before) {
		return fmt.Errorf("configuration changed after review; prepare and confirm again")
	}
	return nil
}
func (p *Plan) apply(rename func(string, string) error) error {
	if err := p.unchanged(); err != nil {
		return err
	}
	if !p.Changed() {
		return nil
	}
	// The profile directory must already exist. Do not create arbitrary parent
	// directories selected by an ambiguous shell configuration.
	f, err := os.CreateTemp(filepath.Dir(p.Path), ".cachecow-shell-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(p.mode); err != nil {
		return err
	}
	if _, err = f.Write(p.After); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = p.unchanged(); err != nil {
		return err
	}
	if p.security != nil {
		if err := p.security.preserve(f.Name()); err != nil {
			return err
		}
	}
	if err := rename(f.Name(), p.Path); err != nil {
		return err
	}
	slog.Debug("CacheCow shell configuration updated", "path", p.Path, "shell", p.Kind, "action", p.Action)
	return nil
}
