package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/cache"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/emitter"
	"github.com/delinoio/oss/cmds/ttlc/internal/fingerprint"
	"github.com/delinoio/oss/cmds/ttlc/internal/graph"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
	"github.com/delinoio/oss/cmds/ttlc/internal/logging"
	"github.com/delinoio/oss/cmds/ttlc/internal/parser"
	"github.com/delinoio/oss/cmds/ttlc/internal/runner"
	"github.com/delinoio/oss/cmds/ttlc/internal/sema"
	"github.com/delinoio/oss/cmds/ttlc/internal/source"
)

type CheckOptions struct {
	Entry string
}

type BuildOptions struct {
	Entry  string
	OutDir string
}

type ExplainOptions struct {
	Entry string
	Task  string
}

type RunOptions struct {
	Entry string
	Task  string
	Args  map[string]any
}

type Task struct {
	ID         string           `json:"id"`
	Params     []sema.TaskParam `json:"params"`
	ReturnType string           `json:"return_type"`
	Deps       []string         `json:"deps"`
	CacheKey   string           `json:"cache_key"`
}

type CacheAnalysis struct {
	TaskID             string                          `json:"task_id"`
	CacheKey           string                          `json:"cache_key"`
	CacheHit           bool                            `json:"cache_hit"`
	InvalidationReason contracts.TtlInvalidationReason `json:"invalidation_reason"`
}

type Result struct {
	Entry                 string                  `json:"entry"`
	Module                string                  `json:"module"`
	Task                  string                  `json:"task,omitempty"`
	Args                  map[string]any          `json:"args,omitempty"`
	Tasks                 []Task                  `json:"tasks,omitempty"`
	RunResult             any                     `json:"result,omitempty"`
	RunTrace              []string                `json:"run_trace,omitempty"`
	Diagnostics           []diagnostic.Diagnostic `json:"diagnostics"`
	FingerprintComponents fingerprint.Components  `json:"fingerprint_components"`
	GeneratedFiles        []string                `json:"generated_files,omitempty"`
	CacheDBPath           string                  `json:"cache_db_path,omitempty"`
	CacheAnalysis         []CacheAnalysis         `json:"cache_analysis,omitempty"`
}

type Service struct {
	logger *slog.Logger
}

type taskFingerprint struct {
	Task       sema.Task
	Components fingerprint.Components
	CacheKey   string
}

type analysis struct {
	paths            source.Paths
	module           *ast.Module
	moduleName       string
	typeDeclarations []sema.TypeDecl
	diagnostics      []diagnostic.Diagnostic
	taskFingerprints []taskFingerprint
	sourceBytes      []byte
	result           Result
}

var openCacheStore = cache.Open

func New() *Service {
	handler := slog.NewJSONHandler(io.Discard, nil)
	return &Service{logger: slog.New(handler)}
}

func NewWithLogger(logger *slog.Logger) *Service {
	if logger == nil {
		return New()
	}
	return &Service{logger: logger}
}

func (s *Service) Check(ctx context.Context, options CheckOptions) (Result, error) {
	analysisResult, err := s.analyze(ctx, options.Entry, ".ttl/gen", "")
	if err != nil {
		return Result{}, err
	}
	return analysisResult.result, nil
}

