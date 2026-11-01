//go:build linux || darwin

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func stateRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
func shell(dir, script string) Command {
	return Command{Path: "/bin/sh", Args: []string{"sh", "-c", script}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin", "TEST_VALUE=separate"}}
}
func hooks() Hooks {
	return Hooks{Owned: func(Scope) error { return nil }, Drained: func(Scope) error { return nil }}
}
func TestNativeStreamsAndExit(t *testing.T) {
	dir := stateRoot(t)
	input, err := os.CreateTemp(dir, "input")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	_, _ = input.WriteString("from stdin\n")
	_, _ = input.Seek(0, 0)
	out, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	stderr, err := os.CreateTemp(dir, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	owned := false
	drained := false
	h := Hooks{Owned: func(s Scope) error {
		owned = true
		if s.Started {
			t.Error("child started before ownership barrier")
		}
		return nil
	}, Drained: func(s Scope) error {
		drained = true
		if !s.Complete || s.ExitCode != 37 {
			t.Error("incorrect completion")
		}
		return nil
	}}
	r, err := Run(context.Background(), dir, shell(dir, `read value; printf '%s|%s|%s' "$value" "$TEST_VALUE" "$PWD"; printf 'error only' >&2; exit 37`), Streams{input, out, stderr}, h)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 37 || !owned || !drained {
		t.Fatalf("result=%+v owned=%v drained=%v", r, owned, drained)
	}
	b, _ := os.ReadFile(out.Name())
	if string(b) != "from stdin|separate|"+dir {
		t.Fatalf("stdout %q", b)
	}
	b, _ = os.ReadFile(stderr.Name())
	if string(b) != "error only" {
		t.Fatalf("stderr %q", b)
	}
	b, _ = os.ReadFile(filepath.Join(r.Scope.Directory, "scope.json"))
	if strings.Contains(string(b), "TEST_VALUE") || strings.Contains(string(b), "printf") {
		t.Fatal("sensitive command persisted")
	}
}
func TestOwnershipFailureNeverStartsChild(t *testing.T) {
	dir := stateRoot(t)
	sentinel := filepath.Join(dir, "started")
	failure := errors.New("durable claim failed")
	h := hooks()
	h.Owned = func(Scope) error { return failure }
	h.Drained = func(Scope) error { t.Error("unexpected completion callback"); return nil }
	_, err := Run(context.Background(), dir, shell(dir, `touch "`+sentinel+`"`), Streams{}, h)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("child crossed failed barrier")
	}
}
func TestCancellationWaitsFiveSeconds(t *testing.T) {
	dir := stateRoot(t)
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(ready); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	started := time.Now()
	r, err := Run(ctx, dir, shell(dir, `trap '' TERM; touch "`+ready+`"; while :; do sleep 1; done`), Streams{}, hooks())
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < GracePeriod {
		t.Fatal("killed before five-second grace")
	}
	if r.ExitCode != 137 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
}
func TestRootExitDrainsDetachedDescendant(t *testing.T) {
	dir := stateRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := Command{Path: exe, Args: []string{exe, "-test.run=^TestTreeHelper$"}, Dir: dir, Env: append(os.Environ(), "CCOW_TREE_HELPER=root", "CCOW_TEST_DIR="+dir)}
	start := time.Now()
	r, err := Run(context.Background(), dir, command, Streams{}, hooks())
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 23 {
		t.Fatalf("root exit=%d", r.ExitCode)
	}
	if time.Since(start) < GracePeriod {
		t.Fatal("descendant skipped grace")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "descendant"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatalf("invalid descendant PID %q: %v", raw, err)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("descendant %d survived completed scope", pid)
	}
}
func TestTreeHelper(t *testing.T) {
	switch os.Getenv("CCOW_TREE_HELPER") {
	case "root":
		exe, _ := os.Executable()
		c := exec.Command(exe, "-test.run=^TestTreeHelper$")
		c.Env = append(os.Environ(), "CCOW_TREE_HELPER=child")
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if c.Start() != nil {
			os.Exit(90)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(os.Getenv("CCOW_TEST_DIR"), "descendant")); err == nil {
				os.Exit(23)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(91)
	case "child":
		// exec a shell so SIGTERM is ignored before readiness becomes visible.
		dir := os.Getenv("CCOW_TEST_DIR")
		_ = syscall.Exec("/bin/sh", []string{"sh", "-c", fmt.Sprintf(`trap '' TERM; printf '%%s' $$ > '%s'; while :; do sleep 1; done`, filepath.Join(dir, "descendant"))}, os.Environ())
		os.Exit(92)
	}
}

func TestManagerDeathStillDrains(t *testing.T) {
	dir := stateRoot(t)
	exe, _ := os.Executable()
	manager := exec.Command(exe, "-test.run=^TestManagerHelper$")
	manager.Env = append(os.Environ(), "CCOW_MANAGER_HELPER=1", "CCOW_TEST_DIR="+dir)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Process.Kill(); _ = manager.Wait() }()
	scopePath := filepath.Join(dir, "owned")
	deadline := time.Now().Add(20 * time.Second)
	var scopeDir string
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(scopePath)
		if err == nil {
			scopeDir = string(raw)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if scopeDir == "" {
		t.Fatal("manager did not own a scope")
	}
	// Wait until user code is running, not merely until ownership was persisted.
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil {
		t.Fatal("child not ready")
	}
	if err := manager.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	s := Scope{Version: 1, ID: filepath.Base(scopeDir), Directory: scopeDir}
	deadline = time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if Reconcile(s) == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("supervisor did not drain after manager death")
}
func TestManagerHelper(t *testing.T) {
	if os.Getenv("CCOW_MANAGER_HELPER") != "1" {
		return
	}
	dir := os.Getenv("CCOW_TEST_DIR")
	h := hooks()
	h.Owned = func(s Scope) error { return os.WriteFile(filepath.Join(dir, "owned"), []byte(s.Directory), 0600) }
	_, err := Run(context.Background(), dir, shell(dir, `trap '' TERM; touch ready; while :; do sleep 1; done`), Streams{}, h)
	if err != nil {
		os.Exit(95)
	}
	os.Exit(0)
}

func TestGracefulCancellationPreservesExit(t *testing.T) {
	dir := stateRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	r, err := Run(ctx, dir, shell(dir, `trap 'exit 42' TERM; touch ready; while :; do sleep 1; done`), Streams{}, hooks())
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 42 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
	if time.Since(start) >= GracePeriod {
		t.Fatal("graceful exit unnecessarily delayed")
	}
}
func TestSupervisorDeathBlocksRecovery(t *testing.T) {
	dir := stateRoot(t)
	scopeReady := make(chan Scope, 1)
	h := hooks()
	h.Owned = func(s Scope) error { scopeReady <- s; return nil }
	h.Drained = func(Scope) error { t.Error("dead supervisor reported drained"); return nil }
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), dir, shell(dir, `trap '' TERM; printf '%s' $$ > child; exec /bin/sleep 60`), Streams{}, h)
		done <- err
	}()
	var scope Scope
	select {
	case scope = <-scopeReady:
	case <-time.After(20 * time.Second):
		t.Fatal("no supervisor")
	}
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(dir, "child"))
		if err == nil {
			pid, _ = strconv.Atoi(string(raw))
			if pid > 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid <= 1 {
		_ = syscall.Kill(scope.OwnerPID, syscall.SIGTERM)
		t.Fatal("no child")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if err := syscall.Kill(scope.OwnerPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrReconciliation) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not report lost supervisor")
	}
	if !errors.Is(Reconcile(scope), ErrReconciliation) {
		t.Fatal("dead supervisor unlocked unfinished scope")
	}
}
func TestStartFailureExitCodes(t *testing.T) {
	dir := stateRoot(t)
	for _, tc := range []struct {
		name string
		mode os.FileMode
		want int
	}{{"missing", 0, 127}, {"denied", 0600, 126}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if tc.mode != 0 {
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Run(context.Background(), dir, Command{Path: path, Args: []string{path}, Dir: dir}, Streams{}, hooks())
			if err != nil {
				t.Fatal(err)
			}
			if r.ExitCode != tc.want {
				t.Fatalf("exit=%d want=%d", r.ExitCode, tc.want)
			}
		})
	}
}

