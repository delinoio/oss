use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int, dev_t, mode_t, off_t, timespec, timeval, utimbuf};

use crate::{
    client::{convert::PathAt, handle_open},
    macros::intercept,
};

unsafe fn track_path(path: *const c_char) {
    if !path.is_null() {
        // SAFETY: a non-null path passed to the intercepted libc call is a C string.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), AccessMode::WRITE) };
    }
}

unsafe fn track_path_at(dirfd: c_int, path: *const c_char) {
    if !path.is_null() {
        // SAFETY: the descriptor and non-null path are forwarded from the caller.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, path), AccessMode::WRITE) };
    }
}

macro_rules! intercept_mutation {
    ($name:ident($($arg:ident: $ty:ty),*), $record:block, alias64) => {
        intercept!($name(64): unsafe extern "C" fn($($ty),*) -> c_int);
        intercept_mutation!(@function $name($($arg: $ty),*), $record);
    };
    ($name:ident($($arg:ident: $ty:ty),*), $record:block) => {
        intercept!($name: unsafe extern "C" fn($($ty),*) -> c_int);
        intercept_mutation!(@function $name($($arg: $ty),*), $record);
    };
    (@function $name:ident($($arg:ident: $ty:ty),*), $record:block) => {
        unsafe extern "C" fn $name($($arg: $ty),*) -> c_int {
            // SAFETY: each path comes from the intercepted libc call and is
            // inspected only while that call's arguments remain alive.
            unsafe { $record }
            // SAFETY: forward every original argument without modification.
            unsafe { $name::original()($($arg),*) }
        }
    };
}

intercept_mutation!(unlink(path: *const c_char), { track_path(path) });
intercept_mutation!(remove(path: *const c_char), { track_path(path) });
intercept_mutation!(rmdir(path: *const c_char), { track_path(path) });
intercept_mutation!(mkdir(path: *const c_char, mode: mode_t), { track_path(path) });
intercept_mutation!(mkfifo(path: *const c_char, mode: mode_t), { track_path(path) });
intercept_mutation!(mknod(path: *const c_char, mode: mode_t, dev: dev_t), { track_path(path) });
intercept_mutation!(creat(path: *const c_char, mode: mode_t), { track_path(path) }, alias64);
intercept_mutation!(truncate(path: *const c_char, length: off_t), { track_path(path) }, alias64);
intercept_mutation!(chmod(path: *const c_char, mode: mode_t), { track_path(path) });
intercept_mutation!(chown(path: *const c_char, owner: libc::uid_t, group: libc::gid_t), { track_path(path) });
intercept_mutation!(lchown(path: *const c_char, owner: libc::uid_t, group: libc::gid_t), { track_path(path) });
intercept_mutation!(utime(path: *const c_char, times: *const utimbuf), { track_path(path) });
intercept_mutation!(utimes(path: *const c_char, times: *const timeval), { track_path(path) });

intercept_mutation!(unlinkat(dirfd: c_int, path: *const c_char, flags: c_int), { track_path_at(dirfd, path) });
intercept_mutation!(mkdirat(dirfd: c_int, path: *const c_char, mode: mode_t), { track_path_at(dirfd, path) });
intercept_mutation!(mkfifoat(dirfd: c_int, path: *const c_char, mode: mode_t), { track_path_at(dirfd, path) });
intercept_mutation!(mknodat(dirfd: c_int, path: *const c_char, mode: mode_t, dev: dev_t), { track_path_at(dirfd, path) });
intercept_mutation!(fchmodat(dirfd: c_int, path: *const c_char, mode: mode_t, flags: c_int), { track_path_at(dirfd, path) });
intercept_mutation!(fchownat(dirfd: c_int, path: *const c_char, owner: libc::uid_t, group: libc::gid_t, flags: c_int), { track_path_at(dirfd, path) });
intercept_mutation!(utimensat(dirfd: c_int, path: *const c_char, times: *const timespec, flags: c_int), { track_path_at(dirfd, path) });

intercept_mutation!(rename(from: *const c_char, to: *const c_char), {
    track_path(from);
    track_path(to);
});
intercept_mutation!(link(from: *const c_char, to: *const c_char), {
    track_path(from);
    track_path(to);
});
intercept_mutation!(symlink(target: *const c_char, linkpath: *const c_char), {
    track_path(linkpath);
});
intercept_mutation!(renameat(from_fd: c_int, from: *const c_char, to_fd: c_int, to: *const c_char), {
    track_path_at(from_fd, from);
    track_path_at(to_fd, to);
});
intercept_mutation!(linkat(from_fd: c_int, from: *const c_char, to_fd: c_int, to: *const c_char, flags: c_int), {
    track_path_at(from_fd, from);
    track_path_at(to_fd, to);
});
intercept_mutation!(symlinkat(target: *const c_char, dirfd: c_int, linkpath: *const c_char), {
    track_path_at(dirfd, linkpath);
});

#[cfg(target_os = "linux")]
intercept_mutation!(renameat2(from_fd: c_int, from: *const c_char, to_fd: c_int, to: *const c_char, flags: libc::c_uint), {
    track_path_at(from_fd, from);
    track_path_at(to_fd, to);
});

#[cfg(target_os = "macos")]
intercept_mutation!(renamex_np(from: *const c_char, to: *const c_char, flags: libc::c_uint), {
    track_path(from);
    track_path(to);
});
#[cfg(target_os = "macos")]
intercept_mutation!(renameatx_np(from_fd: c_int, from: *const c_char, to_fd: c_int, to: *const c_char, flags: libc::c_uint), {
    track_path_at(from_fd, from);
    track_path_at(to_fd, to);
});
#[cfg(target_os = "macos")]
intercept_mutation!(chflags(path: *const c_char, flags: libc::c_uint), { track_path(path) });
#[cfg(target_os = "macos")]
intercept_mutation!(lutimes(path: *const c_char, times: *const timeval), { track_path(path) });