func (s *Service) Explain(ctx context.Context, options ExplainOptions) (Result, error) {
	analysisResult, err := s.analyze(ctx, options.Entry, ".ttl/gen", options.Task)
	if err != nil {
		return Result{}, err
	}
	result := analysisResult.result
	if len(result.Tasks) == 0 {
		return result, nil
	}

	cacheStart := time.Now()
	s.logStageStart(contracts.CompileStageCache)
	store, err := openCacheStore(analysisResult.paths.CacheDBPath)
	if err != nil {
		s.logStageFailure(contracts.CompileStageCache, time.Since(cacheStart), contracts.DiagnosticKindIOError, err)
		result.CacheAnalysis = make([]CacheAnalysis, 0)
		return result, nil
	}
	defer store.Close()

	fingerprintByTaskID := make(map[string]taskFingerprint, len(analysisResult.taskFingerprints))
	for _, taskFingerprint := range analysisResult.taskFingerprints {
		fingerprintByTaskID[taskFingerprint.Task.ID] = taskFingerprint
	}

	analysisRecords := make([]CacheAnalysis, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		taskStart := time.Now()
		taskFingerprint, ok := fingerprintByTaskID[task.ID]
		if !ok {
			continue
		}

		taskAnalysis, errorKind, lookupErr := s.analyzeTaskCacheState(store, analysisResult.moduleName, taskFingerprint, false)
		if lookupErr != nil {
			s.logTaskCacheEvent(task.ID, taskFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(taskStart))
			analysisRecords = append(analysisRecords, CacheAnalysis{
				TaskID:             taskFingerprint.Task.ID,
				CacheKey:           taskFingerprint.CacheKey,
				CacheHit:           false,
				InvalidationReason: contracts.TtlInvalidationReasonCacheMiss,
			})
			continue
		}
		analysisRecords = append(analysisRecords, taskAnalysis)
		s.logTaskCacheEvent(taskAnalysis.TaskID, taskAnalysis.CacheKey, taskAnalysis.CacheHit, taskAnalysis.InvalidationReason, errorKind, time.Since(taskStart))
	}

	result.CacheAnalysis = analysisRecords
	s.logStageEnd(contracts.CompileStageCache, time.Since(cacheStart))
	return result, nil
}

