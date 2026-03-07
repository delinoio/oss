package compiler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/cache"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"

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
			if issue.Message == "task not found: MissingTask" {
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
			if issue.Message == "invalid run argument type: target expects string" {
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
