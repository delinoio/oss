use std::{io, path::PathBuf};

use thiserror::Error;

pub type Result<T> = std::result::Result<T, WithWatchError>;

#[derive(Debug, Error)]
pub enum WithWatchError {
    #[error(
        "Provide a delegated utility, `--shell <expr>`, or `exec --input <glob>... -- <command> \
         ...`."
    )]
    MissingCommand,
    #[error("`--shell` cannot be combined with delegated argv or the `exec` subcommand.")]
    ConflictingModes,
    #[error("`--shell` requires a non-empty shell expression.")]
    EmptyShellExpression,
    #[error("`exec` requires a delegated command after `--`.")]
    MissingExecCommand,
    #[error(
        "No watch inputs could be inferred from the delegated command. Use `with-watch exec \
         --input <glob>... -- <command> [args...]`."
    )]
    NoWatchInputs,
    #[error("Failed to determine the current working directory: {0}")]
    CurrentDirectory(#[source] io::Error),
    #[error("Failed to parse shell expression: {message}")]
    ShellParse { message: String },
    #[error("Shell control-flow is out of scope for with-watch v1: {construct}")]
    UnsupportedShellConstruct { construct: String },
    #[error("Failed to compile glob pattern `{pattern}`: {message}")]
    InvalidGlob { pattern: String, message: String },
    #[error("Could not derive a watch anchor for `{path}`.")]
    MissingWatchAnchor { path: PathBuf },
    #[error("Failed to read metadata for `{path}`: {source}")]
    Metadata {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to read `{path}` while hashing: {source}")]
    HashRead {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to create a filesystem watcher: {0}")]
    WatcherCreate(#[source] notify::Error),
    #[error("Failed to watch `{path}` recursively: {source}")]
    WatchPath {
        path: PathBuf,
        #[source]
        source: notify::Error,
    },
    #[error("Failed to spawn `{command}`: {source}")]
    Spawn {
        command: String,
        #[source]
        source: io::Error,
    },
    #[error("Failed while waiting for `{command}`: {source}")]
    Wait {
        command: String,
        #[source]
        source: io::Error,
    },
    #[error("`--shell` execution is only supported on Unix-like platforms.")]
    UnsupportedShellPlatform,
}

impl WithWatchError {
    pub fn exit_code(&self) -> i32 {
        match self {
            Self::MissingCommand
            | Self::ConflictingModes
            | Self::EmptyShellExpression
            | Self::MissingExecCommand
            | Self::NoWatchInputs
            | Self::ShellParse { .. }
            | Self::UnsupportedShellConstruct { .. }
            | Self::InvalidGlob { .. }
            | Self::MissingWatchAnchor { .. }
            | Self::UnsupportedShellPlatform => 2,
            Self::CurrentDirectory(_)
            | Self::Metadata { .. }
            | Self::HashRead { .. }
            | Self::WatcherCreate(_)
            | Self::WatchPath { .. }
            | Self::Spawn { .. }
            | Self::Wait { .. } => 1,
        }
    }
}
