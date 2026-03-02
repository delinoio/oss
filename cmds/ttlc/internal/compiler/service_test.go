package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReportsUnsupportedImports(t *testing.T) {
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
		service := New()
		result, err := service.Check(context.Background(), CheckOptions{Entry: "./main.ttl"})
		if err != nil {
			t.Fatalf("check returned error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}
	})
}

func TestBuildWritesGeneratedFileAndCache(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    return vc(Artifact{Path: target})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Build(context.Background(), BuildOptions{Entry: "./main.ttl", OutDir: "./out"})
		if err != nil {
			t.Fatalf("build returned error: %v", err)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("expected no diagnostics, got=%+v", result.Diagnostics)
		}
		if len(result.GeneratedFiles) != 1 {
			t.Fatalf("expected one generated file, got=%+v", result.GeneratedFiles)
		}
		if _, err := os.Stat(result.GeneratedFiles[0]); err != nil {
			t.Fatalf("generated file missing: %v", err)
		}
		generatedPayload, err := os.ReadFile(result.GeneratedFiles[0])
		if err != nil {
			t.Fatalf("read generated file: %v", err)
		}
		if !strings.Contains(string(generatedPayload), "type Artifact struct") {
			t.Fatalf("expected emitted type declaration, got=%s", string(generatedPayload))
		}
		if _, err := os.Stat(result.CacheDBPath); err != nil {
			t.Fatalf("cache db missing: %v", err)
		}
	})
}

func TestExplainTaskFilter(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    return vc(Artifact{Path: target})
}

task func Resolve() Vc[Artifact] {
    return vc(Artifact{Path: "x"})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Explain(context.Background(), ExplainOptions{Entry: "./main.ttl", Task: "Build"})
		if err != nil {
			t.Fatalf("explain returned error: %v", err)
		}
		if len(result.Tasks) != 1 {
			t.Fatalf("expected filtered single task, got=%+v", result.Tasks)
		}
		if result.Tasks[0].ID != "Build" {
			t.Fatalf("unexpected task id: %s", result.Tasks[0].ID)
		}
		if strings.TrimSpace(result.FingerprintComponents.InputContentHash) == "" {
			t.Fatal("expected fingerprint components")
		}
	})
}

func withWorkingDirectory(t *testing.T, directory string, run func()) {
	t.Helper()
	currentDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(currentDirectory)
	}()
	run()
}
