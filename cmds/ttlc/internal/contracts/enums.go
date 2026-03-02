package contracts

type TtlCommand string

const (
	TtlCommandBuild   TtlCommand = "build"
	TtlCommandCheck   TtlCommand = "check"
	TtlCommandExplain TtlCommand = "explain"
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

type DiagnosticKind string

const (
	DiagnosticKindSyntaxError       DiagnosticKind = "syntax_error"
	DiagnosticKindTypeError         DiagnosticKind = "type_error"
	DiagnosticKindUnsupportedImport DiagnosticKind = "unsupported_imports"
	DiagnosticKindPathViolation     DiagnosticKind = "path_violation"
	DiagnosticKindCycleError        DiagnosticKind = "cycle_error"
	DiagnosticKindIOError           DiagnosticKind = "io_error"
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
)
