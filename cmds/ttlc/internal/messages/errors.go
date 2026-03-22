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
	ErrorUnsupportedLogLevel:        "Invalid log level %q. Expected one of: debug, info, warn, error.",
	ErrorParseRunArgsDecode:         "Invalid --args payload. Expected a single JSON object (for example: {\"target\":\"web\"}).",
	ErrorParseRunArgsExpectedObject: "Invalid --args payload. Expected a JSON object, got %s.",
	ErrorParseRunArgsTrailingTokens: "Invalid --args payload. Expected exactly one JSON object, but found trailing content (%s).",
	ErrorResolveCwd:                 "Failed to resolve the current working directory.",
	ErrorResolveWorkspaceRoot:       "Failed to resolve workspace root from cwd %q.",
	ErrorResolveEntryPath:           "Failed to resolve entry path %q within workspace root %q.",
	ErrorEntryEscapesWorkspace:      "Entry path %q escapes workspace root %q.",
	ErrorEntryFileExtension:         "Entry path %q must use the .ttl extension (detected extension %q).",
	ErrorResolveOutDirPath:          "Failed to resolve output directory %q within workspace root %q.",
	ErrorOutDirEscapesWorkspace:     "Output directory %q escapes workspace root %q.",
	ErrorResolveCacheDBPath:         "Failed to resolve cache database path %q.",
	ErrorCacheDBEscapesWorkspace:    "Cache database path %q escapes workspace root %q.",
	ErrorSymlinkDepthExceeded:       "Failed to resolve path %q because symlink depth exceeded the safety limit (%d).",
	ErrorResolveAbsolutePath:        "Failed to resolve absolute path for %q.",
	ErrorEvaluateSymlinks:           "Failed to evaluate symlinks for path %q.",
	ErrorReadSymlink:                "Failed to read symlink %q.",
	ErrorStatPath:                   "Failed to inspect path %q.",
	ErrorImportPathEmpty:            "Import path is empty. Provide a non-empty import path.",
	ErrorResolveImportPath:          "Failed to resolve import path %q from file %q.",
	ErrorResolveImportWorkspaceRoot: "Failed to resolve workspace root %q while resolving imports.",
	ErrorImportEscapesWorkspace:     "Import path %q escapes workspace root %q.",
	ErrorRootTaskFingerprintMissing: "Missing root task fingerprint for task %q. Re-run check/build to regenerate fingerprints.",
	ErrorBuildRunParameterHash:      "Failed to compute run parameter hash for task %q.",
	ErrorOpenCacheStore:             "Failed to open TTL cache store at %q.",
	ErrorAnalyzeTaskCacheState:      "Failed to analyze cache state for module %q, task %q, cache key %q.",
	ErrorReadCachedTaskState:        "Failed to read cached state for module %q, task %q, cache key %q.",
	ErrorBuildRunProgram:            "Failed to build runtime program for task %q.",
	ErrorGenerateRunnerSource:       "Failed to generate runner source for task %q.",
	ErrorExecuteGeneratedRunner:     "Failed to execute generated runner for task %q in out-dir %q.",
	ErrorUpsertRunCacheRecord:       "Failed to update run cache record for module %q, task %q, cache key %q.",
	ErrorEmitGoSource:               "Failed to emit Go source for TTL module %q.",
	ErrorUpsertTaskCacheRecord:      "Failed to update cache record for module %q, task %q, cache key %q.",
	ErrorDeleteCorruptedCacheState:  "Failed to remove corrupted cache state for module %q and task %q.",
	ErrorResolveCompilerPaths:       "Failed to resolve compiler paths for entry %q and out-dir %q.",
	ErrorReadEntrySourceFile:        "Failed to read entry source file %q.",
	ErrorUnsupportedRunArgumentType: "Unsupported run argument runtime type %T while building a deterministic hash.",
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
