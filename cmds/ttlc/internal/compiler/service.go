package compiler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	"math/bits"
	"os"
	"path/filepath"
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
	"github.com/delinoio/oss/cmds/ttlc/internal/messages"
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
	funcDeclarations []sema.FuncInfo
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
	traceID := newTraceID()
	analysisResult, err := s.analyze(ctx, traceID, options.Entry, ".ttl/gen", "")
	if err != nil {
		return Result{}, err
	}
	return analysisResult.result, nil
}

func (s *Service) Explain(ctx context.Context, options ExplainOptions) (Result, error) {
	traceID := newTraceID()
	analysisResult, err := s.analyze(ctx, traceID, options.Entry, ".ttl/gen", options.Task)
	if err != nil {
		return Result{}, err
	}
	result := analysisResult.result
	if len(result.Tasks) == 0 {
		return result, nil
	}

	cacheStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageCache, "")
	store, err := openCacheStore(analysisResult.paths.CacheDBPath)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageCache, "", time.Since(cacheStart), contracts.DiagnosticKindIOError, err)
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
			s.logTaskCacheEvent(traceID, "", task.ID, taskFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(taskStart))
			analysisRecords = append(analysisRecords, CacheAnalysis{
				TaskID:             taskFingerprint.Task.ID,
				CacheKey:           taskFingerprint.CacheKey,
				CacheHit:           false,
				InvalidationReason: contracts.TtlInvalidationReasonCacheMiss,
			})
			continue
		}
		analysisRecords = append(analysisRecords, taskAnalysis)
		s.logTaskCacheEvent(traceID, "", taskAnalysis.TaskID, taskAnalysis.CacheKey, taskAnalysis.CacheHit, taskAnalysis.InvalidationReason, errorKind, time.Since(taskStart))
	}

	result.CacheAnalysis = analysisRecords
	s.logStageEnd(traceID, contracts.CompileStageCache, "", time.Since(cacheStart))
	return result, nil
}

