package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/delinoio/oss/cmds/ttlc/internal/cache"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/emitter"
	"github.com/delinoio/oss/cmds/ttlc/internal/fingerprint"
	"github.com/delinoio/oss/cmds/ttlc/internal/graph"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
	"github.com/delinoio/oss/cmds/ttlc/internal/logging"
	"github.com/delinoio/oss/cmds/ttlc/internal/parser"
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
	Tasks                 []Task                  `json:"tasks,omitempty"`
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
