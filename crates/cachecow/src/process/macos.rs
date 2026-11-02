// SPDX-License-Identifier: Apache-2.0
//! macOS ownership through the launchd resource coalition of a dedicated
//! background job. These XNU coalition interfaces are not a portable public
//! API: validate the response shape and the singleton coalition before
//! starting user code. Unsupported hosts fail closed and never fall back to
//! PID trees.
//!
//! Coalition IDs are scoped to one boot, so journal version 2 also records
//! `kern.bootsessionuuid` (#4582). A different boot session proves that every
//! owned process has exited.

use std::{
    collections::HashSet,
    ffi::CString,
    fmt::Write as _,
    fs, io,
    process::Stdio,
    sync::mpsc,
    time::{Duration, Instant},
};

use anyhow::{Result, anyhow, bail};

pub use super::unix::{base_command, private_root, request_termination, sync_directory};
use super::{
    Backend, ProcessError, ResolvedStreams, Retention, SUPERVISOR_ARGUMENT, Scope, read, unix,
};

const SYS_PROC_INFO: libc::c_int = 336;
const SYS_COALITION_INFO: libc::c_int = 459;
const PROC_INFO_CALL_PIDINFO: usize = 2;
const PROC_PIDCOALITIONINFO: usize = 20;
const COALITION_INFO_RESOURCE_USAGE: usize = 1;

/// Resource coalition ID of `pid`.
fn coalition(pid: i64) -> Result<u64> {
    let mut ids = [0u64; 5];
    // SAFETY: proc_info writes at most `size_of_val(&ids)` bytes into `ids`.
    // Every variadic argument is register-sized, as the kernel expects.
    #[allow(
        deprecated,
        reason = "libc marks syscall(2) deprecated on Apple targets"
    )]
    let n = unsafe {
        libc::syscall(
            SYS_PROC_INFO,
            PROC_INFO_CALL_PIDINFO,
            pid as usize,
            PROC_PIDCOALITIONINFO,
            0usize,
            ids.as_mut_ptr() as usize,
            std::mem::size_of_val(&ids),
        )
    };
    if n < 0 {
        return Err(io::Error::last_os_error().into());
    }
    if n as usize != std::mem::size_of_val(&ids) || ids[0] == 0 {
        return Err(anyhow::Error::new(ProcessError::Unavailable));
    }
    Ok(ids[0])
}

/// Live member count of a coalition.
fn coalition_count(id: u64) -> Result<u64> {
    let mut usage = [0u64; 2];
    let mut size: u64 = std::mem::size_of_val(&usage) as u64;
    let mut id = id;
    // SAFETY: coalition_info reads `id` and writes at most `size` bytes into
    // `usage`, then stores the copied size in `size`.
    #[allow(
        deprecated,
        reason = "libc marks syscall(2) deprecated on Apple targets"
    )]
    let r = unsafe {
        libc::syscall(
            SYS_COALITION_INFO,
            COALITION_INFO_RESOURCE_USAGE,
            (&raw mut id) as usize,
            usage.as_mut_ptr() as usize,
            (&raw mut size) as usize,
            0usize,
            0usize,
        )
    };
    if r < 0 {
        return Err(io::Error::last_os_error().into());
    }
    if size != std::mem::size_of_val(&usage) as u64 || usage[1] > usage[0] {
        return Err(anyhow::Error::new(ProcessError::Reconciliation));
    }
    Ok(usage[0] - usage[1])
}

