// SPDX-License-Identifier: Apache-2.0
package shell

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoundTripAndConfirmation(t *testing.T) {
	for _, kind := range []Kind{Bash, Zsh, PowerShell} {
		for _, original := range []string{"", "# keep\nexport A='한 글'\n", "# keep without newline", "# keep\r\n# CRLF\r\n"} {
			t.Run(string(kind)+original, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "profile with spaces")
				if err := os.WriteFile(path, []byte(original), 0640); err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(t.TempDir(), "shims '한 글' $x `cmd` [*]")
				p, err := Prepare(kind, Setup, path, dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Apply(Approval{}); !errors.Is(err, ErrConfirmationRequired) {
					t.Fatal(err)
				}
				if err := p.Apply(Approval{Interactive: true, Confirm: func(shown *Plan) (bool, error) {
					if shown.Path != path || !bytes.Equal(shown.Before, []byte(original)) {
						t.Fatal("wrong proposal")
					}
					return false, nil
				}}); !errors.Is(err, ErrCancelled) {
					t.Fatal(err)
				}
				if b, _ := os.ReadFile(path); string(b) != original {
					t.Fatal("changed before approval")
				}
				if err := p.Apply(Approval{Yes: true}); err != nil {
					t.Fatal(err)
				}
				again, err := Prepare(kind, Setup, path, dir)
				if err != nil {
					t.Fatal(err)
				}
				if again.Changed() {
					t.Fatal("not idempotent")
				}
				if runtime.GOOS != "windows" {
					st, _ := os.Stat(path)
					if st.Mode().Perm() != 0640 {
						t.Fatal("changed permissions")
					}
				}
				remove, err := Prepare(kind, Remove, path, "")
				if err != nil {
					t.Fatal(err)
				}
				if err := remove.Apply(Approval{Yes: true}); err != nil {
					t.Fatal(err)
				}
				if b, _ := os.ReadFile(path); string(b) != original {
					t.Fatalf("round trip changed bytes: %q != %q", b, original)
				}
				remove, err = Prepare(kind, Remove, path, "")
				if err != nil || remove.Changed() {
					t.Fatal("remove not idempotent", err)
				}
			})
		}
	}
}
func TestFailurePreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	original := []byte("# unrelated\n")
	os.WriteFile(path, original, 0600)
	p, err := Prepare(Bash, Setup, path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("simulated disk or rename failure")
	if err := p.apply(func(string, string) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, original) {
		t.Fatal("lost original")
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatal("leaked staging", files)
	}
	os.WriteFile(path, []byte("concurrent user edit"), 0600)
	if err := p.Apply(Approval{Yes: true}); err == nil {
		t.Fatal("overwrote concurrent edit")
	}
}
func TestMalformedAndSymlink(t *testing.T) {
	for _, s := range []string{begin + "\n", end + "\n", begin + "\n" + begin + "\n" + end + "\n", begin + "\n" + end + "\n" + begin + "\n" + end + "\n"} {
		path := filepath.Join(t.TempDir(), "profile")
		os.WriteFile(path, []byte(s), 0600)
		if _, err := Prepare(Bash, Setup, path, t.TempDir()); err == nil {
			t.Fatal("accepted malformed markers")
		}
	}
	if runtime.GOOS != "windows" {
		dir := t.TempDir()
		path := filepath.Join(dir, "profile")
		target := filepath.Join(dir, "target")
		os.WriteFile(target, []byte("keep"), 0600)
		os.Symlink(target, path)
		if _, err := Prepare(Bash, Setup, path, dir); err == nil {
			t.Fatal("followed profile symlink")
		}
	}
}
func TestSelection(t *testing.T) {
	home := t.TempDir()
	p, err := SelectFile(Bash, Selection{Home: home})
	if err != nil || p != filepath.Join(home, ".bashrc") {
		t.Fatal(p, err)
	}
	os.WriteFile(filepath.Join(home, ".bashrc"), nil, 0600)
	os.WriteFile(filepath.Join(home, ".bash_profile"), nil, 0600)
	if _, err := SelectFile(Bash, Selection{Home: home}); err == nil {
		t.Fatal("guessed ambiguous profile")
	}
	explicit := filepath.Join(home, "my profile")
	p, err = SelectFile(Bash, Selection{Home: home, File: explicit})
	if err != nil || p != explicit {
		t.Fatal(p, err)
	}
	p, err = SelectFile(Zsh, Selection{Home: home, ZDotDir: filepath.Join(home, "zsh")})
	if err != nil || p != filepath.Join(home, "zsh", ".zshrc") {
		t.Fatal(p, err)
	}
	if _, err := SelectFile(PowerShell, Selection{Home: home}); err == nil {
		t.Fatal("guessed PowerShell host")
	}
	p, err = SelectFile(PowerShell, Selection{PowerShellProfile: explicit})
	if err != nil || p != explicit {
		t.Fatal(p, err)
	}
}
func TestActivationInNativeShells(t *testing.T) {
	for _, kind := range []Kind{Bash, Zsh} {
		t.Run(string(kind), func(t *testing.T) {
			binary, err := exec.LookPath(string(kind))
			if err != nil {
				t.Skip(err)
			}
			dir := filepath.Join(t.TempDir(), "한 글 ' $HOME `touch bad` [*]")
			code, err := Init(kind, dir)
			if err != nil {
				t.Fatal(err)
			}
			initial := "/usr/bin::/bin:" + dir + ":" + dir
			args := []string{"-c", code + code + "printf '%s' \"$PATH\""}
			if kind == Zsh {
				// User startup files can change PATH before this isolated activation test.
				args = append([]string{"-f"}, args...)
			}
			cmd := exec.Command(binary, args...)
			cmd.Env = append(os.Environ(), "PATH="+initial)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			expected := dir + ":/usr/bin::/bin"
			if string(out) != expected {
				t.Fatalf("got %q expected %q", out, expected)
			}
		})
	}
	t.Run("powershell", func(t *testing.T) {
		binary, err := exec.LookPath("pwsh")
		if err != nil {
			t.Skip("PowerShell 7 unavailable")
		}
		dir := filepath.Join(t.TempDir(), "한 글 ' $HOME `cmd` [*]")
		code, err := Init(PowerShell, dir)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$env:PATH = $env:CCOW_TEST_INITIAL_PATH; "+code+code+"[Console]::Write($env:PATH)")
		initial := filepath.Join(t.TempDir(), "real")
		// PowerShell may prepend its installation directory during startup. Set
		// the controlled input inside the shell before exercising activation.
		cmd.Env = append(os.Environ(), "CCOW_TEST_INITIAL_PATH="+initial)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != dir+string(os.PathListSeparator)+initial {
			t.Fatalf("wrong activation: %q", out)
		}
	})
}
func TestInitValidation(t *testing.T) {
	if _, err := Init(Kind("fish"), t.TempDir()); err == nil {
		t.Fatal("accepted fish")
	}
	for _, p := range []string{"relative", "/tmp/a\nb", "/tmp/a\x00b"} {
		if _, err := Init(Bash, p); err == nil {
			t.Fatal("accepted", p)
		}
	}
	code, err := Init(PowerShell, filepath.Join(t.TempDir(), "a'b"))
	if err != nil || !strings.Contains(code, "a''b") {
		t.Fatal(code, err)
	}
}

func TestOwnedChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shims")
	code, err := Init(Bash, dir)
	if err != nil {
		t.Fatal(err)
	}
	owned := func(marker, nl string) string {
		return marker + nl + strings.ReplaceAll(code, "\n", nl) + end + nl
	}
	oldBlock := begin + "\n# stale activation\n" + end + "\n"
	for _, tc := range []struct {
		name      string
		original  string
		action    Action
		removed   string
		added     string
		separator bool
		changed   bool
	}{
		{"append to empty", "", Setup, "", owned(begin, "\n"), false, true},
		{"append to newline-terminated", "# keep\n", Setup, "", owned(begin, "\n"), false, true},
		{"append to profile without final newline", "# keep", Setup, "", owned(joinedBegin, "\n"), true, true},
		{"append to CRLF profile", "# keep\r\n", Setup, "", owned(begin, "\r\n"), false, true},
		{"append to CRLF profile without final newline", "# keep\r\n# tail", Setup, "", owned(joinedBegin, "\r\n"), true, true},
		{"replace different block", "# keep\n" + oldBlock + "# after\n", Setup, oldBlock, owned(begin, "\n"), false, true},
		{"replace different joined block", "# keep\n" + joinedBegin + "\n# stale\n" + end + "\n", Setup, joinedBegin + "\n# stale\n" + end + "\n", owned(joinedBegin, "\n"), false, true},
		{"identical block", "# keep\n" + owned(begin, "\n"), Setup, "", "", false, false},
		{"remove normal block", "# keep\n" + owned(begin, "\n") + "# after\n", Remove, owned(begin, "\n"), "", false, true},
		{"remove joined block", "# keep" + "\n" + owned(joinedBegin, "\n"), Remove, owned(joinedBegin, "\n"), "", true, true},
		{"remove joined CRLF block", "# keep\r\n" + owned(joinedBegin, "\r\n"), Remove, owned(joinedBegin, "\r\n"), "", true, true},
		{"remove joined block at file start", owned(joinedBegin, "\n"), Remove, owned(joinedBegin, "\n"), "", false, true},
		{"remove without block", "# keep\n", Remove, "", "", false, false},
		{"remove from empty", "", Remove, "", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile")
			if err := os.WriteFile(path, []byte(tc.original), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := Prepare(Bash, tc.action, path, dir)
			if err != nil {
				t.Fatal(err)
			}
			if p.Changed() != tc.changed {
				t.Fatalf("changed=%v want %v", p.Changed(), tc.changed)
			}
			removed, added, separator := p.OwnedChange()
			if string(removed) != tc.removed || string(added) != tc.added || separator != tc.separator {
				t.Fatalf("OwnedChange = %q, %q, %v; want %q, %q, %v", removed, added, separator, tc.removed, tc.added, tc.separator)
			}
			for _, b := range [][]byte{removed, added} {
				if len(b) == 0 {
					continue
				}
				if strings.Contains(string(b), "# keep") || strings.Contains(string(b), "# after") {
					t.Fatalf("block contains unrelated content: %q", b)
				}
				first, _, _ := strings.Cut(string(b), "\n")
				first = strings.TrimSuffix(first, "\r")
				if first != begin && first != joinedBegin {
					t.Fatalf("block does not start with a marker: %q", b)
				}
				if !strings.HasSuffix(strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"), end) {
					t.Fatalf("block does not end with the end marker: %q", b)
				}
			}
			// Returned slices must be copies.
			if len(added) > 0 {
				added[0] = 'X'
				_, again, _ := p.OwnedChange()
				if again[0] == 'X' {
					t.Fatal("OwnedChange exposed internal bytes")
				}
			}
		})
	}
}
