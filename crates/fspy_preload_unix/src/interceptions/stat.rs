#[cfg(target_os = "linux")]
use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int, stat as stat_struct};

#[cfg(target_os = "linux")]
use crate::client::{convert::PathAt, handle_open};
use crate::macros::intercept;
#[cfg(target_os = "macos")]
use crate::operation::{self, FinalSymlink};

intercept!(stat(64): unsafe extern "C" fn(path: *const c_char, buf: *mut stat_struct) -> c_int);
unsafe extern "C" fn stat(path: *const c_char, buf: *mut stat_struct) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: path is the caller's pathname passed unchanged to libc.
    let operation = unsafe { operation::enter_metadata_path(path, FinalSymlink::Follow) };
    super::observe_path(path, AccessMode::READ);
    // SAFETY: calling the original libc stat() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { stat::original()(path, buf) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

intercept!(lstat(64): unsafe extern "C" fn(path: *const c_char, buf: *mut stat_struct) -> c_int);
unsafe extern "C" fn lstat(path: *const c_char, buf: *mut stat_struct) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: path is the caller's pathname passed unchanged to libc.
    let operation = unsafe { operation::enter_metadata_path(path, FinalSymlink::NoFollow) };
    // The legacy access hint has no final-component resolution policy.
    super::observe_path(path, AccessMode::READ);
    // SAFETY: calling the original libc lstat() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { lstat::original()(path, buf) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

intercept!(fstatat(64): unsafe extern "C" fn(dirfd: c_int, pathname: *const c_char, buf: *mut stat_struct, flags: c_int) -> c_int);
unsafe extern "C" fn fstatat(
    dirfd: c_int,
    pathname: *const c_char,
    buf: *mut stat_struct,
    flags: c_int,
) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: pathname is the caller's path passed unchanged to libc.
    let operation = unsafe {
        operation::enter_metadata_at(
            dirfd,
            pathname,
            if flags & libc::AT_SYMLINK_NOFOLLOW != 0 {
                FinalSymlink::NoFollow
            } else {
                FinalSymlink::Follow
            },
        )
    };
    super::observe_at(dirfd, pathname, AccessMode::READ);
    // SAFETY: calling the original libc fstatat() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { fstatat::original()(dirfd, pathname, buf, flags) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
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
            unsafe { handle_open(BorrowedFd::borrow_raw(dirfd), AccessMode::READ) };
        }
    } else {
        // SAFETY: pathname is a non-null C string pointer provided by the statx caller.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, pathname), AccessMode::READ) };
    }
    // SAFETY: calling the original libc statx() with the same arguments forwarded
    // from the interposed function
    unsafe { original(dirfd, pathname, flags, mask, statxbuf) }
}
