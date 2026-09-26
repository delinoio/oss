//! Linux syscall entry/exit supervision for newly launched processes.
//!
//! This low-level layer does not classify file operations. Consumers must
//! decode paths and results and must not report a complete file trace solely
//! because this supervisor observed syscall stops.

use std::{
    collections::{HashMap, HashSet},
    io,
    os::unix::process::CommandExt,
    process::Command,
    sync::{
        atomic::{AtomicBool, Ordering},
        Mutex,
    },
    thread,
    time::{Duration, Instant},
};

use libc::{c_void, pid_t};

const WAIT_POLL: Duration = Duration::from_millis(2);
const FORCE_CONFIRM: Duration = Duration::from_secs(5);
const GET_SYSCALL_INFO: libc::c_uint = 0x420e;
const SYSCALL_INFO_ENTRY: u8 = 1;
const SYSCALL_INFO_EXIT: u8 = 2;
const SYSCALL_INFO_SECCOMP: u8 = 3;
static TRACE_LOCK: Mutex<()> = Mutex::new(());

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TraceFailure {
    Spawn,
    Permission,
    UnsupportedKernel,
    Supervision(&'static str),
    EventLimit,
    Timeout,
    Cancellation,
    Cleanup,
}

impl std::fmt::Display for TraceFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Spawn => "spawn_failure",
            Self::Permission => "trace_permission",
            Self::UnsupportedKernel => "unsupported_trace_kernel",
            Self::Supervision(_) => "trace_supervision",
            Self::EventLimit => "event_limit",
            Self::Timeout => "timeout",
            Self::Cancellation => "cancellation",
            Self::Cleanup => "cleanup_failure",
        })
    }
}

impl std::error::Error for TraceFailure {}

fn supervision(stage: &'static str) -> TraceFailure {
    tracing::error!(
        stage,
        classification = "trace_supervision",
        "file trace failed"
    );
    TraceFailure::Supervision(stage)
}

#[derive(Debug, Clone, Copy)]
pub struct RawEntry {
    pub pid: u32,
    pub tid: u32,
    pub syscall: u64,
    pub args: [u64; 6],
    pub monotonic_ns: u64,
}

#[derive(Debug, Clone, Copy)]
pub struct RawCompletion {
    pub entry: RawEntry,
    pub monotonic_ns: u64,
    pub result: i64,
    pub failed: bool,
}

#[derive(Debug, Clone, Copy)]
pub enum ChildOutcome {
    Exit(i64),
    Signal(i32),
}

#[derive(Debug)]
pub struct TraceResult {
    pub operations: Vec<RawCompletion>,
    pub outcome: ChildOutcome,
}

#[derive(Debug, Clone, Copy)]
pub struct Limits {
    pub max_events: usize,
    pub timeout: Option<Duration>,
    pub kill_after: Duration,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            max_events: 1_000_000,
            timeout: None,
            kill_after: Duration::from_secs(5),
        }
    }
}

#[repr(C)]
#[derive(Default)]
struct SyscallInfo {
    op: u8,
    padding: [u8; 3],
    arch: u32,
    instruction_pointer: u64,
    stack_pointer: u64,
    data: [u64; 7],
}

enum Stop {
    Entry { syscall: u64, args: [u64; 6] },
    Exit { result: i64, failed: bool },
    Seccomp,
    None,
}

fn syscall_stop(tid: pid_t) -> Result<Stop, TraceFailure> {
    let mut info = SyscallInfo::default();
    // SAFETY: the kernel writes at most size_of::<SyscallInfo>() bytes into
    // the fixed C-compatible buffer while this tracee is stopped.
    let size = unsafe {
        libc::ptrace(
            GET_SYSCALL_INFO,
            tid,
            std::mem::size_of::<SyscallInfo>(),
            (&raw mut info).cast::<c_void>(),
        )
    };
    if size < 0 {
        let error = io::Error::last_os_error();
        return Err(if error.raw_os_error() == Some(libc::EINVAL) {
            TraceFailure::UnsupportedKernel
        } else {
            supervision("syscall_info_request")
        });
    }
    let stop = match info.op {
        SYSCALL_INFO_ENTRY if size as usize >= 80 => Stop::Entry {
            syscall: info.data[0],
            args: info.data[1..7]
                .try_into()
                .expect("the syscall-info entry has six arguments"),
        },
        SYSCALL_INFO_EXIT if size as usize >= 33 => Stop::Exit {
            result: info.data[0] as i64,
            failed: (info.data[1] as u8) != 0,
        },
        SYSCALL_INFO_SECCOMP => Stop::Seccomp,
        0 => Stop::None,
        _ => return Err(supervision("syscall_info_shape")),
    };
    Ok(stop)
}

