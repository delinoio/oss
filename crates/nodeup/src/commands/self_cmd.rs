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

use crate::{
    cli::{OutputFormat, SelfCommand},
    commands::print_output,
    errors::{ErrorKind, NodeupError, Result},
    overrides::{OverrideEntry, OverridesFile, OVERRIDES_SCHEMA_VERSION},
    store::{SettingsFile, SETTINGS_SCHEMA_VERSION},
    NodeupApp,
};

const NODEUP_SELF_UPDATE_SOURCE: &str = "NODEUP_SELF_UPDATE_SOURCE";
const NODEUP_SELF_BIN_PATH: &str = "NODEUP_SELF_BIN_PATH";

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

pub fn execute(command: SelfCommand, output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    match command {
        SelfCommand::Update => update(output, app),
        SelfCommand::Uninstall => uninstall(output, app),
        SelfCommand::UpgradeData => upgrade_data(output, app),
    }
}

fn update(output: OutputFormat, _app: &NodeupApp) -> Result<i32> {
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
    print_output(output, &human, &response)?;

    Ok(0)
}

fn uninstall(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
    let action = SelfAction::Uninstall;

    let mut deletion_targets = Vec::new();
    for (path, bootstrap_child) in [
        (
            &app.paths.data_root,
            app.paths
                .toolchains_dir
                .file_name()
                .map(|value| value.to_os_string()),
        ),
        (
            &app.paths.cache_root,
            app.paths
                .downloads_dir
                .file_name()
                .map(|value| value.to_os_string()),
        ),
        (&app.paths.config_root, None),
    ] {
        let normalized_path =
            normalize_target_path(path).map_err(|error| log_failure(action, error))?;
        ensure_safe_uninstall_path(&normalized_path).map_err(|error| log_failure(action, error))?;

        let has_artifacts = if normalized_path.exists() {
            path_has_artifacts(&normalized_path, bootstrap_child.as_deref())
                .map_err(|error| log_failure(action, error))?
        } else {
            false
        };

        if has_artifacts {
            deletion_targets.push(normalized_path);
        }
    }

    let mut removed_paths = Vec::new();
    for target in deletion_targets {
        fs::remove_dir_all(&target).map_err(|error| {
            log_failure(
                action,
                NodeupError::new(
                    ErrorKind::Internal,
                    format!(
                        "Failed to remove uninstall target {}: {error}",
                        target.display()
                    ),
                ),
            )
        })?;
        removed_paths.push(target.display().to_string());
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

    let response = SelfUninstallResponse {
        action,
        status,
        removed_paths,
    };

    let human = format!(
        "Self uninstall status: {} (removed paths: {})",
        status.as_str(),
        response.removed_paths.len()
    );
    print_output(output, &human, &response)?;

    Ok(0)
}

fn upgrade_data(output: OutputFormat, app: &NodeupApp) -> Result<i32> {
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
    print_output(output, &human, &response)?;

    Ok(0)
}

fn resolve_update_source_path() -> Result<PathBuf> {
    let source = env::var_os(NODEUP_SELF_UPDATE_SOURCE).ok_or_else(|| {
        NodeupError::invalid_input(format!(
            "Self update source is not configured. Set {NODEUP_SELF_UPDATE_SOURCE} to a binary \
             path"
        ))
    })?;

    let source_path = PathBuf::from(source);
    if !source_path.exists() {
        return Err(NodeupError::not_found(format!(
            "Self update source does not exist: {}",
            source_path.display()
        )));
    }

    if !source_path.is_file() {
        return Err(NodeupError::invalid_input(format!(
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
        NodeupError::new(
            ErrorKind::Internal,
            format!("Failed to resolve current executable path: {error}"),
        )
    })
}

fn replace_binary(source: &Path, target: &Path) -> Result<()> {
    let parent = target.parent().ok_or_else(|| {
        NodeupError::internal(format!(
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
                NodeupError::new(
                    ErrorKind::Internal,
                    format!(
                        "Failed to clean stale backup {} before self update: {error}",
                        backup_target.display()
                    ),
                )
            })?;
        }

        fs::rename(target, &backup_target).map_err(|error| {
            NodeupError::new(
                ErrorKind::Internal,
                format!(
                    "Failed to stage existing binary {} for replacement: {error}",
                    target.display()
                ),
            )
        })?;

        if let Err(error) = fs::rename(&staged_path, target) {
            let rollback = fs::rename(&backup_target, target);
            return Err(NodeupError::new(
                ErrorKind::Internal,
                format!(
                    "Failed to replace binary {} with staged update: {error}. Rollback status: {}",
                    target.display(),
                    if rollback.is_ok() {
                        "restored-previous-binary"
                    } else {
                        "rollback-failed"
                    }
                ),
            ));
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
            NodeupError::new(
                ErrorKind::Internal,
                format!(
                    "Failed to replace binary {} with staged update: {error}",
                    target.display()
                ),
            )
        })?;
    }

    Ok(())
}

fn backup_target_path(target: &Path) -> Result<PathBuf> {
    let filename = target.file_name().ok_or_else(|| {
        NodeupError::invalid_input(format!(
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
        return Err(NodeupError::invalid_input(format!(
            "Refusing to uninstall unsafe path: {}",
            path.display()
        )));
    }

    let owned_by_nodeup = path.components().any(|component| match component {
        Component::Normal(value) => value.to_str().is_some_and(is_nodeup_owned_component),
        _ => false,
    });

    if !owned_by_nodeup {
        return Err(NodeupError::invalid_input(format!(
            "Refusing to uninstall non-nodeup-owned path: {}",
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
    let from_schema = extract_schema_version(&raw_value)?;

    if from_schema > SETTINGS_SCHEMA_VERSION {
        return Err(NodeupError::invalid_input(format!(
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

    let migrated = migrate_settings_legacy(&raw_value, from_schema)?;
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
    let from_schema = extract_schema_version(&raw_value)?;

    if from_schema > OVERRIDES_SCHEMA_VERSION {
        return Err(NodeupError::invalid_input(format!(
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

    let migrated = migrate_overrides_legacy(&raw_value, from_schema)?;
    app.overrides.save(&migrated)?;

    Ok(SchemaMigrationResult {
        file: file_path.display().to_string(),
        from_schema,
        to_schema: OVERRIDES_SCHEMA_VERSION,
        status: SchemaMigrationOutcome::Upgraded,
    })
}

fn extract_schema_version(value: &Value) -> Result<u32> {
    let table = value
        .as_table()
        .ok_or_else(|| NodeupError::invalid_input("Expected a TOML table at the document root"))?;

    let Some(version_value) = table.get("schema_version") else {
        return Ok(0);
    };

    let version = version_value
        .as_integer()
        .ok_or_else(|| NodeupError::invalid_input("schema_version must be an integer"))?;

    if version < 0 {
        return Err(NodeupError::invalid_input(
            "schema_version cannot be negative",
        ));
    }

    Ok(version as u32)
}

fn migrate_settings_legacy(value: &Value, from_schema: u32) -> Result<SettingsFile> {
    if from_schema != 0 {
        return Err(NodeupError::invalid_input(format!(
            "Unsupported legacy settings schema version: {from_schema}"
        )));
    }

    let table = value
        .as_table()
        .ok_or_else(|| NodeupError::invalid_input("Expected settings file to be a TOML table"))?;

    let default_selector = optional_string(table, "default_selector")?;
    let linked_runtimes = string_table(table, "linked_runtimes")?;
    let tracked_selectors = string_array(table, "tracked_selectors")?;

    Ok(SettingsFile {
        schema_version: SETTINGS_SCHEMA_VERSION,
        default_selector,
        linked_runtimes,
        tracked_selectors,
    })
}

fn migrate_overrides_legacy(value: &Value, from_schema: u32) -> Result<OverridesFile> {
    if from_schema != 0 {
        return Err(NodeupError::invalid_input(format!(
            "Unsupported legacy overrides schema version: {from_schema}"
        )));
    }

    let table = value
        .as_table()
        .ok_or_else(|| NodeupError::invalid_input("Expected overrides file to be a TOML table"))?;

    let entries = if let Some(entries_value) = table.get("entries") {
        parse_override_entries(entries_value)?
    } else {
        Vec::new()
    };

    Ok(OverridesFile {
        schema_version: OVERRIDES_SCHEMA_VERSION,
        entries,
    })
}

fn optional_string(table: &Table, field: &str) -> Result<Option<String>> {
    let Some(value) = table.get(field) else {
        return Ok(None);
    };

    let string = value
        .as_str()
        .ok_or_else(|| NodeupError::invalid_input(format!("Expected '{field}' to be a string")))?;

    Ok(Some(string.to_string()))
}

fn string_table(table: &Table, field: &str) -> Result<BTreeMap<String, String>> {
    let Some(value) = table.get(field) else {
        return Ok(BTreeMap::new());
    };

    let map = value
        .as_table()
        .ok_or_else(|| NodeupError::invalid_input(format!("Expected '{field}' to be a table")))?;

    let mut result = BTreeMap::new();
    for (key, item) in map {
        let value = item.as_str().ok_or_else(|| {
            NodeupError::invalid_input(format!("Expected '{field}.{key}' to be a string"))
        })?;
        result.insert(key.clone(), value.to_string());
    }

    Ok(result)
}

fn string_array(table: &Table, field: &str) -> Result<Vec<String>> {
    let Some(value) = table.get(field) else {
        return Ok(Vec::new());
    };

    let items = value
        .as_array()
        .ok_or_else(|| NodeupError::invalid_input(format!("Expected '{field}' to be an array")))?;

    let mut result = Vec::new();
    for (index, item) in items.iter().enumerate() {
        let value = item.as_str().ok_or_else(|| {
            NodeupError::invalid_input(format!("Expected '{field}[{index}]' to be a string"))
        })?;
        result.push(value.to_string());
    }

    Ok(result)
}

fn parse_override_entries(value: &Value) -> Result<Vec<OverrideEntry>> {
    let items = value
        .as_array()
        .ok_or_else(|| NodeupError::invalid_input("Expected 'entries' to be an array"))?;

    let mut entries = Vec::new();
    for (index, item) in items.iter().enumerate() {
        let table = item.as_table().ok_or_else(|| {
            NodeupError::invalid_input(format!("Expected 'entries[{index}]' to be a table"))
        })?;

        let path = table.get("path").and_then(Value::as_str).ok_or_else(|| {
            NodeupError::invalid_input(format!("Expected 'entries[{index}].path' to be a string"))
        })?;

        let selector = table
            .get("selector")
            .and_then(Value::as_str)
            .ok_or_else(|| {
                NodeupError::invalid_input(format!(
                    "Expected 'entries[{index}].selector' to be a string"
                ))
            })?;

        entries.push(OverrideEntry {
            path: path.to_string(),
            selector: selector.to_string(),
        });
    }

    Ok(entries)
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
