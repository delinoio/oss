// SPDX-License-Identifier: Apache-2.0
//! APFS clones with `fclonefileat`.

use std::{
    ffi::{CStr, CString},
    fs::File,
    io,
    os::{fd::AsRawFd, unix::ffi::OsStrExt},
};

use anyhow::Result;

use super::Unsupported;
use crate::{Context, paths};

/// Keep the newly created cache owned by the current user.
const CLONE_NOOWNERCOPY: u32 = 0x0002;

fn fs_type(st: &libc::statfs) -> String {
    // SAFETY: f_fstypename is a NUL-terminated fixed-size C string.
    unsafe { CStr::from_ptr(st.f_fstypename.as_ptr()) }
        .to_string_lossy()
        .into_owned()
}

fn c_path(path: &str) -> io::Result<CString> {
    CString::new(std::ffi::OsStr::new(path).as_bytes())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "path contains NUL"))
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
    let parent = c_path(&paths::dir(dst))?;
    // SAFETY: `parent` is NUL-terminated and the output buffer is valid.
    if unsafe { libc::statfs(parent.as_ptr(), &raw mut destination_fs) } != 0 {
        return Err(io::Error::last_os_error().into());
    }
    if fs_type(&source_fs) != "apfs" || fs_type(&destination_fs) != "apfs" {
        return Err(Unsupported.into());
    }
    // fclonefileat clones the verified open inode, not a replaceable source
    // pathname.
    let target = c_path(dst)?;
    // SAFETY: `src` is a live descriptor and `target` is NUL-terminated.
    if unsafe {
        libc::fclonefileat(
            src.as_raw_fd(),
            libc::AT_FDCWD,
            target.as_ptr(),
            CLONE_NOOWNERCOPY,
        )
    } != 0
    {
        return Err(capability_error(io::Error::last_os_error()));
    }
    Ok(())
}

pub(super) fn capability_error(error: io::Error) -> anyhow::Error {
    match error.raw_os_error() {
        Some(libc::EXDEV | libc::ENOTSUP | libc::ENOSYS) => super::unsupported(error),
        _ => error.into(),
    }
}
