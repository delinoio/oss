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
