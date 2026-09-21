use fspy_nostd::BorrowedFd;
use libc::{statfs as StatFs, statvfs as StatVfs};
use fspy_shared::ipc::AccessMode;
use crate::{client::{convert::PathAt, handle_open}, libc::*, macros::intercept};

// Darwin filesystem metadata is an input independently of file contents.
// Output structures stay entirely caller-owned; retain only the accessed path.
intercept!(statfs(64): unsafe extern "C" fn(*const c_char, *mut StatFs) -> c_int);
unsafe extern "C" fn statfs(path: *const c_char, output: *mut StatFs) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::READ);
        statfs::original()(path, output)
    }
}
intercept!(fstatfs(64): unsafe extern "C" fn(c_int, *mut StatFs) -> c_int);
unsafe extern "C" fn fstatfs(fd: c_int, output: *mut StatFs) -> c_int {
    unsafe {
        if fd >= 0 { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ); }
        fstatfs::original()(fd, output)
    }
}
intercept!(statvfs: unsafe extern "C" fn(*const c_char, *mut StatVfs) -> c_int);
unsafe extern "C" fn statvfs(path: *const c_char, output: *mut StatVfs) -> c_int {
    unsafe {
        handle_open(PathAt::borrow_raw(AT_FDCWD, path), AccessMode::READ);
        statvfs::original()(path, output)
    }
}
intercept!(fstatvfs: unsafe extern "C" fn(c_int, *mut StatVfs) -> c_int);
unsafe extern "C" fn fstatvfs(fd: c_int, output: *mut StatVfs) -> c_int {
    unsafe {
        if fd >= 0 { handle_open(BorrowedFd::borrow_raw(fd), AccessMode::READ); }
        fstatvfs::original()(fd, output)
    }
}
