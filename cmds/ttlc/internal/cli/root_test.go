package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteRequiresCommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("expected usage output, got=%s", stderr.String())
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"unknown"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2, got=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got=%s", stderr.String())
	}
}

func TestCheckUsesDefaultEntry(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"check"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if payload["entry"] != defaultEntryPath {
		t.Fatalf("unexpected entry: got=%v want=%s", payload["entry"], defaultEntryPath)
	}
}

func TestBuildUsesConfiguredFlags(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"build", "--entry", "./test.ttl", "--out-dir", "./out"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if payload["entry"] != "./test.ttl" {
		t.Fatalf("unexpected entry: %v", payload["entry"])
	}
	if payload["outDir"] != "./out" {
		t.Fatalf("unexpected outDir: %v", payload["outDir"])
	}
}

func TestExplainSupportsTaskFilter(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := execute([]string{"explain", "--task", "Build"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got=%d stderr=%s", code, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if payload["entry"] != defaultEntryPath {
		t.Fatalf("unexpected entry: got=%v want=%s", payload["entry"], defaultEntryPath)
	}
}
