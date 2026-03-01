use std::{
    cmp::Reverse,
    fs,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

use crate::{
    errors::{NodeupError, Result},
    paths::NodeupPaths,
};

pub const OVERRIDES_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OverrideEntry {
    pub path: String,
    pub selector: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OverridesFile {
    pub schema_version: u32,
    pub entries: Vec<OverrideEntry>,
}

impl Default for OverridesFile {
    fn default() -> Self {
        Self {
            schema_version: OVERRIDES_SCHEMA_VERSION,
            entries: Vec::new(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct OverrideStore {
    paths: NodeupPaths,
}

impl OverrideStore {
    pub fn new(paths: NodeupPaths) -> Self {
        Self { paths }
    }

    pub fn load(&self) -> Result<OverridesFile> {
        if !self.paths.overrides_file.exists() {
            return Ok(OverridesFile::default());
        }

        let content = fs::read_to_string(&self.paths.overrides_file)?;
        let file: OverridesFile = toml::from_str(&content)?;
        if file.schema_version != OVERRIDES_SCHEMA_VERSION {
            return Err(NodeupError::invalid_input(format!(
                "Unsupported overrides schema version: {}",
                file.schema_version
            )));
        }

        Ok(file)
    }

    pub fn save(&self, overrides: &OverridesFile) -> Result<()> {
        let serialized = toml::to_string_pretty(overrides)?;
        fs::write(&self.paths.overrides_file, serialized)?;
        Ok(())
    }

    pub fn list(&self) -> Result<Vec<OverrideEntry>> {
        Ok(self.load()?.entries)
    }

    pub fn set(&self, path: &Path, selector: &str) -> Result<()> {
        let absolute_path = canonical_or_absolute(path)?;

        let mut file = self.load()?;
        file.entries.retain(|entry| entry.path != absolute_path);
        file.entries.push(OverrideEntry {
            path: absolute_path,
            selector: selector.to_string(),
        });

        file.entries.sort_by_key(|entry| entry.path.clone());
        self.save(&file)
    }

    pub fn unset(
        &self,
        path: Option<&Path>,
        remove_nonexistent: bool,
    ) -> Result<Vec<OverrideEntry>> {
        let mut file = self.load()?;
        let mut removed = Vec::new();

        if remove_nonexistent {
            let mut retained = Vec::new();
            for entry in file.entries {
                if PathBuf::from(&entry.path).exists() {
                    retained.push(entry);
                } else {
                    removed.push(entry);
                }
            }
            file.entries = retained;
            self.save(&file)?;
            return Ok(removed);
        }

        let target = if let Some(path) = path {
            canonical_or_absolute(path)?
        } else {
            canonical_or_absolute(&std::env::current_dir()?)?
        };

        let mut retained = Vec::new();
        for entry in file.entries {
            if entry.path == target {
                removed.push(entry);
            } else {
                retained.push(entry);
            }
        }
        file.entries = retained;
        self.save(&file)?;
        Ok(removed)
    }

    pub fn resolve_for_path(&self, path: &Path) -> Result<Option<OverrideEntry>> {
        let absolute = canonical_or_absolute_path(path)?;
        let mut entries = self.load()?.entries;

        entries.sort_by_key(|entry| Reverse(entry.path.len()));

        for entry in entries {
            let candidate = PathBuf::from(&entry.path);
            if absolute == candidate || absolute.starts_with(&candidate) {
                return Ok(Some(entry));
            }
        }

        Ok(None)
    }
}

fn canonical_or_absolute(path: &Path) -> Result<String> {
    let normalized = canonical_or_absolute_path(path)?;
    normalized
        .to_str()
        .map(|value| value.to_string())
        .ok_or_else(|| {
            NodeupError::invalid_input(format!("Path is not valid UTF-8: {}", normalized.display()))
        })
}

fn canonical_or_absolute_path(path: &Path) -> Result<PathBuf> {
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        std::env::current_dir()?.join(path)
    };

    let normalized = if absolute.exists() {
        absolute.canonicalize()?
    } else {
        canonicalize_nonexistent_path(&absolute)?
    };

    Ok(normalized)
}

fn canonicalize_nonexistent_path(path: &Path) -> Result<PathBuf> {
    let mut missing_parts = Vec::new();
    let mut cursor = path;

    while !cursor.exists() {
        let Some(file_name) = cursor.file_name() else {
            return Err(NodeupError::invalid_input(format!(
                "Cannot canonicalize path with missing root: {}",
                path.display()
            )));
        };
        missing_parts.push(file_name.to_os_string());
        cursor = cursor.parent().ok_or_else(|| {
            NodeupError::invalid_input(format!(
                "Cannot canonicalize path without parent: {}",
                path.display()
            ))
        })?;
    }

    let mut canonical = cursor.canonicalize()?;
    for part in missing_parts.iter().rev() {
        canonical.push(part);
    }

    Ok(canonical)
}

#[cfg(test)]
mod tests {
    use std::time::{SystemTime, UNIX_EPOCH};

    use super::*;
    use crate::paths::NodeupPaths;

    fn temp_root(name: &str) -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        std::env::temp_dir().join(format!("nodeup-overrides-{name}-{nonce}"))
    }

    #[test]
    fn resolve_prefers_most_specific_path() {
        let root = temp_root("specific");
        let paths = NodeupPaths {
            data_root: root.join("data"),
            cache_root: root.join("cache"),
            config_root: root.join("config"),
            toolchains_dir: root.join("data").join("toolchains"),
            downloads_dir: root.join("cache").join("downloads"),
            settings_file: root.join("config").join("settings.toml"),
            overrides_file: root.join("config").join("overrides.toml"),
        };
        paths.ensure_layout().unwrap();

        let store = OverrideStore::new(paths);
        let workspace = root.join("workspace");
        let nested = workspace.join("nested");
        fs::create_dir_all(&nested).unwrap();

        store.set(&workspace, "lts").unwrap();
        store.set(&nested, "v22.1.0").unwrap();

        let resolved = store
            .resolve_for_path(&nested.join("src"))
            .unwrap()
            .unwrap();
        assert_eq!(resolved.selector, "v22.1.0");

        let _ = fs::remove_dir_all(root);
    }
}