func (s *Service) Run(ctx context.Context, options RunOptions) (Result, error) {
	traceID := newTraceID()
	analysisResult, err := s.analyze(ctx, traceID, options.Entry, ".ttl/gen", options.Task)
	if err != nil {
		return Result{}, err
	}

	result := analysisResult.result
	result.Task = options.Task
	result.Args = cloneArgs(options.Args)

	if strings.TrimSpace(options.Task) == "" {
		result.Diagnostics = append(result.Diagnostics, messages.NewDiagnostic(
			contracts.DiagnosticKindTypeError,
			messages.DiagnosticRunTaskRequired,
			1,
			1,
		))
		return result, nil
	}
	if hasErrorDiagnostics(result.Diagnostics) {
		return result, nil
	}
	if len(result.Tasks) != 1 {
		return result, nil
	}

	rootTask := result.Tasks[0]
	result.Diagnostics = append(result.Diagnostics, validateRunArgs(rootTask.Params, options.Args, analysisResult.typeDeclarations)...)
	if hasErrorDiagnostics(result.Diagnostics) {
		return result, nil
	}

	rootFingerprint, found := findTaskFingerprintByID(analysisResult.taskFingerprints, rootTask.ID)
	if !found {
		return Result{}, messages.NewError(messages.ErrorRootTaskFingerprintMissing, rootTask.ID)
	}
	runParameterHash, err := buildRunParameterHash(rootFingerprint.Task, options.Args)
	if err != nil {
		return Result{}, messages.WrapError(messages.ErrorBuildRunParameterHash, err, rootTask.ID)
	}
	rootFingerprint.Components.ParameterHash = runParameterHash
	rootFingerprint.CacheKey = fingerprint.CacheKey(rootFingerprint.Components)
	result.FingerprintComponents = rootFingerprint.Components
	runCacheModuleName := composeRunCacheModuleName(analysisResult.moduleName)
	executionTraceID := ""

	cacheStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageCache, executionTraceID)
	store, err := openCacheStore(analysisResult.paths.CacheDBPath)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageCache, executionTraceID, time.Since(cacheStart), contracts.DiagnosticKindIOError, err)
		return Result{}, messages.WrapError(messages.ErrorOpenCacheStore, err, analysisResult.paths.CacheDBPath)
	}
	defer store.Close()

	cacheAnalysis, errorKind, lookupErr := s.analyzeTaskCacheState(store, runCacheModuleName, rootFingerprint, true)
	if lookupErr != nil {
		s.logTaskCacheEvent(traceID, executionTraceID, rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(cacheStart))
		return Result{}, messages.WrapError(messages.ErrorAnalyzeTaskCacheState, lookupErr, runCacheModuleName, rootFingerprint.Task.ID, rootFingerprint.CacheKey)
	}

	if cacheAnalysis.CacheHit {
		cachedState, stateFound, stateErr := store.GetTaskStateByTaskKey(rootFingerprint.CacheKey)
		if stateErr != nil {
			s.logTaskCacheEvent(traceID, executionTraceID, rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(cacheStart))
			return Result{}, messages.WrapError(messages.ErrorReadCachedTaskState, stateErr, runCacheModuleName, rootFingerprint.Task.ID, rootFingerprint.CacheKey)
		}
		if stateFound {
			cachedResult, cachedRunTrace, ok := decodeRunMetadata(cachedState.Metadata)
			if ok {
				executionTraceID = buildExecutionTraceID(cachedRunTrace)
				result.RunResult = cachedResult
				result.RunTrace = cachedRunTrace
				result.CacheDBPath = analysisResult.paths.CacheDBPath
				result.CacheAnalysis = []CacheAnalysis{cacheAnalysis}
				s.logTaskCacheEvent(traceID, executionTraceID, rootFingerprint.Task.ID, rootFingerprint.CacheKey, true, cacheAnalysis.InvalidationReason, errorKind, time.Since(cacheStart))
				s.logStageEnd(traceID, contracts.CompileStageCache, executionTraceID, time.Since(cacheStart))
				return result, nil
			}
		}
		cacheAnalysis.CacheHit = false
		cacheAnalysis.InvalidationReason = contracts.TtlInvalidationReasonCacheMiss
	}

	runStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageRun, executionTraceID)
	runProgram, err := runner.BuildProgram(analysisResult.module, rootTask.ID, options.Args)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageRun, executionTraceID, time.Since(runStart), contracts.DiagnosticKindTypeError, err)
		return Result{}, messages.WrapError(messages.ErrorBuildRunProgram, err, rootTask.ID)
	}
	runnerSource, err := runner.GenerateGoSource(runProgram)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageRun, executionTraceID, time.Since(runStart), contracts.DiagnosticKindIOError, err)
		return Result{}, messages.WrapError(messages.ErrorGenerateRunnerSource, err, rootTask.ID)
	}
	runExecutionResult, err := runner.Execute(ctx, analysisResult.paths.OutDir, runnerSource)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageRun, executionTraceID, time.Since(runStart), contracts.DiagnosticKindIOError, err)
		return Result{}, messages.WrapError(messages.ErrorExecuteGeneratedRunner, err, rootTask.ID, analysisResult.paths.OutDir)
	}
	executionTraceID = buildExecutionTraceID(runExecutionResult.ExecutedTasks)
	s.logStageEnd(traceID, contracts.CompileStageRun, executionTraceID, time.Since(runStart))

	record := cache.TaskRecord{
		TaskKey:                 rootFingerprint.CacheKey,
		Module:                  runCacheModuleName,
		TaskID:                  rootTask.ID,
		InputContentHash:        rootFingerprint.Components.InputContentHash,
		ParameterHash:           rootFingerprint.Components.ParameterHash,
		EnvironmentSnapshotHash: rootFingerprint.Components.EnvironmentSnapshotHash,
		InputFingerprint:        composeInputFingerprint(rootFingerprint.Components),
		OutputBlobRef:           "",
		Deps:                    append([]string{}, rootTask.Deps...),
		Metadata: map[string]any{
			"module":      analysisResult.moduleName,
			"cache_scope": "run",
			"task_id":     rootTask.ID,
			"return_type": rootTask.ReturnType,
			"run_result":  runExecutionResult.Result,
			"run_trace":   runExecutionResult.ExecutedTasks,
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertTask(record); err != nil {
		s.logTaskCacheEvent(traceID, executionTraceID, rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(cacheStart))
		return Result{}, messages.WrapError(messages.ErrorUpsertRunCacheRecord, err, runCacheModuleName, rootTask.ID, rootFingerprint.CacheKey)
	}

	s.logTaskCacheEvent(traceID, executionTraceID, rootFingerprint.Task.ID, rootFingerprint.CacheKey, false, cacheAnalysis.InvalidationReason, errorKind, time.Since(cacheStart))
	s.logStageEnd(traceID, contracts.CompileStageCache, executionTraceID, time.Since(cacheStart))

	result.RunResult = runExecutionResult.Result
	result.RunTrace = runExecutionResult.ExecutedTasks
	result.CacheDBPath = analysisResult.paths.CacheDBPath
	result.CacheAnalysis = []CacheAnalysis{cacheAnalysis}
	return result, nil
}

func (s *Service) Build(ctx context.Context, options BuildOptions) (Result, error) {
	traceID := newTraceID()
	analysisResult, err := s.analyze(ctx, traceID, options.Entry, options.OutDir, "")
	if err != nil {
		return Result{}, err
	}
	result := analysisResult.result
	if hasErrorDiagnostics(result.Diagnostics) {
		return result, nil
	}

	emitStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageEmit, "")
	emitResult, err := emitter.EmitGoWithAST(
		analysisResult.moduleName,
		analysisResult.typeDeclarations,
		toSemaTasks(analysisResult.taskFingerprints),
		toSemaFuncs(analysisResult),
		analysisResult.module,
		analysisResult.paths.OutDir,
	)
	if err != nil {
		s.logDiagnosticEvent(traceID, contracts.CompileStageEmit, analysisResult.paths.EntryPath, diagnostic.Diagnostic{
			Kind:    contracts.DiagnosticKindIOError,
			Message: messages.FormatDiagnostic(messages.DiagnosticEmitStageFailure, err.Error()),
			Line:    1,
			Column:  1,
		})
		s.logStageFailure(traceID, contracts.CompileStageEmit, "", time.Since(emitStart), contracts.DiagnosticKindIOError, err)
		return Result{}, messages.WrapError(messages.ErrorEmitGoSource, err, analysisResult.moduleName)
	}
	s.logStageEnd(traceID, contracts.CompileStageEmit, "", time.Since(emitStart))
	result.GeneratedFiles = []string{emitResult.Path}

	cacheStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageCache, "")
	store, err := openCacheStore(analysisResult.paths.CacheDBPath)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageCache, "", time.Since(cacheStart), contracts.DiagnosticKindIOError, err)
		return Result{}, messages.WrapError(messages.ErrorOpenCacheStore, err, analysisResult.paths.CacheDBPath)
	}
	defer store.Close()

	analysisRecords := make([]CacheAnalysis, 0, len(analysisResult.taskFingerprints))
	for _, fingerprintedTask := range analysisResult.taskFingerprints {
		taskStart := time.Now()

		cacheAnalysis, errorKind, lookupErr := s.analyzeTaskCacheState(store, analysisResult.moduleName, fingerprintedTask, true)
		if lookupErr != nil {
			s.logTaskCacheEvent(traceID, "", fingerprintedTask.Task.ID, fingerprintedTask.CacheKey, false, contracts.TtlInvalidationReasonCacheMiss, contracts.DiagnosticKindIOError, time.Since(taskStart))
			return Result{}, messages.WrapError(messages.ErrorAnalyzeTaskCacheState, lookupErr, analysisResult.moduleName, fingerprintedTask.Task.ID, fingerprintedTask.CacheKey)
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
			s.logTaskCacheEvent(traceID, "", fingerprintedTask.Task.ID, fingerprintedTask.CacheKey, cacheAnalysis.CacheHit, cacheAnalysis.InvalidationReason, contracts.DiagnosticKindIOError, time.Since(taskStart))
			return Result{}, messages.WrapError(messages.ErrorUpsertTaskCacheRecord, err, analysisResult.moduleName, fingerprintedTask.Task.ID, fingerprintedTask.CacheKey)
		}
		s.logTaskCacheEvent(traceID, "", fingerprintedTask.Task.ID, fingerprintedTask.CacheKey, cacheAnalysis.CacheHit, cacheAnalysis.InvalidationReason, errorKind, time.Since(taskStart))
		analysisRecords = append(analysisRecords, cacheAnalysis)
	}

	s.logStageEnd(traceID, contracts.CompileStageCache, "", time.Since(cacheStart))
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
					return CacheAnalysis{}, contracts.DiagnosticKindCacheCorruption, messages.WrapError(messages.ErrorDeleteCorruptedCacheState, deleteErr, moduleName, fingerprintedTask.Task.ID)
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

