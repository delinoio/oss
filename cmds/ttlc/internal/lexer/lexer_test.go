package lexer

import "testing"

func TestLexKeywordsAndSymbols(t *testing.T) {
	source := `package build

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
    return vc(Artifact{Path: src.Path})
}
`
	tokens, diagnostics := Lex(source)
	if len(diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got=%+v", diagnostics)
	}
	if len(tokens) == 0 {
		t.Fatal("expected tokens")
	}
	if tokens[0].Kind != TokenKeywordPackage {
		t.Fatalf("expected first token package, got=%s", tokens[0].Kind)
	}
	if tokens[len(tokens)-1].Kind != TokenEOF {
		t.Fatalf("expected EOF token at end, got=%s", tokens[len(tokens)-1].Kind)
	}
}

func TestLexReportsUnsupportedToken(t *testing.T) {
	tokens, diagnostics := Lex("package x\n@")
	if len(tokens) == 0 {
		t.Fatal("expected tokens")
	}
	if len(diagnostics) == 0 {
		t.Fatal("expected diagnostics for illegal token")
	}
	if diagnostics[0].Line != 2 {
		t.Fatalf("expected line 2 diagnostic, got=%d", diagnostics[0].Line)
	}
}
