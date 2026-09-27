mod access;
mod cwd;
mod dirent;
mod mutate;
mod open;
#[cfg(target_os = "macos")]
mod operation_io;
mod readlink;
mod spawn;
mod stat;

#[cfg(target_os = "macos")]
fn observe_path(path: *const libc::c_char, mode: impl crate::client::convert::ToAccessMode) {
    if let Some(path) = crate::operation::safe_path(path) {
        unsafe {
            crate::client::handle_open(fspy_nostd::CStr::from_ptr(path.as_ptr().cast()), mode);
        }
    }
}

#[cfg(target_os = "macos")]
fn observe_at(
    dirfd: libc::c_int,
    path: *const libc::c_char,
    mode: impl crate::client::convert::ToAccessMode,
) {
    if let Some(path) = crate::operation::safe_path(path) {
        unsafe {
            crate::client::handle_open(
                crate::client::convert::PathAt::borrow_raw(dirfd, path.as_ptr()),
                mode,
            );
        }
    }
}

#[cfg(target_os = "linux")]
fn observe_path(path: *const libc::c_char, mode: impl crate::client::convert::ToAccessMode) {
    if !path.is_null() {
        unsafe {
            crate::client::handle_open(fspy_nostd::CStr::from_ptr(path.cast()), mode);
        }
    }
}

#[cfg(target_os = "linux")]
fn observe_at(
    dirfd: libc::c_int,
    path: *const libc::c_char,
    mode: impl crate::client::convert::ToAccessMode,
) {
    if !path.is_null() {
        unsafe {
            crate::client::handle_open(
                crate::client::convert::PathAt::borrow_raw(dirfd, path),
                mode,
            )
        }
    }
}
