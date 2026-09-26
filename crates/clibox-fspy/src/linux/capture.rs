//! Convert paired ptrace stops into a complete version-one operation record.

use std::{
    collections::HashMap,
    env,
    ffi::{CString, OsString},
    fs,
    os::unix::{ffi::OsStrExt, fs::PermissionsExt},
    path::{Path, PathBuf},
    process::Command,
    sync::atomic::{AtomicBool, Ordering},
    thread,
    time::{Duration, Instant},
};

use super::{
    paths, supervision, trace_controlled_locked, ChildOutcome, ControlDirective, EntryAction,
    Limits, RawEntry, TraceClock, TraceFailure, TRACE_LOCK,
};
use crate::record::{
    Backend, CompleteRecord, Completion, CoverageBoundary, Header, NativePath, Operation,
    OperationPair, Platform, Start, Summary, SCHEMA_VERSION,
};

#[derive(Debug, Clone)]
struct CapturedEntry {
    operation: paths::DecodedOperation,
    requested_delay_ns: u64,
    observed_delay_ns: u64,
}

pub enum CaptureAction {
    Proceed(Duration),
    Hold,
}

#[derive(Debug, Clone)]
enum Event {
    Start(Start),
    Completion(Completion),
}

impl Event {
    fn monotonic_ns(&self) -> u64 {
        match self {
            Self::Start(value) => value.monotonic_ns,
            Self::Completion(value) => value.monotonic_ns,
        }
    }

    fn correlation_id(&self) -> u64 {
        match self {
            Self::Start(value) => value.correlation_id,
            Self::Completion(value) => value.correlation_id,
        }
    }
}

fn root_program(command: &Command) -> Result<PathBuf, TraceFailure> {
    let cwd = command.get_current_dir().unwrap_or_else(|| Path::new("."));
    let cwd = std::path::absolute(cwd).map_err(|_| TraceFailure::Spawn)?;
    let program = Path::new(command.get_program());
    let executable = |path: &Path| {
        fs::metadata(path)
            .is_ok_and(|metadata| metadata.is_file() && metadata.permissions().mode() & 0o111 != 0)
    };
    if program.as_os_str().as_bytes().contains(&b'/') {
        let path = if program.is_absolute() {
            program.to_path_buf()
        } else {
            cwd.join(program)
        };
        return executable(&path).then_some(path).ok_or(TraceFailure::Spawn);
    }
    let path_env = command
        .get_envs()
        .find(|(key, _)| *key == "PATH")
        .map(|(_, value)| value.map(OsString::from))
        .unwrap_or_else(|| env::var_os("PATH"))
        .unwrap_or_else(|| OsString::from("/bin:/usr/bin"));
    for directory in env::split_paths(&path_env) {
        let base = if directory.is_absolute() {
            directory
        } else {
            cwd.join(directory)
        };
        let candidate = base.join(program);
        if executable(&candidate) {
            return Ok(candidate);
        }
    }
    Err(TraceFailure::Spawn)
}

/// Record all selected synchronous file operations. The optional policy runs
/// while the calling tracee thread is stopped at syscall entry. It may select
/// a bounded per-operation delay; every baseline run still uses the same
/// tracing path with zero injected delay.
pub fn capture<F>(
    command: &mut Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
    mut delay_for: F,
) -> Result<CompleteRecord, TraceFailure>
where
    F: FnMut(&paths::DecodedOperation) -> Duration,
{
    capture_controlled(
        command,
        root,
        limits,
        cancelled,
        |operation| CaptureAction::Proceed(delay_for(operation)),
        |_| Ok(ControlDirective::Wait),
    )
}

