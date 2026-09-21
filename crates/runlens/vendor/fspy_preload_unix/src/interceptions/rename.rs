use fspy_shared::ipc::AccessMode;

use crate::{client::{convert::PathAt, handle_open}, libc::{c_char, c_int, c_uint}, macros::intercept};

// Rename mutates both directory entries, even without opening either file.
// Record attempts before forwarding unchanged; snapshots establish actual changes.
// Remove this local patch when upstream covers both endpoints (see PATCHES.md).
unsafe fn record(old_fd: c_int, old: *const c_char, new_fd: c_int, new: *const c_char) {
    // SAFETY: pointers and descriptors are forwarded from the native caller.
    unsafe {
        handle_open(PathAt::borrow_raw(old_fd, old), AccessMode::WRITE.union(AccessMode::PATH_MUTATION));
        handle_open(PathAt::borrow_raw(new_fd, new), AccessMode::WRITE.union(AccessMode::PATH_MUTATION));
    }
}

intercept!(rename: unsafe extern "C" fn(*const c_char, *const c_char) -> c_int);
unsafe extern "C" fn rename(old: *const c_char, new: *const c_char) -> c_int {
    // SAFETY: preserve the caller's original arguments and return value.
    unsafe {
        record(libc::AT_FDCWD, old, libc::AT_FDCWD, new);
        rename::original()(old, new)
    }
}

intercept!(renameat: unsafe extern "C" fn(c_int, *const c_char, c_int, *const c_char) -> c_int);
unsafe extern "C" fn renameat(old_fd: c_int, old: *const c_char, new_fd: c_int, new: *const c_char) -> c_int {
    // SAFETY: preserve the caller's original arguments and return value.
    unsafe {
        record(old_fd, old, new_fd, new);
        renameat::original()(old_fd, old, new_fd, new)
    }
}

#[cfg(target_os = "linux")]
intercept!(renameat2: unsafe extern "C" fn(c_int, *const c_char, c_int, *const c_char, c_uint) -> c_int);
#[cfg(target_os = "linux")]
unsafe extern "C" fn renameat2(old_fd: c_int, old: *const c_char, new_fd: c_int, new: *const c_char, flags: c_uint) -> c_int {
    // SAFETY: preserve the caller's original arguments, flags, and return value.
    unsafe {
        record(old_fd, old, new_fd, new);
        renameat2::original()(old_fd, old, new_fd, new, flags)
    }
}

#[cfg(target_os = "macos")]
intercept!(renamex_np: unsafe extern "C" fn(*const c_char, *const c_char, c_uint) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn renamex_np(old: *const c_char, new: *const c_char, flags: c_uint) -> c_int {
    // SAFETY: preserve the caller's original arguments, flags, and return value.
    unsafe {
        record(libc::AT_FDCWD, old, libc::AT_FDCWD, new);
        renamex_np::original()(old, new, flags)
    }
}

#[cfg(target_os = "macos")]
intercept!(renameatx_np: unsafe extern "C" fn(c_int, *const c_char, c_int, *const c_char, c_uint) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn renameatx_np(old_fd: c_int, old: *const c_char, new_fd: c_int, new: *const c_char, flags: c_uint) -> c_int {
    // SAFETY: preserve the caller's original arguments, flags, and return value.
    unsafe {
        record(old_fd, old, new_fd, new);
        renameatx_np::original()(old_fd, old, new_fd, new, flags)
    }
}
