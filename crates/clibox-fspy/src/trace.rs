use std::{
    collections::{BTreeMap, BTreeSet},
    fmt,
    io::{self, BufRead, Write},
};

use base64::{Engine as _, engine::general_purpose::STANDARD};
use clap::ValueEnum;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub const SCHEMA_VERSION: u32 = 1;
pub const DEFAULT_MAX_EVENTS: usize = 1_000_000;
pub const DEFAULT_MAX_BYTES: usize = 256 * 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Platform {
    Macos,
    Windows,
    LinuxGnu,
    LinuxMusl,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Backend {
    Ptrace,
    UnixInjection,
    WindowsInjection,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PathEncoding {
    UnixBytes,
    WindowsUtf16Le,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PathScope {
    Project,
    External,
}

/// Paths are encoded as raw Unix bytes or UTF-16LE code units. The latter
/// preserves even ill-formed native Windows names without replacement chars.
#[derive(Clone, Debug, Eq, PartialEq, Ord, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EncodedPath {
    pub scope: PathScope,
    pub encoding: PathEncoding,
    pub base64: String,
}

impl EncodedPath {
    #[must_use]
    pub fn from_raw(scope: PathScope, encoding: PathEncoding, raw: &[u8]) -> Self {
        Self {
            scope,
            encoding,
            base64: STANDARD.encode(raw),
        }
    }

    /// # Errors
    ///
    /// Returns `InvalidStructure` for invalid base64, empty paths, or NUL
    /// units.
    pub fn decode(&self) -> Result<Vec<u8>, TraceError> {
        let bytes = STANDARD
            .decode(&self.base64)
            .map_err(|_| TraceError::InvalidStructure)?;
        let invalid_units = match self.encoding {
            PathEncoding::UnixBytes => bytes.contains(&0),
            PathEncoding::WindowsUtf16Le => {
                bytes.len() % 2 != 0 || bytes.chunks_exact(2).any(|unit| unit == [0, 0])
            }
        };
        if bytes.is_empty() || invalid_units || STANDARD.encode(&bytes) != self.base64 {
            return Err(TraceError::InvalidStructure);
        }
        Ok(bytes)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Serialize, Deserialize, ValueEnum)]
#[serde(rename_all = "kebab-case")]
pub enum Operation {
    Open,
    Close,
    Read,
    Pread,
    Write,
    Pwrite,
    Metadata,
    Directory,
    Create,
    Remove,
    Rename,
    Link,
    OtherMutation,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FailureClass {
    TracingUnavailable,
    PermissionDenied,
    EventLoss,
    ResourceLimit,
    Timeout,
    Cancelled,
    ChildFailure,
    CleanupFailure,
    OutputFailure,
    ControlLoss,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CoverageKind {
    SynchronousOpenClose,
    SynchronousReadWrite,
    PositionalReadWrite,
    Metadata,
    Directory,
    PathnameMutation,
    Descendants,
    Mmap,
    AsynchronousIo,
    InterceptionBypass,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Coverage {
    pub observed: [CoverageKind; 7],
    pub excluded: [CoverageKind; 3],
}

impl Coverage {
    #[must_use]
    pub const fn declared() -> Self {
        use CoverageKind as K;
        Self {
            observed: [
                K::SynchronousOpenClose,
                K::SynchronousReadWrite,
                K::PositionalReadWrite,
                K::Metadata,
                K::Directory,
                K::PathnameMutation,
                K::Descendants,
            ],
            excluded: [K::Mmap, K::AsynchronousIo, K::InterceptionBypass],
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Header {
    pub schema_version: u32,
    pub execution_id: Uuid,
    pub platform: Platform,
    pub backend: Backend,
    pub root: EncodedPath,
    pub coverage: Coverage,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Start {
    pub sequence: u64,
    pub operation_id: u64,
    pub pid: u32,
    pub tid: u32,
    pub parent_pid: Option<u32>,
    pub operation: Operation,
    pub paths: Vec<EncodedPath>,
    pub monotonic_ns: u64,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Completion {
    pub sequence: u64,
    pub operation_id: u64,
    pub monotonic_ns: u64,
    pub native_result: i64,
    pub native_error: Option<i64>,
    pub bytes: Option<u64>,
    pub injected_delay_ns: u64,
    pub resolved_paths: Vec<EncodedPath>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Summary {
    pub sequence: u64,
    pub complete: bool,
    pub child_exit_code: Option<i32>,
    pub child_signal: Option<i32>,
    pub operation_count: u64,
    pub failure_count: u64,
    pub classification: Option<FailureClass>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "kebab-case", deny_unknown_fields)]
pub enum Event {
    Header(Header),
    OperationStart(Start),
    OperationCompletion(Completion),
    Summary(Summary),
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum TraceError {
    Input,
    ResourceLimit,
    UnsupportedVersion,
    InvalidStructure,
    Incomplete,
    Incompatible,
}

impl fmt::Display for TraceError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::Input => "trace_input_failed",
            Self::ResourceLimit => "trace_resource_limit",
            Self::UnsupportedVersion => "trace_unsupported_version",
            Self::InvalidStructure => "trace_invalid_structure",
            Self::Incomplete => "trace_incomplete",
            Self::Incompatible => "trace_incompatible",
        })
    }
}

impl std::error::Error for TraceError {}

/// # Errors
///
/// Returns an I/O error when encoding or writing fails.
pub fn write_event(writer: &mut impl Write, event: &Event) -> io::Result<()> {
    serde_json::to_writer(&mut *writer, event).map_err(io::Error::other)?;
    writer.write_all(b"\n")
}

pub struct CompleteTrace {
    pub header: Header,
    pub operations: Vec<(Start, Completion)>,
    pub summary: Summary,
}

/// The limit is on encoded bytes, before JSON parsing, and counts line feeds.
/// Receipt sequence establishes collector order only; concurrent execution
/// order cannot be inferred from it.
///
/// # Errors
///
/// Returns a classified error for I/O failure, malformed input, incomplete
/// execution, unsupported versions, or exhausted limits.
#[expect(
    clippy::too_many_lines,
    reason = "The record state machine is kept together for auditability"
)]
pub fn read_complete(
    reader: impl BufRead,
    max_events: usize,
    max_bytes: usize,
) -> Result<CompleteTrace, TraceError> {
    let mut header = None;
    let mut summary = None;
    let mut active = BTreeMap::<u64, Start>::new();
    let mut operations = Vec::new();
    let mut next_sequence = 1u64;
    let mut total_bytes = 0usize;
    let mut count = 0usize;
    let mut failures = 0u64;
    // Bound a single attacker-controlled line before allocating it. The
    // parser must reject a trace exceeding the configured encoded limit.
    for line in reader.take(max_bytes.saturating_add(1) as u64).split(b'\n') {
        let line = line.map_err(|_| TraceError::Input)?;
        total_bytes = total_bytes
            .checked_add(line.len() + 1)
            .ok_or(TraceError::ResourceLimit)?;
        count += 1;
        if total_bytes > max_bytes || count > max_events.saturating_mul(2).saturating_add(2) {
            return Err(TraceError::ResourceLimit);
        }
        if line.is_empty() {
            return Err(TraceError::InvalidStructure);
        }
        let event: Event =
            serde_json::from_slice(&line).map_err(|_| TraceError::InvalidStructure)?;
        if summary.is_some() {
            return Err(TraceError::InvalidStructure);
        }
        match event {
            Event::Header(value) if header.is_none() && count == 1 => {
                if value.schema_version != SCHEMA_VERSION {
                    return Err(TraceError::UnsupportedVersion);
                }
                if value.execution_id.get_version_num() != 7
                    || value.coverage != Coverage::declared()
                    || value.root.scope != PathScope::Project
                    || value.root.decode().is_err()
                    || !matches!(
                        (value.platform, value.backend),
                        (Platform::LinuxGnu | Platform::LinuxMusl, Backend::Ptrace)
                            | (Platform::Macos, Backend::UnixInjection)
                            | (Platform::Windows, Backend::WindowsInjection)
                    )
                {
                    return Err(TraceError::InvalidStructure);
                }
                header = Some(value);
            }
            Event::OperationStart(value) if header.is_some() => {
                check_sequence(value.sequence, &mut next_sequence)?;
                if value.pid == 0
                    || value.tid == 0
                    || value.operation_id == 0
                    || value.paths.is_empty()
                    || value.paths.len() > 2
                    || value.paths.iter().any(|path| path.decode().is_err())
                    || active.insert(value.operation_id, value.clone()).is_some()
                {
                    return Err(TraceError::InvalidStructure);
                }
                if operations.len() + active.len() > max_events {
                    return Err(TraceError::ResourceLimit);
                }
            }
            Event::OperationCompletion(value) if header.is_some() => {
                check_sequence(value.sequence, &mut next_sequence)?;
                let start = active
                    .remove(&value.operation_id)
                    .ok_or(TraceError::InvalidStructure)?;
                let expected_error = if value.native_result < 0 {
                    Some(
                        value
                            .native_result
                            .checked_neg()
                            .ok_or(TraceError::InvalidStructure)?,
                    )
                } else {
                    None
                };
                if value.monotonic_ns < start.monotonic_ns
                    || value.native_error != expected_error
                    || (value.native_error.is_none()
                        && matches!(
                            start.operation,
                            Operation::Read
                                | Operation::Pread
                                | Operation::Write
                                | Operation::Pwrite
                        ) != value.bytes.is_some())
                    || value.resolved_paths.len() > start.paths.len()
                    || value
                        .resolved_paths
                        .iter()
                        .any(|path| path.decode().is_err())
                    || (value.native_result >= 0
                        && value
                            .bytes
                            .is_some_and(|bytes| bytes != value.native_result.cast_unsigned()))
                {
                    return Err(TraceError::InvalidStructure);
                }
                failures += u64::from(value.native_error.is_some());
                operations.push((start, value));
            }
            Event::Summary(value) if header.is_some() => {
                check_sequence(value.sequence, &mut next_sequence)?;
                if (value.complete && !active.is_empty())
                    || value.operation_count != operations.len() as u64
                    || value.failure_count != failures
                    || (value.complete
                        && value.child_exit_code.is_some() == value.child_signal.is_some())
                    || (value.complete && value.classification.is_some())
                    || (!value.complete && value.classification.is_none())
                {
                    return Err(TraceError::InvalidStructure);
                }
                summary = Some(value);
            }
            _ => return Err(TraceError::InvalidStructure),
        }
    }
    let header = header.ok_or(TraceError::InvalidStructure)?;
    let summary = summary.ok_or(TraceError::Incomplete)?;
    if !summary.complete {
        return Err(TraceError::Incomplete);
    }
    Ok(CompleteTrace {
        header,
        operations,
        summary,
    })
}

fn check_sequence(sequence: u64, next: &mut u64) -> Result<(), TraceError> {
    if sequence != *next {
        return Err(TraceError::InvalidStructure);
    }
    *next = next.checked_add(1).ok_or(TraceError::ResourceLimit)?;
    Ok(())
}

#[derive(Clone, Debug, Serialize)]
pub struct PathComparison {
    pub path: EncodedPath,
    pub before_kinds: BTreeSet<Operation>,
    pub after_kinds: BTreeSet<Operation>,
    pub before_count: usize,
    pub after_count: usize,
    pub before_failures: usize,
    pub after_failures: usize,
    pub before_operation_ns: u128,
    pub after_operation_ns: u128,
}

#[derive(Clone, Debug, Serialize)]
pub struct Comparison {
    pub added: Vec<PathComparison>,
    pub removed: Vec<PathComparison>,
    pub changed_kinds: Vec<PathComparison>,
    pub count_or_timing: Vec<PathComparison>,
    pub external: Vec<PathComparison>,
}

impl Comparison {
    #[must_use]
    pub const fn has_structural_change(&self) -> bool {
        !(self.added.is_empty() && self.removed.is_empty() && self.changed_kinds.is_empty())
    }
}

/// # Errors
///
/// Returns `Incompatible` when platform, backend, or coverage differs.
pub fn compare(before: &CompleteTrace, after: &CompleteTrace) -> Result<Comparison, TraceError> {
    if before.header.platform != after.header.platform
        || before.header.backend != after.header.backend
        || before.header.coverage != after.header.coverage
    {
        return Err(TraceError::Incompatible);
    }
    let before_paths = aggregate(before);
    let after_paths = aggregate(after);
    let mut result = Comparison {
        added: Vec::new(),
        removed: Vec::new(),
        changed_kinds: Vec::new(),
        count_or_timing: Vec::new(),
        external: Vec::new(),
    };
    let mut seen = BTreeSet::new();
    for path in before_paths.keys().chain(after_paths.keys()) {
        if !seen.insert(path) {
            continue;
        }
        let before = before_paths.get(path);
        let after = after_paths.get(path);
        let entry = PathComparison {
            path: (*path).clone(),
            before_kinds: before.map_or_else(BTreeSet::new, |v| v.kinds.clone()),
            after_kinds: after.map_or_else(BTreeSet::new, |v| v.kinds.clone()),
            before_count: before.map_or(0, |v| v.count),
            after_count: after.map_or(0, |v| v.count),
            before_failures: before.map_or(0, |v| v.failures),
            after_failures: after.map_or(0, |v| v.failures),
            before_operation_ns: before.map_or(0, |v| v.operation_ns),
            after_operation_ns: after.map_or(0, |v| v.operation_ns),
        };
        if path.scope == PathScope::External {
            result.external.push(entry);
        } else if before.is_none() {
            result.added.push(entry);
        } else if after.is_none() {
            result.removed.push(entry);
        } else if entry.before_kinds != entry.after_kinds {
            result.changed_kinds.push(entry);
        } else if entry.before_count != entry.after_count
            || entry.before_failures != entry.after_failures
            || entry.before_operation_ns != entry.after_operation_ns
        {
            result.count_or_timing.push(entry);
        }
    }
    Ok(result)
}

#[derive(Default)]
struct Aggregate {
    kinds: BTreeSet<Operation>,
    count: usize,
    failures: usize,
    operation_ns: u128,
}

fn aggregate(trace: &CompleteTrace) -> BTreeMap<EncodedPath, Aggregate> {
    let mut result = BTreeMap::<EncodedPath, Aggregate>::new();
    for (start, completion) in &trace.operations {
        for path in &start.paths {
            let entry = result.entry(path.clone()).or_default();
            entry.kinds.insert(start.operation);
            entry.count += 1;
            entry.failures += usize::from(completion.native_error.is_some());
            entry.operation_ns += u128::from(completion.monotonic_ns - start.monotonic_ns);
        }
    }
    result
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;

    use super::*;

    fn fixture(path: EncodedPath, operation: Operation, bytes: Option<u64>) -> Vec<Event> {
        vec![
            Event::Header(Header {
                schema_version: SCHEMA_VERSION,
                execution_id: Uuid::now_v7(),
                platform: Platform::LinuxGnu,
                backend: Backend::Ptrace,
                root: EncodedPath::from_raw(
                    PathScope::Project,
                    PathEncoding::UnixBytes,
                    b"/workspace",
                ),
                coverage: Coverage::declared(),
            }),
            Event::OperationStart(Start {
                sequence: 1,
                operation_id: 91,
                pid: 12,
                tid: 13,
                parent_pid: Some(11),
                operation,
                paths: vec![path],
                monotonic_ns: 100,
            }),
            Event::OperationCompletion(Completion {
                sequence: 2,
                operation_id: 91,
                monotonic_ns: 125,
                native_result: i64::try_from(bytes.unwrap_or(0)).unwrap(),
                native_error: None,
                bytes,
                injected_delay_ns: 0,
                resolved_paths: Vec::new(),
            }),
            Event::Summary(Summary {
                sequence: 3,
                complete: true,
                child_exit_code: Some(0),
                child_signal: None,
                operation_count: 1,
                failure_count: 0,
                classification: None,
            }),
        ]
    }

    fn parse(events: &[Event]) -> Result<CompleteTrace, TraceError> {
        let mut encoded = Vec::new();
        for event in events {
            write_event(&mut encoded, event).unwrap();
        }
        read_complete(Cursor::new(encoded), DEFAULT_MAX_EVENTS, DEFAULT_MAX_BYTES)
    }

    #[test]
    fn unix_non_utf8_and_windows_utf16_paths_round_trip() {
        for (encoding, raw) in [
            (PathEncoding::UnixBytes, &[b'f', b'\xff'][..]),
            (PathEncoding::WindowsUtf16Le, &[b'f', 0, 0x00, 0xd8][..]),
        ] {
            let path = EncodedPath::from_raw(PathScope::Project, encoding, raw);
            assert_eq!(path.decode().unwrap(), raw);
            let serialized = serde_json::to_vec(&path).unwrap();
            let decoded: EncodedPath = serde_json::from_slice(&serialized).unwrap();
            assert_eq!(decoded.decode().unwrap(), raw);
        }
    }

    #[test]
    fn complete_pair_round_trips_and_missing_summary_fails() {
        let events = fixture(
            EncodedPath::from_raw(PathScope::Project, PathEncoding::UnixBytes, b"input"),
            Operation::Read,
            Some(1),
        );
        assert_eq!(parse(&events).unwrap().operations.len(), 1);
        assert_eq!(parse(&events[..3]).err(), Some(TraceError::Incomplete));
    }

    #[test]
    fn event_loss_and_invalid_operation_results_fail_closed() {
        let mut events = fixture(
            EncodedPath::from_raw(PathScope::Project, PathEncoding::UnixBytes, b"input"),
            Operation::Read,
            Some(1),
        );
        if let Event::OperationCompletion(completion) = &mut events[2] {
            completion.sequence = 4;
        }
        assert_eq!(parse(&events).err(), Some(TraceError::InvalidStructure));
        if let Event::OperationCompletion(completion) = &mut events[2] {
            completion.sequence = 2;
            completion.bytes = Some(2);
        }
        assert_eq!(parse(&events).err(), Some(TraceError::InvalidStructure));
        if let Event::Summary(summary) = &mut events[3] {
            summary.complete = false;
            summary.classification = Some(FailureClass::EventLoss);
        }
        if let Event::OperationCompletion(completion) = &mut events[2] {
            completion.bytes = Some(1);
        }
        assert_eq!(parse(&events).err(), Some(TraceError::Incomplete));
    }

    #[test]
    fn unknown_version_and_oversized_trace_fail() {
        let mut events = fixture(
            EncodedPath::from_raw(PathScope::Project, PathEncoding::UnixBytes, b"input"),
            Operation::Open,
            None,
        );
        if let Event::Header(header) = &mut events[0] {
            header.schema_version = 2;
        }
        assert_eq!(parse(&events).err(), Some(TraceError::UnsupportedVersion));
        if let Event::Header(header) = &mut events[0] {
            header.schema_version = 1;
        }
        let mut encoded = Vec::new();
        for event in &events {
            write_event(&mut encoded, event).unwrap();
        }
        assert_eq!(
            read_complete(Cursor::new(encoded), 1, 10).err(),
            Some(TraceError::ResourceLimit)
        );
    }

    #[test]
    fn comparison_rebases_project_paths_and_gates_only_structural_changes() {
        let path = EncodedPath::from_raw(PathScope::Project, PathEncoding::UnixBytes, b"input");
        let before = parse(&fixture(path.clone(), Operation::Read, Some(1))).unwrap();
        let mut noisy = fixture(path.clone(), Operation::Read, Some(2));
        if let Event::Header(header) = &mut noisy[0] {
            header.root =
                EncodedPath::from_raw(PathScope::Project, PathEncoding::UnixBytes, b"/other-root");
        }
        if let Event::OperationStart(start) = &mut noisy[1] {
            start.pid = 999;
            start.monotonic_ns = 1000;
        }
        if let Event::OperationCompletion(completion) = &mut noisy[2] {
            completion.monotonic_ns = 1100;
        }
        let after = parse(&noisy).unwrap();
        let comparison = compare(&before, &after).unwrap();
        assert!(!comparison.has_structural_change());
        assert_eq!(comparison.count_or_timing.len(), 1);

        let changed = parse(&fixture(path, Operation::Write, Some(1))).unwrap();
        assert!(compare(&before, &changed).unwrap().has_structural_change());
    }
}
