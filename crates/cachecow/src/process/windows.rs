// SPDX-License-Identifier: Apache-2.0
//! Windows ownership through an unnamed job object.
//!
//! The job uses `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. Its handle is unnamed,
//! not inheritable and owned only by the detached supervisor, and breakaway is
//! not permitted. Windows terminates every process in the job when the last
//! handle closes, so supervisor exit (including `TerminateProcess` or a crash)
//! terminates the whole owned tree. A recorded supervisor identity of PID plus
//! creation time that no longer names a running process is therefore positive
//! whole-tree completion evidence, with or without a reboot (#4582). Windows
//! has no documented user-mode boot identity, so none is used.

use std::{
    fs,
    os::windows::{io::AsRawHandle, process::CommandExt},
    process::Stdio,
    sync::{
        atomic::{AtomicIsize, Ordering},
        mpsc,
    },
};

use anyhow::{Result, anyhow, bail};
use windows_sys::Win32::{
    Foundation::{
        CloseHandle, ERROR_INVALID_PARAMETER, FILETIME, GetLastError, HANDLE, WAIT_OBJECT_0,
    },
    System::{
        Console::{CTRL_BREAK_EVENT, GenerateConsoleCtrlEvent},
        JobObjects::{
            AssignProcessToJobObject, CreateJobObjectW, IsProcessInJob,
            JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, JOBOBJECT_BASIC_ACCOUNTING_INFORMATION,
            JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JobObjectBasicAccountingInformation,
            JobObjectExtendedLimitInformation, QueryInformationJobObject, SetInformationJobObject,
            TerminateJobObject,
        },
        SystemInformation::GetSystemDirectoryW,
        Threading::{
            CREATE_NEW_PROCESS_GROUP, CREATE_SUSPENDED, GetCurrentProcess, GetProcessTimes,
            OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, PROCESS_SYNCHRONIZE,
            WaitForSingleObject,
        },
    },
};

use super::{
    Backend, Command, ProcessError, ResolvedStreams, Retention, SUPERVISOR_ARGUMENT, Scope,
    infrastructure_error,
};

#[link(name = "ntdll", kind = "raw-dylib")]
unsafe extern "system" {
    fn NtResumeProcess(process: HANDLE) -> i32;
}

/// The supervisor's job, for IPC membership checks.
static ACTIVE_JOB: AtomicIsize = AtomicIsize::new(0);

pub fn boot_identity() -> Result<String> {
    bail!("Windows has no documented user-mode boot identity")
}

pub fn private_root(path: &str, _info: &fs::Metadata) -> Result<()> {
    use crate::winsec;
    // Require an owner-only DACL on the supplied storage root. Never rewrite a
    // caller's ACL.
    let security = winsec::read_security(
        path,
        winsec::Parts {
            owner: true,
            group: false,
            dacl: true,
        },
    )?;
    let owner = security
        .owner
        .ok_or_else(|| infrastructure_error("state directory has no owner"))?;
    if !winsec::accepted_owner(&owner)? {
        return Err(infrastructure_error(
            "state directory has a different owner",
        ));
    }
    let user = winsec::current_user()?;
    let Some(dacl) = security.dacl else {
        return Err(infrastructure_error("state directory needs a private DACL"));
    };
    // An elevated administrator's objects are owned by Administrators, so
    // compare grants with the user SID rather than the owner. Conservatively
    // reject every allow ACE for a different SID, including inherited grants.
    for ace in dacl {
        if ace.ace_type != 0 {
            return Err(infrastructure_error(
                "state directory must use simple owner-only allow ACEs",
            ));
        }
        if ace.sid != user {
            return Err(infrastructure_error(
                "state directory grants another principal access",
            ));
        }
    }
    Ok(())
}

/// The journal file is flushed; directory fsync is unavailable on Windows.
pub fn sync_directory(_path: &str) -> std::io::Result<()> {
    Ok(())
}

fn system_directory() -> Result<String> {
    let mut buf = [0u16; 512];
    // SAFETY: `buf` has the length passed to the call.
    let n = unsafe { GetSystemDirectoryW(buf.as_mut_ptr(), buf.len() as u32) } as usize;
    if n == 0 || n > buf.len() {
        return Err(std::io::Error::last_os_error().into());
    }
    String::from_utf16(&buf[..n]).map_err(|_| anyhow!("system directory is not valid UTF-16"))
}

/// npm wrappers forward `%*` through another cmd parsing pass. Quotes, percent
/// expansion and control characters cannot be passed transparently by generic
/// batch scripts, so they fail closed rather than run altered or injected
/// commands.
fn batch_quote(arg: &str) -> Result<String> {
    if arg.contains(['"', '%', '\r', '\n', '\0']) {
        bail!(
            "batch wrapper arguments containing quotes, percent signs, or control characters are \
             unavailable; invoke the underlying executable directly"
        );
    }
    // Quote every argument, including empty strings and shell metacharacters.
    // Double trailing backslashes for the receiving executable's argv parser.
    let trailing = arg.len() - arg.trim_end_matches('\\').len();
    Ok(format!("\"{arg}{}\"", "\\".repeat(trailing)))
}

