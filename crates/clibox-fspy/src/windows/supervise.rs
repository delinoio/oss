//! Supervise an injected Windows process tree within a private job object.

use std::{
    future::Future,
    io,
    os::windows::{
        ffi::OsStrExt,
        io::{AsHandle, AsRawHandle, FromRawHandle, OwnedHandle},
    },
    path::Path,
    sync::atomic::{AtomicBool, Ordering},
    thread,
    time::{Duration, Instant},
};

use futures_util::future::BoxFuture;
use tokio_util::sync::CancellationToken;
use winapi::{
    shared::minwindef::TRUE,
    um::{
        jobapi2::{
            CreateJobObjectW, QueryInformationJobObject, SetInformationJobObject,
            TerminateJobObject,
        },
        wincon::{GenerateConsoleCtrlEvent, CTRL_BREAK_EVENT},
        winnt::{
            JobObjectBasicAccountingInformation, JobObjectExtendedLimitInformation,
            JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
            JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
        },
    },
};

use super::{
    assemble_candidate_record, classify_path, Admission, Frame, FrameKind, OperationReceiver,
};
use crate::record::CompleteRecord;

const POLL: Duration = Duration::from_millis(20);
const FORCE_CONFIRM: Duration = Duration::from_secs(5);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CaptureFailure {
    Initialization,
    Spawn,
    TraceLoss,
    Timeout,
    Cancellation,
    Cleanup,
    DescendantSurvived,
    Record,
}

impl std::fmt::Display for CaptureFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Initialization => "trace_initialization",
            Self::Spawn => "spawn_failure",
            Self::TraceLoss => "trace_loss",
            Self::Timeout => "timeout",
            Self::Cancellation => "cancellation",
            Self::Cleanup => "cleanup_failure",
            Self::DescendantSurvived => "owned_descendant_survived",
            Self::Record => "record_invalid",
        })
    }
}

impl std::error::Error for CaptureFailure {}

#[derive(Debug, Clone, Copy)]
pub struct Limits {
    pub max_events: usize,
    pub max_bytes: u64,
    pub timeout: Option<Duration>,
    pub kill_after: Duration,
}

struct Job(OwnedHandle);

impl Job {
    fn new() -> Result<Self, CaptureFailure> {
        // SAFETY: null security attributes and name create an unnamed job.
        let raw = unsafe { CreateJobObjectW(std::ptr::null_mut(), std::ptr::null()) };
        if raw.is_null() {
            return Err(CaptureFailure::Initialization);
        }
        // SAFETY: CreateJobObjectW returned a fresh owned HANDLE.
        let handle = unsafe { OwnedHandle::from_raw_handle(raw.cast()) };
        // SAFETY: zero is a valid initial state for this Win32 structure.
        let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
        limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        // SAFETY: the job handle and exact structure buffer remain live.
        let configured = unsafe {
            SetInformationJobObject(
                handle.as_raw_handle().cast(),
                JobObjectExtendedLimitInformation,
                (&raw mut limits).cast(),
                std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
            )
        };
        if configured != TRUE {
            return Err(CaptureFailure::Initialization);
        }
        Ok(Self(handle))
    }

    fn active(&self) -> Result<u32, CaptureFailure> {
        // SAFETY: zero is a valid initial state for the output structure.
        let mut accounting: JOBOBJECT_BASIC_ACCOUNTING_INFORMATION = unsafe { std::mem::zeroed() };
        // SAFETY: the query receives a valid handle, exact class, and output.
        let success = unsafe {
            QueryInformationJobObject(
                self.0.as_raw_handle().cast(),
                JobObjectBasicAccountingInformation,
                (&raw mut accounting).cast(),
                std::mem::size_of::<JOBOBJECT_BASIC_ACCOUNTING_INFORMATION>() as u32,
                std::ptr::null_mut(),
            )
        };
        if success != TRUE {
            return Err(CaptureFailure::Cleanup);
        }
        Ok(accounting.ActiveProcesses)
    }