func (s *Service) analyze(_ context.Context, traceID string, entry string, outDir string, taskFilter string) (analysis, error) {
	result := analysis{}

	loadStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageLoad, "")
	paths, err := source.ResolvePaths("", entry, outDir)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageLoad, "", time.Since(loadStart), contracts.DiagnosticKindPathViolation, err)
		return analysis{}, messages.WrapError(messages.ErrorResolveCompilerPaths, err, entry, outDir)
	}
	sourceBytes, err := os.ReadFile(paths.EntryPath)
	if err != nil {
		s.logStageFailure(traceID, contracts.CompileStageLoad, "", time.Since(loadStart), contracts.DiagnosticKindIOError, err)
		return analysis{}, messages.WrapError(messages.ErrorReadEntrySourceFile, err, paths.EntryPath)
	}
	s.logStageEnd(traceID, contracts.CompileStageLoad, "", time.Since(loadStart))

	lexStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageLex, "")
	tokens, lexDiagnostics := lexer.Lex(string(sourceBytes))
	s.logStageEnd(traceID, contracts.CompileStageLex, "", time.Since(lexStart))
	s.logDiagnostics(traceID, contracts.CompileStageLex, paths.EntryPath, lexDiagnostics)

	parseStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageParse, "")
	module, parseDiagnostics := parser.Parse(tokens)
	s.logStageEnd(traceID, contracts.CompileStageParse, "", time.Since(parseStart))
	s.logDiagnostics(traceID, contracts.CompileStageParse, paths.EntryPath, parseDiagnostics)

	importDiagnostics := make([]diagnostic.Diagnostic, 0)
	if len(module.Imports) > 0 {
		visited := map[string]struct{}{paths.EntryPath: {}}
		importDiags := s.loadImportsRecursive(traceID, module, paths.WorkspaceRoot, paths.EntryPath, visited)
		importDiagnostics = append(importDiagnostics, importDiags...)
	}

	typecheckStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageTypecheck, "")
	semaResult := sema.Check(module)
	s.logStageEnd(traceID, contracts.CompileStageTypecheck, "", time.Since(typecheckStart))

	graphStart := time.Now()
	s.logStageStart(traceID, contracts.CompileStageGraph, "")
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
			Message: messages.FormatDiagnostic(messages.DiagnosticTaskDependencyCycle, cycleText),
			Line:    1,
			Column:  1,
		})
	}
	s.logStageEnd(traceID, contracts.CompileStageGraph, "", time.Since(graphStart))
	s.logDiagnostics(traceID, contracts.CompileStageTypecheck, paths.EntryPath, semaResult.Diagnostics)

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
			issue := diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: messages.FormatDiagnostic(messages.DiagnosticTaskNotFound, taskFilter),
				Line:    1,
				Column:  1,
			}
			semaResult.Diagnostics = append(semaResult.Diagnostics, issue)
			s.logDiagnosticEvent(traceID, contracts.CompileStageTypecheck, paths.EntryPath, issue)
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
		funcDeclarations: append([]sema.FuncInfo{}, semaResult.Funcs...),
		diagnostics:      mergeDiagnostics(lexDiagnostics, parseDiagnostics, importDiagnostics, semaResult.Diagnostics),
		taskFingerprints: fingerprintedTasks,
		sourceBytes:      sourceBytes,
		result: Result{
			Entry:                 paths.EntryPath,
			Module:                module.PackageName,
			Tasks:                 selectedTasks,
			Diagnostics:           mergeDiagnostics(lexDiagnostics, parseDiagnostics, importDiagnostics, semaResult.Diagnostics),
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

func (s *Service) loadImportsRecursive(traceID string, module *ast.Module, workspaceRoot string, currentFilePath string, visited map[string]struct{}) []diagnostic.Diagnostic {
	diagnostics := make([]diagnostic.Diagnostic, 0)

	for _, importDecl := range module.Imports {
		resolvedPath, err := source.ResolveImportPath(workspaceRoot, currentFilePath, importDecl.Path)
		if err != nil {
			issue := diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindImportNotFound,
				Message: messages.FormatDiagnostic(messages.DiagnosticImportResolveFailed, importDecl.Path, err.Error()),
				Line:    importDecl.Span.Start.Line,
				Column:  importDecl.Span.Start.Column,
			}
			diagnostics = append(diagnostics, issue)
			s.logDiagnosticEvent(traceID, contracts.CompileStageLoad, currentFilePath, issue)
			continue
		}

		if _, alreadyVisited := visited[resolvedPath]; alreadyVisited {
			issue := diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindImportCycle,
				Message: messages.FormatDiagnostic(messages.DiagnosticImportCycle, importDecl.Path),
				Line:    importDecl.Span.Start.Line,
				Column:  importDecl.Span.Start.Column,
			}
			diagnostics = append(diagnostics, issue)
			s.logDiagnosticEvent(traceID, contracts.CompileStageLoad, currentFilePath, issue)
			continue
		}
		visited[resolvedPath] = struct{}{}

		importedBytes, err := os.ReadFile(resolvedPath)
		if err != nil {
			issue := diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindImportNotFound,
				Message: messages.FormatDiagnostic(messages.DiagnosticImportReadFailed, importDecl.Path, err.Error()),
				Line:    importDecl.Span.Start.Line,
				Column:  importDecl.Span.Start.Column,
			}
			diagnostics = append(diagnostics, issue)
			s.logDiagnosticEvent(traceID, contracts.CompileStageLoad, resolvedPath, issue)
			continue
		}

		tokens, lexDiags := lexer.Lex(string(importedBytes))
		diagnostics = append(diagnostics, lexDiags...)
		s.logDiagnostics(traceID, contracts.CompileStageLex, resolvedPath, lexDiags)

		importedModule, parseDiags := parser.Parse(tokens)
		diagnostics = append(diagnostics, parseDiags...)
		s.logDiagnostics(traceID, contracts.CompileStageParse, resolvedPath, parseDiags)

		if len(importedModule.Imports) > 0 {
			importDiags := s.loadImportsRecursive(traceID, importedModule, workspaceRoot, resolvedPath, visited)
			diagnostics = append(diagnostics, importDiags...)
		}

		module.Decls = append(module.Decls, importedModule.Decls...)
	}

	module.Imports = nil
	return diagnostics
}

