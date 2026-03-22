package compiler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/cache"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/messages"

	_ "modernc.org/sqlite"
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
		if len(result.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", result.CacheAnalysis)
		}
		if result.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonCacheMiss {
			t.Fatalf("unexpected invalidation reason: %s", result.CacheAnalysis[0].InvalidationReason)
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
		if len(result.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", result.CacheAnalysis)
		}
		if result.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonCacheMiss {
			t.Fatalf("expected first explain to miss cache, got=%s", result.CacheAnalysis[0].InvalidationReason)
		}
	})
}

func TestExplainReportsInputContentChangeAfterBuild(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	initialContent := `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    return vc(Artifact{Path: target})
}
`
	if err := os.WriteFile(entryPath, []byte(initialContent), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	updatedContent := `package build

type Artifact struct {
    Path string
}

task func Build(target string) Vc[Artifact] {
    return vc(Artifact{Path: target + "-updated"})
}
`

	withWorkingDirectory(t, workspace, func() {
		service := New()
		buildResult, err := service.Build(context.Background(), BuildOptions{Entry: "./main.ttl", OutDir: "./out"})
		if err != nil {
			t.Fatalf("build returned error: %v", err)
		}
		if len(buildResult.Diagnostics) > 0 {
			t.Fatalf("unexpected build diagnostics: %+v", buildResult.Diagnostics)
		}

		explainResult, err := service.Explain(context.Background(), ExplainOptions{Entry: "./main.ttl", Task: "Build"})
		if err != nil {
			t.Fatalf("explain returned error: %v", err)
		}
		if len(explainResult.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", explainResult.CacheAnalysis)
		}
		if explainResult.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonNone {
			t.Fatalf("expected explain after build to be cache hit, got=%s", explainResult.CacheAnalysis[0].InvalidationReason)
		}
		if !explainResult.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected cache hit after build, got=%+v", explainResult.CacheAnalysis[0])
		}

		if err := os.WriteFile(entryPath, []byte(updatedContent), 0o600); err != nil {
			t.Fatalf("write updated ttl file: %v", err)
		}

		changedExplainResult, err := service.Explain(context.Background(), ExplainOptions{Entry: "./main.ttl", Task: "Build"})
		if err != nil {
			t.Fatalf("explain with changed source returned error: %v", err)
		}
		if len(changedExplainResult.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", changedExplainResult.CacheAnalysis)
		}
		if changedExplainResult.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonInputContentChanged {
			t.Fatalf("expected input content change, got=%s", changedExplainResult.CacheAnalysis[0].InvalidationReason)
		}
		if changedExplainResult.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected cache miss when content changed, got=%+v", changedExplainResult.CacheAnalysis[0])
		}
	})
}

func TestBuildRecoversFromCacheCorruption(t *testing.T) {
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
		initialBuildResult, err := service.Build(context.Background(), BuildOptions{Entry: "./main.ttl", OutDir: "./out"})
		if err != nil {
			t.Fatalf("initial build returned error: %v", err)
		}
		if len(initialBuildResult.Diagnostics) > 0 {
			t.Fatalf("unexpected diagnostics for initial build: %+v", initialBuildResult.Diagnostics)
		}

		db, err := sql.Open("sqlite", initialBuildResult.CacheDBPath)
		if err != nil {
			t.Fatalf("open cache sqlite db: %v", err)
		}
		defer db.Close()

		if _, err := db.Exec(`UPDATE task_cache SET metadata = '{' WHERE module = 'build' AND task_id = 'Build'`); err != nil {
			t.Fatalf("corrupt metadata json: %v", err)
		}

		rebuildResult, err := service.Build(context.Background(), BuildOptions{Entry: "./main.ttl", OutDir: "./out"})
		if err != nil {
			t.Fatalf("rebuild returned error: %v", err)
		}
		if len(rebuildResult.Diagnostics) > 0 {
			t.Fatalf("unexpected diagnostics for rebuild: %+v", rebuildResult.Diagnostics)
		}
		if len(rebuildResult.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", rebuildResult.CacheAnalysis)
		}
		if rebuildResult.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonCacheCorruption {
			t.Fatalf("expected cache corruption invalidation reason, got=%s", rebuildResult.CacheAnalysis[0].InvalidationReason)
		}
	})
}

