package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendGitHubOutputWritesRawSingleLineValue(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "github-output.txt")

	err := appendGitHubOutput(outputPath, []githubOutputEntry{
		{Key: "status_url", Value: "https://example.com/status?context=perf%2Fcommit-tracker"},
	})
	if err != nil {
		t.Fatalf("append github output: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read github output: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "status_url=https://example.com/status?context=perf%2Fcommit-tracker") {
		t.Fatalf("expected raw output value, got=%s", content)
	}
	if strings.Contains(content, "%252F") {
		t.Fatalf("did not expect escaped percent value, got=%s", content)
	}
}

func TestAppendGitHubOutputWritesMultilineValueWithDelimiterSyntax(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "github-output.txt")
	value := "line-1\nline-2\n"

	err := appendGitHubOutput(outputPath, []githubOutputEntry{
		{Key: "markdown", Value: value},
	})
	if err != nil {
		t.Fatalf("append github output: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read github output: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "markdown<<COMMIT_TRACKER_OUTPUT_EOF") {
		t.Fatalf("expected multiline delimiter syntax, got=%s", content)
	}
	if !strings.Contains(content, value) {
		t.Fatalf("expected raw multiline value, got=%s", content)
	}
}
