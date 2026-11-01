// SPDX-License-Identifier: Apache-2.0
//! Git worktree discovery and local cache boundary validation (Go
//! `internal/repository`). Discovery does not authorize mutation: callers
//! revalidate under their locks.

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::Context;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    #[default]
    Checkout,
    Bare,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SourceOrigin {
    Invocation,
    Saved,
    Primary,
}

/// Canonical repository identity. Field names match the Go JSON encoding.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Info {
    #[serde(rename = "CWD")]
    pub cwd: String,
    #[serde(rename = "Root")]
    pub root: String,
    #[serde(rename = "GitDir")]
    pub git_dir: String,
    #[serde(rename = "CommonDir")]
    pub common_dir: String,
    /// Empty for a bare repository, including its linked worktrees.
    #[serde(rename = "Primary")]
    pub primary: String,
    #[serde(rename = "Kind")]
    pub kind: Kind,
    /// SHA-256 of the canonical worktree Git directory.
    #[serde(rename = "ID")]
    pub id: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Source {
    pub repository: Info,
    pub origin: SourceOrigin,
    pub is_self: bool,
}

/// One input file's path, metadata and content digest (never its contents).
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileStamp {
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Exists")]
    pub exists: bool,
    #[serde(rename = "Mode")]
    pub mode: u32,
    #[serde(rename = "Size")]
    pub size: i64,
    #[serde(rename = "Modified")]
    pub modified: i64,
    #[serde(rename = "Digest")]
    pub digest: String,
}

/// Ephemeral same-user validation token captured before requesting a lease.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Snapshot {
    #[serde(rename = "Schema")]
    pub schema: i64,
    #[serde(rename = "Repository")]
    pub repository: Info,
    #[serde(rename = "Worktrees")]
    pub worktrees: Vec<Info>,
    #[serde(rename = "IgnoredDirectories")]
    pub ignored_directories: Vec<String>,
    #[serde(rename = "Caches")]
    pub caches: Vec<String>,
    #[serde(rename = "Protected")]
    pub protected: Vec<String>,
    #[serde(rename = "Files")]
    pub files: Vec<FileStamp>,
    #[serde(rename = "InventoryDigest")]
    pub inventory_digest: String,
}

/// Discovers the canonical repository for `cwd` (runs Git).
pub fn discover(ctx: &Context, cwd: &str) -> Result<Info> {
    let _ = (ctx, cwd);
    todo!("repository::discover")
}

/// Invocation > saved setting > primary checkout. An invalid explicit
/// selection never falls back to a different source.
pub fn select_source(
    ctx: &Context,
    current: &Info,
    invocation: &str,
    saved: &str,
) -> Result<Source> {
    let _ = (ctx, current, invocation, saved);
    todo!("repository::select_source")
}

/// Resolves existing ancestors so aliases of absent caches share an identity.
/// Rejects `..` components and dangling links.
pub fn canonical(path: &str) -> Result<String> {
    let _ = path;
    todo!("repository::canonical")
}

/// Lexical containment (`child` equals or is below `parent`).
pub fn contains(parent: &str, child: &str) -> bool {
    crate::paths::lexically_within(parent, child)
}

pub fn overlaps(a: &str, b: &str) -> bool {
    contains(a, b) || contains(b, a)
}

/// Checks that the existing object (or nearest existing parent) is owned by
/// the current user.
pub fn check_owner(path: &str) -> Result<()> {
    let _ = path;
    todo!("repository::check_owner")
}

impl Info {
    /// Protects worktree roots, Git metadata, tracked/unignored files and the
    /// supplied protected paths; returns the canonical cache path.
    pub fn validate_cache(
        &self,
        ctx: &Context,
        path: &str,
        protected: &[String],
    ) -> Result<String> {
        let _ = (ctx, path, protected);
        todo!("repository::Info::validate_cache")
    }
}

/// Filesystem-only repository identity verification (no subprocesses).
pub fn verify(info: &Info) -> Result<()> {
    let _ = info;
    todo!("repository::verify")
}

/// Filesystem-only cache claim recheck (no subprocesses).
pub fn verify_cache_claim(info: &Info, path: &str, protected: &[String]) -> Result<String> {
    let _ = (info, path, protected);
    todo!("repository::verify_cache_claim")
}

/// Captures index, ignore rules, Git configuration and source namespace.
pub fn capture_snapshot(
    ctx: &Context,
    info: &Info,
    caches: &[String],
    protected: &[String],
) -> Result<Snapshot> {
    let _ = (ctx, info, caches, protected);
    todo!("repository::capture_snapshot")
}

impl Snapshot {
    /// Filesystem-only verification under the leases.
    pub fn verify(&self) -> Result<()> {
        todo!("repository::Snapshot::verify")
    }
}

/// Stamps every path, including absent candidates.
pub fn capture_file_stamps(paths: &[String]) -> Result<Vec<FileStamp>> {
    let _ = paths;
    todo!("repository::capture_file_stamps")
}

pub fn verify_file_stamps(stamps: &[FileStamp]) -> Result<()> {
    let _ = stamps;
    todo!("repository::verify_file_stamps")
}