func TestSupervisorSessionHooks(t *testing.T) {
	dir := stateRoot(t)
	r, err := Run(context.Background(), dir, shell(dir, `printf '%s' "$CCOW_TEST_SUPERVISOR" > hook-token`), Streams{}, hooks())
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(filepath.Join(dir, "hook-token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(token) != r.Scope.ID {
		t.Fatal("supervisor did not inject transient command environment")
	}
	for _, name := range []string{"test-ready", "test-quiesced", "test-drained"} {
		if _, err := os.Stat(filepath.Join(r.Scope.Directory, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCancellationAfterOwnershipFinalizesWithoutChild(t *testing.T) {
	dir := stateRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drained := false
	h := hooks()
	h.Owned = func(Scope) error { cancel(); return nil }
	h.Drained = func(s Scope) error {
		drained = true
		if s.Started || !s.Complete {
			t.Errorf("unexpected no-child scope: %+v", s)
		}
		return Reconcile(s)
	}
	r, err := Run(ctx, dir, shell(dir, "touch should-not-start"), Streams{}, h)
	if !errors.Is(err, context.Canceled) || !drained {
		t.Fatalf("error=%v drained=%v", err, drained)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-start")); !os.IsNotExist(err) {
		t.Fatal("child ran")
	}
	// Supervisor hooks must also finish after the control connection is gone.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(filepath.Join(r.Scope.Directory, "test-drained"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
