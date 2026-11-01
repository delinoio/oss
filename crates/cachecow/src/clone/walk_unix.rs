// SPDX-License-Identifier: Apache-2.0
//! Unix source traversal through directory descriptors. Every entry is
//! inspected with `fstatat(AT_SYMLINK_NOFOLLOW)` and opened with
//! `openat(O_NOFOLLOW | O_NONBLOCK)` relative to its already verified parent,
//! so a replaced symlink can never redirect the walk outside the source root
//! (Go `os.OpenRoot`).

use std::{
    ffi::{CStr, CString},
    fs::File,
    io,
    os::{
        fd::{AsRawFd, FromRawFd, RawFd},
        unix::ffi::OsStrExt,
    },
};

use anyhow::{Result, anyhow};

use super::{CloneFile, CloneResult, Kind, Meta};
use crate::{Context, paths};

pub(super) struct SourceRoot {
    root: File,
}

fn cstring(value: &[u8]) -> io::Result<CString> {
    CString::new(value)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "path contains NUL"))
}

fn meta(st: &libc::stat) -> Meta {
    let kind = match st.st_mode & libc::S_IFMT {
        libc::S_IFDIR => Kind::Directory,
        libc::S_IFREG => Kind::Regular,
        _ => Kind::Other,
    };
    Meta {
        id: paths::FileId {
            #[allow(clippy::unnecessary_cast, reason = "dev_t width differs by platform")]
            device: st.st_dev as u64,
            #[allow(clippy::unnecessary_cast, reason = "ino_t width differs by platform")]
            index: st.st_ino as u64,
        },
        kind,
        size: u64::try_from(st.st_size).unwrap_or(0),
        #[allow(clippy::unnecessary_cast, reason = "time_t width differs by platform")]
        mtime_sec: st.st_mtime as i64,
        #[allow(clippy::unnecessary_cast, reason = "nsec width differs by platform")]
        mtime_nsec: st.st_mtime_nsec as i64,
        #[allow(clippy::unnecessary_cast, reason = "mode_t width differs by platform")]
        mode: st.st_mode as u32,
    }
}

fn fstat(fd: RawFd) -> io::Result<Meta> {
    // SAFETY: `stat` is plain data; fstat fills it for a live descriptor.
    let mut st: libc::stat = unsafe { std::mem::zeroed() };
    // SAFETY: `fd` is a descriptor owned by the caller for this call.
    if unsafe { libc::fstat(fd, &raw mut st) } != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(meta(&st))
}

fn lstat_at(parent: RawFd, name: &CStr) -> io::Result<Meta> {
    // SAFETY: `stat` is plain data.
    let mut st: libc::stat = unsafe { std::mem::zeroed() };
    // SAFETY: `parent` is a live directory descriptor and `name` is
    // NUL-terminated.
    if unsafe {
        libc::fstatat(
            parent,
            name.as_ptr(),
            &raw mut st,
            libc::AT_SYMLINK_NOFOLLOW,
        )
    } != 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(meta(&st))
}

