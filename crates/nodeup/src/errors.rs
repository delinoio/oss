use std::{fmt, io};

use serde::Serialize;
use thiserror::Error;

pub type Result<T> = std::result::Result<T, NodeupError>;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ErrorKind {
    Internal,
    InvalidInput,
    UnsupportedPlatform,
    Network,
    NotFound,
    Conflict,
    NotImplemented,
}

impl ErrorKind {
    pub fn exit_code(self) -> i32 {
        match self {
            Self::Internal => 1,
            Self::InvalidInput => 2,
            Self::UnsupportedPlatform => 3,
            Self::Network => 4,
            Self::NotFound => 5,
            Self::Conflict => 6,
            Self::NotImplemented => 7,
        }
    }
}

#[derive(Debug, Error)]
#[error("{message}")]
pub struct NodeupError {
    pub kind: ErrorKind,
    pub message: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct NodeupErrorEnvelope {
    pub kind: ErrorKind,
    pub message: String,
    pub exit_code: i32,
}

fn default_hint_for_kind(kind: ErrorKind) -> &'static str {
    match kind {
        ErrorKind::Internal => {
            "Retry the command. If it still fails, run again with `RUST_LOG=nodeup=debug` and \
             inspect the logs."
        }
        ErrorKind::InvalidInput => {
            "Check the command arguments with `nodeup --help` and try again."
        }
        ErrorKind::UnsupportedPlatform => {
            "Run this command on a supported macOS/Linux/Windows x64/arm64 host."
        }
        ErrorKind::Network => {
            "Check your network connection and retry. If it keeps failing, run again with \
             `RUST_LOG=nodeup=debug`."
        }
        ErrorKind::NotFound => {
            "Verify the referenced selector, runtime, path, or command and retry."
        }
        ErrorKind::Conflict => "Resolve the conflicting state and run the command again.",
        ErrorKind::NotImplemented => "Use a supported command or selector instead.",
    }
}

fn sanitized_url(url: &reqwest::Url) -> String {
    let mut sanitized = url.clone();
    sanitized.set_query(None);
    sanitized.set_fragment(None);
    sanitized.to_string()
}

pub fn sanitize_url_text(raw: &str) -> String {
    match reqwest::Url::parse(raw) {
        Ok(url) => sanitized_url(&url),
        Err(_) => raw.to_string(),
    }
}

fn reqwest_error_classification(error: &reqwest::Error) -> &'static str {
    if error.is_timeout() {
        "timeout"
    } else if error.is_connect() {
        "connect"
    } else if error.is_status() {
        "http-status"
    } else if error.is_decode() {
        "decode"
    } else if error.is_request() {
        "request"
    } else if error.is_body() {
        "body"
    } else {
        "other"
    }
}

fn format_error_message(
    kind: ErrorKind,
    cause: impl Into<String>,
    hint: impl Into<String>,
) -> String {
    let cause = cause.into();
    let normalized_cause = {
        let trimmed = cause.trim();
        if trimmed.is_empty() {
            "An unexpected error occurred".to_string()
        } else {
            trimmed.trim_end_matches('.').to_string()
        }
    };

    let hint = hint.into();
    let normalized_hint = {
        let trimmed = hint.trim();
        if trimmed.is_empty() {
            default_hint_for_kind(kind).to_string()
        } else {
            trimmed.to_string()
        }
    };

    format!("{normalized_cause}. Hint: {normalized_hint}")
}

impl NodeupError {
    pub fn new(kind: ErrorKind, cause: impl Into<String>) -> Self {
        Self::with_hint(kind, cause, default_hint_for_kind(kind))
    }

    pub fn with_hint(kind: ErrorKind, cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self {
            kind,
            message: format_error_message(kind, cause, hint),
        }
    }

