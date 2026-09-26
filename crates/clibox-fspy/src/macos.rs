//! Strict receiver for the private macOS injected-operation side channel.
//!
//! The wire is intentionally independent of the legacy attempted-access
//! channel. A caller must validate both channels and owned-process cleanup
//! before it may publish a complete execution.

pub mod supervise;

use std::{
    collections::{HashMap, HashSet},
    ffi::OsString,
    fs,
    io::{self, Read},
    os::unix::{
        ffi::{OsStrExt, OsStringExt},
        net::{UnixListener, UnixStream},
        process::ExitStatusExt,
    },
    path::{Component, Path, PathBuf},
    process::ExitStatus,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};

use crate::record::{
    self, AccessPath, Backend, CompleteRecord, Completion, CoverageBoundary, FileIdentity, Header,
    NativePath, Operation, OperationPair, PathClass, Platform, Start, Summary, SCHEMA_VERSION,
};

const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;
const MAX_CONNECTIONS: usize = 4096;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FrameKind {
    Hello,
    Start,
    Completion,
}

#[derive(Debug, Clone)]
pub struct Frame {
    pub sequence: u64,
    pub kind: FrameKind,
    pub operation: u8,
    pub pid: u32,
    pub parent_pid: u32,
    pub tid: u64,
    pub id: u64,
    pub monotonic_ns: u64,
    pub result: i64,
    pub error: i32,
    pub path: Vec<u8>,
    pub access_path: Option<AccessPath>,
    pub requested_delay_ns: u64,
    pub observed_delay_ns: u64,
}

fn invalid(reason: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, reason)
}

pub fn read_frame(reader: &mut impl Read) -> io::Result<Option<Frame>> {
    let mut header = [0_u8; HEADER_BYTES];
    let first = loop {
        match reader.read(&mut header[..1]) {
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            result => break result?,
        }
    };
    match first {
        0 => return Ok(None),
        1 => reader.read_exact(&mut header[1..])?,
        _ => unreachable!("one-byte read exceeded its buffer"),
    }
    let kind = match header[0] {
        b'h' => FrameKind::Hello,
        b's' => FrameKind::Start,
        b'e' => FrameKind::Completion,
        _ => return Err(invalid("frame_kind")),
    };
    let operation = header[1];
    if (kind == FrameKind::Hello && operation != 0)
        || (kind != FrameKind::Hello && !(1..=9).contains(&operation))
    {
        return Err(invalid("operation_kind"));
    }
    let pid = u32::from_le_bytes(header[2..6].try_into().expect("fixed header"));
    let parent_pid = u32::from_le_bytes(header[6..10].try_into().expect("fixed header"));
    let tid = u64::from_le_bytes(header[10..18].try_into().expect("fixed header"));
    let id = u64::from_le_bytes(header[18..26].try_into().expect("fixed header"));
    let monotonic_ns = u64::from_le_bytes(header[26..34].try_into().expect("fixed header"));
    let result = i64::from_le_bytes(header[34..42].try_into().expect("fixed header"));
    let error = i32::from_le_bytes(header[42..46].try_into().expect("fixed header"));
    let length = u32::from_le_bytes(header[46..50].try_into().expect("fixed header")) as usize;
    if length > MAX_PATH_BYTES
        || pid == 0
        || tid == 0
        || id == 0
        || monotonic_ns == 0
        || (matches!(kind, FrameKind::Hello | FrameKind::Start) && (result != 0 || error != 0))
        || (kind == FrameKind::Hello && length != 0)
        || (kind == FrameKind::Completion
            && (length != 0 || (result < 0) != (error != 0) || error < 0))
    {
        return Err(invalid("frame_shape"));
    }
    let mut path = vec![0; length];
    reader.read_exact(&mut path)?;
    if path.contains(&0) {
        return Err(invalid("path_nul"));
    }
    Ok(Some(Frame {
        sequence: 0,
        kind,
        operation,
        pid,
        parent_pid,
        tid,
        id,
        monotonic_ns,
        result,
        error,
        path,
        access_path: None,
        requested_delay_ns: 0,
        observed_delay_ns: 0,
    }))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Admission {
    Proceed(Duration),
    Quit,
}

type AdmissionPolicy = dyn Fn(&Frame) -> Admission + Send + Sync;

#[derive(Default)]
pub struct FrameLedger {
    pending: HashMap<(u32, u64, u64), Frame>,
    completed: Vec<(Frame, Frame)>,
    hello_pids: HashSet<u32>,
    frame_count: u64,
    event_count: usize,
    max_events: usize,
    max_bytes: u64,
    received_bytes: u64,
}

impl FrameLedger {
    pub fn new(max_events: usize, max_bytes: u64) -> Self {
        Self {
            max_events,
            max_bytes,
            ..Self::default()
        }
    }

    pub fn push(&mut self, mut frame: Frame) -> io::Result<()> {
        self.frame_count = self
            .frame_count
            .checked_add(1)
            .ok_or_else(|| invalid("frame_limit"))?;
        if frame.kind != FrameKind::Hello {
            self.event_count = self
                .event_count
                .checked_add(1)
                .ok_or_else(|| invalid("event_limit"))?;
        }
        self.received_bytes = self
            .received_bytes
            .checked_add((HEADER_BYTES + frame.path.len()) as u64)
            .ok_or_else(|| invalid("byte_limit"))?;
        if self.event_count > self.max_events || self.received_bytes > self.max_bytes {
            return Err(invalid("frame_limit"));
        }
        frame.sequence = self.frame_count;
        let key = (frame.pid, frame.tid, frame.id);
        match frame.kind {
            FrameKind::Hello => {
                self.hello_pids.insert(frame.pid);
            }
            FrameKind::Start => {
                if self.pending.insert(key, frame).is_some() {
                    return Err(invalid("duplicate_start"));
                }
            }
            FrameKind::Completion => {
                let start = self
                    .pending
                    .remove(&key)
                    .ok_or_else(|| invalid("unpaired_completion"))?;
                if start.operation != frame.operation
                    || start.parent_pid != frame.parent_pid
                    || frame.monotonic_ns < start.monotonic_ns
                {
                    return Err(invalid("mismatched_completion"));
                }
                self.completed.push((start, frame));
            }
        }
        Ok(())
    }

    pub fn finish(self) -> io::Result<CollectedOperations> {
        if !self.pending.is_empty() {
            return Err(invalid("unpaired_start"));
        }
        Ok(CollectedOperations {
            pairs: self.completed,
            hello_pids: self.hello_pids,
        })
    }
}

pub struct CollectedOperations {
    pub pairs: Vec<(Frame, Frame)>,
    pub hello_pids: HashSet<u32>,
}

struct RetryRead<'a> {
    stream: &'a mut UnixStream,
    stopping: &'a AtomicBool,
}