    fn terminate(&self) -> Result<(), CaptureFailure> {
        // SAFETY: the handle remains owned by this supervisor.
        if unsafe { TerminateJobObject(self.0.as_raw_handle().cast(), 130) } != TRUE {
            return Err(CaptureFailure::Cleanup);
        }
        let until = Instant::now() + FORCE_CONFIRM;
        while Instant::now() < until {
            if self.active()? == 0 {
                return Ok(());
            }
            thread::sleep(POLL);
        }
        Err(CaptureFailure::Cleanup)
    }
}

fn poll_wait<F: Future + Unpin>(
    runtime: &tokio::runtime::Runtime,
    wait: &mut F,
) -> Option<F::Output> {
    runtime
        .block_on(async { tokio::time::timeout(POLL, wait).await })
        .ok()
}

fn cleanup(
    job: &Job,
    pid: u32,
    wait: &mut BoxFuture<'static, io::Result<fspy::ChildTermination>>,
    runtime: &tokio::runtime::Runtime,
    kill_after: Duration,
    mut root_done: bool,
) -> Result<(), CaptureFailure> {
    // A detached or nonconsole child may reject CTRL_BREAK_EVENT. The bounded
    // graceful interval still lets cooperative descendants exit before force.
    // SAFETY: the root was launched with CREATE_NEW_PROCESS_GROUP.
    unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid) };
    let graceful_until = Instant::now()
        .checked_add(kill_after)
        .ok_or(CaptureFailure::Cleanup)?;
    while Instant::now() < graceful_until {
        if !root_done {
            root_done = poll_wait(runtime, wait).is_some();
        } else {
            thread::sleep(POLL);
        }
        if root_done && job.active()? == 0 {
            return Ok(());
        }
    }
    job.terminate()?;
    if !root_done {
        let until = Instant::now() + FORCE_CONFIRM;
        while Instant::now() < until {
            if poll_wait(runtime, wait).is_some() {
                return Ok(());
            }
        }
        return Err(CaptureFailure::Cleanup);
    }
    Ok(())
}

pub fn capture(
    command: fspy::Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
) -> Result<CompleteRecord, CaptureFailure> {
    capture_with_delay(command, root, limits, cancelled, |_| Duration::ZERO)
}

pub fn capture_with_delay<F>(
    command: fspy::Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
    delay_for: F,
) -> Result<CompleteRecord, CaptureFailure>
where
    F: Fn(&Frame) -> Duration + Send + Sync + 'static,
{
    capture_with_admission(command, root, limits, cancelled, move |frame| {
        Admission::Proceed(delay_for(frame))
    })
}

