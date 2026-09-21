use std::thread;

use fspy_shared_unix::exec::ExecResolveConfig;
use libc::{c_char, c_int};

use crate::{
    client::{global_client, raw_exec::RawExec},
    macros::intercept,
};

type PosixSpawnFn = unsafe extern "C" fn(
    pid: *mut libc::pid_t,
    prog: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *mut c_char,
    envp: *const *mut c_char,
) -> libc::c_int;

#[expect(
    clippy::too_many_arguments,
    reason = "mirrors the posix_spawn(3) signature which requires all these parameters"
)]
unsafe fn handle_posix_spawn(
    config: ExecResolveConfig,
    original: PosixSpawnFn,
    pid: *mut libc::pid_t,
    file: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *mut c_char,
    envp: *const *mut c_char,
) -> c_int {
    struct AssertSend<T>(T);
    #[expect(
        clippy::non_send_fields_in_send_ty,
        reason = "the closure captures raw pointers that are valid for the duration of the thread::scope call, so sending them to the scoped thread is safe"
    )]
    // SAFETY: the raw pointers captured inside T are valid for the duration of the thread::scope call, so sending them to the scoped thread is safe
    unsafe impl<T> Send for AssertSend<T> {}

    let Some(client) = global_client() else {
        return unsafe { original(pid, file, file_actions, attrp, argv, envp) };
    };

    if !attrp.is_null() {
        let mut flags = 0;
        // SAFETY: attrp is the caller's live posix_spawn attributes; the getter
        // copies flags without changing the requested spawn configuration.
        let read = unsafe { libc::posix_spawnattr_getflags(attrp, &mut flags) };
        // Darwin's SETSID extension is 0x0400 (not exposed by pinned libc).
        #[cfg(target_os = "macos")]
        let setsid = 0x0400;
        #[cfg(target_os = "linux")]
        let setsid = i32::from(libc::POSIX_SPAWN_SETSID);
        if read != 0 || i32::from(flags) & (i32::from(libc::POSIX_SPAWN_SETPGROUP) | setsid) != 0 {
            client.report_failure();
        }
    }

    // SAFETY: file, argv, and envp are valid pointers forwarded from the interposed posix_spawn(p) function
    let result = unsafe {
        client.handle_exec::<c_int>(
            config,
            RawExec { prog: file, argv: argv.cast(), envp: envp.cast() },
            fspy_nostd_alloc::pooled_bump(),
            |raw_command, pre_exec| {
                let call_original = move || {
                    original(
                        pid,
                        raw_command.prog,
                        file_actions,
                        attrp,
                        raw_command.argv.cast(),
                        raw_command.envp.cast(),
                    )
                };
                if let Some(pre_exec) = pre_exec {
                    thread::scope(move |s| {
                        let call_original = AssertSend(call_original);
                        s.spawn(move || {
                            let call_original = call_original;
                            if pre_exec.run().is_err() { client.report_failure(); }

                            nix::Result::Ok((call_original.0)())
                        })
                        .join()
                        .unwrap_or_else(|_| { client.report_failure(); Err(nix::Error::EIO) })
                    })
                } else {
                    Ok(call_original())
                }
            },
        )
    };
    match result {
        Err(errno) => errno as _,
        Ok(ret) => ret,
    }
}

intercept!(posix_spawnp(64): PosixSpawnFn);
unsafe extern "C" fn posix_spawnp(
    pid: *mut libc::pid_t,
    file: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *mut c_char,
    envp: *const *mut c_char,
) -> libc::c_int {
    // SAFETY: all arguments are valid pointers forwarded from the interposed posix_spawnp function
    unsafe {
        handle_posix_spawn(
            ExecResolveConfig::search_path_enabled(None),
            posix_spawnp::original(),
            pid,
            file,
            file_actions,
            attrp,
            argv,
            envp,
        )
    }
}

intercept!(posix_spawn(64): PosixSpawnFn);
unsafe extern "C" fn posix_spawn(
    pid: *mut libc::pid_t,
    file: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *mut c_char,
    envp: *const *mut c_char,
) -> libc::c_int {
    // SAFETY: all arguments are valid pointers forwarded from the interposed posix_spawn function
    unsafe {
        handle_posix_spawn(
            ExecResolveConfig::search_path_disabled(),
            posix_spawn::original(),
            pid,
            file,
            file_actions,
            attrp,
            argv,
            envp,
        )
    }
}

// Darwin performs these opens before the child's injected library starts.
// Mark loss at action construction (even if the action is later unused) rather
// than assuming every opaque action list is unsafe: ordinary close/dup actions
// are used for pipes by supported child launches. Do not dereference the opaque
// list, retain its path, or change the native action/spawn result.
#[cfg(target_os = "macos")]
intercept!(posix_spawn_file_actions_addopen: unsafe extern "C" fn(*mut libc::posix_spawn_file_actions_t, c_int, *const c_char, c_int, libc::mode_t) -> c_int);
#[cfg(target_os = "macos")]
unsafe extern "C" fn posix_spawn_file_actions_addopen(actions: *mut libc::posix_spawn_file_actions_t, fd: c_int, path: *const c_char, flags: c_int, mode: libc::mode_t) -> c_int {
    if let Some(client) = global_client() { client.report_failure(); }
    // SAFETY: preserve the caller's live action list and every native operand.
    unsafe { posix_spawn_file_actions_addopen::original()(actions, fd, path, flags, mode) }
}
