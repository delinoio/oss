// SPDX-License-Identifier: Apache-2.0
//! Independent writable cache trees through filesystem copy-on-write
//! (Go `internal/clone`). Callers hold source and destination leases and
//! validate repository ownership.

use std::{
    fs::{self, File, OpenOptions},
    io::{self, Read, Seek, SeekFrom, Write},
};

use anyhow::{Result, bail};
use serde::{Deserialize, Serialize};

use crate::{
    Context,
    context::CancelError,
    paths::{self, Walk},
};

mod chunks;
#[cfg(target_os = "linux")]
#[path = "native_linux.rs"]
mod native;
#[cfg(target_os = "macos")]
#[path = "native_macos.rs"]
mod native;
#[cfg(not(any(target_os = "linux", target_os = "macos", windows)))]
#[path = "native_other.rs"]
mod native;
#[cfg(windows)]
#[path = "native_windows.rs"]
mod native;
#[cfg(test)]
mod tests;
#[cfg(unix)]
#[path = "walk_unix.rs"]
mod walk;
#[cfg(windows)]
#[path = "walk_windows.rs"]
mod walk;

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

/// `Unsupported` joined with the native cause, like Go's
/// `errors.Join(ErrUnsupported, err)`.
#[cfg_attr(
    not(any(target_os = "linux", target_os = "macos", windows)),
    allow(dead_code)
)]
pub(crate) fn unsupported(cause: io::Error) -> anyhow::Error {
    anyhow::Error::new(crate::error::Joined(vec![Unsupported.into(), cause.into()]))
}

/// Creates `destination` (which must not exist) as a clone of `source`, and
/// removes it on any error. Preserves permission bits and modification times;
/// rejects symlinks and special files; never creates hard links. The caller
/// owns publication of this staging tree and must prevent concurrent writes.
pub fn tree(
    ctx: &Context,
    source: &str,
    destination: &str,
    allow_copy: bool,
) -> Result<CloneResult> {
    tree_with(
        ctx,
        source,
        destination,
        allow_copy,
        &mut native::clone_file,
    )
}

/// Clones one open regular file to the absent path `destination`.
pub(crate) type CloneFile<'a> = &'a mut dyn FnMut(&Context, &File, &str) -> Result<()>;

/// Formats `message: cause` like Go's `fmt.Errorf("%s: %w")` while keeping the
/// typed cause chain searchable.
pub(crate) fn wrap(error: impl Into<anyhow::Error>, message: &str) -> anyhow::Error {
    let error = error.into();
    let text = format!("{message}: {error}");
    error.context(text)
}

pub(crate) fn tree_with(
    ctx: &Context,
    source: &str,
    destination: &str,
    allow_copy: bool,
    clone_file: CloneFile<'_>,
) -> Result<CloneResult> {
    ctx.err()?;
    let source = paths::eval_symlinks(source).map_err(|e| wrap(e, "resolve source cache"))?;
    let source = paths::abs(&source)?;
    let destination = paths::abs(destination)?;
    let parent = paths::eval_symlinks(&paths::dir(&destination))
        .map_err(|e| wrap(e, "resolve destination parent"))?;
    let destination = paths::join2(&parent, &paths::base(&destination));
    if overlaps(&source, &destination) {
        bail!("source and destination overlap; choose separate cache trees");
    }
    let root = walk::SourceRoot::open(&source).map_err(|e| wrap(e, "open source cache"))?;
    // String comparison cannot detect case aliases or bind-mounted source
    // roots. Compare existing ancestor identities before creating anything.
    let source_id = root.id()?;
    let mut ancestor = parent;
    loop {
        if paths::file_id(&ancestor, true)? == source_id {
            bail!("destination is within the source cache; choose separate cache trees");
        }
        let next = paths::dir(&ancestor);
        if next == ancestor {
            break;
        }
        ancestor = next;
    }
    make_dir(&destination).map_err(|e| wrap(e, "create absent destination cache"))?;
    let mut result = CloneResult::default();
    let walked = root
        .walk(ctx, &destination, allow_copy, clone_file, &mut result)
        .and_then(|()| Ok(ctx.err()?));
    if let Err(error) = walked {
        // Finalized directories may be read-only. Restore owner access only in
        // this staging tree so failure cleanup can remove their children.
        let restored = paths::walk_dir(&destination, &mut |entry| {
            if !entry.file_type.is_symlink() {
                set_owner_access(&entry.path, entry.is_dir())?;
            }
            Ok(Walk::Continue)
        })
        .err();
        let removed = fs::remove_dir_all(&destination)
            .err()
            .map(anyhow::Error::from);
        return Err(crate::error::join([Some(error), restored, removed])
            .unwrap_or_else(|| anyhow::anyhow!("cache preparation failed")));
    }
    if result.copied_files > 0 {
        result.mode = if result.cloned_files > 0 {
            Mode::Mixed
        } else {
            Mode::Copy
        };
    }
    tracing::debug!(
        mode = ?result.mode,
        cloned_files = result.cloned_files,
        copied_files = result.copied_files,
        "prepared cache tree"
    );
    Ok(result)
}

