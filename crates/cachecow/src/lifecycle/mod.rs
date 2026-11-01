// SPDX-License-Identifier: Apache-2.0
//! Publishes cache trees only after every clone succeeds (Go
//! `internal/lifecycle`). Callers hold all source, destination and transaction
//! leases throughout.

use std::{fs, io};

use anyhow::{Result, anyhow, bail};
use serde::{Deserialize, Deserializer, Serialize};

use crate::{
    Context,
    clone::{self, CloneResult},
    paths::{self, Walk},
    state,
};

#[cfg(test)]
mod tests;

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

/// One staged publication. The JSON field names match the Go journal, which
/// embedded `Pair`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, default)]
struct Entry {
    source: String,
    destination: String,
    stage: String,
    backup: String,
    existing: bool,
}

/// Transaction journal stored at the plan's record path.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, default)]
struct Journal {
    #[serde(rename = "schemaVersion")]
    schema_version: i64,
    committed: bool,
    #[serde(deserialize_with = "null_as_empty")]
    entries: Vec<Entry>,
}

/// Go encoded an empty entry list as `null`.
fn null_as_empty<'de, D: Deserializer<'de>>(deserializer: D) -> Result<Vec<Entry>, D::Error> {
    Ok(Option::<Vec<Entry>>::deserialize(deserializer)?.unwrap_or_default())
}

/// Reports whether a cache directory exists; non-directories and symlinks are
/// errors that ask the user to repair the cache.
pub fn exists(path: &str) -> Result<bool> {
    match fs::symlink_metadata(path) {
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(false),
        Err(e) => Err(e.into()),
        Ok(m) if !m.is_dir() || m.file_type().is_symlink() => {
            bail!("cache {path} is not a directory; repair it before continuing")
        }
        Ok(_) => Ok(true),
    }
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
    recover(record, pairs)?;
    let mut journal = Journal {
        schema_version: 1,
        ..Journal::default()
    };
    let mut result = LifecycleResult {
        reused: true,
        trees: Vec::new(),
    };
    let id = uuid::Uuid::now_v7();
    for p in pairs {
        let existing = exists(&p.destination)?;
        if existing && !replace {
            continue;
        }
        if p.source == p.destination {
            if replace {
                bail!("cannot replace a cache from itself; select another source");
            }
            continue;
        }
        if !exists(&p.source)? {
            bail!("source cache unavailable; select another source or use ccow run interactively");
        }
        create_parent(&paths::dir(&p.destination))?;
        let suffix = format!(".ccow-{id}");
        journal.entries.push(Entry {
            source: p.source.clone(),
            destination: p.destination.clone(),
            stage: format!("{}{suffix}.stage", p.destination),
            backup: format!("{}{suffix}.backup", p.destination),
            existing,
        });
        result.reused = false;
    }
    if journal.entries.is_empty() {
        return Ok(result);
    }
    state::write(record, &journal)?;
    match publish(ctx, record, pairs, &mut journal, allow_copy, &mut result) {
        Ok(()) => {
            tracing::debug!(
                trees = result.trees.len(),
                replace,
                "published prepared cache trees"
            );
            Ok(result)
        }
        Err(error) => {
            tracing::debug!(error = %error, "cache preparation failed; recovering transaction");
            crate::error::join_result(Err(error), recover(record, pairs).err())
        }
    }
}

fn create_parent(path: &str) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        fs::DirBuilder::new()
            .recursive(true)
            .mode(0o755)
            .create(path)
    }
    #[cfg(not(unix))]
    {
        fs::create_dir_all(path)
    }
}

fn publish(
    ctx: &Context,
    record: &str,
    pairs: &[Pair],
    journal: &mut Journal,
    allow_copy: bool,
    result: &mut LifecycleResult,
) -> Result<()> {
    for v in &journal.entries {
        let tree = clone::tree(ctx, &v.source, &v.stage, allow_copy)?;
        sync_tree(&v.stage)?;
        result.trees.push(tree);
    }
    for v in &journal.entries {
        ctx.err()?;
        if v.existing {
            fs::rename(&v.destination, &v.backup)?;
        }
        sync_parent(&v.destination)?;
        fs::rename(&v.stage, &v.destination)?;
    }
    for v in &journal.entries {
        sync_parent(&v.destination)?;
    }
    journal.committed = true;
    state::write(record, journal)?;
    recover(record, pairs)
}