fn ptrace_control(request: libc::c_uint, tid: pid_t, data: usize) -> Result<(), TraceFailure> {
    // SAFETY: all requests used here take a null address and an integer data
    // argument, and the caller owns a stopped tracee with this TID.
    let result = unsafe { libc::ptrace(request, tid, std::ptr::null_mut::<c_void>(), data) };
    if result < 0 {
        let error = io::Error::last_os_error();
        return Err(if error.raw_os_error() == Some(libc::EPERM) {
            TraceFailure::Permission
        } else {
            supervision("ptrace_control")
        });
    }
    Ok(())
}

fn set_options(tid: pid_t) -> Result<(), TraceFailure> {
    #[allow(clippy::cast_sign_loss)]
    let options = (libc::PTRACE_O_TRACESYSGOOD
        | libc::PTRACE_O_TRACECLONE
        | libc::PTRACE_O_TRACEFORK
        | libc::PTRACE_O_TRACEVFORK
        | libc::PTRACE_O_TRACEEXEC
        | libc::PTRACE_O_EXITKILL) as usize;
    ptrace_control(libc::PTRACE_SETOPTIONS, tid, options)
}

fn continue_syscall(tid: pid_t, signal: i32) -> Result<(), TraceFailure> {
    ptrace_control(libc::PTRACE_SYSCALL, tid, signal as usize)
}

fn event_child(tid: pid_t) -> Result<pid_t, TraceFailure> {
    let mut child = 0_usize;
    // SAFETY: child points to writable storage for the kernel event message;
    // the tracee is stopped at a fork/clone/vfork event.
    let result = unsafe {
        libc::ptrace(
            libc::PTRACE_GETEVENTMSG,
            tid,
            std::ptr::null_mut::<c_void>(),
            (&raw mut child).cast::<c_void>(),
        )
    };
    if result < 0 || child == 0 || child > pid_t::MAX as usize {
        return Err(supervision("event_message"));
    }
    Ok(child as pid_t)
}

fn task_pid(tid: pid_t) -> Result<u32, TraceFailure> {
    let text = std::fs::read_to_string(format!("/proc/{tid}/status"))
        .map_err(|_| supervision("thread_status_read"))?;
    text.lines()
        .find_map(|line| {
            line.strip_prefix("Tgid:")
                .and_then(|value| value.trim().parse().ok())
        })
        .ok_or(supervision("thread_group_id"))
}

fn request_signal(tasks: &HashSet<pid_t>, signal: i32) {
    for tid in tasks {
        // SAFETY: each positive TID came from a traced task. ESRCH races with
        // normal exits and is resolved by the next waitpid result.
        unsafe { libc::kill(*tid, signal) };
    }
}

struct OwnedTasks {
    tasks: HashSet<pid_t>,
}

impl Drop for OwnedTasks {
    fn drop(&mut self) {
        if self.tasks.is_empty() {
            return;
        }
        request_signal(&self.tasks, libc::SIGKILL);
        let deadline = Instant::now() + FORCE_CONFIRM;
        while !self.tasks.is_empty() && Instant::now() < deadline {
            let mut status = 0;
            // SAFETY: waitpid observes only children traced by this process;
            // WNOHANG keeps cleanup bounded when a tracee cannot be reaped.
            let tid = unsafe { libc::waitpid(-1, &raw mut status, libc::__WALL | libc::WNOHANG) };
            if tid > 0 {
                if libc::WIFEXITED(status) || libc::WIFSIGNALED(status) {
                    self.tasks.remove(&tid);
                } else if libc::WIFSTOPPED(status) {
                    let _ = continue_syscall(tid, libc::SIGKILL);
                }
            } else {
                thread::sleep(WAIT_POLL);
            }
        }
    }
}