pub fn boot_identity() -> Result<String> {
    let name = CString::new("kern.bootsessionuuid")?;
    let mut len: libc::size_t = 0;
    // SAFETY: a null buffer queries the required length.
    if unsafe {
        libc::sysctlbyname(
            name.as_ptr(),
            std::ptr::null_mut(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    } != 0
    {
        return Err(io::Error::last_os_error().into());
    }
    let mut buf = vec![0u8; len];
    // SAFETY: `buf` has `len` writable bytes.
    if unsafe {
        libc::sysctlbyname(
            name.as_ptr(),
            buf.as_mut_ptr().cast(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    } != 0
    {
        return Err(io::Error::last_os_error().into());
    }
    buf.truncate(len);
    let id = String::from_utf8(buf)?
        .trim_end_matches('\0')
        .trim()
        .to_string();
    if id.is_empty() {
        bail!("empty macOS boot session identity");
    }
    Ok(id)
}

struct DarwinBackend {
    id: u64,
    signalled: HashSet<String>,
}

pub fn new_backend(scope: &mut Scope) -> Result<Box<dyn Backend>> {
    let id = coalition(i64::from(std::process::id()))?;
    if coalition_count(id).ok() != Some(1) {
        return Err(anyhow::Error::new(ProcessError::Unavailable));
    }
    let boot = super::current_boot_identity()
        .map_err(|_| anyhow::Error::new(ProcessError::Unavailable))?;
    scope.native_id = id;
    scope.boot_id = boot;
    Ok(Box::new(DarwinBackend {
        id,
        signalled: HashSet::new(),
    }))
}

fn xml_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for c in s.chars() {
        match c {
            '&' => out.push_str("&amp;"),
            '<' => out.push_str("&lt;"),
            '>' => out.push_str("&gt;"),
            '"' => out.push_str("&#34;"),
            '\'' => out.push_str("&#39;"),
            '\t' => out.push_str("&#x9;"),
            '\n' => out.push_str("&#xA;"),
            '\r' => out.push_str("&#xD;"),
            c => out.push(c),
        }
    }
    out
}

fn launchctl(args: &[&str]) -> Result<()> {
    let status = std::process::Command::new("/bin/launchctl")
        .args(args)
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()?;
    if !status.success() {
        bail!(
            "launchctl {} failed: {status}",
            args.first().copied().unwrap_or_default()
        );
    }
    Ok(())
}

pub fn launch(
    scope: &Scope,
    endpoint: &str,
    _streams: &ResolvedStreams,
) -> Result<Box<dyn FnOnce()>> {
    let executable = std::env::current_exe()?;
    let executable = executable
        .to_str()
        .ok_or_else(|| anyhow!("executable path is not UTF-8"))?
        .to_string();
    let label = format!("io.delino.ccow.{}", scope.id);
    // SAFETY: getuid has no preconditions.
    let domain = format!("user/{}", unsafe { libc::getuid() });
    let mut plist = String::from(
        r#"<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>"#,
    );
    plist.push_str(&xml_escape(&label));
    plist.push_str("</string><key>ProgramArguments</key><array>");
    for arg in [
        executable.as_str(),
        SUPERVISOR_ARGUMENT,
        scope.directory.as_str(),
        endpoint,
    ] {
        let _ = write!(plist, "<string>{}</string>", xml_escape(arg));
    }
    plist.push_str(
        "</array><key>LimitLoadToSessionType</key><string>Background</string><key>RunAtLoad</\
         key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><true/></dict></\
         plist>",
    );
    let path = crate::paths::join2(&scope.directory, "supervisor.plist");
    {
        use std::os::unix::fs::OpenOptionsExt;
        let mut f = fs::OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(true)
            .mode(0o600)
            .open(&path)?;
        std::io::Write::write_all(&mut f, plist.as_bytes())?;
    }
    launchctl(&["bootstrap", &domain, &path])?;
    let scope = scope.clone();
    Ok(Box::new(move || {
        // Never bootout an unfinished supervisor: it still owns the writers.
        // A stale unloaded job definition is safe; interrupted recovery stays
        // blocked.
        let Ok(saved) = read(&scope) else { return };
        if !saved.complete {
            return;
        }
        // Completion precedes the release hook. Wait for supervisor exit before
        // bootout so launchd cannot interrupt that hook. This timeout only
        // leaves a stale job definition; it never terminates a live supervisor.
        let deadline = Instant::now() + Duration::from_secs(1);
        loop {
            let id = coalition(saved.owner_pid);
            // SAFETY: signal 0 only checks existence.
            let gone = unsafe { libc::kill(saved.owner_pid as libc::pid_t, 0) } != 0
                && io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH);
            if same_boot_coalition_empty(&saved) || gone || id.is_ok_and(|id| id != saved.native_id)
            {
                let _ = launchctl(&["bootout", &format!("{domain}/{label}")]);
                return;
            }
            if Instant::now() > deadline {
                return;
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }))
}

/// Process start time of `pid`, as (seconds, microseconds).
fn start_time(pid: i32) -> Option<(u64, u64)> {
    // SAFETY: zeroed proc_bsdinfo is a valid output buffer.
    let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
    let size = std::mem::size_of::<libc::proc_bsdinfo>() as libc::c_int;
    // SAFETY: proc_pidinfo writes at most `size` bytes into `info`.
    let n =
        unsafe { libc::proc_pidinfo(pid, libc::PROC_PIDTBSDINFO, 0, (&raw mut info).cast(), size) };
    (n == size).then_some((info.pbi_start_tvsec, info.pbi_start_tvusec))
}

fn all_pids() -> Result<Vec<i32>> {
    // SAFETY: a null buffer returns the current process count.
    let count = unsafe { libc::proc_listallpids(std::ptr::null_mut(), 0) };
    if count < 0 {
        return Err(io::Error::last_os_error().into());
    }
    let mut pids = vec![0i32; usize::try_from(count)? + 64];
    let bytes = libc::c_int::try_from(pids.len() * std::mem::size_of::<i32>())?;
    // SAFETY: `pids` has `bytes` writable bytes.
    let n = unsafe { libc::proc_listallpids(pids.as_mut_ptr().cast(), bytes) };
    if n < 0 {
        return Err(io::Error::last_os_error().into());
    }
    pids.truncate(usize::try_from(n)?.min(pids.len()));
    Ok(pids)
}

impl Backend for DarwinBackend {
    fn start(&mut self, command: std::process::Command) -> Result<mpsc::Receiver<i32>> {
        unix::start(command).map(|(_, rx)| rx)
    }

    fn signal(&mut self, force: bool) -> Result<()> {
        let me = std::process::id() as i32;
        for pid in all_pids()? {
            if pid <= 0 || pid == me {
                continue;
            }
            if coalition(i64::from(pid)).ok() != Some(self.id) {
                continue;
            }
            let Some(birth) = start_time(pid) else {
                continue;
            };
            let key = format!("{pid}:{}:{}", birth.0, birth.1);
            if !force && self.signalled.contains(&key) {
                continue;
            }
            if start_time(pid) != Some(birth) || coalition(i64::from(pid)).ok() != Some(self.id) {
                continue;
            }
            let signal = if force { libc::SIGKILL } else { libc::SIGTERM };
            // Darwin offers no pidfd equivalent here. Birth and membership
            // revalidation narrows PID recycling risk but cannot atomically
            // bind the signal to the identity.
            // SAFETY: kill has no memory-safety preconditions.
            if unsafe { libc::kill(pid, signal) } == 0 {
                self.signalled.insert(key);
            }
        }
        Ok(())
    }

    fn empty(&mut self, root_done: bool) -> Result<bool> {
        if !root_done {
            return Ok(false);
        }
        let n = coalition_count(self.id)?;
        if n == 0 {
            return Err(anyhow::Error::new(ProcessError::Reconciliation));
        }
        Ok(n == 1)
    }
}

pub fn contains(scope: &Scope, pid: i64) -> bool {
    if pid <= 0 || pid == scope.owner_pid || scope.native_id == 0 {
        return false;
    }
    coalition(pid).is_ok_and(|id| id == scope.native_id)
}

/// A surviving coalition with zero members is positive whole-tree evidence.
/// Missing or unsupported coalition accounting is not evidence of completion.
fn same_boot_coalition_empty(s: &Scope) -> bool {
    s.native_id != 0 && coalition_count(s.native_id).is_ok_and(|n| n == 0)
}

/// Version 2: a different boot session proves completion; within the same
/// boot the zero-member coalition check applies unchanged.
/// A version-2 journal without its recorded identity stays protected.
pub fn native_completion(s: &Scope) -> bool {
    if s.boot_id.is_empty() {
        return false;
    }
    match super::current_boot_identity() {
        Ok(current) if current != s.boot_id => true,
        Ok(_) => same_boot_coalition_empty(s),
        Err(_) => false,
    }
}

/// Version 1 journals keep the zero-member coalition check.
pub fn legacy_completion(s: &Scope) -> bool {
    same_boot_coalition_empty(s)
}

pub fn retention(s: &Scope) -> Retention {
    if s.native_id != 0 && coalition_count(s.native_id).is_ok_and(|n| n > 0) {
        return Retention::OwnerRunning;
    }
    if s.version < 2 || s.boot_id.is_empty() {
        return Retention::NoNativeIdentity;
    }
    if unix::alive(s.owner_pid) && coalition(s.owner_pid).is_ok_and(|id| id == s.native_id) {
        return Retention::OwnerRunning;
    }
    Retention::SameBoot
}
