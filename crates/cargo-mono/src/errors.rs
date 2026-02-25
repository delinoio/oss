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
        Self::internal(format!("I/O error: {value}"))
    }
}

impl From<cargo_metadata::Error> for CargoMonoError {
    fn from(value: cargo_metadata::Error) -> Self {
        Self::cargo(format!("cargo metadata error: {value}"))
    }
}

impl From<serde_json::Error> for CargoMonoError {
    fn from(value: serde_json::Error) -> Self {
        Self::internal(format!("JSON error: {value}"))
    }
}

impl From<semver::Error> for CargoMonoError {
    fn from(value: semver::Error) -> Self {
        Self::invalid_input(format!("Invalid semantic version: {value}"))
    }
}

impl From<toml_edit::TomlError> for CargoMonoError {
    fn from(value: toml_edit::TomlError) -> Self {
        Self::internal(format!("TOML error: {value}"))
    }
}

impl From<CargoMonoError> for io::Error {
    fn from(value: CargoMonoError) -> Self {
        io::Error::other(value.to_string())
    }
}

pub fn with_context<E: fmt::Display>(kind: ErrorKind, context: &str, error: E) -> CargoMonoError {
    CargoMonoError::new(kind, format!("{context}: {error}"))
}
