use std::{fmt, io, path::PathBuf, sync::OnceLock};

use thiserror::Error;

pub type Result<T> = std::result::Result<T, BinpmError>;

static FROZEN_LOCKFILE_CONTEXT: OnceLock<FrozenLockfileCommandContext> = OnceLock::new();

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FrozenLockfileCommandContext {
    Add {
        cmd: String,
        source: String,
        bin: Option<String>,
        require_verified: bool,
        mode: Option<&'static str>,
    },
    InstallLocalSource {
        source: String,
        require_verified: bool,
        mode: Option<&'static str>,
    },
    InstallLocal {
        require_verified: bool,
        mode: Option<&'static str>,
    },
    UpdateLocal {
        cmds: Vec<String>,
        require_verified: bool,
        mode: Option<&'static str>,
    },
    Exec {
        mode: Option<&'static str>,
    },
    Other {
        mode: Option<&'static str>,
    },
    NotFrozen,
}

pub fn set_frozen_lockfile_context(context: FrozenLockfileCommandContext) {
    let _ = FROZEN_LOCKFILE_CONTEXT.set(context);
}

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
    #[error(
        "Unsupported shell `{shell}` for binpm env. Supported shells: bash, zsh, fish, \
         powershell. Deferred shell: cmd."
    )]
    UnsupportedShell { shell: String },
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
    #[error("Release asset `{url}` returned unexpected HTTP status {status}.")]
    ReleaseAssetStatus { url: String, status: u16 },
    #[error("Failed to stream release asset `{url}`: {source}")]
    DownloadStream {
        url: String,
        #[source]
        source: io::Error,
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
    #[error("Failed to serialize JSON output: {0}")]
    SerializeJson(#[source] serde_json::Error),
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
    #[error("{}", frozen_lockfile_message(path))]
    FrozenLockfile { path: PathBuf },
    #[error("{}", missing_lockfile_record_message(path, cmd))]
    FrozenLockfileMissingRecord { path: PathBuf, cmd: String },
    #[error("{}", orphan_lockfile_cleanup_message(path))]
    FrozenLockfileOrphanCleanup { path: PathBuf },
    #[error("{}", stale_lockfile_message(path, cmd))]
    StaleLockfile { path: PathBuf, cmd: String },
    #[error("Package record for `{cmd}` has inconsistent source identity.")]
    StalePackageRecord { cmd: String },
    #[error("No local binpm.toml manifest found from `{}`.", start.display())]
    MissingManifest { start: PathBuf },
    #[error("Tool `{cmd}` is not declared in `{}`.", manifest.display())]
    MissingTool { cmd: String, manifest: PathBuf },
    #[error("No installable asset matched `{package}` for target `{target}`.")]
    AssetNotFound { package: String, target: String },
    #[error(
        "Archive `{asset}` does not contain an executable binary with permission metadata or an \
         unambiguous filename/target match. Set `bin` in binpm.toml to the intended archive \
         member when upstream archives omit executable metadata."
    )]
    ArchiveBinaryNotFound { asset: String },
    #[error(
        "{}",
        ambiguous_archive_binaries_message(asset, candidates, suggestions)
    )]
    AmbiguousArchiveBinaries {
        asset: String,
        candidates: Vec<String>,
        suggestions: Vec<String>,
    },
    #[error("Invalid binary selection `{bin}`: binary selection must not be empty.")]
    InvalidBinSelection { bin: String },
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
    pub fn suggest_verbose_diagnostics(&self) -> bool {
        matches!(
            self,
            Self::ReleaseLookup(_)
                | Self::ReleaseAssetStatus { .. }
                | Self::DownloadStream { .. }
                | Self::ReleaseNotFound { .. }
                | Self::AssetNotFound { .. }
                | Self::ArchiveBinaryNotFound { .. }
                | Self::AmbiguousArchiveBinaries { .. }
                | Self::ArchiveMemberNotFound { .. }
                | Self::VerificationRequired { .. }
                | Self::DigestMismatch { .. }
                | Self::ProviderDigestMismatch { .. }
        )
    }

    pub fn structured_diagnostic(&self) -> Option<serde_json::Value> {
        match self {
            Self::FrozenLockfile { path } => {
                let reason = frozen_lockfile_reason(path);
                let safest_next_command = frozen_lockfile_safest_next_command(None);
                Some(serde_json::json!({
                    "kind": "frozen_lockfile",
                    "mode": frozen_lockfile_mode_label(),
                    "reason": reason,
                    "file": path.display().to_string(),
                    "record": frozen_lockfile_record(reason),
                    "on_demand_install_attempt": frozen_lockfile_on_demand_install_attempt(),
                    "would_change": path.display().to_string(),
                    "safest_next_command": safest_next_command,
                    "local_development_escape_hatch": "--no-frozen-lockfile"
                }))
            }
            Self::FrozenLockfileMissingRecord { path, cmd } => {
                let safest_next_command = frozen_lockfile_safest_next_command(Some(cmd));
                Some(serde_json::json!({
                    "kind": "frozen_lockfile",
                    "mode": frozen_lockfile_mode_label(),
                    "reason": "missing_lockfile_record",
                    "file": path.display().to_string(),
                    "record": format!("tools.{cmd} target record"),
                    "on_demand_install_attempt": frozen_lockfile_on_demand_install_attempt(),
                    "would_change": path.display().to_string(),
                    "safest_next_command": safest_next_command,
                    "local_development_escape_hatch": "--no-frozen-lockfile"
                }))
            }
            Self::FrozenLockfileOrphanCleanup { path } => Some(serde_json::json!({
                "kind": "frozen_lockfile",
                "mode": frozen_lockfile_mode_label(),
                "reason": "orphan_lockfile_record",
                "file": path.display().to_string(),
                "record": "orphaned lockfile or package record",
                "on_demand_install_attempt": frozen_lockfile_on_demand_install_attempt(),
                "would_change": path.display().to_string(),
                "safest_next_command": frozen_lockfile_safest_next_command(None),
                "local_development_escape_hatch": "--no-frozen-lockfile"
            })),
            Self::StaleLockfile { path, cmd } => frozen_lockfile_mode().map(|mode| {
                serde_json::json!({
                    "kind": "frozen_lockfile",
                    "mode": mode,
                    "reason": "stale_lockfile_record",
                    "file": path.display().to_string(),
                    "record": format!("tools.{cmd} target record"),
                    "on_demand_install_attempt": frozen_lockfile_on_demand_install_attempt(),
                    "would_change": path.display().to_string(),
                    "safest_next_command": frozen_lockfile_safest_next_command(Some(cmd)),
                    "local_development_escape_hatch": "--no-frozen-lockfile"
                })
            }),
            _ => None,
        }
    }

    pub fn exit_code(&self) -> i32 {
        match self {
            Self::NotImplemented { .. } => 2,
            Self::InvalidSourceSpec { .. }
            | Self::InvalidTargetKey { .. }
            | Self::InvalidCommandName { .. }
            | Self::InvalidBinSelection { .. }
            | Self::UnsupportedTargetComponent { .. }
            | Self::UnsupportedShell { .. }
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
            | Self::SerializeJson(_)
            | Self::ParseToml { .. }
            | Self::DigestMismatch { .. }
            | Self::MissingGlobalHome
            | Self::InvalidGlobalHome { .. }
            | Self::ReleaseHttpClient(_)
            | Self::ReleaseLookup(_)
            | Self::ReleaseLookupDiagnostic { .. }
            | Self::ReleaseAssetStatus { .. }
            | Self::DownloadStream { .. }
            | Self::ReleasePaginationLoop { .. } => 1,
            Self::FrozenLockfile { .. }
            | Self::FrozenLockfileMissingRecord { .. }
            | Self::FrozenLockfileOrphanCleanup { .. }
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

fn frozen_lockfile_message(path: &std::path::Path) -> String {
    let reason = frozen_lockfile_reason(path);
    let safest_next_command = frozen_lockfile_safest_next_command(None);
    let commit_target = frozen_lockfile_commit_target();
    let mut message = format!(
        "Frozen lockfile failure: mode `{}`; reason `{reason}`; file `{}`; record `{}`; would \
         change `{}`. Safest next command: `{safest_next_command}`, then commit {commit_target}. \
         For local development only, retry with `--no-frozen-lockfile`.",
        frozen_lockfile_mode_label(),
        path.display(),
        frozen_lockfile_record(reason),
        path.display()
    );
    if frozen_lockfile_on_demand_install_attempt() {
        message.push_str(
            " On-demand install attempt: `binpm x` needed to sync a missing executable or package \
             record before running.",
        );
    }
    message
}

fn missing_lockfile_record_message(path: &std::path::Path, cmd: &str) -> String {
    let safest_next_command = frozen_lockfile_safest_next_command(Some(cmd));
    let commit_target = frozen_lockfile_commit_target();
    let mut message = format!(
        "Frozen lockfile failure: mode `{}`; reason `missing_lockfile_record`; file `{}`; record \
         `tools.{cmd} target record`; would change `{}`. Safest next command: \
         `{safest_next_command}`, then commit {commit_target}. For local development only, retry \
         with `--no-frozen-lockfile`.",
        frozen_lockfile_mode_label(),
        path.display(),
        path.display()
    );
    if frozen_lockfile_on_demand_install_attempt() {
        message.push_str(
            " On-demand install attempt: `binpm x` needed to sync a missing executable or package \
             record before running.",
        );
    }
    message
}

fn orphan_lockfile_cleanup_message(path: &std::path::Path) -> String {
    format!(
        "Frozen lockfile failure: mode `{}`; reason `orphan_lockfile_record`; file `{}`; record \
         `orphaned lockfile or package record`; would change `{}`. Safest next command: `{}`, \
         then commit `binpm.lock`. For local development only, retry with `--no-frozen-lockfile`.",
        frozen_lockfile_mode_label(),
        path.display(),
        path.display(),
        frozen_lockfile_safest_next_command(None)
    )
}

fn stale_lockfile_message(path: &std::path::Path, cmd: &str) -> String {
    let Some(mode) = frozen_lockfile_mode() else {
        return format!(
            "Lockfile `{}` is stale for `{cmd}`. Run `binpm update --local {}` to refresh it.",
            path.display(),
            cli_quote(cmd)
        );
    };
    let command = frozen_lockfile_safest_next_command(Some(cmd));
    let commit_target = frozen_lockfile_commit_target();
    let mut message = format!(
        "Frozen lockfile failure: mode `{}`; reason `stale_lockfile_record`; file `{}`; record \
         `tools.{cmd} target record`; would change `{}`. Safest next command: `{command}`, then \
         commit {commit_target}. For local development only, retry with `--no-frozen-lockfile`.",
        mode,
        path.display(),
        path.display()
    );
    if frozen_lockfile_on_demand_install_attempt() {
        message.push_str(
            " On-demand install attempt: `binpm x` needed to sync a missing executable or package \
             record before running.",
        );
    }
    message
}

fn frozen_lockfile_reason(path: &std::path::Path) -> &'static str {
    if !path.exists() {
        "missing_lockfile"
    } else {
        "missing_lockfile_record"
    }
}

fn frozen_lockfile_record(reason: &str) -> &'static str {
    if reason == "missing_lockfile" {
        "binpm.lock"
    } else {
        "target-specific tool record"
    }
}

