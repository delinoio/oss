//! Supervise a newly launched, injected macOS process group.

use std::{
    collections::HashSet,
    future::Future,
    io,
    os::unix::ffi::OsStrExt,
    path::Path,
    sync::atomic::{AtomicBool, Ordering},
    thread,
    time::{Duration, Instant},
};

use futures_util::future::BoxFuture;
use tokio_util::sync::CancellationToken;

use super::{
    assemble_candidate_record, classify_path, Admission, Frame, FrameKind, OperationReceiver,
};
use crate::record::{self, CompleteRecord};

const POLL: Duration = Duration::from_millis(20);
const FORCE_CONFIRM: Duration = Duration::from_secs(5);

fn monotonic_ns() -> Result<u64, CaptureFailure> {
    let mut time = libc::timespec {
        tv_sec: 0,
        tv_nsec: 0,
    };
    // SAFETY: the stack value is writable for the duration of this clock call.
    if unsafe { libc::clock_gettime(libc::CLOCK_MONOTONIC, &raw mut time) } != 0 {
        return Err(CaptureFailure::Initialization);
    }
    u64::try_from(time.tv_sec)
        .ok()
        .and_then(|seconds| seconds.checked_mul(1_000_000_000))
        .and_then(|base| u64::try_from(time.tv_nsec).ok()?.checked_add(base))
        .ok_or(CaptureFailure::Initialization)
}

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
    let os_code = (result != 0)
        .then(|| io::Error::last_os_error().raw_os_error())
        .flatten();
    if result == 0 || os_code == Some(libc::ESRCH) {
        Ok(())
    } else {
        tracing::debug!(
            stage = "cleanup_signal",
            signal,
            ?os_code,
            "owned group signal failed"
        );
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

fn signal_tracked(
    receiver: &OperationReceiver,
    signal: i32,
    signalled: &mut HashSet<u32>,
) -> Result<(), CaptureFailure> {
    for tracked_pid in receiver
        .live_processes()
        .map_err(|_| CaptureFailure::Cleanup)?
    {
        if signalled.contains(&tracked_pid) {
            continue;
        }
        let pid = i32::try_from(tracked_pid).map_err(|_| CaptureFailure::Cleanup)?;
        // SAFETY: the receiver recorded this process's start identity at its
        // authenticated hello and rechecked it immediately before signaling.
        if unsafe { libc::kill(pid, signal) } != 0
            && io::Error::last_os_error().raw_os_error() != Some(libc::ESRCH)
        {
            return Err(CaptureFailure::Cleanup);
        }
        signalled.insert(tracked_pid);
    }
    Ok(())
}

fn cleanup_owned(
    pid: u32,
    receiver: &OperationReceiver,
    wait: &mut BoxFuture<'static, io::Result<fspy::ChildTermination>>,
    runtime: &tokio::runtime::Runtime,
    kill_after: Duration,
    mut root_done: bool,
) -> Result<(), CaptureFailure> {
    // The first group signal can fail while its final callers are exiting.
    // Do not mistake that request failure for an unconfirmed cleanup: the
    // identity-checked process signals and both absence checks below still
    // run within the same grace/force budgets. Persistent denial fails at the
    // final confirmation boundary; no extra process-group signal is retried.
    let _ = signal_group(pid, libc::SIGTERM);
    let mut signalled = HashSet::new();
    let graceful_until = Instant::now()
        .checked_add(kill_after)
        .ok_or(CaptureFailure::Cleanup)?;
    while Instant::now() < graceful_until {
        // A process can exit between enumeration and proc_pidinfo/kill. Keep
        // trying within the owned cleanup budget instead of treating one
        // transient inspection failure as confirmed cleanup failure.
        if signal_tracked(receiver, libc::SIGTERM, &mut signalled).is_err() {
            tracing::debug!(stage = "cleanup_grace", "retrying process inspection");
        }
        if !root_done {
            root_done = poll_wait(runtime, wait).is_some();
        } else {
            thread::sleep(POLL);
        }
        if root_done
            && matches!(group_exists(pid), Ok(false))
            && receiver
                .live_processes()
                .is_ok_and(|processes| processes.is_empty())
        {
            return Ok(());
        }
    }
    let _ = signal_group(pid, libc::SIGKILL);
    let mut signalled = HashSet::new();
    let force_until = Instant::now() + FORCE_CONFIRM;
    while Instant::now() < force_until {
        if signal_tracked(receiver, libc::SIGKILL, &mut signalled).is_err() {
            tracing::debug!(stage = "cleanup_force", "retrying process inspection");
        }
        if !root_done {
            root_done = poll_wait(runtime, wait).is_some();
        } else {
            thread::sleep(POLL);
        }
        if root_done
            && matches!(group_exists(pid), Ok(false))
            && receiver
                .live_processes()
                .is_ok_and(|processes| processes.is_empty())
        {
            return Ok(());
        }
    }
    let group_live = group_exists(pid).ok();
    let tracked_count = receiver
        .live_processes()
        .ok()
        .map(|processes| processes.len());
    tracing::error!(
        stage = "cleanup_unconfirmed",
        root_done,
        ?group_live,
        ?tracked_count,
        "owned process cleanup could not be confirmed"
    );
    eprintln!(
        "clibox fspy supervisor: stage=cleanup_unconfirmed root_done={root_done} \
         group_live={group_live:?} tracked_count={tracked_count:?}"
    );
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
    command: fspy::Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
    delay_for: F,
) -> Result<CompleteRecord, CaptureFailure>
where
    F: Fn(&Frame) -> Duration + Send + Sync + 'static,
{
    capture_with_admission(command, root, limits, cancelled, move |frame, _| {
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
    F: Fn(&Frame, &AtomicBool) -> Admission + Send + Sync + 'static,
{
    if cancelled.load(Ordering::Acquire) {
        return Err(CaptureFailure::Cancellation);
    }
    if limits.max_events < 2 {
        return Err(CaptureFailure::Record);
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
    let failed_lookups = command.failed_program_lookups().to_vec();
    let reserved_events = failed_lookups
        .len()
        .checked_add(1)
        .and_then(|pairs| pairs.checked_mul(2))
        .ok_or(CaptureFailure::Record)?;
    if reserved_events > limits.max_events {
        return Err(CaptureFailure::Record);
    }
    let program =
        std::path::absolute(Path::new(command.program())).map_err(|_| CaptureFailure::Spawn)?;
    let path = program.as_os_str().as_bytes().to_vec();
    let mut root_start = Frame {
        sequence: 1,
        kind: FrameKind::Start,
        operation: 10,
        pid: 0,
        parent_pid: std::process::id(),
        tid: 0,
        id: 1,
        monotonic_ns: monotonic_ns()?,
        result: 0,
        error: 0,
        access_path: classify_path(root, &path).map_err(|_| CaptureFailure::Record)?,
        identity: None,
        path,
        requested_delay_ns: 0,
        observed_delay_ns: 0,
        image_id: None,
    };
    // Reserve a conservative NDJSON-sized charge for the header, summary,
    // failed PATH probes, and root pair before any native call is admitted.
    let mut reserved_bytes =
        record::retained_frame_charge(root.as_os_str().as_bytes().len(), std::iter::empty())
            .and_then(|charge| charge.checked_add(512))
            .ok_or(CaptureFailure::Record)?;
    for failure in &failed_lookups {
        let path = failure.path.as_os_str().as_bytes();
        let classified = classify_path(root, path).map_err(|_| CaptureFailure::Record)?;
        let charge = record::retained_frame_charge(path.len(), classified.iter())
            .and_then(|charge| charge.checked_add(512))
            .ok_or(CaptureFailure::Record)?;
        reserved_bytes = reserved_bytes
            .checked_add(charge)
            .ok_or(CaptureFailure::Record)?;
    }
    let root_charge =
        record::retained_frame_charge(root_start.path.len(), root_start.access_path.iter())
            .and_then(|charge| charge.checked_add(512))
            .ok_or(CaptureFailure::Record)?;
    let native_bytes = limits
        .max_bytes
        .checked_sub(
            reserved_bytes
                .checked_add(root_charge)
                .ok_or(CaptureFailure::Record)?,
        )
        .filter(|remaining| *remaining > 0)
        .ok_or(CaptureFailure::Record)?;
    let delay = match admission(&root_start, cancelled) {
        Admission::Proceed(delay) => delay,
        Admission::Quit => {
            return Err(
                if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                    CaptureFailure::Timeout
                } else {
                    CaptureFailure::Cancellation
                },
            );
        }
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
        root,
        limits.max_events - reserved_events,
        native_bytes,
        admission,
    )
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
    root_completion.sequence = 2;
    root_completion.kind = FrameKind::Completion;
    root_completion.path.clear();
    root_completion.access_path = None;
    root_completion.monotonic_ns = match monotonic_ns() {
        Ok(time) => time,
        Err(failure) => {
            let cleanup = cleanup_owned(
                pid,
                &receiver,
                &mut wait,
                &runtime,
                limits.kill_after,
                false,
            );
            token.cancel();
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(failure));
        }
    };
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
            let cleanup = cleanup_owned(
                pid,
                &receiver,
                &mut wait,
                &runtime,
                limits.kill_after,
                false,
            );
            token.cancel();
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(failure));
        }
    };
    // An admission quit can finish the root while its wait is being polled.
    // Cancellation still owns the remaining processes and queued callers;
    // confirm cleanup before inspecting a normal-completion trace. The wait
    // future has completed, so cleanup must not poll it a second time.
    if cancelled.load(Ordering::Acquire) {
        let cleanup = cleanup_owned(pid, &receiver, &mut wait, &runtime, limits.kill_after, true);
        token.cancel();
        let _ = receiver.finish();
        return Err(cleanup.err().unwrap_or(CaptureFailure::Cancellation));
    }
    let group_live = group_exists(pid);
    let tracked_live = receiver.live_processes();
    match (group_live, tracked_live) {
        (Ok(true), _) => {
            let cleanup =
                cleanup_owned(pid, &receiver, &mut wait, &runtime, limits.kill_after, true);
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(CaptureFailure::DescendantSurvived));
        }
        (Ok(false), Ok(ref processes)) if !processes.is_empty() => {
            let cleanup =
                cleanup_owned(pid, &receiver, &mut wait, &runtime, limits.kill_after, true);
            let _ = receiver.finish();
            return Err(cleanup.err().unwrap_or(CaptureFailure::DescendantSurvived));
        }
        (Err(_), _) | (_, Err(_)) => {
            let _ = cleanup_owned(pid, &receiver, &mut wait, &runtime, limits.kill_after, true);
            let _ = receiver.finish();
            return Err(CaptureFailure::Cleanup);
        }
        (Ok(false), Ok(_)) => {}
    }
    let mut collected = receiver.finish().map_err(|_| CaptureFailure::TraceLoss)?;
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
    let reserved_sequence = u64::try_from(reserved_events).map_err(|_| CaptureFailure::Record)?;
    for (start, completion) in &mut collected.pairs {
        start.sequence = start
            .sequence
            .checked_add(reserved_sequence)
            .ok_or(CaptureFailure::Record)?;
        completion.sequence = completion
            .sequence
            .checked_add(reserved_sequence)
            .ok_or(CaptureFailure::Record)?;
    }
    let mut prefix = Vec::with_capacity(failed_lookups.len() + 1);
    for (index, failure) in failed_lookups.iter().enumerate() {
        let path = failure.path.as_os_str().as_bytes().to_vec();
        let sequence = u64::try_from(index)
            .ok()
            .and_then(|index| index.checked_mul(2))
            .and_then(|index| index.checked_add(1))
            .ok_or(CaptureFailure::Record)?;
        let start = Frame {
            sequence,
            kind: FrameKind::Start,
            operation: 10,
            pid,
            parent_pid: root_start.parent_pid,
            tid: u64::from(pid),
            id: sequence,
            monotonic_ns: root_start.monotonic_ns.saturating_sub(1).max(1),
            result: 0,
            error: 0,
            access_path: classify_path(root, &path).map_err(|_| CaptureFailure::Record)?,
            identity: None,
            path,
            requested_delay_ns: 0,
            observed_delay_ns: 0,
            image_id: None,
        };
        let mut completion = start.clone();
        completion.kind = FrameKind::Completion;
        completion.sequence = sequence + 1;
        completion.result = -1;
        completion.error = failure.os_error;
        completion.path.clear();
        completion.access_path = None;
        prefix.push((start, completion));
    }
    root_start.sequence = reserved_sequence - 1;
    root_completion.sequence = reserved_sequence;
    prefix.push((root_start, root_completion));
    prefix.extend(collected.pairs);
    assemble_candidate_record(
        root,
        prefix,
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

    #[test]
    fn failed_path_candidate_is_recorded_before_root_exec() {
        use std::os::unix::fs::symlink;

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let first = root.join("first");
        let second = root.join("second");
        fs::create_dir(&first).unwrap();
        fs::create_dir(&second).unwrap();
        symlink(std::env::current_exe().unwrap(), second.join("probe-tool")).unwrap();
        let mut command = fspy::Command::new("probe-tool");
        command
            .args([
                "--exact",
                "macos::supervise::tests::short_lived_process_hello_is_recorded_before_exit",
            ])
            .envs(std::env::vars_os())
            .env("PATH", std::env::join_paths([&first, &second]).unwrap())
            .env("CLIBOX_FSPY_SHORT_CHILD", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let record = capture(
            command,
            &root,
            Limits {
                max_events: 100_000,
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        )
        .unwrap();
        assert_eq!(record.summary.child_exit_code, Some(0));
        assert_eq!(
            record.operations[0].start.operation,
            crate::record::Operation::Exec
        );
        assert_eq!(
            record.operations[0].completion.native_error,
            Some(libc::ENOENT)
        );
        assert_eq!(
            record.operations[0].start.paths[0].project_relative,
            Some(crate::record::NativePath::UnixBytes(
                b"first/probe-tool".to_vec()
            ))
        );
        assert_eq!(
            record.operations[1].start.operation,
            crate::record::Operation::Exec
        );
        assert_eq!(record.operations[1].completion.native_error, None);
    }

    #[test]
    fn short_lived_process_hello_is_recorded_before_exit() {
        if std::env::var_os("CLIBOX_FSPY_SHORT_CHILD").is_some() {
            return;
        }
        let directory = tempfile::tempdir().unwrap();
        for _ in 0..12 {
            let mut command = fspy::Command::new(std::env::current_exe().unwrap());
            command
                .args([
                    "--exact",
                    "macos::supervise::tests::short_lived_process_hello_is_recorded_before_exit",
                ])
                .envs(std::env::vars_os())
                .env("CLIBOX_FSPY_SHORT_CHILD", "1")
                .stdout(Stdio::null())
                .stderr(Stdio::null());
            let record = capture(
                command,
                directory.path(),
                Limits {
                    max_events: 100_000,
                    max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                    timeout: Some(Duration::from_secs(5)),
                    kill_after: Duration::from_millis(500),
                },
                &AtomicBool::new(false),
            )
            .unwrap();
            assert_eq!(record.summary.child_exit_code, Some(0));
        }
    }

    #[test]
    fn root_pair_exceeding_event_limit_is_rejected_before_launch() {
        let directory = tempfile::tempdir().unwrap();
        let output = directory.path().join("created.txt");
        let mut command = fspy::Command::new("/usr/bin/touch");
        command.arg(&output);
        let result = capture(
            command,
            directory.path(),
            Limits {
                max_events: 1,
                max_bytes: 1024 * 1024,
                timeout: Some(Duration::from_secs(5)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        );
        assert!(matches!(result, Err(CaptureFailure::Record)));
        assert!(!output.exists());
    }

    #[test]
    fn synthetic_byte_budget_is_rejected_before_launch() {
        let directory = tempfile::tempdir().unwrap();
        let output = directory.path().join("created.txt");
        let mut command = fspy::Command::new("/usr/bin/touch");
        command.arg(&output);
        let result = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100,
                max_bytes: 1,
                timeout: Some(Duration::from_secs(5)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        );
        assert!(matches!(result, Err(CaptureFailure::Record)));
        assert!(!output.exists());
    }

    #[test]
    fn captures_descriptor_only_truncation_as_mutation() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_FTRUNCATE", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let record = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        )
        .unwrap();
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation == crate::record::Operation::Mutation
                && pair.start.paths.iter().any(|path| {
                    path.project_relative
                        == Some(crate::record::NativePath::UnixBytes(b"input.txt".to_vec()))
                })
                && pair.completion.native_error.is_none()
        }));
        assert_eq!(fs::read(input).unwrap(), b"fix");
    }

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
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        )
        .unwrap();
        assert_eq!(record.summary.child_exit_code, Some(0));
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation == crate::record::Operation::Exec
                && pair.start.parent_pid == Some(std::process::id())
                && pair.completion.native_error.is_none()
        }));
        assert!(
            record
                .operations
                .iter()
                .filter(
                    |pair| pair.start.operation == crate::record::Operation::Exec
                        && pair.completion.native_error.is_none()
                )
                .count()
                >= 2
        );
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
    fn root_exec_is_admitted_before_launch() {
        use std::sync::{atomic::Ordering, Arc};

        let directory = tempfile::tempdir().unwrap();
        let admitted = Arc::new(AtomicBool::new(false));
        let observed = Arc::clone(&admitted);
        let command = fspy::Command::new(std::env::current_exe().unwrap());
        let result = capture_with_admission(
            command,
            directory.path(),
            Limits {
                max_events: 100,
                max_bytes: 1024 * 1024,
                timeout: Some(Duration::from_secs(5)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
            move |frame, _| {
                if frame.operation == 10 && frame.pid == 0 {
                    observed.store(true, Ordering::SeqCst);
                    Admission::Quit
                } else {
                    Admission::Proceed(Duration::ZERO)
                }
            },
        );
        assert!(matches!(result, Err(CaptureFailure::Cancellation)));
        assert!(admitted.load(Ordering::SeqCst));
    }

    #[test]
    fn root_exec_delay_obeys_execution_timeout() {
        let directory = tempfile::tempdir().unwrap();
        let command = fspy::Command::new(std::env::current_exe().unwrap());
        let began = Instant::now();
        let result = capture_with_delay(
            command,
            directory.path(),
            Limits {
                max_events: 100,
                max_bytes: 1024 * 1024,
                timeout: Some(Duration::from_millis(100)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
            |frame| {
                if frame.operation == 10 && frame.pid == 0 {
                    Duration::from_secs(60)
                } else {
                    Duration::ZERO
                }
            },
        );
        assert!(matches!(result, Err(CaptureFailure::Timeout)));
        assert!(began.elapsed() < Duration::from_secs(2));
    }

    #[test]
    fn successful_exec_replacement_pairs_with_successor_hello() {
        let directory = tempfile::tempdir().unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::exec_replacement_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_REPLACE", "first")
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let record = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(500),
            },
            &AtomicBool::new(false),
        )
        .unwrap();
        assert_eq!(record.summary.child_exit_code, Some(0));
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation == crate::record::Operation::Exec
                && pair.start.pid == pair.completion.pid
                && pair.completion.native_error.is_none()
                && pair.start.sequence > 2
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
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
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
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                // Leave time for the injected hello on loaded CI hosts so this
                // fixture exercises a running group's timeout and cleanup.
                // Shorten this only if the fixture gains an explicit ready gate.
                timeout: Some(Duration::from_secs(2)),
                kill_after: Duration::from_millis(100),
            },
            &AtomicBool::new(false),
        );
        assert_eq!(result.err(), Some(CaptureFailure::Timeout));
        assert!(began.elapsed() < Duration::from_secs(10));
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
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(100),
            },
            &AtomicBool::new(false),
        );
        assert!(matches!(result, Err(CaptureFailure::DescendantSurvived)));
        // This measures injection startup as well as the owned-group cleanup.
        // The ten-second execution deadline plus forced cleanup confirmation
        // can legitimately exceed five seconds under a loaded test runner.
        assert!(began.elapsed() < Duration::from_secs(16));
    }

    #[test]
    fn detects_and_terminates_a_detached_descendant() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        let marker = directory.path().join("detached.marker");
        fs::write(&input, b"fixture").unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_ORPHAN", "1")
            .env("CLIBOX_FSPY_TEST_DETACH_MARKER", marker.as_os_str())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let result = capture(
            command,
            directory.path(),
            Limits {
                max_events: 100_000,
                max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                timeout: Some(Duration::from_secs(10)),
                kill_after: Duration::from_millis(100),
            },
            &AtomicBool::new(false),
        );
        assert!(marker.exists());
        assert!(matches!(result, Err(CaptureFailure::DescendantSurvived)));
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

    #[test]
    fn admission_cancellation_retains_cancellation_when_root_exits() {
        use std::sync::Arc;

        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        for _ in 0..4 {
            let cancelled = Arc::new(AtomicBool::new(false));
            let stop = Arc::clone(&cancelled);
            let mut command = fspy::Command::new(std::env::current_exe().unwrap());
            command
                .args(["--exact", "macos::tests::read_fixture_child"])
                .envs(std::env::vars_os())
                .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
                .stdout(Stdio::null())
                .stderr(Stdio::null());
            let result = capture_with_admission(
                command,
                directory.path(),
                Limits {
                    max_events: 100_000,
                    max_bytes: crate::record::DEFAULT_BYTE_LIMIT,
                    timeout: Some(Duration::from_secs(10)),
                    kill_after: Duration::from_millis(100),
                },
                &cancelled,
                move |frame, _| {
                    if frame.operation == 3 && frame.path.ends_with(b"input.txt") {
                        stop.store(true, Ordering::SeqCst);
                        // The native quit acknowledgment exits the root; its
                        // wait completion can race the supervisor's next poll.
                        Admission::Quit
                    } else {
                        Admission::Proceed(Duration::ZERO)
                    }
                },
            );
            assert!(cancelled.load(Ordering::SeqCst));
            assert_eq!(result.err(), Some(CaptureFailure::Cancellation));
        }
    }
}
