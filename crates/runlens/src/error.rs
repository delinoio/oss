use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ErrorCode {
    CommandFailed,
    InvalidInput,
    Unsupported,
    Incomplete,
    VerificationFailed,
    Timeout,
    Cancelled,
    SaveFailed,
    CleanupFailed,
    Internal,
}
impl ErrorCode {
    pub fn exit_code(self) -> i32 {
        match self {
            Self::CommandFailed => 1,
            Self::InvalidInput => 2,
            Self::Unsupported => 3,
            Self::Incomplete => 4,
            Self::VerificationFailed => 5,
            Self::Timeout => 6,
            Self::Cancelled => 7,
            Self::SaveFailed => 8,
            Self::CleanupFailed => 9,
            Self::Internal => 10,
        }
    }
}
#[derive(Debug, thiserror::Error)]
#[error("{code:?}: {message}")]
pub struct Error {
    pub code: ErrorCode,
    pub message: &'static str,
}
impl Error {
    pub const fn new(code: ErrorCode, message: &'static str) -> Self {
        Self { code, message }
    }

    pub const fn input(message: &'static str) -> Self {
        Self::new(ErrorCode::InvalidInput, message)
    }

    pub const fn storage() -> Self {
        Self::new(ErrorCode::Incomplete, "temporary metadata storage failed")
    }
}
pub type Result<T> = std::result::Result<T, Error>;
