//! Private Windows paired-operation receiver for injected NT detours.

use std::{
    collections::{HashMap, HashSet},
    ffi::OsString,
    fs,
    io::{self, Read, Write},
    net::{Ipv4Addr, SocketAddr, TcpListener, TcpStream},
    os::windows::ffi::OsStringExt,
    path::{Component, Path, PathBuf},
    sync::{
        atomic::{AtomicBool, AtomicU64, Ordering},
        Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};

use crate::record::{
    self, AccessPath, Backend, CompleteRecord, Completion, CoverageBoundary, FileIdentity, Header,
    NativePath, Operation, OperationPair, PathClass, Platform, Start, Summary, SCHEMA_VERSION,
};

pub mod supervise;

const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;
const MAX_CONNECTIONS: usize = 4096;
const POLL: Duration = Duration::from_millis(10);

fn invalid(reason: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, reason)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FrameKind {
    Hello,
    Start,
    Completion,
}

#[derive(Debug, Clone, Copy)]
pub enum Admission {
    Proceed(Duration),
    Quit,
}

#[derive(Debug, Clone)]
pub struct Frame {
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
    pub sequence: u64,
    pub access_path: Option<AccessPath>,
    pub requested_delay_ns: u64,
    pub observed_delay_ns: u64,
}

pub fn read_frame(reader: &mut impl Read) -> io::Result<Option<Frame>> {
    let mut header = [0_u8; HEADER_BYTES];
    match reader.read(&mut header[..1])? {
        0 => return Ok(None),
        1 => reader.read_exact(&mut header[1..])?,
        _ => unreachable!(),
    }
    let kind = match header[0] {
        b'h' => FrameKind::Hello,
        b's' => FrameKind::Start,
        b'e' => FrameKind::Completion,
        value => {
            eprintln!("clibox fspy receiver: stage=frame_kind value={value}");
            return Err(invalid("frame_kind"));
        }
    };
    let operation = header[1];
    let pid = u32::from_le_bytes(header[2..6].try_into().unwrap());
    let parent_pid = u32::from_le_bytes(header[6..10].try_into().unwrap());
    let tid = u64::from_le_bytes(header[10..18].try_into().unwrap());
    let id = u64::from_le_bytes(header[18..26].try_into().unwrap());
    let monotonic_ns = u64::from_le_bytes(header[26..34].try_into().unwrap());
    let result = i64::from_le_bytes(header[34..42].try_into().unwrap());
    let error = i32::from_le_bytes(header[42..46].try_into().unwrap());
    let length = u32::from_le_bytes(header[46..50].try_into().unwrap()) as usize;
    if length > MAX_PATH_BYTES || pid == 0 || tid == 0 {
        return Err(invalid("frame_boundary"));
    }
    match kind {
        FrameKind::Hello
            if operation != 0 || id != 0 || result != 0 || error != 0 || length != 32 =>
        {
            return Err(invalid("hello_frame"))
        }
        FrameKind::Start
            if operation_id(operation).is_none()
                || id == 0
                || result != 0
                || error != 0
                || !length.is_multiple_of(2) =>
        {
            return Err(invalid("start_frame"))
        }
        FrameKind::Completion
            if operation_id(operation).is_none()
                || id == 0
                || length != 0
                || ((result < 0) != (error != 0)) =>
        {
            return Err(invalid("completion_frame"))
        }
        _ => {}
    }
    let mut path = vec![0_u8; length];
    reader.read_exact(&mut path)?;
    Ok(Some(Frame {
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
        sequence: 0,
        access_path: None,
        requested_delay_ns: 0,
        observed_delay_ns: 0,
    }))
}

pub(crate) fn operation_id(value: u8) -> Option<Operation> {
    Some(match value {
        1 => Operation::Open,
        2 => Operation::Close,
        3 => Operation::Read,
        4 => Operation::Write,
        5 => Operation::PositionalRead,
        6 => Operation::PositionalWrite,
        7 => Operation::Metadata,
        8 => Operation::Directory,
        9 => Operation::Mutation,
        10 => Operation::Exec,
        _ => return None,
    })
}

fn native_path(bytes: &[u8]) -> io::Result<NativePath> {
    if !bytes.len().is_multiple_of(2) {
        return Err(invalid("windows_path_length"));
    }
    let units = bytes
        .chunks_exact(2)
        .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
        .collect::<Vec<_>>();
    if units.contains(&0) {
        return Err(invalid("windows_path_nul"));
    }
    Ok(NativePath::WindowsUtf16(units))
}

fn fs_path(units: &[u16]) -> PathBuf {
    if units.starts_with(&[b'\\' as u16, b'?' as u16, b'?' as u16, b'\\' as u16]) {
        let mut normalized = vec![b'\\' as u16, b'\\' as u16, b'?' as u16, b'\\' as u16];
        normalized.extend_from_slice(&units[4..]);
        return PathBuf::from(OsString::from_wide(&normalized));
    }
    PathBuf::from(OsString::from_wide(units))
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

fn resolve_even_if_absent(path: &Path) -> io::Result<PathBuf> {
    let mut cursor = path;
    let mut tail = Vec::<OsString>::new();
    loop {
        match fs::canonicalize(cursor) {
            Ok(mut resolved) => {
                for component in tail.iter().rev() {
                    resolved.push(component);
                }
                return Ok(normalize(&resolved));
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
            Err(error) => return Err(error),
        }
    }
}

fn classify_path(root: &Path, bytes: &[u8]) -> io::Result<AccessPath> {
    let logical = native_path(bytes)?;
    let NativePath::WindowsUtf16(units) = &logical else {
        unreachable!()
    };
    let path = fs_path(units);
    if !path.is_absolute() {
        return Err(invalid("non_absolute_path"));
    }
    let resolved = resolve_even_if_absent(&path)?;
    let relative = resolved.strip_prefix(root).ok();
    let identity = file_id::get_file_id(&path).ok().map(FileIdentity::from);
    use std::os::windows::ffi::OsStrExt;
    Ok(AccessPath {
        class: if relative.is_some() {
            PathClass::Project
        } else {
            PathClass::External
        },
        logical,
        resolved: Some(NativePath::WindowsUtf16(
            resolved.as_os_str().encode_wide().collect(),
        )),
        project_relative: relative.map(|path| {
            let units = path.as_os_str().encode_wide().collect::<Vec<_>>();
            NativePath::WindowsUtf16(if units.is_empty() {
                vec![b'.' as u16]
            } else {
                units
            })
        }),
        identity,
    })
}

#[derive(Default)]
struct Ledger {
    starts: HashMap<(u32, u64, u64), Frame>,
    pairs: Vec<(Frame, Frame)>,
    hello_pids: HashSet<u32>,
    event_count: usize,
    byte_count: u64,
}

impl Ledger {
    fn push(
        &mut self,
        frame: Frame,
        root: &Path,
        max_events: usize,
        max_bytes: u64,
    ) -> io::Result<()> {
        self.byte_count = self
            .byte_count
            .checked_add((HEADER_BYTES + frame.path.len()) as u64)
            .ok_or_else(|| invalid("byte_limit"))?;
        if self.byte_count > max_bytes {
            return Err(invalid("byte_limit"));
        }
        match frame.kind {
            FrameKind::Hello => {
                self.hello_pids.insert(frame.pid);
            }
            FrameKind::Start => {
                self.event_count += 1;
                if self.event_count > max_events {
                    return Err(invalid("event_limit"));
                }
                let mut frame = frame;
                if !frame.path.is_empty() && frame.access_path.is_none() {
                    frame.access_path = Some(classify_path(root, &frame.path)?);
                }
                let key = (frame.pid, frame.tid, frame.id);
                if self.starts.insert(key, frame).is_some() {
                    return Err(invalid("duplicate_start"));
                }
            }
            FrameKind::Completion => {
                self.event_count += 1;
                if self.event_count > max_events {
                    return Err(invalid("event_limit"));
                }
                let key = (frame.pid, frame.tid, frame.id);
                let start = self
                    .starts
                    .remove(&key)
                    .ok_or_else(|| invalid("unpaired_completion"))?;
                if start.operation != frame.operation
                    || start.parent_pid != frame.parent_pid
                    || frame.monotonic_ns < start.monotonic_ns
                {
                    return Err(invalid("mismatched_completion"));
                }
                self.pairs.push((start, frame));
            }
        }
        Ok(())
    }
}

pub struct CollectedOperations {
    pub pairs: Vec<(Frame, Frame)>,
    pub hello_pids: HashSet<u32>,
}

struct ReceiveContext<F> {
    root: PathBuf,
    token: String,
    ledger: Arc<Mutex<Ledger>>,
    sequence: Arc<AtomicU64>,
    stop: Arc<AtomicBool>,
    max_events: usize,
    max_bytes: u64,
    admission: Arc<F>,
}

struct RetryRead<'a> {
    stream: &'a mut TcpStream,
    stopping: &'a AtomicBool,
    consumed: bool,
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
                Ok(length) => {
                    self.consumed |= length != 0;
                    return Ok(length);
                }
                result => return result,
            }
        }
    }
}

