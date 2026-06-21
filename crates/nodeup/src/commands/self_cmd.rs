use std::{
    collections::BTreeMap,
    env, fs,
    io::Write,
    path::{Component, Path, PathBuf},
};

use serde::Serialize;
use tempfile::NamedTempFile;
use toml::{value::Table, Value};
use tracing::{info, warn};

use super::shim_cmd;
use crate::{
    cli::{OutputColorMode, OutputFormat, SelfCommand},
    commands::print_output,
    errors::{NodeupError, Result},
    overrides::{OverrideEntry, OverridesFile, OVERRIDES_SCHEMA_VERSION},
    store::{SettingsFile, SETTINGS_SCHEMA_VERSION},
    NodeupApp,
};

const NODEUP_SELF_UPDATE_SOURCE: &str = "NODEUP_SELF_UPDATE_SOURCE";
const NODEUP_SELF_BIN_PATH: &str = "NODEUP_SELF_BIN_PATH";

fn self_internal(cause: impl Into<String>) -> NodeupError {
    NodeupError::internal_with_hint(
        cause,
        "Retry `nodeup self ...`. If it keeps failing, run with `RUST_LOG=nodeup=debug` and \
         inspect logs.",
    )
}

fn self_invalid_input(cause: impl Into<String>) -> NodeupError {
    NodeupError::invalid_input_with_hint(
        cause,
        "Review command inputs and local nodeup data files, then retry the `nodeup self` command.",
    )
}

fn self_not_found(cause: impl Into<String>) -> NodeupError {
    NodeupError::not_found_with_hint(
        cause,
        "Verify the referenced path exists and is accessible, then retry the command.",
    )
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum SelfAction {
    Update,
    Uninstall,
    UpgradeData,
}

impl SelfAction {
    fn as_str(self) -> &'static str {
        match self {
            Self::Update => "self update",
            Self::Uninstall => "self uninstall",
            Self::UpgradeData => "self upgrade-data",
        }
    }

    fn command_path(self) -> &'static str {
        match self {
            Self::Update => "nodeup.self.update",
            Self::Uninstall => "nodeup.self.uninstall",
            Self::UpgradeData => "nodeup.self.upgrade-data",
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum SelfUpdateOutcome {
    Updated,
    AlreadyUpToDate,
}

impl SelfUpdateOutcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Updated => "updated",
            Self::AlreadyUpToDate => "already-up-to-date",
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum SelfUninstallOutcome {
    Removed,
    AlreadyClean,
}

impl SelfUninstallOutcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Removed => "removed",
            Self::AlreadyClean => "already-clean",
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum SchemaMigrationOutcome {
    Created,
    Upgraded,
    AlreadyCurrent,
}

impl SchemaMigrationOutcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Created => "created",
            Self::Upgraded => "upgraded",
            Self::AlreadyCurrent => "already-current",
        }
    }

    fn is_changed(self) -> bool {
        matches!(self, Self::Created | Self::Upgraded)
    }
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum SelfUpgradeDataOutcome {
    Upgraded,
    AlreadyCurrent,
}

impl SelfUpgradeDataOutcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Upgraded => "upgraded",
            Self::AlreadyCurrent => "already-current",
        }
    }
}

#[derive(Debug, Serialize)]
struct SelfUpdateResponse {
    action: SelfAction,
    status: SelfUpdateOutcome,
    target_binary: String,
    source_binary: String,
}

#[derive(Debug, Serialize)]
struct SelfUninstallResponse {
    action: SelfAction,
    status: SelfUninstallOutcome,
    removed_paths: Vec<String>,
    cleanup_boundaries: Vec<SelfUninstallCleanupBoundary>,
    remaining_manual_steps: Vec<String>,
    likely_leftover_paths: Vec<String>,
}

#[derive(Debug, Serialize)]
struct SelfUninstallCleanupBoundary {
    category: &'static str,
    cleanup: &'static str,
    paths: Vec<String>,
}

struct SelfUninstallRoot {
    category: &'static str,
    path: PathBuf,
    bootstrap_child: Option<std::ffi::OsString>,
}

struct SelfUninstallTarget {
    category: &'static str,
    path: PathBuf,
}

#[derive(Debug, Serialize)]
struct SchemaMigrationResult {
    file: String,
    from_schema: u32,
    to_schema: u32,
    status: SchemaMigrationOutcome,
}