/// Rolls back an unfinished publication or removes backups of a committed one.
pub fn recover(record: &str, pairs: &[Pair]) -> Result<()> {
    let journal: Journal = match state::read(record) {
        Ok(j) => j,
        Err(e) if crate::error::is_not_found(&e) => return Ok(()),
        Err(e) => return Err(e),
    };
    if journal.schema_version != 1 {
        bail!("unsupported transaction schemaVersion; use a compatible ccow");
    }
    // A journal is not authority to delete arbitrary paths. Revalidate every
    // entry against the freshly discovered preset paths before any change.
    for v in &journal.entries {
        let found = pairs.iter().any(|p| p.destination == v.destination);
        let parent = paths::dir(&v.destination);
        if !found || paths::dir(&v.stage) != parent || paths::dir(&v.backup) != parent {
            bail!(
                "transaction paths differ from verified cache paths; retain journal for recovery"
            );
        }
        let prefix = format!("{}.ccow-", v.destination);
        let id = v
            .stage
            .strip_prefix(&prefix)
            .and_then(|suffix| suffix.strip_suffix(".stage"))
            .ok_or_else(|| anyhow!("invalid transaction stage"))?;
        let valid = uuid::Uuid::parse_str(id).is_ok_and(|u| u.get_version_num() == 7);
        if !valid || v.backup != format!("{prefix}{id}.backup") {
            bail!("invalid transaction backup");
        }
    }
    for v in journal.entries.iter().rev() {
        let backup = exists(&v.backup)?;
        let stage = exists(&v.stage)?;
        if journal.committed {
            remove_tree(&v.backup)?;
        } else if backup {
            remove_tree(&v.destination)?;
            fs::rename(&v.backup, &v.destination)?;
        } else if !v.existing && !stage {
            remove_tree(&v.destination)?;
        }
        remove_tree(&v.stage)?;
    }
    for v in &journal.entries {
        sync_parent(&v.destination)?;
    }
    fs::remove_file(record)?;
    sync_parent(record)
}

/// Removes a validated tree, first restoring owner write permission on every
/// directory without following symlinks, so read-only directories cannot block
/// removal (#4576). A symlink root is removed itself; its target is never
/// touched. A missing path is not an error.
pub fn remove_tree(path: &str) -> Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e.into()),
        Ok(m) => m,
    };
    if meta.file_type().is_symlink() {
        return remove_link(path);
    }
    paths::walk_dir(path, &mut |entry| {
        if entry.is_dir() {
            clone::set_owner_access(&entry.path, true)?;
        } else if cfg!(windows) && entry.file_type.is_file() {
            // Windows refuses to delete files with the read-only attribute.
            clone::set_owner_access(&entry.path, false)?;
        }
        Ok(Walk::Continue)
    })?;
    if meta.is_dir() {
        fs::remove_dir_all(path)?;
    } else {
        fs::remove_file(path)?;
    }
    tracing::debug!(path, "removed cache tree");
    Ok(())
}

fn remove_link(path: &str) -> Result<()> {
    #[cfg(windows)]
    {
        // Directory symbolic links and junctions are removed as directories.
        if fs::remove_file(path).is_ok() {
            return Ok(());
        }
        fs::remove_dir(path)?;
        Ok(())
    }
    #[cfg(not(windows))]
    {
        fs::remove_file(path)?;
        Ok(())
    }
}

#[cfg(unix)]
fn sync_tree(path: &str) -> Result<()> {
    paths::walk_dir(path, &mut |entry| {
        fs::File::open(&entry.path)?.sync_all()?;
        Ok(Walk::Continue)
    })
}

#[cfg(windows)]
fn sync_tree(path: &str) -> Result<()> {
    paths::walk_dir(path, &mut |entry| {
        if !entry.is_dir() {
            sync_staged_file(&entry.path)?;
        }
        Ok(Walk::Continue)
    })
}

/// Clone restores source permissions before returning. Windows requires a
/// writable handle for `FlushFileBuffers`, so clear only the read-only bit on
/// this private staging file and restore it even if open or flush fails.
#[cfg(windows)]
fn sync_staged_file(path: &str) -> Result<()> {
    let original = fs::metadata(path)?.permissions();
    let readonly = original.readonly();
    if readonly {
        let mut writable = original.clone();
        #[expect(
            clippy::permissions_set_readonly_false,
            reason = "temporarily clears only the Windows read-only attribute"
        )]
        writable.set_readonly(false);
        fs::set_permissions(path, writable)?;
    }
    let flushed = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(path)
        .and_then(|f| f.sync_all())
        .map_err(anyhow::Error::from);
    let restored = if readonly {
        fs::set_permissions(path, original)
            .err()
            .map(anyhow::Error::from)
    } else {
        None
    };
    crate::error::join_result(flushed, restored)
}

#[cfg(unix)]
fn sync_parent(path: &str) -> Result<()> {
    fs::File::open(paths::dir(path))?.sync_all()?;
    Ok(())
}

/// Windows offers no directory fsync. The journal remains durable, but sudden
/// storage loss still requires native filesystem acceptance.
#[cfg(windows)]
#[expect(clippy::unnecessary_wraps, reason = "matches the Unix signature")]
fn sync_parent(_path: &str) -> Result<()> {
    Ok(())
}