/// Capture while allowing a selected calling thread to remain stopped at
/// operation entry until the controller releases it. Other tracees continue.
pub fn capture_controlled<F, C>(
    command: &mut Command,
    root: &Path,
    limits: Limits,
    cancelled: &AtomicBool,
    mut action_for: F,
    mut control: C,
) -> Result<CompleteRecord, TraceFailure>
where
    F: FnMut(&paths::DecodedOperation) -> CaptureAction,
    C: FnMut(&RawEntry) -> Result<ControlDirective, TraceFailure>,
{
    let root = fs::canonicalize(root).map_err(|_| supervision("root_resolution"))?;
    if !root.is_dir() {
        return Err(supervision("root_directory"));
    }
    // Root exec happens in Command::spawn before ptrace can observe a syscall
    // entry. Serialize admission with the trace session and account for its
    // delay and breakpoint against the same execution deadline.
    let _trace_lock = TRACE_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    if limits.max_events < 2 || limits.max_bytes == 0 || limits.kill_after.is_zero() {
        return Err(supervision("limits"));
    }
    let clock = TraceClock::start(limits)?;
    let TraceClock { began, deadline } = clock;
    if cancelled.load(Ordering::SeqCst) {
        return Err(TraceFailure::Cancellation);
    }
    let program = root_program(command)?;
    let encoded_program =
        CString::new(program.as_os_str().as_bytes()).map_err(|_| TraceFailure::Spawn)?;
    let root_entry = RawEntry {
        ordinal: 0,
        pid: std::process::id(),
        tid: unsafe { libc::gettid() as u32 },
        parent_pid: None,
        syscall: libc::SYS_execve as u64,
        args: [encoded_program.as_ptr() as usize as u64, 0, 0, 0, 0, 0],
        monotonic_ns: u64::try_from(began.elapsed().as_nanos())
            .map_err(|_| supervision("time_limit"))?,
    };
    let mut root_operation =
        paths::decode(&root_entry, &root)?.ok_or_else(|| supervision("root_exec_decode"))?;
    if root_operation.path_unavailable || root_operation.paths.len() != 1 {
        return Err(supervision("root_exec_path"));
    }
    root_operation.paths[0].identity = file_id::get_file_id(&program)
        .ok()
        .map(crate::record::FileIdentity::from);
    let root_charge = serde_json::to_vec(&root_operation.paths)
        .map_err(|_| supervision("capture_encoding"))?
        .len() as u64
        + 1024;
    let mut retained_bytes = root
        .as_os_str()
        .as_bytes()
        .len()
        .saturating_mul(4)
        .saturating_add(1024) as u64
        + root_charge;
    if retained_bytes > limits.max_bytes {
        return Err(TraceFailure::ByteLimit);
    }
    let root_action = action_for(&root_operation);
    let root_delay = match root_action {
        CaptureAction::Proceed(delay) => delay,
        CaptureAction::Hold => Duration::ZERO,
    };
    let requested_delay_ns =
        u64::try_from(root_delay.as_nanos()).map_err(|_| supervision("delay_limit"))?;
    let observed_delay_ns = if root_delay.is_zero() {
        0
    } else {
        u64::try_from(
            crate::delay::wait(root_delay, cancelled, deadline)
                .map_err(|failure| match failure {
                    crate::delay::DelayFailure::Cancelled => TraceFailure::Cancellation,
                    crate::delay::DelayFailure::Timeout => TraceFailure::Timeout,
                })?
                .as_nanos(),
        )
        .map_err(|_| supervision("delay_limit"))?
    };
    let mut continue_all = false;
    if matches!(root_action, CaptureAction::Hold) {
        loop {
            if cancelled.load(Ordering::SeqCst) {
                return Err(TraceFailure::Cancellation);
            }
            if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                return Err(TraceFailure::Timeout);
            }
            match control(&root_entry)? {
                ControlDirective::Wait => thread::sleep(Duration::from_millis(2)),
                ControlDirective::ReleaseOne => break,
                ControlDirective::ContinueAll => {
                    continue_all = true;
                    break;
                }
                ControlDirective::Quit => return Err(TraceFailure::Cancellation),
            }
        }
    }
    let root_completion_ns =
        u64::try_from(began.elapsed().as_nanos()).map_err(|_| supervision("time_limit"))?;
    let mut starts = HashMap::<u64, CapturedEntry>::new();
    // Charge a generous upper bound before retaining each decoded path. This
    // bounds both the start map and the supervisor's completion buffer while
    // the child runs, before final NDJSON serialization checks exact bytes.
    let result = trace_controlled_locked(
        command,
        limits,
        cancelled,
        clock,
        continue_all,
        |entry| {
            let Some(decoded) = paths::decode(entry, &root)? else {
                return Ok(EntryAction::Ignore);
            };
            let path_bytes = serde_json::to_vec(&decoded.paths)
                .map_err(|_| supervision("capture_encoding"))?
                .len() as u64;
            let charge = path_bytes.saturating_add(1024);
            if retained_bytes.saturating_add(charge) > limits.max_bytes {
                return Err(TraceFailure::ByteLimit);
            }
            if starts.len().saturating_add(2).saturating_mul(2) > limits.max_events {
                return Err(TraceFailure::EventLimit);
            }
            retained_bytes += charge;
            let action = action_for(&decoded);
            let delay = match action {
                CaptureAction::Proceed(delay) => delay,
                CaptureAction::Hold => Duration::ZERO,
            };
            let requested_delay_ns =
                u64::try_from(delay.as_nanos()).map_err(|_| supervision("delay_limit"))?;
            let observed = if delay.is_zero() {
                Duration::ZERO
            } else {
                crate::delay::wait(delay, cancelled, deadline).map_err(|failure| match failure {
                    crate::delay::DelayFailure::Cancelled => TraceFailure::Cancellation,
                    crate::delay::DelayFailure::Timeout => TraceFailure::Timeout,
                })?
            };
            let observed_delay_ns =
                u64::try_from(observed.as_nanos()).map_err(|_| supervision("delay_limit"))?;
            if starts
                .insert(
                    entry.ordinal,
                    CapturedEntry {
                        operation: decoded,
                        requested_delay_ns,
                        observed_delay_ns,
                    },
                )
                .is_some()
            {
                return Err(supervision("duplicate_capture"));
            }
            Ok(match action {
                CaptureAction::Proceed(_) => EntryAction::Record,
                CaptureAction::Hold => EntryAction::Hold,
            })
        },
        control,
    )?;
    if starts.len() != result.operations.len() {
        return Err(supervision("unpaired_capture"));
    }
    let mut events =
        Vec::<Event>::with_capacity(result.operations.len().saturating_add(1).saturating_mul(2));
    events.push(Event::Start(Start {
        sequence: 0,
        correlation_id: 1,
        pid: result.root_pid,
        tid: result.root_pid,
        parent_pid: Some(std::process::id()),
        operation: Operation::Exec,
        open_mutates: false,
        paths: root_operation.paths,
        path_unavailable: false,
        descriptor: None,
        monotonic_ns: root_entry.monotonic_ns,
        requested_delay_ns,
    }));
    events.push(Event::Completion(Completion {
        sequence: 0,
        correlation_id: 1,
        pid: result.root_pid,
        tid: result.root_pid,
        monotonic_ns: root_completion_ns,
        native_result: 0,
        native_error: None,
        byte_count: None,
        observed_delay_ns,
    }));
    for completed in result.operations {
        let entry = completed.entry;
        let correlation_id = entry
            .ordinal
            .checked_add(1)
            .ok_or(TraceFailure::EventLimit)?;
        let captured = starts
            .remove(&entry.ordinal)
            .ok_or_else(|| supervision("missing_capture"))?;
        let operation = captured.operation.operation;
        let byte_count = if !completed.failed
            && matches!(
                operation,
                Operation::Read
                    | Operation::Write
                    | Operation::PositionalRead
                    | Operation::PositionalWrite
            ) {
            Some(u64::try_from(completed.result).map_err(|_| supervision("negative_byte_count"))?)
        } else {
            None
        };
        let native_error = completed
            .failed
            .then(|| i32::try_from(-completed.result).unwrap_or(libc::EIO));
        events.push(Event::Start(Start {
            sequence: 0,
            correlation_id,
            pid: entry.pid,
            tid: entry.tid,
            parent_pid: entry.parent_pid,
            operation,
            open_mutates: captured.operation.open_mutates,
            paths: captured.operation.paths,
            path_unavailable: captured.operation.path_unavailable,
            descriptor: captured.operation.descriptor,
            monotonic_ns: entry.monotonic_ns,
            requested_delay_ns: captured.requested_delay_ns,
        }));
        events.push(Event::Completion(Completion {
            sequence: 0,
            correlation_id,
            pid: entry.pid,
            tid: entry.tid,
            monotonic_ns: completed.monotonic_ns,
            native_result: completed.result,
            native_error,
            byte_count,
            observed_delay_ns: captured.observed_delay_ns,
        }));
    }
    events.sort_by_key(|event| {
        (
            event.monotonic_ns(),
            event.correlation_id(),
            matches!(event, Event::Completion(_)),
        )
    });
    let mut in_flight = HashMap::<u64, Start>::new();
    let mut operations = Vec::with_capacity(events.len() / 2);
    for (index, event) in events.into_iter().enumerate() {
        let sequence = u64::try_from(index + 1).map_err(|_| TraceFailure::EventLimit)?;
        match event {
            Event::Start(mut start) => {
                start.sequence = sequence;
                in_flight.insert(start.correlation_id, start);
            }
            Event::Completion(mut completion) => {
                completion.sequence = sequence;
                let start = in_flight
                    .remove(&completion.correlation_id)
                    .ok_or_else(|| supervision("capture_event_order"))?;
                operations.push(OperationPair { start, completion });
            }
        }
    }
    if !in_flight.is_empty() {
        return Err(supervision("capture_event_loss"));
    }
    let failure_count = operations
        .iter()
        .filter(|pair| pair.completion.native_error.is_some())
        .count() as u64;
    let (child_exit_code, child_signal) = match result.outcome {
        ChildOutcome::Exit(code) => (Some(code), None),
        ChildOutcome::Signal(signal) => (None, Some(signal)),
    };
    let record = CompleteRecord {
        header: Header {
            schema_version: SCHEMA_VERSION,
            execution_id: uuid::Uuid::now_v7(),
            platform: Platform::Linux,
            backend: Backend::Ptrace,
            root: NativePath::UnixBytes(root.as_os_str().as_bytes().to_vec()),
            coverage: CoverageBoundary::SynchronousFileOperationsV1,
        },
        summary: Summary {
            complete: true,
            child_exit_code,
            child_signal,
            operation_count: operations.len() as u64,
            failure_count,
            failure: None,
        },
        operations,
    };
    crate::record::serialize(
        &record,
        &mut std::io::sink(),
        limits.max_events,
        limits.max_bytes,
    )
    .map_err(|error| match error {
        crate::record::ParseFailure::ByteLimit => TraceFailure::ByteLimit,
        crate::record::ParseFailure::EventLimit => TraceFailure::EventLimit,
        _ => supervision("capture_encoding"),
    })?;
    Ok(record)
}