fn receive_connection<F>(mut stream: TcpStream, context: &ReceiveContext<F>) -> io::Result<()>
where
    F: Fn(&Frame) -> Admission,
{
    stream.set_read_timeout(Some(Duration::from_millis(100)))?;
    stream.set_write_timeout(Some(Duration::from_secs(5)))?;
    let hello = read_frame(&mut stream)?.ok_or_else(|| invalid("missing_hello"))?;
    if hello.kind != FrameKind::Hello || hello.path != context.token.as_bytes() {
        return Err(invalid("invalid_hello"));
    }
    context
        .ledger
        .lock()
        .map_err(|_| invalid("collector_lock"))?
        .push(hello, &context.root, context.max_events, context.max_bytes)?;
    stream.write_all(b"g")?;
    loop {
        // Keep the framing cursor across short socket timeouts. Retrying a
        // partially read frame at its next boundary would interpret UTF-16
        // path bytes as a new header and silently lose observations.
        let mut reader = RetryRead {
            stream: &mut stream,
            stopping: &context.stop,
            consumed: false,
        };
        let mut frame = match read_frame(&mut reader) {
            Ok(Some(frame)) => frame,
            Ok(None) => return Ok(()),
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
                ) && !reader.consumed =>
            {
                if context.stop.load(Ordering::Acquire) {
                    return Ok(());
                }
                continue;
            }
            Err(error) => return Err(error),
        };
        frame.sequence = context.sequence.fetch_add(1, Ordering::AcqRel) + 1;
        if frame.kind == FrameKind::Hello || frame.pid == 0 {
            return Err(invalid("unexpected_hello"));
        }
        if frame.kind == FrameKind::Start {
            if !frame.path.is_empty() {
                frame.access_path = Some(classify_path(&context.root, &frame.path)?);
            }
            let began = Instant::now();
            let requested = match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                (context.admission)(&frame)
            }))
            .map_err(|_| invalid("admission_policy"))?
            {
                Admission::Proceed(delay) => delay,
                Admission::Quit => {
                    stream.write_all(b"q")?;
                    return Ok(());
                }
            };
            frame.requested_delay_ns =
                u64::try_from(requested.as_nanos()).map_err(|_| invalid("delay_limit"))?;
            if !requested.is_zero() {
                thread::sleep(requested);
            }
            frame.observed_delay_ns =
                u64::try_from(began.elapsed().as_nanos()).map_err(|_| invalid("delay_limit"))?;
            if requested.is_zero() {
                frame.observed_delay_ns = 0;
            }
        }
        let start = frame.kind == FrameKind::Start;
        context
            .ledger
            .lock()
            .map_err(|_| invalid("collector_lock"))?
            .push(frame, &context.root, context.max_events, context.max_bytes)?;
        if start {
            stream.write_all(b"g")?;
        }
    }
}