func (s *Service) Run(ctx context.Context, options RunOptions) (Result, error) {
	analysisResult, err := s.analyze(ctx, options.Entry, ".ttl/gen", options.Task)
	if err != nil {
		return Result{}, err
	}

	result := analysisResult.result
	result.Task = options.Task
	result.Args = cloneArgs(options.Args)

	if strings.TrimSpace(options.Task) == "" {
		result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
			Kind:    contracts.DiagnosticKindTypeError,
			Message: "--task is required for run command",
			Line:    1,
			Column:  1,
		})
		return result, nil
	}
	if hasErrorDiagnostics(result.Diagnostics) {
		return result, nil
	}
	if len(result.Tasks) != 1 {
		return result, nil
	}

	rootTask := result.Tasks[0]
	result.Diagnostics = append(result.Diagnostics, validateRunArgs(rootTask.Params, options.Args)...)
	if hasErrorDiagnostics(result.Diagnostics) {
		return result, nil
	}

	rootFingerprint, found := findTaskFingerprintByID(analysisResult.taskFingerprints, rootTask.ID)
	if !found {
		return Result{}, fmt.Errorf("resolve root task fingerprint: %s", rootTask.ID)
	}
	runParameterHash, err := buildRunParameterHash(rootFingerprint.Task, options.Args)
	if err != nil {
		return Result{}, fmt.Errorf("build run parameter hash: %w", err)
	}
	rootFingerprint.Components.ParameterHash = runParameterHash
	rootFingerprint.CacheKey = fingerprint.CacheKey(rootFingerprint.Components)
	result.FingerprintComponents = rootFingerprint.Components

	cacheStart := time.Now()
	s.logStageStart(contracts.CompileStageCache)
	store, err := openCacheStore(analysisResult.paths.CacheDBPath)
	if err != nil {
		s.logStageFailure(contracts.CompileStageCache, time.Since(cacheStart), contracts.DiagnosticKindIOError, err)
		return Result{}, fmt.Errorf("open cache store: %w", err)
	}
	defer store.Close()

	cacheAnalysis, errorKind, lookupErr := s.analyzeTaskCacheState(store, analysisResult.moduleName, rootFingerprint, true)
	if lookupErr != nil {
		s.logTaskCacheEvent(rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(cacheStart))
		return Result{}, fmt.Errorf("analyze task cache state for %s: %w", rootFingerprint.Task.ID, lookupErr)
	}

	if cacheAnalysis.CacheHit {
		cachedState, stateFound, stateErr := store.GetTaskState(analysisResult.moduleName, rootFingerprint.Task.ID)
		if stateErr != nil {
			s.logTaskCacheEvent(rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(cacheStart))
			return Result{}, fmt.Errorf("read cache state for %s: %w", rootFingerprint.Task.ID, stateErr)
		}
		if stateFound {
			cachedResult, cachedRunTrace, ok := decodeRunMetadata(cachedState.Metadata)
			if ok {
				result.RunResult = cachedResult
				result.RunTrace = cachedRunTrace
				result.CacheDBPath = analysisResult.paths.CacheDBPath
				result.CacheAnalysis = []CacheAnalysis{cacheAnalysis}
				s.logTaskCacheEvent(rootFingerprint.Task.ID, rootFingerprint.CacheKey, true, cacheAnalysis.InvalidationReason, errorKind, time.Since(cacheStart))
				s.logStageEnd(contracts.CompileStageCache, time.Since(cacheStart))
				return result, nil
			}
		}
		cacheAnalysis.CacheHit = false
		cacheAnalysis.InvalidationReason = contracts.TtlInvalidationReasonCacheMiss
	}

	runStart := time.Now()
	s.logStageStart(contracts.CompileStageRun)
	runProgram, err := runner.BuildProgram(analysisResult.module, rootTask.ID, options.Args)
	if err != nil {
		s.logStageFailure(contracts.CompileStageRun, time.Since(runStart), contracts.DiagnosticKindTypeError, err)
		return Result{}, fmt.Errorf("build run program: %w", err)
	}
	runnerSource, err := runner.GenerateGoSource(runProgram)
	if err != nil {
		s.logStageFailure(contracts.CompileStageRun, time.Since(runStart), contracts.DiagnosticKindIOError, err)
		return Result{}, fmt.Errorf("generate runner source: %w", err)
	}
	runExecutionResult, err := runner.Execute(ctx, analysisResult.paths.OutDir, runnerSource)
	if err != nil {
		s.logStageFailure(contracts.CompileStageRun, time.Since(runStart), contracts.DiagnosticKindIOError, err)
		return Result{}, fmt.Errorf("execute generated runner: %w", err)
	}
	s.logStageEnd(contracts.CompileStageRun, time.Since(runStart))

	record := cache.TaskRecord{
		TaskKey:                 rootFingerprint.CacheKey,
		Module:                  analysisResult.moduleName,
		TaskID:                  rootTask.ID,
		InputContentHash:        rootFingerprint.Components.InputContentHash,
		ParameterHash:           rootFingerprint.Components.ParameterHash,
		EnvironmentSnapshotHash: rootFingerprint.Components.EnvironmentSnapshotHash,
		InputFingerprint:        composeInputFingerprint(rootFingerprint.Components),
		OutputBlobRef:           "",
		Deps:                    append([]string{}, rootTask.Deps...),
		Metadata: map[string]any{
			"module":      analysisResult.moduleName,
			"task_id":     rootTask.ID,
			"return_type": rootTask.ReturnType,
			"run_result":  runExecutionResult.Result,
			"run_trace":   runExecutionResult.ExecutedTasks,
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertTask(record); err != nil {
		s.logTaskCacheEvent(rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(cacheStart))
		return Result{}, fmt.Errorf("upsert run cache row for %s: %w", rootTask.ID, err)
	}

	s.logTaskCacheEvent(rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, cacheAnalysis.InvalidationReason, errorKind, time.Since(cacheStart))
	s.logStageEnd(contracts.CompileStageCache, time.Since(cacheStart))

	result.RunResult = runExecutionResult.Result
	result.RunTrace = runExecutionResult.ExecutedTasks
	result.CacheDBPath = analysisResult.paths.CacheDBPath
	result.CacheAnalysis = []CacheAnalysis{cacheAnalysis}
	return result, nil
}

func (s *Service) Build(ctx context.Context, options BuildOptions) (Result, error) {
	analysisResult, err := s.analyze(ctx, options.Entry, options.OutDir, "")
	if err != nil {
		return Result{}, err
	}
	result := analysisResult.result
	if hasErrorDiagnostics(result.Diagnostics) {
		return result, nil
	}

	emitStart := time.Now()
	s.logStageStart(contracts.CompileStageEmit)
	emitResult, err := emitter.EmitGo(
		analysisResult.moduleName,
		analysisResult.typeDeclarations,
		toSemaTasks(analysisResult.taskFingerprints),
		analysisResult.paths.OutDir,
	)
	if err != nil {
		s.logStageFailure(contracts.CompileStageEmit, time.Since(emitStart), contracts.DiagnosticKindIOError, err)
		return Result{}, fmt.Errorf("emit go source: %w", err)
	}
	s.logStageEnd(contracts.CompileStageEmit, time.Since(emitStart))
	result.GeneratedFiles = []string{emitResult.Path}

	cacheStart := time.Now()
	s.logStageStart(contracts.CompileStageCache)
	store, err := openCacheStore(analysisResult.paths.CacheDBPath)
	if err != nil {
		s.logStageFailure(contracts.CompileStageCache, time.Since(cacheStart), contracts.DiagnosticKindIOError, err)
		return Result{}, fmt.Errorf("open cache store: %w", err)
	}
	defer store.Close()

	analysisRecords := make([]CacheAnalysis, 0, len(analysisResult.taskFingerprints))
	for _, fingerprintedTask := range analysisResult.taskFingerprints {
		taskStart := time.Now()

		cacheAnalysis, errorKind, lookupErr := s.analyzeTaskCacheState(store, analysisResult.moduleName, fingerprintedTask, true)
		if lookupErr != nil {
			s.logTaskCacheEvent(fingerprintedTask.Task.ID, fingerprintedTask.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(taskStart))
			return Result{}, fmt.Errorf("analyze task cache state for %s: %w", fingerprintedTask.Task.ID, lookupErr)
		}

		record := cache.TaskRecord{
			TaskKey:                 fingerprintedTask.CacheKey,
			Module:                  analysisResult.moduleName,
			TaskID:                  fingerprintedTask.Task.ID,
			InputContentHash:        fingerprintedTask.Components.InputContentHash,
			ParameterHash:           fingerprintedTask.Components.ParameterHash,
			EnvironmentSnapshotHash: fingerprintedTask.Components.EnvironmentSnapshotHash,
			InputFingerprint:        composeInputFingerprint(fingerprintedTask.Components),
			OutputBlobRef:           "",
			Deps:                    append([]string{}, fingerprintedTask.Task.Deps...),
			Metadata: map[string]any{
				"module":      analysisResult.moduleName,
				"task_id":     fingerprintedTask.Task.ID,
				"return_type": fingerprintedTask.Task.ReturnType,
			},
			UpdatedAt: time.Now().UTC(),
		}
		if err := store.UpsertTask(record); err != nil {
			s.logTaskCacheEvent(fingerprintedTask.Task.ID, fingerprintedTask.CacheKey, cacheAnalysis.CacheHit, cacheAnalysis.InvalidationReason, contracts.DiagnosticKindIOError, time.Since(taskStart))
			return Result{}, fmt.Errorf("upsert cache row for %s: %w", fingerprintedTask.Task.ID, err)
		}
		s.logTaskCacheEvent(fingerprintedTask.Task.ID, fingerprintedTask.CacheKey, cacheAnalysis.CacheHit, cacheAnalysis.InvalidationReason, errorKind, time.Since(taskStart))
		analysisRecords = append(analysisRecords, cacheAnalysis)
	}

	s.logStageEnd(contracts.CompileStageCache, time.Since(cacheStart))
	result.CacheDBPath = analysisResult.paths.CacheDBPath
	result.CacheAnalysis = analysisRecords
	return result, nil
}

func (s *Service) analyzeTaskCacheState(store *cache.Store, moduleName string, fingerprintedTask taskFingerprint, repairCorruption bool) (CacheAnalysis, contracts.DiagnosticKind, error) {
	analysisRecord := CacheAnalysis{
		TaskID:             fingerprintedTask.Task.ID,
		CacheKey:           fingerprintedTask.CacheKey,
		CacheHit:           false,
		InvalidationReason: contracts.TtlInvalidationReasonCacheMiss,
	}

	previousState, found, err := store.GetTaskState(moduleName, fingerprintedTask.Task.ID)
	if err != nil {
		var corruptionErr *cache.CorruptionError
		if errors.As(err, &corruptionErr) {
			analysisRecord.InvalidationReason = contracts.TtlInvalidationReasonCacheCorruption
			if repairCorruption {
				if deleteErr := store.DeleteTaskState(moduleName, fingerprintedTask.Task.ID); deleteErr != nil {
					return CacheAnalysis{}, contracts.DiagnosticKindCacheCorruption, fmt.Errorf("delete corrupted cache state: %w", deleteErr)
				}
			}
			return analysisRecord, contracts.DiagnosticKindCacheCorruption, nil
		}
		return CacheAnalysis{}, contracts.DiagnosticKindIOError, err
	}

	analysisRecord.InvalidationReason, analysisRecord.CacheHit = detectInvalidationReason(found, previousState, fingerprintedTask.Components)
	return analysisRecord, "", nil
}

func detectInvalidationReason(found bool, previousState cache.TaskState, components fingerprint.Components) (contracts.TtlInvalidationReason, bool) {
	if !found {
		return contracts.TtlInvalidationReasonCacheMiss, false
	}

	if previousState.InputContentHash != components.InputContentHash {
		return contracts.TtlInvalidationReasonInputContentChanged, false
	}
	if previousState.ParameterHash != components.ParameterHash {
		return contracts.TtlInvalidationReasonParameterChanged, false
	}
	if previousState.EnvironmentSnapshotHash != components.EnvironmentSnapshotHash {
		return contracts.TtlInvalidationReasonEnvironmentChanged, false
	}
	return contracts.TtlInvalidationReasonNone, true
}

func composeInputFingerprint(components fingerprint.Components) string {
	return components.InputContentHash + ":" + components.ParameterHash + ":" + components.EnvironmentSnapshotHash
}

func (s *Service) analyze(_ context.Context, entry string, outDir string, taskFilter string) (analysis, error) {
	result := analysis{}

	loadStart := time.Now()
	s.logStageStart(contracts.CompileStageLoad)
	paths, err := source.ResolvePaths("", entry, outDir)
	if err != nil {
		s.logStageFailure(contracts.CompileStageLoad, time.Since(loadStart), contracts.DiagnosticKindPathViolation, err)
		return analysis{}, fmt.Errorf("resolve paths: %w", err)
	}
	sourceBytes, err := os.ReadFile(paths.EntryPath)
	if err != nil {
		s.logStageFailure(contracts.CompileStageLoad, time.Since(loadStart), contracts.DiagnosticKindIOError, err)
		return analysis{}, fmt.Errorf("read entry source: %w", err)
	}
	s.logStageEnd(contracts.CompileStageLoad, time.Since(loadStart))

	lexStart := time.Now()
	s.logStageStart(contracts.CompileStageLex)
	tokens, lexDiagnostics := lexer.Lex(string(sourceBytes))
	s.logStageEnd(contracts.CompileStageLex, time.Since(lexStart))

	parseStart := time.Now()
	s.logStageStart(contracts.CompileStageParse)
	module, parseDiagnostics := parser.Parse(tokens)
	s.logStageEnd(contracts.CompileStageParse, time.Since(parseStart))

	typecheckStart := time.Now()
	s.logStageStart(contracts.CompileStageTypecheck)
	semaResult := sema.Check(module)
	s.logStageEnd(contracts.CompileStageTypecheck, time.Since(typecheckStart))

	graphStart := time.Now()
	s.logStageStart(contracts.CompileStageGraph)
	dependencyGraph := graph.New(toGraphTasks(semaResult.Tasks))
	if cycle, hasCycle := dependencyGraph.DetectCycle(); hasCycle {
		cycleText := ""
		for index, node := range cycle {
			if index > 0 {
				cycleText += " -> "
			}
			cycleText += node
		}
		semaResult.Diagnostics = append(semaResult.Diagnostics, diagnostic.Diagnostic{
			Kind:    contracts.DiagnosticKindCycleError,
			Message: "task dependency cycle detected: " + cycleText,
			Line:    1,
			Column:  1,
		})
	}
	s.logStageEnd(contracts.CompileStageGraph, time.Since(graphStart))

	fingerprintedTasks := make([]taskFingerprint, 0, len(semaResult.Tasks))
	for _, task := range semaResult.Tasks {
		parameterTypes := make([]string, 0, len(task.Params))
		for _, parameter := range task.Params {
			parameterTypes = append(parameterTypes, parameter.Type)
		}
		signature := fingerprint.CanonicalSignature(task.ID, parameterTypes, task.ReturnType)
		components := fingerprint.BuildComponents(sourceBytes, signature)
		cacheKey := fingerprint.CacheKey(components)
		fingerprintedTasks = append(fingerprintedTasks, taskFingerprint{Task: task, Components: components, CacheKey: cacheKey})
	}
	sort.Slice(fingerprintedTasks, func(left int, right int) bool {
		return fingerprintedTasks[left].Task.ID < fingerprintedTasks[right].Task.ID
	})

	tasks := make([]Task, 0, len(fingerprintedTasks))
	for _, fingerprintedTask := range fingerprintedTasks {
		tasks = append(tasks, Task{
			ID:         fingerprintedTask.Task.ID,
			Params:     append([]sema.TaskParam{}, fingerprintedTask.Task.Params...),
			ReturnType: fingerprintedTask.Task.ReturnType,
			Deps:       append([]string{}, fingerprintedTask.Task.Deps...),
			CacheKey:   fingerprintedTask.CacheKey,
		})
	}

	selectedTasks := tasks
	selectedFingerprints := fingerprintedTasks
	if taskFilter != "" {
		selectedTasks = make([]Task, 0, 1)
		selectedFingerprints = make([]taskFingerprint, 0, 1)
		for _, task := range tasks {
			if task.ID == taskFilter {
				selectedTasks = append(selectedTasks, task)
			}
		}
		for _, task := range fingerprintedTasks {
			if task.Task.ID == taskFilter {
				selectedFingerprints = append(selectedFingerprints, task)
			}
		}
		if len(selectedTasks) == 0 {
			semaResult.Diagnostics = append(semaResult.Diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: fmt.Sprintf("task not found: %s", taskFilter),
				Line:    1,
				Column:  1,
			})
		}
	}

	components := fingerprint.Components{}
	if len(selectedFingerprints) > 0 {
		components = selectedFingerprints[0].Components
	}

	result = analysis{
		paths:            paths,
		module:           module,
		moduleName:       module.PackageName,
		typeDeclarations: append([]sema.TypeDecl{}, semaResult.Types...),
		diagnostics:      mergeDiagnostics(lexDiagnostics, parseDiagnostics, semaResult.Diagnostics),
		taskFingerprints: fingerprintedTasks,
		sourceBytes:      sourceBytes,
		result: Result{
			Entry:                 paths.EntryPath,
			Module:                module.PackageName,
			Tasks:                 selectedTasks,
			Diagnostics:           mergeDiagnostics(lexDiagnostics, parseDiagnostics, semaResult.Diagnostics),
			FingerprintComponents: components,
		},
	}
	return result, nil
}