#[derive(Debug, Serialize)]
struct SelfUpgradeDataResponse {
    action: SelfAction,
    status: SelfUpgradeDataOutcome,
    settings: SchemaMigrationResult,
    overrides: SchemaMigrationResult,
}

pub fn execute(
    command: SelfCommand,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    match command {
        SelfCommand::Update => update(output, color, app),
        SelfCommand::Uninstall => uninstall(output, color, app),
        SelfCommand::UpgradeData => upgrade_data(output, color, app),
    }
}

fn update(output: OutputFormat, color: Option<OutputColorMode>, _app: &NodeupApp) -> Result<i32> {
    let action = SelfAction::Update;
    let command_path = action.command_path();

    let source_binary = resolve_update_source_path().map_err(|error| log_failure(action, error))?;
    let target_binary = resolve_target_binary_path().map_err(|error| log_failure(action, error))?;

    let source_hash = file_hash(&source_binary).map_err(|error| log_failure(action, error))?;
    let status = if target_binary.exists() {
        let current_hash = file_hash(&target_binary).map_err(|error| log_failure(action, error))?;
        if current_hash == source_hash {
            SelfUpdateOutcome::AlreadyUpToDate
        } else {
            replace_binary(&source_binary, &target_binary)
                .map_err(|error| log_failure(action, error))?;
            SelfUpdateOutcome::Updated
        }
    } else {
        replace_binary(&source_binary, &target_binary)
            .map_err(|error| log_failure(action, error))?;
        SelfUpdateOutcome::Updated
    };

    info!(
        command_path,
        action = action.as_str(),
        outcome = status.as_str(),
        target_binary = %target_binary.display(),
        source_binary = %source_binary.display(),
        "Processed self update"
    );

    let response = SelfUpdateResponse {
        action,
        status,
        target_binary: target_binary.display().to_string(),
        source_binary: source_binary.display().to_string(),
    };

    let human = format!(
        "Self update status: {} (target: {})",
        status.as_str(),
        response.target_binary
    );
    print_output(output, color, &human, &response)?;

    Ok(0)
}

fn uninstall(output: OutputFormat, color: Option<OutputColorMode>, app: &NodeupApp) -> Result<i32> {
    let action = SelfAction::Uninstall;

    let mut deletion_targets = Vec::new();
    for root in uninstall_roots(app) {
        let normalized_path =
            normalize_target_path(&root.path).map_err(|error| log_failure(action, error))?;
        ensure_safe_uninstall_path(&normalized_path).map_err(|error| log_failure(action, error))?;
        ensure_uninstall_path_excludes_running_binary(&normalized_path)
            .map_err(|error| log_failure(action, error))?;

        let has_artifacts = if normalized_path.exists() {
            path_has_artifacts(&normalized_path, root.bootstrap_child.as_deref())
                .map_err(|error| log_failure(action, error))?
        } else {
            false
        };

        if has_artifacts {
            deletion_targets.push(SelfUninstallTarget {
                category: root.category,
                path: normalized_path,
            });
        }
    }

    let mut removed_paths = Vec::new();
    let mut removed_targets = Vec::new();
    let preserved_paths = preserved_uninstall_paths();
    for target in deletion_targets {
        remove_uninstall_target(&target.path, &preserved_paths).map_err(|error| {
            log_failure(
                action,
                self_internal(format!(
                    "Failed to remove uninstall target {}: {error}",
                    target.path.display()
                )),
            )
        })?;
        removed_paths.push(target.path.display().to_string());
        removed_targets.push(target);
    }

    let status = if removed_paths.is_empty() {
        SelfUninstallOutcome::AlreadyClean
    } else {
        removed_paths.sort();
        SelfUninstallOutcome::Removed
    };

    info!(
        command_path = action.command_path(),
        action = action.as_str(),
        outcome = status.as_str(),
        removed_count = removed_paths.len(),
        "Processed self uninstall"
    );

    let likely_leftover_paths = likely_leftover_paths();
    let remaining_manual_steps = remaining_manual_steps(&likely_leftover_paths);
    let response = SelfUninstallResponse {
        action,
        status,
        cleanup_boundaries: cleanup_boundaries(&removed_targets, &likely_leftover_paths),
        removed_paths,
        remaining_manual_steps,
        likely_leftover_paths,
    };

    let human = format!(
        "Self uninstall status: {} | removed paths: {} | manual steps: {}",
        status.as_str(),
        human_list(&response.removed_paths),
        human_list(&response.remaining_manual_steps)
    );
    print_output(output, color, &human, &response)?;

    Ok(0)
}

