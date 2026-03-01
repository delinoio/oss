use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Write,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};
use tempfile::NamedTempFile;

use crate::{
    errors::{NodeupError, Result},
    paths::NodeupPaths,
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
            return Err(NodeupError::invalid_input(format!(
                "Unsupported settings schema version: {}",
                file.schema_version
            )));
        }

        Ok(file)
    }

    pub fn save_settings(&self, settings: &SettingsFile) -> Result<()> {
        let serialized = toml::to_string_pretty(settings)?;
        atomic_write(&self.paths.settings_file, serialized.as_bytes())
    }

    pub fn track_selector(&self, selector: &str) -> Result<()> {
        let mut settings = self.load_settings()?;
        let mut deduplicated: BTreeSet<String> = settings.tracked_selectors.into_iter().collect();
        deduplicated.insert(selector.to_string());
        settings.tracked_selectors = deduplicated.into_iter().collect();
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
        self.runtime_dir(version).join("bin").join(command)
    }

    pub fn remove_runtime(&self, version: &str) -> Result<()> {
        let runtime_dir = self.runtime_dir(version);
        if !runtime_dir.exists() {
            return Err(NodeupError::not_found(format!(
                "Runtime {version} is not installed"
            )));
        }

        fs::remove_dir_all(runtime_dir)?;
        Ok(())
    }

    pub fn paths(&self) -> &NodeupPaths {
        &self.paths
    }
}

fn atomic_write(path: &Path, content: &[u8]) -> Result<()> {
    let parent = path.parent().ok_or_else(|| {
        NodeupError::internal(format!(
            "Cannot determine parent directory for {}",
            path.display()
        ))
    })?;

    fs::create_dir_all(parent)?;
    let mut temp_file = NamedTempFile::new_in(parent)?;
    temp_file.write_all(content)?;
    temp_file.flush()?;

    temp_file.persist(path).map_err(|error| {
        NodeupError::internal(format!("Failed to persist {}: {error}", path.display()))
    })?;

    Ok(())
}