/// Trace selected syscall entries and completions in a newly launched command
/// and all ptrace-followed descendants. The callback runs while the calling
/// thread is stopped and may delay or hold it before the syscall. A false
/// callback result ignores that syscall. The caller must classify operations
/// and enforce encoded-record limits before claiming a complete file trace.
pub fn trace<F>(
    command: &mut Command,
    limits: Limits,
    cancelled: &AtomicBool,
    mut before: F,
) -> Result<TraceResult, TraceFailure>
where
    F: FnMut(&RawEntry) -> Result<bool, TraceFailure>,
{
    // waitpid(__WALL) receives stops from every tracee owned by this process.
    // Serialize local trace sessions so their supervisors cannot consume each
    // other's stops; the command workflows already execute their runs serially.
    let _trace_lock = TRACE_LOCK.lock().map_err(|_| supervision("trace_lock"))?;
    if limits.max_events == 0 || limits.kill_after.is_zero() {
        return Err(supervision("limits"));
    }
    // SAFETY: pre_exec calls only async-signal-safe C functions. A failed
    // ptrace admission returns through Command's error pipe before exec.
    unsafe {
        command.pre_exec(|| {
            if libc::setsid() < 0 {
                return Err(io::Error::last_os_error());
            }
            if libc::ptrace(libc::PTRACE_TRACEME, 0, 0, 0) < 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let child = command.spawn().map_err(|error| {
        if error.kind() == io::ErrorKind::PermissionDenied {
            TraceFailure::Permission
        } else {
            TraceFailure::Spawn
        }
    })?;
    let root_tid = child.id() as pid_t;
    let mut owned = OwnedTasks {
        tasks: HashSet::from([root_tid]),
    };
    let mut configured = HashSet::new();
    let mut process_ids = HashMap::<pid_t, u32>::new();
    let mut pending = HashMap::<pid_t, Option<RawEntry>>::new();
    let mut operations = Vec::new();
    let mut outcome = None;
    let began = Instant::now();
    let mut failure = None;
    let mut graceful_at = None;
    let mut forced_at = None;

    while !owned.tasks.is_empty() {
        if failure.is_none() {
            if cancelled.load(Ordering::SeqCst) {
                failure = Some(TraceFailure::Cancellation);
            } else if limits
                .timeout
                .is_some_and(|timeout| began.elapsed() >= timeout)
            {
                failure = Some(TraceFailure::Timeout);
            } else if outcome.is_some() {
                failure = Some(TraceFailure::Cleanup);
            }
            if failure.is_some() {
                request_signal(&owned.tasks, libc::SIGTERM);
                graceful_at = Some(Instant::now());
            }
        }
        if graceful_at.is_some_and(|time| time.elapsed() >= limits.kill_after)
            && forced_at.is_none()
        {
            request_signal(&owned.tasks, libc::SIGKILL);
            forced_at = Some(Instant::now());
        }
        if forced_at.is_some_and(|time| time.elapsed() >= FORCE_CONFIRM) {
            return Err(TraceFailure::Cleanup);
        }
        let mut status = 0;
        // SAFETY: WNOHANG allows cancellation and cleanup deadlines to be
        // enforced even while every tracee is running or blocked.
        let tid = unsafe { libc::waitpid(-1, &raw mut status, libc::__WALL | libc::WNOHANG) };
        if tid == 0 {
            thread::sleep(WAIT_POLL);
            continue;
        }
        if tid < 0 {
            if io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
                continue;
            }
            return Err(supervision("waitpid"));
        }
        if libc::WIFEXITED(status) || libc::WIFSIGNALED(status) {
            owned.tasks.remove(&tid);
            configured.remove(&tid);
            process_ids.remove(&tid);
            if pending
                .remove(&tid)
                .is_some_and(|in_flight| in_flight.is_some())
                && failure.is_none()
            {
                return Err(supervision("unpaired_exit"));
            }
            if tid == root_tid {
                outcome = Some(if libc::WIFEXITED(status) {
                    ChildOutcome::Exit(i64::from(libc::WEXITSTATUS(status)))
                } else {
                    ChildOutcome::Signal(libc::WTERMSIG(status))
                });
            }
            continue;
        }
        if !libc::WIFSTOPPED(status) {
            return Err(supervision("wait_status"));
        }
        if !configured.contains(&tid) {
            set_options(tid)?;
            configured.insert(tid);
            process_ids.insert(tid, task_pid(tid)?);
            continue_syscall(tid, 0)?;
            continue;
        }
        let signal = libc::WSTOPSIG(status);
        let event = status >> 16;
        if signal == libc::SIGTRAP
            && matches!(
                event,
                libc::PTRACE_EVENT_CLONE | libc::PTRACE_EVENT_FORK | libc::PTRACE_EVENT_VFORK
            )
        {
            owned.tasks.insert(event_child(tid)?);
            continue_syscall(tid, 0)?;
            continue;
        }
        if signal == libc::SIGTRAP && event == libc::PTRACE_EVENT_EXEC {
            // A non-leader thread that execs becomes the thread-group leader.
            // The event message carries its former TID. Transfer its pending
            // syscall state and remove the dead TID from the owned set.
            let old_tid = event_child(tid)?;
            if old_tid != tid {
                if pending.get(&tid).is_some_and(Option::is_some) {
                    return Err(supervision("exec_leader_pending"));
                }
                pending.remove(&tid);
                if let Some(in_flight) = pending.remove(&old_tid) {
                    pending.insert(tid, in_flight);
                }
                owned.tasks.remove(&old_tid);
                configured.remove(&old_tid);
                process_ids.remove(&old_tid);
                process_ids.insert(tid, task_pid(tid)?);
            }
            continue_syscall(tid, 0)?;
            continue;
        }
        if signal == (libc::SIGTRAP | 0x80) {
            match syscall_stop(tid)? {
                Stop::Entry { syscall, args } => {
                    if pending.contains_key(&tid) {
                        return Err(supervision("duplicate_entry"));
                    }
                    let entry = RawEntry {
                        pid: *process_ids.get(&tid).ok_or(supervision("missing_tgid"))?,
                        tid: tid as u32,
                        syscall,
                        args,
                        monotonic_ns: began.elapsed().as_nanos() as u64,
                    };
                    let keep = before(&entry)?;
                    pending.insert(tid, keep.then_some(entry));
                }
                Stop::Exit { result, failed } => {
                    let entry = pending.remove(&tid).ok_or(supervision("missing_entry"))?;
                    if let Some(entry) = entry {
                        operations.push(RawCompletion {
                            entry,
                            monotonic_ns: began.elapsed().as_nanos() as u64,
                            result,
                            failed,
                        });
                        if operations.len().saturating_mul(2) > limits.max_events {
                            return Err(TraceFailure::EventLimit);
                        }
                    }
                }
                Stop::Seccomp | Stop::None => return Err(supervision("unexpected_syscall_stop")),
            }
            continue_syscall(tid, 0)?;
            continue;
        }
        // Ordinary delivery stops must preserve the target's signal. The
        // auto-attached child's initial SIGSTOP was handled above.
        continue_syscall(tid, signal)?;
    }
    if let Some(failure) = failure {
        return Err(failure);
    }
    Ok(TraceResult {
        operations,
        outcome: outcome.ok_or(supervision("missing_root_outcome"))?,
    })
}

#[cfg(test)]
mod tests {
    use std::{io::Write, process::Stdio, sync::atomic::AtomicBool};

    use super::*;

    #[test]
    fn observes_real_read_entry_and_completion() {
        let mut input = tempfile::NamedTempFile::new().unwrap();
        input.write_all(b"trace fixture\n").unwrap();
        let mut command = Command::new("/bin/cat");
        command.arg(input.path()).stdout(Stdio::null());
        let cancelled = AtomicBool::new(false);
        let result = trace(&mut command, Limits::default(), &cancelled, |entry| {
            Ok(entry.syscall == libc::SYS_read as u64)
        })
        .unwrap();
        assert!(matches!(result.outcome, ChildOutcome::Exit(0)));
        assert!(result
            .operations
            .iter()
            .any(|operation| !operation.failed && operation.result > 0));
    }

    #[test]
    fn cancellation_reaps_an_owned_child() {
        let mut command = Command::new("/bin/sleep");
        command.arg("10").stdout(Stdio::null());
        let cancelled = AtomicBool::new(true);
        let outcome = trace(&mut command, Limits::default(), &cancelled, |_| Ok(false));
        assert!(matches!(outcome, Err(TraceFailure::Cancellation)));
    }

    #[test]
    fn follows_a_forked_descendant() {
        let mut input = tempfile::NamedTempFile::new().unwrap();
        input.write_all(b"descendant fixture\n").unwrap();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg("cat \"$1\"; :")
            .arg("sh")
            .arg(input.path())
            .stdout(Stdio::null());
        let cancelled = AtomicBool::new(false);
        let result = trace(&mut command, Limits::default(), &cancelled, |entry| {
            Ok(entry.syscall == libc::SYS_read as u64)
        })
        .unwrap();
        assert!(matches!(result.outcome, ChildOutcome::Exit(0)));
        assert!(
            result
                .operations
                .iter()
                .map(|operation| operation.entry.pid)
                .collect::<HashSet<_>>()
                .len()
                >= 2
        );
    }

    #[test]
    fn timeout_terminates_the_owned_execution() {
        let mut command = Command::new("/bin/sleep");
        command.arg("10").stdout(Stdio::null());
        let cancelled = AtomicBool::new(false);
        let outcome = trace(
            &mut command,
            Limits {
                timeout: Some(Duration::from_millis(30)),
                kill_after: Duration::from_millis(30),
                ..Limits::default()
            },
            &cancelled,
            |_| Ok(false),
        );
        assert!(matches!(outcome, Err(TraceFailure::Timeout)));
    }
}
