package process

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func windowsStateRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestWindowsNativeStreamsAndExit(t *testing.T) {
	dir := windowsStateRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(dir, "input")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	_, _ = input.WriteString("stdin bytes")
	_, _ = input.Seek(0, 0)
	stdout, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(dir, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	owned, drained := false, false
	h := Hooks{Owned: func(s Scope) error {
		owned = true
		if s.Started {
			t.Error("child crossed barrier")
		}
		return nil
	}, Drained: func(s Scope) error {
		drained = true
		if !s.Complete {
			t.Error("unfinished scope")
		}
		return nil
	}}
	c := Command{Path: exe, Args: []string{exe, "-test.run=^TestWindowsChild$"}, Dir: dir, Env: append(os.Environ(), "CCOW_WINDOWS_CHILD=1")}
	r, err := Run(context.Background(), dir, c, Streams{input, stdout, stderr}, h)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 37 || !owned || !drained {
		t.Fatalf("result=%+v owned=%v drained=%v", r, owned, drained)
	}
	out, _ := os.ReadFile(stdout.Name())
	want := "stdin bytes|" + filepath.Clean(dir)
	if string(out) != want {
		t.Fatalf("stdout=%q want=%q", out, want)
	}
	out, _ = os.ReadFile(stderr.Name())
	if string(out) != "stderr bytes" {
		t.Fatalf("stderr=%q", out)
	}
}
func TestWindowsChild(t *testing.T) {
	if os.Getenv("CCOW_WINDOWS_CHILD") != "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(90)
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(91)
	}
	fmt.Fprintf(os.Stdout, "%s|%s", data, cwd)
	fmt.Fprint(os.Stderr, "stderr bytes")
	os.Exit(37)
}

func TestWindowsRootExitDrainsJobDescendant(t *testing.T) {
	dir := windowsStateRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := Command{Path: exe, Args: []string{exe, "-test.run=^TestWindowsTreeHelper$"}, Dir: dir, Env: append(os.Environ(), "CCOW_WINDOWS_TREE=root", "CCOW_TREE_DIR="+dir)}
	h := Hooks{Owned: func(Scope) error { return nil }, Drained: func(s Scope) error { return Reconcile(s) }}
	started := time.Now()
	result, err := Run(context.Background(), dir, command, Streams{}, h)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 23 {
		t.Fatalf("root status %d", result.ExitCode)
	}
	if time.Since(started) < GracePeriod {
		t.Fatal("job descendant skipped grace interval")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "child"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == nil {
		defer windows.CloseHandle(handle)
		status, err := windows.WaitForSingleObject(handle, 0)
		if err != nil || status == uint32(windows.WAIT_TIMEOUT) {
			t.Fatal("completed job still has a live descendant")
		}
	}
}
func TestWindowsTreeHelper(t *testing.T) {
	switch os.Getenv("CCOW_WINDOWS_TREE") {
	case "root":
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "-test.run=^TestWindowsTreeHelper$")
		cmd.Env = append(os.Environ(), "CCOW_WINDOWS_TREE=child")
		if cmd.Start() != nil {
			os.Exit(90)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(os.Getenv("CCOW_TREE_DIR"), "child")); err == nil {
				os.Exit(23)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(91)
	case "child":
		signal.Ignore(os.Interrupt)
		if os.WriteFile(filepath.Join(os.Getenv("CCOW_TREE_DIR"), "child"), []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
			os.Exit(92)
		}
		for {
			time.Sleep(time.Second)
		}
	}
}

func TestWindowsBatchArguments(t *testing.T) {
	dir := windowsStateRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".cmd", ".bat"} {
		t.Run(ext, func(t *testing.T) {
			wrapper := filepath.Join(dir, "npm wrapper 한글"+ext)
			body := "@echo off\r\n\"" + exe + "\" -test.run=^TestWindowsBatchChild$ -- %*\r\n"
			if err := os.WriteFile(wrapper, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"", "space value", "한글", "a&b", "a|b", "a<b", "a>b", "(value)", "caret^", "!VALUE!", `trailing\`}
			c := Command{Path: wrapper, Args: append([]string{wrapper}, args...), Dir: dir, Env: append(os.Environ(), "CCOW_BATCH_CHILD=1")}
			cmd, err := NewExecCommand(c)
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("%s: %v", out, err)
			}
			if !reflect.DeepEqual(got, args) {
				t.Fatalf("got=%q want=%q", got, args)
			}
			stdout, err := os.CreateTemp(dir, "batch-output")
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()
			h := Hooks{Owned: func(Scope) error { return nil }, Drained: Reconcile}
			r, err := Run(context.Background(), dir, c, Streams{Stdout: stdout}, h)
			if err != nil || r.ExitCode != 0 {
				t.Fatalf("supervised batch: %+v %v", r, err)
			}
			out, err = os.ReadFile(stdout.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &got); err != nil || !reflect.DeepEqual(got, args) {
				t.Fatalf("supervised output=%q: %v", out, err)
			}
			for _, arg := range []string{`%PATH%`, `"&echo injected`, "line\nnext"} {
				c.Args = []string{wrapper, arg}
				if _, err := NewExecCommand(c); err == nil {
					t.Fatalf("unsafe argument accepted: %q", arg)
				}
			}
		})
	}
}

func TestWindowsBatchChild(t *testing.T) {
	if os.Getenv("CCOW_BATCH_CHILD") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(90)
}