func toSemaFuncs(a analysis) []sema.FuncInfo {
	return append([]sema.FuncInfo{}, a.funcDeclarations...)
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
			return "", messages.NewError(messages.ErrorUnsupportedRunArgumentType, value)
		}
		return "json:" + string(payload), nil
	}
}

func validateRunArgs(parameters []sema.TaskParam, args map[string]any, typeDeclarations []sema.TypeDecl) []diagnostic.Diagnostic {
	diagnostics := make([]diagnostic.Diagnostic, 0)
	if args == nil {
		args = map[string]any{}
	}
	typeDeclarationsByName := make(map[string]sema.TypeDecl, len(typeDeclarations))
	for _, typeDeclaration := range typeDeclarations {
		typeDeclarationsByName[typeDeclaration.Name] = typeDeclaration
	}

	parameterByName := make(map[string]sema.TaskParam, len(parameters))
	for _, parameter := range parameters {
		parameterByName[parameter.Name] = parameter

		value, exists := args[parameter.Name]
		if !exists {
			diagnostics = append(diagnostics, messages.NewDiagnostic(
				contracts.DiagnosticKindTypeError,
				messages.DiagnosticMissingRunArgument,
				1,
				1,
				parameter.Name,
			))
			continue
		}
		mismatch := runArgumentTypeMismatch(parameter.Type, value, typeDeclarationsByName, parameter.Name, 0)
		if mismatch != nil {
			diagnostics = append(diagnostics, messages.NewDiagnostic(
				contracts.DiagnosticKindTypeError,
				messages.DiagnosticInvalidRunArgumentType,
				1,
				1,
				parameter.Name,
				mismatch.Path,
				mismatch.ExpectedType,
				mismatch.ActualType,
			))
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
		diagnostics = append(diagnostics, messages.NewDiagnostic(
			contracts.DiagnosticKindTypeError,
			messages.DiagnosticUnknownRunArgument,
			1,
			1,
			name,
		))
	}
	return diagnostics
}

const maxRunArgumentValidationDepth = 128

type runArgumentMismatch struct {
	Path         string
	ExpectedType string
	ActualType   string
}

func runArgumentTypeMismatch(expectedType string, value any, typeDeclarationsByName map[string]sema.TypeDecl, path string, depth int) *runArgumentMismatch {
	if depth > maxRunArgumentValidationDepth {
		return &runArgumentMismatch{
			Path:         path,
			ExpectedType: strings.TrimSpace(expectedType),
			ActualType:   "validation_depth_exceeded",
		}
	}

	normalizedType := strings.TrimSpace(expectedType)
	switch normalizedType {
	case "string":
		_, ok := value.(string)
		if ok {
			return nil
		}
		return &runArgumentMismatch{
			Path:         path,
			ExpectedType: normalizedType,
			ActualType:   runValueTypeLabel(value),
		}
	case "bool":
		_, ok := value.(bool)
		if ok {
			return nil
		}
		return &runArgumentMismatch{
			Path:         path,
			ExpectedType: normalizedType,
			ActualType:   runValueTypeLabel(value),
		}
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		if runIntegerArgumentTypeMatches(normalizedType, value) {
			return nil
		}
		return &runArgumentMismatch{
			Path:         path,
			ExpectedType: normalizedType,
			ActualType:   runValueTypeLabel(value),
		}
	case "float32", "float64":
		if runFloatArgumentTypeMatches(normalizedType, value) {
			return nil
		}
		return &runArgumentMismatch{
			Path:         path,
			ExpectedType: normalizedType,
			ActualType:   runValueTypeLabel(value),
		}
	default:
		typeName := normalizedType
		if separator := strings.LastIndex(typeName, "."); separator >= 0 {
			typeName = typeName[separator+1:]
		}
		typeDeclaration, exists := typeDeclarationsByName[typeName]
		if !exists {
			return &runArgumentMismatch{
				Path:         path,
				ExpectedType: normalizedType,
				ActualType:   runValueTypeLabel(value),
			}
		}

		objectValue, ok := value.(map[string]any)
		if !ok {
			return &runArgumentMismatch{
				Path:         path,
				ExpectedType: "object(" + normalizedType + ")",
				ActualType:   runValueTypeLabel(value),
			}
		}

		seenFields := make(map[string]struct{}, len(objectValue))
		for fieldName := range objectValue {
			seenFields[fieldName] = struct{}{}
		}

		for _, field := range typeDeclaration.Fields {
			fieldValue, exists := objectValue[field.Name]
			if !exists {
				return &runArgumentMismatch{
					Path:         runArgumentPath(path, field.Name),
					ExpectedType: strings.TrimSpace(field.Type),
					ActualType:   "missing",
				}
			}
			delete(seenFields, field.Name)
			mismatch := runArgumentTypeMismatch(field.Type, fieldValue, typeDeclarationsByName, runArgumentPath(path, field.Name), depth+1)
			if mismatch != nil {
				return mismatch
			}
		}
		if len(seenFields) == 0 {
			return nil
		}
		extraFieldNames := make([]string, 0, len(seenFields))
		for fieldName := range seenFields {
			extraFieldNames = append(extraFieldNames, fieldName)
		}
		sort.Strings(extraFieldNames)
		firstUnexpectedField := extraFieldNames[0]
		return &runArgumentMismatch{
			Path:         runArgumentPath(path, firstUnexpectedField),
			ExpectedType: "absent",
			ActualType:   runValueTypeLabel(objectValue[firstUnexpectedField]),
		}
	}
}

func runArgumentPath(base string, segment string) string {
	normalizedBase := strings.TrimSpace(base)
	normalizedSegment := strings.TrimSpace(segment)
	if normalizedBase == "" {
		return normalizedSegment
	}
	if normalizedSegment == "" {
		return normalizedBase
	}
	return normalizedBase + "." + normalizedSegment
}

func runValueTypeLabel(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return "integer"
	case float32, float64:
		return "number"
	case json.Number:
		if isJSONIntegerValue(typed.String()) {
			return "number(integer)"
		}
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func runIntegerArgumentTypeMatches(expectedType string, value any) bool {
	minimum, maximum, hasBounds := runIntegerBounds(expectedType)
	if !hasBounds {
		return false
	}
	valueAsInteger, ok := runIntegerValueAsBigInt(value)
	if !ok {
		return false
	}
	if valueAsInteger.Cmp(minimum) < 0 {
		return false
	}
	if valueAsInteger.Cmp(maximum) > 0 {
		return false
	}
	return true
}

func runFloatArgumentTypeMatches(expectedType string, value any) bool {
	fitsExpectedType := func(floatValue float64) bool {
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return false
		}
		if expectedType == "float32" {
			return floatValue <= math.MaxFloat32 && floatValue >= -math.MaxFloat32
		}
		return true
	}

	switch typed := value.(type) {
	case int, int8, int16, int32, int64:
		if expectedType != "float32" {
			return true
		}
		integerValue, ok := runIntegerValueAsBigInt(typed)
		if !ok {
			return false
		}
		return runIntegerFitsFloat32(integerValue)
	case uint, uint8, uint16, uint32, uint64, uintptr:
		if expectedType != "float32" {
			return true
		}
		integerValue, ok := runIntegerValueAsBigInt(typed)
		if !ok {
			return false
		}
		return runIntegerFitsFloat32(integerValue)
	case float32:
		return fitsExpectedType(float64(typed))
	case float64:
		return fitsExpectedType(typed)
	case json.Number:
		floatValue, err := typed.Float64()
		if err != nil {
			return false
		}
		return fitsExpectedType(floatValue)
	default:
		return false
	}
}

func runIntegerFitsFloat32(value *big.Int) bool {
	if value == nil {
		return false
	}
	if value.Sign() == 0 {
		return true
	}

	absoluteValue := new(big.Int).Abs(new(big.Int).Set(value))
	if absoluteValue.BitLen() <= 24 {
		return true
	}

	truncatedBits := absoluteValue.BitLen() - 24
	mask := new(big.Int).Lsh(big.NewInt(1), uint(truncatedBits))
	mask.Sub(mask, big.NewInt(1))
	lowBits := new(big.Int).And(absoluteValue, mask)
	return lowBits.Sign() == 0
}

func isFiniteWholeNumber(value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	return math.Trunc(value) == value
}

func isJSONIntegerValue(raw string) bool {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return false
	}
	if strings.ContainsAny(normalized, ".eE") {
		return false
	}

	start := 0
	if normalized[0] == '+' || normalized[0] == '-' {
		start = 1
	}
	if start >= len(normalized) {
		return false
	}
	for _, character := range normalized[start:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func runIntegerValueAsBigInt(value any) (*big.Int, bool) {
	switch typed := value.(type) {
	case int:
		return big.NewInt(int64(typed)), true
	case int8:
		return big.NewInt(int64(typed)), true
	case int16:
		return big.NewInt(int64(typed)), true
	case int32:
		return big.NewInt(int64(typed)), true
	case int64:
		return big.NewInt(typed), true
	case uint:
		number := new(big.Int)
		number.SetUint64(uint64(typed))
		return number, true
	case uint8:
		number := new(big.Int)
		number.SetUint64(uint64(typed))
		return number, true
	case uint16:
		number := new(big.Int)
		number.SetUint64(uint64(typed))
		return number, true
	case uint32:
		number := new(big.Int)
		number.SetUint64(uint64(typed))
		return number, true
	case uint64:
		number := new(big.Int)
		number.SetUint64(typed)
		return number, true
	case uintptr:
		number := new(big.Int)
		number.SetUint64(uint64(typed))
		return number, true
	case float32:
		if !isFiniteWholeNumber(float64(typed)) {
			return nil, false
		}
		return parseIntegerStringToBigInt(strconv.FormatFloat(float64(typed), 'f', 0, 64))
	case float64:
		if !isFiniteWholeNumber(typed) {
			return nil, false
		}
		return parseIntegerStringToBigInt(strconv.FormatFloat(typed, 'f', 0, 64))
	case json.Number:
		return parseIntegerStringToBigInt(typed.String())
	default:
		return nil, false
	}
}

func parseIntegerStringToBigInt(raw string) (*big.Int, bool) {
	normalizedValue := strings.TrimSpace(raw)
	if normalizedValue == "" {
		return nil, false
	}
	if !isJSONIntegerValue(normalizedValue) {
		return nil, false
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(normalizedValue, 10); !ok {
		return nil, false
	}
	return parsed, true
}

func runIntegerBounds(expectedType string) (*big.Int, *big.Int, bool) {
	switch expectedType {
	case "int8":
		return big.NewInt(math.MinInt8), big.NewInt(math.MaxInt8), true
	case "int16":
		return big.NewInt(math.MinInt16), big.NewInt(math.MaxInt16), true
	case "int32":
		return big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32), true
	case "int64":
		return big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64), true
	case "int":
		if strconv.IntSize == 32 {
			return big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32), true
		}
		return big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64), true
	case "uint8":
		return big.NewInt(0), big.NewInt(math.MaxUint8), true
	case "uint16":
		return big.NewInt(0), big.NewInt(math.MaxUint16), true
	case "uint32":
		return big.NewInt(0), new(big.Int).SetUint64(math.MaxUint32), true
	case "uint64":
		return big.NewInt(0), new(big.Int).SetUint64(math.MaxUint64), true
	case "uint":
		if bits.UintSize == 32 {
			return big.NewInt(0), new(big.Int).SetUint64(math.MaxUint32), true
		}
		return big.NewInt(0), new(big.Int).SetUint64(math.MaxUint64), true
	case "uintptr":
		if bits.UintSize == 32 {
			return big.NewInt(0), new(big.Int).SetUint64(math.MaxUint32), true
		}
		return big.NewInt(0), new(big.Int).SetUint64(math.MaxUint64), true
	default:
		return nil, nil, false
	}
}

