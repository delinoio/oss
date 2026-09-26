use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int, size_t, ssize_t};

#[cfg(target_os = "macos")]
use crate::operation::{self, Kind};
use crate::{
    client::{convert::PathAt, handle_open},
    macros::intercept,
};

intercept!(readlink: unsafe extern "C" fn(path: *const c_char, output: *mut c_char, size: size_t) -> ssize_t);
unsafe extern "C" fn readlink(path: *const c_char, output: *mut c_char, size: size_t) -> ssize_t {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's pathname is forwarded unchanged to libc.
    let operation = unsafe { operation::enter_path(Kind::Metadata, path) };
    if !path.is_null() {
        // SAFETY: the non-null path is valid for this intercepted libc call.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), AccessMode::READ) };
    }
    // SAFETY: forward the original pointers and buffer length unchanged.
    let result = unsafe { readlink::original()(path, output, size) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, result as i64);
    result
}

intercept!(readlinkat: unsafe extern "C" fn(dirfd: c_int, path: *const c_char, output: *mut c_char, size: size_t) -> ssize_t);
unsafe extern "C" fn readlinkat(
    dirfd: c_int,
    path: *const c_char,
    output: *mut c_char,
    size: size_t,
) -> ssize_t {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's descriptor and pathname are forwarded unchanged.
    let operation = unsafe { operation::enter_at(Kind::Metadata, dirfd, path) };
    if !path.is_null() {
        // SAFETY: the descriptor and non-null path are caller-provided.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, path), AccessMode::READ) };
    }
    // SAFETY: forward the original descriptor, pointers, and length unchanged.
    let result = unsafe { readlinkat::original()(dirfd, path, output, size) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, result as i64);
    result
}