fn open_at(parent: RawFd, name: &CStr) -> io::Result<File> {
    let flags = libc::O_RDONLY | libc::O_NOFOLLOW | libc::O_NONBLOCK | libc::O_CLOEXEC;
    // SAFETY: `parent` is a live directory descriptor and `name` is
    // NUL-terminated.
    let fd = unsafe { libc::openat(parent, name.as_ptr(), flags) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: `fd` was just returned by openat and is owned by the new File.
    Ok(unsafe { File::from_raw_fd(fd) })
}

#[cfg(target_os = "macos")]
fn set_errno(value: libc::c_int) {
    // SAFETY: __error returns this thread's errno location.
    unsafe { *libc::__error() = value };
}

#[cfg(not(target_os = "macos"))]
fn set_errno(value: libc::c_int) {
    // SAFETY: __errno_location returns this thread's errno location.
    unsafe { *libc::__errno_location() = value };
}

/// Entry names of an open directory, excluding `.` and `..`.
fn read_names(dir: &File) -> io::Result<Vec<CString>> {
    // SAFETY: duplicates a live descriptor; the copy is owned by the stream.
    let fd = unsafe { libc::dup(dir.as_raw_fd()) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: `fd` is a fresh descriptor; on success the stream owns it.
    let stream = unsafe { libc::fdopendir(fd) };
    if stream.is_null() {
        let error = io::Error::last_os_error();
        // SAFETY: fdopendir failed, so `fd` is still ours to close.
        unsafe { libc::close(fd) };
        return Err(error);
    }
    let mut names = Vec::new();
    let result = loop {
        set_errno(0);
        // SAFETY: `stream` is a live directory stream.
        let entry = unsafe { libc::readdir(stream) };
        if entry.is_null() {
            let error = io::Error::last_os_error();
            break if error.raw_os_error() == Some(0) {
                Ok(())
            } else {
                Err(error)
            };
        }
        // SAFETY: readdir returned a valid entry with a NUL-terminated name
        // that stays valid until the next readdir call.
        let name = unsafe { CStr::from_ptr((*entry).d_name.as_ptr()) };
        let bytes = name.to_bytes();
        if bytes != b"." && bytes != b".." {
            names.push(name.to_owned());
        }
    };
    // SAFETY: `stream` is closed exactly once, releasing its descriptor.
    unsafe { libc::closedir(stream) };
    result.map(|()| names)
}

impl SourceRoot {
    pub(super) fn open(path: &str) -> io::Result<Self> {
        let c = cstring(std::ffi::OsStr::new(path).as_bytes())?;
        let flags = libc::O_RDONLY | libc::O_DIRECTORY | libc::O_CLOEXEC;
        // SAFETY: `c` is NUL-terminated.
        let fd = unsafe { libc::open(c.as_ptr(), flags) };
        if fd < 0 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: `fd` was just opened and is owned by the File.
        Ok(Self {
            root: unsafe { File::from_raw_fd(fd) },
        })
    }

    pub(super) fn id(&self) -> io::Result<paths::FileId> {
        Ok(fstat(self.root.as_raw_fd())?.id)
    }

    pub(super) fn walk(
        &self,
        ctx: &Context,
        destination: &str,
        allow_copy: bool,
        clone_file: CloneFile<'_>,
        result: &mut CloneResult,
    ) -> Result<()> {
        let mut walker = Walker {
            ctx,
            allow_copy,
            clone_file,
            result,
        };
        walker.entry(self.root.as_raw_fd(), None, ".", destination)
    }
}

struct Walker<'a, 'b> {
    ctx: &'a Context,
    allow_copy: bool,
    clone_file: CloneFile<'b>,
    result: &'a mut CloneResult,
}

impl Walker<'_, '_> {
    /// Prepares `name` (relative to the source root). `entry` is `None` for the
    /// root itself, which is already open as `parent`.
    fn entry(&mut self, parent: RawFd, entry: Option<&CStr>, name: &str, dst: &str) -> Result<()> {
        self.ctx.err()?;
        let (info, file) = match entry {
            None => {
                // SAFETY: duplicates the live root descriptor for this entry.
                let fd = unsafe { libc::dup(parent) };
                if fd < 0 {
                    return Err(io::Error::last_os_error().into());
                }
                // SAFETY: `fd` is a fresh descriptor owned by the File.
                let file = unsafe { File::from_raw_fd(fd) };
                (fstat(fd)?, file)
            }
            Some(c) => {
                let info = lstat_at(parent, c)?;
                if info.kind == Kind::Other {
                    return Err(super::link_or_special(name));
                }
                let file = open_at(parent, c)
                    .map_err(|e| super::wrap(e, &format!("open cache entry {name:?}")))?;
                (info, file)
            }
        };
        if info.kind == Kind::Other {
            return Err(super::link_or_special(name));
        }
        let opened = fstat(file.as_raw_fd())?;
        super::check_identity(name, &info, &opened)?;
        if info.kind == Kind::Directory {
            if entry.is_some() {
                super::make_dir(dst)?;
            }
            for child in read_names(&file)? {
                let child_name = child
                    .to_str()
                    .map_err(|_| {
                        anyhow!("cache entry name in {name:?} is not valid UTF-8; rename it")
                    })?
                    .to_string();
                self.entry(
                    file.as_raw_fd(),
                    Some(&child),
                    &paths::join2(name, &child_name),
                    &paths::join2(dst, &child_name),
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
        let after = fstat(file.as_raw_fd())?;
        super::finish_entry(self.ctx, name, &info, &after, dst)
    }
}

/// Restores permission bits, then the modification time (access time is set
/// to the same value, as Go's `os.Chtimes(dst, mtime, mtime)` did).
pub(super) fn restore_metadata(destination: &str, before: &Meta) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    std::fs::set_permissions(
        destination,
        std::fs::Permissions::from_mode(before.mode & 0o777),
    )?;
    let c = cstring(std::ffi::OsStr::new(destination).as_bytes())?;
    let time = libc::timespec {
        tv_sec: before.mtime_sec as libc::time_t,
        #[allow(clippy::unnecessary_cast, reason = "tv_nsec width differs by platform")]
        tv_nsec: before.mtime_nsec as _,
    };
    let times = [time, time];
    // SAFETY: `c` is NUL-terminated and `times` holds two timespec values.
    if unsafe {
        libc::utimensat(
            libc::AT_FDCWD,
            c.as_ptr(),
            times.as_ptr(),
            libc::AT_SYMLINK_NOFOLLOW,
        )
    } != 0
    {
        return Err(io::Error::last_os_error().into());
    }
    Ok(())
}
