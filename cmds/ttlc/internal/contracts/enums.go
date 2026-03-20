package contracts

type TtlCommand string

const (
	TtlCommandBuild   TtlCommand = "build"
	TtlCommandCheck   TtlCommand = "check"
	TtlCommandExplain TtlCommand = "explain"
	TtlCommandRun     TtlCommand = "run"
)

type TtlSchemaVersion string

const (
	TtlSchemaVersionV1Alpha1 TtlSchemaVersion = "v1alpha1"
)

type TtlResponseStatus string

const (
	TtlResponseStatusOK     TtlResponseStatus = "ok"
	TtlResponseStatusFailed TtlResponseStatus = "failed"
)

type TtlCompileTarget string

const (
	TtlCompileTargetGoSource TtlCompileTarget = "go-source"
)

type TtlCacheBackend string

const (
	TtlCacheBackendSQLite TtlCacheBackend = "sqlite"
)

type TtlCoreType string

const (
	TtlCoreTypeVc             TtlCoreType = "vc"
	TtlCoreTypeResolvedVc     TtlCoreType = "resolved-vc"
	TtlCoreTypeOperationVc    TtlCoreType = "operation-vc"
	TtlCoreTypeTransientValue TtlCoreType = "transient-value"
	TtlCoreTypeState          TtlCoreType = "state"
)

type TtlInvalidationReason string

const (
	TtlInvalidationReasonNone                TtlInvalidationReason = "none"
	TtlInvalidationReasonCacheMiss           TtlInvalidationReason = "cache_miss"
	TtlInvalidationReasonInputContentChanged TtlInvalidationReason = "input_content_changed"
	TtlInvalidationReasonParameterChanged    TtlInvalidationReason = "parameter_changed"
	TtlInvalidationReasonEnvironmentChanged  TtlInvalidationReason = "environment_changed"
	TtlInvalidationReasonCacheCorruption     TtlInvalidationReason = "cache_corruption"
)

type DiagnosticKind string

const (
	DiagnosticKindSyntaxError       DiagnosticKind = "syntax_error"
	DiagnosticKindTypeError         DiagnosticKind = "type_error"
	DiagnosticKindUnsupportedImport DiagnosticKind = "unsupported_imports"
	DiagnosticKindPathViolation     DiagnosticKind = "path_violation"
	DiagnosticKindCycleError        DiagnosticKind = "cycle_error"
	DiagnosticKindIOError           DiagnosticKind = "io_error"
	DiagnosticKindCacheCorruption   DiagnosticKind = "cache_corruption"
	DiagnosticKindImportCycle       DiagnosticKind = "import_cycle"
	DiagnosticKindImportNotFound    DiagnosticKind = "import_not_found"
)

type CompileStage string

const (
	CompileStageLoad      CompileStage = "load"
	CompileStageLex       CompileStage = "lex"
	CompileStageParse     CompileStage = "parse"
	CompileStageTypecheck CompileStage = "typecheck"
	CompileStageGraph     CompileStage = "graph"
	CompileStageEmit      CompileStage = "emit"
	CompileStageCache     CompileStage = "cache"
	CompileStageRun       CompileStage = "run"
)
