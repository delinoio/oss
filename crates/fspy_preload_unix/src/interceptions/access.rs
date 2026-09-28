use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int};

use crate::macros::intercept;
#[cfg(target_os = "macos")]
use crate::operation::{self, Kind};

intercept!(access(64): unsafe extern "C" fn(pathname: *const c_char, mode: c_int) -> c_int);
unsafe extern "C" fn access(pathname: *const c_char, mode: c_int) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's pathname is forwarded unchanged to libc.
    let operation = unsafe { operation::enter_path(Kind::Metadata, pathname) };
    super::observe_path(pathname, AccessMode::READ);
    // SAFETY: calling the original libc access() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { access::original()(pathname, mode) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

intercept!(faccessat(64): unsafe extern "C" fn(dirfd: c_int, pathname: *const c_char, mode: c_int, flags: c_int) -> c_int);
unsafe extern "C" fn faccessat(
    dirfd: c_int,
    pathname: *const c_char,
    mode: c_int,
    flags: c_int,
) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's descriptor and pathname are forwarded unchanged.
    let operation = unsafe { operation::enter_at(Kind::Metadata, dirfd, pathname) };
    super::observe_at(dirfd, pathname, AccessMode::READ);
    // SAFETY: calling the original libc faccessat() with the same arguments
    // forwarded from the interposed function
    let result = unsafe { faccessat::original()(dirfd, pathname, mode, flags) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}