pub struct OperationReceiver {
    address: SocketAddr,
    token: String,
    stop: Arc<AtomicBool>,
    failed: Arc<AtomicBool>,
    receiver: Option<thread::JoinHandle<io::Result<CollectedOperations>>>,
}

impl OperationReceiver {
    pub fn bind<F>(root: &Path, max_events: usize, max_bytes: u64, delay_for: F) -> io::Result<Self>
    where
        F: Fn(&Frame) -> Duration + Send + Sync + 'static,
    {
        Self::bind_with_admission(root, max_events, max_bytes, move |frame| {
            Admission::Proceed(delay_for(frame))
        })
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
        if max_events == 0 || max_bytes == 0 {
            return Err(invalid("receiver_limit"));
        }
        let root = fs::canonicalize(root)?;
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0))?;
        listener.set_nonblocking(true)?;
        let address = listener.local_addr()?;
        let token = uuid::Uuid::now_v7().simple().to_string();
        let worker_token = token.clone();
        let stop = Arc::new(AtomicBool::new(false));
        let failed = Arc::new(AtomicBool::new(false));
        let worker_stop = Arc::clone(&stop);
        let worker_failed = Arc::clone(&failed);
        let admission = Arc::new(admission);
        let receiver = thread::spawn(move || {
            let ledger = Arc::new(Mutex::new(Ledger::default()));
            let sequence = Arc::new(AtomicU64::new(0));
            let mut connections = Vec::new();
            let mut idle_after_stop = 0;
            loop {
                match listener.accept() {
                    Ok((stream, _)) => {
                        if connections.len() >= MAX_CONNECTIONS {
                            worker_failed.store(true, Ordering::Release);
                            return Err(invalid("connection_limit"));
                        }
                        idle_after_stop = 0;
                        let failure = Arc::clone(&worker_failed);
                        let context = ReceiveContext {
                            root: root.clone(),
                            token: worker_token.clone(),
                            ledger: Arc::clone(&ledger),
                            sequence: Arc::clone(&sequence),
                            stop: Arc::clone(&worker_stop),
                            max_events,
                            max_bytes,
                            admission: Arc::clone(&admission),
                        };
                        connections.push(thread::spawn(move || {
                            let result = receive_connection(stream, &context);
                            if let Err(error) = &result {
                                eprintln!(
                                    "clibox fspy receiver: stage=connection kind={:?} \
                                     reason={error}",
                                    error.kind()
                                );
                                failure.store(true, Ordering::Release);
                            }
                            result
                        }));
                    }
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                        if worker_stop.load(Ordering::Acquire) {
                            idle_after_stop += 1;
                            if idle_after_stop >= 2 {
                                break;
                            }
                        }
                        thread::sleep(POLL);
                    }
                    Err(error) => {
                        worker_failed.store(true, Ordering::Release);
                        return Err(error);
                    }
                }
            }
            for connection in connections {
                connection.join().map_err(|_| invalid("receiver_panic"))??;
            }
            let ledger = Arc::try_unwrap(ledger)
                .map_err(|_| invalid("receiver_references"))?
                .into_inner()
                .map_err(|_| invalid("collector_lock"))?;
            if !ledger.starts.is_empty() {
                eprintln!(
                    "clibox fspy receiver: stage=unpaired_start count={}",
                    ledger.starts.len()
                );
                return Err(invalid("unpaired_start"));
            }
            Ok(CollectedOperations {
                pairs: ledger.pairs,
                hello_pids: ledger.hello_pids,
            })
        });
        Ok(Self {
            address,
            token,
            stop,
            failed,
            receiver: Some(receiver),
        })
    }

    pub fn address(&self) -> SocketAddr {
        self.address
    }

    pub fn token(&self) -> &str {
        &self.token
    }

    pub fn failed(&self) -> bool {
        self.failed.load(Ordering::Acquire)
    }

    pub fn finish(mut self) -> io::Result<CollectedOperations> {
        self.stop.store(true, Ordering::Release);
        self.receiver
            .take()
            .expect("receiver exists")
            .join()
            .map_err(|_| invalid("receiver_panic"))?
    }
}

