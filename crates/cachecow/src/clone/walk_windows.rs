// SPDX-License-Identifier: Apache-2.0
//! Windows source traversal. Every entry is inspected without following
//! reparse points and opened with `FILE_FLAG_OPEN_REPARSE_POINT`; any reparse
//! point (symbolic link or junction) is rejected, so the walk cannot leave the
//! verified source root.

use std::{
    fs::{self, File, FileTimes, OpenOptions},
    os::windows::fs::{MetadataExt, OpenOptionsExt},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use anyhow::{Result, anyhow};
use windows_sys::Win32::Storage::FileSystem::{
    FILE_ATTRIBUTE_REPARSE_POINT, FILE_FLAG_BACKUP_SEMANTICS, FILE_FLAG_OPEN_REPARSE_POINT,
    FILE_WRITE_ATTRIBUTES,
};

use super::{CloneFile, CloneResult, Kind, Meta};
use crate::{Context, paths};

pub(super) struct SourceRoot {
    path: String,
    root: File,
}

fn open_entry(path: &str) -> std::io::Result<File> {
    OpenOptions::new()
        .read(true)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)
}

fn split_time(time: SystemTime) -> (i64, i64) {
    match time.duration_since(UNIX_EPOCH) {
        Ok(d) => (
            i64::try_from(d.as_secs()).unwrap_or(i64::MAX),
            i64::from(d.subsec_nanos()),
        ),
        Err(e) => {
            let d = e.duration();
            let secs = -i64::try_from(d.as_secs()).unwrap_or(i64::MAX);
            if d.subsec_nanos() == 0 {
                (secs, 0)
            } else {
                (secs - 1, 1_000_000_000 - i64::from(d.subsec_nanos()))
            }
        }
    }
}

fn join_time(sec: i64, nsec: i64) -> SystemTime {
    let nanos = u32::try_from(nsec).unwrap_or(0);
    if sec >= 0 {
        UNIX_EPOCH + Duration::new(u64::try_from(sec).unwrap_or(0), nanos)
    } else {
        UNIX_EPOCH - Duration::from_secs(sec.unsigned_abs())
            + Duration::from_nanos(u64::from(nanos))
    }
}

fn meta(m: &fs::Metadata, id: paths::FileId) -> Result<Meta> {
    let kind = if m.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
        Kind::Other
    } else if m.is_dir() {
        Kind::Directory
    } else if m.is_file() {
        Kind::Regular
    } else {
        Kind::Other
    };
    let (mtime_sec, mtime_nsec) = split_time(m.modified()?);
    Ok(Meta {
        id,
        kind,
        size: m.len(),
        mtime_sec,
        mtime_nsec,
        mode: paths::go_file_mode(m),
    })
}

impl SourceRoot {
    pub(super) fn open(path: &str) -> std::io::Result<Self> {
        let root = OpenOptions::new()
            .read(true)
            .custom_flags(FILE_FLAG_BACKUP_SEMANTICS)
            .open(path)?;
        if !root.metadata()?.is_dir() {
            return Err(std::io::Error::new(
                std::io::ErrorKind::NotADirectory,
                "source cache is not a directory",
            ));
        }
        Ok(Self {
            path: path.to_string(),
            root,
        })
    }

    pub(super) fn id(&self) -> std::io::Result<paths::FileId> {
        paths::file_id_of(&self.root)
    }

    pub(super) fn walk(
        &self,
        ctx: &Context,
        destination: &str,
        allow_copy: bool,
        clone_file: CloneFile<'_>,
        result: &mut CloneResult,
    ) -> Result<()> {
        let root_id = self.id()?;
        let mut walker = Walker {
            ctx,
            allow_copy,
            clone_file,
            result,
        };
        walker.entry(&self.path, ".", destination, Some(root_id))
    }
}

struct Walker<'a, 'b> {
    ctx: &'a Context,
    allow_copy: bool,
    clone_file: CloneFile<'b>,
    result: &'a mut CloneResult,
}

impl Walker<'_, '_> {
    /// `root` carries the opened root identity; the root may be reached
    /// through the resolved source path only.
    fn entry(
        &mut self,
        path: &str,
        name: &str,
        dst: &str,
        root: Option<paths::FileId>,
    ) -> Result<()> {
        self.ctx.err()?;
        let listed = fs::symlink_metadata(path)?;
        let info = meta(&listed, paths::file_id(path, false)?)?;
        if info.kind == Kind::Other {
            return Err(super::link_or_special(name));
        }
        if root.is_some_and(|id| id != info.id) {
            return Err(super::changed(name));
        }
        let file =
            open_entry(path).map_err(|e| super::wrap(e, &format!("open cache entry {name:?}")))?;
        let opened = meta(&file.metadata()?, paths::file_id_of(&file)?)?;
        super::check_identity(name, &info, &opened)?;
        if info.kind == Kind::Directory {
            if root.is_none() {
                super::make_dir(dst)?;
            }
            let mut children = Vec::new();
            for child in fs::read_dir(path)? {
                let child = child?;
                children.push(child.file_name().into_string().map_err(|_| {
                    anyhow!("cache entry name in {name:?} is not valid UTF-8; rename it")
                })?);
            }
            for child in children {
                self.entry(
                    &paths::join2(path, &child),
                    &paths::join2(name, &child),
                    &paths::join2(dst, &child),
                    None,
                )?;
            }
        } else {
            super::clone_entry(
                self.ctx,
                &file,
                name,
                dst,
                self.allow_copy,
                self.clone_file,
                self.result,
            )?;
        }
        let after = meta(&file.metadata()?, paths::file_id_of(&file)?)?;
        super::finish_entry(self.ctx, name, &info, &after, dst)
    }
}

/// Restores the modification time (access time set to the same value), then
/// the read-only attribute, which is the only permission Windows records.
pub(super) fn restore_metadata(destination: &str, before: &Meta) -> Result<()> {
    let time = join_time(before.mtime_sec, before.mtime_nsec);
    let handle = OpenOptions::new()
        .access_mode(FILE_WRITE_ATTRIBUTES)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS)
        .open(destination)?;
    handle.set_times(FileTimes::new().set_accessed(time).set_modified(time))?;
    drop(handle);
    let mut permissions = fs::metadata(destination)?.permissions();
    let readonly = before.mode & 0o200 == 0;
    if permissions.readonly() != readonly {
        permissions.set_readonly(readonly);
        fs::set_permissions(destination, permissions)?;
    }
    Ok(())
}
