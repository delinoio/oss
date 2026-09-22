use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int, stat as stat_struct};

use crate::{
    client::{convert::PathAt, handle_open},
    macros::intercept,
};

intercept!(stat(64): unsafe extern "C" fn(path: *const c_char, buf: *mut stat_struct) -> c_int);
unsafe extern "C" fn stat(path: *const c_char, buf: *mut stat_struct) -> c_int {
    // SAFETY: observe a bounded copy of the caller pathname; forward its original address
    unsafe {
        handle_open(crate::client::convert::PathAt::borrow_raw(libc::AT_FDCWD, path), AccessMode::READ);
    }
    // SAFETY: calling the original libc stat() with the same arguments forwarded from the interposed function
    unsafe { stat::original()(path, buf) }
}

intercept!(lstat(64): unsafe extern "C" fn(path: *const c_char, buf: *mut stat_struct) -> c_int);
unsafe extern "C" fn lstat(path: *const c_char, buf: *mut stat_struct) -> c_int {
    // SAFETY: observe a bounded copy of the caller pathname; forward its original address
    unsafe {
        handle_open(crate::client::convert::PathAt::borrow_raw(libc::AT_FDCWD, path), AccessMode::READ_NOFOLLOW);
    }
    // SAFETY: calling the original libc lstat() with the same arguments forwarded from the interposed function
    unsafe { lstat::original()(path, buf) }
}

intercept!(fstatat(64): unsafe extern "C" fn(dirfd: c_int, pathname: *const c_char, buf: *mut stat_struct, flags: c_int) -> c_int);
unsafe extern "C" fn fstatat(
    dirfd: c_int,
    pathname: *const c_char,
    buf: *mut stat_struct,
    flags: c_int,
) -> c_int {
    // SAFETY: dirfd and pathname are valid arguments provided by the caller of the interposed function
    unsafe {
        handle_open(PathAt::borrow_raw(dirfd, pathname), if flags & libc::AT_SYMLINK_NOFOLLOW != 0 { AccessMode::READ_NOFOLLOW } else { AccessMode::READ });
    }
    // SAFETY: calling the original libc fstatat() with the same arguments forwarded from the interposed function
    unsafe { fstatat::original()(dirfd, pathname, buf, flags) }
}

#[cfg(target_os = "linux")]
intercept!(statx: unsafe extern "C" fn(
    dirfd: c_int,
    pathname: *const c_char,
    flags: c_int,
    mask: libc::c_uint,
    statxbuf: *mut libc::statx,
) -> c_int);
#[cfg(target_os = "linux")]
unsafe extern "C" fn statx(
    dirfd: c_int,
    pathname: *const c_char,
    flags: c_int,
    mask: libc::c_uint,
    statxbuf: *mut libc::statx,
) -> c_int {
    let Some(original) = statx::try_original() else {
        // Rust's standard library interprets ENOSYS from its statx availability
        // probe as unsupported and falls back to stat64.
        // SAFETY: __errno_location returns the calling thread's errno storage on Linux.
        unsafe { *libc::__errno_location() = libc::ENOSYS };
        return -1;
    };

    if pathname.is_null() {
        if flags & libc::AT_EMPTY_PATH != 0 {
            // SAFETY: dirfd is provided by the statx caller.
            unsafe { handle_open(BorrowedFd::borrow_raw(dirfd), if flags & libc::AT_SYMLINK_NOFOLLOW != 0 { AccessMode::READ_NOFOLLOW } else { AccessMode::READ }) };
        }
    } else {
        // SAFETY: pathname is a non-null C string pointer provided by the statx caller.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, pathname), if flags & libc::AT_SYMLINK_NOFOLLOW != 0 { AccessMode::READ_NOFOLLOW } else { AccessMode::READ }) };
    }
    // SAFETY: calling the original libc statx() with the same arguments forwarded from the interposed function
    unsafe { original(dirfd, pathname, flags, mask, statxbuf) }
}

#[cfg(target_os = "macos")]
intercept!(fstat(64): unsafe extern "C" fn(c_int, *mut stat_struct) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn fstat(fd: c_int, buf: *mut stat_struct) -> c_int {
    // A write-only open does not account for this independent metadata read.
    // Never inspect the caller's output buffer; preserve even EFAULT/EBADF.
    unsafe {
        if fd >= 0 { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ); }
        fstat::original()(fd, buf)
    }
}
