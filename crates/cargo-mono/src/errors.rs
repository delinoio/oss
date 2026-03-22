use std::io;

use thiserror::Error;

pub type Result<T> = std::result::Result<T, CargoMonoError>;
const MAX_CONTEXT_VALUE_CHARS: usize = 160;

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
        Self::with_details(kind, summary, Vec::new(), hint)
    }

    pub fn with_details(
        kind: ErrorKind,
        summary: impl AsRef<str>,
        context: Vec<(&str, String)>,
        hint: impl AsRef<str>,
    ) -> Self {
        Self::new(kind, message_with_details(summary, &context, hint))
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
        Self::with_details(
            ErrorKind::Internal,
            "I/O operation failed.",
            vec![("error", value.to_string())],
            "Verify paths and permissions, then retry the command.",
        )
    }
}

impl From<cargo_metadata::Error> for CargoMonoError {
    fn from(value: cargo_metadata::Error) -> Self {
        let working_directory = std::env::current_dir()
            .map(|path| path.display().to_string())
            .unwrap_or_else(|error| format!("<unavailable:{error}>"));

        Self::with_details(
            ErrorKind::Cargo,
            "Failed to load workspace metadata via cargo.",
            vec![
                ("working_directory", working_directory),
                (
                    "metadata_command",
                    "cargo metadata --format-version 1".to_string(),
                ),
                ("error", value.to_string()),
            ],
            "Run `cargo metadata` in this workspace to reproduce and inspect the root cause.",
        )
    }
}

impl From<serde_json::Error> for CargoMonoError {
    fn from(value: serde_json::Error) -> Self {
        Self::with_details(
            ErrorKind::Internal,
            "Failed to parse JSON content.",
            vec![
                ("error", value.to_string()),
                ("line", value.line().to_string()),
                ("column", value.column().to_string()),
            ],
            "Check the JSON payload for syntax issues near the reported location.",
        )
    }
}

impl From<semver::Error> for CargoMonoError {
    fn from(value: semver::Error) -> Self {
        Self::with_details(
            ErrorKind::InvalidInput,
            "Invalid semantic version.",
            vec![("error", value.to_string())],
            "Use a valid SemVer value such as `1.2.3` or `1.2.3-rc.1`.",
        )
    }
}

impl From<toml_edit::TomlError> for CargoMonoError {
    fn from(value: toml_edit::TomlError) -> Self {
        Self::with_details(
            ErrorKind::Internal,
            "Failed to parse TOML document.",
            vec![("error", value.to_string())],
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
    message_with_details(summary, &[], hint)
}

pub fn message_with_details(
    summary: impl AsRef<str>,
    context: &[(&str, String)],
    hint: impl AsRef<str>,
) -> String {
    let summary_line = normalize_line(summary.as_ref());
    let context_line = if context.is_empty() {
        "none".to_string()
    } else {
        context
            .iter()
            .map(|(key, value)| format!("{key}={}", normalize_context_value(value)))
            .collect::<Vec<_>>()
            .join(", ")
    };
    let hint_line = normalize_line(hint.as_ref());

    format!("Summary: {summary_line}\nContext: {context_line}\nHint: {hint_line}")
}

fn normalize_context_value(raw: &str) -> String {
    let normalized = normalize_line(raw);
    if normalized.is_empty() {
        return "n/a".to_string();
    }

    let mut chars = normalized.chars();
    let truncated = chars
        .by_ref()
        .take(MAX_CONTEXT_VALUE_CHARS)
        .collect::<String>();
    if chars.next().is_some() {
        return format!("{truncated}...(truncated)");
    }
    truncated
}

fn normalize_line(raw: &str) -> String {
    raw.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod tests {
    use super::{message_with_details, message_with_hint, CargoMonoError, ErrorKind};

    #[test]
    fn error_kind_labels_are_stable() {
        assert_eq!(ErrorKind::Internal.label(), "internal");
        assert_eq!(ErrorKind::InvalidInput.label(), "invalid-input");
        assert_eq!(ErrorKind::Git.label(), "git");
        assert_eq!(ErrorKind::Cargo.label(), "cargo");
        assert_eq!(ErrorKind::Conflict.label(), "conflict");
    }

    #[test]
    fn message_with_hint_uses_multiline_contract_with_empty_context() {
        let message = message_with_hint("Unable to read manifest.", "Check file permissions.");
        assert_eq!(
            message,
            "Summary: Unable to read manifest.\nContext: none\nHint: Check file permissions."
        );
    }

    #[test]
    fn message_with_details_formats_context_line() {
        let message = message_with_details(
            "Failed to run command.",
            &[
                ("command", "git status --porcelain".to_string()),
                ("status", "exit status: 1".to_string()),
            ],
            "Run the command directly to inspect stderr.",
        );
        assert_eq!(
            message,
            "Summary: Failed to run command.\nContext: command=git status --porcelain, \
             status=exit status: 1\nHint: Run the command directly to inspect stderr."
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
            "Summary: Invalid package selector.\nContext: none\nHint: Run `cargo mono list`."
        );
    }
}
