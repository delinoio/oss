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

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReleaseSkipDiagnostic {
    pub tag: String,
    pub reason: String,
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
    #[error("Invalid target key `{raw}`. {message}")]
    InvalidTargetKey { raw: String, message: String },
    #[error("Invalid command name `{cmd}`: command names must be executable basenames.")]
    InvalidCommandName { cmd: String },
    #[error("Duplicate local command declaration `{cmd}` in binpm add arguments.")]
    DuplicateAddDeclaration { cmd: String },
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
         powershell. Alias: pwsh renders PowerShell syntax. Deferred shell: cmd. {cmd_hint}"
    )]
    UnsupportedShell { shell: String, cmd_hint: String },
    #[error(
        "Failed to infer a shell for binpm env. Pass `--shell \
         <bash|zsh|fish|powershell|pwsh|cmd>`."
    )]
    ShellRequired,
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
    #[error(
        "Frozen restore failed while downloading locked asset URL `{url}` for `{cmd}` after cache \
         state `{cache_state}` at `{}`. Network access was attempted in frozen mode; provider \
         authentication attached: {authenticated}. This is a frozen lockfile restore, not an \
         offline/cache-only run. Source error: {source}. Hint: pre-populate the binpm cache for \
         the locked SHA-256, configure the host-scoped provider token for private same-origin \
         assets, or refresh the lockfile/cache outside frozen mode.",
        cache_path.display()
    )]
    FrozenRestoreDownload {
        cmd: String,
        cache_path: PathBuf,
        cache_state: &'static str,
        url: String,
        authenticated: bool,
        source: Box<BinpmError>,
    },
    #[error("Release pagination loop detected at `{url}`.")]
    ReleasePaginationLoop { url: String },
    #[error("{}", release_not_found_message(package, message, skipped_releases))]
    ReleaseNotFound {
        package: String,
        message: String,
        skipped_releases: Vec<ReleaseSkipDiagnostic>,
    },
    #[error("Failed to determine the current working directory: {0}")]
    CurrentDirectory(#[source] io::Error),
    #[error("Failed to write `{}`: {source}", path.display())]
    WriteFile {
        path: PathBuf,
        #[source]
        source: io::Error,
    },
    #[error("Refusing to overwrite existing manifest `{}`.", path.display())]
    ManifestExists { path: PathBuf },
    #[error(
        "Invalid init manifest destination `{}`: explicit init destinations must be named `{}`.",
        path.display(),
        crate::storage::MANIFEST_FILE
    )]
    InvalidInitManifestPath { path: PathBuf },
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
    #[error(
        "Ambiguous `--package` execution arguments: `binpm x --package <source>` without CMD is \
         only the one-off shortcut and cannot forward args. To pass args, run `binpm x --package \
         <source> <cmd> -- <args...>`. To persist a local command, run `binpm add <cmd> <source>` \
         and then `binpm x <cmd> -- <args...>`."
    )]
    AmbiguousPackageShortcutArgs,
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
    #[error("{}", asset_selection_failed_message(package, target, diagnostics))]
    AssetSelectionFailed {
        package: String,
        target: String,
        diagnostics: Vec<String>,
    },
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
        "Tool `{cmd}` is not declared in `{}`. binpm will not infer a package source from the command name. Declare it explicitly with `binpm add {cmd} <source>` or run it one-off with `binpm x --package <source> {cmd}`.",
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
                | Self::FrozenRestoreDownload { .. }
                | Self::ReleaseNotFound { .. }
                | Self::AssetNotFound { .. }
                | Self::AssetSelectionFailed { .. }
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
            Self::FrozenRestoreDownload {
                cmd,
                cache_path,
                cache_state,
                url,
                authenticated,
                ..
            } => Some(serde_json::json!({
                "kind": "frozen_restore",
                "mode": frozen_lockfile_mode_label(),
                "reason": "locked_asset_download_failed",
                "cmd": cmd,
                "cache_path": cache_path.display().to_string(),
                "cache_state": cache_state,
                "restore_source": "locked_sanitized_asset_url",
                "locked_asset_url": url,
                "on_demand_install_attempt": frozen_lockfile_on_demand_install_attempt(),
                "would_change": cache_path.display().to_string(),
                "network_access_attempted": true,
                "provider_authentication_attached": authenticated,
                "offline_or_cache_only": false,
                "release_list_pagination_attempted": false,
                "safest_next_command": "pre-populate the binpm cache for the locked SHA-256 or run binpm install --local outside frozen mode with the required provider token",
                "local_development_escape_hatch": "--no-frozen-lockfile"
            })),
            Self::ReleaseNotFound {
                skipped_releases, ..
            } if !skipped_releases.is_empty() => Some(serde_json::json!({
                "kind": "release_not_found",
                "skipped_releases": skipped_releases
                    .iter()
                    .map(|release| serde_json::json!({
                        "tag": release.tag,
                        "reason": release.reason,
                    }))
                    .collect::<Vec<_>>()
            })),
            _ => None,
        }
    }

    pub fn exit_code(&self) -> i32 {
        match self {
            Self::NotImplemented { .. } => 2,
            Self::InvalidSourceSpec { .. }
            | Self::InvalidTargetKey { .. }
            | Self::InvalidCommandName { .. }
            | Self::AmbiguousPackageShortcutArgs
            | Self::DuplicateAddDeclaration { .. }
            | Self::InvalidBinSelection { .. }
            | Self::UnsupportedTargetComponent { .. }
            | Self::UnsupportedShell { .. }
            | Self::ShellRequired
            | Self::ReleaseNotFound { .. }
            | Self::ManifestExists { .. }
            | Self::InvalidInitManifestPath { .. }
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
            | Self::FrozenRestoreDownload { .. }
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
            | Self::AssetSelectionFailed { .. }
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

fn release_not_found_message(
    package: &str,
    message: &str,
    skipped_releases: &[ReleaseSkipDiagnostic],
) -> String {
    let mut rendered = format!("Failed to resolve release for `{package}`: {message}");
    if !skipped_releases.is_empty() {
        let skipped = skipped_releases
            .iter()
            .map(|release| format!("`{}` ({})", release.tag, release.reason))
            .collect::<Vec<_>>()
            .join(", ");
        rendered.push_str(". Skipped releases: ");
        rendered.push_str(&skipped);
    }
    rendered
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
            frozen_update_local_command(&[cmd.expect("checked command is present")])
        }
        Some(FrozenLockfileCommandContext::UpdateLocal { cmds, .. }) if !cmds.is_empty() => {
            let selected = cmds.iter().map(String::as_str).collect::<Vec<_>>();
            frozen_update_local_command(&selected)
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

fn frozen_update_local_command(cmds: &[&str]) -> String {
    let mut parts = vec![
        "binpm".to_string(),
        "update".to_string(),
        "--local".to_string(),
    ];
    parts.extend(cmds.iter().map(|cmd| cli_quote(cmd)));
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

fn asset_selection_failed_message(package: &str, target: &str, diagnostics: &[String]) -> String {
    let mut message = format!("No installable asset matched `{package}` for target `{target}`.");
    if !diagnostics.is_empty() {
        message.push_str(" Diagnostics: ");
        message.push_str(&diagnostics.join(" "));
    }
    message
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frozen_restore_download_diagnostic_preserves_common_frozen_fields() {
        let cache_path = PathBuf::from("/tmp/binpm/cache/sha256/abc/asset");
        let error = BinpmError::FrozenRestoreDownload {
            cmd: "tool".to_string(),
            cache_path: cache_path.clone(),
            cache_state: "missing",
            url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux".to_string(),
            authenticated: false,
            source: Box::new(BinpmError::ReleaseAssetStatus {
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux".to_string(),
                status: 503,
            }),
        };

        let diagnostic = error
            .structured_diagnostic()
            .expect("frozen restore diagnostic");
        assert_eq!(diagnostic["kind"], "frozen_restore");
        assert_eq!(diagnostic["reason"], "locked_asset_download_failed");
        assert_eq!(diagnostic["on_demand_install_attempt"], false);
        assert_eq!(diagnostic["would_change"], cache_path.display().to_string());
        assert_eq!(
            diagnostic["local_development_escape_hatch"],
            "--no-frozen-lockfile"
        );
    }
}
