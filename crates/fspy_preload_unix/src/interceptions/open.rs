use libc::FILE;

#[cfg(target_os = "macos")]
use crate::operation;
use crate::{
    client::{
        convert::{ModeStr, OpenFlags, PathAt},
        handle_open,
    },
    libc::{c_char, c_int},
    macros::intercept,
};

const fn has_mode_arg(o_flags: c_int) -> bool {
    if o_flags & libc::O_CREAT != 0 {
        return true;
    }
    #[cfg(target_os = "linux")]
    if o_flags & libc::O_TMPFILE != 0 {
        return true;
    }
    false
}

#[cfg(target_os = "macos")]
const fn mutates_at_open(flags: c_int) -> bool {
    flags & (libc::O_CREAT | libc::O_TRUNC) != 0
}

#[cfg(target_os = "macos")]
const unsafe fn stdio_mutates_at_open(mode: *const c_char) -> bool {
    // SAFETY: an interposed stdio call supplies the same valid mode string to libc.
    !mode.is_null() && matches!(unsafe { *mode.cast::<u8>() }, b'w' | b'a')
}

#[cfg(not(target_os = "macos"))]
type Mode = libc::mode_t;
#[cfg(target_os = "macos")] // https://github.com/tailhook/openat/issues/21#issuecomment-535914957
type Mode = c_int;

intercept!(open(64): unsafe extern "C" fn(*const c_char, c_int, args: ...) -> c_int);
unsafe extern "C" fn open(path: *const c_char, flags: c_int, mut args: ...) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: the pointer is the caller's pathname passed unchanged to libc.
    let operation = unsafe { operation::enter_open_path(path, mutates_at_open(flags)) };
    // SAFETY: path is a valid C string pointer provided by the caller of the
    // interposed function
    if !path.is_null() {
        // SAFETY: the non-null pathname is valid for the intercepted call.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), OpenFlags(flags)) };
    }
    let result = if has_mode_arg(flags) {
        // SAFETY: when O_CREAT or O_TMPFILE is set, a mode_t argument is required by
        // the open() contract
        let mode: Mode = unsafe { args.arg() };
        // SAFETY: calling the original libc open() with the same arguments forwarded
        // from the interposed function
        unsafe { open::original()(path, flags, mode) }
    } else {
        // SAFETY: calling the original libc open() with the same arguments forwarded
        // from the interposed function
        unsafe { open::original()(path, flags) }
    };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

intercept!(openat(64): unsafe extern "C" fn(c_int, *const c_char, c_int, ...) -> c_int);
unsafe extern "C" fn openat(
    dirfd: c_int,
    path: *const c_char,
    flags: c_int,
    mut args: ...
) -> c_int {
    #[cfg(target_os = "macos")]
    // SAFETY: the pointer is the caller's pathname passed unchanged to libc.
    let operation = unsafe { operation::enter_open_at(dirfd, path, mutates_at_open(flags)) };
    // SAFETY: dirfd and path are valid arguments provided by the caller of the
    // interposed function
    if !path.is_null() {
        // SAFETY: the non-null pathname and descriptor are caller-provided.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, path), OpenFlags(flags)) };
    }

    let result = if has_mode_arg(flags) {
        // https://github.com/tailhook/openat/issues/21#issuecomment-535914957
        // SAFETY: when O_CREAT or O_TMPFILE is set, a mode_t argument is required by
        // the openat() contract
        let mode: Mode = unsafe { args.arg() };
        // SAFETY: calling the original libc openat() with the same arguments forwarded
        // from the interposed function
        unsafe { openat::original()(dirfd, path, flags, mode) }
    } else {
        // SAFETY: calling the original libc openat() with the same arguments forwarded
        // from the interposed function
        unsafe { openat::original()(dirfd, path, flags) }
    };
    #[cfg(target_os = "macos")]
    operation::finish(operation, i64::from(result));
    result
}