fn upgrade_data(
    output: OutputFormat,
    color: Option<OutputColorMode>,
    app: &NodeupApp,
) -> Result<i32> {
    let action = SelfAction::UpgradeData;
    let settings = migrate_settings_schema(app).map_err(|error| log_failure(action, error))?;
    let overrides = migrate_overrides_schema(app).map_err(|error| log_failure(action, error))?;

    let status = if settings.status.is_changed() || overrides.status.is_changed() {
        SelfUpgradeDataOutcome::Upgraded
    } else {
        SelfUpgradeDataOutcome::AlreadyCurrent
    };

    info!(
        command_path = action.command_path(),
        action = action.as_str(),
        outcome = status.as_str(),
        settings_status = settings.status.as_str(),
        overrides_status = overrides.status.as_str(),
        "Processed self data schema upgrade"
    );

    let response = SelfUpgradeDataResponse {
        action,
        status,
        settings,
        overrides,
    };

    let human = format!(
        "Self upgrade-data status: {} (settings: {}, overrides: {})",
        status.as_str(),
        response.settings.status.as_str(),
        response.overrides.status.as_str()
    );
    print_output(output, color, &human, &response)?;

    Ok(0)
}

fn resolve_update_source_path() -> Result<PathBuf> {
    let source = env::var_os(NODEUP_SELF_UPDATE_SOURCE).ok_or_else(|| {
        self_invalid_input(format!(
            "Self update source is not configured. Set {NODEUP_SELF_UPDATE_SOURCE} to a binary \
             path"
        ))
    })?;

    let source_path = PathBuf::from(source);
    if !source_path.exists() {
        return Err(self_not_found(format!(
            "Self update source does not exist: {}",
            source_path.display()
        )));
    }

    if !source_path.is_file() {
        return Err(self_invalid_input(format!(
            "Self update source is not a file: {}",
            source_path.display()
        )));
    }

    Ok(source_path)
}

fn resolve_target_binary_path() -> Result<PathBuf> {
    if let Some(path) = env::var_os(NODEUP_SELF_BIN_PATH) {
        return Ok(PathBuf::from(path));
    }

    env::current_exe().map_err(|error| {
        self_internal(format!(
            "Failed to resolve current executable path: {error}"
        ))
    })
}

fn replace_binary(source: &Path, target: &Path) -> Result<()> {
    let parent = target.parent().ok_or_else(|| {
        self_internal(format!(
            "Cannot replace binary without parent directory: {}",
            target.display()
        ))
    })?;

    fs::create_dir_all(parent)?;

    let mut staged = NamedTempFile::new_in(parent)?;
    let bytes = fs::read(source)?;
    staged.write_all(&bytes)?;
    staged.flush()?;
    let source_permissions = fs::metadata(source)?.permissions();
    fs::set_permissions(staged.path(), source_permissions)?;

    let staged_path = staged.into_temp_path();
    if target.exists() {
        let backup_target = backup_target_path(target)?;
        if backup_target.exists() {
            fs::remove_file(&backup_target).map_err(|error| {
                self_internal(format!(
                    "Failed to clean stale backup {} before self update: {error}",
                    backup_target.display()
                ))
            })?;
        }

        fs::rename(target, &backup_target).map_err(|error| {
            self_internal(format!(
                "Failed to stage existing binary {} for replacement: {error}",
                target.display()
            ))
        })?;

        if let Err(error) = fs::rename(&staged_path, target) {
            let rollback = fs::rename(&backup_target, target);
            return Err(self_internal(format!(
                "Failed to replace binary {} with staged update: {error}. Rollback status: {}",
                target.display(),
                if rollback.is_ok() {
                    "restored-previous-binary"
                } else {
                    "rollback-failed"
                }
            )));
        }

        if let Err(error) = fs::remove_file(&backup_target) {
            warn!(
                command_path = "nodeup.self.update",
                action = "self update",
                outcome = "updated",
                cleanup_status = "deferred",
                backup_path = %backup_target.display(),
                cleanup_error = %error,
                "Updated binary but deferred backup cleanup"
            );
        }
    } else {
        fs::rename(&staged_path, target).map_err(|error| {
            self_internal(format!(
                "Failed to replace binary {} with staged update: {error}",
                target.display()
            ))
        })?;
    }

    Ok(())
}

