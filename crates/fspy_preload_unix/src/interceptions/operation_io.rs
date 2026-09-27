//! Paired file descriptor operation hooks for the optional clibox side channel.

use libc::{c_int, c_void, off_t, size_t, ssize_t};

use crate::{
    macros::intercept,
    operation::{self, Kind},
};

intercept!(read: unsafe extern "C" fn(c_int, *mut c_void, size_t) -> ssize_t);
unsafe extern "C" fn read(fd: c_int, buffer: *mut c_void, count: size_t) -> ssize_t {
    let token = operation::enter_fd_requested(Kind::Read, fd, u64::try_from(count).ok());
    // SAFETY: forwards the caller's original valid arguments.
    let result = unsafe { read::original()(fd, buffer, count) };
    operation::finish(token, result as i64);
    result
}

intercept!(readv: unsafe extern "C" fn(c_int, *const libc::iovec, c_int) -> ssize_t);
unsafe extern "C" fn readv(fd: c_int, vectors: *const libc::iovec, count: c_int) -> ssize_t {
    let requested = operation::requested_vector_bytes(vectors, count);
    let token = operation::enter_fd_requested(Kind::Read, fd, requested);
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
    let token = operation::enter_fd_requested(Kind::PositionalRead, fd, u64::try_from(count).ok());
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
    let requested = operation::requested_vector_bytes(vectors, count);
    let token = operation::enter_fd_requested(Kind::PositionalRead, fd, requested);
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

intercept!(ftruncate: unsafe extern "C" fn(c_int, off_t) -> c_int);
unsafe extern "C" fn ftruncate(fd: c_int, length: off_t) -> c_int {
    let token = operation::enter_fd(Kind::Mutation, fd);
    // SAFETY: forwards the caller's descriptor and requested length unchanged.
    let result = unsafe { ftruncate::original()(fd, length) };
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

// macOS libc exposes cancellation-free descriptor entry points separately.
// They must use the same paired side channel as the ordinary symbols.
#[cfg(target_os = "macos")]
intercept!(read_nocancel: unsafe extern "C" fn(c_int, *mut c_void, size_t) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn read_nocancel(fd: c_int, buffer: *mut c_void, count: size_t) -> ssize_t {
    let token = operation::enter_fd_requested(Kind::Read, fd, u64::try_from(count).ok());
    // SAFETY: forwards the caller's original descriptor, buffer, and count.
    let result = unsafe { read_nocancel::original()(fd, buffer, count) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(readv_nocancel: unsafe extern "C" fn(c_int, *const libc::iovec, c_int) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn readv_nocancel(
    fd: c_int,
    vectors: *const libc::iovec,
    count: c_int,
) -> ssize_t {
    let token = operation::enter_fd_requested(
        Kind::Read,
        fd,
        operation::requested_vector_bytes(vectors, count),
    );
    // SAFETY: forwards the caller's original descriptor and vector array.
    let result = unsafe { readv_nocancel::original()(fd, vectors, count) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(pread_nocancel: unsafe extern "C" fn(c_int, *mut c_void, size_t, off_t) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn pread_nocancel(
    fd: c_int,
    buffer: *mut c_void,
    count: size_t,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd_requested(Kind::PositionalRead, fd, u64::try_from(count).ok());
    // SAFETY: forwards the caller's original descriptor, buffer, count, and offset.
    let result = unsafe { pread_nocancel::original()(fd, buffer, count, offset) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(preadv_nocancel: unsafe extern "C" fn(c_int, *const libc::iovec, c_int, off_t) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn preadv_nocancel(
    fd: c_int,
    vectors: *const libc::iovec,
    count: c_int,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd_requested(
        Kind::PositionalRead,
        fd,
        operation::requested_vector_bytes(vectors, count),
    );
    // SAFETY: forwards the caller's original descriptor, vectors, and offset.
    let result = unsafe { preadv_nocancel::original()(fd, vectors, count, offset) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(write_nocancel: unsafe extern "C" fn(c_int, *const c_void, size_t) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn write_nocancel(fd: c_int, buffer: *const c_void, count: size_t) -> ssize_t {
    let token = operation::enter_fd(Kind::Write, fd);
    // SAFETY: forwards the caller's original descriptor, buffer, and count.
    let result = unsafe { write_nocancel::original()(fd, buffer, count) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(writev_nocancel: unsafe extern "C" fn(c_int, *const libc::iovec, c_int) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn writev_nocancel(
    fd: c_int,
    vectors: *const libc::iovec,
    count: c_int,
) -> ssize_t {
    let token = operation::enter_fd(Kind::Write, fd);
    // SAFETY: forwards the caller's original descriptor and vector array.
    let result = unsafe { writev_nocancel::original()(fd, vectors, count) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(pwrite_nocancel: unsafe extern "C" fn(c_int, *const c_void, size_t, off_t) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn pwrite_nocancel(
    fd: c_int,
    buffer: *const c_void,
    count: size_t,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd(Kind::PositionalWrite, fd);
    // SAFETY: forwards the caller's original descriptor, buffer, count, and offset.
    let result = unsafe { pwrite_nocancel::original()(fd, buffer, count, offset) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(pwritev_nocancel: unsafe extern "C" fn(c_int, *const libc::iovec, c_int, off_t) -> ssize_t);
#[cfg(target_os = "macos")]
unsafe extern "C" fn pwritev_nocancel(
    fd: c_int,
    vectors: *const libc::iovec,
    count: c_int,
    offset: off_t,
) -> ssize_t {
    let token = operation::enter_fd(Kind::PositionalWrite, fd);
    // SAFETY: forwards the caller's original descriptor, vectors, and offset.
    let result = unsafe { pwritev_nocancel::original()(fd, vectors, count, offset) };
    operation::finish(token, result as i64);
    result
}

#[cfg(target_os = "macos")]
intercept!(close_nocancel: unsafe extern "C" fn(c_int) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn close_nocancel(fd: c_int) -> c_int {
    let token = operation::enter_fd(Kind::Close, fd);
    // SAFETY: forwards the caller's original descriptor.
    let result = unsafe { close_nocancel::original()(fd) };
    operation::finish(token, i64::from(result));
    result
}
