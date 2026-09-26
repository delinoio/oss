//! Supervise an injected Windows process tree within a private job object.

use std::{
    future::Future,
    io,
    os::windows::io::{AsHandle, AsRawHandle, FromRawHandle, OwnedHandle},
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

use super::{assemble_candidate_record, Admission, Frame, OperationReceiver};
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
    let deadline = limits
        .timeout
        .map(|duration| {
            Instant::now()
                .checked_add(duration)
                .ok_or(CaptureFailure::Timeout)
        })
        .transpose()?;
    let receiver = OperationReceiver::bind_with_admission(
        root,
        limits.max_events,
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
    let collected = receiver.finish().map_err(|error| {
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
    assemble_candidate_record(
        root,
        collected.pairs,
        termination.status.code().map(i64::from),
        limits.max_events,
        limits.max_bytes,
    )
    .map_err(|_| CaptureFailure::Record)
}
