package e2e

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/cli"
	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

const helperEnv = "GO_WANT_DERUN_E2E_HELPER"

func TestDerunE2EHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}

	mode, modeArgs, ok := helperModeFromArgs(os.Args)
	if !ok {
		os.Exit(2)
	}

	switch mode {
	case "ansi":
		_, _ = os.Stdout.WriteString("\x1b[31mred\x1b[0m\n")
		os.Exit(0)
	case "token":
		if len(modeArgs) < 1 || modeArgs[0] == "" {
			os.Exit(2)
		}
		token := modeArgs[0]
		for i := 0; i < 5; i++ {
			_, _ = fmt.Fprintf(os.Stdout, "%s-%d\n", token, i)
			time.Sleep(15 * time.Millisecond)
		}
		os.Exit(0)
	case "large":
		if len(modeArgs) < 1 {
			os.Exit(2)
		}
		expectedBytes, err := strconv.Atoi(modeArgs[0])
		if err != nil || expectedBytes <= 0 {
			os.Exit(2)
		}
		block := strings.Repeat("A", 8192)
		remaining := expectedBytes
		for remaining > 0 {
			n := len(block)
			if remaining < n {
				n = remaining
			}
			payload := []byte(block[:n])
			written := 0
			for written < len(payload) {
				w, err := os.Stdout.Write(payload[written:])
				if err != nil {
					os.Exit(1)
				}
				if w <= 0 {
					os.Exit(1)
				}
				written += w
			}
			remaining -= n
		}
		os.Exit(0)
	case "sleep":
		for {
			time.Sleep(200 * time.Millisecond)
		}
	default:
		os.Exit(2)
	}
}

