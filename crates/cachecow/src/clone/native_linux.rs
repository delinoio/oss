// SPDX-License-Identifier: Apache-2.0
//! Btrfs and XFS reflinks with `FICLONE`.

use std::{
    ffi::CString,
    fs::{File, OpenOptions},
    io,
    os::{
        fd::{AsRawFd, IntoRawFd},
        unix::{ffi::OsStrExt, fs::OpenOptionsExt},
    },
};

use anyhow::Result;

use super::Unsupported;
use crate::{Context, paths};

const BTRFS_SUPER_MAGIC: i64 = 0x9123_683e;
const XFS_SUPER_MAGIC: i64 = 0x5846_5342;
/// `_IOW(0x94, 9, int)`.
const FICLONE: libc::c_ulong = 0x4004_9409;

fn supported(st: &libc::statfs) -> bool {
    #[allow(
        clippy::unnecessary_cast,
        reason = "f_type width differs by architecture"
    )]
    let kind = st.f_type as i64;
    kind == BTRFS_SUPER_MAGIC || kind == XFS_SUPER_MAGIC
}

pub(super) fn clone_file(ctx: &Context, src: &File, dst: &str) -> Result<()> {
    ctx.err()?;
    // SAFETY: statfs is plain data filled by the calls below.
    let mut source_fs: libc::statfs = unsafe { std::mem::zeroed() };
    // SAFETY: as above.
    let mut destination_fs: libc::statfs = unsafe { std::mem::zeroed() };
    // SAFETY: `src` is a live descriptor and the output buffer is valid.
    if unsafe { libc::fstatfs(src.as_raw_fd(), &raw mut source_fs) } != 0 {
        return Err(io::Error::last_os_error().into());
    }
    let parent = CString::new(std::ffi::OsStr::new(&paths::dir(dst)).as_bytes())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "path contains NUL"))?;
    // SAFETY: `parent` is NUL-terminated and the output buffer is valid.
    if unsafe { libc::statfs(parent.as_ptr(), &raw mut destination_fs) } != 0 {
        return Err(io::Error::last_os_error().into());
    }
    if !supported(&source_fs) || !supported(&destination_fs) {
        return Err(Unsupported.into());
    }
    let out = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(dst)?;
    // SAFETY: both descriptors are live for the duration of the ioctl.
    let cloned = if unsafe { libc::ioctl(out.as_raw_fd(), FICLONE as _, src.as_raw_fd()) } == 0 {
        Ok(())
    } else {
        Err(capability_error(io::Error::last_os_error()))
    };
    let fd = out.into_raw_fd();
    // SAFETY: `fd` was released from the File and is closed exactly once.
    let closed = if unsafe { libc::close(fd) } == 0 {
        None
    } else {
        let error = io::Error::last_os_error();
        Some(anyhow::anyhow!("close native clone: {error}").context(error))
    };
    crate::error::join_result(cloned, closed)
}

/// EINVAL and EBADF are not capability errors: they also indicate invalid
/// arguments or descriptors and must not conceal implementation bugs.
pub(super) fn capability_error(error: io::Error) -> anyhow::Error {
    match error.raw_os_error() {
        Some(libc::EXDEV | libc::EOPNOTSUPP | libc::ENOTTY | libc::ENOSYS) => {
            super::unsupported(error)
        }
        _ => error.into(),
    }
}