func TestExplainDoesNotFailWhenCacheOpenFails(t *testing.T) {
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

	originalOpenCacheStore := openCacheStore
	openCacheStore = func(_ string) (*cache.Store, error) {
		return nil, errors.New("cache unavailable")
	}
	t.Cleanup(func() {
		openCacheStore = originalOpenCacheStore
	})

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Explain(context.Background(), ExplainOptions{Entry: "./main.ttl", Task: "Build"})
		if err != nil {
			t.Fatalf("explain should not fail when cache open fails: %v", err)
		}
		if len(result.Tasks) != 1 {
			t.Fatalf("expected one explained task, got=%+v", result.Tasks)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("expected diagnostics to remain unchanged, got=%+v", result.Diagnostics)
		}
		if len(result.CacheAnalysis) != 0 {
			t.Fatalf("expected empty cache analysis when cache is unavailable, got=%+v", result.CacheAnalysis)
		}
	})
}

func TestRunCachesRootTask(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

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
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()

		firstRun, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"target": "web",
			},
		})
		if err != nil {
			t.Fatalf("first run returned error: %v", err)
		}
		if len(firstRun.Diagnostics) != 0 {
			t.Fatalf("expected no diagnostics for first run, got=%+v", firstRun.Diagnostics)
		}
		if len(firstRun.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", firstRun.CacheAnalysis)
		}
		if firstRun.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonCacheMiss {
			t.Fatalf("expected first run cache miss, got=%s", firstRun.CacheAnalysis[0].InvalidationReason)
		}
		if firstRun.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected first run cache miss state, got=%+v", firstRun.CacheAnalysis[0])
		}
		if len(firstRun.RunTrace) != 2 || firstRun.RunTrace[0] != "Build" || firstRun.RunTrace[1] != "ResolveSource" {
			t.Fatalf("unexpected first run trace: %+v", firstRun.RunTrace)
		}
		firstRunResultObject, ok := firstRun.RunResult.(map[string]any)
		if !ok {
			t.Fatalf("expected first run result object, got=%T", firstRun.RunResult)
		}
		if firstRunResultObject["Path"] != "web" {
			t.Fatalf("unexpected first run result path: %#v", firstRunResultObject["Path"])
		}

		secondRun, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"target": "web",
			},
		})
		if err != nil {
			t.Fatalf("second run returned error: %v", err)
		}
		if len(secondRun.Diagnostics) != 0 {
			t.Fatalf("expected no diagnostics for second run, got=%+v", secondRun.Diagnostics)
		}
		if len(secondRun.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", secondRun.CacheAnalysis)
		}
		if secondRun.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonNone {
			t.Fatalf("expected second run cache hit, got=%s", secondRun.CacheAnalysis[0].InvalidationReason)
		}
		if !secondRun.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected second run cache hit state, got=%+v", secondRun.CacheAnalysis[0])
		}
		if len(secondRun.RunTrace) != 2 || secondRun.RunTrace[0] != "Build" || secondRun.RunTrace[1] != "ResolveSource" {
			t.Fatalf("unexpected second run trace: %+v", secondRun.RunTrace)
		}
		secondRunResultObject, ok := secondRun.RunResult.(map[string]any)
		if !ok {
			t.Fatalf("expected second run result object, got=%T", secondRun.RunResult)
		}
		if secondRunResultObject["Path"] != "web" {
			t.Fatalf("unexpected second run result path: %#v", secondRunResultObject["Path"])
		}
	})
}

func TestRunCacheHitPreservesLargeIntegerJSONNumber(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Count int64
}