func TestConcurrentSessionsAreIsolated(t *testing.T) {
	stateRoot := t.TempDir()
	setEnv(t, "DERUN_STATE_ROOT", stateRoot)
	setEnv(t, helperEnv, "1")
	muteStdStreams(t)

	store, err := state.New(stateRoot)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}

	sessions := []struct {
		sessionID string
		token     string
	}{
		{sessionID: "01J1A111111111111111111111", token: "alpha"},
		{sessionID: "01J1A222222222222222222222", token: "beta"},
		{sessionID: "01J1A333333333333333333333", token: "gamma"},
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(sessions))
	for _, tc := range sessions {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			args := append([]string{"--session-id", tc.sessionID, "--"}, helperCommandArgs(t, "token", tc.token)...)
			exitCode := cli.ExecuteRun(args)
			if exitCode != 0 {
				errCh <- fmt.Errorf("unexpected exit code for session %s: got=%d want=0", tc.sessionID, exitCode)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for runErr := range errCh {
		t.Fatalf("ExecuteRun returned error: %v", runErr)
	}

	for _, tc := range sessions {
		detail, err := store.GetSession(tc.sessionID)
		if err != nil {
			t.Fatalf("GetSession(%s) returned error: %v", tc.sessionID, err)
		}
		if detail.State != contracts.DerunSessionStateExited {
			t.Fatalf("unexpected state for %s: got=%s want=%s", tc.sessionID, detail.State, contracts.DerunSessionStateExited)
		}

		outputText := waitForOutputContainingToken(t, store, tc.sessionID, tc.token, 2*time.Second)
		for _, other := range sessions {
			if other.sessionID == tc.sessionID {
				continue
			}
			if strings.Contains(outputText, other.token) {
				t.Fatalf("session %s output leaked token from session %s", tc.sessionID, other.sessionID)
			}
		}
	}
}

func TestLargeOutputChunkingHasStableCursorProgression(t *testing.T) {
	stateRoot := t.TempDir()
	setEnv(t, "DERUN_STATE_ROOT", stateRoot)
	setEnv(t, helperEnv, "1")
	muteStdStreams(t)

	const targetBytes = 256 * 1024
	sessionID := "01J1B111111111111111111111"

	exitCode := cli.ExecuteRun(append(
		[]string{"--session-id", sessionID, "--"},
		helperCommandArgs(t, "large", strconv.Itoa(targetBytes))...,
	))
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
	if detail.OutputBytes < 128*1024 {
		t.Fatalf("expected sustained large output, got only %d bytes", detail.OutputBytes)
	}

	cursor := uint64(0)
	totalDecodedBytes := 0
	for step := 0; step < 1024; step++ {
		chunks, nextCursor, eof, err := store.ReadOutput(sessionID, cursor, 4096)
		if err != nil {
			t.Fatalf("ReadOutput returned error at cursor %d: %v", cursor, err)
		}
		if nextCursor < cursor {
			t.Fatalf("cursor regressed: cursor=%d next=%d", cursor, nextCursor)
		}
		for _, chunk := range chunks {
			decoded, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
			if err != nil {
				t.Fatalf("DecodeString returned error: %v", err)
			}
			totalDecodedBytes += len(decoded)
		}

		if eof {
			if nextCursor != detail.OutputBytes {
				t.Fatalf("unexpected final cursor: got=%d want=%d", nextCursor, detail.OutputBytes)
			}
			if uint64(totalDecodedBytes) != detail.OutputBytes {
				t.Fatalf("unexpected decoded byte count: got=%d want=%d", totalDecodedBytes, detail.OutputBytes)
			}
			return
		}
		if nextCursor == cursor {
			t.Fatalf("cursor did not advance at step=%d cursor=%d", step, cursor)
		}
		cursor = nextCursor
	}

	t.Fatalf("output stream did not reach eof after maximum steps")
}

func readAllOutput(t *testing.T, store *state.Store, sessionID string) string {
	t.Helper()

	chunks, _, _, err := store.ReadOutput(sessionID, 0, 10*1024*1024)
	if err != nil {
		t.Fatalf("ReadOutput(%s) returned error: %v", sessionID, err)
	}
	var builder strings.Builder
	for _, chunk := range chunks {
		decoded, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil {
			t.Fatalf("DecodeString returned error for session %s: %v", sessionID, err)
		}
		builder.Write(decoded)
	}
	return builder.String()
}

func waitForOutputContainingToken(
	t *testing.T,
	store *state.Store,
	sessionID string,
	token string,
	timeout time.Duration,
) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	lastOutput := ""
	for time.Now().Before(deadline) {
		lastOutput = readAllOutput(t, store, sessionID)
		if strings.Contains(lastOutput, token) {
			return lastOutput
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("session %s output missing token %q within %s: %q", sessionID, token, timeout, lastOutput)
	return ""
}

func waitForSessionPID(t *testing.T, store *state.Store, sessionID string, timeout time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		detail, err := store.GetSession(sessionID)
		if err == nil && detail.PID > 0 {
			return detail.PID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for session pid: %s", sessionID)
	return 0
}

func helperModeFromArgs(args []string) (string, []string, bool) {
	for i, arg := range args {
		if arg == "--" {
			if i+1 >= len(args) {
				return "", nil, false
			}
			return args[i+1], args[i+2:], true
		}
	}
	return "", nil, false
}

func helperCommandArgs(t *testing.T, mode string, modeArgs ...string) []string {
	t.Helper()

	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	commandArgs := []string{testBinary, "-test.run=^TestDerunE2EHelperProcess$", "--", mode}
	commandArgs = append(commandArgs, modeArgs...)
	return commandArgs
}

func setEnv(t *testing.T, key string, value string) {
	t.Helper()
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("Setenv %s returned error: %v", key, err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(key)
	})
}

func muteStdStreams(t *testing.T) {
	t.Helper()

	devNullOut, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile dev null returned error: %v", err)
	}

	originalStdout := os.Stdout
	originalStderr := os.Stderr
	os.Stdout = devNullOut
	os.Stderr = devNullOut
	t.Cleanup(func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
		_ = devNullOut.Close()
	})
}

func decodeChunks(chunks []session.OutputChunk) (string, error) {
	var builder strings.Builder
	for _, chunk := range chunks {
		decoded, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil {
			return "", err
		}
		builder.Write(decoded)
	}
	return builder.String(), nil
}
