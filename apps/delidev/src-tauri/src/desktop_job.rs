// SPDX-License-Identifier: Apache-2.0
use std::{
    os::windows::process::{CommandExt, ProcThreadAttributeList},
    process::{Child, Command},
    ptr,
};

use windows_sys::Win32::{
    Foundation::{CloseHandle, HANDLE},
    System::JobObjects::{
        CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK,
        JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JobObjectExtendedLimitInformation,
        SetInformationJobObject,
    },
};

use crate::{NativeFailure, Result};

pub(super) struct Containment(HANDLE);
impl Drop for Containment {
    fn drop(&mut self) {
        unsafe {
            CloseHandle(self.0);
        }
    }
}
// This handle never crosses a thread or process: only the lifetime thread
// retains the unnamed non-inheritable Job. Independent Workers break away.
pub(super) fn contained_spawn(command: &mut Command) -> Result<(Child, Containment)> {
    let job = unsafe { CreateJobObjectW(ptr::null(), ptr::null()) };
    if job.is_null() {
        return Err(NativeFailure::SidecarFailed);
    }
    let job = Containment(job);
    let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
    limits.BasicLimitInformation.LimitFlags =
        JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK;
    if unsafe {
        SetInformationJobObject(
            job.0,
            JobObjectExtendedLimitInformation,
            (&limits as *const JOBOBJECT_EXTENDED_LIMIT_INFORMATION).cast(),
            std::mem::size_of_val(&limits) as u32,
        )
    } == 0
    {
        return Err(NativeFailure::SidecarFailed);
    }
    // PROC_THREAD_ATTRIBUTE_JOB_LIST attaches before the child can execute.
    // The native packaging toolchain is pinned to nightly; keep this narrow
    // std feature until Windows attribute-list support stabilizes.
    let jobs = [job.0];
    let attributes = ProcThreadAttributeList::build()
        .attribute(0x0002_000d, &jobs)
        .finish()
        .map_err(|_| NativeFailure::SidecarFailed)?;
    let child = command
        .spawn_with_attributes(&attributes)
        .map_err(|_| NativeFailure::SidecarFailed)?;
    Ok((child, job))
}