func toGraphTasks(tasks []sema.Task) []graph.Task {
	results := make([]graph.Task, 0, len(tasks))
	for _, task := range tasks {
		parameterTypes := make([]string, 0, len(task.Params))
		for _, parameter := range task.Params {
			parameterTypes = append(parameterTypes, parameter.Type)
		}
		results = append(results, graph.Task{
			ID:         task.ID,
			Params:     parameterTypes,
			ReturnType: task.ReturnType,
			Deps:       append([]string{}, task.Deps...),
		})
	}
	return results
}

func toSemaTasks(tasks []taskFingerprint) []sema.Task {
	results := make([]sema.Task, 0, len(tasks))
	for _, task := range tasks {
		results = append(results, task.Task)
	}
	return results
}

func mergeDiagnostics(groups ...[]diagnostic.Diagnostic) []diagnostic.Diagnostic {
	results := make([]diagnostic.Diagnostic, 0)
	for _, group := range groups {
		results = append(results, group...)
	}
	return results
}

func hasErrorDiagnostics(diagnostics []diagnostic.Diagnostic) bool {
	return len(diagnostics) > 0
}

func findTaskFingerprintByID(tasks []taskFingerprint, taskID string) (taskFingerprint, bool) {
	for _, fingerprintedTask := range tasks {
		if fingerprintedTask.Task.ID == taskID {
			return fingerprintedTask, true
		}
	}
	return taskFingerprint{}, false
}

