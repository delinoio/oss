// SPDX-License-Identifier: Apache-2.0
//! Linux ownership: a dedicated child subreaper retains orphan ancestry even
//! when a descendant calls setsid. No writable delegated cgroup is assumed;
//! most user sessions do not have one. Requiring pidfd avoids signalling a
//! recycled PID while enumerating the tree.
//!
//! If the subreaper dies, its unfinished journal stays blocked for the rest of
//! the boot: orphaned descendants reparent to init and may keep writing. A
//! different `/proc/sys/kernel/random/boot_id` (random(4): generated once per
//! boot) proves that every owned process has exited (#4582).

use std::{
    collections::{HashMap, HashSet},
    fs, io,
    os::unix::process::CommandExt,
    process::Stdio,
    sync::mpsc,
};

use anyhow::{Result, anyhow};

pub use super::unix::{base_command, private_root, request_termination, sync_directory};
use super::{Backend, ProcessError, ResolvedStreams, Retention, SUPERVISOR_ARGUMENT, Scope, unix};

const BOOT_ID: &str = "/proc/sys/kernel/random/boot_id";

pub fn boot_identity() -> Result<String> {
    let id = fs::read_to_string(BOOT_ID)?.trim().to_string();
    if id.is_empty() {
        return Err(anyhow!("empty Linux boot identity"));
    }
    Ok(id)
}