impl Read for RetryRead<'_> {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        loop {
            match self.stream.read(buffer) {
                Err(error)
                    if matches!(
                        error.kind(),
                        io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
                    ) && !self.stopping.load(Ordering::Acquire) => {}
                result => return result,
            }
        }
    }
}

fn receive_connection(
    mut stream: UnixStream,
    ledger: &Mutex<FrameLedger>,
    stopping: &AtomicBool,
    root: Option<&Path>,
    admission: &AdmissionPolicy,
) -> io::Result<()> {
    use std::io::Write;

    stream.set_nonblocking(false)?;
    stream.set_read_timeout(Some(Duration::from_millis(100)))?;
    loop {
        let mut reader = RetryRead {
            stream: &mut stream,
            stopping,
        };
        let Some(mut frame) = read_frame(&mut reader)? else {
            return Ok(());
        };
        let start = frame.kind == FrameKind::Start;
        if start && !frame.path.is_empty() {
            if let Some(root) = root {
                frame.access_path = classify_path(root, &frame.path)?;
            }
        }
        if start {
            let decision =
                std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| admission(&frame)))
                    .map_err(|_| invalid("admission_policy"))?;
            let requested = match decision {
                Admission::Proceed(delay) => delay,
                Admission::Quit => {
                    stream.write_all(b"q")?;
                    return Ok(());
                }
            };
            frame.requested_delay_ns =
                u64::try_from(requested.as_nanos()).map_err(|_| invalid("delay_limit"))?;
            if !requested.is_zero() {
                let began = Instant::now();
                let deadline = began
                    .checked_add(requested)
                    .ok_or_else(|| invalid("delay_limit"))?;
                while Instant::now() < deadline {
                    if stopping.load(Ordering::Acquire) {
                        return Err(invalid("delay_cancelled"));
                    }
                    thread::sleep(
                        deadline
                            .saturating_duration_since(Instant::now())
                            .min(Duration::from_millis(20)),
                    );
                }
                frame.observed_delay_ns = u64::try_from(began.elapsed().as_nanos())
                    .map_err(|_| invalid("delay_limit"))?;
            }
        }
        ledger
            .lock()
            .map_err(|_| invalid("collector_lock"))?
            .push(frame)?;
        if start {
            stream.write_all(b"g")?;
        }
    }
}