fn backup_target_path(target: &Path) -> Result<PathBuf> {
    let filename = target.file_name().ok_or_else(|| {
        self_invalid_input(format!(
            "Cannot create backup path for binary without filename: {}",
            target.display()
        ))
    })?;

    let mut backup_name = filename.to_os_string();
    backup_name.push(".nodeup-backup");
    Ok(target.with_file_name(backup_name))
}

fn file_hash(path: &Path) -> Result<Vec<u8>> {
    let bytes = fs::read(path)?;
    use sha2::{Digest, Sha256};

    Ok(Sha256::digest(&bytes).to_vec())
}

fn normalize_target_path(path: &Path) -> Result<PathBuf> {
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        std::env::current_dir()?.join(path)
    };

    if absolute.exists() {
        return Ok(absolute.canonicalize()?);
    }

    Ok(absolute)
}

fn ensure_safe_uninstall_path(path: &Path) -> Result<()> {
    if path.parent().is_none() {
        return Err(self_invalid_input(format!(
            "Refusing to uninstall unsafe path: {}",
            path.display()
        )));
    }

    let owned_by_nodeup = path.components().any(|component| match component {
        Component::Normal(value) => value.to_str().is_some_and(is_nodeup_owned_component),
        _ => false,
    });

    if !owned_by_nodeup {
        return Err(self_invalid_input(format!(
            "Refusing to uninstall non-nodeup-owned path: {}",
            path.display()
        )));
    }

    Ok(())
}

fn ensure_uninstall_path_excludes_running_binary(path: &Path) -> Result<()> {
    if !path.exists() {
        return Ok(());
    }

    let current_exe = env::current_exe().map_err(|error| {
        self_internal(format!(
            "Failed to resolve current executable path before uninstall: {error}"
        ))
    })?;
    let current_exe = normalize_target_path(&current_exe)?;

    if current_exe.starts_with(path) {
        return Err(self_invalid_input(format!(
            "Refusing to uninstall path containing the running nodeup binary: {}",
            path.display()
        )));
    }

    Ok(())
}

fn is_nodeup_owned_component(component: &str) -> bool {
    let lowercase = component.to_ascii_lowercase();
    lowercase == "nodeup"
        || lowercase.starts_with("nodeup-")
        || lowercase.starts_with("nodeup_")
        || lowercase.ends_with("-nodeup")
        || lowercase.ends_with("_nodeup")
}

fn path_has_artifacts(path: &Path, bootstrap_child: Option<&std::ffi::OsStr>) -> Result<bool> {
    if !path.exists() {
        return Ok(false);
    }

    for entry in fs::read_dir(path)? {
        let entry = entry?;
        if let Some(expected) = bootstrap_child {
            if entry.file_name() == expected
                && entry.file_type()?.is_dir()
                && directory_is_empty(&entry.path())?
            {
                continue;
            }
        }
        return Ok(true);
    }

    Ok(false)
}

fn directory_is_empty(path: &Path) -> Result<bool> {
    let mut entries = fs::read_dir(path)?;
    if let Some(entry) = entries.next() {
        let _ = entry?;
        return Ok(false);
    }

    Ok(true)
}

fn remove_uninstall_target(path: &Path, preserved_paths: &[PathBuf]) -> std::io::Result<()> {
    let preserved_descendants: Vec<&Path> = preserved_paths
        .iter()
        .map(PathBuf::as_path)
        .filter(|preserved| preserved.starts_with(path))
        .collect();

    if preserved_descendants.is_empty() {
        return fs::remove_dir_all(path);
    }

    remove_path_preserving(path, &preserved_descendants)
}

