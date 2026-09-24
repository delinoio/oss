//! Linux syscall-entry/exit collector for newly launched process trees.
//!
//! This backend is intentionally independent of the fork's seccomp pre-call
//! recorder: post-call results and thread-scoped intervention require the
//! ptrace stop on both sides of a syscall.

use std::{
    collections::{BTreeMap, VecDeque},
    ffi::{OsStr, OsString},
    fs,
    io::{self, Read as _, Write as _},
    os::{
        fd::{FromRawFd as _, OwnedFd},
        raw::c_void,
        unix::{
            ffi::OsStrExt as _,
            process::{CommandExt as _, ExitStatusExt as _},
        },
    },
    path::{Component, Path, PathBuf},
    process::{ChildStderr, Command, ExitStatus, Stdio},
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc::{Receiver, TryRecvError},
    },
    thread,
    time::{Duration, Instant},
};

use linux_raw_sys::ptrace as raw_ptrace;
use uuid::Uuid;

use crate::trace::{
    Backend, Completion, Coverage, EncodedPath, Event, FailureClass, Header, Operation,
    PathEncoding, PathScope, Platform, SCHEMA_VERSION, Start, Summary,
};

const MAX_PATH_BYTES: usize = 4096;
const SUMMARY_RESERVE_BYTES: usize = 512;
const POLL: Duration = Duration::from_millis(2);

#[derive(Clone, Copy, Debug)]
pub enum ChildIo {
    Inherit,
    Report,
    Interactive,
}

#[derive(Clone, Copy)]
pub enum BreakCommand {
    Next,
    Continue,
    Quit,
}

pub struct BreakControl<'a> {
    pub rule: &'a dyn Fn(&Start) -> bool,
    pub commands: &'a Receiver<BreakCommand>,
    pub display: &'a dyn Fn(&Start),
}

#[derive(Clone, Copy)]
pub struct CaptureRequest<'a> {
    pub root: &'a Path,
    pub program: &'a OsStr,
    pub arguments: &'a [OsString],
    pub child_io: ChildIo,
    pub timeout: Option<Duration>,
    pub kill_after: Duration,
    pub max_events: usize,
    pub max_bytes: usize,
    pub delay_rule: Option<&'a dyn Fn(&Start) -> Duration>,
    pub break_control: Option<&'a BreakControl<'a>>,
    pub child_cwd: Option<&'a Path>,
    pub stderr_match: Option<&'a str>,
    pub deny_rule: Option<&'a dyn Fn(&Start) -> bool>,
}

#[derive(Debug)]
pub enum LinuxTraceError {
    Spawn(io::Error),
    Tracing(io::Error),
    ResourceLimit,
    Timeout,
    Cancelled,
    Cleanup,
    ControlLost,
    Output(io::Error),
    CandidateBoundary,
}

impl LinuxTraceError {
    #[must_use]
    pub const fn classification(&self) -> FailureClass {
        match self {
            Self::Spawn(_) | Self::Tracing(_) => FailureClass::TracingUnavailable,
            Self::ResourceLimit => FailureClass::ResourceLimit,
            Self::Timeout => FailureClass::Timeout,
            Self::Cancelled => FailureClass::Cancelled,
            Self::Cleanup => FailureClass::CleanupFailure,
            Self::ControlLost => FailureClass::ControlLoss,
            Self::Output(_) => FailureClass::OutputFailure,
            Self::CandidateBoundary => FailureClass::CandidateBoundary,
        }
    }
}

pub struct LinuxCapture {
    pub events: Vec<Event>,
    pub root_status: Option<ExitStatus>,
    pub failure: Option<LinuxTraceError>,
    pub stderr_matched: bool,
}

impl LinuxCapture {
    #[must_use]
    pub const fn complete(&self) -> bool {
        self.failure.is_none() && self.root_status.is_some()
    }
}

struct Task {
    pending: Option<Start>,
    delayed: Option<(Instant, Instant)>,
    injected_delay_ns: u64,
    parent_pid: Option<u32>,
    initialized: bool,
}

struct Collector {
    root: PathBuf,
    started: Instant,
    sequence: u64,
    operation_id: u64,
    operation_count: u64,
    failure_count: u64,
    events: Vec<Event>,
    tasks: BTreeMap<i32, Task>,
    max_events: usize,
    max_bytes: usize,
    encoded_bytes: usize,
    paused: VecDeque<i32>,
    break_disabled: bool,
}