/// Bounded receiver for an explicitly launched macOS injected process tree.
/// The caller must keep it alive until every owned process has exited.
pub struct OperationReceiver {
    _directory: tempfile::TempDir,
    socket_path: PathBuf,
    stopping: Arc<AtomicBool>,
    failed: Arc<AtomicBool>,
    receiver: Option<thread::JoinHandle<io::Result<CollectedOperations>>>,
}

type FramePairs = Vec<(Frame, Frame)>;

impl OperationReceiver {
    pub fn bind(max_events: usize, max_bytes: u64) -> io::Result<Self> {
        Self::bind_inner(
            None,
            max_events,
            max_bytes,
            Arc::new(|_| Admission::Proceed(Duration::ZERO)),
        )
    }

    pub fn bind_for_root(root: &Path, max_events: usize, max_bytes: u64) -> io::Result<Self> {
        Self::bind_with_delay(root, max_events, max_bytes, |_| Duration::ZERO)
    }

    pub fn bind_with_delay<F>(
        root: &Path,
        max_events: usize,
        max_bytes: u64,
        delay_for: F,
    ) -> io::Result<Self>
    where
        F: Fn(&Frame) -> Duration + Send + Sync + 'static,
    {
        let root = fs::canonicalize(root)?;
        if !root.is_dir() {
            return Err(invalid("root_not_directory"));
        }
        Self::bind_inner(
            Some(root),
            max_events,
            max_bytes,
            Arc::new(move |frame| Admission::Proceed(delay_for(frame))),
        )
    }

    pub fn bind_with_admission<F>(
        root: &Path,
        max_events: usize,
        max_bytes: u64,
        admission: F,
    ) -> io::Result<Self>
    where
        F: Fn(&Frame) -> Admission + Send + Sync + 'static,
    {
        let root = fs::canonicalize(root)?;
        if !root.is_dir() {
            return Err(invalid("root_not_directory"));
        }
        Self::bind_inner(Some(root), max_events, max_bytes, Arc::new(admission))
    }

    fn bind_inner(
        root: Option<PathBuf>,
        max_events: usize,
        max_bytes: u64,
        admission: Arc<AdmissionPolicy>,
    ) -> io::Result<Self> {
        if max_events == 0 || max_bytes == 0 {
            return Err(invalid("receiver_limit"));
        }
        let directory = tempfile::Builder::new().prefix("clibox-fspy-").tempdir()?;
        let socket_path = directory.path().join("operation.sock");
        let listener = UnixListener::bind(&socket_path)?;
        listener.set_nonblocking(true)?;
        let stopping = Arc::new(AtomicBool::new(false));
        let stop = Arc::clone(&stopping);
        let failed = Arc::new(AtomicBool::new(false));
        let failure = Arc::clone(&failed);
        let receiver = thread::spawn(move || {
            let ledger = Arc::new(Mutex::new(FrameLedger::new(max_events, max_bytes)));
            let mut connections = Vec::new();
            let mut idle_after_stop = 0;
            let mut accept_failure = None;
            loop {
                match listener.accept() {
                    Ok((stream, _)) => {
                        if connections.len() >= MAX_CONNECTIONS {
                            failure.store(true, Ordering::Release);
                            accept_failure = Some(invalid("connection_limit"));
                            stop.store(true, Ordering::Release);
                            drop(stream);
                            break;
                        }
                        idle_after_stop = 0;
                        let ledger = Arc::clone(&ledger);
                        let stop = Arc::clone(&stop);
                        let failure = Arc::clone(&failure);
                        let root = root.clone();
                        let admission = Arc::clone(&admission);
                        connections.push(thread::spawn(move || {
                            let result = receive_connection(
                                stream,
                                &ledger,
                                &stop,
                                root.as_deref(),
                                admission.as_ref(),
                            );
                            if result.is_err() {
                                failure.store(true, Ordering::Release);
                            }
                            result
                        }));
                    }
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                        if stop.load(Ordering::Acquire) {
                            idle_after_stop += 1;
                            if idle_after_stop >= 2 {
                                break;
                            }
                        }
                        thread::sleep(Duration::from_millis(10));
                    }
                    Err(error) => {
                        failure.store(true, Ordering::Release);
                        accept_failure = Some(error);
                        stop.store(true, Ordering::Release);
                        break;
                    }
                }
            }
            let mut connection_failure = None;
            for connection in connections {
                let result = connection.join().map_err(|_| invalid("receiver_panic"));
                if let Err(error) = result.and_then(|result| result) {
                    failure.store(true, Ordering::Release);
                    connection_failure.get_or_insert(error);
                }
            }
            if let Some(error) = accept_failure.or(connection_failure) {
                return Err(error);
            }
            Arc::try_unwrap(ledger)
                .map_err(|_| invalid("receiver_references"))?
                .into_inner()
                .map_err(|_| invalid("collector_lock"))?
                .finish()
        });
        Ok(Self {
            _directory: directory,
            socket_path,
            stopping,
            failed,
            receiver: Some(receiver),
        })
    }

    pub fn socket_path(&self) -> &Path {
        &self.socket_path
    }

    pub fn failed(&self) -> bool {
        self.failed.load(Ordering::Acquire)
    }

    pub fn finish(mut self) -> io::Result<CollectedOperations> {
        self.stopping.store(true, Ordering::Release);
        self.receiver
            .take()
            .expect("receiver exists before finish")
            .join()
            .map_err(|_| invalid("receiver_panic"))?
    }
}