func decodeRunMetadata(metadata map[string]any) (any, []string, bool) {
	if metadata == nil {
		return nil, nil, false
	}

	runResult, exists := metadata["run_result"]
	if !exists {
		return nil, nil, false
	}
	runTraceRaw, exists := metadata["run_trace"]
	if !exists {
		return nil, nil, false
	}
	runTrace, ok := toStringSlice(runTraceRaw)
	if !ok {
		return nil, nil, false
	}
	return runResult, runTrace, true
}

func toStringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...), true
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			stringValue, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, stringValue)
		}
		return values, true
	default:
		return nil, false
	}
}

func buildRunParameterHash(task sema.Task, args map[string]any) (string, error) {
	parameterTypes := make([]string, 0, len(task.Params))
	for _, parameter := range task.Params {
		parameterTypes = append(parameterTypes, parameter.Type)
	}
	signature := fingerprint.CanonicalSignature(task.ID, parameterTypes, task.ReturnType)

	if args == nil {
		args = map[string]any{}
	}
	argsSignature, err := stableRunValueString(args)
	if err != nil {
		return "", err
	}
	return fingerprint.HashString(signature + "|" + argsSignature), nil
}

func stableRunValueString(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case string:
		return "str:" + strconv.Quote(typed), nil
	case bool:
		if typed {
			return "bool:true", nil
		}
		return "bool:false", nil
	case json.Number:
		return "num:" + typed.String(), nil
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("num:%v", typed), nil
	case uint, uint8, uint16, uint32, uint64, uintptr:
		return fmt.Sprintf("num:%v", typed), nil
	case float32, float64:
		return fmt.Sprintf("num:%v", typed), nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			itemSignature, err := stableRunValueString(typed[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, key+"="+itemSignature)
		}
		return "obj:{" + strings.Join(parts, ",") + "}", nil
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			itemSignature, err := stableRunValueString(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, itemSignature)
		}
		return "arr:[" + strings.Join(parts, ",") + "]", nil
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("unsupported run argument type: %T", value)
		}
		return "json:" + string(payload), nil
	}
}

