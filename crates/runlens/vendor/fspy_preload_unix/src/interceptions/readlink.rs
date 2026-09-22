use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, handle_open}, macros::intercept};

// Linux already observes these calls in seccomp; interposing readlink there
// would recursively intercept its descriptor resolver. macOS needs libc hooks.
intercept!(readlink: unsafe extern "C" fn(*const libc::c_char, *mut libc::c_char, libc::size_t) -> libc::ssize_t);
unsafe extern "C" fn readlink(path: *const libc::c_char, buffer: *mut libc::c_char, size: libc::size_t) -> libc::ssize_t {
    // SAFETY: observe the borrowed link pathname, never its target or output;
    // preserve all original operands and the returned byte count/error.
    unsafe {
        handle_open(PathAt::borrow_raw(libc::AT_FDCWD, path), AccessMode::READ_NOFOLLOW);
        readlink::original()(path, buffer, size)
    }
}
intercept!(readlinkat: unsafe extern "C" fn(libc::c_int, *const libc::c_char, *mut libc::c_char, libc::size_t) -> libc::ssize_t);
unsafe extern "C" fn readlinkat(fd: libc::c_int, path: *const libc::c_char, buffer: *mut libc::c_char, size: libc::size_t) -> libc::ssize_t {
    // SAFETY: resolve against the caller's directory without following the link.
    unsafe {
        handle_open(PathAt::borrow_raw(fd, path), AccessMode::READ_NOFOLLOW);
        readlinkat::original()(fd, path, buffer, size)
    }
}
