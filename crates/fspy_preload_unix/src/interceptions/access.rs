use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int};

use crate::{
    client::{convert::PathAt, handle_open},
    macros::intercept,
};

intercept!(access(64): unsafe extern "C" fn(pathname: *const c_char, mode: c_int) -> c_int);
unsafe extern "C" fn access(pathname: *const c_char, mode: c_int) -> c_int {
    if !pathname.is_null() {
        // SAFETY: the non-null pathname is valid for the intercepted call.
        unsafe {
            handle_open(
                fspy_nostd::CStr::from_ptr(pathname.cast()),
                AccessMode::READ,
            )
        };
    }
    // SAFETY: calling the original libc access() with the same arguments forwarded
    // from the interposed function
    unsafe { access::original()(pathname, mode) }
}

intercept!(faccessat(64): unsafe extern "C" fn(dirfd: c_int, pathname: *const c_char, mode: c_int, flags: c_int) -> c_int);
unsafe extern "C" fn faccessat(
    dirfd: c_int,
    pathname: *const c_char,
    mode: c_int,
    flags: c_int,
) -> c_int {
    if !pathname.is_null() {
        // SAFETY: the non-null pathname and descriptor are caller-provided.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, pathname), AccessMode::READ) };
    }
    // SAFETY: calling the original libc faccessat() with the same arguments
    // forwarded from the interposed function
    unsafe { faccessat::original()(dirfd, pathname, mode, flags) }
}
