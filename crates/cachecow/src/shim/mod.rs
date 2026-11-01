// SPDX-License-Identifier: Apache-2.0
//! Optional tool entry points (Go `internal/shim`). This module does not
//! execute commands or acquire cache locks; callers route managed and clean
//! operations through the shared lifecycle.

use anyhow::Result;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Tool {
    Cargo,
    Rspack,
    Rsbuild,
}

impl Tool {
    pub const ALL: [Self; 3] = [Self::Cargo, Self::Rspack, Self::Rsbuild];

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Cargo => "cargo",
            Self::Rspack => "rspack",
            Self::Rsbuild => "rsbuild",
        }
    }

    pub fn parse(value: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|t| t.as_str() == value)
    }
}

impl std::fmt::Display for Tool {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Recognizes installed native copies, including Windows `.exe` names.
pub fn from_argv0(argv0: &str) -> Option<Tool> {
    let _ = argv0;
    todo!("shim::from_argv0")
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Mode {
    Passthrough,
    Managed,
    /// Lock and wait, but never prepare or clone.
    Clean,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Reason {
    #[serde(rename = "cache-operation")]
    CacheOperation,
    #[serde(rename = "clean-operation")]
    CleanOperation,
    #[serde(rename = "query")]
    Query,
    #[serde(rename = "unknown-command")]
    Unknown,
    #[serde(rename = "outside-git")]
    OutsideGit,
    #[serde(rename = "bypass")]
    Bypass,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Classification {
    pub mode: Mode,
    pub reason: Reason,
}

/// Inspects arguments only; never removes or rewrites them.
pub fn classify(tool: Tool, args: &[String], in_git: bool, bypass: &str) -> Classification {
    let _ = (tool, args, in_git, bypass);
    todo!("shim::classify")
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum State {
    Missing,
    Owned,
    Conflict,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Entry {
    #[serde(rename = "Tool")]
    pub tool: Tool,
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "State")]
    pub state: State,
}

/// Content-verified ownership of each shim in `dir`.
pub fn list(dir: &str) -> Result<Vec<Entry>> {
    let _ = dir;
    todo!("shim::list")
}

/// Copies the native `ccow` executable to each shim name (idempotent).
pub fn install(dir: &str, executable: &str) -> Result<()> {
    let _ = (dir, executable);
    todo!("shim::install")
}

/// Removes only CacheCow-owned shims.
pub fn uninstall(dir: &str) -> Result<()> {
    let _ = dir;
    todo!("shim::uninstall")
}

/// Populated for each invocation, never pinned at installation.
#[derive(Debug, Clone, Default)]
pub struct ResolveOptions {
    pub path: String,
    pub path_ext: String,
    pub shim_dir: String,
    pub ccow_executable: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Candidate {
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Excluded")]
    pub excluded: bool,
    #[serde(rename = "Executable")]
    pub executable: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Resolution {
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Candidates")]
    pub candidates: Vec<Candidate>,
}

/// No usable real executable was found (exit 127), or one was denied (126).
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("{}", if *.denied { format!("{} exists but is not executable; correct its permissions or PATH", .tool) } else { format!("real {} executable not found; install the tool and add it to PATH after the CacheCow shims", .tool) })]
pub struct ResolveError {
    pub tool: Tool,
    pub denied: bool,
}

impl ResolveError {
    pub const fn exit_code(&self) -> i32 {
        if self.denied { 126 } else { 127 }
    }
}

/// A failed resolution keeps the candidates inspected so far.
#[derive(Debug)]
pub struct ResolveFailure {
    pub resolution: Resolution,
    pub error: anyhow::Error,
}

/// Selects the first executable in PATH after excluding CacheCow shims.
pub fn resolve(tool: Tool, options: &ResolveOptions) -> Result<Resolution, ResolveFailure> {
    let _ = (tool, options);
    todo!("shim::resolve")
}
