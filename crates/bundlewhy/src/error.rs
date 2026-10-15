//! Stable bundlewhy error codes and the user-facing error type.

use std::fmt;

use crate::model::{Problem, ProblemSeverity};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ErrorCode {
    Usage,
    ProjectNotFound,
    BundlerNotDetected,
    BundlerAmbiguous,
    ConfigNotFound,
    ConfigAmbiguous,
    ConfigLoadFailed,
    BundlerNotInstalled,
    EngineVersionUnsupported,
    NodeNotFound,
    NodeVersionUnsupported,
    BuildFailed,
    CollectionFailed,
    UnsupportedBuild,
    SourceMapMissing,
    SourceUnreadable,
    SourceUnverified,
    MinifierNonDefault,
    MinificationDisabled,
    ProjectModified,
    RunCancelled,
    RunInterrupted,
    SchemaUnsupported,
    ResultInvalid,
    OutputDirInvalid,
    ServerBindFailed,
    BrowserOpenFailed,
    ExportFailed,
    AccessDenied,
    SourceStale,
    SourceFreshnessUnverified,
    SourceNotFound,
    ResultTooLarge,
}

impl ErrorCode {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Usage => "BW_USAGE",
            Self::ProjectNotFound => "BW_PROJECT_NOT_FOUND",
            Self::BundlerNotDetected => "BW_BUNDLER_NOT_DETECTED",
            Self::BundlerAmbiguous => "BW_BUNDLER_AMBIGUOUS",
            Self::ConfigNotFound => "BW_CONFIG_NOT_FOUND",
            Self::ConfigAmbiguous => "BW_CONFIG_AMBIGUOUS",
            Self::ConfigLoadFailed => "BW_CONFIG_LOAD_FAILED",
            Self::BundlerNotInstalled => "BW_BUNDLER_NOT_INSTALLED",
            Self::EngineVersionUnsupported => "BW_ENGINE_VERSION_UNSUPPORTED",
            Self::NodeNotFound => "BW_NODE_NOT_FOUND",
            Self::NodeVersionUnsupported => "BW_NODE_VERSION_UNSUPPORTED",
            Self::BuildFailed => "BW_BUILD_FAILED",
            Self::CollectionFailed => "BW_COLLECTION_FAILED",
            Self::UnsupportedBuild => "BW_UNSUPPORTED_BUILD",
            Self::SourceMapMissing => "BW_SOURCE_MAP_MISSING",
            Self::SourceUnreadable => "BW_SOURCE_UNREADABLE",
            Self::SourceUnverified => "BW_SOURCE_UNVERIFIED",
            Self::MinifierNonDefault => "BW_MINIFIER_NON_DEFAULT",
            Self::MinificationDisabled => "BW_MINIFICATION_DISABLED",
            Self::ProjectModified => "BW_PROJECT_MODIFIED",
            Self::RunCancelled => "BW_RUN_CANCELLED",
            Self::RunInterrupted => "BW_RUN_INTERRUPTED",
            Self::SchemaUnsupported => "BW_SCHEMA_UNSUPPORTED",
            Self::ResultInvalid => "BW_RESULT_INVALID",
            Self::OutputDirInvalid => "BW_OUTPUT_DIR_INVALID",
            Self::ServerBindFailed => "BW_SERVER_BIND_FAILED",
            Self::BrowserOpenFailed => "BW_BROWSER_OPEN_FAILED",
            Self::ExportFailed => "BW_EXPORT_FAILED",
            Self::AccessDenied => "BW_ACCESS_DENIED",
            Self::SourceStale => "BW_SOURCE_STALE",
            Self::SourceFreshnessUnverified => "BW_SOURCE_FRESHNESS_UNVERIFIED",
            Self::SourceNotFound => "BW_SOURCE_NOT_FOUND",
            Self::ResultTooLarge => "BW_RESULT_TOO_LARGE",
        }
    }

    /// Maps an adapter-reported code to the enum; unknown codes become build
    /// failures.
    pub fn from_adapter(code: &str) -> Self {
        match code {
            "BW_BUNDLER_NOT_INSTALLED" => Self::BundlerNotInstalled,
            "BW_CONFIG_LOAD_FAILED" => Self::ConfigLoadFailed,
            "BW_CONFIG_AMBIGUOUS" => Self::ConfigAmbiguous,
            "BW_COLLECTION_FAILED" => Self::CollectionFailed,
            "BW_ENGINE_VERSION_UNSUPPORTED" => Self::EngineVersionUnsupported,
            "BW_UNSUPPORTED_BUILD" => Self::UnsupportedBuild,
            _ => Self::BuildFailed,
        }
    }

    pub const fn exit_code(self) -> i32 {
        match self {
            Self::Usage | Self::BundlerAmbiguous | Self::ConfigAmbiguous => 2,
            Self::RunCancelled => 130,
            _ => 1,
        }
    }
}

impl fmt::Display for ErrorCode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// A user-facing failure with a stable code and a recovery instruction.
#[derive(Debug, Clone, thiserror::Error)]
#[error("{code}: {message}")]
pub struct BwError {
    pub code: ErrorCode,
    pub message: String,
    pub recovery: String,
}

impl BwError {
    pub fn new(code: ErrorCode, message: impl Into<String>, recovery: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            recovery: recovery.into(),
        }
    }

    pub fn problem(&self, severity: ProblemSeverity) -> Problem {
        Problem {
            code: self.code.as_str().to_owned(),
            severity,
            message: self.message.clone(),
            recovery: self.recovery.clone(),
        }
    }
}

pub fn problem(
    code: ErrorCode,
    severity: ProblemSeverity,
    message: impl Into<String>,
    recovery: impl Into<String>,
) -> Problem {
    BwError::new(code, message, recovery).problem(severity)
}

pub type BwResult<T> = Result<T, BwError>;

/// Problem codes whose messages can quote project output (code frames,
/// stderr tails) and therefore source code.
pub const OUTPUT_QUOTING_CODES: [&str; 3] = [
    "BW_BUILD_FAILED",
    "BW_COLLECTION_FAILED",
    "BW_CONFIG_LOAD_FAILED",
];

/// Generic replacement message for source-free results.
pub const SANITIZED_FAILURE_MESSAGE: &str = "The diagnostic build failed. Details were not saved \
                                             because sources are excluded; see the terminal \
                                             output of `bundlewhy analyze`.";

/// Replaces the message of a problem that may quote source code.
pub fn sanitize_problem(problem: &mut Problem) {
    if OUTPUT_QUOTING_CODES.contains(&problem.code.as_str()) {
        SANITIZED_FAILURE_MESSAGE.clone_into(&mut problem.message);
    }
}
