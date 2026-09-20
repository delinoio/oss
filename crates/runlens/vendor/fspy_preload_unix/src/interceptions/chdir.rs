use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;

use crate::{client::handle_open, macros::intercept};

intercept!(chdir: unsafe extern "C" fn(*const libc::c_char) -> libc::c_int);
unsafe extern "C" fn chdir(path: *const libc::c_char) -> libc::c_int {
    // SAFETY: resolve the caller's C pathname against the old cwd, then forward
    // the same operand. The operation probes directory metadata, not membership.
    unsafe {
        handle_open(fspy_nostd::CStr::from_ptr(path.cast()), AccessMode::READ);
        chdir::original()(path)
    }
}

intercept!(fchdir: unsafe extern "C" fn(libc::c_int) -> libc::c_int);
unsafe extern "C" fn fchdir(fd: libc::c_int) -> libc::c_int {
    // SAFETY: the borrowed descriptor belongs to the caller and is never closed.
    unsafe {
        handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ);
        fchdir::original()(fd)
    }
}