task func Build(count int64) Vc[Artifact] {
    return vc(Artifact{Count: count})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	expectCountNumber := func(value any) {
		t.Helper()
		resultObject, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expected run result object, got=%T", value)
		}

		rawCount, ok := resultObject["Count"]
		if !ok {
			t.Fatalf("missing Count field: %#v", resultObject)
		}
		preciseCount, ok := rawCount.(json.Number)
		if !ok {
			t.Fatalf("expected Count as json.Number, got=%T value=%#v", rawCount, rawCount)
		}
		if preciseCount.String() != "9007199254740993" {
			t.Fatalf("unexpected Count value: %s", preciseCount.String())
		}
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()

		firstRun, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"count": json.Number("9007199254740993"),
			},
		})
		if err != nil {
			t.Fatalf("first run returned error: %v", err)
		}
		if len(firstRun.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics for first run: %+v", firstRun.Diagnostics)
		}
		if len(firstRun.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", firstRun.CacheAnalysis)
		}
		if firstRun.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonCacheMiss {
			t.Fatalf("expected first run cache miss, got=%s", firstRun.CacheAnalysis[0].InvalidationReason)
		}
		expectCountNumber(firstRun.RunResult)

		secondRun, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"count": json.Number("9007199254740993"),
			},
		})
		if err != nil {
			t.Fatalf("second run returned error: %v", err)
		}
		if len(secondRun.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics for second run: %+v", secondRun.Diagnostics)
		}
		if len(secondRun.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", secondRun.CacheAnalysis)
		}
		if secondRun.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonNone {
			t.Fatalf("expected second run cache hit, got=%s", secondRun.CacheAnalysis[0].InvalidationReason)
		}
		if !secondRun.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected cache hit for second run, got=%+v", secondRun.CacheAnalysis[0])
		}
		expectCountNumber(secondRun.RunResult)
	})
}

func TestRunReportsTaskNotFound(t *testing.T) {
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
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "MissingTask",
			Args:  map[string]any{},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}
		foundTaskNotFound := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticTaskNotFound, "MissingTask") {
				foundTaskNotFound = true
				break
			}
		}
		if !foundTaskNotFound {
			t.Fatalf("expected task not found diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunReportsArgumentTypeMismatch(t *testing.T) {
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
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"target": 10,
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}
		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "target", "string") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunInvalidatesCacheWhenArgsChange(t *testing.T) {
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

		firstRun, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"target": "web",
			},
		})
		if err != nil {
			t.Fatalf("first run returned error: %v", err)
		}
		if len(firstRun.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics for first run: %+v", firstRun.Diagnostics)
		}
		if len(firstRun.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", firstRun.CacheAnalysis)
		}
		if firstRun.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonCacheMiss {
			t.Fatalf("expected first run cache miss, got=%s", firstRun.CacheAnalysis[0].InvalidationReason)
		}

		secondRun, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"target": "mobile",
			},
		})
		if err != nil {
			t.Fatalf("second run returned error: %v", err)
		}
		if len(secondRun.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics for second run: %+v", secondRun.Diagnostics)
		}
		if len(secondRun.CacheAnalysis) != 1 {
			t.Fatalf("expected one cache analysis row, got=%+v", secondRun.CacheAnalysis)
		}
		if secondRun.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonParameterChanged {
			t.Fatalf("expected parameter_changed on second run, got=%s", secondRun.CacheAnalysis[0].InvalidationReason)
		}
		if secondRun.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected cache miss on changed args, got=%+v", secondRun.CacheAnalysis[0])
		}

		resultObject, ok := secondRun.RunResult.(map[string]any)
		if !ok {
			t.Fatalf("expected second run result object, got=%T", secondRun.RunResult)
		}
		if resultObject["Path"] != "mobile" {
			t.Fatalf("expected updated run result for changed args, got=%#v", resultObject["Path"])
		}
	})
}

func TestRunRejectsFractionalIntegerArgument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Count int
}

task func Build(count int) Vc[Artifact] {
    return vc(Artifact{Count: count})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"count": json.Number("1.5"),
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "count", "int") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected integer type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunRejectsQuotedIntegerArgument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Count int
}

task func Build(count int) Vc[Artifact] {
    return vc(Artifact{Count: count})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"count": "1",
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "count", "int") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected integer type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunRejectsOutOfRangeBoundedIntegerArgument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Count int8
}

task func Build(count int8) Vc[Artifact] {
    return vc(Artifact{Count: count})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"count": json.Number("300"),
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "count", "int8") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected bounded integer type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunRejectsNonObjectForStructArgument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Path string
}

task func Build(input Artifact) Vc[Artifact] {
    return vc(Artifact{Path: input.Path})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"input": "invalid-scalar",
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "input", "Artifact") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected struct type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunRejectsInvalidNestedFieldForRecursiveStructArgument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Node struct {
    Name string
    Next Node
}

type Artifact struct {
    Path string
}

