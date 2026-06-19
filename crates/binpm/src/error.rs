use std::{io, path::PathBuf};

use thiserror::Error;

pub type Result<T> = std::result::Result<T, BinpmError>;

#[derive(Debug, Error)]
pub enum BinpmError {
    #[error(
        "`{command}` is part of the binpm command surface but runtime behavior is not implemented \
         yet."
    )]
    NotImplemented { command: &'static str },
    #[error("Invalid source spec `{raw}`: {message}")]
    InvalidSourceSpec { raw: String, message: String },
    #[error("Invalid target key `{raw}`. Expected `<os>-<arch>-<libc>`.")]
    InvalidTargetKey { raw: String },
    #[error("Unsupported target component `{raw}` for {component}.")]
    UnsupportedTargetComponent {
        component: &'static str,
        raw: String,
    },
    #[error("Failed to determine the current working directory: {0}")]
    CurrentDirectory(#[source] io::Error),
    #[error("Failed to write `{path}`: {source}")]
    WriteFile {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Refusing to overwrite existing manifest `{path}`. Use `--force` to replace it.")]
    ManifestExists { path: PathBuf },
    #[error("Failed to read `{path}`: {source}")]
    ReadFile {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to determine binpm global home. Set BINPM_HOME, HOME, or USERPROFILE.")]
    MissingGlobalHome,
    #[error("Invalid {name}: binpm global home must be an absolute path, got `{path}`.")]
    InvalidGlobalHome { name: &'static str, path: PathBuf },
}

impl BinpmError {
    pub fn exit_code(&self) -> i32 {
        match self {
            Self::NotImplemented { .. } => 2,
            Self::InvalidSourceSpec { .. }
            | Self::InvalidTargetKey { .. }
            | Self::UnsupportedTargetComponent { .. }
            | Self::ManifestExists { .. } => 2,
            Self::CurrentDirectory(_)
            | Self::WriteFile { .. }
            | Self::ReadFile { .. }
            | Self::MissingGlobalHome
            | Self::InvalidGlobalHome { .. } => 1,
        }
    }
}
