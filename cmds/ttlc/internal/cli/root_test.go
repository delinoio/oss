package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
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

		envelope := decodeEnvelope(t, stdout.Bytes())
		if envelope.SchemaVersion != contracts.TtlSchemaVersionV1Alpha1 {
			t.Fatalf("unexpected schema version: %s", envelope.SchemaVersion)
		}
		if envelope.Command != contracts.TtlCommandCheck {
			t.Fatalf("unexpected command: %s", envelope.Command)
		}
		if envelope.Status != contracts.TtlResponseStatusOK {
			t.Fatalf("unexpected status: %s diagnostics=%+v", envelope.Status, envelope.Diagnostics)
		}

		data, ok := envelope.Data.(map[string]any)
		if !ok {
			t.Fatalf("expected object data payload, got=%T", envelope.Data)
		}
		entry, ok := data["entry"].(string)
		if !ok {
			t.Fatalf("missing entry field in output data: %v", data)
		}
		if !strings.HasSuffix(entry, filepath.FromSlash("main.ttl")) {
			t.Fatalf("unexpected entry path: %s", entry)
		}
	})
}

func TestCheckReportsFailedEnvelopeWhenDiagnosticsExist(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

import "example.com/x"

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		code := execute([]string{"check"}, stdout, stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got=%d stderr=%s", code, stderr.String())
		}

		envelope := decodeEnvelope(t, stdout.Bytes())
		if envelope.Status != contracts.TtlResponseStatusFailed {
			t.Fatalf("expected failed status, got=%s", envelope.Status)
		}
		if len(envelope.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}
	})
}

func TestCheckColorFlag(t *testing.T) {
	workspace := t.TempDir()
	writeTTLFile(t, filepath.Join(workspace, "main.ttl"))

	withWorkingDirectory(t, workspace, func() {
		colorStdout := &bytes.Buffer{}
		colorStderr := &bytes.Buffer{}
		colorCode := execute([]string{"check"}, colorStdout, colorStderr)
		if colorCode != 0 {
			t.Fatalf("expected color run to succeed, got=%d stderr=%s", colorCode, colorStderr.String())
		}
		if !strings.Contains(colorStderr.String(), "\x1b[") {
			t.Fatalf("expected ANSI color sequences in logs, got=%q", colorStderr.String())
		}

		noColorStdout := &bytes.Buffer{}
		noColorStderr := &bytes.Buffer{}
		noColorCode := execute([]string{"check", "--no-color"}, noColorStdout, noColorStderr)
		if noColorCode != 0 {
			t.Fatalf("expected no-color run to succeed, got=%d stderr=%s", noColorCode, noColorStderr.String())
		}
		if strings.Contains(noColorStderr.String(), "\x1b[") {
			t.Fatalf("did not expect ANSI color sequences when --no-color is set, got=%q", noColorStderr.String())
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

		envelope := decodeEnvelope(t, stdout.Bytes())
		if envelope.Command != contracts.TtlCommandBuild {
			t.Fatalf("unexpected command: %s", envelope.Command)
		}
		if envelope.Status != contracts.TtlResponseStatusOK {
			t.Fatalf("unexpected status: %s diagnostics=%+v", envelope.Status, envelope.Diagnostics)
		}
		data, ok := envelope.Data.(map[string]any)
		if !ok {
			t.Fatalf("expected object data payload, got=%T", envelope.Data)
		}
		generatedFiles, ok := data["generated_files"].([]any)
		if !ok || len(generatedFiles) != 1 {
			t.Fatalf("unexpected generated_files payload: %#v", data["generated_files"])
		}
		generatedFile, _ := generatedFiles[0].(string)
		if !strings.HasSuffix(generatedFile, filepath.FromSlash("out/build_ttl_gen.go")) {
			t.Fatalf("unexpected generated file path: %s", generatedFile)
		}

		cacheAnalysis, ok := data["cache_analysis"].([]any)
		if !ok || len(cacheAnalysis) == 0 {
			t.Fatalf("expected cache_analysis entries, got=%#v", data["cache_analysis"])
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

		envelope := decodeEnvelope(t, stdout.Bytes())
		if envelope.Command != contracts.TtlCommandExplain {
			t.Fatalf("unexpected command: %s", envelope.Command)
		}
		if envelope.Status != contracts.TtlResponseStatusOK {
			t.Fatalf("unexpected status: %s diagnostics=%+v", envelope.Status, envelope.Diagnostics)
		}

		data, ok := envelope.Data.(map[string]any)
		if !ok {
			t.Fatalf("expected object data payload, got=%T", envelope.Data)
		}
		tasks, ok := data["tasks"].([]any)
		if !ok || len(tasks) != 1 {
			t.Fatalf("unexpected tasks payload: %#v", data["tasks"])
		}

		cacheAnalysis, ok := data["cache_analysis"].([]any)
		if !ok || len(cacheAnalysis) != 1 {
			t.Fatalf("unexpected cache_analysis payload: %#v", data["cache_analysis"])
		}
		analysisRow, ok := cacheAnalysis[0].(map[string]any)
		if !ok {
			t.Fatalf("expected object cache analysis row, got=%T", cacheAnalysis[0])
		}
		if analysisRow["invalidation_reason"] != string(contracts.TtlInvalidationReasonCacheMiss) {
			t.Fatalf("unexpected invalidation reason: %#v", analysisRow["invalidation_reason"])
		}
	})
}

type envelopePayload struct {
	SchemaVersion contracts.TtlSchemaVersion  `json:"schema_version"`
	Command       contracts.TtlCommand        `json:"command"`
	Status        contracts.TtlResponseStatus `json:"status"`
	Diagnostics   []map[string]any            `json:"diagnostics"`
	Data          any                         `json:"data"`
}

func decodeEnvelope(t *testing.T, payload []byte) envelopePayload {
	t.Helper()
	envelope := envelopePayload{}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	return envelope
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