pub fn base_command(c: &Command) -> Result<std::process::Command> {
    let ext = crate::paths::ext(&c.path).to_ascii_lowercase();
    if ext != ".cmd" && ext != ".bat" {
        let mut cmd = std::process::Command::new(&c.path);
        cmd.args(&c.args[1..]);
        return Ok(cmd);
    }
    let info = fs::metadata(&c.path)?;
    if !info.is_file() {
        return Err(std::io::Error::from(std::io::ErrorKind::PermissionDenied).into());
    }
    let mut parts = vec![batch_quote(&c.path)?];
    for arg in &c.args[1..] {
        parts.push(batch_quote(arg)?);
    }
    let cmd_exe = crate::paths::join2(&system_directory()?, "cmd.exe");
    let mut cmd = std::process::Command::new(cmd_exe);
    // /d disables AutoRun; /v:off disables !variable! expansion. raw_arg keeps
    // std from applying executable quoting rules to cmd's command string.
    cmd.raw_arg(format!("/d /s /v:off /c \"{}\"", parts.join(" ")));
    Ok(cmd)
}

pub fn request_termination(owner_pid: i64) -> Result<()> {
    let pid = u32::try_from(owner_pid)?;
    // The native supervisor starts in its own console process group.
    // SAFETY: GenerateConsoleCtrlEvent has no memory-safety preconditions.
    if unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid) } == 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    Ok(())
}

pub fn launch(
    scope: &Scope,
    endpoint: &str,
    streams: &ResolvedStreams,
) -> Result<Box<dyn FnOnce()>> {
    let executable = std::env::current_exe()?;
    let mut cmd = std::process::Command::new(executable);
    cmd.arg(SUPERVISOR_ARGUMENT)
        .arg(&scope.directory)
        .arg(endpoint)
        .env_clear()
        .stdin(Stdio::from(streams.stdin.try_clone()?))
        .stdout(Stdio::from(streams.stdout.try_clone()?))
        .stderr(Stdio::from(streams.stderr.try_clone()?))
        .creation_flags(CREATE_NEW_PROCESS_GROUP);
    if let Ok(root) = std::env::var("SYSTEMROOT") {
        cmd.env("SYSTEMROOT", root);
    }
    let mut child = cmd.spawn()?;
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(Box::new(|| {}))
}

fn filetime(t: FILETIME) -> i64 {
    (i64::from(t.dwHighDateTime) << 32) | i64::from(t.dwLowDateTime)
}

/// Creation time of an open process handle.
fn creation_time(process: HANDLE) -> Option<i64> {
    let zero = FILETIME {
        dwLowDateTime: 0,
        dwHighDateTime: 0,
    };
    let (mut created, mut exited, mut kernel, mut user) = (zero, zero, zero, zero);
    // SAFETY: `process` is a live handle and all outputs are valid.
    let ok = unsafe { GetProcessTimes(process, &mut created, &mut exited, &mut kernel, &mut user) };
    (ok != 0).then(|| filetime(created))
}

struct OwnedHandle(HANDLE);

impl Drop for OwnedHandle {
    fn drop(&mut self) {
        // SAFETY: the handle is owned here and closed once.
        unsafe { CloseHandle(self.0) };
    }
}

struct WindowsBackend {
    job: OwnedHandle,
    root: u32,
    signalled: bool,
}

pub fn new_backend(scope: &mut Scope) -> Result<Box<dyn Backend>> {
    // SAFETY: GetCurrentProcess returns a pseudo-handle valid for this process.
    let created = creation_time(unsafe { GetCurrentProcess() }).unwrap_or(0);
    if created == 0 {
        // Fail closed: never write an empty identity.
        return Err(anyhow::Error::new(ProcessError::Unavailable));
    }
    // SAFETY: creates an unnamed job with default security.
    let job = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
    if job.is_null() {
        return Err(std::io::Error::last_os_error().into());
    }
    let job = OwnedHandle(job);
    // SAFETY: zeroed limit information is a valid starting value.
    let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
    info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    // SAFETY: `info` matches the requested information class and size.
    if unsafe {
        SetInformationJobObject(
            job.0,
            JobObjectExtendedLimitInformation,
            (&raw const info).cast(),
            std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
        )
    } == 0
    {
        return Err(std::io::Error::last_os_error().into());
    }
    ACTIVE_JOB.store(job.0 as isize, Ordering::SeqCst);
    scope.owner_created = created;
    Ok(Box::new(WindowsBackend {
        job,
        root: 0,
        signalled: false,
    }))
}