impl Drop for OperationReceiver {
    fn drop(&mut self) {
        self.stopping.store(true, Ordering::Release);
    }
}

fn normalize(path: &Path) -> PathBuf {
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                normalized.pop();
            }
            Component::CurDir => {}
            Component::RootDir | Component::Normal(_) | Component::Prefix(_) => {
                normalized.push(component.as_os_str());
            }
        }
    }
    normalized
}

fn resolve_even_if_absent(path: &Path) -> io::Result<Option<PathBuf>> {
    let mut cursor = path;
    let mut tail = Vec::<OsString>::new();
    loop {
        match fs::canonicalize(cursor) {
            Ok(mut resolved) => {
                for component in tail.iter().rev() {
                    resolved.push(component);
                }
                return Ok(Some(normalize(&resolved)));
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                let component = cursor
                    .components()
                    .next_back()
                    .ok_or_else(|| invalid("missing_path_ancestor"))?;
                tail.push(component.as_os_str().to_os_string());
                cursor = cursor
                    .parent()
                    .ok_or_else(|| invalid("missing_path_parent"))?;
            }
            Err(error) if error.kind() == io::ErrorKind::PermissionDenied => return Ok(None),
            Err(error) => return Err(error),
        }
    }
}

fn classify_path(root: &Path, bytes: &[u8]) -> io::Result<Option<AccessPath>> {
    let logical = PathBuf::from(OsString::from_vec(bytes.to_vec()));
    if !logical.is_absolute() {
        return Err(invalid("non_absolute_path"));
    }
    let Some(resolved) = resolve_even_if_absent(&logical)? else {
        return Ok(None);
    };
    let relative = resolved.strip_prefix(root).ok();
    let identity = file_id::get_file_id(&logical).ok().map(FileIdentity::from);
    Ok(Some(AccessPath {
        class: if relative.is_some() {
            PathClass::Project
        } else {
            PathClass::External
        },
        logical: NativePath::UnixBytes(bytes.to_vec()),
        resolved: Some(NativePath::UnixBytes(
            resolved.as_os_str().as_bytes().to_vec(),
        )),
        project_relative: relative.map(|path| {
            let bytes = path.as_os_str().as_bytes();
            NativePath::UnixBytes(if bytes.is_empty() {
                b".".to_vec()
            } else {
                bytes.to_vec()
            })
        }),
        identity,
    }))
}

pub(crate) fn operation(kind: u8) -> Option<Operation> {
    Some(match kind {
        1 => Operation::Open,
        2 => Operation::Close,
        3 => Operation::Read,
        4 => Operation::Write,
        5 => Operation::PositionalRead,
        6 => Operation::PositionalWrite,
        7 => Operation::Metadata,
        8 => Operation::Directory,
        9 => Operation::Mutation,
        _ => return None,
    })
}