fn overlaps(a: &str, b: &str) -> bool {
    paths::lexically_within(a, b) || paths::lexically_within(b, a)
}

/// Creates a private (0700) directory.
pub(crate) fn make_dir(path: &str) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        fs::DirBuilder::new().mode(0o700).create(path)
    }
    #[cfg(not(unix))]
    {
        fs::create_dir(path)
    }
}

/// Restores owner access: directories 0700 and files 0600, or on Windows
/// clears the read-only attribute.
pub(crate) fn set_owner_access(path: &str, directory: bool) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = if directory { 0o700 } else { 0o600 };
        fs::set_permissions(path, fs::Permissions::from_mode(mode))
    }
    #[cfg(not(unix))]
    {
        let _ = directory;
        let mut permissions = fs::symlink_metadata(path)?.permissions();
        if permissions.readonly() {
            #[expect(
                clippy::permissions_set_readonly_false,
                reason = "clears only the Windows read-only attribute"
            )]
            permissions.set_readonly(false);
            fs::set_permissions(path, permissions)?;
        }
        Ok(())
    }
}

/// Metadata compared before and after preparing each entry.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct Meta {
    pub id: paths::FileId,
    pub kind: Kind,
    pub size: u64,
    pub mtime_sec: i64,
    pub mtime_nsec: i64,
    /// Full native mode (Unix `st_mode`, or Go-style bits on Windows).
    pub mode: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Kind {
    Directory,
    Regular,
    Other,
}

pub(crate) fn link_or_special(name: &str) -> anyhow::Error {
    anyhow::anyhow!(
        "cache entry {name:?} is a link or special file; remove it or select another source"
    )
}

pub(crate) fn changed(name: &str) -> anyhow::Error {
    anyhow::anyhow!(
        "cache entry {name:?} changed during preparation; stop source writers and retry"
    )
}

/// Verifies that the opened entry is the inspected one.
pub(crate) fn check_identity(name: &str, before: &Meta, opened: &Meta) -> Result<()> {
    if before.id != opened.id || before.kind != opened.kind {
        return Err(changed(name));
    }
    Ok(())
}

/// Clones (or, when explicitly authorized, copies) one regular file.
pub(crate) fn clone_entry(
    ctx: &Context,
    file: &File,
    name: &str,
    destination: &str,
    allow_copy: bool,
    clone_file: CloneFile<'_>,
    result: &mut CloneResult,
) -> Result<()> {
    let Err(error) = clone_file(ctx, file, destination) else {
        result.cloned_files += 1;
        return Ok(());
    };
    if !allow_copy || crate::error::find::<Unsupported>(&error).is_none() {
        return Err(wrap(
            error,
            &format!(
                "clone cache entry {name:?} (check space, permissions, and filesystem capability)"
            ),
        ));
    }
    // A native operation may have created or partially cloned its destination.
    // Discard that inode before copying; never reuse a partial native result.
    if let Err(e) = fs::remove_file(destination)
        && e.kind() != io::ErrorKind::NotFound
    {
        return Err(e.into());
    }
    let mut reader = file;
    reader.seek(SeekFrom::Start(0))?;
    copy_file(ctx, file, destination)
        .map_err(|e| wrap(e, &format!("copy cache entry {name:?}")))?;
    result.copied_files += 1;
    Ok(())
}

/// Rejects source changes, then restores permissions and modification time.
pub(crate) fn finish_entry(
    ctx: &Context,
    name: &str,
    before: &Meta,
    after: &Meta,
    destination: &str,
) -> Result<()> {
    if after.size != before.size
        || after.mtime_sec != before.mtime_sec
        || after.mtime_nsec != before.mtime_nsec
        || after.mode != before.mode
    {
        return Err(changed(name));
    }
    ctx.err()?;
    walk::restore_metadata(destination, before)
}

/// Reader that checks cancellation before every read.
pub(crate) struct ContextReader<'a, R> {
    pub ctx: &'a Context,
    pub reader: R,
}

impl<R: Read> Read for ContextReader<'_, R> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if let Err(e) = self.ctx.err() {
            return Err(io::Error::other(e));
        }
        self.reader.read(buf)
    }
}

/// Converts an I/O error that carries a cancellation back to the typed error.
fn cancel_or_io(error: io::Error) -> anyhow::Error {
    if let Some(cancel) = error
        .get_ref()
        .and_then(|inner| inner.downcast_ref::<CancelError>())
    {
        return (*cancel).into();
    }
    error.into()
}

/// Ordinary byte copy into a new private file.
pub(crate) fn copy_file(ctx: &Context, source: &File, destination: &str) -> Result<()> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut out = options.open(destination)?;
    let mut reader = ContextReader {
        ctx,
        reader: source,
    };
    let mut buf = vec![0_u8; 256 * 1024];
    loop {
        let n = match reader.read(&mut buf) {
            Ok(0) => break,
            Ok(n) => n,
            Err(e) if e.kind() == io::ErrorKind::Interrupted => continue,
            Err(e) => return Err(cancel_or_io(e)),
        };
        out.write_all(&buf[..n])?;
    }
    out.flush()?;
    Ok(())
}
