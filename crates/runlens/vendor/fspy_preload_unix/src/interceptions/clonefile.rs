use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, handle_open}, libc::*, macros::intercept};

// Cloning consumes source contents and creates a destination without open().
// Retain attempts on both operands; snapshots distinguish actual changes.
intercept!(clonefile: unsafe extern "C" fn(*const c_char, *const c_char, u32) -> c_int);
unsafe extern "C" fn clonefile(source: *const c_char, destination: *const c_char, flags: u32) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, source), AccessMode::READ);
        handle_open(PathAt::borrow_raw(AT_FDCWD, destination), AccessMode::WRITE);
        clonefile::original()(source, destination, flags)
    }
}
intercept!(clonefileat: unsafe extern "C" fn(c_int, *const c_char, c_int, *const c_char, u32) -> c_int);
unsafe extern "C" fn clonefileat(source_fd: c_int, source: *const c_char, destination_fd: c_int, destination: *const c_char, flags: u32) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(source_fd, source), AccessMode::READ);
        handle_open(PathAt::borrow_raw(destination_fd, destination), AccessMode::WRITE);
        clonefileat::original()(source_fd, source, destination_fd, destination, flags)
    }
}
intercept!(fclonefileat: unsafe extern "C" fn(c_int, c_int, *const c_char, u32) -> c_int);
unsafe extern "C" fn fclonefileat(source_fd: c_int, destination_fd: c_int, destination: *const c_char, flags: u32) -> c_int {
    unsafe {
        if source_fd >= 0 { handle_open(BorrowedFd::borrow_raw(source_fd), AccessMode::READ); }
        handle_open(PathAt::borrow_raw(destination_fd, destination), AccessMode::WRITE);
        fclonefileat::original()(source_fd, destination_fd, destination, flags)
    }
}