func composeRunCacheModuleName(moduleName string) string {
	return moduleName + "#run"
}

func buildExecutionTraceID(runTrace []string) string {
	if len(runTrace) == 0 {
		return ""
	}

	payload := strings.Builder{}
	payload.WriteString("run-trace")
	payload.WriteString("|")
	for _, taskID := range runTrace {
		payload.WriteString(strconv.Itoa(len(taskID)))
		payload.WriteString(":")
		payload.WriteString(taskID)
		payload.WriteString("|")
	}
	return fingerprint.HashString(payload.String())
}

func newTraceID() string {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err == nil {
		return "trace_" + hex.EncodeToString(entropy)
	}
	return "trace_" + fingerprint.HashString(fmt.Sprintf("%d:%d", time.Now().UnixNano(), os.Getpid()))
}

func normalizeSourcePath(path string) string {
	return filepath.ToSlash(strings.TrimSpace(path))
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

func (s *Service) logStageStart(traceID string, stage contracts.CompileStage, executionTraceID string) {
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"compile_stage_started",
		slog.String("trace_id", traceID),
		slog.String("execution_trace_id", executionTraceID),
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.String("diagnostic_id", ""),
		slog.String("diagnostic_kind", ""),
		slog.String("source_path", ""),
		slog.Int("line", 0),
		slog.Int("column", 0),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", 0),
		slog.String("error_kind", ""),
	)
}