/// Build a candidate record from paired side-channel events. The caller must
/// independently prove complete injection, coverage, and process cleanup
/// before publishing it as a complete execution.
pub fn assemble_candidate_record(
    root: &Path,
    pairs: FramePairs,
    status: ExitStatus,
    max_events: usize,
    max_bytes: u64,
) -> io::Result<CompleteRecord> {
    let root = fs::canonicalize(root)?;
    if !root.is_dir() {
        return Err(invalid("root_not_directory"));
    }
    let mut operations = Vec::with_capacity(pairs.len());
    for (start, completion) in pairs {
        let kind = operation(start.operation).ok_or_else(|| invalid("operation_kind"))?;
        let path_unavailable = start.path.is_empty() || start.access_path.is_none();
        let paths = if path_unavailable {
            Vec::new()
        } else {
            vec![start
                .access_path
                .ok_or_else(|| invalid("unclassified_path"))?]
        };
        let byte_count = if completion.result >= 0
            && matches!(
                kind,
                Operation::Read
                    | Operation::Write
                    | Operation::PositionalRead
                    | Operation::PositionalWrite
            ) {
            Some(u64::try_from(completion.result).map_err(|_| invalid("byte_count"))?)
        } else {
            None
        };
        operations.push(OperationPair {
            start: Start {
                sequence: start.sequence,
                correlation_id: start.sequence,
                pid: start.pid,
                tid: u32::try_from(start.tid).map_err(|_| invalid("thread_id"))?,
                parent_pid: (start.parent_pid != 0).then_some(start.parent_pid),
                operation: kind,
                paths,
                path_unavailable,
                descriptor: None,
                monotonic_ns: start.monotonic_ns,
                requested_delay_ns: start.requested_delay_ns,
            },
            completion: Completion {
                sequence: completion.sequence,
                correlation_id: start.sequence,
                pid: completion.pid,
                tid: u32::try_from(completion.tid).map_err(|_| invalid("thread_id"))?,
                monotonic_ns: completion.monotonic_ns,
                native_result: completion.result,
                native_error: (completion.error != 0).then_some(completion.error),
                byte_count,
                observed_delay_ns: start.observed_delay_ns,
            },
        });
    }
    let failure_count = operations
        .iter()
        .filter(|pair| pair.completion.native_error.is_some())
        .count() as u64;
    let record = CompleteRecord {
        header: Header {
            schema_version: SCHEMA_VERSION,
            execution_id: uuid::Uuid::now_v7(),
            platform: Platform::Macos,
            backend: Backend::Injection,
            root: NativePath::UnixBytes(root.as_os_str().as_bytes().to_vec()),
            coverage: CoverageBoundary::SynchronousFileOperationsV1,
        },
        summary: Summary {
            complete: true,
            child_exit_code: status.code().map(i64::from),
            child_signal: status.signal(),
            operation_count: operations.len() as u64,
            failure_count,
            failure: None,
        },
        operations,
    };
    let mut encoded = Vec::new();
    record::serialize(&record, &mut encoded, max_events, max_bytes).map_err(|error| {
        tracing::error!(stage = "macos_candidate_serialize", classification = %error, "candidate record limit reached");
        invalid("record_limit")
    })?;
    record::parse(
        io::BufReader::new(encoded.as_slice()),
        max_events,
        max_bytes,
    )
    .map_err(|error| {
        tracing::error!(stage = "macos_candidate_parse", classification = %error, "candidate record rejected");
        invalid("candidate_record")
    })
}

#[cfg(test)]
mod tests {
    use std::{
        fs,
        io::{self, Read, Write},
        os::{
            fd::AsRawFd,
            unix::{ffi::OsStrExt, fs::symlink, net::UnixStream},
        },
        path::PathBuf,
        process::Stdio,
        time::{Duration, Instant},
    };

    use tokio_util::sync::CancellationToken;

    use super::{
        assemble_candidate_record, classify_path, read_frame, FrameKind, FrameLedger,
        OperationReceiver,
    };

