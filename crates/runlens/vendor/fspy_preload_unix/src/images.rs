//! Observe dyld images already mapped before attachment and subsequent loads.
#![allow(deprecated, reason = "Pinned libc exposes the stable Mach-O header ABI")]

use fspy_shared::ipc::AccessMode;

use crate::client::{global_client, handle_open};

unsafe extern "C" {
    fn _dyld_register_func_for_add_image(callback: unsafe extern "C" fn(*const libc::mach_header, isize));
    fn _dyld_shared_cache_contains_path(path: *const libc::c_char) -> bool;
}

pub fn observe() {
    // SAFETY: the callback has static lifetime. dyld invokes it for existing
    // images before returning and for future images before their initializers.
    unsafe { _dyld_register_func_for_add_image(image_added) };
}

unsafe extern "C" fn image_added(header: *const libc::mach_header, _slide: isize) {
    let Some(client) = global_client() else { return; };
    // SAFETY: dyld owns each live header throughout this callback. dladdr is
    // thread-safe, unlike enumerating the process's changing image list.
    unsafe {
        if header.is_null() { client.report_failure(); return; }
        // The main image has its own launch-bound executable identity check.
        if (*header).filetype == 2 { return; }
        let mut image = std::mem::MaybeUninit::<libc::Dl_info>::zeroed();
        let mut own = std::mem::MaybeUninit::<libc::Dl_info>::zeroed();
        if libc::dladdr(header.cast(), image.as_mut_ptr()) == 0
            || libc::dladdr(image_added as *const () as *const libc::c_void, own.as_mut_ptr()) == 0 {
            client.report_failure(); return;
        }
        let image = image.assume_init();
        if image.dli_fbase == own.assume_init().dli_fbase { return; }
        if image.dli_fbase != header.cast_mut().cast() || image.dli_fname.is_null() {
            client.report_failure(); return;
        }
        let length = libc::strnlen(image.dli_fname, libc::PATH_MAX as usize + 1);
        if length == 0 || length > libc::PATH_MAX as usize || *image.dli_fname != b'/' as libc::c_char {
            client.report_failure(); return;
        }
        handle_open(fspy_nostd::CStr::from_ptr(image.dli_fname.cast()), AccessMode::READ);
        // MH_DYLIB_IN_CACHE and dyld's active-cache membership must agree.
        // Loose libraries may have been read or initialized before our client
        // attached; naming their image cannot bind those earlier observations.
        // Retain their read evidence, but never certify complete collection.
        if (*header).flags & 0x8000_0000 == 0 || !_dyld_shared_cache_contains_path(image.dli_fname) {
            client.report_failure();
        }
    }
}
