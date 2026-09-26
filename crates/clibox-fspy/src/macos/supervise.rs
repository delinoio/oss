//! Supervise a newly launched, injected macOS process group.

use std::{
    future::Future,
    io,
    path::Path,
    sync::atomic::{AtomicBool, Ordering},
    thread,
    time::{Duration, Instant},
};

use futures_util::future::BoxFuture;
use tokio_util::sync::CancellationToken;

use super::{assemble_candidate_record, Frame, OperationReceiver};
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

fn group_exists(pid: u32) -> Result<bool, CaptureFailure> {
    let pid = i32::try_from(pid).map_err(|_| CaptureFailure::Cleanup)?;
    // SAFETY: a negative PID addresses only the new session's process group.
    let result = unsafe { libc::kill(-pid, 0) };
    if result == 0 {
        return Ok(true);
    }
    match io::Error::last_os_error().raw_os_error() {
        Some(libc::ESRCH) => Ok(false),
        Some(libc::EPERM) => Ok(true),
        _ => Err(CaptureFailure::Cleanup),
    }
}

fn signal_group(pid: u32, signal: i32) -> Result<(), CaptureFailure> {
    let pid = i32::try_from(pid).map_err(|_| CaptureFailure::Cleanup)?;
    // SAFETY: this group was created for the launched child before exec.
    let result = unsafe { libc::kill(-pid, signal) };
    if result == 0 || io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH) {
        Ok(())
    } else {
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

fn cleanup_group(
    pid: u32,
    wait: &mut BoxFuture<'static, io::Result<fspy::ChildTermination>>,
    runtime: &tokio::runtime::Runtime,
    kill_after: Duration,
    mut root_done: bool,
) -> Result<(), CaptureFailure> {
    signal_group(pid, libc::SIGTERM)?;
    let graceful_until = Instant::now()
        .checked_add(kill_after)
        .ok_or(CaptureFailure::Cleanup)?;
    while Instant::now() < graceful_until {
        if !root_done {
            root_done = poll_wait(runtime, wait).is_some();
        } else {
            thread::sleep(POLL);
        }
        if root_done && !group_exists(pid)? {
            return Ok(());
        }
    }
    signal_group(pid, libc::SIGKILL)?;
    let force_until = Instant::now() + FORCE_CONFIRM;
    while Instant::now() < force_until {
        if !root_done {
            root_done = poll_wait(runtime, wait).is_some();
        } else {
            thread::sleep(POLL);
        }
        if root_done && !group_exists(pid)? {
            return Ok(());
        }
    }
    Err(CaptureFailure::Cleanup)
}

/// Capture paired operations from a command that already has its environment,
/// cwd, and stdio configured. This does not assert the final macOS operation
/// coverage boundary; callers must keep it private until every hook is proven.
pub fn capture(
    command: fspy::Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
) -> Result<CompleteRecord, CaptureFailure> {
    capture_with_delay(command, root, limits, cancelled, |_| Duration::ZERO)
}

pub fn capture_with_delay<F>(
    mut command: fspy::Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
    delay_for: F,
) -> Result<CompleteRecord, CaptureFailure>
where
    F: Fn(&Frame) -> Duration + Send + Sync + 'static,
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
    let receiver =
        OperationReceiver::bind_with_delay(root, limits.max_events, limits.max_bytes, delay_for)
            .map_err(|_| CaptureFailure::Initialization)?;
    command.env("CLIBOX_FSPY_SOCKET", receiver.socket_path().as_os_str());
    // SAFETY: setsid is async-signal-safe and isolates only this fresh child
    // and its inherited descendants before the tracked image is executed.
    unsafe {
        command.pre_exec(|| {
            if libc::setsid() < 0 {
                Err(io::Error::last_os_error())
            } else {
                Ok(())
            }
        });
    }
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|_| CaptureFailure::Initialization)?;
    let token = CancellationToken::new();
    let child = match runtime.block_on(command.spawn(token.clone())) {
        Ok(child) => child,
        Err(_) => {
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
            break Err(CaptureFailure::TraceLoss);
        }
        if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
            break Err(CaptureFailure::Timeout);
        }
        if let Some(result) = poll_wait(&runtime, &mut wait) {
            break result.map_err(|_| CaptureFailure::TraceLoss);
        }
    };
    let termination = match result {
        Ok(termination) => termination,
        Err(failure) => {
            let cleanup = cleanup_group(pid, &mut wait, &runtime, limits.kill_after, false);
            token.cancel();
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(failure));
        }
    };
    match group_exists(pid) {
        Ok(true) => {
            let cleanup = cleanup_group(pid, &mut wait, &runtime, limits.kill_after, true);
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(CaptureFailure::DescendantSurvived));
        }
        Err(_) => {
            let _ = cleanup_group(pid, &mut wait, &runtime, limits.kill_after, true);
            let _ = receiver.finish();
            return Err(CaptureFailure::Cleanup);
        }
        Ok(false) => {}
    }
    let collected = receiver.finish().map_err(|_| CaptureFailure::TraceLoss)?;
    if !collected.hello_pids.contains(&pid)
        || collected
            .pairs
            .iter()
            .any(|(start, _)| !collected.hello_pids.contains(&start.pid))
    {
        return Err(CaptureFailure::TraceLoss);
    }
    if termination.path_accesses.is_err() {
        return Err(CaptureFailure::TraceLoss);
    }
    assemble_candidate_record(
        root,
        collected.pairs,
        termination.status,
        limits.max_events,
        limits.max_bytes,
    )
    .map_err(|error| {
        tracing::error!(
            stage = "macos_record",
            classification = "record_invalid",
            "candidate assembly failed"
        );
        let _ = error;
        CaptureFailure::Record
    })
}