#[cfg(test)]
mod tests {
    use std::{io::Write, process::Stdio};

    use super::*;
    use crate::record::{parse, serialize, DEFAULT_BYTE_LIMIT, DEFAULT_EVENT_LIMIT};

    #[test]
    fn root_executable_is_recorded_before_child_operations() {
        let directory = tempfile::tempdir().unwrap();
        let program = directory.path().join("tool");
        fs::copy("/bin/true", &program).unwrap();
        let mut command = Command::new("./tool");
        command.current_dir(directory.path()).stdout(Stdio::null());
        let record = capture(
            &mut command,
            directory.path(),
            Limits::default(),
            &AtomicBool::new(false),
            |operation| {
                if operation.operation == Operation::Exec {
                    Duration::from_millis(10)
                } else {
                    Duration::ZERO
                }
            },
        )
        .unwrap();
        let first = &record.operations[0];
        assert_eq!(first.start.operation, Operation::Exec);
        assert_eq!(first.start.sequence, 1);
        assert_eq!(first.completion.sequence, 2);
        assert_eq!(first.start.correlation_id, 1);
        assert_eq!(first.start.pid, first.completion.pid);
        assert_eq!(first.start.requested_delay_ns, 10_000_000);
        assert!(first.completion.observed_delay_ns > 0);
        assert!(first.start.paths.iter().any(|path| {
            path.project_relative == Some(NativePath::UnixBytes(b"tool".to_vec()))
                && path.identity.is_some()
        }));
        let mut encoded = Vec::new();
        serialize(
            &record,
            &mut encoded,
            DEFAULT_EVENT_LIMIT,
            DEFAULT_BYTE_LIMIT,
        )
        .unwrap();
        parse(
            std::io::Cursor::new(encoded),
            DEFAULT_EVENT_LIMIT,
            DEFAULT_BYTE_LIMIT,
        )
        .unwrap();
    }

