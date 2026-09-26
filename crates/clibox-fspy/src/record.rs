//! Strict, bounded parsing of the version-one operation record.

use std::{
    collections::{BTreeMap, BTreeSet},
    io::{self, BufRead, Read},
};

use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub const SCHEMA_VERSION: u32 = 1;
pub const DEFAULT_EVENT_LIMIT: usize = 1_000_000;
pub const DEFAULT_BYTE_LIMIT: u64 = 256 * 1024 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Platform {
    Linux,
    Macos,
    Windows,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Backend {
    Ptrace,
    Injection,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Operation {
    Open,
    Close,
    Read,
    Write,
    PositionalRead,
    PositionalWrite,
    Metadata,
    Directory,
    Mutation,
    Exec,
}

impl Operation {
    pub const fn is_content_read(self) -> bool {
        matches!(self, Self::Read | Self::PositionalRead)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum CoverageBoundary {
    SynchronousFileOperationsV1,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "encoding", content = "units", rename_all = "snake_case")]
pub enum NativePath {
    UnixBytes(Vec<u8>),
    WindowsUtf16(Vec<u16>),
}

impl NativePath {
    fn is_valid(&self, platform: Platform) -> bool {
        match (self, platform) {
            (Self::UnixBytes(bytes), Platform::Linux | Platform::Macos) => {
                !bytes.is_empty() && !bytes.contains(&0)
            }
            (Self::WindowsUtf16(units), Platform::Windows) => {
                !units.is_empty() && !units.contains(&0)
            }
            _ => false,
        }
    }

    fn is_project_relative(&self) -> bool {
        match self {
            Self::UnixBytes(bytes) => {
                !bytes.starts_with(b"/")
                    && !bytes.split(|byte| *byte == b'/').any(|part| part == b"..")
            }
            Self::WindowsUtf16(units) => {
                !units
                    .first()
                    .is_some_and(|unit| *unit == b'\\' as u16 || *unit == b'/' as u16)
                    && !units.contains(&(b':' as u16))
                    && !units
                        .split(|unit| *unit == b'\\' as u16 || *unit == b'/' as u16)
                        .any(|part| part == [b'.' as u16, b'.' as u16])
            }
        }
    }

    fn is_absolute(&self) -> bool {
        match self {
            Self::UnixBytes(bytes) => bytes.starts_with(b"/"),
            Self::WindowsUtf16(units) => {
                let is_separator = |unit: u16| unit == b'\\' as u16 || unit == b'/' as u16;
                (units.len() >= 2 && is_separator(units[0]) && is_separator(units[1]))
                    || (units.len() >= 3
                        && units[0] <= 127
                        && (units[0] as u8).is_ascii_alphabetic()
                        && units[1] == b':' as u16
                        && is_separator(units[2]))
            }
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PathClass {
    Project,
    External,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AccessPath {
    pub class: PathClass,
    pub logical: NativePath,
    pub resolved: Option<NativePath>,
    pub project_relative: Option<NativePath>,
    pub identity: Option<FileIdentity>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum FileIdentity {
    Inode { device: u64, inode: u64 },
    Windows { volume: u64, file_id: u128 },
}

impl From<file_id::FileId> for FileIdentity {
    fn from(value: file_id::FileId) -> Self {
        match value {
            file_id::FileId::Inode {
                device_id,
                inode_number,
            } => Self::Inode {
                device: device_id,
                inode: inode_number,
            },
            file_id::FileId::LowRes {
                volume_serial_number,
                file_index,
            } => Self::Windows {
                volume: u64::from(volume_serial_number),
                file_id: u128::from(file_index),
            },
            file_id::FileId::HighRes {
                volume_serial_number,
                file_id,
            } => Self::Windows {
                volume: volume_serial_number,
                file_id,
            },
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Header {
    pub schema_version: u32,
    pub execution_id: Uuid,
    pub platform: Platform,
    pub backend: Backend,
    pub root: NativePath,
    pub coverage: CoverageBoundary,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Start {
    pub sequence: u64,
    pub correlation_id: u64,
    pub pid: u32,
    pub tid: u32,
    pub parent_pid: Option<u32>,
    pub operation: Operation,
    pub paths: Vec<AccessPath>,
    /// True when a failed native call supplied an unreadable or unresolvable
    /// pathname argument; the operation result is still observed.
    pub path_unavailable: bool,
    pub descriptor: Option<i32>,
    pub monotonic_ns: u64,
    pub requested_delay_ns: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Completion {
    pub sequence: u64,
    pub correlation_id: u64,
    pub pid: u32,
    pub tid: u32,
    pub monotonic_ns: u64,
    pub native_result: i64,
    pub native_error: Option<i32>,
    pub byte_count: Option<u64>,
    pub observed_delay_ns: u64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum FailureClass {
    Permission,
    UnsupportedTarget,
    TraceInitialization,
    TraceLoss,
    EventLimit,
    ByteLimit,
    Timeout,
    Cancellation,
    Cleanup,
    ChildFailure,
    Publication,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Summary {
    pub complete: bool,
    pub child_exit_code: Option<i64>,
    pub child_signal: Option<i32>,
    pub operation_count: u64,
    pub failure_count: u64,
    pub failure: Option<FailureClass>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(
    tag = "type",
    content = "data",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub enum RecordLine {
    Header(Header),
    Start(Start),
    Completion(Completion),
    Summary(Summary),
}

#[derive(Debug, Clone)]
pub struct OperationPair {
    pub start: Start,
    pub completion: Completion,
}

#[derive(Debug, Clone)]
pub struct CompleteRecord {
    pub header: Header,
    pub operations: Vec<OperationPair>,
    pub summary: Summary,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ParseFailure {
    Io,
    InvalidStructure,
    UnknownVersion,
    Incomplete,
    EventLimit,
    ByteLimit,
}

impl std::fmt::Display for ParseFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let code = match self {
            Self::Io => "record_io",
            Self::InvalidStructure => "invalid_record",
            Self::UnknownVersion => "unsupported_schema",
            Self::Incomplete => "incomplete_record",
            Self::EventLimit => "event_limit",
            Self::ByteLimit => "byte_limit",
        };
        formatter.write_str(code)
    }
}

impl std::error::Error for ParseFailure {}

fn valid_path(path: &AccessPath, platform: Platform) -> bool {
    if !path.logical.is_valid(platform)
        || path
            .resolved
            .as_ref()
            .is_some_and(|resolved| !resolved.is_valid(platform))
    {
        return false;
    }
    if path.identity.is_some_and(|identity| {
        !matches!(
            (platform, identity),
            (
                Platform::Linux | Platform::Macos,
                FileIdentity::Inode { .. }
            ) | (Platform::Windows, FileIdentity::Windows { .. })
        )
    }) {
        return false;
    }
    match path.class {
        PathClass::Project => path
            .project_relative
            .as_ref()
            .is_some_and(|relative| relative.is_valid(platform) && relative.is_project_relative()),
        PathClass::External => path.project_relative.is_none(),
    }
}

/// Parse one complete record. The bound applies before JSON decoding and the
/// parser never treats a missing terminal summary as a successful execution.
pub fn parse<R: BufRead>(
    mut reader: R,
    event_limit: usize,
    byte_limit: u64,
) -> Result<CompleteRecord, ParseFailure> {
    if event_limit == 0 || byte_limit == 0 {
        return Err(ParseFailure::InvalidStructure);
    }
    let mut total_bytes = 0_u64;
    let mut line = Vec::new();
    let mut header = None;
    let mut summary = None;
    let mut pending = BTreeMap::<u64, Start>::new();
    let mut pairs = Vec::new();
    let mut event_count = 0_usize;
    let mut previous_sequence = 0_u64;
    loop {
        line.clear();
        // take() keeps even a maliciously long single line within the bound.
        let remaining = byte_limit.saturating_sub(total_bytes);
        if remaining == 0 {
            if reader.fill_buf().map_err(|_| ParseFailure::Io)?.is_empty() {
                break;
            }
            return Err(ParseFailure::ByteLimit);
        }
        let read = (&mut reader)
            .take(remaining.saturating_add(1))
            .read_until(b'\n', &mut line)
            .map_err(|_| ParseFailure::Io)?;
        if read == 0 {
            break;
        }
        total_bytes = total_bytes
            .checked_add(read as u64)
            .ok_or(ParseFailure::ByteLimit)?;
        if total_bytes > byte_limit {
            return Err(ParseFailure::ByteLimit);
        }
        if !line.ends_with(b"\n") {
            return Err(ParseFailure::Incomplete);
        }
        let item: RecordLine =
            serde_json::from_slice(&line).map_err(|_| ParseFailure::InvalidStructure)?;
        match item {
            RecordLine::Header(value)
                if header.is_none()
                    && summary.is_none()
                    && pairs.is_empty()
                    && pending.is_empty() =>
            {
                if value.schema_version != SCHEMA_VERSION {
                    return Err(ParseFailure::UnknownVersion);
                }
                if value.execution_id.get_version_num() != 7
                    || !value.root.is_valid(value.platform)
                    || !value.root.is_absolute()
                    || !matches!(
                        (value.platform, value.backend),
                        (Platform::Linux, Backend::Ptrace)
                            | (Platform::Macos | Platform::Windows, Backend::Injection)
                    )
                {
                    return Err(ParseFailure::InvalidStructure);
                }
                header = Some(value);
            }
            RecordLine::Start(value) if header.is_some() && summary.is_none() => {
                event_count += 1;
                if event_count > event_limit {
                    return Err(ParseFailure::EventLimit);
                }
                let platform = header.as_ref().expect("guarded header").platform;
                if value.sequence <= previous_sequence
                    || value.correlation_id == 0
                    || value.pid == 0
                    || value.tid == 0
                    || (value.paths.is_empty()
                        && value.descriptor.is_none()
                        && !value.path_unavailable)
                    || !value.paths.iter().all(|path| valid_path(path, platform))
                    || pending
                        .insert(value.correlation_id, value.clone())
                        .is_some()
                {
                    return Err(ParseFailure::InvalidStructure);
                }
                previous_sequence = value.sequence;
            }
            RecordLine::Completion(value) if header.is_some() && summary.is_none() => {
                event_count += 1;
                if event_count > event_limit {
                    return Err(ParseFailure::EventLimit);
                }
                let start = pending
                    .remove(&value.correlation_id)
                    .ok_or(ParseFailure::InvalidStructure)?;
                if value.sequence <= previous_sequence
                    || value.pid != start.pid
                    || value.tid != start.tid
                    || value.monotonic_ns < start.monotonic_ns
                    || value.observed_delay_ns > value.monotonic_ns - start.monotonic_ns
                    || (value.native_error.is_some() != (value.native_result < 0))
                    || (value.byte_count.is_some()
                        && !matches!(
                            start.operation,
                            Operation::Read
                                | Operation::Write
                                | Operation::PositionalRead
                                | Operation::PositionalWrite
                        ))
                    || (value.native_result < 0 && value.byte_count.is_some())
                    || (value.native_result >= 0
                        && matches!(
                            start.operation,
                            Operation::Read
                                | Operation::Write
                                | Operation::PositionalRead
                                | Operation::PositionalWrite
                        )
                        && value.byte_count.is_none())
                {
                    return Err(ParseFailure::InvalidStructure);
                }
                if let Some(bytes) = value.byte_count {
                    if value.native_result < 0
                        || u64::try_from(value.native_result).ok() != Some(bytes)
                    {
                        return Err(ParseFailure::InvalidStructure);
                    }
                }
                previous_sequence = value.sequence;
                pairs.push(OperationPair {
                    start,
                    completion: value,
                });
            }
            RecordLine::Summary(value) if header.is_some() && summary.is_none() => {
                if !pending.is_empty()
                    || value.operation_count != pairs.len() as u64
                    || value.failure_count
                        != pairs
                            .iter()
                            .filter(|pair| pair.completion.native_error.is_some())
                            .count() as u64
                    || (value.complete && value.failure.is_some())
                    || (!value.complete && value.failure.is_none())
                    || (value.child_exit_code.is_some() && value.child_signal.is_some())
                    || (value.complete
                        && value.child_exit_code.is_none()
                        && value.child_signal.is_none())
                    || value.child_signal.is_some_and(|signal| signal <= 0)
                {
                    return Err(ParseFailure::InvalidStructure);
                }
                summary = Some(value);
            }
            _ => return Err(ParseFailure::InvalidStructure),
        }
    }
    let header = header.ok_or(ParseFailure::InvalidStructure)?;
    let summary = summary.ok_or(ParseFailure::Incomplete)?;
    if !summary.complete {
        return Err(ParseFailure::Incomplete);
    }
    Ok(CompleteRecord {
        header,
        operations: pairs,
        summary,
    })
}

/// Structural keys ignore incidental process IDs and absolute start times.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
pub struct ProjectKey(pub Vec<u8>);

fn native_key(path: &NativePath) -> Vec<u8> {
    match path {
        NativePath::UnixBytes(bytes) => bytes.clone(),
        NativePath::WindowsUtf16(units) => {
            units.iter().flat_map(|unit| unit.to_le_bytes()).collect()
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PathAggregate {
    pub operations: BTreeSet<OperationKey>,
    pub count: u64,
    pub failures: u64,
    pub total_duration_ns: u128,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum OperationKey {
    Open,
    Close,
    Read,
    Write,
    PositionalRead,
    PositionalWrite,
    Metadata,
    Directory,
    Mutation,
    Exec,
}

impl From<Operation> for OperationKey {
    fn from(value: Operation) -> Self {
        match value {
            Operation::Open => Self::Open,
            Operation::Close => Self::Close,
            Operation::Read => Self::Read,
            Operation::Write => Self::Write,
            Operation::PositionalRead => Self::PositionalRead,
            Operation::PositionalWrite => Self::PositionalWrite,
            Operation::Metadata => Self::Metadata,
            Operation::Directory => Self::Directory,
            Operation::Mutation => Self::Mutation,
            Operation::Exec => Self::Exec,
        }
    }
}

pub fn aggregate_project(record: &CompleteRecord) -> BTreeMap<ProjectKey, PathAggregate> {
    aggregate_paths(record, true)
}

pub fn aggregate_external(record: &CompleteRecord) -> BTreeMap<ProjectKey, PathAggregate> {
    aggregate_paths(record, false)
}

fn aggregate_paths(record: &CompleteRecord, project: bool) -> BTreeMap<ProjectKey, PathAggregate> {
    let mut result = BTreeMap::<ProjectKey, PathAggregate>::new();
    for pair in &record.operations {
        for path in &pair.start.paths {
            let selected = if project {
                path.project_relative.as_ref()
            } else if path.class == PathClass::External {
                Some(&path.logical)
            } else {
                None
            };
            if let Some(path) = selected {
                let entry = result
                    .entry(ProjectKey(native_key(path)))
                    .or_insert_with(|| PathAggregate {
                        operations: BTreeSet::new(),
                        count: 0,
                        failures: 0,
                        total_duration_ns: 0,
                    });
                entry.operations.insert(pair.start.operation.into());
                entry.count += 1;
                entry.failures += u64::from(pair.completion.native_error.is_some());
                entry.total_duration_ns +=
                    u128::from(pair.completion.monotonic_ns - pair.start.monotonic_ns);
            }
        }
    }
    result
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Comparison {
    pub added: Vec<ProjectKey>,
    pub removed: Vec<ProjectKey>,
    pub changed_operations: Vec<ProjectKey>,
    pub changed_counts: Vec<ProjectKey>,
    pub changed_failures: Vec<ProjectKey>,
    pub changed_timing: Vec<ProjectKey>,
    pub external_added: Vec<ProjectKey>,
    pub external_removed: Vec<ProjectKey>,
    pub external_changed_operations: Vec<ProjectKey>,
    pub external_changed_counts: Vec<ProjectKey>,
    pub external_changed_failures: Vec<ProjectKey>,
    pub external_changed_timing: Vec<ProjectKey>,
}

impl Comparison {
    pub fn gated_change(&self) -> bool {
        !self.added.is_empty() || !self.removed.is_empty() || !self.changed_operations.is_empty()
    }
}

pub fn compare(
    before: &CompleteRecord,
    after: &CompleteRecord,
) -> Result<Comparison, ParseFailure> {
    if before.header.platform != after.header.platform
        || before.header.coverage != after.header.coverage
        || before.header.backend != after.header.backend
    {
        return Err(ParseFailure::InvalidStructure);
    }
    let old = aggregate_project(before);
    let new = aggregate_project(after);
    let mut comparison = Comparison {
        added: Vec::new(),
        removed: Vec::new(),
        changed_operations: Vec::new(),
        changed_counts: Vec::new(),
        changed_failures: Vec::new(),
        changed_timing: Vec::new(),
        external_added: Vec::new(),
        external_removed: Vec::new(),
        external_changed_operations: Vec::new(),
        external_changed_counts: Vec::new(),
        external_changed_failures: Vec::new(),
        external_changed_timing: Vec::new(),
    };
    for (key, value) in &new {
        match old.get(key) {
            None => comparison.added.push(key.clone()),
            Some(previous) => {
                if previous.operations != value.operations {
                    comparison.changed_operations.push(key.clone());
                }
                if previous.count != value.count {
                    comparison.changed_counts.push(key.clone());
                }
                if previous.failures != value.failures {
                    comparison.changed_failures.push(key.clone());
                }
                if previous.total_duration_ns != value.total_duration_ns {
                    comparison.changed_timing.push(key.clone());
                }
            }
        }
    }
    for key in old.keys() {
        if !new.contains_key(key) {
            comparison.removed.push(key.clone());
        }
    }
    let old_external = aggregate_external(before);
    let new_external = aggregate_external(after);
    for (key, value) in &new_external {
        match old_external.get(key) {
            None => comparison.external_added.push(key.clone()),
            Some(previous) => {
                if previous.operations != value.operations {
                    comparison.external_changed_operations.push(key.clone());
                }
                if previous.count != value.count {
                    comparison.external_changed_counts.push(key.clone());
                }
                if previous.failures != value.failures {
                    comparison.external_changed_failures.push(key.clone());
                }
                if previous.total_duration_ns != value.total_duration_ns {
                    comparison.external_changed_timing.push(key.clone());
                }
            }
        }
    }
    for key in old_external.keys() {
        if !new_external.contains_key(key) {
            comparison.external_removed.push(key.clone());
        }
    }
    Ok(comparison)
}

pub fn write_line<W: io::Write>(writer: &mut W, line: &RecordLine) -> io::Result<()> {
    serde_json::to_writer(&mut *writer, line)?;
    writer.write_all(b"\n")
}

/// Encode an in-memory completed execution in receipt order. Build within the
/// byte bound before touching the caller's writer, so an exceeded limit never
/// publishes a truncated record that could look successful.
pub fn serialize<W: io::Write>(
    record: &CompleteRecord,
    writer: &mut W,
    event_limit: usize,
    byte_limit: u64,
) -> Result<(), ParseFailure> {
    let event_count = record
        .operations
        .len()
        .checked_mul(2)
        .ok_or(ParseFailure::EventLimit)?;
    if event_limit == 0 || event_count > event_limit {
        return Err(ParseFailure::EventLimit);
    }
    if byte_limit == 0 {
        return Err(ParseFailure::ByteLimit);
    }
    let mut events = Vec::with_capacity(event_count);
    for pair in &record.operations {
        events.push((pair.start.sequence, RecordLine::Start(pair.start.clone())));
        events.push((
            pair.completion.sequence,
            RecordLine::Completion(pair.completion.clone()),
        ));
    }
    events.sort_by_key(|(sequence, _)| *sequence);
    let mut encoded = Vec::new();
    write_line(&mut encoded, &RecordLine::Header(record.header.clone()))
        .map_err(|_| ParseFailure::Io)?;
    if encoded.len() as u64 > byte_limit {
        return Err(ParseFailure::ByteLimit);
    }
    for (_, event) in events {
        write_line(&mut encoded, &event).map_err(|_| ParseFailure::Io)?;
        if encoded.len() as u64 > byte_limit {
            return Err(ParseFailure::ByteLimit);
        }
    }
    write_line(&mut encoded, &RecordLine::Summary(record.summary.clone()))
        .map_err(|_| ParseFailure::Io)?;
    if encoded.len() as u64 > byte_limit {
        return Err(ParseFailure::ByteLimit);
    }
    writer.write_all(&encoded).map_err(|_| ParseFailure::Io)
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;

    use super::*;

    fn fixture(relative: &[u8], operation: Operation) -> Vec<u8> {
        let id = Uuid::parse_str("01890f7e-4b1c-7cc2-9dce-dfca43a88f6f").unwrap();
        let header = RecordLine::Header(Header {
            schema_version: SCHEMA_VERSION,
            execution_id: id,
            platform: Platform::Macos,
            backend: Backend::Injection,
            root: NativePath::UnixBytes(b"/project".to_vec()),
            coverage: CoverageBoundary::SynchronousFileOperationsV1,
        });
        let start = RecordLine::Start(Start {
            sequence: 1,
            correlation_id: 5,
            pid: 100,
            tid: 101,
            parent_pid: None,
            operation,
            paths: vec![AccessPath {
                class: PathClass::Project,
                logical: NativePath::UnixBytes([b"/project/".as_slice(), relative].concat()),
                resolved: None,
                project_relative: Some(NativePath::UnixBytes(relative.to_vec())),
                identity: None,
            }],
            path_unavailable: false,
            descriptor: None,
            monotonic_ns: 100,
            requested_delay_ns: 0,
        });
        let completion = RecordLine::Completion(Completion {
            sequence: 2,
            correlation_id: 5,
            pid: 100,
            tid: 101,
            monotonic_ns: 120,
            native_result: 1,
            native_error: None,
            byte_count: operation.is_content_read().then_some(1),
            observed_delay_ns: 0,
        });
        let summary = RecordLine::Summary(Summary {
            complete: true,
            child_exit_code: Some(0),
            child_signal: None,
            operation_count: 1,
            failure_count: 0,
            failure: None,
        });
        let mut out = Vec::new();
        for line in [header, start, completion, summary] {
            write_line(&mut out, &line).unwrap();
        }
        out
    }

    fn parse_fixture(bytes: &[u8]) -> Result<CompleteRecord, ParseFailure> {
        parse(Cursor::new(bytes), DEFAULT_EVENT_LIMIT, DEFAULT_BYTE_LIMIT)
    }

    #[test]
    fn accepts_complete_record_with_non_utf8_path() {
        let record = parse_fixture(&fixture(b"odd-\xff", Operation::Read)).unwrap();
        assert_eq!(record.operations.len(), 1);
        assert_eq!(record.operations[0].completion.byte_count, Some(1));
        assert_eq!(aggregate_project(&record).len(), 1);
    }

    #[test]
    fn rejects_missing_and_incomplete_summaries() {
        let bytes = fixture(b"input", Operation::Open);
        let mut lines = bytes.split_inclusive(|byte| *byte == b'\n');
        let incomplete = lines
            .by_ref()
            .take(3)
            .flatten()
            .copied()
            .collect::<Vec<_>>();
        assert_eq!(
            parse_fixture(&incomplete).unwrap_err(),
            ParseFailure::Incomplete
        );
        let mut malformed = bytes.clone();
        malformed.truncate(malformed.len() - 1);
        assert_eq!(
            parse_fixture(&malformed).unwrap_err(),
            ParseFailure::Incomplete
        );
    }

    #[test]
    fn rejects_unknown_version_and_mismatched_operations() {
        let mut values = fixture(b"input", Operation::Read)
            .split_inclusive(|byte| *byte == b'\n')
            .map(|line| serde_json::from_slice::<serde_json::Value>(line).unwrap())
            .collect::<Vec<_>>();
        values[0]["data"]["schema_version"] = 2.into();
        let encoded = values
            .iter()
            .flat_map(|value| {
                let mut bytes = serde_json::to_vec(value).unwrap();
                bytes.push(b'\n');
                bytes
            })
            .collect::<Vec<_>>();
        assert_eq!(
            parse_fixture(&encoded).unwrap_err(),
            ParseFailure::UnknownVersion
        );

        values[0]["data"]["schema_version"] = 1.into();
        values[2]["data"]["correlation_id"] = 6.into();
        let encoded = values
            .iter()
            .flat_map(|value| {
                let mut bytes = serde_json::to_vec(value).unwrap();
                bytes.push(b'\n');
                bytes
            })
            .collect::<Vec<_>>();
        assert_eq!(
            parse_fixture(&encoded).unwrap_err(),
            ParseFailure::InvalidStructure
        );
    }

    #[test]
    fn rejects_extra_fields_and_escaping_project_paths() {
        let bytes = fixture(b"input", Operation::Read);
        let mut values = bytes
            .split_inclusive(|byte| *byte == b'\n')
            .map(|line| serde_json::from_slice::<serde_json::Value>(line).unwrap())
            .collect::<Vec<_>>();
        values[1]["unexpected"] = true.into();
        let encoded = values
            .iter()
            .flat_map(|value| {
                let mut bytes = serde_json::to_vec(value).unwrap();
                bytes.push(b'\n');
                bytes
            })
            .collect::<Vec<_>>();
        assert_eq!(
            parse_fixture(&encoded).unwrap_err(),
            ParseFailure::InvalidStructure
        );

        values[1].as_object_mut().unwrap().remove("unexpected");
        values[1]["data"]["paths"][0]["project_relative"]["units"] =
            serde_json::json!([46, 46, 47, 115, 101, 99, 114, 101, 116]);
        let encoded = values
            .iter()
            .flat_map(|value| {
                let mut bytes = serde_json::to_vec(value).unwrap();
                bytes.push(b'\n');
                bytes
            })
            .collect::<Vec<_>>();
        assert_eq!(
            parse_fixture(&encoded).unwrap_err(),
            ParseFailure::InvalidStructure
        );
    }

    #[test]
    fn enforces_limits_before_accepting_analysis() {
        let bytes = fixture(b"input", Operation::Read);
        assert_eq!(
            parse(Cursor::new(&bytes), 1, DEFAULT_BYTE_LIMIT).unwrap_err(),
            ParseFailure::EventLimit
        );
        assert_eq!(
            parse(Cursor::new(&bytes), 100, 12).unwrap_err(),
            ParseFailure::ByteLimit
        );
    }

    #[test]
    fn structural_comparison_ignores_process_and_timing_noise() {
        let before = parse_fixture(&fixture(b"a", Operation::Read)).unwrap();
        let mut after = parse_fixture(&fixture(b"a", Operation::Read)).unwrap();
        after.operations[0].start.pid = 900;
        after.operations[0].completion.pid = 900;
        after.operations[0].start.monotonic_ns = 300;
        after.operations[0].completion.monotonic_ns = 320;
        assert!(!compare(&before, &after).unwrap().gated_change());
        after.operations[0].start.operation = Operation::Write;
        let changed = compare(&before, &after).unwrap();
        assert!(changed.gated_change());
        assert_eq!(changed.changed_operations.len(), 1);
    }

    #[test]
    fn detects_added_and_removed_paths_across_roots() {
        let before = parse_fixture(&fixture(b"a", Operation::Read)).unwrap();
        let mut after = parse_fixture(&fixture(b"b", Operation::Read)).unwrap();
        after.header.root = NativePath::UnixBytes(b"/other".to_vec());
        let changed = compare(&before, &after).unwrap();
        assert_eq!(changed.added.len(), 1);
        assert_eq!(changed.removed.len(), 1);
    }
}
