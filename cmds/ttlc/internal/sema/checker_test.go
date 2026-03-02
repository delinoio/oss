package sema

import (
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
	"github.com/delinoio/oss/cmds/ttlc/internal/parser"
)

func TestCheckValidTask(t *testing.T) {
	module := parseModuleForTest(t, `package build

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
    return vc(src)
}
`)

	result := Check(module)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got=%+v", result.Diagnostics)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 task, got=%d", len(result.Tasks))
	}
	if result.Tasks[0].ID != "Build" {
		t.Fatalf("unexpected task id: %s", result.Tasks[0].ID)
	}
}

func TestCheckReportsUnsupportedImports(t *testing.T) {
	module := parseModuleForTest(t, `package build

import "example.com/x"

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}
`)

	result := Check(module)
	if len(result.Diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
	if result.Diagnostics[0].Kind != "unsupported_imports" {
		t.Fatalf("unexpected diagnostic kind: %s", result.Diagnostics[0].Kind)
	}
}

func TestCheckReportsNonVcTaskReturn(t *testing.T) {
	module := parseModuleForTest(t, `package build

task func Build() Artifact {
    return Artifact{}
}
`)
	result := Check(module)
	if len(result.Diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
	found := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Kind == "type_error" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected type_error diagnostic, got=%+v", result.Diagnostics)
	}
}

func TestCheckReportsReadArity(t *testing.T) {
	module := parseModuleForTest(t, `package build

task func Build() Vc[Artifact] {
    val := read(A(), B())
    return vc(val)
}

task func A() Vc[Artifact] {
    return vc(Artifact{})
}

task func B() Vc[Artifact] {
    return vc(Artifact{})
}
`)
	result := Check(module)
	found := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Message == "read(...) requires exactly one argument" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected read arity diagnostic, got=%+v", result.Diagnostics)
	}
}

func TestCheckExtractsTaskDependencies(t *testing.T) {
	module := parseModuleForTest(t, `package build

task func Build() Vc[Artifact] {
    a := read(ResolveA())
    b := read(ResolveB())
    return vc(merge(a, b))
}

task func ResolveA() Vc[Artifact] {
    return vc(Artifact{})
}

task func ResolveB() Vc[Artifact] {
    return vc(Artifact{})
}
`)
	result := Check(module)

	var buildDeps []string
	for _, task := range result.Tasks {
		if task.ID == "Build" {
			buildDeps = task.Deps
			break
		}
	}
	if len(buildDeps) != 2 {
		t.Fatalf("unexpected deps: %+v", buildDeps)
	}
	if buildDeps[0] != "ResolveA" || buildDeps[1] != "ResolveB" {
		t.Fatalf("unexpected deps order/content: %+v", buildDeps)
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
