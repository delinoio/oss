use std::fmt;

use serde::Serialize;

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Code {
    PnportManifestMissing,
    PnportManifestInvalid,
    PnportResolutionFailed,
    PnportFilesystemConflict,
    PnportUnsupportedOperation,
    PnportInjectionFailed,
    PnportArchiveCorrupt,
    PnportCacheFailed,
    PnportGraphChanged,
    PnportCleanupFailed,
    PnportCommandNotFound,
    PnportCommandNotExecutable,
    PnportReady,
}

impl Code {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::PnportManifestMissing => "PNPORT_MANIFEST_MISSING",
            Self::PnportManifestInvalid => "PNPORT_MANIFEST_INVALID",
            Self::PnportResolutionFailed => "PNPORT_RESOLUTION_FAILED",
            Self::PnportFilesystemConflict => "PNPORT_FILESYSTEM_CONFLICT",
            Self::PnportUnsupportedOperation => "PNPORT_UNSUPPORTED_OPERATION",
            Self::PnportInjectionFailed => "PNPORT_INJECTION_FAILED",
            Self::PnportArchiveCorrupt => "PNPORT_ARCHIVE_CORRUPT",
            Self::PnportCacheFailed => "PNPORT_CACHE_FAILED",
            Self::PnportGraphChanged => "PNPORT_GRAPH_CHANGED",
            Self::PnportCleanupFailed => "PNPORT_CLEANUP_FAILED",
            Self::PnportCommandNotFound => "PNPORT_COMMAND_NOT_FOUND",
            Self::PnportCommandNotExecutable => "PNPORT_COMMAND_NOT_EXECUTABLE",
            Self::PnportReady => "PNPORT_READY",
        }
    }
}

/// Messages are static so parser/OS errors cannot disclose input contents.
#[derive(Clone, Debug)]
pub struct Error {
    pub code: Code,
    pub message: &'static str,
}
impl Error {
    pub const fn new(code: Code, message: &'static str) -> Self {
        Self { code, message }
    }

    pub fn exit_code(&self) -> i32 {
        match self.code {
            Code::PnportCommandNotFound => 127,
            Code::PnportCommandNotExecutable => 126,
            _ => 125,
        }
    }
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.code.as_str(), self.message)
    }
}
impl std::error::Error for Error {}
pub type Result<T> = std::result::Result<T, Error>;

pub fn manifest_error() -> Error {
    Error::new(
        Code::PnportManifestInvalid,
        "Invalid Yarn 4 PnP data; install dependencies with Yarn, then retry.",
    )
}
pub fn cache_error() -> Error {
    Error::new(
        Code::PnportCacheFailed,
        "Cannot access or publish the private cache; check permissions and available disk space.",
    )
}
pub fn archive_error() -> Error {
    Error::new(
        Code::PnportArchiveCorrupt,
        "The package archive is missing, corrupt, or unsafe; restore the Yarn installation and \
         restart.",
    )
}
