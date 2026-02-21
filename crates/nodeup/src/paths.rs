use std::{
    env, fs,
    path::{Path, PathBuf},
};

use crate::errors::{NodeupError, Result};

#[derive(Debug, Clone)]
pub struct NodeupPaths {
    pub data_root: PathBuf,
    pub cache_root: PathBuf,
    pub config_root: PathBuf,
    pub toolchains_dir: PathBuf,
    pub downloads_dir: PathBuf,
    pub settings_file: PathBuf,
    pub overrides_file: PathBuf,
}

impl NodeupPaths {
    pub fn detect() -> Result<Self> {
        let data_root = env_path("NODEUP_DATA_HOME").unwrap_or_else(default_data_root);
        let cache_root = env_path("NODEUP_CACHE_HOME").unwrap_or_else(default_cache_root);
        let config_root = env_path("NODEUP_CONFIG_HOME").unwrap_or_else(default_config_root);

        let toolchains_dir = data_root.join("toolchains");
        let downloads_dir = cache_root.join("downloads");
        let settings_file = config_root.join("settings.toml");
        let overrides_file = config_root.join("overrides.toml");

        Ok(Self {
            data_root,
            cache_root,
            config_root,
            toolchains_dir,
            downloads_dir,
            settings_file,
            overrides_file,
        })
    }

    pub fn ensure_layout(&self) -> Result<()> {
        for dir in [
            &self.data_root,
            &self.cache_root,
            &self.config_root,
            &self.toolchains_dir,
            &self.downloads_dir,
        ] {
            fs::create_dir_all(dir)?;
            ensure_secure_directory_permissions(dir)?;
        }

        Ok(())
    }

    pub fn normalize_runtime_version(version: &str) -> String {
        if version.starts_with('v') {
            version.to_string()
        } else {
            format!("v{version}")
        }
    }

    pub fn runtime_dir(&self, version: &str) -> PathBuf {
        self.toolchains_dir
            .join(Self::normalize_runtime_version(version))
    }
}

fn env_path(name: &str) -> Option<PathBuf> {
    env::var_os(name).map(PathBuf::from)
}

fn default_data_root() -> PathBuf {
    if cfg!(windows) {
        env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| home_dir().join("AppData").join("Local"))
            .join("nodeup")
            .join("data")
    } else {
        env::var_os("XDG_DATA_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| home_dir().join(".local").join("share"))
            .join("nodeup")
    }
}

fn default_cache_root() -> PathBuf {
    if cfg!(windows) {
        env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| home_dir().join("AppData").join("Local"))
            .join("nodeup")
            .join("cache")
    } else {
        env::var_os("XDG_CACHE_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| home_dir().join(".cache"))
            .join("nodeup")
    }
}

fn default_config_root() -> PathBuf {
    if cfg!(windows) {
        env::var_os("APPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| home_dir().join("AppData").join("Roaming"))
            .join("nodeup")
            .join("config")
    } else {
        env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| home_dir().join(".config"))
            .join("nodeup")
    }
}

fn home_dir() -> PathBuf {
    env::var_os("HOME")
        .map(PathBuf::from)
        .or_else(|| env::var_os("USERPROFILE").map(PathBuf::from))
        .unwrap_or_else(|| PathBuf::from("."))
}

fn ensure_secure_directory_permissions(path: &Path) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;

        let permissions = fs::Permissions::from_mode(0o700);
        fs::set_permissions(path, permissions)?;
    }

    if !path.exists() {
        return Err(NodeupError::internal(format!(
            "Failed to create required directory: {}",
            path.display()
        )));
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_version_adds_v_prefix() {
        assert_eq!(NodeupPaths::normalize_runtime_version("22.0.0"), "v22.0.0");
        assert_eq!(NodeupPaths::normalize_runtime_version("v22.0.0"), "v22.0.0");
    }
}
