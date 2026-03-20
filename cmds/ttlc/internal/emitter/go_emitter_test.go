package emitter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
	"github.com/delinoio/oss/cmds/ttlc/internal/parser"
	"github.com/delinoio/oss/cmds/ttlc/internal/sema"
)

func TestEmitGoDeterministic(t *testing.T) {
	outDir := t.TempDir()
	types := []sema.TypeDecl{
		{
			Name: "Artifact",
			Fields: []sema.TypeField{
				{Name: "Path", Type: "string"},
			},
		},
	}
	tasks := []sema.Task{
		{
			ID: "Build",
			Params: []sema.TaskParam{
				{Name: "target", Type: "string"},
			},
			ReturnType: "Vc[Artifact]",
		},
	}

	first, err := EmitGo("build", types, tasks, outDir)
	if err != nil {
		t.Fatalf("emit first: %v", err)
	}
	second, err := EmitGo("build", types, tasks, outDir)
	if err != nil {
		t.Fatalf("emit second: %v", err)
	}
	if string(first.Content) != string(second.Content) {
		t.Fatal("expected deterministic emitter output")
	}

	payload, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("read emitted file: %v", err)
	}
	if !strings.Contains(string(payload), "func Build(target string) Vc[Artifact]") {
		t.Fatalf("unexpected generated content: %s", string(payload))
	}
	if !strings.Contains(string(payload), "type Artifact struct") {
		t.Fatalf("expected generated type declaration, got=%s", string(payload))
	}
	if filepath.Base(first.Path) != "build_ttl_gen.go" {
		t.Fatalf("unexpected generated file name: %s", filepath.Base(first.Path))
	}
}

func TestEmitGoWithASTGeneratesCompilableCode(t *testing.T) {
	source := `package build

type Artifact struct {
    Path string
    Digest string
}

func makeDigest(input string) string {
    return hash(input)
}

task func Build(target string) Vc[Artifact] {
    digest := makeDigest(target)
    return vc(Artifact{Path: target, Digest: digest})
}

task func ResolveSource(target string) Vc[Artifact] {
    return vc(Artifact{Path: target, Digest: "seed"})
}
`
	module := parseModuleForTest(t, source)
	semaResult := sema.Check(module)
	if len(semaResult.Diagnostics) != 0 {
		t.Fatalf("unexpected sema diagnostics: %+v", semaResult.Diagnostics)
	}

	outDir := t.TempDir()
	result, err := EmitGoWithAST("build", semaResult.Types, semaResult.Tasks, semaResult.Funcs, module, outDir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	content := string(result.Content)
	if strings.Contains(content, "TODO(ttlc)") {
		t.Fatal("generated code should not contain TODO stubs when AST is provided")
	}
	if !strings.Contains(content, "_ttlPayload") {
		t.Fatal("expected embedded payload in generated code")
	}
	if !strings.Contains(content, "func Build(target string) Vc[Artifact]") {
		t.Fatal("expected Build wrapper function")
	}
	if !strings.Contains(content, "func makeDigest(input string) string") {
		t.Fatal("expected makeDigest wrapper function")
	}

	// Verify the generated code compiles by running go vet
	cmd := exec.Command("go", "vet", result.Path)
	cmd.Dir = outDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated code failed go vet: %v\noutput: %s\ncode:\n%s", err, string(output), content)
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
