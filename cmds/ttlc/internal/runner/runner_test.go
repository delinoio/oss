package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
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

func TestExecuteSupportsPrintBuiltin(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    print(target)
    return vc(Artifact{Path: target})
}
`)

	program, err := BuildProgram(module, "Build", map[string]any{"target": "mobile"})
	if err != nil {
		t.Fatalf("build program: %v", err)
	}

	source, err := GenerateGoSource(program)
	if err != nil {
		t.Fatalf("generate source: %v", err)
	}

	result, err := Execute(context.Background(), t.TempDir(), source)
	if err != nil {
		t.Fatalf("execute runner with print builtin: %v", err)
	}

	resultObject, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got=%T", result.Result)
	}
	if resultObject["Path"] != "mobile" {
		t.Fatalf("unexpected run result payload: %#v", resultObject["Path"])
	}
}

func TestExecuteForwardsPrintBuiltinOutputToStderr(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    print("trace:", target)
    return vc(Artifact{Path: target})
}
`)

	program, err := BuildProgram(module, "Build", map[string]any{"target": "mobile"})
	if err != nil {
		t.Fatalf("build program: %v", err)
	}

	source, err := GenerateGoSource(program)
	if err != nil {
		t.Fatalf("generate source: %v", err)
	}

	originalStderr := os.Stderr
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stderr = writePipe
	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = writePipe.Close()
		_ = readPipe.Close()
	})

	result, err := Execute(context.Background(), t.TempDir(), source)
	if err != nil {
		t.Fatalf("execute runner with print builtin: %v", err)
	}

	if err := writePipe.Close(); err != nil {
		t.Fatalf("close stderr write pipe: %v", err)
	}
	os.Stderr = originalStderr

	stderrPayload, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stderr payload: %v", err)
	}

	if !bytes.Contains(stderrPayload, []byte("trace: mobile")) {
		t.Fatalf("expected print output on stderr, got=%q", string(stderrPayload))
	}

	resultObject, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got=%T", result.Result)
	}
	if resultObject["Path"] != "mobile" {
		t.Fatalf("unexpected run result payload: %#v", resultObject["Path"])
	}
}

func TestExecutePrefersTaskCallOverBuiltinName(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
}

task func hash(input string) Vc[Artifact] {
    return vc(Artifact{Path: input})
}

task func Build(target string) Vc[Artifact] {
    return hash(target)
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
		t.Fatalf("expected task call trace, got=%+v", result.ExecutedTasks)
	}
	if result.ExecutedTasks[0] != "Build" || result.ExecutedTasks[1] != "hash" {
		t.Fatalf("expected Build -> hash trace, got=%+v", result.ExecutedTasks)
	}

	resultObject, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected task result object, got=%T value=%#v", result.Result, result.Result)
	}
	if resultObject["Path"] != "web" {
		t.Fatalf("unexpected task result path: %#v", resultObject["Path"])
	}
}

func TestExecuteHashBuiltinDisambiguatesArgumentBoundaries(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Digest string
}

task func Build(left string, right string) Vc[Artifact] {
    return vc(Artifact{Digest: hash(left, right)})
}
`)

	runDigest := func(left string, right string) string {
		t.Helper()

		program, err := BuildProgram(module, "Build", map[string]any{
			"left":  left,
			"right": right,
		})
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

		resultObject, ok := result.Result.(map[string]any)
		if !ok {
			t.Fatalf("expected object result, got=%T", result.Result)
		}
		digest, ok := resultObject["Digest"].(string)
		if !ok {
			t.Fatalf("expected Digest string, got=%T", resultObject["Digest"])
		}
		return digest
	}

	leftDigest := runDigest("a|b", "c")
	rightDigest := runDigest("a", "b|c")
	if leftDigest == rightDigest {
		t.Fatalf("expected distinct hash outputs for different argument boundaries, digest=%s", leftDigest)
	}
}

func TestExecuteHashBuiltinCanonicalizesMapValues(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Path string
    Digest string
}

task func Build(input Artifact) Vc[Artifact] {
    return vc(Artifact{
        Path: input.Path,
        Digest: hash(input),
    })
}
`)

	runDigest := func(input map[string]any) string {
		t.Helper()

		program, err := BuildProgram(module, "Build", map[string]any{
			"input": input,
		})
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

		resultObject, ok := result.Result.(map[string]any)
		if !ok {
			t.Fatalf("expected object result, got=%T", result.Result)
		}
		digest, ok := resultObject["Digest"].(string)
		if !ok {
			t.Fatalf("expected Digest string, got=%T", resultObject["Digest"])
		}
		return digest
	}

	firstDigest := runDigest(map[string]any{
		"Path":   "c",
		"Digest": "a,Path=b",
	})
	secondDigest := runDigest(map[string]any{
		"Path":   "b,Path=c",
		"Digest": "a",
	})
	if firstDigest == secondDigest {
		t.Fatalf("expected distinct hash outputs for distinct map values, digest=%s", firstDigest)
	}
}