fn pidfd_open(pid: i64) -> io::Result<i32> {
    // SAFETY: pidfd_open takes a PID and flags and returns a new descriptor.
    let fd = unsafe { libc::syscall(libc::SYS_pidfd_open, pid as libc::c_long, 0 as libc::c_long) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    i32::try_from(fd).map_err(|_| io::Error::other("invalid pidfd"))
}

fn close(fd: i32) {
    // SAFETY: `fd` is a descriptor owned by the caller and closed once.
    unsafe { libc::close(fd) };
}

struct LinuxBackend {
    signalled: HashSet<String>,
    /// PID of the root command. Only its `ccow-root-wait` thread may reap it.
    root_pid: Option<libc::pid_t>,
}

pub fn new_backend(scope: &mut Scope) -> Result<Box<dyn Backend>> {
    // SAFETY: prctl with PR_SET_CHILD_SUBREAPER only changes this process.
    if unsafe { libc::prctl(libc::PR_SET_CHILD_SUBREAPER, 1 as libc::c_ulong, 0, 0, 0) } != 0 {
        return Err(io::Error::last_os_error().into());
    }
    let fd = pidfd_open(i64::from(std::process::id()))?;
    close(fd);
    // Fail closed: never write an empty identity on a platform that needs one.
    let boot = super::current_boot_identity()
        .map_err(|_| anyhow::Error::new(ProcessError::Unavailable))?;
    scope.boot_id = boot;
    Ok(Box::new(LinuxBackend {
        signalled: HashSet::new(),
        root_pid: None,
    }))
}

pub fn launch(
    scope: &Scope,
    endpoint: &str,
    _streams: &ResolvedStreams,
) -> Result<Box<dyn FnOnce()>> {
    let executable = std::env::current_exe()?;
    let mut cmd = std::process::Command::new(executable);
    cmd.arg(SUPERVISOR_ARGUMENT)
        .arg(&scope.directory)
        .arg(endpoint)
        .env_clear()
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    // SAFETY: setsid is async-signal-safe and touches no Rust state.
    unsafe {
        cmd.pre_exec(|| {
            if libc::setsid() < 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let mut child = cmd.spawn()?;
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(Box::new(|| {}))
}

#[derive(Clone)]
struct ProcRow {
    pid: i64,
    ppid: i64,
    birth: String,
}

fn proc_row(pid: i64) -> Result<ProcRow> {
    let raw = fs::read(format!("/proc/{pid}/stat"))?;
    let raw = String::from_utf8_lossy(&raw);
    let end = raw
        .rfind(')')
        .ok_or_else(|| anyhow::Error::new(ProcessError::Reconciliation))?;
    let fields: Vec<&str> = raw[end + 1..].split_whitespace().collect();
    if fields.len() < 20 {
        return Err(anyhow::Error::new(ProcessError::Reconciliation));
    }
    Ok(ProcRow {
        pid,
        ppid: fields[1].parse()?,
        birth: fields[19].to_string(),
    })
}

impl Backend for LinuxBackend {
    fn start(&mut self, command: std::process::Command) -> Result<mpsc::Receiver<i32>> {
        let (pid, rx) = unix::start(command)?;
        self.root_pid = libc::pid_t::try_from(pid).ok();
        Ok(rx)
    }

    fn reap(&mut self) -> usize {
        let mut reaped = 0;
        loop {
            // SAFETY: an all-zero siginfo_t is a valid value for waitid to
            // fill.
            let mut info: libc::siginfo_t = unsafe { std::mem::zeroed() };
            // Peek without consuming: a global wait4(-1) would steal the
            // root's status from its waiter thread (ECHILD, exit code 125).
            // SAFETY: waitid writes only to `info`.
            let rc = unsafe {
                libc::waitid(
                    libc::P_ALL,
                    0,
                    &mut info,
                    libc::WEXITED | libc::WNOHANG | libc::WNOWAIT,
                )
            };
            if rc != 0 {
                if io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
                    continue;
                }
                return reaped;
            }
            // SAFETY: a successful waitid with WEXITED fills `si_pid`.
            let pid = unsafe { info.si_pid() };
            // pid 0: no exited child. The unreaped root is left to its
            // waiter thread; it is reaped within moments, so orphans queued
            // behind it are reaped on a later tick.
            if pid <= 0 || Some(pid) == self.root_pid {
                return reaped;
            }
            let mut status = 0;
            // SAFETY: waitpid writes only to `status` and targets one child.
            let done = unsafe { libc::waitpid(pid, &mut status, libc::WNOHANG) };
            if done != pid {
                return reaped;
            }
            reaped += 1;
        }
    }

    fn signal(&mut self, force: bool) -> Result<()> {
        let me = i64::from(std::process::id());
        let mut rows = Vec::new();
        for entry in fs::read_dir("/proc")? {
            let Ok(entry) = entry else { continue };
            let Some(pid) = entry
                .file_name()
                .to_str()
                .and_then(|n| n.parse::<i64>().ok())
            else {
                continue;
            };
            if let Ok(r) = proc_row(pid) {
                rows.push(r);
            }
        }
        let mut owned: HashSet<i64> = HashSet::from([me]);
        let mut changed = true;
        while changed {
            changed = false;
            for r in &rows {
                if owned.contains(&r.ppid) && owned.insert(r.pid) {
                    changed = true;
                }
            }
        }
        for r in &rows {
            if r.pid == me || !owned.contains(&r.pid) {
                continue;
            }
            let key = format!("{}:{}", r.pid, r.birth);
            if !force && self.signalled.contains(&key) {
                continue;
            }
            let Ok(fd) = pidfd_open(r.pid) else { continue };
            if proc_row(r.pid).is_ok_and(|current| current.birth == r.birth) {
                let signal = if force { libc::SIGKILL } else { libc::SIGTERM };
                // SAFETY: `fd` is a pidfd for the revalidated process.
                let sent = unsafe {
                    libc::syscall(
                        libc::SYS_pidfd_send_signal,
                        fd as libc::c_long,
                        signal as libc::c_long,
                        std::ptr::null::<libc::siginfo_t>(),
                        0 as libc::c_long,
                    )
                };
                if sent == 0 {
                    self.signalled.insert(key);
                }
            }
            close(fd);
        }
        Ok(())
    }

    fn empty(&mut self, root_done: bool) -> Result<bool> {
        if !root_done {
            return Ok(false);
        }
        // The root waiter alone reaps the root. Once it finished, ECHILD
        // proves no live descendants or unreaped adopted zombies remain;
        // process snapshots cannot.
        loop {
            let mut status = 0;
            // SAFETY: wait4 writes only to `status`; rusage is not requested.
            let pid = unsafe { libc::wait4(-1, &mut status, libc::WNOHANG, std::ptr::null_mut()) };
            if pid == 0 {
                return Ok(false);
            }
            if pid > 0 {
                continue;
            }
            let e = io::Error::last_os_error();
            match e.raw_os_error() {
                Some(libc::ECHILD) => return Ok(true),
                Some(libc::EINTR) => {}
                _ => return Err(e.into()),
            }
        }
    }
}

/// Live native ancestry under this supervisor. The caller obtains `pid` from
/// kernel-authenticated IPC, never from a request body.
pub fn contains(scope: &Scope, pid: i64) -> bool {
    if scope.owner_pid <= 0 || pid == scope.owner_pid {
        return false;
    }
    let Ok(first) = proc_row(pid) else {
        return false;
    };
    let mut seen: HashMap<i64, ()> = HashMap::new();
    let mut current = pid;
    while current > 1 && seen.insert(current, ()).is_none() {
        let Ok(r) = proc_row(current) else {
            return false;
        };
        if r.ppid == scope.owner_pid {
            return proc_row(pid).is_ok_and(|again| again.birth == first.birth);
        }
        current = r.ppid;
    }
    false
}

/// Version 2: a different boot proves whole-tree exit. In the same boot the
/// journal stays protected, because descendants of a killed subreaper can
/// still be running.
pub fn native_completion(s: &Scope) -> bool {
    if s.boot_id.is_empty() {
        return false;
    }
    super::current_boot_identity().is_ok_and(|current| current != s.boot_id)
}

/// Version 1 journals carry no persistent native identity.
pub fn legacy_completion(_s: &Scope) -> bool {
    false
}

fn supervisor_running(s: &Scope) -> bool {
    let Ok(raw) = fs::read(format!("/proc/{}/cmdline", s.owner_pid)) else {
        return false;
    };
    let args: Vec<&[u8]> = raw.split(|b| *b == 0).collect();
    args.contains(&SUPERVISOR_ARGUMENT.as_bytes()) && args.contains(&s.directory.as_bytes())
}

pub fn retention(s: &Scope) -> Retention {
    if s.version < 2 || s.boot_id.is_empty() {
        return Retention::NoNativeIdentity;
    }
    if s.owner_pid > 0 && unix::alive(s.owner_pid) && supervisor_running(s) {
        return Retention::OwnerRunning;
    }
    Retention::SameBoot
}