    pub fn internal(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::Internal, cause)
    }

    pub fn internal_with_hint(cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self::with_hint(ErrorKind::Internal, cause, hint)
    }

    pub fn invalid_input(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::InvalidInput, cause)
    }

    pub fn invalid_input_with_hint(cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self::with_hint(ErrorKind::InvalidInput, cause, hint)
    }

    pub fn unsupported_platform(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::UnsupportedPlatform, cause)
    }

    pub fn unsupported_platform_with_hint(
        cause: impl Into<String>,
        hint: impl Into<String>,
    ) -> Self {
        Self::with_hint(ErrorKind::UnsupportedPlatform, cause, hint)
    }

    pub fn network(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::Network, cause)
    }

    pub fn network_with_hint(cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self::with_hint(ErrorKind::Network, cause, hint)
    }

    pub fn not_found(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::NotFound, cause)
    }

    pub fn not_found_with_hint(cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self::with_hint(ErrorKind::NotFound, cause, hint)
    }

    pub fn conflict(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::Conflict, cause)
    }

    pub fn conflict_with_hint(cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self::with_hint(ErrorKind::Conflict, cause, hint)
    }

    pub fn not_implemented(cause: impl Into<String>) -> Self {
        Self::new(ErrorKind::NotImplemented, cause)
    }

    pub fn not_implemented_with_hint(cause: impl Into<String>, hint: impl Into<String>) -> Self {
        Self::with_hint(ErrorKind::NotImplemented, cause, hint)
    }

    pub fn exit_code(&self) -> i32 {
        self.kind.exit_code()
    }

    pub fn json_envelope(&self) -> NodeupErrorEnvelope {
        NodeupErrorEnvelope {
            kind: self.kind,
            message: self.message.clone(),
            exit_code: self.exit_code(),
        }
    }
}

impl From<io::Error> for NodeupError {
    fn from(value: io::Error) -> Self {
        let io_kind = format!("{:?}", value.kind());
        let raw_os_error = value
            .raw_os_error()
            .map(|code| code.to_string())
            .unwrap_or_else(|| "none".to_string());
        Self::internal_with_hint(
            format!(
                "I/O operation failed: {value} (io_kind={io_kind}, raw_os_error={raw_os_error})"
            ),
            "Check file permissions and disk availability, then retry the command.",
        )
    }
}

impl From<reqwest::Error> for NodeupError {
    fn from(value: reqwest::Error) -> Self {
        let classification = reqwest_error_classification(&value);
        let status = value
            .status()
            .map(|status| status.as_u16().to_string())
            .unwrap_or_else(|| "none".to_string());
        let url = value
            .url()
            .map(sanitized_url)
            .unwrap_or_else(|| "none".to_string());
        if value.is_timeout() || value.is_connect() {
            return Self::network_with_hint(
                format!(
                    "Network request failed: {value} (classification={classification}, \
                     status={status}, url={url})"
                ),
                "Check your internet connection and retry the command.",
            );
        }
        Self::internal_with_hint(
            format!(
                "HTTP client failed: {value} (classification={classification}, status={status}, \
                 url={url})"
            ),
            "Retry the command. If it still fails, run again with `RUST_LOG=nodeup=debug`.",
        )
    }
}

impl From<serde_json::Error> for NodeupError {
    fn from(value: serde_json::Error) -> Self {
        Self::internal_with_hint(
            format!("JSON operation failed: {value}"),
            "Validate the JSON payload and retry. If it still fails, run with \
             `RUST_LOG=nodeup=debug`.",
        )
    }
}

impl From<toml::de::Error> for NodeupError {
    fn from(value: toml::de::Error) -> Self {
        Self::internal_with_hint(
            format!("Failed to decode TOML content: {value}"),
            "Fix the TOML syntax and retry the command.",
        )
    }
}

impl From<toml::ser::Error> for NodeupError {
    fn from(value: toml::ser::Error) -> Self {
        Self::internal_with_hint(
            format!("Failed to encode TOML content: {value}"),
            "Check the generated configuration values and retry.",
        )
    }
}

impl From<semver::Error> for NodeupError {
    fn from(value: semver::Error) -> Self {
        Self::invalid_input_with_hint(
            format!("Invalid semantic version: {value}"),
            "Use a selector like `22.1.0`, `v22.1.0`, `lts`, `current`, or `latest`.",
        )
    }
}

impl From<NodeupError> for io::Error {
    fn from(value: NodeupError) -> Self {
        io::Error::other(value.to_string())
    }
}

pub fn with_context<E: fmt::Display>(kind: ErrorKind, context: &str, error: E) -> NodeupError {
    NodeupError::with_hint(
        kind,
        format!("{context}: {error}"),
        default_hint_for_kind(kind),
    )
}