func TestExecutePreservesLargeIntegerJSONNumber(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Count int64
}

task func Build(count int64) Vc[Artifact] {
    return vc(Artifact{Count: count})
}
`)

	program, err := BuildProgram(module, "Build", map[string]any{
		"count": json.Number("9007199254740993"),
	})
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

	resultObject, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got=%T", result.Result)
	}
	countValue, ok := resultObject["Count"]
	if !ok {
		t.Fatalf("missing Count field: %#v", resultObject)
	}
	preciseNumber, ok := countValue.(json.Number)
	if !ok {
		t.Fatalf("expected Count to decode as json.Number, got=%T value=%#v", countValue, countValue)
	}
	if preciseNumber.String() != "9007199254740993" {
		t.Fatalf("unexpected Count number value: %s", preciseNumber.String())
	}
}

func TestExecutePreservesLargeIntegerLiteralPrecision(t *testing.T) {
	module := parseModuleForTest(t, `package build

type Artifact struct {
    Count int64
}

task func Build() Vc[Artifact] {
    return vc(Artifact{Count: 9007199254740993})
}
`)

	program, err := BuildProgram(module, "Build", map[string]any{})
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

	resultObject, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got=%T", result.Result)
	}
	countValue, ok := resultObject["Count"]
	if !ok {
		t.Fatalf("missing Count field: %#v", resultObject)
	}
	preciseNumber, ok := countValue.(json.Number)
	if !ok {
		t.Fatalf("expected Count to decode as json.Number, got=%T value=%#v", countValue, countValue)
	}
	if preciseNumber.String() != "9007199254740993" {
		t.Fatalf("unexpected Count number value: %s", preciseNumber.String())
	}
}

func TestExecuteWithFuncHelper(t *testing.T) {
	module := parseModuleForTest(t, `package helpers

type Result struct {
    Value string
    Label string
}

func makeLabel(prefix string) string {
    return prefix
}

task func Process(input string) Vc[Result] {
    label := makeLabel("processed")
    return vc(Result{Value: input, Label: label})
}
`)

	program, err := BuildProgram(module, "Process", map[string]any{"input": "test"})
	if err != nil {
		t.Fatalf("build program: %v", err)
	}
	if len(program.Funcs) != 1 {
		t.Fatalf("expected 1 func, got=%d", len(program.Funcs))
	}

	source, err := GenerateGoSource(program)
	if err != nil {
		t.Fatalf("generate source: %v", err)
	}

	result, err := Execute(context.Background(), t.TempDir(), source)
	if err != nil {
		t.Fatalf("execute runner: %v", err)
	}

	resultObj, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got=%T", result.Result)
	}
	if resultObj["Value"] != "test" {
		t.Fatalf("unexpected Value: %v", resultObj["Value"])
	}
	if resultObj["Label"] != "processed" {
		t.Fatalf("unexpected Label: %v", resultObj["Label"])
	}

	// func calls should NOT appear in executed_tasks
	for _, taskName := range result.ExecutedTasks {
		if taskName == "makeLabel" {
			t.Fatal("func makeLabel should not appear in executed_tasks")
		}
	}
	found := false
	for _, taskName := range result.ExecutedTasks {
		if taskName == "Process" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Process in executed_tasks, got=%+v", result.ExecutedTasks)
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
