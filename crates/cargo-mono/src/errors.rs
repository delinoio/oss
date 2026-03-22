use std::{fmt, io};

use thiserror::Error;

pub type Result<T> = std::result::Result<T, CargoMonoError>;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorKind {
    Internal,
    InvalidInput,
    Git,
    Cargo,
    Conflict,
}

impl ErrorKind {
    pub fn exit_code(self) -> i32 {
        match self {
            Self::Internal => 1,
            Self::InvalidInput => 2,
            Self::Git => 3,
            Self::Cargo => 4,
            Self::Conflict => 5,
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Self::Internal => "internal",
            Self::InvalidInput => "invalid-input",
            Self::Git => "git",
            Self::Cargo => "cargo",
            Self::Conflict => "conflict",
        }
    }
}

#[derive(Debug, Error)]
#[error("{message}")]
pub struct CargoMonoError {
    pub kind: ErrorKind,
    pub message: String,
}

impl CargoMonoError {
    pub fn new(kind: ErrorKind, message: impl Into<String>) -> Self {
        Self {
            kind,
            message: message.into(),
        }
    }

    pub fn with_hint(kind: ErrorKind, summary: impl AsRef<str>, hint: impl AsRef<str>) -> Self {
        Self::new(kind, message_with_hint(summary, hint))
    }

    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Internal, message)
    }

    pub fn invalid_input(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::InvalidInput, message)
    }

    pub fn git(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Git, message)
    }

    pub fn cargo(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Cargo, message)
    }

    pub fn conflict(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Conflict, message)
    }

    pub fn exit_code(&self) -> i32 {
        self.kind.exit_code()
    }
}

impl From<io::Error> for CargoMonoError {
    fn from(value: io::Error) -> Self {
        Self::with_hint(
            ErrorKind::Internal,
            format!("I/O operation failed: {value}"),
            "Verify paths and permissions, then retry the command.",
        )
    }
}

impl From<cargo_metadata::Error> for CargoMonoError {
    fn from(value: cargo_metadata::Error) -> Self {
        Self::with_hint(
            ErrorKind::Cargo,
            format!("Failed to load workspace metadata via cargo: {value}"),
            "Run `cargo metadata` in this workspace to reproduce and inspect the root cause.",
        )
    }
}

impl From<serde_json::Error> for CargoMonoError {
    fn from(value: serde_json::Error) -> Self {
        Self::with_hint(
            ErrorKind::Internal,
            format!("Failed to parse JSON content: {value}"),
            "Check the JSON payload for syntax issues near the reported location.",
        )
    }
}

impl From<semver::Error> for CargoMonoError {
    fn from(value: semver::Error) -> Self {
        Self::with_hint(
            ErrorKind::InvalidInput,
            format!("Invalid semantic version: {value}"),
            "Use a valid SemVer value such as `1.2.3` or `1.2.3-rc.1`.",
        )
    }
}

impl From<toml_edit::TomlError> for CargoMonoError {
    fn from(value: toml_edit::TomlError) -> Self {
        Self::with_hint(
            ErrorKind::Internal,
            format!("Failed to parse TOML document: {value}"),
            "Check TOML syntax in Cargo manifests and retry.",
        )
    }
}

impl From<CargoMonoError> for io::Error {
    fn from(value: CargoMonoError) -> Self {
        io::Error::other(value.to_string())
    }
}

pub fn message_with_hint(summary: impl AsRef<str>, hint: impl AsRef<str>) -> String {
    format!("{} Hint: {}", summary.as_ref(), hint.as_ref())
}

pub fn with_context<E: fmt::Display>(
    kind: ErrorKind,
    context: &str,
    error: E,
    hint: &str,
) -> CargoMonoError {
    CargoMonoError::with_hint(kind, format!("{context}: {error}"), hint)
}

#[cfg(test)]
mod tests {
    use super::{message_with_hint, CargoMonoError, ErrorKind};

    #[test]
    fn error_kind_labels_are_stable() {
        assert_eq!(ErrorKind::Internal.label(), "internal");
        assert_eq!(ErrorKind::InvalidInput.label(), "invalid-input");
        assert_eq!(ErrorKind::Git.label(), "git");
        assert_eq!(ErrorKind::Cargo.label(), "cargo");
        assert_eq!(ErrorKind::Conflict.label(), "conflict");
    }

    #[test]
    fn message_with_hint_uses_single_line_contract() {
        let message = message_with_hint("Unable to read manifest.", "Check file permissions.");
        assert_eq!(
            message,
            "Unable to read manifest. Hint: Check file permissions."
        );
    }

    #[test]
    fn with_hint_formats_error_message() {
        let error = CargoMonoError::with_hint(
            ErrorKind::InvalidInput,
            "Invalid package selector.",
            "Run `cargo mono list`.",
        );
        assert_eq!(error.kind, ErrorKind::InvalidInput);
        assert_eq!(
            error.message,
            "Invalid package selector. Hint: Run `cargo mono list`."
        );
    }
}
