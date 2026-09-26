//! Convert paired ptrace stops into a complete version-one operation record.

use std::{
    collections::HashMap,
    fs,
    os::unix::ffi::OsStrExt,
    path::Path,
    process::Command,
    sync::atomic::AtomicBool,
    thread,
    time::{Duration, Instant},
};

use super::{paths, supervision, trace, ChildOutcome, Limits, TraceFailure};
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
    let root = fs::canonicalize(root).map_err(|_| supervision("root_resolution"))?;
    if !root.is_dir() {
        return Err(supervision("root_directory"));
    }
    let mut starts = HashMap::<u64, CapturedEntry>::new();
    let result = trace(command, limits, cancelled, |entry| {
        let Some(decoded) = paths::decode(entry, &root)? else {
            return Ok(false);
        };
        let delay = delay_for(&decoded);
        let requested_delay_ns =
            u64::try_from(delay.as_nanos()).map_err(|_| supervision("delay_limit"))?;
        let began = Instant::now();
        if !delay.is_zero() {
            thread::sleep(delay);
        }
        let observed_delay_ns =
            u64::try_from(began.elapsed().as_nanos()).map_err(|_| supervision("delay_limit"))?;
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
        Ok(true)
    })?;
    if starts.len() != result.operations.len() {
        return Err(supervision("unpaired_capture"));
    }
    let mut events = Vec::<Event>::with_capacity(result.operations.len().saturating_mul(2));
    for completed in result.operations {
        let entry = completed.entry;
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
            correlation_id: entry.ordinal,
            pid: entry.pid,
            tid: entry.tid,
            parent_pid: entry.parent_pid,
            operation,
            paths: captured.operation.paths,
            path_unavailable: captured.operation.path_unavailable,
            descriptor: captured.operation.descriptor,
            monotonic_ns: entry.monotonic_ns,
            requested_delay_ns: captured.requested_delay_ns,
        }));
        events.push(Event::Completion(Completion {
            sequence: 0,
            correlation_id: entry.ordinal,
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
}
