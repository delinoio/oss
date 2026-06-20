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
    #[error("Invalid command name `{cmd}`: command names must be executable basenames.")]
    InvalidCommandName { cmd: String },
    #[error("Unsupported target component `{raw}` for {component}.")]
    UnsupportedTargetComponent {
        component: &'static str,
        raw: String,
    },
    #[error("Failed to build release HTTP client: {0}")]
    ReleaseHttpClient(#[source] reqwest::Error),
    #[error("Failed to look up release metadata: {0}")]
    ReleaseLookup(#[source] reqwest::Error),
    #[error("Failed to resolve release for `{package}`: {message}")]
    ReleaseNotFound { package: String, message: String },
    #[error("Failed to determine the current working directory: {0}")]
    CurrentDirectory(#[source] io::Error),
    #[error("Failed to write `{}`: {source}", path.display())]
    WriteFile {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error(
        "Refusing to overwrite existing manifest `{}`. Use `--force` to replace it.",
        path.display()
    )]
    ManifestExists { path: PathBuf },
    #[error("Failed to read `{}`: {source}", path.display())]
    ReadFile {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to create directory `{}`: {source}", path.display())]
    CreateDirectory {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to remove `{}`: {source}", path.display())]
    RemovePath {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to rename `{}` to `{}`: {source}", from.display(), to.display())]
    RenamePath {
        from: PathBuf,
        to: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Failed to serialize `{}`: {source}", path.display())]
    SerializeToml {
        path: PathBuf,
        #[source]
        source: toml::ser::Error,
    },
    #[error("Failed to parse `{}`: {source}", path.display())]
    ParseToml {
        path: PathBuf,
        #[source]
        source: toml::de::Error,
    },
    #[error("Unsupported {kind} version {version} in `{}`. Supported version is 1.", path.display())]
    UnsupportedStorageVersion {
        kind: &'static str,
        path: PathBuf,
        version: u8,
    },
    #[error("Frozen lockfile prevents modifying `{}`.", path.display())]
    FrozenLockfile { path: PathBuf },
    #[error("Frozen lockfile `{}` is stale for `{cmd}`.", path.display())]
    StaleLockfile { path: PathBuf, cmd: String },
    #[error("No local binpm.toml manifest found from `{}`.", start.display())]
    MissingManifest { start: PathBuf },
    #[error("Tool `{cmd}` is not declared in `{}`.", manifest.display())]
    MissingTool { cmd: String, manifest: PathBuf },
    #[error("No installable asset matched `{package}` for target `{target}`.")]
    AssetNotFound { package: String, target: String },
    #[error("Archive extraction is not implemented for `{asset}` yet.")]
    ArchiveExtractionNotImplemented { asset: String },
    #[error(
        "`--require-verified` requires upstream digest, checksum, or verified signature material \
         for `{package}`."
    )]
    VerificationRequired { package: String },
    #[error(
        "Manifest checksum_source `{checksum_source}` is declarative only and cannot be used as \
         verified checksum evidence."
    )]
    UnverifiedChecksumSourceOverride { checksum_source: String },
    #[error("SHA-256 mismatch for `{}`: expected {expected}, got {actual}.", path.display())]
    DigestMismatch {
        path: PathBuf,
        expected: String,
        actual: String,
    },
    #[error("Unsafe persisted URL `{url}`: {message}")]
    UnsafeUrl { url: String, message: String },
    #[error(
        "Unsafe installed path `{}`: expected managed path `{}`.",
        path.display(),
        expected.display()
    )]
    UnsafeInstalledPath { path: PathBuf, expected: PathBuf },
    #[error("Failed to determine binpm global home. Set BINPM_HOME, HOME, or USERPROFILE.")]
    MissingGlobalHome,
    #[error(
        "Invalid {name}: binpm global home must be an absolute path, got `{}`.",
        path.display()
    )]
    InvalidGlobalHome { name: &'static str, path: PathBuf },
}

impl BinpmError {
    pub fn exit_code(&self) -> i32 {
        match self {
            Self::NotImplemented { .. } => 2,
            Self::InvalidSourceSpec { .. }
            | Self::InvalidTargetKey { .. }
            | Self::InvalidCommandName { .. }
            | Self::UnsupportedTargetComponent { .. }
            | Self::ReleaseNotFound { .. }
            | Self::ManifestExists { .. }
            | Self::UnsupportedStorageVersion { .. } => 2,
            Self::CurrentDirectory(_)
            | Self::WriteFile { .. }
            | Self::ReadFile { .. }
            | Self::CreateDirectory { .. }
            | Self::RemovePath { .. }
            | Self::RenamePath { .. }
            | Self::SerializeToml { .. }
            | Self::ParseToml { .. }
            | Self::DigestMismatch { .. }
            | Self::MissingGlobalHome
            | Self::InvalidGlobalHome { .. }
            | Self::ReleaseHttpClient(_)
            | Self::ReleaseLookup(_) => 1,
            Self::FrozenLockfile { .. }
            | Self::StaleLockfile { .. }
            | Self::MissingManifest { .. }
            | Self::MissingTool { .. }
            | Self::AssetNotFound { .. }
            | Self::ArchiveExtractionNotImplemented { .. }
            | Self::VerificationRequired { .. }
            | Self::UnverifiedChecksumSourceOverride { .. }
            | Self::UnsafeUrl { .. }
            | Self::UnsafeInstalledPath { .. } => 2,
        }
    }
}
