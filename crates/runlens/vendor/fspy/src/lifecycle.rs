//! Runlens lifetime patch: collection ends only after the owned process group.
//! This is operational ownership for supported finite commands, not a sandbox.
#[cfg(unix)]
pub async fn wait_unix(child: &mut tokio::process::Child, cancel: tokio_util::sync::CancellationToken) -> std::io::Result<(std::process::ExitStatus, bool)> {
    use std::{io, time::Duration};
    let group = child.id().ok_or_else(|| io::Error::other("missing child identifier"))? as i32;
    let mut incomplete = false;
    let status = tokio::select! {
        status = child.wait() => status?,
        () = cancel.cancelled() => {
            incomplete = true;
            signal(group, libc::SIGTERM)?;
            match tokio::time::timeout(Duration::from_secs(5), child.wait()).await {
                Ok(status) => status?,
                Err(_) => { signal(group, libc::SIGKILL)?; child.wait().await? }
            }
        }
    };
    if alive(group) {
        incomplete = true;
        signal(group, libc::SIGTERM)?;
        let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
        while alive(group) && tokio::time::Instant::now() < deadline {
            reap(group);
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        if alive(group) { signal(group, libc::SIGKILL)?; }
        let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
        while alive(group) && tokio::time::Instant::now() < deadline {
            reap(group);
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        if alive(group) { return Err(io::Error::other("owned process cleanup failed")); }
    }
    Ok((status, incomplete))
}
#[cfg(unix)]
fn signal(group: i32, signal: i32) -> std::io::Result<()> {
    // SAFETY: a positive group is obtained from our own child created with PGID=PID.
    if unsafe { libc::kill(-group, signal) } == 0 { return Ok(()); }
    let error = std::io::Error::last_os_error();
    if error.raw_os_error() == Some(libc::ESRCH) { Ok(()) } else { Err(error) }
}
#[cfg(unix)]
fn alive(group: i32) -> bool {
    // SAFETY: signal zero only probes the owned process group.
    unsafe { libc::kill(-group, 0) == 0 }
}
#[cfg(unix)]
fn reap(group: i32) {
    #[cfg(target_os = "linux")]
    // SAFETY: nonblocking wait is restricted to adopted children of the owned group.
    unsafe { while libc::waitpid(-group, std::ptr::null_mut(), libc::WNOHANG) > 0 {} }
    #[cfg(not(target_os = "linux"))]
    let _ = group;
}

#[cfg(windows)]
pub struct Job(windows_sys::Win32::Foundation::HANDLE);
#[cfg(windows)]
// SAFETY: the uniquely owned kernel job handle may be used on the async wait thread.
unsafe impl Send for Job {}
#[cfg(windows)]
impl Job {
    pub fn new() -> std::io::Result<Self> {
        use windows_sys::Win32::System::JobObjects::*;
        // SAFETY: null security/name arguments create a private unnamed job.
        let handle = unsafe { CreateJobObjectW(std::ptr::null(),std::ptr::null()) };
        if handle.is_null() { return Err(std::io::Error::last_os_error()); }
        let job=Self(handle);
        let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION=unsafe { std::mem::zeroed() };
        limits.BasicLimitInformation.LimitFlags=JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        // SAFETY: limits has the exact advertised type/size and handle is owned.
        if unsafe { SetInformationJobObject(handle,JobObjectExtendedLimitInformation,(&limits as *const JOBOBJECT_EXTENDED_LIMIT_INFORMATION).cast(),std::mem::size_of_val(&limits) as u32) } == 0 { return Err(std::io::Error::last_os_error()); }
        Ok(job)
    }
    pub fn assign(&self, process: std::os::windows::io::RawHandle) -> std::io::Result<()> {
        // SAFETY: the child remains suspended while ownership is assigned.
        if unsafe { windows_sys::Win32::System::JobObjects::AssignProcessToJobObject(self.0,process) } == 0 { Err(std::io::Error::last_os_error()) } else { Ok(()) }
    }
    fn active(&self) -> std::io::Result<u32> {
        use windows_sys::Win32::System::JobObjects::*;
        let mut info: JOBOBJECT_BASIC_ACCOUNTING_INFORMATION=unsafe { std::mem::zeroed() };
        // SAFETY: the output buffer has the documented type and exact size.
        if unsafe { QueryInformationJobObject(self.0,JobObjectBasicAccountingInformation,(&mut info as *mut JOBOBJECT_BASIC_ACCOUNTING_INFORMATION).cast(),std::mem::size_of_val(&info) as u32,std::ptr::null_mut()) } == 0 { return Err(std::io::Error::last_os_error()); }
        Ok(info.ActiveProcesses)
    }
    fn terminate(&self) -> std::io::Result<()> {
        // SAFETY: termination is restricted to the private job owned by this run.
        if unsafe { windows_sys::Win32::System::JobObjects::TerminateJobObject(self.0,1) } == 0 { Err(std::io::Error::last_os_error()) } else { Ok(()) }
    }
    pub async fn wait(self, child:&mut tokio::process::Child,cancel:tokio_util::sync::CancellationToken) -> std::io::Result<(std::process::ExitStatus,bool)> {
        use std::time::Duration;
        let pid=child.id().ok_or_else(|| std::io::Error::other("missing child identifier"))?;
        let mut incomplete=false;
        let status=tokio::select! {
            status=child.wait()=>status?,
            ()=cancel.cancelled()=>{
                incomplete=true;
                // SAFETY: CTRL_BREAK is addressed only to the child's new group.
                unsafe { windows_sys::Win32::System::Console::GenerateConsoleCtrlEvent(1,pid); }
                match tokio::time::timeout(Duration::from_secs(5),child.wait()).await {
                    Ok(status)=>status?,Err(_)=>{self.terminate()?;child.wait().await?}
                }
            }
        };
        if self.active()? > 0 {
            incomplete=true;
            unsafe { windows_sys::Win32::System::Console::GenerateConsoleCtrlEvent(1,pid); }
            let deadline=tokio::time::Instant::now()+Duration::from_secs(5);
            while self.active()?>0 && tokio::time::Instant::now()<deadline {tokio::time::sleep(Duration::from_millis(25)).await;}
            if self.active()?>0 {self.terminate()?;}
            let deadline=tokio::time::Instant::now()+Duration::from_secs(5);
            while self.active()?>0 && tokio::time::Instant::now()<deadline {tokio::time::sleep(Duration::from_millis(25)).await;}
            if self.active()?>0 {return Err(std::io::Error::other("owned process cleanup failed"));}
        }
        Ok((status,incomplete))
    }
}
#[cfg(windows)]
impl Drop for Job {
    fn drop(&mut self) {
        // SAFETY: this is the unique owned handle; closing also kills live members.
        unsafe {windows_sys::Win32::Foundation::CloseHandle(self.0);}
    }
}

/// Runlens-owned uninstrumented metadata/preparation subprocess. It uses the
/// same process lifetime rules as tracing without injecting a library into Git.
pub struct OwnedChild {
    pub child: tokio::process::Child,
    #[cfg(windows)]
    job: Job,
}
impl OwnedChild {
    pub fn spawn(mut command: tokio::process::Command) -> std::io::Result<Self> {
        command.kill_on_drop(true);
        #[cfg(unix)]
        {
            command.process_group(0);
            #[cfg(target_os = "linux")]
            // SAFETY: this process adopts descendants so its owned groups can be reaped.
            if unsafe { libc::prctl(libc::PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) } != 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(Self { child: command.spawn()? })
        }
        #[cfg(windows)]
        {
            use std::os::windows::{io::AsRawHandle, process::ChildExt};
            let job = Job::new()?;
            command.creation_flags(0x00000004 | 0x00000200);
            let child = command.spawn_with(|command| {
                let mut child = command.spawn()?;
                if let Err(error) = job.assign(child.as_raw_handle()) {
                    let _ = child.kill(); let _ = child.wait(); return Err(error);
                }
                // SAFETY: only the primary thread of our suspended, job-owned child is resumed.
                if unsafe { windows_sys::Win32::System::Threading::ResumeThread(child.main_thread_handle().as_raw_handle()) } == u32::MAX {
                    let error = std::io::Error::last_os_error();
                    let _ = child.kill(); let _ = child.wait(); return Err(error);
                }
                Ok(child)
            })?;
            Ok(Self { child, job })
        }
    }
    pub async fn wait(mut self, cancel: tokio_util::sync::CancellationToken) -> std::io::Result<(std::process::ExitStatus, bool)> {
        #[cfg(unix)]
        { wait_unix(&mut self.child, cancel).await }
        #[cfg(windows)]
        { self.job.wait(&mut self.child, cancel).await }
    }
}
