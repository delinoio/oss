//! Bound paired observations before Windows DLL termination callbacks run.

use winapi::{shared::minwindef::UINT, um::processthreadsapi::ExitProcess};

use crate::windows::{
    detour::{Detour, DetourAny},
    operation,
};

static DETOUR_EXIT_PROCESS: Detour<unsafe extern "system" fn(UINT)> =
    // SAFETY: the replacement has ExitProcess's exact system ABI and forwards
    // to the original function after sealing the observation interval.
    unsafe {
        Detour::new(c"ExitProcess", ExitProcess, {
            unsafe extern "system" fn new_fn(status: UINT) {
                operation::set_process_exiting(true);
                // SAFETY: forward the exact caller-supplied exit status.
                unsafe { (DETOUR_EXIT_PROCESS.real())(status) };
            }
            new_fn
        })
    };

pub const DETOURS: &[DetourAny] = &[DETOUR_EXIT_PROCESS.as_any()];
