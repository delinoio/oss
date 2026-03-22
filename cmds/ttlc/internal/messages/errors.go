package messages

import "fmt"

type ErrorID string

const (
	ErrorUnsupportedLogLevel        ErrorID = "unsupported_log_level"
	ErrorParseRunArgsDecode         ErrorID = "parse_run_args_decode"
	ErrorParseRunArgsExpectedObject ErrorID = "parse_run_args_expected_object"
	ErrorParseRunArgsTrailingTokens ErrorID = "parse_run_args_trailing_tokens"
	ErrorResolveCwd                 ErrorID = "resolve_cwd"
	ErrorResolveWorkspaceRoot       ErrorID = "resolve_workspace_root"
	ErrorResolveEntryPath           ErrorID = "resolve_entry_path"
	ErrorEntryEscapesWorkspace      ErrorID = "entry_escapes_workspace"
	ErrorEntryFileExtension         ErrorID = "entry_file_extension"
	ErrorResolveOutDirPath          ErrorID = "resolve_out_dir_path"
	ErrorOutDirEscapesWorkspace     ErrorID = "out_dir_escapes_workspace"
	ErrorResolveCacheDBPath         ErrorID = "resolve_cache_db_path"
	ErrorCacheDBEscapesWorkspace    ErrorID = "cache_db_escapes_workspace"
	ErrorSymlinkDepthExceeded       ErrorID = "symlink_depth_exceeded"
	ErrorResolveAbsolutePath        ErrorID = "resolve_absolute_path"
	ErrorEvaluateSymlinks           ErrorID = "evaluate_symlinks"
	ErrorReadSymlink                ErrorID = "read_symlink"
	ErrorStatPath                   ErrorID = "stat_path"
	ErrorImportPathEmpty            ErrorID = "import_path_empty"
	ErrorResolveImportPath          ErrorID = "resolve_import_path"
	ErrorResolveImportWorkspaceRoot ErrorID = "resolve_import_workspace_root"
	ErrorImportEscapesWorkspace     ErrorID = "import_escapes_workspace"
	ErrorRootTaskFingerprintMissing ErrorID = "root_task_fingerprint_missing"
	ErrorBuildRunParameterHash      ErrorID = "build_run_parameter_hash"
	ErrorOpenCacheStore             ErrorID = "open_cache_store"
	ErrorAnalyzeTaskCacheState      ErrorID = "analyze_task_cache_state"
	ErrorReadCachedTaskState        ErrorID = "read_cached_task_state"
	ErrorBuildRunProgram            ErrorID = "build_run_program"
	ErrorGenerateRunnerSource       ErrorID = "generate_runner_source"
	ErrorExecuteGeneratedRunner     ErrorID = "execute_generated_runner"
	ErrorUpsertRunCacheRecord       ErrorID = "upsert_run_cache_record"
	ErrorEmitGoSource               ErrorID = "emit_go_source"
	ErrorUpsertTaskCacheRecord      ErrorID = "upsert_task_cache_record"
	ErrorDeleteCorruptedCacheState  ErrorID = "delete_corrupted_cache_state"
	ErrorResolveCompilerPaths       ErrorID = "resolve_compiler_paths"
	ErrorReadEntrySourceFile        ErrorID = "read_entry_source_file"
	ErrorUnsupportedRunArgumentType ErrorID = "unsupported_run_argument_type"
)

var errorTemplates = map[ErrorID]string{
	ErrorUnsupportedLogLevel:        "Unsupported log level %q. Use one of: debug, info, warn, error.",
	ErrorParseRunArgsDecode:         "Invalid --args value. Provide a single JSON object (for example: {\"target\":\"web\"}).",
	ErrorParseRunArgsExpectedObject: "Invalid --args value: expected a JSON object (for example: {\"target\":\"web\"}).",
	ErrorParseRunArgsTrailingTokens: "Invalid --args value: remove trailing tokens after the JSON object.",
	ErrorResolveCwd:                 "Could not resolve the current working directory.",
	ErrorResolveWorkspaceRoot:       "Could not resolve the workspace root path.",
	ErrorResolveEntryPath:           "Could not resolve the entry path %q.",
	ErrorEntryEscapesWorkspace:      "Entry path %q escapes the workspace root.",
	ErrorEntryFileExtension:         "Entry path %q must use the .ttl extension.",
	ErrorResolveOutDirPath:          "Could not resolve the output directory path %q.",
	ErrorOutDirEscapesWorkspace:     "Output directory path %q escapes the workspace root.",
	ErrorResolveCacheDBPath:         "Could not resolve the cache database path.",
	ErrorCacheDBEscapesWorkspace:    "Cache database path %q escapes the workspace root.",
	ErrorSymlinkDepthExceeded:       "Could not resolve path %q because symlink depth exceeded the safety limit.",
	ErrorResolveAbsolutePath:        "Could not resolve absolute path for %q.",
	ErrorEvaluateSymlinks:           "Could not evaluate symlinks for %q.",
	ErrorReadSymlink:                "Could not read symlink %q.",
	ErrorStatPath:                   "Could not inspect path %q.",
	ErrorImportPathEmpty:            "Import path is empty. Provide a non-empty import path.",
	ErrorResolveImportPath:          "Could not resolve import path %q.",
	ErrorResolveImportWorkspaceRoot: "Could not resolve workspace root while resolving imports.",
	ErrorImportEscapesWorkspace:     "Import path %q escapes the workspace root.",
	ErrorRootTaskFingerprintMissing: "Root task fingerprint for %q is missing. Re-run check/build to regenerate fingerprints.",
	ErrorBuildRunParameterHash:      "Could not compute the run parameter hash.",
	ErrorOpenCacheStore:             "Could not open the TTL cache store at %q.",
	ErrorAnalyzeTaskCacheState:      "Could not analyze cache state for task %q.",
	ErrorReadCachedTaskState:        "Could not read cached state for task %q.",
	ErrorBuildRunProgram:            "Could not build the runtime program for task %q.",
	ErrorGenerateRunnerSource:       "Could not generate runner source code.",
	ErrorExecuteGeneratedRunner:     "Could not execute the generated runner.",
	ErrorUpsertRunCacheRecord:       "Could not update run cache record for task %q.",
	ErrorEmitGoSource:               "Could not emit Go source from TTL module %q.",
	ErrorUpsertTaskCacheRecord:      "Could not update cache record for task %q.",
	ErrorDeleteCorruptedCacheState:  "Could not remove corrupted cache state for task %q.",
	ErrorResolveCompilerPaths:       "Could not resolve compiler input/output paths.",
	ErrorReadEntrySourceFile:        "Could not read the entry source file %q.",
	ErrorUnsupportedRunArgumentType: "Run argument value uses unsupported runtime type %T.",
}

func FormatError(id ErrorID, args ...any) string {
	template, ok := errorTemplates[id]
	if !ok {
		return fmt.Sprintf("Internal error template %q is not defined.", id)
	}
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}

func NewError(id ErrorID, args ...any) error {
	return fmt.Errorf("%s", FormatError(id, args...))
}

func WrapError(id ErrorID, cause error, args ...any) error {
	if cause == nil {
		return NewError(id, args...)
	}
	return fmt.Errorf("%s: %w", FormatError(id, args...), cause)
}