func validateRunArgs(parameters []sema.TaskParam, args map[string]any) []diagnostic.Diagnostic {
	diagnostics := make([]diagnostic.Diagnostic, 0)
	if args == nil {
		args = map[string]any{}
	}

	parameterByName := make(map[string]sema.TaskParam, len(parameters))
	for _, parameter := range parameters {
		parameterByName[parameter.Name] = parameter

		value, exists := args[parameter.Name]
		if !exists {
			diagnostics = append(diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: fmt.Sprintf("missing run argument: %s", parameter.Name),
				Line:    1,
				Column:  1,
			})
			continue
		}
		if !runArgumentTypeMatches(parameter.Type, value) {
			diagnostics = append(diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: fmt.Sprintf("invalid run argument type: %s expects %s", parameter.Name, parameter.Type),
				Line:    1,
				Column:  1,
			})
		}
	}

	argumentNames := make([]string, 0, len(args))
	for name := range args {
		argumentNames = append(argumentNames, name)
	}
	sort.Strings(argumentNames)
	for _, name := range argumentNames {
		if _, exists := parameterByName[name]; exists {
			continue
		}
		diagnostics = append(diagnostics, diagnostic.Diagnostic{
			Kind:    contracts.DiagnosticKindTypeError,
			Message: fmt.Sprintf("unknown run argument: %s", name),
			Line:    1,
			Column:  1,
		})
	}
	return diagnostics
}