fn remove_path_preserving(path: &Path, preserved_dirs: &[&Path]) -> std::io::Result<()> {
    if preserved_dirs
        .iter()
        .any(|preserved| paths_equal(path, preserved))
    {
        return Ok(());
    }

    if preserved_dirs
        .iter()
        .any(|preserved| preserved.starts_with(path))
    {
        for entry in fs::read_dir(path)? {
            let entry = entry?;
            remove_path_preserving(&entry.path(), preserved_dirs)?;
        }
        return Ok(());
    }

    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_dir() && !metadata.file_type().is_symlink() => {
            fs::remove_dir_all(path)
        }
        Ok(_) => fs::remove_file(path),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

fn paths_equal(left: &Path, right: &Path) -> bool {
    if left == right {
        return true;
    }

    match (left.canonicalize(), right.canonicalize()) {
        (Ok(left), Ok(right)) => left == right,
        _ => false,
    }
}

fn human_list(values: &[String]) -> String {
    if values.is_empty() {
        "<none>".to_string()
    } else {
        values.join("; ")
    }
}

fn cleanup_boundaries(
    removed_targets: &[SelfUninstallTarget],
    likely_leftover_paths: &[String],
) -> Vec<SelfUninstallCleanupBoundary> {
    vec![
        SelfUninstallCleanupBoundary {
            category: "data",
            cleanup: "removed-when-nodeup-owned-and-populated",
            paths: boundary_paths("data", removed_targets),
        },
        SelfUninstallCleanupBoundary {
            category: "cache",
            cleanup: "removed-when-nodeup-owned-and-populated",
            paths: boundary_paths("cache", removed_targets),
        },
        SelfUninstallCleanupBoundary {
            category: "config",
            cleanup: "removed-when-nodeup-owned-and-populated",
            paths: boundary_paths("config", removed_targets),
        },
        SelfUninstallCleanupBoundary {
            category: "binary",
            cleanup: "manual",
            paths: likely_leftover_paths
                .iter()
                .filter(|path| !is_likely_shim_path(path))
                .cloned()
                .collect(),
        },
        SelfUninstallCleanupBoundary {
            category: "shims",
            cleanup: "manual",
            paths: likely_leftover_paths
                .iter()
                .filter(|path| is_likely_shim_path(path))
                .cloned()
                .collect(),
        },
        SelfUninstallCleanupBoundary {
            category: "shell-profile-path",
            cleanup: "manual",
            paths: Vec::new(),
        },
    ]
}

fn boundary_paths(category: &'static str, removed_targets: &[SelfUninstallTarget]) -> Vec<String> {
    removed_targets
        .iter()
        .filter(|target| target.category == category)
        .map(|target| target.path.display().to_string())
        .collect()
}

fn uninstall_roots(app: &NodeupApp) -> [SelfUninstallRoot; 3] {
    [
        SelfUninstallRoot {
            category: "data",
            path: app.paths.data_root.clone(),
            bootstrap_child: app
                .paths
                .toolchains_dir
                .file_name()
                .map(|value| value.to_os_string()),
        },
        SelfUninstallRoot {
            category: "cache",
            path: app.paths.cache_root.clone(),
            bootstrap_child: app
                .paths
                .downloads_dir
                .file_name()
                .map(|value| value.to_os_string()),
        },
        SelfUninstallRoot {
            category: "config",
            path: app.paths.config_root.clone(),
            bootstrap_child: None,
        },
    ]
}

fn likely_leftover_paths() -> Vec<String> {
    let mut paths = Vec::new();

    if let Ok(path) = resolve_target_binary_path() {
        if path.exists() {
            paths.push(path.display().to_string());
        }
    }

    for shim_dir in leftover_shim_directories() {
        for alias in ["node", "npm", "npx", "yarn", "pnpm"] {
            for candidate in [
                shim_dir.join(alias),
                shim_dir.join(format!("{alias}.exe")),
                shim_dir.join(format!(".{alias}.exe.nodeup-shim")),
            ] {
                if shim_cmd::is_nodeup_owned_shim_path(&candidate)
                    || shim_cmd::is_nodeup_copy_marker_path(&candidate)
                {
                    paths.push(candidate.display().to_string());
                }
            }
        }
    }

    paths.sort();
    paths.dedup();
    paths
}

fn preserved_uninstall_paths() -> Vec<PathBuf> {
    let mut paths = existing_shim_directories();
    if let Ok(path) = resolve_target_binary_path() {
        if path.exists() {
            if let Ok(path) = normalize_target_path(&path) {
                paths.push(path);
            }
        }
    }

    paths.into_iter().fold(Vec::new(), |mut unique, path| {
        if !unique.iter().any(|existing| paths_equal(existing, &path)) {
            unique.push(path);
        }
        unique
    })
}

fn existing_shim_directories() -> Vec<PathBuf> {
    let mut paths = shim_directories();
    for root in known_nodeup_roots() {
        collect_managed_shim_dirs(&root, &mut paths);
    }

    paths
        .into_iter()
        .filter(|path| path.exists())
        .filter_map(|path| normalize_target_path(&path).ok())
        .collect::<Vec<_>>()
        .into_iter()
        .fold(Vec::new(), |mut unique, path| {
            if !unique.iter().any(|existing| paths_equal(existing, &path)) {
                unique.push(path);
            }
            unique
        })
}

fn collect_managed_shim_dirs(path: &Path, paths: &mut Vec<PathBuf>) {
    let Ok(metadata) = fs::symlink_metadata(path) else {
        return;
    };
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return;
    }

    if directory_contains_nodeup_shim(path) {
        paths.push(path.to_path_buf());
    }

    let Ok(entries) = fs::read_dir(path) else {
        return;
    };
    for entry in entries.flatten() {
        collect_managed_shim_dirs(&entry.path(), paths);
    }
}

