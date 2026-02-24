use std::{fmt, io};

use thiserror::Error;

pub type Result<T> = std::result::Result<T, NodeupError>;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
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

impl NodeupError {
    pub fn new(kind: ErrorKind, message: impl Into<String>) -> Self {
        Self {
            kind,
            message: message.into(),
        }
    }

    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Internal, message)
    }

    pub fn invalid_input(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::InvalidInput, message)
    }

    pub fn unsupported_platform(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::UnsupportedPlatform, message)
    }

    pub fn network(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Network, message)
    }

    pub fn not_found(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::NotFound, message)
    }

    pub fn conflict(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::Conflict, message)
    }

    pub fn not_implemented(message: impl Into<String>) -> Self {
        Self::new(ErrorKind::NotImplemented, message)
    }

    pub fn exit_code(&self) -> i32 {
        self.kind.exit_code()
    }
}

impl From<io::Error> for NodeupError {
    fn from(value: io::Error) -> Self {
        Self::internal(format!("I/O error: {value}"))
    }
}

impl From<reqwest::Error> for NodeupError {
    fn from(value: reqwest::Error) -> Self {
        if value.is_timeout() || value.is_connect() {
            return Self::network(format!("Network error: {value}"));
        }
        Self::internal(format!("HTTP client error: {value}"))
    }
}

impl From<serde_json::Error> for NodeupError {
    fn from(value: serde_json::Error) -> Self {
        Self::internal(format!("JSON error: {value}"))
    }
}

impl From<toml::de::Error> for NodeupError {
    fn from(value: toml::de::Error) -> Self {
        Self::internal(format!("TOML decode error: {value}"))
    }
}

impl From<toml::ser::Error> for NodeupError {
    fn from(value: toml::ser::Error) -> Self {
        Self::internal(format!("TOML encode error: {value}"))
    }
}

impl From<semver::Error> for NodeupError {
    fn from(value: semver::Error) -> Self {
        Self::invalid_input(format!("Invalid semantic version: {value}"))
    }
}

impl From<NodeupError> for io::Error {
    fn from(value: NodeupError) -> Self {
        io::Error::other(value.to_string())
    }
}

pub fn with_context<E: fmt::Display>(kind: ErrorKind, context: &str, error: E) -> NodeupError {
    NodeupError::new(kind, format!("{context}: {error}"))
}
