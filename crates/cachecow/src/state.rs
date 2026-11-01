// SPDX-License-Identifier: Apache-2.0
//! Versioned, current-user-private CacheCow records (Go `internal/state`).

use anyhow::Result;
use serde::{Deserialize, Serialize, de::DeserializeOwned};

/// Root of current-user-private CacheCow storage.
#[derive(Debug, Clone)]
pub struct Store {
    pub root: String,
}

/// Per-worktree settings record `settings-<key>.json`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Settings {
    #[serde(rename = "schemaVersion", default = "one")]
    pub schema_version: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source: String,
}

const fn one() -> i64 {
    1
}

impl Store {
    /// Opens (creating privately) the state root. An empty `root` selects
    /// `user_config_dir()/cachecow`, or `user_cache_dir()/cachecow` on Windows.
    pub fn open(root: &str) -> Result<Self> {
        let _ = root;
        todo!("state::Store::open")
    }

    /// Ensures the private subdirectory `name` exists and returns its path.
    pub fn directory(&self, name: &str) -> Result<String> {
        let _ = name;
        todo!("state::Store::directory")
    }

    /// Reads the saved settings for a worktree ID; absent records yield
    /// defaults.
    pub fn settings(&self, worktree: &str) -> Result<Settings> {
        let _ = worktree;
        todo!("state::Store::settings")
    }

    /// Saves (or clears with an empty string) the source selection.
    pub fn set_source(&self, worktree: &str, source: &str) -> Result<()> {
        let _ = (worktree, source);
        todo!("state::Store::set_source")
    }
}

/// SHA-256 hex digest used for record names.
pub fn key(value: &str) -> String {
    crate::jsonx::sha256_hex(value.as_bytes())
}

/// Validates privacy, then strictly decodes one record. A missing file keeps
/// its I/O `NotFound` cause (see [`crate::error::is_not_found`]).
pub fn read<T: DeserializeOwned>(path: &str) -> Result<T> {
    let _ = path;
    todo!("state::read")
}

/// Atomically replaces a private record (temporary file, fsync, rename, and
/// parent directory sync on Unix).
pub fn write<T: Serialize>(path: &str, value: &T) -> Result<()> {
    let _ = (path, value);
    todo!("state::write")
}
