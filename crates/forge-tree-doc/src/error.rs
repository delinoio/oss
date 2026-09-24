use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum ErrorCode {
    InvalidJson,
    InvalidVersion,
    InvalidField,
    InvalidGeometry,
    InvalidReference,
    DuplicateIdentity,
    ResourceLimit,
    LayoutCycle,
    TextOverflow,
    FontUnavailable,
    RevisionConflict,
    SourceChanged,
    NotFound,
    UnsupportedEdit,
    UnsupportedPackage,
    InvalidPackage,
    StaleMetadata,
    Io,
    OutputExists,
    Cancelled,
    Timeout,
    RendererUnavailable,
    RendererFailed,
    Busy,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
pub struct Diagnostic {
    pub code: ErrorCode,
    pub path: String,
    pub message: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub node_key: Option<String>,
}
impl Diagnostic {
    pub fn new(code: ErrorCode, path: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            code,
            path: path.into(),
            message: message.into(),
            node_key: None,
        }
    }
}
impl std::fmt::Display for Diagnostic {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{:?}: {}", self.code, self.message)
    }
}
impl std::error::Error for Diagnostic {}
pub type Result<T> = std::result::Result<T, Diagnostic>;
pub fn error<T>(code: ErrorCode, path: &str, message: &str) -> Result<T> {
    Err(Diagnostic::new(code, path, message))
}
pub fn io_error(_: std::io::Error) -> Diagnostic {
    Diagnostic::new(ErrorCode::Io, "", "File operation failed")
}