pub fn capture_with_admission<F>(
    mut command: fspy::Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
    admission: F,
) -> Result<CompleteRecord, CaptureFailure>
where
    F: Fn(&Frame) -> Admission + Send + Sync + 'static,
{
    if cancelled.load(Ordering::Acquire) {
        return Err(CaptureFailure::Cancellation);
    }
    if limits.max_events < 2 {
        return Err(CaptureFailure::Record);
    }
    let root = std::fs::canonicalize(root).map_err(|_| CaptureFailure::Initialization)?;
    if !root.is_dir() {
        return Err(CaptureFailure::Initialization);
    }
    let deadline = limits
        .timeout
        .map(|duration| {
            Instant::now()
                .checked_add(duration)
                .ok_or(CaptureFailure::Timeout)
        })
        .transpose()?;
    command
        .resolve_program()
        .map_err(|_| CaptureFailure::Spawn)?;
    let program =
        std::path::absolute(Path::new(command.program())).map_err(|_| CaptureFailure::Spawn)?;
    let path = program
        .as_os_str()
        .encode_wide()
        .flat_map(u16::to_le_bytes)
        .collect::<Vec<_>>();
    if path.is_empty() || path.len() > super::MAX_PATH_BYTES {
        return Err(CaptureFailure::Record);
    }
    let began = Instant::now();
    let mut root_start = Frame {
        kind: FrameKind::Start,
        operation: 10,
        pid: 0,
        parent_pid: std::process::id(),
        tid: 0,
        id: 1,
        monotonic_ns: 1,
        result: 0,
        error: 0,
        access_path: classify_path(&root, &path).map_err(|_| CaptureFailure::Record)?,
        path,
        second_path: None,
        sequence: 1,
        second_access_path: None,
        requested_delay_ns: 0,
        observed_delay_ns: 0,
    };
    let delay = match admission(&root_start) {
        Admission::Proceed(delay) => delay,
        Admission::Quit => return Err(CaptureFailure::Cancellation),
    };
    root_start.requested_delay_ns =
        u64::try_from(delay.as_nanos()).map_err(|_| CaptureFailure::Record)?;
    if !delay.is_zero() {
        let observed =
            crate::delay::wait(delay, cancelled, deadline).map_err(|failure| match failure {
                crate::delay::DelayFailure::Cancelled => CaptureFailure::Cancellation,
                crate::delay::DelayFailure::Timeout => CaptureFailure::Timeout,
            })?;
        root_start.observed_delay_ns =
            u64::try_from(observed.as_nanos()).map_err(|_| CaptureFailure::Record)?;
    }
    if cancelled.load(Ordering::Acquire) {
        return Err(CaptureFailure::Cancellation);
    }
    if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
        return Err(CaptureFailure::Timeout);
    }
    let receiver = OperationReceiver::bind_with_admission(
        &root,
        limits.max_events.saturating_sub(2).max(1),
        limits.max_bytes,
        admission,
    )
    .map_err(|_| CaptureFailure::Initialization)?;
    let job = Job::new()?;
    command
        .windows_job_handle(job.0.as_handle())
        .map_err(|_| CaptureFailure::Initialization)?;
    command
        .env("CLIBOX_FSPY_ENDPOINT", receiver.address().to_string())
        .env("CLIBOX_FSPY_TOKEN", receiver.token());
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|_| CaptureFailure::Initialization)?;
    let token = CancellationToken::new();
    let child = match runtime.block_on(command.spawn(token.clone())) {
        Ok(child) => child,
        Err(error) => {
            let (kind, os_code) = match &error {
                fspy::error::SpawnError::SpyInitialization(cause) => {
                    ("spy_initialization", cause.raw_os_error())
                }
                fspy::error::SpawnError::Which { .. } => ("program_resolution", None),
                fspy::error::SpawnError::Supervisor(cause) => ("supervisor", cause.raw_os_error()),
                fspy::error::SpawnError::ChannelCreation(cause) => {
                    ("channel", cause.raw_os_error())
                }
                fspy::error::SpawnError::Injection(cause) => ("injection", cause.raw_os_error()),
                fspy::error::SpawnError::OsSpawn(cause) => ("os_spawn", cause.raw_os_error()),
            };
            tracing::error!(stage = "spawn", kind, os_code, "file trace failed");
            eprintln!("clibox fspy supervisor: stage=spawn kind={kind} os_code={os_code:?}");
            let _ = receiver.finish();
            return Err(CaptureFailure::Spawn);
        }
    };
    let pid = child.root_pid;
    let mut wait = child.wait_handle;
    root_start.pid = pid;
    root_start.tid = u64::from(pid);
    let mut root_completion = root_start.clone();
    root_completion.kind = FrameKind::Completion;
    root_completion.path.clear();
    root_completion.access_path = None;
    root_completion.sequence = 2;
    root_completion.monotonic_ns = u64::try_from(began.elapsed().as_nanos())
        .unwrap_or(u64::MAX - 1)
        .saturating_add(1);
    let result = loop {
        if cancelled.load(Ordering::Acquire) {
            break Err(CaptureFailure::Cancellation);
        }
        if receiver.failed() {
            eprintln!("clibox fspy supervisor: stage=receiver_failed");
            break Err(CaptureFailure::TraceLoss);
        }
        if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
            break Err(CaptureFailure::Timeout);
        }
        if let Some(result) = poll_wait(&runtime, &mut wait) {
            break result.map_err(|error| {
                eprintln!(
                    "clibox fspy supervisor: stage=child_wait kind={:?}",
                    error.kind()
                );
                CaptureFailure::TraceLoss
            });
        }
    };
    let termination = match result {
        Ok(termination) => termination,
        Err(failure) => {
            let cleanup = cleanup(&job, pid, &mut wait, &runtime, limits.kill_after, false);
            token.cancel();
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(failure));
        }
    };
    tracing::debug!(
        stage = "child_termination",
        exit_code = ?termination.status.code(),
        legacy_complete = termination.path_accesses.is_ok(),
        "file trace child exited"
    );
    if !termination.status.success() {
        eprintln!(
            "clibox fspy supervisor: stage=child_termination exit_code={:?}",
            termination.status.code()
        );
    }
    match job.active() {
        Ok(0) => {}
        Ok(_) => {
            let cleanup = cleanup(&job, pid, &mut wait, &runtime, limits.kill_after, true);
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(CaptureFailure::DescendantSurvived));
        }
        Err(_) => {
            let _ = job.terminate();
            let _ = receiver.finish();
            return Err(CaptureFailure::Cleanup);
        }
    }
    let mut collected = receiver.finish().map_err(|error| {
        eprintln!(
            "clibox fspy supervisor: stage=receiver_finish kind={:?} reason={error}",
            error.kind()
        );
        CaptureFailure::TraceLoss
    })?;
    if !collected.hello_pids.contains(&pid)
        || collected
            .pairs
            .iter()
            .any(|(start, _)| !collected.hello_pids.contains(&start.pid))
        || termination.path_accesses.is_err()
    {
        eprintln!(
            "clibox fspy supervisor: stage=completeness root_hello={} hello_count={} pairs={} \
             legacy_complete={}",
            collected.hello_pids.contains(&pid),
            collected.hello_pids.len(),
            collected.pairs.len(),
            termination.path_accesses.is_ok(),
        );
        return Err(CaptureFailure::TraceLoss);
    }
    for (start, completion) in &mut collected.pairs {
        start.sequence = start
            .sequence
            .checked_add(2)
            .ok_or(CaptureFailure::Record)?;
        completion.sequence = completion
            .sequence
            .checked_add(2)
            .ok_or(CaptureFailure::Record)?;
    }
    collected.pairs.insert(0, (root_start, root_completion));
    assemble_candidate_record(
        &root,
        collected.pairs,
        termination.status.code().map(i64::from),
        limits.max_events,
        limits.max_bytes,
    )
    .map_err(|error| {
        eprintln!(
            "clibox fspy supervisor: stage=assemble_record kind={:?} reason={error}",
            error.kind()
        );
        CaptureFailure::Record
    })
}

