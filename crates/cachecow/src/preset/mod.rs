// SPDX-License-Identifier: Apache-2.0
//! Tool-owned cache discovery without modifying freshness metadata (Go
//! `internal/preset`). A successful discovery is not proof that an existing
//! cache was built with these settings: compare its recorded compatibility.

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::{
    Context,
    repository::{FileStamp, Info},
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Id {
    #[default]
    Cargo,
    Rspack,
    Rsbuild,
}

impl Id {
    pub const ALL: [Self; 3] = [Self::Cargo, Self::Rspack, Self::Rsbuild];

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Cargo => "cargo",
            Self::Rspack => "rspack",
            Self::Rsbuild => "rsbuild",
        }
    }

    pub fn parse(value: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|id| id.as_str() == value)
    }
}

impl std::fmt::Display for Id {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum RootKind {
    Target,
    Build,
    Persistent,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Root {
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Kinds")]
    pub kinds: Vec<RootKind>,
}

/// Compatibility evidence. Field names match the Go JSON encoding.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Compatibility {
    #[serde(rename = "Schema")]
    pub schema: i64,
    #[serde(rename = "Preset")]
    pub preset: Id,
    #[serde(rename = "ToolVersion")]
    pub tool_version: String,
    #[serde(rename = "CompilerVersion")]
    pub compiler_version: String,
    #[serde(rename = "NodeVersion")]
    pub node_version: String,
    #[serde(rename = "Platform")]
    pub platform: String,
    #[serde(rename = "Architecture")]
    pub architecture: String,
    #[serde(rename = "SettingsDigest")]
    pub settings_digest: String,
}

impl Compatibility {
    pub fn verified(&self) -> bool {
        todo!("preset::Compatibility::verified")
    }

    pub fn compatible(&self, other: &Self) -> bool {
        self.verified() && other.verified() && self == other
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CachePath {
    #[serde(rename = "ToolPath")]
    pub tool_path: String,
    #[serde(rename = "Canonical")]
    pub canonical: String,
}

/// Discovery result. Field names match the Go JSON encoding.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PresetResult {
    #[serde(rename = "CachePaths")]
    pub cache_paths: Vec<CachePath>,
    #[serde(rename = "Inputs")]
    pub inputs: Vec<FileStamp>,
    #[serde(rename = "Preset")]
    pub preset: Id,
    #[serde(rename = "Repository")]
    pub repository: Info,
    #[serde(rename = "Roots")]
    pub roots: Vec<Root>,
    #[serde(rename = "Compatibility")]
    pub compatibility: Compatibility,
}

/// Process adapter so fixtures can replace tool execution.
pub trait Runner: Sync {
    fn output(&self, ctx: &Context, cwd: &str, env: &[String], argv: &[String]) -> Result<Vec<u8>>;
}

/// Runs discovery commands, resolving PATH from the supplied environment.
pub struct ExecRunner;

impl Runner for ExecRunner {
    fn output(&self, ctx: &Context, cwd: &str, env: &[String], argv: &[String]) -> Result<Vec<u8>> {
        let _ = (ctx, cwd, env, argv);
        todo!("preset::ExecRunner::output")
    }
}

pub struct Request<'a> {
    pub cwd: String,
    /// Exact command including the executable; never persisted.
    pub argv: Vec<String>,
    /// Complete child environment. `None` inherits the current environment.
    pub env: Option<Vec<String>>,
    /// `None` selects from the executable basename.
    pub preset: Option<Id>,
    /// Settings storage, source caches and other forbidden paths.
    pub protected: Vec<String>,
    pub runner: Option<&'a dyn Runner>,
}

pub fn discover(ctx: &Context, request: Request<'_>) -> Result<PresetResult> {
    let _ = (ctx, request);
    todo!("preset::discover")
}

/// Binds tool-visible aliases to the leased resource names.
pub fn verify_cache_paths(result: &PresetResult) -> Result<()> {
    let _ = result;
    todo!("preset::verify_cache_paths")
}

/// Platform identifier recorded in compatibility (Go `GOOS` values).
pub const fn platform() -> &'static str {
    if cfg!(target_os = "macos") {
        "darwin"
    } else if cfg!(target_os = "windows") {
        "windows"
    } else if cfg!(target_os = "linux") {
        "linux"
    } else {
        std::env::consts::OS
    }
}

/// Architecture identifier recorded in compatibility (Go `GOARCH` values).
pub const fn architecture() -> &'static str {
    if cfg!(target_arch = "aarch64") {
        "arm64"
    } else if cfg!(target_arch = "x86_64") {
        "amd64"
    } else {
        std::env::consts::ARCH
    }
}
