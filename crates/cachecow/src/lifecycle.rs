// SPDX-License-Identifier: Apache-2.0
//! Publishes cache trees only after every clone succeeds (Go
//! `internal/lifecycle`). Callers hold all source, destination and transaction
//! leases throughout.

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::{Context, clone::CloneResult};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Pair {
    pub source: String,
    pub destination: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LifecycleResult {
    pub reused: bool,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub trees: Vec<CloneResult>,
}

/// Reports whether a cache directory exists; non-directories and symlinks are
/// errors that ask the user to repair the cache.
pub fn exists(path: &str) -> Result<bool> {
    let _ = path;
    todo!("lifecycle::exists")
}

/// Stages every requested tree before moving any existing cache. On failure
/// the old caches are preserved and staging is removed.
pub fn prepare(
    ctx: &Context,
    record: &str,
    pairs: &[Pair],
    replace: bool,
    allow_copy: bool,
) -> Result<LifecycleResult> {
    let _ = (ctx, record, pairs, replace, allow_copy);
    todo!("lifecycle::prepare")
}

/// Rolls back an unfinished publication or removes backups of a committed one.
pub fn recover(record: &str, pairs: &[Pair]) -> Result<()> {
    let _ = (record, pairs);
    todo!("lifecycle::recover")
}

/// Removes a validated tree, first restoring owner write permission on every
/// directory without following symlinks, so read-only directories cannot block
/// removal (#4576). A missing path is not an error.
pub fn remove_tree(path: &str) -> Result<()> {
    let _ = path;
    todo!("lifecycle::remove_tree")
}
