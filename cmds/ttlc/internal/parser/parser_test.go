package parser

import (
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
)

func TestParseValidModule(t *testing.T) {
	source := `package build

type Artifact struct {
    Path string
    Digest string
}

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
    digest := hash(src.Path, src.Digest)
    return vc(Artifact{Path: src.Path, Digest: digest})
}

func Main(target string) {
    val := read(Build(target))
    print(val.Path)
}
`
	tokens, lexDiagnostics := lexer.Lex(source)
	if len(lexDiagnostics) != 0 {
		t.Fatalf("expected no lex diagnostics, got=%+v", lexDiagnostics)
	}
	module, parseDiagnostics := Parse(tokens)
	if len(parseDiagnostics) != 0 {
		t.Fatalf("expected no parse diagnostics, got=%+v", parseDiagnostics)
	}
	if module.PackageName != "build" {
		t.Fatalf("unexpected package: %s", module.PackageName)
	}
	if len(module.Decls) != 3 {
		t.Fatalf("unexpected declaration count: %d", len(module.Decls))
	}
	if _, ok := module.Decls[1].(*ast.TaskDecl); !ok {
		t.Fatalf("expected second declaration to be task, got=%T", module.Decls[1])
	}
}

func TestParseMalformedModuleReportsSyntaxError(t *testing.T) {
	source := `package build

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
`
	tokens, _ := lexer.Lex(source)
	_, diagnostics := Parse(tokens)
	if len(diagnostics) == 0 {
		t.Fatal("expected syntax diagnostics")
	}
}

func TestParseUnsupportedTopLevelDeclaration(t *testing.T) {
	source := `package build

var x = 1
`
	tokens, _ := lexer.Lex(source)
	_, diagnostics := Parse(tokens)
	if len(diagnostics) == 0 {
		t.Fatal("expected unsupported top-level declaration diagnostic")
	}
}
