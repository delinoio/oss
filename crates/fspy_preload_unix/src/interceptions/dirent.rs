use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use libc::{DIR, c_char, c_int, c_long, c_void};

#[cfg(target_os = "macos")]
use crate::operation::{self, Kind};
use crate::{client::handle_open, macros::intercept};

intercept!(scandir(64): unsafe extern "C" fn (
    dirname: *const c_char,
    namelist: *mut c_void,
    select: *const c_void,
    compar: *const c_void,
) -> c_int);
unsafe extern "C" fn scandir(
    dirname: *const c_char,
    namelist: *mut c_void,
    select: *const c_void,
    compar: *const c_void,
) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: dirname is the caller's path passed unchanged to libc.
    let operation = unsafe { operation::enter_path(Kind::Directory, dirname) };
    if !dirname.is_null() {
        // SAFETY: the non-null pathname is valid for the intercepted call.
        unsafe {
            handle_open(
                fspy_nostd::CStr::from_ptr(dirname.cast()),
                AccessMode::READ_DIR,
            );
        };
    }
    // SAFETY: calling the original libc scandir() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { scandir::original()(dirname, namelist, select, compar) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

#[cfg(target_os = "macos")]
mod macos_only {
    use super::{AccessMode, BorrowedFd, c_char, c_int, c_void, handle_open, intercept};
    use crate::operation::{self, Kind};

    intercept!(scandir_b: unsafe extern "C" fn (
        dirname: *const c_char,
        namelist: *mut c_void,
        select: *const c_void,
        compar: *const c_void,
    ) -> c_int);
    unsafe extern "C" fn scandir_b(
        dirname: *const c_char,
        namelist: *mut c_void,
        select: *const c_void,
        compar: *const c_void,
    ) -> c_int {
        // SAFETY: dirname is the caller's path passed unchanged to libc.
        let operation = unsafe { operation::enter_path(Kind::Directory, dirname) };
        if !dirname.is_null() {
            // SAFETY: the non-null pathname is valid for the intercepted call.
            unsafe {
                handle_open(
                    fspy_nostd::CStr::from_ptr(dirname.cast()),
                    AccessMode::READ_DIR,
                );
            };
        }
        // SAFETY: calling the original libc scandir_b() with the same arguments
        // forwarded from the interposed function
        let result = unsafe { scandir_b::original()(dirname, namelist, select, compar) };
        operation::finish(operation, i64::from(result));
        result
    }

    intercept!(__getdirentries64: unsafe extern "C" fn(c_int, *mut u8, usize, *mut i64) -> isize);
    unsafe extern "C" fn __getdirentries64(
        fd: c_int,
        buf: *mut u8,
        buf_len: usize,
        basep: *mut i64,
    ) -> isize {
        let operation = operation::enter_fd(Kind::Directory, fd);
        // SAFETY: fd is a valid file descriptor provided by the caller of
        // __getdirentries64
        unsafe { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ_DIR) };
        // SAFETY: calling the original libc __getdirentries64() with the same arguments
        // forwarded from the interposed function
        let result = unsafe { __getdirentries64::original()(fd, buf, buf_len, basep) };
        operation::finish(operation, result as i64);
        result
    }
}

intercept!(getdirentries(64): unsafe extern "C" fn (fd: c_int, buf: *mut c_char, nbytes: c_int, basep: *mut c_long) -> c_int);
unsafe extern "C" fn getdirentries(
    fd: c_int,
    buf: *mut c_char,
    nbytes: c_int,
    basep: *mut c_long,
) -> c_int {
    #[cfg(target_os = "macos")]
    let operation = operation::enter_fd(Kind::Directory, fd);
    // SAFETY: fd is a valid file descriptor provided by the caller of the
    // interposed function
    unsafe { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ_DIR) };
    // SAFETY: calling the original libc getdirentries() with the same arguments
    // forwarded from the interposed function
    let result = unsafe { getdirentries::original()(fd, buf, nbytes, basep) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

intercept!(fdopendir(64): unsafe extern "C" fn (fd: c_int) -> *mut DIR);
unsafe extern "C" fn fdopendir(fd: c_int) -> *mut DIR {
    #[cfg(target_os = "macos")]
    let operation = operation::enter_fd(Kind::Directory, fd);
    // SAFETY: fd is a valid file descriptor provided by the caller of the
    // interposed function
    unsafe { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ_DIR) };
    // SAFETY: calling the original libc fdopendir() with the same arguments
    // forwarded from the interposed function
    let result = unsafe { fdopendir::original()(fd) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, if result.is_null() { -1 } else { 0 });
    result
}

intercept!(opendir(64): unsafe extern "C" fn (*const c_char) -> *mut DIR);
unsafe extern "C" fn opendir(dir_name: *const c_char) -> *mut DIR {
    #[cfg(target_os = "macos")]
    // SAFETY: dir_name is the caller's path passed unchanged to libc.
    let operation = unsafe { operation::enter_path(Kind::Directory, dir_name) };
    if !dir_name.is_null() {
        // SAFETY: the non-null pathname is valid for the intercepted call.
        unsafe {
            handle_open(
                fspy_nostd::CStr::from_ptr(dir_name.cast()),
                AccessMode::READ_DIR,
            );
        };
    }
    // SAFETY: calling the original libc opendir() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { opendir::original()(dir_name) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, if result.is_null() { -1 } else { 0 });
    result
}
