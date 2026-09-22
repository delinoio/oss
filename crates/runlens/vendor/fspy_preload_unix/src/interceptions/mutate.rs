use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, handle_open}, libc::*, macros::intercept};

// Path-only and descriptor-only mutations do not need a writable open. Record
// their attempts before forwarding every argument unchanged. See PATCHES.md.
macro_rules! path_mutation {
    ($name:ident, $($arg:ident: $ty:ty),* $(,)?) => {
        intercept!($name: unsafe extern "C" fn(*const c_char, $($ty),*) -> c_int);
        unsafe extern "C" fn $name(path: *const c_char, $($arg: $ty),*) -> c_int {
            // SAFETY: caller-owned arguments live through the original call.
            unsafe {
                handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::WRITE);
                $name::original()(path, $($arg),*)
            }
        }
    };
}
macro_rules! fd_mutation {
    ($name:ident, $($arg:ident: $ty:ty),* $(,)?) => {
        intercept!($name: unsafe extern "C" fn(c_int, $($ty),*) -> c_int);
        unsafe extern "C" fn $name(fd: c_int, $($arg: $ty),*) -> c_int {
            // SAFETY: the descriptor is borrowed for the original call only.
            unsafe {
                handle_open(BorrowedFd::borrow_raw(fd), AccessMode::WRITE);
                $name::original()(fd, $($arg),*)
            }
        }
    };
}
macro_rules! at_mutation {
    ($name:ident, $($arg:ident: $ty:ty),* $(,)?) => {
        intercept!($name: unsafe extern "C" fn(c_int, *const c_char, $($ty),*) -> c_int);
        unsafe extern "C" fn $name(fd: c_int, path: *const c_char, $($arg: $ty),*) -> c_int {
            // SAFETY: a null utimensat path selects the descriptor; other calls
            // retain the original path and flags, including no-follow behavior.
            unsafe {
                if path.is_null() { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::WRITE); }
                else { handle_open(PathAt::borrow_raw(fd, path), AccessMode::WRITE); }
                $name::original()(fd, path, $($arg),*)
            }
        }
    };
}
path_mutation!(mkdir, mode: mode_t);
path_mutation!(mkfifo, mode: mode_t);
path_mutation!(mknod, mode: mode_t, device: dev_t);
path_mutation!(chmod, mode: mode_t);
path_mutation!(chown, owner: uid_t, group: gid_t);
path_mutation!(lchown, owner: uid_t, group: gid_t);
path_mutation!(truncate, length: off_t);
path_mutation!(utimes, times: *const timeval);
path_mutation!(lutimes, times: *const timeval);
fd_mutation!(fchmod, mode: mode_t);
fd_mutation!(fchown, owner: uid_t, group: gid_t);
fd_mutation!(ftruncate, length: off_t);
fd_mutation!(futimes, times: *const timeval);
fd_mutation!(futimens, times: *const timespec);
at_mutation!(mkdirat, mode: mode_t);
at_mutation!(fchmodat, mode: mode_t, flags: c_int);
at_mutation!(fchownat, owner: uid_t, group: gid_t, flags: c_int);
at_mutation!(utimensat, times: *const timespec, flags: c_int);
#[cfg(target_os = "linux")]
path_mutation!(utime, times: *const utimbuf);
#[cfg(target_os = "linux")]
path_mutation!(truncate64, length: off64_t);
#[cfg(target_os = "linux")]
fd_mutation!(ftruncate64, length: off64_t);
#[cfg(target_os = "linux")]
at_mutation!(mknodat, mode: mode_t, device: dev_t);
#[cfg(target_os = "linux")]
at_mutation!(futimesat, times: *const timeval);

#[cfg(target_os = "linux")]
path_mutation!(setxattr, name: *const c_char, value: *const c_void, size: size_t, flags: c_int);
#[cfg(target_os = "linux")]
path_mutation!(lsetxattr, name: *const c_char, value: *const c_void, size: size_t, flags: c_int);
#[cfg(target_os = "linux")]
fd_mutation!(fsetxattr, name: *const c_char, value: *const c_void, size: size_t, flags: c_int);
#[cfg(target_os = "linux")]
path_mutation!(removexattr, name: *const c_char);
#[cfg(target_os = "linux")]
path_mutation!(lremovexattr, name: *const c_char);
#[cfg(target_os = "linux")]
fd_mutation!(fremovexattr, name: *const c_char);
#[cfg(target_os = "macos")]
path_mutation!(setxattr, name: *const c_char, value: *const c_void, size: size_t, position: u32, options: c_int);
#[cfg(target_os = "macos")]
fd_mutation!(fsetxattr, name: *const c_char, value: *const c_void, size: size_t, position: u32, options: c_int);
#[cfg(target_os = "macos")]
path_mutation!(removexattr, name: *const c_char, options: c_int);
#[cfg(target_os = "macos")]
fd_mutation!(fremovexattr, name: *const c_char, options: c_int);
#[cfg(target_os = "macos")]
path_mutation!(chflags, flags: c_uint);
// lchflags mutates the link itself; PathAt retains its lexical leaf.
#[cfg(target_os = "macos")]
path_mutation!(lchflags, flags: c_uint);
#[cfg(target_os = "macos")]
fd_mutation!(fchflags, flags: c_uint);

intercept!(link: unsafe extern "C" fn(*const c_char, *const c_char) -> c_int);
unsafe extern "C" fn link(source: *const c_char, destination: *const c_char) -> c_int {
    // SAFETY: both caller-owned path strings remain live and are forwarded.
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, source), AccessMode::READ | AccessMode::WRITE);
        handle_open(PathAt::borrow_raw(AT_FDCWD, destination), AccessMode::WRITE);
        link::original()(source, destination)
    }
}
intercept!(linkat: unsafe extern "C" fn(c_int, *const c_char, c_int, *const c_char, c_int) -> c_int);
unsafe extern "C" fn linkat(source_fd: c_int, source: *const c_char, destination_fd: c_int, destination: *const c_char, flags: c_int) -> c_int {
    // SAFETY: descriptors and path strings are borrowed until the call returns.
    unsafe {
        handle_open(PathAt::borrow_raw(source_fd, source), AccessMode::READ | AccessMode::WRITE);
        handle_open(PathAt::borrow_raw(destination_fd, destination), AccessMode::WRITE);
        linkat::original()(source_fd, source, destination_fd, destination, flags)
    }
}
intercept!(symlink: unsafe extern "C" fn(*const c_char, *const c_char) -> c_int);
unsafe extern "C" fn symlink(target: *const c_char, path: *const c_char) -> c_int {
    // SAFETY: a symlink's target is opaque content, not an access to that path.
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::WRITE);
        symlink::original()(target, path)
    }
}
intercept!(symlinkat: unsafe extern "C" fn(*const c_char, c_int, *const c_char) -> c_int);
unsafe extern "C" fn symlinkat(target: *const c_char, fd: c_int, path: *const c_char) -> c_int {
    // SAFETY: only the link pathname is observed; original arguments pass through.
    unsafe {
        handle_open(PathAt::borrow_raw(fd, path), AccessMode::WRITE);
        symlinkat::original()(target, fd, path)
    }
}