func (s *Service) logStageEnd(traceID string, stage contracts.CompileStage, executionTraceID string, duration time.Duration) {
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"compile_stage_completed",
		slog.String("trace_id", traceID),
		slog.String("execution_trace_id", executionTraceID),
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.String("diagnostic_id", ""),
		slog.String("diagnostic_kind", ""),
		slog.String("source_path", ""),
		slog.Int("line", 0),
		slog.Int("column", 0),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("error_kind", ""),
	)
}

func (s *Service) logStageFailure(traceID string, stage contracts.CompileStage, executionTraceID string, duration time.Duration, errorKind contracts.DiagnosticKind, err error) {
	logging.Event(
		s.logger,
		slog.LevelError,
		"compile_stage_failed",
		slog.String("trace_id", traceID),
		slog.String("execution_trace_id", executionTraceID),
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.String("diagnostic_id", ""),
		slog.String("diagnostic_kind", ""),
		slog.String("source_path", ""),
		slog.Int("line", 0),
		slog.Int("column", 0),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("error_kind", string(errorKind)),
		slog.String("error", err.Error()),
	)
}

func (s *Service) logTaskCacheEvent(traceID string, executionTraceID string, taskID string, cacheKey string, cacheHit bool, invalidationReason contracts.TtlInvalidationReason, errorKind contracts.DiagnosticKind, duration time.Duration) {
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"task_cache_processed",
		slog.String("trace_id", traceID),
		slog.String("execution_trace_id", executionTraceID),
		slog.String("compile_stage", string(contracts.CompileStageCache)),
		slog.String("task_id", taskID),
		slog.String("cache_key", cacheKey),
		slog.Bool("cache_hit", cacheHit),
		slog.String("invalidation_reason", string(invalidationReason)),
		slog.String("diagnostic_id", ""),
		slog.String("diagnostic_kind", ""),
		slog.String("source_path", ""),
		slog.Int("line", 0),
		slog.Int("column", 0),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("error_kind", string(errorKind)),
	)
}