task func Build(input Node) Vc[Artifact] {
    return vc(Artifact{Path: input.Next.Name})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"input": map[string]any{
					"Name": "root",
					"Next": map[string]any{
						"Name": json.Number("10"),
						"Next": map[string]any{
							"Name": "leaf",
							"Next": map[string]any{},
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "input", "Node") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected recursive struct type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
		if len(result.RunTrace) != 0 {
			t.Fatalf("expected run trace to remain empty when validation fails, got=%+v", result.RunTrace)
		}
	})
}

func TestRunRejectsOutOfRangeFloat32Argument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Value float32
}

task func Build(value float32) Vc[Artifact] {
    return vc(Artifact{Value: value})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"value": json.Number("3.5e38"),
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "value", "float32") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected float32 type mismatch diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunRejectsNonRepresentableIntegerForFloat32Argument(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

type Artifact struct {
    Value float32
}

task func Build(value float32) Vc[Artifact] {
    return vc(Artifact{Value: value})
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"value": int64(math.MaxInt64),
			},
		})
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics")
		}

		foundTypeMismatch := false
		for _, issue := range result.Diagnostics {
			if issue.Message == messages.FormatDiagnostic(messages.DiagnosticInvalidRunArgumentType, "value", "float32") {
				foundTypeMismatch = true
				break
			}
		}
		if !foundTypeMismatch {
			t.Fatalf("expected float32 type mismatch diagnostic for non-representable integer, got=%+v", result.Diagnostics)
		}
	})
}

func TestRunCacheDoesNotMutateBuildOrExplainState(t *testing.T) {
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

		buildResult, err := service.Build(context.Background(), BuildOptions{Entry: "./main.ttl", OutDir: "./out"})
		if err != nil {
			t.Fatalf("build returned error: %v", err)
		}
		if len(buildResult.Diagnostics) != 0 {
			t.Fatalf("unexpected build diagnostics: %+v", buildResult.Diagnostics)
		}

		runResult, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args: map[string]any{
				"target": "web",
			},
		})
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
		if len(runResult.Diagnostics) != 0 {
			t.Fatalf("unexpected run diagnostics: %+v", runResult.Diagnostics)
		}

		explainResult, err := service.Explain(context.Background(), ExplainOptions{Entry: "./main.ttl", Task: "Build"})
		if err != nil {
			t.Fatalf("explain returned error: %v", err)
		}
		if len(explainResult.Diagnostics) != 0 {
			t.Fatalf("unexpected explain diagnostics: %+v", explainResult.Diagnostics)
		}
		if len(explainResult.CacheAnalysis) != 1 {
			t.Fatalf("expected one explain cache row, got=%+v", explainResult.CacheAnalysis)
		}
		if explainResult.CacheAnalysis[0].InvalidationReason != contracts.TtlInvalidationReasonNone {
			t.Fatalf("expected explain cache state to remain stable, got=%s", explainResult.CacheAnalysis[0].InvalidationReason)
		}
		if !explainResult.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected explain cache hit, got=%+v", explainResult.CacheAnalysis[0])
		}
	})
}

func TestImportResolvesAndMergesDeclarations(t *testing.T) {
	workspace := t.TempDir()
	libContent := `package lib

type Metadata struct {
    Version string
}

task func GetMetadata() Vc[Metadata] {
    return vc(Metadata{Version: "1.0"})
}
`
	mainContent := `package main

import "./lib.ttl"

type Result struct {
    Name string
    Version string
}

task func Build(name string) Vc[Result] {
    meta := read(GetMetadata())
    return vc(Result{Name: name, Version: meta.Version})
}
`
	if err := os.WriteFile(filepath.Join(workspace, "lib.ttl"), []byte(libContent), 0o600); err != nil {
		t.Fatalf("write lib.ttl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.ttl"), []byte(mainContent), 0o600); err != nil {
		t.Fatalf("write main.ttl: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args:  map[string]any{"name": "myapp"},
		})
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("expected no diagnostics, got=%+v", result.Diagnostics)
		}
		resultObj, ok := result.RunResult.(map[string]any)
		if !ok {
			t.Fatalf("expected object result, got=%T", result.RunResult)
		}
		if resultObj["Name"] != "myapp" {
			t.Fatalf("unexpected Name: %v", resultObj["Name"])
		}
		if resultObj["Version"] != "1.0" {
			t.Fatalf("unexpected Version: %v", resultObj["Version"])
		}
	})
}

