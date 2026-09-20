use crate::{client::global_client, macros::intercept};

// The lifecycle owner uses a process group, not a security sandbox. Mark loss
// before a caller can leave it, while preserving the requested OS operation.
intercept!(setsid: unsafe extern "C" fn() -> libc::pid_t);
unsafe extern "C" fn setsid() -> libc::pid_t {
    if let Some(client) = global_client() { client.report_failure(); }
    // SAFETY: forward the original argument-free call.
    unsafe { setsid::original()() }
}
intercept!(setpgid: unsafe extern "C" fn(libc::pid_t, libc::pid_t) -> libc::c_int);
unsafe extern "C" fn setpgid(pid: libc::pid_t, group: libc::pid_t) -> libc::c_int {
    if let Some(client) = global_client() { client.report_failure(); }
    // SAFETY: preserve the caller's process/group arguments and OS result.
    unsafe { setpgid::original()(pid, group) }
}