    #[test]
    fn inaccessible_ancestor_does_not_abort_path_classification() {
        use std::os::unix::fs::PermissionsExt;

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let hidden = root.join("hidden");
        fs::create_dir(&hidden).unwrap();
        fs::set_permissions(&hidden, fs::Permissions::from_mode(0o000)).unwrap();
        let target = hidden.join("input.txt");
        let denied = fs::canonicalize(&target)
            .is_err_and(|error| error.kind() == io::ErrorKind::PermissionDenied);
        if denied {
            assert!(classify_path(&root, target.as_os_str().as_bytes())
                .unwrap()
                .is_none());
        }
        fs::set_permissions(&hidden, fs::Permissions::from_mode(0o700)).unwrap();
    }

    fn frame_bytes(kind: u8, path: &[u8]) -> Vec<u8> {
        let mut frame = Vec::new();
        frame.push(kind);
        frame.push(3);
        frame.extend_from_slice(&123_u32.to_le_bytes());
        frame.extend_from_slice(&1_u32.to_le_bytes());
        frame.extend_from_slice(&456_u64.to_le_bytes());
        frame.extend_from_slice(&789_u64.to_le_bytes());
        frame.extend_from_slice(&123_456_u64.to_le_bytes());
        frame.extend_from_slice(&0_i64.to_le_bytes());
        frame.extend_from_slice(&0_i32.to_le_bytes());
        frame.extend_from_slice(&(path.len() as u32).to_le_bytes());
        frame.extend_from_slice(path);
        frame
    }

