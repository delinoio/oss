use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int};

#[cfg(target_os = "macos")]
use crate::operation::{self, Kind};
use crate::{client::handle_open, macros::intercept};

intercept!(chdir: unsafe extern "C" fn(path: *const c_char) -> c_int);
unsafe extern "C" fn chdir(path: *const c_char) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's pathname is forwarded unchanged to libc.
    let operation = unsafe { operation::enter_path(Kind::Metadata, path) };
    if !path.is_null() {
        // SAFETY: the non-null path belongs to this intercepted libc call.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), AccessMode::READ) };
    }
    // SAFETY: forward the original path without modification.
    let result = unsafe { chdir::original()(path) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

intercept!(fchdir: unsafe extern "C" fn(fd: c_int) -> c_int);
unsafe extern "C" fn fchdir(fd: c_int) -> c_int {
    #[cfg(target_os = "macos")]
    let operation = operation::enter_fd(Kind::Metadata, fd);
    // SAFETY: the descriptor belongs to this intercepted libc call.
    unsafe { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ) };
    // SAFETY: forward the original descriptor without modification.
    let result = unsafe { fchdir::original()(fd) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}