func (s *Service) logDiagnostics(traceID string, stage contracts.CompileStage, sourcePath string, diagnostics []diagnostic.Diagnostic) {
	for _, issue := range diagnostics {
		s.logDiagnosticEvent(traceID, stage, sourcePath, issue)
	}
}

func (s *Service) logDiagnosticEvent(traceID string, stage contracts.CompileStage, sourcePath string, issue diagnostic.Diagnostic) {
	normalizedSourcePath := normalizeSourcePath(sourcePath)
	logging.Event(
		s.logger,
		slog.LevelInfo,
		"diagnostic_reported",
		slog.String("trace_id", traceID),
		slog.String("execution_trace_id", ""),
		slog.String("compile_stage", string(stage)),
		slog.String("task_id", ""),
		slog.String("cache_key", ""),
		slog.Bool("cache_hit", false),
		slog.String("invalidation_reason", ""),
		slog.String("diagnostic_id", issue.DeterministicID(normalizedSourcePath)),
		slog.String("diagnostic_kind", string(issue.Kind)),
		slog.String("source_path", normalizedSourcePath),
		slog.Int("line", issue.Line),
		slog.Int("column", issue.Column),
		slog.Int("worker_id", 0),
		slog.Int64("duration_ms", 0),
		slog.String("error_kind", string(issue.Kind)),
		slog.String("message", issue.Message),
	)
}
