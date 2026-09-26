//! Paired file descriptor operation hooks for the optional clibox side channel.

use libc::{c_int, c_void, off_t, size_t, ssize_t};

use crate::{
    macros::intercept,
    operation::{self, Kind},
};

intercept!(read: unsafe extern "C" fn(c_int, *mut c_void, size_t) -> ssize_t);
unsafe extern "C" fn read(fd: c_int, buffer: *mut c_void, count: size_t) -> ssize_t {
    let token = operation::enter_fd(Kind::Read, fd);
    // SAFETY: forwards the caller's original valid arguments.
    let result = unsafe { read::original()(fd, buffer, count) };
    operation::finish(token, result as i64);
    result
}

intercept!(readv: unsafe extern "C" fn(c_int, *const libc::iovec, c_int) -> ssize_t);
unsafe extern "C" fn readv(fd: c_int, vectors: *const libc::iovec, count: c_int) -> ssize_t {
    let token = operation::enter_fd(Kind::Read, fd);
    // SAFETY: the original call receives the caller's unmodified arguments.
    let result = unsafe { readv::original()(fd, vectors, count) };
    operation::finish(token, result as i64);
    result
}

intercept!(pread: unsafe extern "C" fn(c_int, *mut c_void, size_t, off_t) -> ssize_t);
unsafe extern "C" fn pread(
    fd: c_int,
    buffer: *mut c_void,
    count: size_t,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd(Kind::PositionalRead, fd);
    // SAFETY: forwards the caller's original valid arguments.
    let result = unsafe { pread::original()(fd, buffer, count, offset) };
    operation::finish(token, result as i64);
    result
}

intercept!(preadv: unsafe extern "C" fn(c_int, *const libc::iovec, c_int, off_t) -> ssize_t);
unsafe extern "C" fn preadv(
    fd: c_int,
    vectors: *const libc::iovec,
    count: c_int,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd(Kind::PositionalRead, fd);
    // SAFETY: the original call receives the caller's unmodified arguments.
    let result = unsafe { preadv::original()(fd, vectors, count, offset) };
    operation::finish(token, result as i64);
    result
}

intercept!(write: unsafe extern "C" fn(c_int, *const c_void, size_t) -> ssize_t);
unsafe extern "C" fn write(fd: c_int, buffer: *const c_void, count: size_t) -> ssize_t {
    let token = operation::enter_fd(Kind::Write, fd);
    // SAFETY: forwards the caller's original valid arguments.
    let result = unsafe { write::original()(fd, buffer, count) };
    operation::finish(token, result as i64);
    result
}

intercept!(writev: unsafe extern "C" fn(c_int, *const libc::iovec, c_int) -> ssize_t);
unsafe extern "C" fn writev(fd: c_int, vectors: *const libc::iovec, count: c_int) -> ssize_t {
    let token = operation::enter_fd(Kind::Write, fd);
    // SAFETY: the original call receives the caller's unmodified arguments.
    let result = unsafe { writev::original()(fd, vectors, count) };
    operation::finish(token, result as i64);
    result
}

intercept!(pwrite: unsafe extern "C" fn(c_int, *const c_void, size_t, off_t) -> ssize_t);
unsafe extern "C" fn pwrite(
    fd: c_int,
    buffer: *const c_void,
    count: size_t,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd(Kind::PositionalWrite, fd);
    // SAFETY: forwards the caller's original valid arguments.
    let result = unsafe { pwrite::original()(fd, buffer, count, offset) };
    operation::finish(token, result as i64);
    result
}

intercept!(pwritev: unsafe extern "C" fn(c_int, *const libc::iovec, c_int, off_t) -> ssize_t);
unsafe extern "C" fn pwritev(
    fd: c_int,
    vectors: *const libc::iovec,
    count: c_int,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd(Kind::PositionalWrite, fd);
    // SAFETY: the original call receives the caller's unmodified arguments.
    let result = unsafe { pwritev::original()(fd, vectors, count, offset) };
    operation::finish(token, result as i64);
    result
}

intercept!(close: unsafe extern "C" fn(c_int) -> c_int);
unsafe extern "C" fn close(fd: c_int) -> c_int {
    let token = operation::enter_fd(Kind::Close, fd);
    // SAFETY: forwards the caller's original descriptor.
    let result = unsafe { close::original()(fd) };
    operation::finish(token, i64::from(result));
    result
}

intercept!(fstat: unsafe extern "C" fn(c_int, *mut libc::stat) -> c_int);
unsafe extern "C" fn fstat(fd: c_int, buffer: *mut libc::stat) -> c_int {
    let token = operation::enter_fd(Kind::Metadata, fd);
    // SAFETY: forwards the caller's original descriptor and buffer.
    let result = unsafe { fstat::original()(fd, buffer) };
    operation::finish(token, i64::from(result));
    result
}
