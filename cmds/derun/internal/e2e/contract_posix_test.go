//go:build !windows

package e2e

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/delinoio/oss/cmds/derun/internal/cli"
	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func TestANSIParityThroughRunWithPTY(t *testing.T) {
	stateRoot := t.TempDir()
	setEnv(t, "DERUN_STATE_ROOT", stateRoot)
	setEnv(t, helperEnv, "1")

	ptyMaster, ttyFile, err := pty.Open()
	if err != nil {
		t.Skipf("pty is unavailable in test environment: %v", err)
	}
	defer ptyMaster.Close()
	defer ttyFile.Close()

	originalStdin := os.Stdin
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	os.Stdin = ttyFile
	os.Stdout = ttyFile
	os.Stderr = ttyFile
	t.Cleanup(func() {
		os.Stdin = originalStdin
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	})

	sessionID := "01J1C111111111111111111111"
	exitCode := cli.ExecuteRun(append([]string{"--session-id", sessionID, "--"}, helperCommandArgs(t, "ansi")...))
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: got=%d want=0", exitCode)
	}

	store, err := state.New(stateRoot)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	detail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if detail.TransportMode != contracts.DerunTransportModePosixPTY {
		t.Fatalf("unexpected transport mode: got=%s want=%s", detail.TransportMode, contracts.DerunTransportModePosixPTY)
	}
	if !detail.TTYAttached {
		t.Fatalf("expected tty_attached=true")
	}

	output := readAllOutput(t, store, sessionID)
	if output == "" {
		t.Fatalf("expected captured output")
	}
	if !containsANSISequence(output) {
		t.Fatalf("expected ansi sequence in output: %q", output)
	}
}

func TestSignalPropagationForCtrlC(t *testing.T) {
	stateRoot := t.TempDir()
	setEnv(t, "DERUN_STATE_ROOT", stateRoot)
	setEnv(t, helperEnv, "1")

	originalStdin := os.Stdin
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	devNullIn, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Open dev null for stdin returned error: %v", err)
	}
	devNullOut, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = devNullIn.Close()
		t.Fatalf("OpenFile dev null for stdout returned error: %v", err)
	}
	os.Stdin = devNullIn
	os.Stdout = devNullOut
	os.Stderr = devNullOut
	t.Cleanup(func() {
		os.Stdin = originalStdin
		os.Stdout = originalStdout
		os.Stderr = originalStderr
		_ = devNullIn.Close()
		_ = devNullOut.Close()
	})

	store, err := state.New(stateRoot)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}

	sessionID := "01J1C222222222222222222222"
	exitCh := make(chan int, 1)
	go func() {
		exitCh <- cli.ExecuteRun(append([]string{"--session-id", sessionID, "--"}, helperCommandArgs(t, "sleep")...))
	}()

	waitForSessionPID(t, store, sessionID, 3*time.Second)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("Kill returned error: %v", err)
	}

	select {
	case exitCode := <-exitCh:
		if exitCode != 130 {
			t.Fatalf("unexpected exit code after ctrl-c: got=%d want=130", exitCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for run completion after ctrl-c")
	}

	detail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if detail.State != contracts.DerunSessionStateSignaled {
		t.Fatalf("unexpected final state: got=%s want=%s", detail.State, contracts.DerunSessionStateSignaled)
	}
	if detail.Signal == "" {
		t.Fatalf("expected non-empty signal after ctrl-c")
	}
}

func containsANSISequence(text string) bool {
	return strings.Contains(text, "\x1b[31m") || strings.Contains(text, "\u001b[31m")
}