fn frozen_lockfile_mode() -> Option<&'static str> {
    match FROZEN_LOCKFILE_CONTEXT.get() {
        Some(FrozenLockfileCommandContext::Add { mode, .. })
        | Some(FrozenLockfileCommandContext::InstallLocalSource { mode, .. })
        | Some(FrozenLockfileCommandContext::InstallLocal { mode, .. })
        | Some(FrozenLockfileCommandContext::UpdateLocal { mode, .. })
        | Some(FrozenLockfileCommandContext::Exec { mode })
        | Some(FrozenLockfileCommandContext::Other { mode }) => *mode,
        Some(FrozenLockfileCommandContext::NotFrozen) | None => None,
    }
}

fn frozen_lockfile_mode_label() -> &'static str {
    frozen_lockfile_mode().unwrap_or("frozen-lockfile")
}

fn frozen_lockfile_on_demand_install_attempt() -> bool {
    matches!(
        FROZEN_LOCKFILE_CONTEXT.get(),
        Some(FrozenLockfileCommandContext::Exec { .. })
    )
}

fn frozen_lockfile_safest_next_command(cmd: Option<&str>) -> String {
    match FROZEN_LOCKFILE_CONTEXT.get() {
        Some(FrozenLockfileCommandContext::Add {
            cmd,
            source,
            bin,
            require_verified,
            ..
        }) => {
            let mut parts = vec![
                "binpm".to_string(),
                "add".to_string(),
                cli_quote(cmd),
                cli_quote(source),
            ];
            if let Some(bin) = bin {
                parts.push("--bin".to_string());
                parts.push(cli_quote(bin));
            }
            if *require_verified {
                parts.push("--require-verified".to_string());
            }
            parts.push("--no-frozen-lockfile".to_string());
            parts.join(" ")
        }
        Some(FrozenLockfileCommandContext::UpdateLocal { .. }) if cmd.is_some() => {
            frozen_update_local_command(cmd.expect("checked command is present"))
        }
        Some(FrozenLockfileCommandContext::UpdateLocal { cmds, .. }) if cmds.len() == 1 => {
            frozen_update_local_command(&cmds[0])
        }
        Some(FrozenLockfileCommandContext::InstallLocalSource {
            source,
            require_verified,
            ..
        }) => {
            let mut parts = vec![
                "binpm".to_string(),
                "install".to_string(),
                cli_quote(source),
                "--local".to_string(),
            ];
            if *require_verified {
                parts.push("--require-verified".to_string());
            }
            parts.push("--no-frozen-lockfile".to_string());
            parts.join(" ")
        }
        Some(FrozenLockfileCommandContext::InstallLocal {
            require_verified, ..
        }) => {
            let mut parts = vec![
                "binpm".to_string(),
                "install".to_string(),
                "--local".to_string(),
            ];
            if *require_verified {
                parts.push("--require-verified".to_string());
            }
            parts.join(" ")
        }
        _ => "binpm install --local".to_string(),
    }
}

