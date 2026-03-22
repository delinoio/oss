package sema

import (
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
	"github.com/delinoio/oss/cmds/ttlc/internal/messages"
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
		if diagnostic.Message == messages.FormatDiagnostic(messages.DiagnosticReadRequiresOneArgument) {
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

func TestCheckCollectsTypeDeclarations(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
    Digest string
}

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}
`)
	result := Check(module)
	if len(result.Types) != 1 {
		t.Fatalf("expected one type declaration, got=%d", len(result.Types))
	}
	if result.Types[0].Name != "Artifact" {
		t.Fatalf("unexpected type name: %s", result.Types[0].Name)
	}
	if len(result.Types[0].Fields) != 2 {
		t.Fatalf("unexpected field count: %d", len(result.Types[0].Fields))
	}
}

func TestCheckRejectsDuplicateTaskDeclarations(t *testing.T) {
	module := parseModuleForTest(t, `package build

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}
`)
	result := Check(module)
	foundDuplicateDiagnostic := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticDuplicateTaskDeclaration, "Build") {
			foundDuplicateDiagnostic = true
			break
		}
	}
	if !foundDuplicateDiagnostic {
		t.Fatalf("expected duplicate task diagnostic, got=%+v", result.Diagnostics)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected duplicate task to be emitted once, got=%d", len(result.Tasks))
	}
}

func TestCheckRejectsDuplicateTypeDeclarations(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
}

type Artifact struct {
    Digest string
}

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}
`)
	result := Check(module)
	foundDuplicateDiagnostic := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticDuplicateTypeDeclaration, "Artifact") {
			foundDuplicateDiagnostic = true
			break
		}
	}
	if !foundDuplicateDiagnostic {
		t.Fatalf("expected duplicate type diagnostic, got=%+v", result.Diagnostics)
	}
	if len(result.Types) != 1 {
		t.Fatalf("expected duplicate type to be emitted once, got=%d", len(result.Types))
	}
}

func TestCheckDoesNotInferDependencyFromSelectorCallee(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
}

type Helper struct {
    Path string
}

task func Build() Vc[Artifact] {
    helper := Helper{Path: "x"}
    val := read(helper.Resolve())
    return vc(val)
}

task func Resolve() Vc[Artifact] {
    return vc(Artifact{Path: "task"})
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
	if len(buildDeps) != 0 {
		t.Fatalf("expected no inferred dependency for selector callee, got=%+v", buildDeps)
	}
}

func TestCheckRejectsDuplicateStructFields(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
    Path string
}

task func Build() Vc[Artifact] {
    return vc(Artifact{})
}
`)
	result := Check(module)
	foundDuplicateFieldDiagnostic := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticDuplicateStructFieldDeclaration, "Artifact", "Path") {
			foundDuplicateFieldDiagnostic = true
			break
		}
	}
	if !foundDuplicateFieldDiagnostic {
		t.Fatalf("expected duplicate struct field diagnostic, got=%+v", result.Diagnostics)
	}
	if len(result.Types) != 1 {
		t.Fatalf("expected one type declaration, got=%d", len(result.Types))
	}
	if len(result.Types[0].Fields) != 1 {
		t.Fatalf("expected duplicate field to be emitted once, got=%d", len(result.Types[0].Fields))
	}
}

func TestCheckRejectsDuplicateTaskParameters(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
}

task func Build(target string, target string) Vc[Artifact] {
    return vc(Artifact{Path: target})
}
`)
	result := Check(module)
	foundDuplicateParameterDiagnostic := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticDuplicateTaskParameterName, "Build", "target") {
			foundDuplicateParameterDiagnostic = true
			break
		}
	}
	if !foundDuplicateParameterDiagnostic {
		t.Fatalf("expected duplicate task parameter diagnostic, got=%+v", result.Diagnostics)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected one task declaration, got=%d", len(result.Tasks))
	}
	if len(result.Tasks[0].Params) != 1 {
		t.Fatalf("expected duplicate parameter to be emitted once, got=%d", len(result.Tasks[0].Params))
	}
}

func TestCheckValidFuncDeclaration(t *testing.T) {
	module := parseModuleForTest(t, `package build

func helper(x string) string {
    return x
}

task func Build(target string) Vc[string] {
    val := helper(target)
    return vc(val)
}
`)
	result := Check(module)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got=%+v", result.Diagnostics)
	}
	if len(result.Funcs) != 1 {
		t.Fatalf("expected 1 func, got=%d", len(result.Funcs))
	}
	if result.Funcs[0].ID != "helper" {
		t.Fatalf("unexpected func id: %s", result.Funcs[0].ID)
	}
	if result.Funcs[0].ReturnType != "string" {
		t.Fatalf("unexpected func return type: %s", result.Funcs[0].ReturnType)
	}
}

func TestCheckRejectsDuplicateFuncDeclarations(t *testing.T) {
	module := parseModuleForTest(t, `package build

func helper() string {
    return "a"
}

func helper() string {
    return "b"
}

task func Build() Vc[string] {
    return vc(helper())
}
`)
	result := Check(module)
	found := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticDuplicateFunctionDeclaration, "helper") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate func diagnostic, got=%+v", result.Diagnostics)
	}
}

func TestCheckRejectsFuncTaskNameCollision(t *testing.T) {
	module := parseModuleForTest(t, `package build

task func Build() Vc[string] {
    return vc("done")
}

func Build() string {
    return "conflict"
}
`)
	result := Check(module)
	found := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticFunctionTaskNameCollision, "Build", "Build") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected name collision diagnostic, got=%+v", result.Diagnostics)
	}
}

func TestCheckFuncDoesNotRequireVcReturn(t *testing.T) {
	module := parseModuleForTest(t, `package build

func helper() string {
    return "plain"
}

task func Build() Vc[string] {
    return vc(helper())
}
`)
	result := Check(module)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics for func with non-Vc return, got=%+v", result.Diagnostics)
	}
}

func TestCheckRejectsDuplicateFuncParameters(t *testing.T) {
	module := parseModuleForTest(t, `package build

func helper(x string, x string) string {
    return x
}

task func Build() Vc[string] {
    return vc(helper("a", "b"))
}
`)
	result := Check(module)
	found := false
	for _, issue := range result.Diagnostics {
		if issue.Message == messages.FormatDiagnostic(messages.DiagnosticDuplicateFunctionParameterName, "helper", "x") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate func parameter diagnostic, got=%+v", result.Diagnostics)
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