fn directory_contains_nodeup_shim(path: &Path) -> bool {
    ["node", "npm", "npx", "yarn", "pnpm"].iter().any(|alias| {
        [
            path.join(alias),
            path.join(format!("{alias}.exe")),
            path.join(format!(".{alias}.exe.nodeup-shim")),
        ]
        .into_iter()
        .any(|candidate| {
            shim_cmd::is_nodeup_owned_shim_path(&candidate)
                || shim_cmd::is_nodeup_copy_marker_path(&candidate)
        })
    })
}

fn known_nodeup_roots() -> Vec<PathBuf> {
    [
        env::var_os("NODEUP_DATA_HOME"),
        env::var_os("NODEUP_CACHE_HOME"),
        env::var_os("NODEUP_CONFIG_HOME"),
    ]
    .into_iter()
    .flatten()
    .map(PathBuf::from)
    .collect()
}

fn leftover_shim_directories() -> Vec<PathBuf> {
    let mut paths = existing_shim_directories();
    paths.extend(shim_directories());
    paths
        .into_iter()
        .filter_map(|path| normalize_target_path(&path).ok().or(Some(path)))
        .collect()
}

fn shim_directories() -> Vec<PathBuf> {
    let mut paths = vec![default_shim_dir(), legacy_shim_dir()];
    paths.sort();
    paths.dedup();
    paths
}

fn remaining_manual_steps(likely_leftover_paths: &[String]) -> Vec<String> {
    let mut steps = vec![
        "Remove the nodeup binary from its installation directory if it is no longer needed."
            .to_string(),
        "Remove managed shim files created by `nodeup shim setup` if they are no longer needed."
            .to_string(),
        "Remove the Nodeup shim directory from shell profile files or the user PATH manually."
            .to_string(),
    ];

    if !likely_leftover_paths.is_empty() {
        steps.push(format!(
            "Review likely leftover paths: {}",
            likely_leftover_paths.join(", ")
        ));
    }

    steps
}

fn default_shim_dir() -> PathBuf {
    if let Some(dir) = env::var_os("NODEUP_SHIM_DIR") {
        return PathBuf::from(dir);
    }

    home_dir().join(".local").join("bin")
}

fn legacy_shim_dir() -> PathBuf {
    home_dir()
        .join(".local")
        .join("share")
        .join("nodeup")
        .join("shims")
}

fn home_dir() -> PathBuf {
    env::var_os("HOME")
        .map(PathBuf::from)
        .or_else(|| env::var_os("USERPROFILE").map(PathBuf::from))
        .unwrap_or_else(|| PathBuf::from("."))
}

fn is_likely_shim_path(path: &str) -> bool {
    ["node", "npm", "npx", "yarn", "pnpm"].iter().any(|alias| {
        path.ends_with(alias)
            || path.ends_with(&format!("{alias}.exe"))
            || path.ends_with(&format!(".{alias}.exe.nodeup-shim"))
    })
}