fn frozen_update_local_command(cmd: &str) -> String {
    let mut parts = vec![
        "binpm".to_string(),
        "update".to_string(),
        "--local".to_string(),
        cli_quote(cmd),
    ];
    if matches!(
        FROZEN_LOCKFILE_CONTEXT.get(),
        Some(FrozenLockfileCommandContext::UpdateLocal {
            require_verified: true,
            ..
        })
    ) {
        parts.push("--require-verified".to_string());
    }
    parts.join(" ")
}

fn frozen_lockfile_commit_target() -> &'static str {
    match FROZEN_LOCKFILE_CONTEXT.get() {
        Some(
            FrozenLockfileCommandContext::Add { .. }
            | FrozenLockfileCommandContext::InstallLocalSource { .. },
        ) => "`binpm.toml` and `binpm.lock`",
        _ => "`binpm.lock`",
    }
}

fn cli_quote(raw: &str) -> String {
    if !raw.is_empty()
        && raw.chars().all(|character| {
            character.is_ascii_alphanumeric()
                || matches!(character, '-' | '_' | '.' | '/' | ':' | '@')
        })
    {
        raw.to_string()
    } else {
        posix_single_quote(raw)
    }
}

fn posix_single_quote(raw: &str) -> String {
    format!("'{}'", raw.replace('\'', "'\\''"))
}

fn ambiguous_archive_binaries_message(
    asset: &str,
    candidates: &[String],
    suggestions: &[String],
) -> String {
    let mut message = format!(
        "Archive `{asset}` contains multiple plausible executables: {}.",
        candidates.join(", ")
    );
    if suggestions.is_empty() {
        message.push_str(" Retry with `--bin <candidate>` or set `bin` in binpm.toml.");
    } else {
        message.push_str(" Retry with ");
        message.push_str(&suggestions.join(" or "));
        message.push('.');
    }
    message
}
