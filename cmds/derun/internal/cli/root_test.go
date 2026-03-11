package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStderrOutput(t *testing.T, fn func() int) (int, string) {
	t.Helper()

	originalStderr := os.Stderr
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe returned error: %v", err)
	}
	os.Stderr = stderrWriter

	stderrDone := make(chan string, 1)
	go func() {
		var stderrBuffer bytes.Buffer
		_, _ = io.Copy(&stderrBuffer, stderrReader)
		stderrDone <- stderrBuffer.String()
	}()

	exitCode := fn()

	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("stderrWriter.Close returned error: %v", err)
	}
	os.Stderr = originalStderr
	output := <-stderrDone
	_ = stderrReader.Close()

	return exitCode, output
}

func TestExecuteRootHelpFlagShowsDetailedUsage(t *testing.T) {
	exitCode, stderrOutput := captureStderrOutput(t, func() int {
		return Execute([]string{"--help"})
	})
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: got=%d want=0", exitCode)
	}
	if !strings.Contains(stderrOutput, "derun: terminal-faithful command execution with local transcript capture for MCP clients.") {
		t.Fatalf("expected root help header: %q", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "derun help [run|mcp]") {
		t.Fatalf("expected root help command list: %q", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "Use `derun help run` or `derun help mcp` for command-specific details.") {
		t.Fatalf("expected command-specific help hint: %q", stderrOutput)
	}
}

func TestExecuteHelpCommandRunTopicShowsDetailedUsage(t *testing.T) {
	exitCode, stderrOutput := captureStderrOutput(t, func() int {
		return Execute([]string{"help", "run"})
	})
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: got=%d want=0", exitCode)
	}
	if !strings.Contains(stderrOutput, "Run command: execute a target command with terminal-fidelity streaming and transcript capture.") {
		t.Fatalf("expected run help header: %q", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "Transport selection:") {
		t.Fatalf("expected run transport section: %q", stderrOutput)
	}
}

func TestExecuteHelpCommandMCPTopicShowsDetailedUsage(t *testing.T) {
	exitCode, stderrOutput := captureStderrOutput(t, func() int {
		return Execute([]string{"help", "mcp"})
	})
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: got=%d want=0", exitCode)
	}
	if !strings.Contains(stderrOutput, "MCP command: start derun's read-only MCP server over stdio.") {
		t.Fatalf("expected mcp help header: %q", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "Exposed MCP tools:") {
		t.Fatalf("expected mcp tool section: %q", stderrOutput)
	}
}

func TestExecuteHelpCommandRejectsUnknownTopic(t *testing.T) {
	exitCode, stderrOutput := captureStderrOutput(t, func() int {
		return Execute([]string{"help", "unknown-topic"})
	})
	if exitCode != 2 {
		t.Fatalf("unexpected exit code: got=%d want=2", exitCode)
	}
	if !strings.Contains(stderrOutput, "unknown help topic: unknown-topic") {
		t.Fatalf("expected unknown topic error: %q", stderrOutput)
	}
}
