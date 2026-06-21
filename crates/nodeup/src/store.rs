#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;
use std::{
    collections::{BTreeMap, BTreeSet},
    env, fs,
    io::Write,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};
use tempfile::NamedTempFile;

use crate::{
    errors::{NodeupError, Result},
    paths::NodeupPaths,
    selectors::RuntimeSelector,
};

pub const SETTINGS_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SettingsFile {
    pub schema_version: u32,
    pub default_selector: Option<String>,
    pub linked_runtimes: BTreeMap<String, String>,
    pub tracked_selectors: Vec<String>,
}

impl Default for SettingsFile {
    fn default() -> Self {
        Self {
            schema_version: SETTINGS_SCHEMA_VERSION,
            default_selector: None,
            linked_runtimes: BTreeMap::new(),
            tracked_selectors: Vec::new(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct Store {
    paths: NodeupPaths,
}

impl Store {
    pub fn new(paths: NodeupPaths) -> Self {
        Self { paths }
    }

    pub fn load_settings(&self) -> Result<SettingsFile> {
        if !self.paths.settings_file.exists() {
            return Ok(SettingsFile::default());
        }

        let content = fs::read_to_string(&self.paths.settings_file)?;
        let file: SettingsFile = toml::from_str(&content)?;
        if file.schema_version != SETTINGS_SCHEMA_VERSION {
            return Err(NodeupError::invalid_input_with_hint(
                format!(
                    "Unsupported settings schema version: {}",
                    file.schema_version
                ),
                "Run `nodeup self upgrade-data` to migrate local data, then retry.",
            ));
        }

        Ok(SettingsFile {
            tracked_selectors: canonical_tracked_selectors(file.tracked_selectors),
            ..file
        })
    }

    pub fn save_settings(&self, settings: &SettingsFile) -> Result<()> {
        let serialized = toml::to_string_pretty(settings)?;
        atomic_write(&self.paths.settings_file, serialized.as_bytes())
    }

    pub fn track_selector(&self, selector: &str) -> Result<()> {
        let mut settings = self.load_settings()?;
        settings.tracked_selectors.push(selector.to_string());
        settings.tracked_selectors = canonical_tracked_selectors(settings.tracked_selectors);
        self.save_settings(&settings)
    }

    pub fn list_installed_versions(&self) -> Result<Vec<String>> {
        if !self.paths.toolchains_dir.exists() {
            return Ok(Vec::new());
        }

        let mut versions = Vec::new();
        for entry in fs::read_dir(&self.paths.toolchains_dir)? {
            let entry = entry?;
            if entry.file_type()?.is_dir() {
                if let Some(name) = entry.file_name().to_str() {
                    versions.push(name.to_string());
                }
            }
        }
        versions.sort();
        Ok(versions)
    }

    pub fn is_installed(&self, version: &str) -> bool {
        self.runtime_dir(version).exists()
    }

    pub fn runtime_dir(&self, version: &str) -> PathBuf {
        self.paths.runtime_dir(version)
    }

    pub fn runtime_executable(&self, version: &str, command: &str) -> PathBuf {
        runtime_executable_path(&self.runtime_dir(version), command)
    }

    pub fn remove_runtime(&self, version: &str) -> Result<()> {
        let runtime_dir = self.runtime_dir(version);
        if !runtime_dir.exists() {
            return Err(NodeupError::not_found_with_hint(
                format!("Runtime {version} is not installed"),
                "List installed runtimes with `nodeup toolchain list` and retry with a valid \
                 version.",
            ));
        }

        fs::remove_dir_all(runtime_dir)?;
        Ok(())
    }

    pub fn paths(&self) -> &NodeupPaths {
        &self.paths
    }
}

fn canonical_tracked_selectors(selectors: Vec<String>) -> Vec<String> {
    let mut deduplicated = BTreeSet::new();
    for selector in selectors {
        deduplicated.insert(canonical_tracked_selector(&selector));
    }

    deduplicated.into_iter().collect()
}

fn canonical_tracked_selector(selector: &str) -> String {
    match RuntimeSelector::parse(selector) {
        Ok(RuntimeSelector::Version(version)) => format!("v{version}"),
        Ok(RuntimeSelector::Channel(channel)) => channel.to_string(),
        Ok(RuntimeSelector::LinkedName(name)) => name,
        Err(_) => selector.to_string(),
    }
}

pub fn runtime_executable_path(runtime_root: &Path, command: &str) -> PathBuf {
    let bin_dir = runtime_root.join("bin");
    for candidate in runtime_executable_candidates(command) {
        let candidate_path = bin_dir.join(&candidate);
        if candidate_path.exists() {
            return candidate_path;
        }
    }

    bin_dir.join(runtime_primary_executable_name(command))
}

pub fn runtime_executable_is_runnable(path: &Path) -> bool {
    if !path.is_file() {
        return false;
    }

    if runtime_host_is_windows() {
        return true;
    }

    executable_permission_is_set(path)
}

#[cfg(unix)]
fn executable_permission_is_set(path: &Path) -> bool {
    path.metadata()
        .map(|metadata| metadata.permissions().mode() & 0o111 != 0)
        .unwrap_or(false)
}

#[cfg(not(unix))]
fn executable_permission_is_set(path: &Path) -> bool {
    path.exists()
}

fn runtime_executable_candidates(command: &str) -> Vec<String> {
    let primary = runtime_primary_executable_name(command);
    if primary == command {
        vec![primary]
    } else {
        vec![primary, command.to_string()]
    }
}

fn runtime_primary_executable_name(command: &str) -> String {
    if runtime_host_is_windows() {
        match command {
            "node" => "node.exe".to_string(),
            "npm" | "npx" | "yarn" | "pnpm" | "corepack" => format!("{command}.cmd"),
            _ => format!("{command}.exe"),
        }
    } else {
        command.to_string()
    }
}

fn runtime_host_is_windows() -> bool {
    match env::var("NODEUP_FORCE_PLATFORM") {
        Ok(value) => value.starts_with("windows-"),
        Err(_) => cfg!(windows),
    }
}

fn atomic_write(path: &Path, content: &[u8]) -> Result<()> {
    let parent = path.parent().ok_or_else(|| {
        NodeupError::internal_with_hint(
            format!("Cannot determine parent directory for {}", path.display()),
            "Check the configured nodeup data paths and retry.",
        )
    })?;

    fs::create_dir_all(parent)?;
    let mut temp_file = NamedTempFile::new_in(parent)?;
    temp_file.write_all(content)?;
    temp_file.flush()?;

    temp_file.persist(path).map_err(|error| {
        NodeupError::internal_with_hint(
            format!("Failed to persist {}: {error}", path.display()),
            "Ensure the destination directory is writable, then retry.",
        )
    })?;

    Ok(())
}
