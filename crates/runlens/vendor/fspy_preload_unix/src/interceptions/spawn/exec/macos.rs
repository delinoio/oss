#![allow(non_snake_case, reason = "execvP and its generated module match the Darwin ABI")]
use super::*;

intercept!(execvP: unsafe extern "C" fn(*const c_char, *const c_char, *const *mut c_char) -> c_int);
unsafe extern "C" fn execvP(prog: *const c_char, search: *const c_char, argv: *const *mut c_char) -> c_int {
    let Some(client) = global_client() else {
        return unsafe { execvP::original()(prog, search, argv) };
    };
    if search.is_null() {
        client.report_failure();
        return unsafe { execvP::original()(prog, search, argv) };
    }
    // SAFETY: Darwin requires a live NUL-terminated search string. Retain its
    // bytes, not ambient PATH, through synchronous resolution. The exec adapter
    // only reads argv and retains normal protected-image and shell-fallback rules.
    let search = unsafe { std::ffi::CStr::from_ptr(search) };
    handle_exec(
        fspy_nostd_alloc::pooled_bump(),
        ExecResolveConfig::search_path_enabled(Some(search.to_bytes().into())),
        prog, argv.cast(), unsafe { environ() },
    )
}
