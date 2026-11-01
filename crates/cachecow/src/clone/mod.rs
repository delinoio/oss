// SPDX-License-Identifier: Apache-2.0
//! Independent writable cache trees through filesystem copy-on-write
//! (Go `internal/clone`). Callers hold source and destination leases and
//! validate repository ownership.

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::Context;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Mode {
    #[default]
    Clone,
    Copy,
    Mixed,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CloneResult {
    #[serde(rename = "clonedFiles")]
    pub cloned_files: i64,
    #[serde(rename = "copiedFiles")]
    pub copied_files: i64,
    pub mode: Mode,
}

/// Unavailable copy-on-write capability. Only this error permits an explicitly
/// authorized ordinary copy; operational failures remain fatal.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
#[error(
    "copy-on-write is unavailable; use a supported same-volume cache or explicitly allow ordinary \
     copying"
)]
pub struct Unsupported;

/// Creates `destination` (which must not exist) as a clone of `source`, and
/// removes it on any error. Preserves permission bits and modification times;
/// rejects symlinks and special files; never creates hard links.
pub fn tree(
    ctx: &Context,
    source: &str,
    destination: &str,
    allow_copy: bool,
) -> Result<CloneResult> {
    let _ = (ctx, source, destination, allow_copy);
    todo!("clone::tree")
}
