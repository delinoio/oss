use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, handle_open}, libc::{c_char, c_int}, macros::intercept};

// Removal never needs an open call. Retain the attempted pathname even when
// libc fails; only snapshots can prove a workspace deletion. See PATCHES.md.
macro_rules! removal {
    ($name:ident) => {
        intercept!($name: unsafe extern "C" fn(*const c_char) -> c_int);
        unsafe extern "C" fn $name(path: *const c_char) -> c_int {
            // SAFETY: forward the caller's NUL-terminated path unchanged.
            unsafe {
                handle_open(PathAt::borrow_raw(libc::AT_FDCWD, path), AccessMode::WRITE.union(AccessMode::PATH_MUTATION));
                $name::original()(path)
            }
        }
    };
}
removal!(unlink);
removal!(rmdir);
removal!(remove);

intercept!(unlinkat: unsafe extern "C" fn(c_int, *const c_char, c_int) -> c_int);
unsafe extern "C" fn unlinkat(fd: c_int, path: *const c_char, flags: c_int) -> c_int {
    // SAFETY: the directory descriptor and NUL-terminated path are borrowed
    // from the caller, and all original arguments (including AT_REMOVEDIR) pass through.
    unsafe {
        handle_open(PathAt::borrow_raw(fd, path), AccessMode::WRITE.union(AccessMode::PATH_MUTATION));
        unlinkat::original()(fd, path, flags)
    }
}