    struct InterruptOnce<'a> {
        bytes: &'a [u8],
        interrupted: bool,
    }

    impl Read for InterruptOnce<'_> {
        fn read(&mut self, output: &mut [u8]) -> io::Result<usize> {
            if !self.interrupted {
                self.interrupted = true;
                return Err(io::ErrorKind::Interrupted.into());
            }
            self.bytes.read(output)
        }
    }

    #[test]
    fn wire_rejects_truncation_oversize_and_malformed_outcome() {
        let complete = frame_bytes(b's', b"/tmp/input");
        for length in 1..complete.len() {
            assert!(read_frame(&mut &complete[..length]).is_err(), "{length}");
        }
        let mut oversized = frame_bytes(b's', b"");
        oversized[46..50].copy_from_slice(&4097_u32.to_le_bytes());
        assert!(read_frame(&mut oversized.as_slice()).is_err());
        let mut invalid = frame_bytes(b'e', b"");
        invalid[34..42].copy_from_slice(&(-1_i64).to_le_bytes());
        assert!(read_frame(&mut invalid.as_slice()).is_err());
        let with_nul = frame_bytes(b's', b"/tmp/a\0b");
        assert!(read_frame(&mut with_nul.as_slice()).is_err());
        assert!(read_frame(&mut frame_bytes(b'h', b"").as_slice()).is_err());
        let mut hello_with_path = frame_bytes(b'h', b"/tmp/input");
        hello_with_path[1] = 0;
        assert!(read_frame(&mut hello_with_path.as_slice()).is_err());
        let mut hello = frame_bytes(b'h', b"");
        hello[1] = 0;
        let frame = read_frame(&mut hello.as_slice()).unwrap().unwrap();
        let mut ledger = FrameLedger::new(2, 256);
        ledger.push(frame).unwrap();
        assert!(ledger.finish().unwrap().hello_pids.contains(&123));
        let bytes = frame_bytes(b's', b"/tmp/input");
        assert!(read_frame(&mut InterruptOnce {
            bytes: &bytes,
            interrupted: false,
        })
        .unwrap()
        .is_some());
    }

    #[test]
    fn ledger_rejects_unpaired_and_duplicate_operations() {
        let start = read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
            .unwrap()
            .unwrap();
        let mut completion = read_frame(&mut frame_bytes(b'e', b"").as_slice())
            .unwrap()
            .unwrap();
        let mut ledger = FrameLedger::new(2, 256);
        assert!(ledger.push(completion.clone()).is_err());
        ledger.push(start.clone()).unwrap();
        assert!(ledger.finish().is_err());
        let mut ledger = FrameLedger::new(2, 256);
        ledger.push(start.clone()).unwrap();
        assert!(ledger.push(start).is_err());
        let mut ledger = FrameLedger::new(2, 256);
        completion.monotonic_ns -= 1;
        ledger
            .push(
                read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
                    .unwrap()
                    .unwrap(),
            )
            .unwrap();
        assert!(ledger.push(completion).is_err());
    }

    #[test]
    fn hello_does_not_consume_the_operation_event_limit() {
        let mut hello = frame_bytes(b'h', b"");
        hello[1] = 0;
        let hello = read_frame(&mut hello.as_slice()).unwrap().unwrap();
        let start = read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
            .unwrap()
            .unwrap();
        let completion = read_frame(&mut frame_bytes(b'e', b"").as_slice())
            .unwrap()
            .unwrap();
        let mut ledger = FrameLedger::new(2, 256);
        ledger.push(hello).unwrap();
        ledger.push(start).unwrap();
        ledger.push(completion).unwrap();
        let result = ledger.finish().unwrap();
        assert_eq!(result.pairs.len(), 1);
        assert_eq!(result.pairs[0].0.sequence, 2);
        assert_eq!(result.pairs[0].1.sequence, 3);
    }

    #[test]
    fn receiver_reports_corrupt_injected_input() {
        let receiver = OperationReceiver::bind(2, 256).unwrap();
        let mut stream = UnixStream::connect(receiver.socket_path()).unwrap();
        stream.write_all(&frame_bytes(b'x', b"/tmp/input")).unwrap();
        drop(stream);
        let deadline = Instant::now() + Duration::from_secs(2);
        while !receiver.failed() && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(10));
        }
        assert!(receiver.failed());
        assert!(receiver.finish().is_err());
    }

    #[test]
    fn injected_child_reports_actual_read_results() {
        let directory = tempfile::Builder::new()
            .prefix("clibox-fspy-mac-")
            .tempdir_in("/tmp")
            .unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let receiver =
            OperationReceiver::bind_for_root(directory.path(), 1_000_000, 256 * 1024 * 1024)
                .unwrap();
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_SOCKET", receiver.socket_path().as_os_str())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .env("CLIBOX_FSPY_TEST_DESCENDANT", "1")
            .stdout(Stdio::null())
            .stderr(Stdio::inherit());
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        let child = runtime
            .block_on(command.spawn(CancellationToken::new()))
            .unwrap();
        let root_pid = child.root_pid;
        let status = runtime.block_on(child.wait_handle).unwrap();
        assert!(status.status.success(), "{:?}", status.status);
        let collected = receiver.finish().unwrap();
        assert!(collected.hello_pids.contains(&root_pid));
        let pairs = collected.pairs;
        let frames = pairs
            .iter()
            .flat_map(|(start, completion)| [start, completion])
            .collect::<Vec<_>>();
        assert!(status.path_accesses.is_ok(), "frames: {frames:?}");
        assert!(!pairs.is_empty());
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Start
                && frame.operation == 3
                && frame.path.ends_with(b"input.txt")
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Completion && frame.operation == 3 && frame.result == 7
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Completion && frame.operation == 7 && frame.result >= 0
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Completion && frame.operation == 8 && frame.result >= 0
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Completion && frame.operation == 9 && frame.result == 0
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Completion && frame.operation == 5 && frame.result == 7
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Completion && frame.operation == 6 && frame.result == 3
        }));
        assert!(pairs.iter().any(|(start, completion)| {
            start.operation == 7 && start.path.ends_with(b"alias.txt") && completion.result == 9
        }));
        assert!(!frames.iter().any(|frame| {
            frame.kind == FrameKind::Start
                && matches!(frame.operation, 2..=6)
                && frame.path.is_empty()
        }));
        assert!(frames.iter().any(|frame| {
            frame.kind == FrameKind::Start
                && frame.pid != root_pid
                && frame.parent_pid == root_pid
                && frame.operation == 3
                && frame.path.ends_with(b"input.txt")
        }));
        let record = assemble_candidate_record(
            directory.path(),
            pairs,
            status.status,
            1_000_000,
            256 * 1024 * 1024,
        )
        .unwrap();
        assert!(record.operations.iter().any(|pair| {
            pair.start.operation == crate::record::Operation::PositionalRead
                && pair.completion.byte_count == Some(7)
        }));
        assert!(record.operations.iter().any(|pair| {
            pair.start.pid != root_pid
                && pair.start.parent_pid == Some(root_pid)
                && pair.start.operation == crate::record::Operation::Read
                && pair.completion.byte_count == Some(7)
        }));
    }

    #[test]
    #[expect(
        clippy::zombie_processes,
        reason = "this fixture deliberately leaves a descendant running after root exit to test \
                  owned-group cleanup"
    )]
    fn read_fixture_child() {
        let Some(path) = std::env::var_os("CLIBOX_FSPY_TEST_INPUT") else {
            return;
        };
        if std::env::var_os("CLIBOX_FSPY_TEST_SLEEP").is_some() {
            std::thread::sleep(Duration::from_secs(5));
            return;
        }
        if std::env::var_os("CLIBOX_FSPY_TEST_ORPHAN").is_some() {
            std::process::Command::new(std::env::current_exe().unwrap())
                .args(["--exact", "macos::tests::read_fixture_child"])
                .env_remove("CLIBOX_FSPY_TEST_ORPHAN")
                .env("CLIBOX_FSPY_TEST_SLEEP", "1")
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn()
                .unwrap();
            return;
        }
        let path = PathBuf::from(path);
        assert_eq!(fs::read(&path).unwrap(), b"fixture");
        if std::env::var_os("CLIBOX_FSPY_TEST_DESCENDANT").as_deref()
            == Some(std::ffi::OsStr::new("2"))
        {
            return;
        }
        let alias = path.with_file_name("alias.txt");
        symlink("input.txt", &alias).unwrap();
        let native_alias = std::ffi::CString::new(alias.as_os_str().as_bytes()).unwrap();
        let native_path = std::ffi::CString::new(path.as_os_str().as_bytes()).unwrap();
        // SAFETY: both NUL-terminated paths and the output buffer are live.
        assert_eq!(unsafe { libc::access(native_path.as_ptr(), libc::R_OK) }, 0);
        let mut target = [0_u8; 32];
        assert_eq!(
            unsafe {
                libc::readlink(
                    native_alias.as_ptr(),
                    target.as_mut_ptr().cast(),
                    target.len(),
                )
            },
            9
        );
        assert_eq!(&target[..9], b"input.txt");
        // SAFETY: the path and mode are valid C strings, and fclose receives
        // only the non-null stream returned by fopen.
        let stream = unsafe { libc::fopen(native_path.as_ptr(), c"rb".as_ptr()) };
        assert!(!stream.is_null());
        assert_eq!(unsafe { libc::fclose(stream) }, 0);
        let mut pipe = [0_i32; 2];
        // SAFETY: pipe points to two writable descriptor slots.
        assert_eq!(unsafe { libc::pipe(pipe.as_mut_ptr()) }, 0);
        let mut pipe_byte = 0_u8;
        // SAFETY: the descriptors belong to this process and the byte lives
        // through both calls.
        assert_eq!(unsafe { libc::write(pipe[1], b"p".as_ptr().cast(), 1) }, 1);
        assert_eq!(
            unsafe { libc::read(pipe[0], (&raw mut pipe_byte).cast(), 1) },
            1
        );
        assert_eq!(pipe_byte, b'p');
        // SAFETY: both descriptors remain open after the completed transfer.
        unsafe {
            libc::close(pipe[0]);
            libc::close(pipe[1]);
        }
        let file = fs::File::open(&path).unwrap();
        let mut buffer = [0_u8; 7];
        let vector = libc::iovec {
            iov_base: buffer.as_mut_ptr().cast(),
            iov_len: buffer.len(),
        };
        // SAFETY: the descriptor and vector point to live buffers.
        assert_eq!(
            unsafe { libc::preadv(file.as_raw_fd(), &raw const vector, 1, 0) },
            7
        );
        assert_eq!(&buffer, b"fixture");
        let output = path.with_extension("output");
        let file = fs::File::create(&output).unwrap();
        let bytes = b"new";
        let vector = libc::iovec {
            iov_base: bytes.as_ptr().cast_mut().cast(),
            iov_len: bytes.len(),
        };
        // SAFETY: the descriptor and vector point to live buffers.
        assert_eq!(
            unsafe { libc::pwritev(file.as_raw_fd(), &raw const vector, 1, 0) },
            3
        );
        assert_eq!(fs::metadata(&path).unwrap().len(), 7);
        assert!(fs::read_dir(path.parent().unwrap()).unwrap().count() > 0);
        let status = std::process::Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "macos::tests::read_fixture_child"])
            .env("CLIBOX_FSPY_TEST_DESCENDANT", "2")
            .status()
            .unwrap();
        assert!(status.success());
        fs::rename(&path, path.with_extension("moved")).unwrap();
    }
}
