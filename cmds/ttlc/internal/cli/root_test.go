package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	workspace := t.TempDir()
	writeTTLFile(t, filepath.Join(workspace, "main.ttl"))

	withWorkingDirectory(t, workspace, func() {
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
		entry, ok := payload["entry"].(string)
		if !ok {
			t.Fatalf("missing entry field in output: %v", payload)
		}
		if !strings.HasSuffix(entry, filepath.FromSlash("main.ttl")) {
			t.Fatalf("unexpected entry path: %s", entry)
		}
	})
}

func TestBuildUsesConfiguredFlags(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "test.ttl")
	writeTTLFile(t, entryPath)

	withWorkingDirectory(t, workspace, func() {
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
		generatedFiles, ok := payload["generated_files"].([]any)
		if !ok || len(generatedFiles) != 1 {
			t.Fatalf("unexpected generated_files payload: %#v", payload["generated_files"])
		}
		generatedFile, _ := generatedFiles[0].(string)
		if !strings.HasSuffix(generatedFile, filepath.FromSlash("out/build_ttl_gen.go")) {
			t.Fatalf("unexpected generated file path: %s", generatedFile)
		}
	})
}

func TestExplainSupportsTaskFilter(t *testing.T) {
	workspace := t.TempDir()
	writeTTLFile(t, filepath.Join(workspace, "main.ttl"))

	withWorkingDirectory(t, workspace, func() {
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
		tasks, ok := payload["tasks"].([]any)
		if !ok || len(tasks) != 1 {
			t.Fatalf("unexpected tasks payload: %#v", payload["tasks"])
		}
	})
}

func writeTTLFile(t *testing.T, path string) {
	t.Helper()
	content := `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    return vc(Artifact{Path: target})
}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}
}

func withWorkingDirectory(t *testing.T, directory string, run func()) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	run()
}
