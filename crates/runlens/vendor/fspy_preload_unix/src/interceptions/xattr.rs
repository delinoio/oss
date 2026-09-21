use fspy_nostd::BorrowedFd;
use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, handle_open}, libc::*, macros::intercept};

// Darwin has position/options operands and ssize_t results, unlike mutation
// calls. Observe only the file, never attribute names or returned payloads.
macro_rules! path_read {
    ($name:ident, $($arg:ident: $ty:ty),* $(,)?) => {
        intercept!($name: unsafe extern "C" fn(*const c_char, $($ty),*) -> ssize_t);
        unsafe extern "C" fn $name(path: *const c_char, $($arg: $ty),*) -> ssize_t {
            // SAFETY: preserve every caller-owned operand and native result.
            unsafe {
                handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::READ);
                $name::original()(path, $($arg),*)
            }
        }
    };
}
macro_rules! fd_read {
    ($name:ident, $($arg:ident: $ty:ty),* $(,)?) => {
        intercept!($name: unsafe extern "C" fn(c_int, $($ty),*) -> ssize_t);
        unsafe extern "C" fn $name(fd: c_int, $($arg: $ty),*) -> ssize_t {
            // SAFETY: the descriptor is borrowed without changing ownership.
            unsafe {
                handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ);
                $name::original()(fd, $($arg),*)
            }
        }
    };
}
path_read!(getxattr, name: *const c_char, value: *mut c_void, size: size_t, position: u32, options: c_int);
fd_read!(fgetxattr, name: *const c_char, value: *mut c_void, size: size_t, position: u32, options: c_int);
path_read!(listxattr, buffer: *mut c_char, size: size_t, options: c_int);
fd_read!(flistxattr, buffer: *mut c_char, size: size_t, options: c_int);