fn migrate_settings_schema(app: &NodeupApp) -> Result<SchemaMigrationResult> {
    let file_path = app.paths.settings_file.clone();
    if !file_path.exists() {
        let defaults = SettingsFile::default();
        app.store.save_settings(&defaults)?;
        return Ok(SchemaMigrationResult {
            file: file_path.display().to_string(),
            from_schema: SETTINGS_SCHEMA_VERSION,
            to_schema: SETTINGS_SCHEMA_VERSION,
            status: SchemaMigrationOutcome::Created,
        });
    }

    let content = fs::read_to_string(&file_path)?;
    let raw_value: Value = toml::from_str(&content)?;
    let from_schema = extract_schema_version(&raw_value, &file_path)?;

    if from_schema > SETTINGS_SCHEMA_VERSION {
        return Err(self_invalid_input(format!(
            "Unsupported settings schema version: {from_schema}"
        )));
    }

    if from_schema == SETTINGS_SCHEMA_VERSION {
        let _: SettingsFile = toml::from_str(&content)?;
        return Ok(SchemaMigrationResult {
            file: file_path.display().to_string(),
            from_schema,
            to_schema: SETTINGS_SCHEMA_VERSION,
            status: SchemaMigrationOutcome::AlreadyCurrent,
        });
    }

    let migrated = migrate_settings_legacy(&raw_value, from_schema, &file_path)?;
    app.store.save_settings(&migrated)?;

    Ok(SchemaMigrationResult {
        file: file_path.display().to_string(),
        from_schema,
        to_schema: SETTINGS_SCHEMA_VERSION,
        status: SchemaMigrationOutcome::Upgraded,
    })
}

fn migrate_overrides_schema(app: &NodeupApp) -> Result<SchemaMigrationResult> {
    let file_path = app.paths.overrides_file.clone();
    if !file_path.exists() {
        let defaults = OverridesFile::default();
        app.overrides.save(&defaults)?;
        return Ok(SchemaMigrationResult {
            file: file_path.display().to_string(),
            from_schema: OVERRIDES_SCHEMA_VERSION,
            to_schema: OVERRIDES_SCHEMA_VERSION,
            status: SchemaMigrationOutcome::Created,
        });
    }

    let content = fs::read_to_string(&file_path)?;
    let raw_value: Value = toml::from_str(&content)?;
    let from_schema = extract_schema_version(&raw_value, &file_path)?;

    if from_schema > OVERRIDES_SCHEMA_VERSION {
        return Err(self_invalid_input(format!(
            "Unsupported overrides schema version: {from_schema}"
        )));
    }

    if from_schema == OVERRIDES_SCHEMA_VERSION {
        let _: OverridesFile = toml::from_str(&content)?;
        return Ok(SchemaMigrationResult {
            file: file_path.display().to_string(),
            from_schema,
            to_schema: OVERRIDES_SCHEMA_VERSION,
            status: SchemaMigrationOutcome::AlreadyCurrent,
        });
    }

    let migrated = migrate_overrides_legacy(&raw_value, from_schema, &file_path)?;
    app.overrides.save(&migrated)?;

    Ok(SchemaMigrationResult {
        file: file_path.display().to_string(),
        from_schema,
        to_schema: OVERRIDES_SCHEMA_VERSION,
        status: SchemaMigrationOutcome::Upgraded,
    })
}

