//! Runlens patch: never make a successful CreateProcess fail merely because
//! optional collection injection failed. No replacement/helper executable is
//! used.
use fspy_detours_sys::DetourUpdateProcessWithDll;
use winapi::{
    shared::{
        minwindef::{BOOL, DWORD, LPVOID},
        ntdef::{LPCSTR, LPSTR},
    },
    um::{
        handleapi::CloseHandle,
        minwinbase::LPSECURITY_ATTRIBUTES,
        processthreadsapi::{
            CreateProcessA, CreateProcessW, LPPROCESS_INFORMATION, LPSTARTUPINFOA, LPSTARTUPINFOW,
            ResumeThread, TerminateProcess,
        },
        synchapi::WaitForSingleObject,
        winbase::{CREATE_SUSPENDED, INFINITE},
        winnt::{LPCWSTR, LPWSTR},
    },
};

use crate::windows::{
    client::global_client,
    detour::{Detour, DetourAny},
};

thread_local! {
    static IS_HOOKING_CREATE_PROCESS: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
}
struct HookGuard;
impl HookGuard {
    fn new() -> Option<Self> {
        if IS_HOOKING_CREATE_PROCESS.replace(true) {
            None
        } else {
            Some(Self)
        }
    }
}
impl Drop for HookGuard {
    fn drop(&mut self) {
        IS_HOOKING_CREATE_PROCESS.set(false);
    }
}

macro_rules! create_hook {
    ($hook:ident, $original:ident, $name:literal, $const_string:ty, $mut_string:ty, $startup:ty) => {
        static $hook: Detour<
            unsafe extern "system" fn(
                $const_string,
                $mut_string,
                LPSECURITY_ATTRIBUTES,
                LPSECURITY_ATTRIBUTES,
                BOOL,
                DWORD,
                LPVOID,
                $const_string,
                $startup,
                LPPROCESS_INFORMATION,
            ) -> BOOL,
        > = unsafe {
            Detour::new($name, $original, {
                unsafe extern "system" fn hooked(
                    application: $const_string,
                    command_line: $mut_string,
                    process_attributes: LPSECURITY_ATTRIBUTES,
                    thread_attributes: LPSECURITY_ATTRIBUTES,
                    inherit: BOOL,
                    flags: DWORD,
                    environment: LPVOID,
                    directory: $const_string,
                    startup: $startup,
                    information: LPPROCESS_INFORMATION,
                ) -> BOOL {
                    let guard = HookGuard::new();
                    // SAFETY: forward original pointers without interpreting command text.
                    let result = unsafe {
                        ($hook.real())(
                            application,
                            command_line,
                            process_attributes,
                            thread_attributes,
                            inherit,
                            if guard.is_some() {
                                flags | CREATE_SUSPENDED
                            } else {
                                flags
                            },
                            environment,
                            directory,
                            startup,
                            information,
                        )
                    };
                    if result == 0 || guard.is_none() {
                        return result;
                    }
                    // The child inherits its parent's non-breakaway Job membership
                    // before resumption. Payload failure must not leave a suspended orphan.
                    let client = unsafe { global_client() };
                    let mut dll = client.ansi_dll_path().as_ptr();
                    let prepared =
                        unsafe { client.prepare_child_process((*information).hProcess) } != 0;
                    let injected = prepared
                        && unsafe {
                            DetourUpdateProcessWithDll((*information).hProcess, &raw mut dll, 1)
                        } != 0;
                    if !injected {
                        client.report_failure();
                    }
                    if flags & CREATE_SUSPENDED == 0
                        && unsafe { ResumeThread((*information).hThread) } == u32::MAX
                    {
                        client.report_failure();
                        // Resuming failed at the OS boundary. Reap only this child;
                        // the caller receives failure without leaked handles or a live orphan.
                        unsafe {
                            TerminateProcess((*information).hProcess, 1);
                            WaitForSingleObject((*information).hProcess, INFINITE);
                            CloseHandle((*information).hThread);
                            CloseHandle((*information).hProcess);
                        }
                        return 0;
                    }
                    result
                }
                hooked
            })
        };
    };
}
create_hook!(
    DETOUR_CREATE_PROCESS_W,
    CreateProcessW,
    c"CreateProcessW",
    LPCWSTR,
    LPWSTR,
    LPSTARTUPINFOW
);
create_hook!(
    DETOUR_CREATE_PROCESS_A,
    CreateProcessA,
    c"CreateProcessA",
    LPCSTR,
    LPSTR,
    LPSTARTUPINFOA
);
pub const DETOURS: &[DetourAny] = &[
    DETOUR_CREATE_PROCESS_W.as_any(),
    DETOUR_CREATE_PROCESS_A.as_any(),
];