#[cfg(test)]
mod tests {
    use std::{fs, process::Stdio, sync::Arc};

    use super::*;
    use crate::record::{NativePath, Operation, PathClass};

    #[test]
    fn root_executable_is_admitted_before_launch() {
        let directory = tempfile::tempdir().unwrap();
        let seen = Arc::new(AtomicBool::new(false));
        let observed = Arc::clone(&seen);
        let result = capture_with_admission(
            fspy::Command::new(std::env::current_exe().unwrap()),
            directory.path(),
            Limits {
                max_events: 100,
                max_bytes: 1024 * 1024,
                timeout: Some(Duration::from_secs(5)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
            move |frame| {
                assert_eq!(frame.operation, 10);
                assert_eq!(frame.pid, 0);
                observed.store(true, Ordering::Release);
                Admission::Quit
            },
        );
        assert!(matches!(result, Err(CaptureFailure::Cancellation)));
        assert!(seen.load(Ordering::Acquire));
    }

    #[test]
    fn records_project_local_root_executable_before_injected_events() {
        let directory = tempfile::tempdir().unwrap();
        let executable = directory.path().join("tool.exe");
        fs::copy(std::env::current_exe().unwrap(), &executable).unwrap();
        let mut command = fspy::Command::new(&executable);
        command
            .args([
                "--exact",
                "windows::supervise::tests::root_executable_fixture",
            ])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_ROOT_EXE_FIXTURE", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let record = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: 64 * 1024 * 1024,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        )
        .unwrap();
        let root = &record.operations[0];
        assert_eq!(root.start.sequence, 1);
        assert_eq!(root.completion.sequence, 2);
        assert_eq!(root.start.operation, Operation::Exec);
        assert_eq!(root.start.paths[0].class, PathClass::Project);
        assert_eq!(
            root.start.paths[0].project_relative,
            Some(NativePath::WindowsUtf16(
                "tool.exe".encode_utf16().collect()
            ))
        );
        assert!(root.completion.native_error.is_none());
    }

    #[test]
    fn root_executable_fixture() {
        if std::env::var_os("CLIBOX_FSPY_ROOT_EXE_FIXTURE").is_none() {
            return;
        }
    }
}