#[cfg(target_os = "macos")]
intercept!(open_nocancel: unsafe extern "C" fn(*const c_char, c_int, ...) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn open_nocancel(path: *const c_char, flags: c_int, mut args: ...) -> c_int {
    // SAFETY: the pointer is the caller's pathname passed unchanged to libc.
    let operation = unsafe { operation::enter_open_path(path, mutates_at_open(flags)) };
    // SAFETY: path is a valid C string pointer provided by the caller of
    // open$NOCANCEL
    if !path.is_null() {
        // SAFETY: the non-null pathname is valid for the intercepted call.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), OpenFlags(flags)) };
    }
    let result = if has_mode_arg(flags) {
        // SAFETY: O_CREAT requires a mode argument, matching the open$NOCANCEL contract
        let mode: Mode = unsafe { args.arg() };
        // SAFETY: calling the original libc open$NOCANCEL() with the same arguments
        // forwarded from the interposed function
        unsafe { open_nocancel::original()(path, flags, mode) }
    } else {
        // SAFETY: calling the original libc open$NOCANCEL() with the same arguments
        // forwarded from the interposed function
        unsafe { open_nocancel::original()(path, flags) }
    };
    operation::finish(operation, i64::from(result));
    result
}

#[cfg(target_os = "macos")]
intercept!(openat_nocancel: unsafe extern "C" fn(c_int, *const c_char, c_int, ...) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn openat_nocancel(
    dirfd: c_int,
    path: *const c_char,
    flags: c_int,
    mut args: ...
) -> c_int {
    // SAFETY: the pointer is the caller's pathname passed unchanged to libc.
    let operation = unsafe { operation::enter_open_at(dirfd, path, mutates_at_open(flags)) };
    // SAFETY: dirfd and path are valid arguments provided by the caller of
    // openat$NOCANCEL
    if !path.is_null() {
        // SAFETY: the non-null pathname and descriptor are caller-provided.
        unsafe { handle_open(PathAt::borrow_raw(dirfd, path), OpenFlags(flags)) };
    }
    let result = if has_mode_arg(flags) {
        // SAFETY: O_CREAT requires a mode argument, matching the openat$NOCANCEL
        // contract
        let mode: Mode = unsafe { args.arg() };
        // SAFETY: calling the original libc openat$NOCANCEL() with the same arguments
        // forwarded from the interposed function
        unsafe { openat_nocancel::original()(dirfd, path, flags, mode) }
    } else {
        // SAFETY: calling the original libc openat$NOCANCEL() with the same arguments
        // forwarded from the interposed function
        unsafe { openat_nocancel::original()(dirfd, path, flags) }
    };
    operation::finish(operation, i64::from(result));
    result
}

intercept!(fopen(64): unsafe extern "C" fn(path: *const c_char, mode: *const c_char) -> *mut FILE);
unsafe extern "C" fn fopen(path: *const c_char, mode: *const c_char) -> *mut libc::FILE {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's pathname is forwarded unchanged to libc.
    let operation = unsafe { operation::enter_open_path(path, stdio_mutates_at_open(mode)) };
    // SAFETY: path and mode are valid C string pointers provided by the caller of
    // the interposed function
    if !path.is_null() {
        // SAFETY: the non-null pathname and mode are caller-provided strings.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), ModeStr(mode)) };
    }
    // SAFETY: calling the original libc fopen() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { fopen::original()(path, mode) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, if result.is_null() { -1 } else { 0 });
    result
}

intercept!(freopen(64): unsafe extern "C" fn(path: *const c_char, mode: *const c_char, stream: *mut FILE) -> *mut FILE);
unsafe extern "C" fn freopen(
    path: *const c_char,
    mode: *const c_char,
    stream: *mut FILE,
) -> *mut FILE {
    #[cfg(target_os = "macos")]
    // SAFETY: the caller's pathname is forwarded unchanged to libc.
    let operation = unsafe { operation::enter_open_path(path, stdio_mutates_at_open(mode)) };
    // SAFETY: path and mode are valid C string pointers provided by the caller of
    // the interposed function
    if path.is_null() {
        // freopen(NULL, ...) can reopen the stream's previous file without a
        // pathname we can recover here. Keep its native behavior, but do not
        // claim a complete trace of that possible filesystem access.
        if let Some(client) = crate::client::global_client() {
            client.mark_incomplete();
        }
    } else {
        // SAFETY: the non-null pathname and mode are caller-provided strings.
        unsafe { handle_open(fspy_nostd::CStr::from_ptr(path.cast()), ModeStr(mode)) };
    }
    // SAFETY: calling the original libc freopen() with the same arguments forwarded
    // from the interposed function
    let result = unsafe { freopen::original()(path, mode, stream) };
    #[cfg(target_os = "macos")]
    operation::finish(operation, if result.is_null() { -1 } else { 0 });
    result
}
