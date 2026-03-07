package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
	"github.com/delinoio/oss/cmds/ttlc/internal/parser"
)

func TestGenerateGoSourceDeterministic(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
    Digest string
}

task func ResolveSource(target string) Vc[Artifact] {
    return vc(Artifact{Path: target, Digest: "seed"})
}

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
    digest := hash(src.Path, src.Digest)
    return vc(Artifact{Path: src.Path, Digest: digest})
}
`)

	program, err := BuildProgram(module, "Build", map[string]any{"target": "web"})
	if err != nil {
		t.Fatalf("build program: %v", err)
	}

	firstSource, err := GenerateGoSource(program)
	if err != nil {
		t.Fatalf("generate first source: %v", err)
	}
	secondSource, err := GenerateGoSource(program)
	if err != nil {
		t.Fatalf("generate second source: %v", err)
	}

	if string(firstSource) != string(secondSource) {
		t.Fatal("expected deterministic generated runner source")
	}
}

func TestExecuteSmoke(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
    Digest string
}

task func ResolveSource(target string) Vc[Artifact] {
    return vc(Artifact{Path: target, Digest: "seed"})
}

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
    digest := hash(src.Path, src.Digest)
    return vc(Artifact{Path: src.Path, Digest: digest})
}
`)

	program, err := BuildProgram(module, "Build", map[string]any{"target": "web"})
	if err != nil {
		t.Fatalf("build program: %v", err)
	}

	source, err := GenerateGoSource(program)
	if err != nil {
		t.Fatalf("generate source: %v", err)
	}

	result, err := Execute(context.Background(), t.TempDir(), source)
	if err != nil {
		t.Fatalf("execute runner: %v", err)
	}

	if len(result.ExecutedTasks) != 2 {
		t.Fatalf("expected two executed tasks, got=%+v", result.ExecutedTasks)
	}
	if result.ExecutedTasks[0] != "Build" || result.ExecutedTasks[1] != "ResolveSource" {
		t.Fatalf("unexpected execution trace: %+v", result.ExecutedTasks)
	}

	resultObject, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got=%T", result.Result)
	}
	path, ok := resultObject["Path"].(string)
	if !ok || path != "web" {
		t.Fatalf("unexpected Path: %#v", resultObject["Path"])
	}
	digest, ok := resultObject["Digest"].(string)
	if !ok {
		t.Fatalf("expected Digest to be a string, got=%T", resultObject["Digest"])
	}
	if strings.TrimSpace(digest) == "" {
		t.Fatal("expected non-empty digest")
	}
}

func parseModuleForTest(t *testing.T, source string) *ast.Module {
	t.Helper()
	tokens, lexDiagnostics := lexer.Lex(source)
	if len(lexDiagnostics) != 0 {
		t.Fatalf("unexpected lex diagnostics: %+v", lexDiagnostics)
	}

	module, parseDiagnostics := parser.Parse(tokens)
	if len(parseDiagnostics) != 0 {
		t.Fatalf("unexpected parse diagnostics: %+v", parseDiagnostics)
	}
	return module
}
