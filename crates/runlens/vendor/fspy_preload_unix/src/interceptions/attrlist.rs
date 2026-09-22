use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, global_client, handle_open}, libc::*, macros::intercept};

// Attribute request/output buffers belong to the caller. Observe only operands,
// preserving the distinct Darwin options widths and native return values.
intercept!(getattrlist: unsafe extern "C" fn(*const c_char, *mut c_void, *mut c_void, size_t, u32) -> c_int);
unsafe extern "C" fn getattrlist(path: *const c_char, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: u32) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::READ);
        getattrlist::original()(path, attrs, buffer, size, options)
    }
}
intercept!(getattrlistat: unsafe extern "C" fn(c_int, *const c_char, *mut c_void, *mut c_void, size_t, c_ulong) -> c_int);
unsafe extern "C" fn getattrlistat(fd: c_int, path: *const c_char, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: c_ulong) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(fd, path), AccessMode::READ);
        getattrlistat::original()(fd, path, attrs, buffer, size, options)
    }
}
intercept!(fgetattrlist: unsafe extern "C" fn(c_int, *mut c_void, *mut c_void, size_t, u32) -> c_int);
unsafe extern "C" fn fgetattrlist(fd: c_int, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: u32) -> c_int {
    unsafe {
        if fd >= 0 { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ); }
        fgetattrlist::original()(fd, attrs, buffer, size, options)
    }
}
intercept!(getattrlistbulk: unsafe extern "C" fn(c_int, *mut c_void, *mut c_void, size_t, u64) -> c_int);
unsafe extern "C" fn getattrlistbulk(fd: c_int, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: u64) -> c_int {
    // Bulk calls can return metadata for individual children, beyond membership.
    // Keep the directory input and mark loss until per-child identity is bound.
    if let Some(client) = global_client() { client.report_failure(); }
    unsafe {
        if fd >= 0 { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ_DIR); }
        getattrlistbulk::original()(fd, attrs, buffer, size, options)
    }
}

// Attribute buffers stay opaque. Some attribute requests can change pathname
// identity, so conservatively invalidate descendants of mutated directories.
intercept!(setattrlist: unsafe extern "C" fn(*const c_char, *mut c_void, *mut c_void, size_t, u32) -> c_int);
unsafe extern "C" fn setattrlist(path: *const c_char, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: u32) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::WRITE | AccessMode::PATH_MUTATION);
        setattrlist::original()(path, attrs, buffer, size, options)
    }
}
intercept!(setattrlistat: unsafe extern "C" fn(c_int, *const c_char, *mut c_void, *mut c_void, size_t, u32) -> c_int);
unsafe extern "C" fn setattrlistat(fd: c_int, path: *const c_char, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: u32) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(fd, path), AccessMode::WRITE | AccessMode::PATH_MUTATION);
        setattrlistat::original()(fd, path, attrs, buffer, size, options)
    }
}
intercept!(fsetattrlist: unsafe extern "C" fn(c_int, *mut c_void, *mut c_void, size_t, u32) -> c_int);
unsafe extern "C" fn fsetattrlist(fd: c_int, attrs: *mut c_void, buffer: *mut c_void, size: size_t, options: u32) -> c_int {
    unsafe {
        if fd >= 0 { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::WRITE | AccessMode::PATH_MUTATION); }
        fsetattrlist::original()(fd, attrs, buffer, size, options)
    }
}