fn extract_schema_version(value: &Value, file_path: &Path) -> Result<u32> {
    let table = value.as_table().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected TOML table at document root (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    let Some(version_value) = table.get("schema_version") else {
        return Ok(0);
    };

    let version = version_value.as_integer().ok_or_else(|| {
        self_invalid_input(format!(
            "schema_version must be an integer (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(version_value)
        ))
    })?;

    if version < 0 {
        return Err(self_invalid_input(format!(
            "schema_version cannot be negative (file={}, value={version})",
            file_path.display()
        )));
    }

    Ok(version as u32)
}

fn migrate_settings_legacy(
    value: &Value,
    from_schema: u32,
    file_path: &Path,
) -> Result<SettingsFile> {
    if from_schema != 0 {
        return Err(self_invalid_input(format!(
            "Unsupported legacy settings schema version: {from_schema} (file={})",
            file_path.display()
        )));
    }

    let table = value.as_table().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected settings file to be a TOML table (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    let default_selector = optional_string(table, "default_selector", file_path)?;
    let linked_runtimes = string_table(table, "linked_runtimes", file_path)?;
    let tracked_selectors = string_array(table, "tracked_selectors", file_path)?;

    Ok(SettingsFile {
        schema_version: SETTINGS_SCHEMA_VERSION,
        default_selector,
        linked_runtimes,
        tracked_selectors,
    })
}

fn migrate_overrides_legacy(
    value: &Value,
    from_schema: u32,
    file_path: &Path,
) -> Result<OverridesFile> {
    if from_schema != 0 {
        return Err(self_invalid_input(format!(
            "Unsupported legacy overrides schema version: {from_schema} (file={})",
            file_path.display()
        )));
    }

    let table = value.as_table().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected overrides file to be a TOML table (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    let entries = if let Some(entries_value) = table.get("entries") {
        parse_override_entries(entries_value, file_path)?
    } else {
        Vec::new()
    };

    Ok(OverridesFile {
        schema_version: OVERRIDES_SCHEMA_VERSION,
        entries,
    })
}

fn optional_string(table: &Table, field: &str, file_path: &Path) -> Result<Option<String>> {
    let Some(value) = table.get(field) else {
        return Ok(None);
    };

    let string = value.as_str().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected '{field}' to be a string (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    Ok(Some(string.to_string()))
}

fn string_table(table: &Table, field: &str, file_path: &Path) -> Result<BTreeMap<String, String>> {
    let Some(value) = table.get(field) else {
        return Ok(BTreeMap::new());
    };

    let map = value.as_table().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected '{field}' to be a table (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    let mut result = BTreeMap::new();
    for (key, item) in map {
        let value = item.as_str().ok_or_else(|| {
            self_invalid_input(format!(
                "Expected '{field}.{key}' to be a string (file={}, actual_type={})",
                file_path.display(),
                toml_value_type(item)
            ))
        })?;
        result.insert(key.clone(), value.to_string());
    }

    Ok(result)
}

fn string_array(table: &Table, field: &str, file_path: &Path) -> Result<Vec<String>> {
    let Some(value) = table.get(field) else {
        return Ok(Vec::new());
    };

    let items = value.as_array().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected '{field}' to be an array (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    let mut result = Vec::new();
    for (index, item) in items.iter().enumerate() {
        let value = item.as_str().ok_or_else(|| {
            self_invalid_input(format!(
                "Expected '{field}[{index}]' to be a string (file={}, actual_type={})",
                file_path.display(),
                toml_value_type(item)
            ))
        })?;
        result.push(value.to_string());
    }

    Ok(result)
}

fn parse_override_entries(value: &Value, file_path: &Path) -> Result<Vec<OverrideEntry>> {
    let items = value.as_array().ok_or_else(|| {
        self_invalid_input(format!(
            "Expected 'entries' to be an array (file={}, actual_type={})",
            file_path.display(),
            toml_value_type(value)
        ))
    })?;

    let mut entries = Vec::new();
    for (index, item) in items.iter().enumerate() {
        let table = item.as_table().ok_or_else(|| {
            self_invalid_input(format!(
                "Expected 'entries[{index}]' to be a table (file={}, actual_type={})",
                file_path.display(),
                toml_value_type(item)
            ))
        })?;

        let path = table.get("path").and_then(Value::as_str).ok_or_else(|| {
            let actual_type = table.get("path").map(toml_value_type).unwrap_or("none");
            self_invalid_input(format!(
                "Expected 'entries[{index}].path' to be a string (file={}, \
                 actual_type={actual_type})",
                file_path.display()
            ))
        })?;

        let selector = table
            .get("selector")
            .and_then(Value::as_str)
            .ok_or_else(|| {
                let actual_type = table.get("selector").map(toml_value_type).unwrap_or("none");
                self_invalid_input(format!(
                    "Expected 'entries[{index}].selector' to be a string (file={}, \
                     actual_type={actual_type})",
                    file_path.display()
                ))
            })?;

        entries.push(OverrideEntry {
            path: path.to_string(),
            selector: selector.to_string(),
        });
    }

    Ok(entries)
}

fn toml_value_type(value: &Value) -> &'static str {
    match value {
        Value::String(_) => "string",
        Value::Integer(_) => "integer",
        Value::Float(_) => "float",
        Value::Boolean(_) => "boolean",
        Value::Datetime(_) => "datetime",
        Value::Array(_) => "array",
        Value::Table(_) => "table",
    }
}

fn log_failure(action: SelfAction, error: NodeupError) -> NodeupError {
    info!(
        command_path = action.command_path(),
        action = action.as_str(),
        outcome = "failed",
        error = %error.message,
        "Self command failed"
    );

    error
}