impl Drop for OperationReceiver {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
    }
}

pub fn assemble_candidate_record(
    root: &Path,
    pairs: Vec<(Frame, Frame)>,
    exit_code: Option<i64>,
    max_events: usize,
    max_bytes: u64,
) -> io::Result<CompleteRecord> {
    use std::os::windows::ffi::OsStrExt;
    let root = fs::canonicalize(root)?;
    let mut operations = Vec::with_capacity(pairs.len());
    for (start, completion) in pairs {
        let kind = operation_id(start.operation).ok_or_else(|| invalid("operation_kind"))?;
        let path_unavailable = start.path.is_empty();
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
            platform: Platform::Windows,
            backend: Backend::Injection,
            root: NativePath::WindowsUtf16(root.as_os_str().encode_wide().collect()),
            coverage: CoverageBoundary::SynchronousFileOperationsV1,
        },
        summary: Summary {
            complete: true,
            child_exit_code: exit_code,
            child_signal: None,
            operation_count: operations.len() as u64,
            failure_count,
            failure: None,
        },
        operations,
    };
    let mut bytes = Vec::new();
    record::serialize(&record, &mut bytes, max_events, max_bytes)
        .map_err(|_| invalid("record_limit"))?;
    record::parse(io::BufReader::new(bytes.as_slice()), max_events, max_bytes)
        .map_err(|_| invalid("candidate_record"))
}