#[cfg(test)]
mod tests {
    use std::{fs, process::Stdio, sync::atomic::AtomicBool};

    use super::*;

    #[test]
    fn captures_owned_descendant_and_valid_record() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_DESCENDANT", "1")
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
        assert_eq!(record.summary.child_exit_code, Some(0));
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation == crate::record::Operation::PositionalRead
                && pair.completion.byte_count == Some(7)
        }));
        assert!(record.operations.iter().any(|pair| {
            pair.start.parent_pid.is_some_and(|parent| {
                record
                    .operations
                    .iter()
                    .any(|other| other.start.pid == parent && other.start.pid != pair.start.pid)
            })
        }));
    }

    #[test]
    fn injects_delay_before_matching_file_reads() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_DESCENDANT", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let record = capture_with_delay(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: 64 * 1024 * 1024,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
            |frame| {
                if frame.operation == 3 && frame.path.ends_with(b"input.txt") {
                    Duration::from_millis(10)
                } else {
                    Duration::ZERO
                }
            },
        )
        .unwrap();
        let delayed = record
            .operations
            .iter()
            .filter(|pair| pair.start.requested_delay_ns == 10_000_000)
            .collect::<Vec<_>>();
        assert!(!delayed.is_empty());
        assert!(delayed.iter().all(|pair| {
            pair.start.operation == crate::record::Operation::Read
                && pair.completion.observed_delay_ns >= pair.start.requested_delay_ns
                && pair.completion.monotonic_ns - pair.start.monotonic_ns
                    >= pair.completion.observed_delay_ns
        }));
        assert!(record.operations.iter().all(|pair| {
            pair.start.requested_delay_ns != 0 || pair.completion.observed_delay_ns == 0
        }));
    }

    #[test]
    fn timeout_terminates_the_owned_group() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_SLEEP", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let began = Instant::now();
        let result = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: 64 * 1024 * 1024,
                timeout: Some(Duration::from_millis(100)),
                kill_after: Duration::from_millis(100),
            },
            &AtomicBool::new(false),
        );
        assert!(matches!(result, Err(CaptureFailure::Timeout)));
        assert!(began.elapsed() < Duration::from_secs(5));
    }

    #[test]
    fn detects_and_terminates_a_descendant_after_root_exit() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_ORPHAN", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let began = Instant::now();
        let result = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: 64 * 1024 * 1024,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(100),
            },
            &AtomicBool::new(false),
        );
        assert!(matches!(result, Err(CaptureFailure::DescendantSurvived)));
        assert!(began.elapsed() < Duration::from_secs(5));
    }

    #[test]
    fn cancellation_before_spawn_has_no_child_side_effect() {
        let directory = tempfile::tempdir().unwrap();
        let command = fspy::Command::new(directory.path().join("missing-executable"));
        let result = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100,
                max_bytes: 1024,
                timeout: None,
                kill_after: Duration::from_millis(100),
            },
            &AtomicBool::new(true),
        );
        assert!(matches!(result, Err(CaptureFailure::Cancellation)));
    }
}