/// Trace a newly launched command and all children observed by ptrace.
///
/// The caller retains the returned events even on a handled failure so a
/// failed `record` can publish an explicitly incomplete framed trace.
///
/// # Errors
///
/// Returns an I/O error only before tracing begins, when the supplied root
/// cannot be resolved or the child cannot be launched.
#[expect(
    clippy::too_many_lines,
    reason = "Keep the ptrace ownership state machine together"
)]
pub fn capture(request: CaptureRequest<'_>, cancellation: &AtomicBool) -> io::Result<LinuxCapture> {
    if request.delay_rule.is_some() && request.break_control.is_some() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "delay and break controls cannot run together",
        ));
    }
    let CaptureRequest {
        root,
        program,
        arguments,
        child_io,
        timeout,
        kill_after,
        max_events,
        max_bytes,
        delay_rule,
        break_control,
        child_cwd,
        stderr_match,
        deny_rule,
    } = request;
    let root = fs::canonicalize(root)?;
    if !root.is_dir() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "root is not a directory",
        ));
    }
    let started = Instant::now();
    let platform = if cfg!(target_env = "musl") {
        Platform::LinuxMusl
    } else {
        Platform::LinuxGnu
    };
    let mut collector = Collector {
        root: root.clone(),
        started,
        sequence: 1,
        operation_id: 1,
        operation_count: 0,
        failure_count: 0,
        events: vec![Event::Header(Header {
            schema_version: SCHEMA_VERSION,
            execution_id: Uuid::now_v7(),
            platform,
            backend: Backend::Ptrace,
            root: EncodedPath::from_raw(
                PathScope::Project,
                PathEncoding::UnixBytes,
                std::os::unix::ffi::OsStrExt::as_bytes(root.as_os_str()),
            ),
            coverage: Coverage::declared(),
        })],
        tasks: BTreeMap::new(),
        max_events,
        max_bytes,
        encoded_bytes: 0,
        paused: VecDeque::new(),
        break_disabled: false,
    };
    collector.encoded_bytes = encoded_size(&collector.events[0]);
    if max_events == 0
        || collector
            .encoded_bytes
            .saturating_add(SUMMARY_RESERVE_BYTES)
            > max_bytes
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "trace limit cannot hold the header",
        ));
    }
    let mut command = Command::new(program);
    command.args(arguments);
    if let Some(cwd) = child_cwd {
        command.current_dir(cwd);
        // A candidate runs from a different directory. Keep the shell's PWD
        // hint consistent with the kernel cwd so it cannot consult the
        // original project through an inherited stale PWD value.
        command.env("PWD", cwd);
    }
    if stderr_match.is_some() {
        command.stderr(Stdio::piped());
    }
    match child_io {
        ChildIo::Inherit => {}
        ChildIo::Report | ChildIo::Interactive => {
            // SAFETY: dup returns an independently owned descriptor or -1;
            // OwnedFd closes it on every later spawn/error path.
            let duplicated_stderr = unsafe { libc::dup(libc::STDERR_FILENO) };
            if duplicated_stderr == -1 {
                return Err(io::Error::last_os_error());
            }
            // SAFETY: dup returned a fresh owned descriptor above.
            let stderr_fd = unsafe { OwnedFd::from_raw_fd(duplicated_stderr) };
            command.stdout(Stdio::from(stderr_fd));
            if matches!(child_io, ChildIo::Interactive) {
                command.stdin(Stdio::null());
            }
        }
    }
    // SAFETY: these async-signal-safe syscalls do not allocate or consult
    // process-global locks between fork and exec.
    unsafe {
        command.pre_exec(|| {
            if libc::setpgid(0, 0) == -1 {
                return Err(io::Error::last_os_error());
            }
            if libc::ptrace(
                libc::PTRACE_TRACEME,
                0,
                std::ptr::null_mut::<c_void>(),
                std::ptr::null_mut::<c_void>(),
            ) == -1
            {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    let stderr_thread = child.stderr.take().map(|pipe| {
        let needle = stderr_match.unwrap_or_default().as_bytes().to_vec();
        thread::spawn(move || stream_matching_stderr(pipe, &needle))
    });
    let root_pid = i32::try_from(child.id()).map_err(|_| io::Error::other("invalid child pid"))?;
    drop(child);
    collector.tasks.insert(
        root_pid,
        Task {
            pending: None,
            delayed: None,
            injected_delay_ns: 0,
            parent_pid: None,
            initialized: false,
        },
    );
    let mut root_status = None;
    let mut failure = None;
    let deadline = timeout.and_then(|budget| started.checked_add(budget));
    while !collector.tasks.is_empty() {
        if let Some(control) = break_control
            && let Err(error) = collector.process_break_commands(control)
        {
            failure = Some(error);
            break;
        }
        if let Err(error) = collector.resume_due_delays() {
            failure = Some(error);
            break;
        }
        if cancellation.load(Ordering::Relaxed) {
            failure = Some(LinuxTraceError::Cancelled);
            break;
        }
        if deadline.is_some_and(|limit| Instant::now() >= limit) {
            failure = Some(LinuxTraceError::Timeout);
            break;
        }
        let (tid, status) = match wait_owned(collector.tasks.keys().copied()) {
            Ok(Some(event)) => event,
            Ok(None) => {
                thread::sleep(POLL);
                continue;
            }
            Err(error) => {
                failure = Some(LinuxTraceError::Tracing(error));
                break;
            }
        };
        if libc::WIFEXITED(status) || libc::WIFSIGNALED(status) {
            if tid == root_pid {
                root_status = Some(ExitStatus::from_raw(status));
            }
            if collector
                .tasks
                .remove(&tid)
                .and_then(|task| task.pending)
                .is_some()
            {
                failure = Some(LinuxTraceError::Tracing(io::Error::other(
                    "operation lost its completion",
                )));
                break;
            }
            if root_status.is_some() && !collector.tasks.is_empty() {
                failure = Some(LinuxTraceError::Cleanup);
                break;
            }
            continue;
        }
        if !libc::WIFSTOPPED(status) {
            failure = Some(LinuxTraceError::Tracing(io::Error::other(
                "unknown ptrace state",
            )));
            break;
        }
        let signal = libc::WSTOPSIG(status);
        let event = status >> 16;
        let mut first_stop = false;
        if let Some(task) = collector.tasks.get_mut(&tid) {
            if !task.initialized {
                first_stop = true;
                let options = libc::PTRACE_O_TRACESYSGOOD
                    | libc::PTRACE_O_TRACEFORK
                    | libc::PTRACE_O_TRACEVFORK
                    | libc::PTRACE_O_TRACECLONE
                    | libc::PTRACE_O_TRACEEXEC
                    | libc::PTRACE_O_EXITKILL;
                let Ok(options) = usize::try_from(options) else {
                    failure = Some(LinuxTraceError::Tracing(io::Error::other(
                        "invalid ptrace options",
                    )));
                    break;
                };
                if let Err(error) = ptrace(tid, libc::PTRACE_SETOPTIONS, 0, options) {
                    failure = Some(LinuxTraceError::Tracing(error));
                    break;
                }
                task.initialized = true;
            }
        } else {
            failure = Some(LinuxTraceError::Tracing(io::Error::other(
                "unowned ptrace stop",
            )));
            break;
        }
        let result = if signal == (libc::SIGTRAP | 0x80) {
            collector.syscall_stop(tid, delay_rule, break_control, deny_rule)
        } else if signal == libc::SIGTRAP && event != 0 {
            collector.event_stop(tid, event)
        } else {
            // The first SIGTRAP is the post-exec ptrace stop; a newly traced
            // child arrives with SIGSTOP. Deliver other signals unchanged.
            let deliver = if first_stop && (signal == libc::SIGTRAP || signal == libc::SIGSTOP) {
                0
            } else {
                signal
            };
            Collector::resume(tid, deliver)
        };
        if let Err(error) = result {
            failure = Some(error);
            break;
        }
    }
    if failure.is_some() && cleanup(root_pid, &collector.tasks, kill_after).is_err() {
        failure = Some(LinuxTraceError::Cleanup);
    }
    let mut stderr_matched = false;
    if !matches!(failure, Some(LinuxTraceError::Cleanup))
        && let Some(handle) = stderr_thread
    {
        match handle.join() {
            Ok(Ok(matched)) => stderr_matched = matched,
            Ok(Err(error)) => failure = Some(LinuxTraceError::Output(error)),
            Err(_) => {
                failure = Some(LinuxTraceError::Output(io::Error::other(
                    "stderr forwarding failed",
                )));
            }
        }
    }
    if root_status.is_none() && failure.is_none() {
        failure = Some(LinuxTraceError::Tracing(io::Error::other(
            "root process result was lost",
        )));
    }
    let summary = Event::Summary(Summary {
        sequence: collector.sequence,
        complete: failure.is_none() && root_status.is_some(),
        child_exit_code: root_status.as_ref().and_then(ExitStatus::code),
        child_signal: root_status
            .as_ref()
            .and_then(std::os::unix::process::ExitStatusExt::signal),
        operation_count: collector.operation_count,
        failure_count: collector.failure_count,
        classification: failure.as_ref().map(LinuxTraceError::classification),
    });
    collector.events.push(summary);
    Ok(LinuxCapture {
        events: collector.events,
        root_status,
        failure,
        stderr_matched,
    })
}

fn stream_matching_stderr(mut pipe: ChildStderr, needle: &[u8]) -> io::Result<bool> {
    let mut output = io::stderr().lock();
    let mut tail = Vec::new();
    let mut matched = needle.is_empty();
    let mut forwarding_error = None;
    let mut chunk = [0u8; 8192];
    loop {
        let size = match pipe.read(&mut chunk) {
            Ok(0) => break,
            Ok(size) => size,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(error) => return Err(error),
        };
        if forwarding_error.is_none()
            && let Err(error) = output.write_all(&chunk[..size])
        {
            forwarding_error = Some(error);
        }
        if !matched {
            tail.extend_from_slice(&chunk[..size]);
            matched = tail.windows(needle.len()).any(|window| window == needle);
            if !matched {
                let keep = needle.len().saturating_sub(1);
                if tail.len() > keep {
                    tail.drain(..tail.len() - keep);
                }
            }
        }
    }
    forwarding_error.map_or(Ok(matched), Err)
}

fn cleanup(root_pid: i32, tasks: &BTreeMap<i32, Task>, kill_after: Duration) -> io::Result<()> {
    // The group covers ordinary descendants. Explicit per-task signals also
    // reach traced threads whose process changed group before cleanup.
    // SAFETY: kill receives a known positive process-group identifier.
    unsafe { libc::kill(-root_pid, libc::SIGTERM) };
    for tid in tasks.keys() {
        // SAFETY: every TID comes from a ptrace stop owned by this collector.
        unsafe { libc::kill(*tid, libc::SIGTERM) };
        let _ = ptrace(*tid, libc::PTRACE_CONT, 0, 0);
    }
    let graceful = Instant::now() + kill_after;
    let forced = graceful + Duration::from_secs(5);
    let mut remaining = tasks
        .keys()
        .copied()
        .collect::<std::collections::BTreeSet<_>>();
    while !remaining.is_empty() && Instant::now() < forced {
        match wait_owned(remaining.iter().copied()) {
            Ok(Some((tid, status))) => {
                if libc::WIFEXITED(status) || libc::WIFSIGNALED(status) {
                    remaining.remove(&tid);
                } else {
                    let _ = ptrace(tid, libc::PTRACE_CONT, 0, 0);
                }
            }
            Ok(None) => {}
            Err(error) if error.raw_os_error() == Some(libc::ECHILD) => {}
            Err(error) => return Err(error),
        }
        if Instant::now() >= graceful {
            // SAFETY: group ID originates from the spawned process.
            unsafe { libc::kill(-root_pid, libc::SIGKILL) };
            for tid in &remaining {
                // SAFETY: TIDs originate from ptrace child events.
                unsafe { libc::kill(*tid, libc::SIGKILL) };
            }
        }
        thread::sleep(POLL);
    }
    if remaining.is_empty() {
        Ok(())
    } else {
        Err(io::Error::other("traced descendants survived cleanup"))
    }
}

fn wait_owned(tids: impl Iterator<Item = i32>) -> io::Result<Option<(i32, i32)>> {
    for tid in tids {
        let mut status = 0;
        // SAFETY: waitpid writes one status word and is restricted to an
        // explicitly owned tracee, so concurrent collectors cannot consume
        // one another's ptrace stops.
        let waited = unsafe { libc::waitpid(tid, &raw mut status, libc::__WALL | libc::WNOHANG) };
        if waited == tid {
            return Ok(Some((tid, status)));
        }
        if waited == -1 {
            let error = io::Error::last_os_error();
            if error.kind() != io::ErrorKind::Interrupted {
                return Err(error);
            }
        }
    }
    Ok(None)
}

impl Collector {
    fn elapsed_ns(&self) -> u64 {
        u64::try_from(self.started.elapsed().as_nanos()).unwrap_or(u64::MAX)
    }

    fn append(&mut self, event: Event) -> Result<(), LinuxTraceError> {
        let bytes = encoded_size(&event);
        if self
            .encoded_bytes
            .saturating_add(bytes)
            .saturating_add(SUMMARY_RESERVE_BYTES)
            > self.max_bytes
            || self.events.len() >= self.max_events.saturating_mul(2).saturating_add(1)
        {
            return Err(LinuxTraceError::ResourceLimit);
        }
        self.encoded_bytes += bytes;
        self.events.push(event);
        self.sequence += 1;
        Ok(())
    }

    fn resume(tid: i32, signal: i32) -> Result<(), LinuxTraceError> {
        ptrace(
            tid,
            libc::PTRACE_SYSCALL,
            0,
            usize::try_from(signal).expect("nonnegative signal"),
        )
        .map(|_| ())
        .map_err(LinuxTraceError::Tracing)
    }

    fn event_stop(&mut self, tid: i32, event: i32) -> Result<(), LinuxTraceError> {
        if event == libc::PTRACE_EVENT_FORK
            || event == libc::PTRACE_EVENT_VFORK
            || event == libc::PTRACE_EVENT_CLONE
        {
            let mut child_tid: libc::c_ulong = 0;
            ptrace(
                tid,
                libc::PTRACE_GETEVENTMSG,
                0,
                (&raw mut child_tid) as usize,
            )
            .map_err(LinuxTraceError::Tracing)?;
            let child_tid = i32::try_from(child_tid).map_err(|_| {
                LinuxTraceError::Tracing(io::Error::other("invalid traced child id"))
            })?;
            self.tasks.entry(child_tid).or_insert_with(|| Task {
                pending: None,
                delayed: None,
                injected_delay_ns: 0,
                parent_pid: Some(process_id(tid)),
                initialized: false,
            });
        }
        if event == libc::PTRACE_EVENT_EXEC {
            // An exec from a nonleader thread changes its TID. The old TID
            // is supplied by GETEVENTMSG; move any pending state to the new one.
            let mut old_tid: libc::c_ulong = 0;
            ptrace(
                tid,
                libc::PTRACE_GETEVENTMSG,
                0,
                (&raw mut old_tid) as usize,
            )
            .map_err(LinuxTraceError::Tracing)?;
            if old_tid != libc::c_ulong::from(tid.cast_unsigned())
                && let Ok(old_tid) = i32::try_from(old_tid)
                && let Some(task) = self.tasks.remove(&old_tid)
            {
                self.tasks.insert(tid, task);
            }
        }
        Self::resume(tid, 0)
    }

    fn syscall_stop(
        &mut self,
        tid: i32,
        delay_rule: Option<&dyn Fn(&Start) -> Duration>,
        break_control: Option<&BreakControl<'_>>,
        deny_rule: Option<&dyn Fn(&Start) -> bool>,
    ) -> Result<(), LinuxTraceError> {
        let mut info = std::mem::MaybeUninit::<raw_ptrace::ptrace_syscall_info>::zeroed();
        ptrace(
            tid,
            raw_ptrace::PTRACE_GET_SYSCALL_INFO,
            std::mem::size_of::<raw_ptrace::ptrace_syscall_info>(),
            info.as_mut_ptr() as usize,
        )
        .map_err(LinuxTraceError::Tracing)?;
        // SAFETY: a successful PTRACE_GET_SYSCALL_INFO initializes the buffer.
        let info = unsafe { info.assume_init() };
        let mut hold = false;
        match u32::from(info.op) {
            raw_ptrace::PTRACE_SYSCALL_INFO_ENTRY => {
                // SAFETY: the op discriminant identifies the initialized union arm.
                let entry = unsafe { info.__bindgen_anon_1.entry };
                let parent = self.tasks.get(&tid).and_then(|task| task.parent_pid);
                if let Some((operation, paths)) = self.identify(tid, entry.nr, entry.args)? {
                    if self.operation_count >= u64::try_from(self.max_events).unwrap_or(u64::MAX) {
                        return Err(LinuxTraceError::ResourceLimit);
                    }
                    let start = Start {
                        sequence: self.sequence,
                        operation_id: self.operation_id,
                        pid: process_id(tid),
                        tid: tid.cast_unsigned(),
                        parent_pid: parent,
                        operation,
                        paths,
                        monotonic_ns: self.elapsed_ns(),
                    };
                    self.operation_id += 1;
                    self.append(Event::OperationStart(start.clone()))?;
                    if deny_rule.is_some_and(|rule| rule(&start)) {
                        return Err(LinuxTraceError::CandidateBoundary);
                    }
                    if let Some(task) = self.tasks.get_mut(&tid) {
                        task.pending = Some(start.clone());
                        if let Some(duration) = delay_rule.map(|rule| rule(&start))
                            && !duration.is_zero()
                        {
                            let now = Instant::now();
                            let until = now.checked_add(duration).ok_or_else(|| {
                                LinuxTraceError::Tracing(io::Error::other("invalid injected delay"))
                            })?;
                            task.delayed = Some((now, until));
                            hold = true;
                        }
                    }
                    if let Some(control) = break_control
                        && !self.break_disabled
                        && (control.rule)(&start)
                    {
                        (control.display)(&start);
                        self.paused.push_back(tid);
                        hold = true;
                    }
                }
            }
            raw_ptrace::PTRACE_SYSCALL_INFO_EXIT => {
                // SAFETY: the op discriminant identifies the initialized union arm.
                let exit = unsafe { info.__bindgen_anon_1.exit };
                let pending = self.tasks.get_mut(&tid).and_then(|task| {
                    task.pending
                        .take()
                        .map(|start| (start, std::mem::take(&mut task.injected_delay_ns)))
                });
                if let Some((start, injected_delay_ns)) = pending {
                    let native_error = (exit.is_error != 0).then_some(-exit.rval);
                    let bytes = if native_error.is_none()
                        && matches!(
                            start.operation,
                            Operation::Read
                                | Operation::Pread
                                | Operation::Write
                                | Operation::Pwrite
                        ) {
                        Some(exit.rval.cast_unsigned())
                    } else {
                        None
                    };
                    let completion = Completion {
                        sequence: self.sequence,
                        operation_id: start.operation_id,
                        monotonic_ns: self.elapsed_ns(),
                        native_result: exit.rval,
                        native_error,
                        bytes,
                        injected_delay_ns,
                        resolved_paths: self.resolved_paths(tid, &start, exit.rval),
                    };
                    self.append(Event::OperationCompletion(completion))?;
                    self.failure_count += u64::from(native_error.is_some());
                    self.operation_count += 1;
                }
            }
            _ => {
                return Err(LinuxTraceError::Tracing(io::Error::other(
                    "unsupported syscall stop",
                )));
            }
        }
        if hold { Ok(()) } else { Self::resume(tid, 0) }
    }

    fn resume_due_delays(&mut self) -> Result<(), LinuxTraceError> {
        let now = Instant::now();
        for (&tid, task) in &mut self.tasks {
            if let Some((started, until)) = task.delayed
                && now >= until
            {
                task.delayed = None;
                task.injected_delay_ns =
                    u64::try_from(started.elapsed().as_nanos()).unwrap_or(u64::MAX);
                Self::resume(tid, 0)?;
            }
        }
        Ok(())
    }

    fn process_break_commands(
        &mut self,
        control: &BreakControl<'_>,
    ) -> Result<(), LinuxTraceError> {
        if self.break_disabled {
            return Ok(());
        }
        loop {
            match control.commands.try_recv() {
                Ok(BreakCommand::Next) => {
                    if let Some(tid) = self.paused.pop_front() {
                        Self::resume(tid, 0)?;
                    }
                }
                Ok(BreakCommand::Continue) => {
                    self.break_disabled = true;
                    while let Some(tid) = self.paused.pop_front() {
                        Self::resume(tid, 0)?;
                    }
                    return Ok(());
                }
                Ok(BreakCommand::Quit) => return Err(LinuxTraceError::Cancelled),
                Err(TryRecvError::Empty) => return Ok(()),
                Err(TryRecvError::Disconnected) => return Err(LinuxTraceError::ControlLost),
            }
        }
    }

    fn resolved_paths(&self, tid: i32, start: &Start, result: i64) -> Vec<EncodedPath> {
        if result < 0 || start.operation != Operation::Open {
            return Vec::new();
        }
        let link = PathBuf::from(format!("/proc/{tid}/fd/{result}"));
        fs::read_link(link)
            .ok()
            .map(|path| self.encode_path(&path))
            .into_iter()
            .collect()
    }

    #[expect(
        clippy::too_many_lines,
        reason = "Keep the supported syscall mapping in one exhaustive match"
    )]
    fn identify(
        &self,
        tid: i32,
        number: u64,
        args: [u64; 6],
    ) -> Result<Option<(Operation, Vec<EncodedPath>)>, LinuxTraceError> {
        let cwd = i64::from(libc::AT_FDCWD).cast_unsigned();
        let nr = i64::from_ne_bytes(number.to_ne_bytes());
        let a = args;
        let (operation, paths) = match nr {
            libc::SYS_read | libc::SYS_readv => (
                Operation::Read,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_pread64 | libc::SYS_preadv | libc::SYS_preadv2 => (
                Operation::Pread,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_write | libc::SYS_writev => (
                Operation::Write,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_pwrite64 | libc::SYS_pwritev | libc::SYS_pwritev2 => (
                Operation::Pwrite,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_close => (
                Operation::Close,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_openat | libc::SYS_openat2 => {
                (Operation::Open, vec![self.remote_path(tid, a[0], a[1])?])
            }
            libc::SYS_newfstatat | libc::SYS_faccessat | libc::SYS_faccessat2 | libc::SYS_statx => {
                (
                    Operation::Metadata,
                    vec![self.remote_path(tid, a[0], a[1])?],
                )
            }
            libc::SYS_readlinkat => (
                Operation::Metadata,
                vec![self.remote_path(tid, a[0], a[1])?],
            ),
            libc::SYS_fstat => (
                Operation::Metadata,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_fchmod | libc::SYS_fchown | libc::SYS_ftruncate => (
                Operation::OtherMutation,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_getdents64 => (
                Operation::Directory,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_unlinkat
            | libc::SYS_mkdirat
            | libc::SYS_mknodat
            | libc::SYS_fchmodat
            | libc::SYS_fchownat
            | libc::SYS_utimensat => (
                Operation::OtherMutation,
                vec![self.remote_path(tid, a[0], a[1])?],
            ),
            libc::SYS_chdir => (Operation::Metadata, vec![self.remote_path(tid, cwd, a[0])?]),
            libc::SYS_fchdir => (
                Operation::Metadata,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            libc::SYS_renameat | libc::SYS_renameat2 => (
                Operation::Rename,
                vec![
                    self.remote_path(tid, a[0], a[1])?,
                    self.remote_path(tid, a[2], a[3])?,
                ],
            ),
            libc::SYS_linkat => (
                Operation::Link,
                vec![
                    self.remote_path(tid, a[0], a[1])?,
                    self.remote_path(tid, a[2], a[3])?,
                ],
            ),
            libc::SYS_symlinkat => (Operation::Link, vec![self.remote_path(tid, a[1], a[2])?]),
            libc::SYS_execve => (Operation::Open, vec![self.remote_path(tid, cwd, a[0])?]),
            libc::SYS_execveat => (Operation::Open, vec![self.remote_path(tid, a[0], a[1])?]),
            #[cfg(target_arch = "x86_64")]
            libc::SYS_open | libc::SYS_creat => {
                (Operation::Open, vec![self.remote_path(tid, cwd, a[0])?])
            }
            #[cfg(target_arch = "x86_64")]
            libc::SYS_stat | libc::SYS_lstat | libc::SYS_access | libc::SYS_readlink => {
                (Operation::Metadata, vec![self.remote_path(tid, cwd, a[0])?])
            }
            #[cfg(target_arch = "x86_64")]
            libc::SYS_getdents => (
                Operation::Directory,
                self.fd_path(tid, a[0]).into_iter().collect(),
            ),
            #[cfg(target_arch = "x86_64")]
            libc::SYS_unlink
            | libc::SYS_rmdir
            | libc::SYS_mkdir
            | libc::SYS_mknod
            | libc::SYS_chmod
            | libc::SYS_chown
            | libc::SYS_truncate => (
                Operation::OtherMutation,
                vec![self.remote_path(tid, cwd, a[0])?],
            ),
            #[cfg(target_arch = "x86_64")]
            libc::SYS_rename => (
                Operation::Rename,
                vec![
                    self.remote_path(tid, cwd, a[0])?,
                    self.remote_path(tid, cwd, a[1])?,
                ],
            ),
            #[cfg(target_arch = "x86_64")]
            libc::SYS_link => (
                Operation::Link,
                vec![
                    self.remote_path(tid, cwd, a[0])?,
                    self.remote_path(tid, cwd, a[1])?,
                ],
            ),
            #[cfg(target_arch = "x86_64")]
            libc::SYS_symlink => (Operation::Link, vec![self.remote_path(tid, cwd, a[1])?]),
            _ => return Ok(None),
        };
        if paths.is_empty() {
            Ok(None)
        } else {
            Ok(Some((operation, paths)))
        }
    }

    fn fd_path(&self, tid: i32, fd: u64) -> Option<EncodedPath> {
        let fd = i32::try_from(fd).ok()?;
        let path = fs::read_link(format!("/proc/{tid}/fd/{fd}")).ok()?;
        if !path.is_absolute() || path.starts_with("/dev/") {
            return None;
        }
        Some(self.encode_path(&path))
    }

    fn remote_path(
        &self,
        tid: i32,
        dirfd: u64,
        pointer: u64,
    ) -> Result<EncodedPath, LinuxTraceError> {
        let path = read_cstr(tid, pointer).map_err(LinuxTraceError::Tracing)?;
        let relative = Path::new(OsStr::from_bytes(&path));
        let absolute = if relative.is_absolute() {
            relative.to_path_buf()
        } else {
            let base = if dirfd == i64::from(libc::AT_FDCWD).cast_unsigned() {
                fs::read_link(format!("/proc/{tid}/cwd"))
            } else {
                let fd = i32::try_from(dirfd).map_err(|_| {
                    LinuxTraceError::Tracing(io::Error::other("invalid directory descriptor"))
                })?;
                fs::read_link(format!("/proc/{tid}/fd/{fd}"))
            }
            .map_err(LinuxTraceError::Tracing)?;
            base.join(relative)
        };
        Ok(self.encode_path(&normalize(&absolute)))
    }

    fn encode_path(&self, path: &Path) -> EncodedPath {
        use std::os::unix::ffi::OsStrExt as _;
        path.strip_prefix(&self.root).map_or_else(
            |_| {
                EncodedPath::from_raw(
                    PathScope::External,
                    PathEncoding::UnixBytes,
                    path.as_os_str().as_bytes(),
                )
            },
            |relative| {
                let raw = relative.as_os_str().as_bytes();
                EncodedPath::from_raw(
                    PathScope::Project,
                    PathEncoding::UnixBytes,
                    if raw.is_empty() { b"." } else { raw },
                )
            },
        )
    }
}

fn process_id(tid: i32) -> u32 {
    fs::read_to_string(format!("/proc/{tid}/status"))
        .ok()
        .and_then(|text| {
            text.lines().find_map(|line| {
                line.strip_prefix("Tgid:")
                    .and_then(|value| value.trim().parse::<u32>().ok())
            })
        })
        .unwrap_or_else(|| tid.cast_unsigned())
}

fn encoded_size(event: &Event) -> usize {
    serde_json::to_vec(event).map_or(usize::MAX, |bytes| bytes.len().saturating_add(1))
}

fn normalize(path: &Path) -> PathBuf {
    let mut result = PathBuf::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                result.pop();
            }
            Component::CurDir => {}
            other => result.push(other.as_os_str()),
        }
    }
    result
}

fn read_cstr(tid: i32, pointer: u64) -> io::Result<Vec<u8>> {
    if pointer == 0 {
        return Err(io::Error::other("null pathname"));
    }
    let mut bytes = Vec::with_capacity(256);
    while bytes.len() < MAX_PATH_BYTES {
        let mut chunk = [0u8; 256];
        let address = pointer
            .checked_add(u64::try_from(bytes.len()).expect("bounded pathname length"))
            .and_then(|address| usize::try_from(address).ok())
            .ok_or_else(|| io::Error::other("pathname pointer overflow"))?;
        let remaining = MAX_PATH_BYTES - bytes.len();
        let remote = libc::iovec {
            iov_base: address as *mut c_void,
            iov_len: chunk.len().min(remaining),
        };
        let local = libc::iovec {
            iov_base: chunk.as_mut_ptr().cast(),
            iov_len: remote.iov_len,
        };
        // SAFETY: the local iovec points to the writable chunk. The kernel
        // validates the remote tracee address and returns a short read at an
        // unreadable page boundary, which the next iteration handles.
        let read =
            unsafe { libc::process_vm_readv(tid, &raw const local, 1, &raw const remote, 1, 0) };
        if read <= 0 {
            return Err(io::Error::last_os_error());
        }
        let size =
            usize::try_from(read).map_err(|_| io::Error::other("invalid pathname length"))?;
        if let Some(terminator) = chunk[..size].iter().position(|byte| *byte == 0) {
            bytes.extend_from_slice(&chunk[..terminator]);
            return Ok(bytes);
        }
        bytes.extend_from_slice(&chunk[..size]);
    }
    Err(io::Error::other("pathname exceeds tracing limit"))
}

fn ptrace(
    tid: i32,
    request: impl TryInto<i32>,
    address: usize,
    data: usize,
) -> io::Result<libc::c_long> {
    let request = request
        .try_into()
        .map_err(|_| io::Error::other("invalid ptrace request"))?;
    // SAFETY: callers pass a stopped tracee and request-specific address/data
    // pointers. The kernel validates the request and reports errors by errno.
    #[cfg(target_env = "musl")]
    let result = unsafe { libc::ptrace(request, tid, address as *mut c_void, data as *mut c_void) };
    #[cfg(not(target_env = "musl"))]
    // SAFETY: the request is converted from the same validated integer;
    // pointers are supplied by the request-specific caller.
    let result = unsafe {
        libc::ptrace(
            request.cast_unsigned(),
            tid,
            address as *mut c_void,
            data as *mut c_void,
        )
    };
    if result == -1 {
        Err(io::Error::last_os_error())
    } else {
        Ok(result)
    }
}

#[cfg(test)]
mod tests {
    use std::{ffi::OsStr, fs, sync::atomic::AtomicBool, time::Duration};

    use super::{CaptureRequest, ChildIo, capture};
    use crate::trace::{Event, Operation, PathScope};

    #[test]
    fn records_successful_content_read_and_failed_open() {
        let root = tempfile::tempdir().expect("create root");
        let input = root.path().join("input");
        fs::write(&input, b"content").expect("write input");
        let cancellation = AtomicBool::new(false);
        let input_args = [input.into_os_string()];
        let captured = capture(
            CaptureRequest {
                root: root.path(),
                program: OsStr::new("/bin/cat"),
                arguments: &input_args,
                child_io: ChildIo::Report,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_secs(5),
                max_events: 100_000,
                max_bytes: 10 * 1024 * 1024,
                delay_rule: None,
                break_control: None,
                child_cwd: None,
                stderr_match: None,
                deny_rule: None,
            },
            &cancellation,
        )
        .expect("trace cat");
        assert!(captured.complete(), "{:?}", captured.failure);
        assert!(captured.root_status.expect("child status").success());
        let read_start = captured.events.iter().find_map(|event| match event {
            Event::OperationStart(start)
                if start.operation == Operation::Read
                    && start.paths.iter().any(|path| {
                        path.scope == PathScope::Project
                            && path.decode().expect("path encoding") == b"input"
                    }) =>
            {
                Some(start)
            }
            _ => None,
        });
        let read_start = read_start.expect("project content read");
        assert!(captured.events.iter().any(|event| match event {
            Event::OperationCompletion(done) => {
                done.operation_id == read_start.operation_id && done.bytes.is_some_and(|n| n > 0)
            }
            _ => false,
        }));

        let missing = root.path().join("missing");
        let missing_args = [missing.into_os_string()];
        let captured = capture(
            CaptureRequest {
                root: root.path(),
                program: OsStr::new("/bin/cat"),
                arguments: &missing_args,
                child_io: ChildIo::Report,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_secs(5),
                max_events: 100_000,
                max_bytes: 10 * 1024 * 1024,
                delay_rule: None,
                break_control: None,
                child_cwd: None,
                stderr_match: None,
                deny_rule: None,
            },
            &cancellation,
        )
        .expect("trace failed cat");
        assert!(captured.complete(), "{:?}", captured.failure);
        assert!(!captured.root_status.expect("child status").success());
        let open_id = captured.events.iter().find_map(|event| match event {
            Event::OperationStart(start)
                if start.operation == Operation::Open
                    && start.paths.iter().any(|path| {
                        path.scope == PathScope::Project
                            && path.decode().expect("path encoding") == b"missing"
                    }) =>
            {
                Some(start.operation_id)
            }
            _ => None,
        });
        let open_id = open_id.expect("failed project open");
        assert!(captured.events.iter().any(|event| match event {
            Event::OperationCompletion(done) => {
                done.operation_id == open_id && done.native_error == Some(i64::from(libc::ENOENT))
            }
            _ => false,
        }));
    }

    #[test]
    fn holds_matching_caller_before_read_and_records_observed_delay() {
        let root = tempfile::tempdir().expect("create root");
        let input = root.path().join("input");
        fs::write(&input, b"content").expect("write input");
        let arguments = [input.into_os_string()];
        let rule = |start: &crate::trace::Start| {
            if start.operation == Operation::Read
                && start.paths.iter().any(|path| {
                    path.scope == PathScope::Project
                        && path.decode().expect("path encoding") == b"input"
                })
            {
                Duration::from_millis(5)
            } else {
                Duration::ZERO
            }
        };
        let captured = capture(
            CaptureRequest {
                root: root.path(),
                program: OsStr::new("/bin/cat"),
                arguments: &arguments,
                child_io: ChildIo::Report,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_secs(5),
                max_events: 100_000,
                max_bytes: 10 * 1024 * 1024,
                delay_rule: Some(&rule),
                break_control: None,
                child_cwd: None,
                stderr_match: None,
                deny_rule: None,
            },
            &AtomicBool::new(false),
        )
        .expect("trace delayed cat");
        assert!(captured.complete(), "{:?}", captured.failure);
        assert!(captured.events.iter().any(|event| matches!(
            event,
            Event::OperationCompletion(done) if done.injected_delay_ns >= 5_000_000
        )));
    }

    #[test]
    fn matches_child_stderr_without_persisting_it_in_the_trace() {
        let root = tempfile::tempdir().unwrap();
        let arguments = [
            OsStr::new("-c").to_os_string(),
            OsStr::new("printf expected >&2; exit 7").to_os_string(),
        ];
        let captured = capture(
            CaptureRequest {
                root: root.path(),
                program: OsStr::new("/bin/sh"),
                arguments: &arguments,
                child_io: ChildIo::Report,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_secs(5),
                max_events: 100_000,
                max_bytes: 10 * 1024 * 1024,
                delay_rule: None,
                break_control: None,
                child_cwd: None,
                stderr_match: Some("expected"),
                deny_rule: None,
            },
            &AtomicBool::new(false),
        )
        .unwrap();
        assert!(captured.complete());
        assert_eq!(captured.root_status.unwrap().code(), Some(7));
        assert!(captured.stderr_matched);
        let encoded = serde_json::to_string(&captured.events).unwrap();
        assert!(!encoded.contains("expected"));
    }
}
