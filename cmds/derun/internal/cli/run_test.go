package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func TestExecuteRunPipeModeCapturesOutputAndExitCode(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		return
	}

	originalStdin := os.Stdin
	originalStdout := os.Stdout

	devNullRead, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open dev null for stdin: %v", err)
	}
	devNullWrite, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = devNullRead.Close()
		t.Fatalf("open dev null for stdout: %v", err)
	}
	os.Stdin = devNullRead
	os.Stdout = devNullWrite
	t.Cleanup(func() {
		os.Stdin = originalStdin
		os.Stdout = originalStdout
		_ = devNullRead.Close()
		_ = devNullWrite.Close()
	})

	stateRoot := t.TempDir()
	if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
		t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}
	if err := os.Setenv("GO_WANT_HELPER_PROCESS", "1"); err != nil {
		t.Fatalf("Setenv GO_WANT_HELPER_PROCESS: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("GO_WANT_HELPER_PROCESS") })

	exitCode := ExecuteRun([]string{"--", testBinary, "-test.run=^TestExecuteRunPipeModeCapturesOutputAndExitCodeHelperProcess$", "--", "helper"})
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
	finalPath := filepath.Join(stateRoot, "sessions", sessions[0].SessionID, "final.json")
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("final metadata should exist: %v", err)
	}
}

func TestExecuteRunPipeModeCapturesOutputAndExitCodeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "helper" {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, "out")
	_, _ = fmt.Fprint(os.Stderr, "err")
	os.Exit(7)
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

func TestExecuteRunRejectsInvalidSessionID(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
		t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

	exitCode := ExecuteRun([]string{"--session-id", ".", "--", "sh", "-c", "printf 'should-not-run'"})
	if exitCode != 2 {
		t.Fatalf("unexpected exit code: got=%d want=2", exitCode)
	}

	sessionsPath := filepath.Join(stateRoot, "sessions")
	artifactFiles := []string{
		"meta.json",
		"final.json",
		"output.bin",
		"index.jsonl",
		"append.lock",
	}
	for _, artifact := range artifactFiles {
		artifactPath := filepath.Join(sessionsPath, artifact)
		if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
			t.Fatalf("expected no root-level artifact %s after invalid session id rejection, stat err=%v", artifactPath, err)
		}
	}

	entries, err := os.ReadDir(sessionsPath)
	if err != nil {
		t.Fatalf("ReadDir sessions path returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no session directories after invalid session id rejection, got=%d", len(entries))
	}
}

func TestExecuteRunRejectsNonWholeSecondRetention(t *testing.T) {
	testCases := []struct {
		name      string
		retention string
	}{
		{
			name:      "sub-second retention",
			retention: "500ms",
		},
		{
			name:      "fractional second retention",
			retention: "1500ms",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stateRoot := t.TempDir()
			if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
				t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
			}
			t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

			exitCode := ExecuteRun([]string{"--retention", tc.retention, "--", "sh", "-c", "printf 'should-not-run'"})
			if exitCode != 2 {
				t.Fatalf("unexpected exit code for retention %s: got=%d want=2", tc.retention, exitCode)
			}

			store, err := state.New(stateRoot)
			if err != nil {
				t.Fatalf("state.New returned error: %v", err)
			}

			sessions, total, err := store.ListSessions("", 10)
			if err != nil {
				t.Fatalf("ListSessions returned error: %v", err)
			}
			if total != 0 || len(sessions) != 0 {
				t.Fatalf("expected no sessions for invalid retention %s: total=%d len=%d", tc.retention, total, len(sessions))
			}
		})
	}
}

func TestExecuteRunPersistsWholeSecondRetention(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
		t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

	sessionID := "01J0S777777777777777777777"
	exitCode := ExecuteRun([]string{"--session-id", sessionID, "--retention", "1s", "--", "sh", "-c", "printf 'retention-ok'"})
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
	if detail.RetentionSeconds != 1 {
		t.Fatalf("unexpected retention seconds: got=%d want=1", detail.RetentionSeconds)
	}
}

func TestExecuteRunPersistsStartupFailureSessionMetadata(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Setenv("DERUN_STATE_ROOT", stateRoot); err != nil {
		t.Fatalf("Setenv DERUN_STATE_ROOT: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("DERUN_STATE_ROOT") })

	sessionID := "01J0S555555555555555555555"
	exitCode := ExecuteRun([]string{"--session-id", sessionID, "--", "definitely-not-real-command-derun-test"})
	if exitCode != 1 {
		t.Fatalf("unexpected exit code: got=%d want=1", exitCode)
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
		t.Fatalf("expected failed startup session to be listed: total=%d len=%d", total, len(sessions))
	}
	if sessions[0].SessionID != sessionID {
		t.Fatalf("unexpected listed session id: got=%s want=%s", sessions[0].SessionID, sessionID)
	}

	detail, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if detail.State != contracts.DerunSessionStateFailed {
		t.Fatalf("unexpected state: got=%s want=%s", detail.State, contracts.DerunSessionStateFailed)
	}
	if detail.Error == "" {
		t.Fatalf("expected startup failure detail error")
	}
	if !strings.Contains(detail.Error, "start process") && !strings.Contains(detail.Error, "start pty process") {
		t.Fatalf("unexpected startup failure error: %q", detail.Error)
	}

	metaPath := filepath.Join(stateRoot, "sessions", sessionID, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta metadata should exist for failed startup session: %v", err)
	}
}

func TestSelectTransportMode(t *testing.T) {
	testCases := []struct {
		name       string
		tty        bool
		goos       string
		wantResult contracts.DerunTransportMode
	}{
		{
			name:       "pipe mode without tty",
			tty:        false,
			goos:       "linux",
			wantResult: contracts.DerunTransportModePipe,
		},
		{
			name:       "posix pty mode on unix tty",
			tty:        true,
			goos:       "linux",
			wantResult: contracts.DerunTransportModePosixPTY,
		},
		{
			name:       "windows conpty mode on windows tty",
			tty:        true,
			goos:       "windows",
			wantResult: contracts.DerunTransportModeWindowsConPTY,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := selectTransportMode(tc.tty, tc.goos)
			if got != tc.wantResult {
				t.Fatalf("unexpected transport mode: got=%s want=%s", got, tc.wantResult)
			}
		})
	}
}