func TestImportDetectsCycle(t *testing.T) {
	workspace := t.TempDir()
	content := `package cycle

import "./main.ttl"

task func Loop() Vc[string] {
    return vc("unreachable")
}
`
	if err := os.WriteFile(filepath.Join(workspace, "main.ttl"), []byte(content), 0o600); err != nil {
		t.Fatalf("write main.ttl: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Check(context.Background(), CheckOptions{Entry: "./main.ttl"})
		if err != nil {
			t.Fatalf("check returned error: %v", err)
		}
		foundCycleDiag := false
		for _, d := range result.Diagnostics {
			if d.Kind == contracts.DiagnosticKindImportCycle {
				foundCycleDiag = true
				break
			}
		}
		if !foundCycleDiag {
			t.Fatalf("expected import_cycle diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestImportNotFoundReportsDiagnostic(t *testing.T) {
	workspace := t.TempDir()
	content := `package main

import "./nonexistent.ttl"

task func Build() Vc[string] {
    return vc("ok")
}
`
	if err := os.WriteFile(filepath.Join(workspace, "main.ttl"), []byte(content), 0o600); err != nil {
		t.Fatalf("write main.ttl: %v", err)
	}

	withWorkingDirectory(t, workspace, func() {
		service := New()
		result, err := service.Check(context.Background(), CheckOptions{Entry: "./main.ttl"})
		if err != nil {
			t.Fatalf("check returned error: %v", err)
		}
		foundNotFoundDiag := false
		for _, d := range result.Diagnostics {
			if d.Kind == contracts.DiagnosticKindImportNotFound {
				foundNotFoundDiag = true
				break
			}
		}
		if !foundNotFoundDiag {
			t.Fatalf("expected import_not_found diagnostic, got=%+v", result.Diagnostics)
		}
	})
}

func TestServiceLogsIncludeConsistentTraceIDPerExecution(t *testing.T) {
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
		testCases := []struct {
			name string
			run  func(service *Service) error
		}{
			{
				name: "check",
				run: func(service *Service) error {
					_, err := service.Check(context.Background(), CheckOptions{Entry: "./main.ttl"})
					return err
				},
			},
			{
				name: "build",
				run: func(service *Service) error {
					_, err := service.Build(context.Background(), BuildOptions{Entry: "./main.ttl", OutDir: "./out"})
					return err
				},
			},
			{
				name: "explain",
				run: func(service *Service) error {
					_, err := service.Explain(context.Background(), ExplainOptions{Entry: "./main.ttl", Task: "Build"})
					return err
				},
			},
			{
				name: "run",
				run: func(service *Service) error {
					_, err := service.Run(context.Background(), RunOptions{
						Entry: "./main.ttl",
						Task:  "Build",
						Args:  map[string]any{"target": "web"},
					})
					return err
				},
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				logBuffer := &bytes.Buffer{}
				logger := slog.New(slog.NewJSONHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
				service := NewWithLogger(logger)

				if err := testCase.run(service); err != nil {
					t.Fatalf("%s returned error: %v", testCase.name, err)
				}

				entries := decodeJSONLogEntries(t, logBuffer.Bytes())
				if len(entries) == 0 {
					t.Fatalf("expected log entries for %s", testCase.name)
				}

				traceID := ""
				for _, entry := range entries {
					currentTraceID := jsonFieldString(entry, "trace_id")
					if strings.TrimSpace(currentTraceID) == "" {
						t.Fatalf("expected non-empty trace_id for %s log entry: %#v", testCase.name, entry)
					}
					if traceID == "" {
						traceID = currentTraceID
						continue
					}
					if currentTraceID != traceID {
						t.Fatalf("expected single trace_id per execution for %s, got=%s and %s", testCase.name, traceID, currentTraceID)
					}
				}
			})
		}
	})
}

func TestServiceLogsDeterministicDiagnosticIDAcrossRuns(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

import "./missing.ttl"

task func Build() Vc[string] {
    return vc("ok")
}
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	runCheck := func() ([]string, error) {
		logBuffer := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
		service := NewWithLogger(logger)
		_, err := service.Check(context.Background(), CheckOptions{Entry: "./main.ttl"})
		if err != nil {
			return nil, err
		}

		entries := decodeJSONLogEntries(t, logBuffer.Bytes())
		ids := collectDiagnosticIDs(entries)
		sort.Strings(ids)
		return ids, nil
	}

	withWorkingDirectory(t, workspace, func() {
		firstIDs, err := runCheck()
		if err != nil {
			t.Fatalf("first check returned error: %v", err)
		}
		if len(firstIDs) == 0 {
			t.Fatal("expected diagnostic ids in first run logs")
		}

		secondIDs, err := runCheck()
		if err != nil {
			t.Fatalf("second check returned error: %v", err)
		}
		if len(secondIDs) == 0 {
			t.Fatal("expected diagnostic ids in second run logs")
		}
		if !slicesEqual(firstIDs, secondIDs) {
			t.Fatalf("expected deterministic diagnostic ids, first=%v second=%v", firstIDs, secondIDs)
		}
	})
}

func TestServiceRunLogsDeterministicExecutionTraceIDAcrossCacheMissAndHit(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	content := `package build

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
`
	if err := os.WriteFile(entryPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write ttl file: %v", err)
	}

	runWithLogs := func() (Result, []map[string]any, error) {
		logBuffer := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
		service := NewWithLogger(logger)
		result, err := service.Run(context.Background(), RunOptions{
			Entry: "./main.ttl",
			Task:  "Build",
			Args:  map[string]any{"target": "web"},
		})
		entries := decodeJSONLogEntries(t, logBuffer.Bytes())
		return result, entries, err
	}

	withWorkingDirectory(t, workspace, func() {
		firstResult, firstEntries, err := runWithLogs()
		if err != nil {
			t.Fatalf("first run returned error: %v", err)
		}
		if len(firstResult.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics on first run: %+v", firstResult.Diagnostics)
		}
		if len(firstResult.CacheAnalysis) != 1 || firstResult.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected first run cache miss, got=%+v", firstResult.CacheAnalysis)
		}

		secondResult, secondEntries, err := runWithLogs()
		if err != nil {
			t.Fatalf("second run returned error: %v", err)
		}
		if len(secondResult.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics on second run: %+v", secondResult.Diagnostics)
		}
		if len(secondResult.CacheAnalysis) != 1 || !secondResult.CacheAnalysis[0].CacheHit {
			t.Fatalf("expected second run cache hit, got=%+v", secondResult.CacheAnalysis)
		}

		firstExecutionTraceID := singleCacheEventExecutionTraceID(t, firstEntries)
		secondExecutionTraceID := singleCacheEventExecutionTraceID(t, secondEntries)
		if firstExecutionTraceID != secondExecutionTraceID {
			t.Fatalf("expected deterministic execution_trace_id across miss/hit, first=%s second=%s", firstExecutionTraceID, secondExecutionTraceID)
		}
	})
}

func decodeJSONLogEntries(t *testing.T, payload []byte) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		entry := map[string]any{}
		if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
			t.Fatalf("unmarshal log entry: %v payload=%s", err, trimmed)
		}
		entries = append(entries, entry)
	}
	return entries
}