func runArgumentTypeMatches(expectedType string, value any) bool {
	normalizedType := strings.TrimSpace(expectedType)
	switch normalizedType {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		switch value.(type) {
		case int, int8, int16, int32, int64:
			return true
		case uint, uint8, uint16, uint32, uint64, uintptr:
			return true
		case float64, float32:
			return true
		case json.Number:
			return true
		default:
			return false
		}
	case "float32", "float64":
		switch value.(type) {
		case float32, float64:
			return true
		case json.Number:
			return true
		default:
			return false
		}
	default:
		return true
	}
}

func cloneArgs(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(args))
	for key, value := range args {
		clone[key] = value
	}
	return clone
}

func (s *Service) logStageStart(stage contracts.CompileStage) {
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"compile_stage_started",
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", 0),
		slog.String("error_kind", ""),
	)
}

func (s *Service) logStageEnd(stage contracts.CompileStage, duration time.Duration) {
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"compile_stage_completed",
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("error_kind", ""),
	)
}

func (s *Service) logStageFailure(stage contracts.CompileStage, duration time.Duration, errorKind contracts.DiagnosticKind, err error) {
	logging.Event(
		s.logger,
		slog.LevelError,
		"compile_stage_failed",
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("error_kind", string(errorKind)),
		slog.String("error", err.Error()),
	)
}

func (s *Service) logTaskCacheEvent(taskID string, cacheKey string, cacheHit bool, invalidationReason contracts.TtlInvalidationReason, errorKind contracts.DiagnosticKind, duration time.Duration) {
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"task_cache_processed",
		slog.String("compile_stage", string(contracts.CompileStageCache)),
		slog.String("task_id", taskID),
		slog.String("cache_key", cacheKey),
		slog.Bool("cache_hit", cacheHit),
		slog.String("invalidation_reason", string(invalidationReason)),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("error_kind", string(errorKind)),
	)
}