impl Backend for WindowsBackend {
    fn start(&mut self, mut command: std::process::Command) -> Result<mpsc::Receiver<i32>> {
        // User code cannot create processes before assignment. No breakaway
        // flag is permitted; the job handle is not inherited.
        command.creation_flags(CREATE_SUSPENDED | CREATE_NEW_PROCESS_GROUP);
        let mut child = command.spawn()?;
        let handle = child.as_raw_handle() as HANDLE;
        // SAFETY: `handle` is the live child process handle owned by `child`.
        let assigned = unsafe { AssignProcessToJobObject(self.job.0, handle) } != 0;
        // SAFETY: resumes the suspended child only after assignment.
        if !assigned || unsafe { NtResumeProcess(handle) } != 0 {
            let _ = child.kill();
            let _ = child.wait();
            return Err(anyhow::Error::new(ProcessError::Reconciliation));
        }
        self.root = child.id();
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            let code = child.wait().map_or(125, super::exit_code);
            let _ = tx.send(code);
        });
        Ok(rx)
    }

    fn signal(&mut self, force: bool) -> Result<()> {
        if force {
            // SAFETY: the job handle is owned by this backend.
            if unsafe { TerminateJobObject(self.job.0, 137) } == 0 {
                return Err(std::io::Error::last_os_error().into());
            }
            return Ok(());
        }
        if !self.signalled {
            self.signalled = true;
            // SAFETY: GenerateConsoleCtrlEvent has no memory-safety
            // preconditions.
            if unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, self.root) } == 0 {
                return Err(std::io::Error::last_os_error().into());
            }
        }
        Ok(())
    }

    fn empty(&mut self, root_done: bool) -> Result<bool> {
        if !root_done {
            return Ok(false);
        }
        // SAFETY: zeroed accounting information is a valid output buffer.
        let mut info: JOBOBJECT_BASIC_ACCOUNTING_INFORMATION = unsafe { std::mem::zeroed() };
        // SAFETY: `info` matches the requested information class and size.
        if unsafe {
            QueryInformationJobObject(
                self.job.0,
                JobObjectBasicAccountingInformation,
                (&raw mut info).cast(),
                std::mem::size_of::<JOBOBJECT_BASIC_ACCOUNTING_INFORMATION>() as u32,
                std::ptr::null_mut(),
            )
        } == 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(info.ActiveProcesses == 0)
    }
}

impl Drop for WindowsBackend {
    fn drop(&mut self) {
        ACTIVE_JOB.store(0, Ordering::SeqCst);
    }
}

pub fn contains(scope: &Scope, pid: i64) -> bool {
    let job = ACTIVE_JOB.load(Ordering::SeqCst);
    if pid <= 0 || pid == scope.owner_pid || job == 0 {
        return false;
    }
    let Ok(pid) = u32::try_from(pid) else {
        return false;
    };
    // SAFETY: OpenProcess returns an owned handle or null.
    let h = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid) };
    if h.is_null() {
        return false;
    }
    let h = OwnedHandle(h);
    let mut member = 0;
    // SAFETY: both handles are live and `member` is a valid output.
    let ok = unsafe { IsProcessInJob(h.0, job as HANDLE, &mut member) } != 0;
    ok && member != 0
}

/// Whether the recorded supervisor identity is provably gone. Any unexpected
/// failure (for example access denied) is not evidence.
fn supervisor_exited(s: &Scope) -> bool {
    if s.owner_created == 0 {
        return false;
    }
    let Ok(pid) = u32::try_from(s.owner_pid) else {
        return false;
    };
    // SAFETY: OpenProcess returns an owned handle or null.
    let h = unsafe {
        OpenProcess(
            PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_SYNCHRONIZE,
            0,
            pid,
        )
    };
    if h.is_null() {
        // SAFETY: reads the calling thread's last error.
        return unsafe { GetLastError() } == ERROR_INVALID_PARAMETER;
    }
    let h = OwnedHandle(h);
    match creation_time(h.0) {
        Some(created) if created != s.owner_created => true,
        // SAFETY: `h` is a live process handle opened with SYNCHRONIZE.
        Some(_) => (unsafe { WaitForSingleObject(h.0, 0) }) == WAIT_OBJECT_0,
        None => false,
    }
}

pub fn native_completion(s: &Scope) -> bool {
    supervisor_exited(s)
}

/// Version 1 journals carry no supervisor identity.
pub fn legacy_completion(_s: &Scope) -> bool {
    false
}

pub fn retention(s: &Scope) -> Retention {
    if s.version < 2 || s.owner_created == 0 {
        return Retention::NoNativeIdentity;
    }
    Retention::OwnerRunning
}