func jsonFieldString(entry map[string]any, key string) string {
	rawValue, exists := entry[key]
	if !exists {
		return ""
	}
	stringValue, ok := rawValue.(string)
	if !ok {
		return ""
	}
	return stringValue
}

func collectDiagnosticIDs(entries []map[string]any) []string {
	ids := make([]string, 0)
	for _, entry := range entries {
		if jsonFieldString(entry, "event") != "diagnostic_reported" {
			continue
		}
		diagnosticID := jsonFieldString(entry, "diagnostic_id")
		if strings.TrimSpace(diagnosticID) == "" {
			continue
		}
		ids = append(ids, diagnosticID)
	}
	return ids
}

func singleCacheEventExecutionTraceID(t *testing.T, entries []map[string]any) string {
	t.Helper()
	ids := make([]string, 0)
	for _, entry := range entries {
		if jsonFieldString(entry, "event") != "task_cache_processed" {
			continue
		}
		executionTraceID := jsonFieldString(entry, "execution_trace_id")
		if strings.TrimSpace(executionTraceID) == "" {
			continue
		}
		ids = append(ids, executionTraceID)
	}
	if len(ids) != 1 {
		t.Fatalf("expected exactly one non-empty execution_trace_id in task_cache_processed logs, got=%v", ids)
	}
	return ids[0]
}

func slicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
