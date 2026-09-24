use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int};

use crate::{client::handle_open, macros::intercept};

intercept!(chdir: unsafe extern "C" fn(path: *const c_char) -> c_int);
unsafe extern "C" fn chdir(path: *const c_char) -> c_int {
    if !path.is_null() {
        // SAFETY: the non-null path belongs to this intercepted libc call.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), AccessMode::READ) };
    }
    // SAFETY: forward the original path without modification.
    unsafe { chdir::original()(path) }
}

intercept!(fchdir: unsafe extern "C" fn(fd: c_int) -> c_int);
unsafe extern "C" fn fchdir(fd: c_int) -> c_int {
    // SAFETY: the descriptor belongs to this intercepted libc call.
    unsafe { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ) };
    // SAFETY: forward the original descriptor without modification.
    unsafe { fchdir::original()(fd) }
}
