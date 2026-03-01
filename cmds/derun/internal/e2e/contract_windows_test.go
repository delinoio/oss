//go:build windows

package e2e

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/derun/internal/cli"
	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/state"
	"github.com/delinoio/oss/cmds/derun/internal/transport"
)

func TestWindowsConPTYRunnerParity(t *testing.T) {
	setEnv(t, helperEnv, "1")

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}

	var output bytes.Buffer
	pid := 0
	result, err := transport.RunWindowsConPTY(
		context.Background(),
		helperCommandArgs(t, "ansi"),
		workingDir,
		func(startedPID int) error {
			pid = startedPID
			return nil
		},
		&output,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not implemented") {
			t.Skipf("conpty is unavailable: %v", err)
		}
		t.Fatalf("RunWindowsConPTY returned error: %v", err)
	}

	if pid <= 0 {
		t.Fatalf("expected positive pid from onStart callback")
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %v", result.ExitCode)
	}
	if !strings.Contains(output.String(), "\x1b[31mred\x1b[0m") {
		t.Fatalf("expected ansi output, got %q", output.String())
	}
}

func TestInteractiveRunUsesWindowsConPTYTransport(t *testing.T) {
	stateRoot := t.TempDir()
	setEnv(t, "DERUN_STATE_ROOT", stateRoot)
	setEnv(t, helperEnv, "1")

	consoleIn, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("console input handle unavailable: %v", err)
	}
	defer consoleIn.Close()

	consoleOut, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("console output handle unavailable: %v", err)
	}
	defer consoleOut.Close()

	originalStdin := os.Stdin
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	os.Stdin = consoleIn
	os.Stdout = consoleOut
	os.Stderr = consoleOut
	t.Cleanup(func() {
		os.Stdin = originalStdin
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	})

	sessionID := "01J1D111111111111111111111"
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
	if detail.TransportMode != contracts.DerunTransportModeWindowsConPTY {
		t.Fatalf("unexpected transport mode: got=%s want=%s", detail.TransportMode, contracts.DerunTransportModeWindowsConPTY)
	}
}
