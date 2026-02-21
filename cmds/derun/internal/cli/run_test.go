package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func TestExecuteRunPipeModeCapturesOutputAndExitCode(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
		t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

	exitCode := ExecuteRun([]string{"--", "sh", "-c", "printf 'out'; printf 'err' 1>&2; exit 7"})
	if exitCode != 7 {
		t.Fatalf("unexpected exit code: got=%d want=7", exitCode)
	}

	store, err := state.New(stateRoot)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}

	sessions, total, err := store.ListSessions("", 10)
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if total != 1 || len(sessions) != 1 {
		t.Fatalf("unexpected sessions: total=%d len=%d", total, len(sessions))
	}
	detail, err := store.GetSession(sessions[0].SessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if detail.State != contracts.DerunSessionStateExited {
		t.Fatalf("unexpected state: %s", detail.State)
	}
	if detail.ExitCode == nil || *detail.ExitCode != 7 {
		t.Fatalf("unexpected exit code in metadata: %v", detail.ExitCode)
	}
	if detail.OutputBytes < 6 {
		t.Fatalf("expected output bytes >= 6, got=%d", detail.OutputBytes)
	}

	finalPath := filepath.Join(stateRoot, "sessions", sessions[0].SessionID, "final.json")
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("final metadata should exist: %v", err)
	}
}

func TestExecuteRunRejectsDuplicateSessionID(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
		t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

	sessionID := "01J0S444444444444444444444"
	firstExitCode := ExecuteRun([]string{"--session-id", sessionID, "--", "sh", "-c", "printf 'first'"})
	if firstExitCode != 0 {
		t.Fatalf("unexpected first exit code: got=%d want=0", firstExitCode)
	}

	store, err := state.New(stateRoot)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	firstDetail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession after first run returned error: %v", err)
	}

	secondExitCode := ExecuteRun([]string{"--session-id", sessionID, "--", "sh", "-c", "printf 'second'; exit 9"})
	if secondExitCode != 2 {
		t.Fatalf("unexpected second exit code: got=%d want=2", secondExitCode)
	}

	secondDetail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession after duplicate rejection returned error: %v", err)
	}
	if secondDetail.OutputBytes != firstDetail.OutputBytes {
		t.Fatalf("duplicate session attempt should not append output: before=%d after=%d", firstDetail.OutputBytes, secondDetail.OutputBytes)
	}
	if secondDetail.ExitCode == nil || *secondDetail.ExitCode != 0 {
		t.Fatalf("duplicate session attempt should preserve first final metadata: %v", secondDetail.ExitCode)
	}

	sessions, total, err := store.ListSessions("", 10)
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if total != 1 || len(sessions) != 1 {
		t.Fatalf("unexpected sessions after duplicate rejection: total=%d len=%d", total, len(sessions))
	}
}