    #[test]
    fn root_breakpoint_quit_prevents_launch() {
        let directory = tempfile::tempdir().unwrap();
        let marker = directory.path().join("launched");
        let mut command = Command::new("/bin/sh");
        command.arg("-c").arg(format!("touch {}", marker.display()));
        let result = capture_controlled(
            &mut command,
            directory.path(),
            Limits::default(),
            &AtomicBool::new(false),
            |operation| {
                if operation.operation == Operation::Exec {
                    CaptureAction::Hold
                } else {
                    CaptureAction::Proceed(Duration::ZERO)
                }
            },
            |_| Ok(ControlDirective::Quit),
        );
        assert!(matches!(result, Err(TraceFailure::Cancellation)));
        assert!(!marker.exists());
    }

    #[test]
    fn pairs_actual_read_with_native_identity_and_result() {
        let mut input = tempfile::NamedTempFile::new().unwrap();
        input.write_all(b"capture fixture\n").unwrap();
        let root = input.path().parent().unwrap();
        let mut command = Command::new("/bin/cat");
        command.arg(input.path()).stdout(Stdio::null());
        let record = capture(
            &mut command,
            root,
            Limits::default(),
            &AtomicBool::new(false),
            |_| Duration::ZERO,
        )
        .unwrap();
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation == Operation::Read
                && pair.completion.byte_count.is_some_and(|count| count > 0)
                && pair.start.paths.iter().any(|path| path.identity.is_some())
        }));
        let mut encoded = Vec::new();
        serialize(
            &record,
            &mut encoded,
            DEFAULT_EVENT_LIMIT,
            DEFAULT_BYTE_LIMIT,
        )
        .unwrap();
        let restored = parse(
            std::io::Cursor::new(encoded),
            DEFAULT_EVENT_LIMIT,
            DEFAULT_BYTE_LIMIT,
        )
        .unwrap();
        assert_eq!(
            restored.summary.operation_count,
            record.summary.operation_count
        );
    }

    #[test]
    fn refuses_a_trace_larger_than_the_encoded_byte_budget() {
        let directory = tempfile::tempdir().unwrap();
        let mut command = Command::new("/bin/true");
        command.stdout(Stdio::null());
        let result = capture(
            &mut command,
            directory.path(),
            Limits {
                max_bytes: 10,
                ..Limits::default()
            },
            &AtomicBool::new(false),
            |_| Duration::ZERO,
        );
        assert!(matches!(result, Err(TraceFailure::ByteLimit)));
    }

    #[test]
    fn injected_delay_obeys_execution_timeout() {
        let mut input = tempfile::NamedTempFile::new().unwrap();
        input.write_all(b"fixture").unwrap();
        let mut command = Command::new("/bin/cat");
        command.arg(input.path()).stdout(Stdio::null());
        // Capture sessions share a lock, so unrelated tests may run before this read.
        let mut read_started = None;
        let result = capture(
            &mut command,
            input.path().parent().unwrap(),
            Limits {
                timeout: Some(Duration::from_secs(3)),
                kill_after: Duration::from_millis(100),
                ..Limits::default()
            },
            &AtomicBool::new(false),
            |operation| {
                if operation.operation == Operation::Read {
                    read_started.get_or_insert_with(Instant::now);
                    Duration::from_secs(60)
                } else {
                    Duration::ZERO
                }
            },
        );
        assert!(matches!(result, Err(TraceFailure::Timeout)));
        assert!(read_started.is_some_and(|began| began.elapsed() < Duration::from_secs(4)));
    }
}
