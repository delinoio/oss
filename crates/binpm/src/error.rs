use std::{fmt, io, path::PathBuf};

use thiserror::Error;

pub type Result<T> = std::result::Result<T, BinpmError>;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReleaseLookupDiagnosticKind {
    MissingAuth,
    InsufficientPermissions,
    RateLimited,
}

impl fmt::Display for ReleaseLookupDiagnosticKind {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::MissingAuth => "missing authentication",
            Self::InsufficientPermissions => "insufficient permissions",
            Self::RateLimited => "rate limited",
        })
    }
}

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
    #[error(
        "Tool `{cmd}` and `{other_cmd}` both install to `{}` on this target.",
        path.display()
    )]
    InstalledPathCollision {
        cmd: String,
        other_cmd: String,
        path: PathBuf,
    },
    #[error("Unsupported target component `{raw}` for {component}.")]
    UnsupportedTargetComponent {
        component: &'static str,
        raw: String,
    },
    #[error("Failed to build release HTTP client: {0}")]
    ReleaseHttpClient(#[source] reqwest::Error),
    #[error("Failed to look up release metadata: {0}")]
    ReleaseLookup(#[source] reqwest::Error),
    #[error(
        "Failed to look up release metadata for `{package}` on {provider} host `{host}`: {kind} \
         (HTTP {status}). {message} Hint: {hint}"
    )]
    ReleaseLookupDiagnostic {
        package: String,
        provider: &'static str,
        host: String,
        status: u16,
        kind: ReleaseLookupDiagnosticKind,
        message: String,
        hint: String,
    },
    #[error("Release pagination loop detected at `{url}`.")]
    ReleasePaginationLoop { url: String },
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
    #[error("Unsafe managed directory `{}`: managed directories must not be symlinks.", path.display())]
    UnsafeManagedDirectory { path: PathBuf },
    #[error("Unsafe managed file `{}`: managed files must be regular files, not symlinks.", path.display())]
    UnsafeManagedFile { path: PathBuf },
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
    #[error("Package record for `{cmd}` has inconsistent source identity.")]
    StalePackageRecord { cmd: String },
    #[error("No local binpm.toml manifest found from `{}`.", start.display())]
    MissingManifest { start: PathBuf },
    #[error("Tool `{cmd}` is not declared in `{}`.", manifest.display())]
    MissingTool { cmd: String, manifest: PathBuf },
    #[error("No installable asset matched `{package}` for target `{target}`.")]
    AssetNotFound { package: String, target: String },
    #[error("Archive `{asset}` does not contain an executable binary.")]
    ArchiveBinaryNotFound { asset: String },
    #[error(
        "Archive `{asset}` contains multiple plausible executables: {}. Set `bin` in binpm.toml to disambiguate.",
        candidates.join(", ")
    )]
    AmbiguousArchiveBinaries {
        asset: String,
        candidates: Vec<String>,
    },
    #[error("Archive `{asset}` does not contain selected binary `{member}`.")]
    ArchiveMemberNotFound { asset: String, member: String },
    #[error("Unsafe archive member path `{path}` in `{asset}`: {message}")]
    UnsafeArchivePath {
        asset: String,
        path: String,
        message: String,
    },
    #[error("Failed to extract archive `{asset}`: {message}")]
    ArchiveExtraction { asset: String, message: String },
    #[error("Failed to execute `{cmd}`: {source}")]
    Execute {
        cmd: String,
        #[source]
        source: io::Error,
    },
    #[error("Command `{cmd}` exited with status {status}.")]
    CommandFailed { cmd: String, status: i32 },
    #[error(
        "Tool `{cmd}` is not declared in `{}`. Run `binpm add {cmd} <source>` or retry with `binpm x --package <source> {cmd}`.",
        manifest.display()
    )]
    ExecToolMissing { cmd: String, manifest: PathBuf },
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
    #[error("Provider digest evidence does not match the recorded SHA-256 for `{package}`.")]
    ProviderDigestMismatch { package: String },
    #[error("Invalid SHA-256 digest `{value}`: expected 64 hexadecimal characters.")]
    InvalidSha256 { value: String },
    #[error("Unsafe persisted URL `{url}`: {message}")]
    UnsafeUrl { url: String, message: String },
    #[error(
        "Unsafe installed path `{}`: expected managed path `{}`.",
        path.display(),
        expected.display()
    )]
    UnsafeInstalledPath { path: PathBuf, expected: PathBuf },
    #[error(
        "Unsafe cache path `{}`: expected cache asset path `{}`.",
        path.display(),
        expected.display()
    )]
    UnsafeCachePath { path: PathBuf, expected: PathBuf },
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
            | Self::UnsupportedStorageVersion { .. }
            | Self::InvalidSha256 { .. } => 2,
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
            | Self::ReleaseLookup(_)
            | Self::ReleaseLookupDiagnostic { .. }
            | Self::ReleasePaginationLoop { .. } => 1,
            Self::FrozenLockfile { .. }
            | Self::StaleLockfile { .. }
            | Self::StalePackageRecord { .. }
            | Self::MissingManifest { .. }
            | Self::MissingTool { .. }
            | Self::ExecToolMissing { .. }
            | Self::InstalledPathCollision { .. }
            | Self::AssetNotFound { .. }
            | Self::ArchiveBinaryNotFound { .. }
            | Self::AmbiguousArchiveBinaries { .. }
            | Self::ArchiveMemberNotFound { .. }
            | Self::UnsafeArchivePath { .. }
            | Self::ArchiveExtraction { .. }
            | Self::VerificationRequired { .. }
            | Self::ProviderDigestMismatch { .. }
            | Self::UnverifiedChecksumSourceOverride { .. }
            | Self::UnsafeUrl { .. }
            | Self::UnsafeInstalledPath { .. }
            | Self::UnsafeManagedDirectory { .. }
            | Self::UnsafeManagedFile { .. }
            | Self::UnsafeCachePath { .. }
            | Self::CommandFailed { .. } => 2,
            Self::Execute { .. } => 1,
        }
    }
}
