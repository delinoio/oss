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
        fs::MetadataExt,
        net::{UnixListener, UnixStream},
        process::ExitStatusExt,
    },
    path::{Component, Path, PathBuf},
    process::ExitStatus,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex, OnceLock,
    },
    thread,
    time::{Duration, Instant},
};

use crate::record::{
    self, AccessPath, Backend, CompleteRecord, Completion, CoverageBoundary, FileIdentity, Header,
    NativePath, Operation, OperationPair, PathClass, Platform, Start, Summary, SCHEMA_VERSION,
};

const HEADER_BYTES: usize = 66;
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
    /// Native completion result; metadata starts carry the closed final-symlink
    /// policy.
    pub result: i64,
    pub error: i32,
    pub path: Vec<u8>,
    pub access_path: Option<AccessPath>,
    pub identity: Option<FileIdentity>,
    pub requested_delay_ns: u64,
    pub observed_delay_ns: u64,
    /// A per-process-image nonce inherited by every thread connection.
    pub image_id: Option<(u64, u64)>,
}

fn invalid(reason: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, reason)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReceiverFailure {
    EventLimit,
    ByteLimit,
    TraceLoss,
}

impl ReceiverFailure {
    pub fn from_error(error: &io::Error) -> Self {
        error
            .get_ref()
            .and_then(|cause| cause.downcast_ref::<Self>())
            .copied()
            .unwrap_or(Self::TraceLoss)
    }

    fn into_error(self) -> io::Error {
        io::Error::new(io::ErrorKind::InvalidData, self)
    }
}

impl std::fmt::Display for ReceiverFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::EventLimit => "event_limit",
            Self::ByteLimit => "byte_limit",
            Self::TraceLoss => "trace_loss",
        })
    }
}

impl std::error::Error for ReceiverFailure {}

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
        || (kind != FrameKind::Hello && !(1..=11).contains(&operation))
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
    let device = u64::from_le_bytes(header[50..58].try_into().expect("fixed header"));
    let inode = u64::from_le_bytes(header[58..66].try_into().expect("fixed header"));
    let identity = (device != 0 || inode != 0).then_some(FileIdentity::Inode { device, inode });
    if length > MAX_PATH_BYTES
        || pid == 0
        || tid == 0
        || id == 0
        || monotonic_ns == 0
        || (kind == FrameKind::Hello && error != 0)
        || (kind == FrameKind::Start
            && (error != 0
                || (result != 0
                    && !((operation == 1 || operation == 7) && result == 1)
                    && !((operation == 3 || operation == 5) && result >= -1))))
        || (kind == FrameKind::Hello && length != 0)
        || (kind != FrameKind::Start && identity.is_some())
        || (identity.is_some() && !(2..=9).contains(&operation))
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
        identity,
        requested_delay_ns: 0,
        observed_delay_ns: 0,
        image_id: (kind == FrameKind::Hello).then_some((id, result as u64)),
    }))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Admission {
    Proceed(Duration),
    Quit,
}

type AdmissionPolicy = dyn Fn(&Frame, &AtomicBool) -> Admission + Send + Sync;

#[derive(Default)]
pub struct FrameLedger {
    pending: HashMap<(u32, u64, u64), Frame>,
    completed: Vec<(Frame, Frame)>,
    hello_pids: HashSet<u32>,
    replacing: HashMap<u32, (u32, u64, u64)>,
    frame_count: u64,
    event_count: usize,
    max_events: usize,
    max_bytes: u64,
    retained_bytes: u64,
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
                .ok_or_else(|| ReceiverFailure::EventLimit.into_error())?;
        }
        let charge = record::retained_frame_charge(frame.path.len(), frame.access_path.iter())
            .ok_or_else(|| ReceiverFailure::ByteLimit.into_error())?;
        self.retained_bytes = self
            .retained_bytes
            .checked_add(charge)
            .ok_or_else(|| ReceiverFailure::ByteLimit.into_error())?;
        if self.event_count > self.max_events {
            return Err(ReceiverFailure::EventLimit.into_error());
        }
        if self.retained_bytes > self.max_bytes {
            return Err(ReceiverFailure::ByteLimit.into_error());
        }
        frame.sequence = self.frame_count;
        let key = (frame.pid, frame.tid, frame.id);
        match frame.kind {
            FrameKind::Hello => {
                self.hello_pids.insert(frame.pid);
                if let Some(key) = self.replacing.get(&frame.pid).copied() {
                    let start = self
                        .pending
                        .get(&key)
                        .ok_or_else(|| invalid("missing_exec"))?;
                    let old_image = start.image_id.ok_or_else(|| invalid("missing_image"))?;
                    if frame.image_id != Some(old_image) {
                        let mut completion = start.clone();
                        completion.kind = FrameKind::Completion;
                        completion.monotonic_ns = frame.monotonic_ns;
                        completion.result = 0;
                        completion.error = 0;
                        completion.path.clear();
                        completion.access_path = None;
                        self.push(completion)?;
                    }
                }
            }
            FrameKind::Start => {
                if frame.operation == 11 && frame.image_id.is_none() {
                    return Err(invalid("missing_image"));
                }
                if frame.operation == 11 && self.replacing.insert(frame.pid, key).is_some() {
                    return Err(invalid("duplicate_exec"));
                }
                if self.pending.insert(key, frame).is_some() {
                    return Err(invalid("duplicate_start"));
                }
            }
            FrameKind::Completion => {
                if frame.operation == 11 && self.replacing.remove(&frame.pid) != Some(key) {
                    return Err(invalid("missing_exec"));
                }
                let start = self
                    .pending
                    .remove(&key)
                    .ok_or_else(|| invalid("unpaired_completion"))?;
                if start.operation != frame.operation || frame.monotonic_ns < start.monotonic_ns {
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

#[derive(Clone, Copy, PartialEq, Eq)]
struct ProcessIdentity {
    started_seconds: u64,
    started_microseconds: u64,
}

fn process_state(pid: u32) -> io::Result<Option<(ProcessIdentity, u32)>> {
    let pid = i32::try_from(pid).map_err(|_| invalid("process_id"))?;
    let mut info = std::mem::MaybeUninit::<libc::proc_bsdinfo>::uninit();
    let size = i32::try_from(std::mem::size_of::<libc::proc_bsdinfo>())
        .map_err(|_| invalid("process_info_size"))?;
    // SAFETY: proc_pidinfo writes at most size bytes into the matching buffer.
    let read = unsafe {
        libc::proc_pidinfo(
            pid,
            libc::PROC_PIDTBSDINFO,
            0,
            info.as_mut_ptr().cast(),
            size,
        )
    };
    if read == 0 {
        // proc_pidinfo may return zero for both an exited process and a query
        // failure. Only a confirmed absent PID is safe to ignore.
        if unsafe { libc::kill(pid, 0) } == -1
            && io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return Ok(None);
        }
        return Err(invalid("process_info"));
    }
    if read != size {
        return Err(invalid("process_info_size"));
    }
    // SAFETY: the kernel filled the entire structure when read == size.
    let info = unsafe { info.assume_init() };
    if info.pbi_pid != pid as u32 {
        return Err(invalid("process_identity"));
    }
    Ok(Some((
        ProcessIdentity {
            started_seconds: info.pbi_start_tvsec,
            started_microseconds: info.pbi_start_tvusec,
        },
        info.pbi_status,
    )))
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
    processes: &Mutex<HashMap<u32, ProcessIdentity>>,
    stopping: &AtomicBool,
    root: Option<&Path>,
    admission: &AdmissionPolicy,
) -> io::Result<()> {
    use std::io::Write;

    stream.set_nonblocking(false)?;
    stream.set_read_timeout(Some(Duration::from_millis(100)))?;
    let mut image_id = None;
    let mut peer = None;
    loop {
        let mut reader = RetryRead {
            stream: &mut stream,
            stopping,
        };
        let Some(mut frame) = read_frame(&mut reader)? else {
            return Ok(());
        };
        if let Some((pid, tid)) = peer {
            if frame.kind == FrameKind::Hello || frame.pid != pid || frame.tid != tid {
                return Err(invalid("connection_identity"));
            }
            frame.image_id = image_id;
        } else if frame.kind == FrameKind::Hello {
            if root.is_some() {
                let (identity, _) =
                    process_state(frame.pid)?.ok_or_else(|| invalid("hello_process_exited"))?;
                let mut tracked = processes.lock().map_err(|_| invalid("process_lock"))?;
                if tracked
                    .insert(frame.pid, identity)
                    .is_some_and(|prior| prior != identity)
                {
                    return Err(invalid("process_identity_changed"));
                }
            }
            peer = Some((frame.pid, frame.tid));
            image_id = frame.image_id;
        } else {
            return Err(invalid("missing_hello"));
        }
        let acknowledge = frame.kind != FrameKind::Completion;
        let start = frame.kind == FrameKind::Start;
        if start && !frame.path.is_empty() {
            if let Some(root) = root {
                let policy = if frame.operation == 7 && frame.result == 1 {
                    FinalSymlink::NoFollow
                } else {
                    FinalSymlink::Follow
                };
                frame.access_path =
                    classify_path_with_identity(root, &frame.path, frame.identity, policy)?;
            }
        }
        if start {
            let decision = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                admission(&frame, stopping)
            }))
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
        if acknowledge {
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
    failure: Arc<OnceLock<ReceiverFailure>>,
    processes: Arc<Mutex<HashMap<u32, ProcessIdentity>>>,
    receiver: Option<thread::JoinHandle<io::Result<CollectedOperations>>>,
}

type FramePairs = Vec<(Frame, Frame)>;

impl OperationReceiver {
    pub fn bind(max_events: usize, max_bytes: u64) -> io::Result<Self> {
        Self::bind_inner(
            None,
            max_events,
            max_bytes,
            Arc::new(|_, _| Admission::Proceed(Duration::ZERO)),
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
            Arc::new(move |frame, _| Admission::Proceed(delay_for(frame))),
        )
    }

    pub fn bind_with_admission<F>(
        root: &Path,
        max_events: usize,
        max_bytes: u64,
        admission: F,
    ) -> io::Result<Self>
    where
        F: Fn(&Frame, &AtomicBool) -> Admission + Send + Sync + 'static,
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
        if max_bytes == 0 {
            return Err(invalid("receiver_limit"));
        }
        let directory = tempfile::Builder::new().prefix("clibox-fspy-").tempdir()?;
        let socket_path = directory.path().join("operation.sock");
        let listener = UnixListener::bind(&socket_path)?;
        listener.set_nonblocking(true)?;
        let stopping = Arc::new(AtomicBool::new(false));
        let stop = Arc::clone(&stopping);
        let failure = Arc::new(OnceLock::new());
        let first_failure = Arc::clone(&failure);
        let processes = Arc::new(Mutex::new(HashMap::new()));
        let tracked_processes = Arc::clone(&processes);
        let receiver = thread::spawn(move || {
            let ledger = Arc::new(Mutex::new(FrameLedger::new(max_events, max_bytes)));
            let mut connections = Vec::new();
            let mut idle_after_stop = 0;
            loop {
                match listener.accept() {
                    Ok((stream, _)) => {
                        if connections.len() >= MAX_CONNECTIONS {
                            first_failure.get_or_init(|| ReceiverFailure::TraceLoss);
                            stop.store(true, Ordering::Release);
                            drop(stream);
                            break;
                        }
                        idle_after_stop = 0;
                        let ledger = Arc::clone(&ledger);
                        let processes = Arc::clone(&tracked_processes);
                        let stop = Arc::clone(&stop);
                        let failure = Arc::clone(&first_failure);
                        let root = root.clone();
                        let admission = Arc::clone(&admission);
                        connections.push(thread::spawn(move || {
                            let result = receive_connection(
                                stream,
                                &ledger,
                                &processes,
                                &stop,
                                root.as_deref(),
                                admission.as_ref(),
                            );
                            if let Err(error) = &result {
                                failure.get_or_init(|| ReceiverFailure::from_error(error));
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
                    Err(_) => {
                        first_failure.get_or_init(|| ReceiverFailure::TraceLoss);
                        stop.store(true, Ordering::Release);
                        break;
                    }
                }
            }
            for connection in connections {
                let result = connection.join().map_err(|_| invalid("receiver_panic"));
                if let Err(error) = result.and_then(|result| result) {
                    first_failure.get_or_init(|| ReceiverFailure::from_error(&error));
                }
            }
            if let Some(cause) = first_failure.get().copied() {
                // Connection joins occur in acceptance order. Preserve the
                // first observed failure instead of a later shutdown error.
                return Err(cause.into_error());
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
            failure,
            processes,
            receiver: Some(receiver),
        })
    }

    pub fn socket_path(&self) -> &Path {
        &self.socket_path
    }

    pub fn failed(&self) -> bool {
        self.failure().is_some()
    }

    pub fn failure(&self) -> Option<ReceiverFailure> {
        self.failure.get().copied()
    }

    pub fn live_processes(&self) -> io::Result<Vec<u32>> {
        let tracked = self
            .processes
            .lock()
            .map_err(|_| invalid("process_lock"))?
            .clone();
        let mut live = Vec::new();
        for (pid, expected) in tracked {
            if let Some((actual, status)) = process_state(pid)? {
                if actual == expected && status != libc::SZOMB {
                    live.push(pid);
                }
            }
        }
        Ok(live)
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
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::NotFound | io::ErrorKind::NotADirectory
                ) =>
            {
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

#[derive(Clone, Copy)]
enum FinalSymlink {
    Follow,
    NoFollow,
}

pub(super) fn classify_path(root: &Path, bytes: &[u8]) -> io::Result<Option<AccessPath>> {
    classify_path_with_identity(root, bytes, None, FinalSymlink::Follow)
}

fn classify_path_with_identity(
    root: &Path,
    bytes: &[u8],
    descriptor_identity: Option<FileIdentity>,
    policy: FinalSymlink,
) -> io::Result<Option<AccessPath>> {
    let logical = PathBuf::from(OsString::from_vec(bytes.to_vec()));
    if !logical.is_absolute() {
        return Err(invalid("non_absolute_path"));
    }
    // Use the raw terminal component: Path::components removes a trailing slash
    // or dot, whose native lookup still follows the preceding symlink.
    let separator = bytes
        .iter()
        .rposition(|byte| *byte == b'/')
        .expect("absolute path");
    let final_name = &bytes[separator + 1..];
    let nofollow =
        matches!(policy, FinalSymlink::NoFollow) && !matches!(final_name, b"" | b"." | b"..");
    let resolved = if nofollow {
        let parent = Path::new(std::ffi::OsStr::from_bytes(&bytes[..=separator]));
        resolve_even_if_absent(parent)?
            .map(|parent| parent.join(std::ffi::OsStr::from_bytes(final_name)))
    } else {
        resolve_even_if_absent(&logical)?
    };
    let Some(resolved) = resolved else {
        return Ok(None);
    };
    let relative = resolved.strip_prefix(root).ok();
    let identity = descriptor_identity.or_else(|| {
        if nofollow {
            fs::symlink_metadata(&logical)
                .ok()
                .map(|metadata| FileIdentity::Inode {
                    device: metadata.dev(),
                    inode: metadata.ino(),
                })
        } else {
            file_id::get_file_id(&logical).ok().map(FileIdentity::from)
        }
    });
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
        10 => Operation::Exec,
        11 => Operation::Exec,
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
                open_mutates: kind == Operation::Open && start.result == 1,
                paths,
                path_unavailable,
                descriptor: None,
                requested_bytes: (matches!(kind, Operation::Read | Operation::PositionalRead)
                    && start.result >= 0)
                    .then_some(start.result as u64),
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
        match error {
            record::ParseFailure::EventLimit => ReceiverFailure::EventLimit.into_error(),
            record::ParseFailure::ByteLimit => ReceiverFailure::ByteLimit.into_error(),
            _ => invalid("record_limit"),
        }
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
        assemble_candidate_record, classify_path, classify_path_with_identity, read_frame,
        FinalSymlink, FrameKind, FrameLedger, OperationReceiver, ReceiverFailure,
    };

    #[test]
    fn regular_file_ancestor_keeps_failed_probe_classified() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::write(root.join("file"), b"fixture").unwrap();
        let target = root.join("file/child");
        let classified = classify_path(&root, target.as_os_str().as_bytes())
            .unwrap()
            .unwrap();
        assert_eq!(
            classified.project_relative,
            Some(crate::record::NativePath::UnixBytes(b"file/child".to_vec()))
        );
    }

    #[test]
    fn descriptor_identity_survives_path_replacement() {
        use crate::record::FileIdentity;

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let logical = root.join("input");
        fs::write(&logical, b"first").unwrap();
        let original = file_id::get_file_id(&logical).unwrap();
        fs::rename(&logical, root.join("moved")).unwrap();
        fs::write(&logical, b"second").unwrap();
        assert_ne!(original, file_id::get_file_id(&logical).unwrap());
        let observed = classify_path_with_identity(
            &root,
            logical.as_os_str().as_bytes(),
            Some(FileIdentity::from(original)),
            FinalSymlink::Follow,
        )
        .unwrap()
        .unwrap();
        assert_eq!(observed.identity, Some(FileIdentity::from(original)));
    }

    #[test]
    fn nofollow_resolves_parents_and_retains_lossless_final_entry() {
        use std::os::unix::{ffi::OsStringExt, fs::MetadataExt};

        use crate::record::{FileIdentity, NativePath, PathClass};

        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("project");
        fs::create_dir_all(root.join("real")).unwrap();
        fs::create_dir(base.join("external")).unwrap();
        symlink("real", root.join("alias")).unwrap();
        symlink(base.join("external"), root.join("escape")).unwrap();
        let name = std::ffi::OsString::from_vec(b"link-\xc3\xa9".to_vec());
        symlink("missing", root.join("real").join(&name)).unwrap();
        let logical = root.join("alias").join(&name);
        let metadata = fs::symlink_metadata(&logical).unwrap();
        let access = classify_path_with_identity(
            &root,
            logical.as_os_str().as_bytes(),
            None,
            FinalSymlink::NoFollow,
        )
        .unwrap()
        .unwrap();
        assert_eq!(
            access.logical,
            NativePath::UnixBytes(logical.as_os_str().as_bytes().to_vec())
        );
        assert_eq!(
            access.project_relative,
            Some(NativePath::UnixBytes(b"real/link-\xc3\xa9".to_vec()))
        );
        assert_eq!(
            access.identity,
            Some(FileIdentity::Inode {
                device: metadata.dev(),
                inode: metadata.ino()
            })
        );
        // macOS filesystems reject invalid UTF-8 names, but the attempted
        // pathname must still survive classification without lossy decoding.
        let mut invalid_name = root.as_os_str().as_bytes().to_vec();
        invalid_name.extend_from_slice(b"/missing-\xff");
        let access =
            classify_path_with_identity(&root, &invalid_name, None, FinalSymlink::NoFollow)
                .unwrap()
                .unwrap();
        assert_eq!(access.logical, NativePath::UnixBytes(invalid_name));
        assert_eq!(access.identity, None);
        assert_eq!(
            access.project_relative,
            Some(NativePath::UnixBytes(b"missing-\xff".to_vec()))
        );
        symlink("absent", base.join("external/link")).unwrap();
        let access = classify_path_with_identity(
            &root,
            root.join("escape/link").as_os_str().as_bytes(),
            None,
            FinalSymlink::NoFollow,
        )
        .unwrap()
        .unwrap();
        assert_eq!(access.class, PathClass::External);
        assert_eq!(access.project_relative, None);
        for suffix in ["alias/", "alias/.", "alias/.."] {
            let bytes = root.join(suffix).as_os_str().as_bytes().to_vec();
            let following = classify_path(&root, &bytes).unwrap().unwrap();
            let nofollow = classify_path_with_identity(&root, &bytes, None, FinalSymlink::NoFollow)
                .unwrap()
                .unwrap();
            assert_eq!(nofollow, following, "native directory lookup: {suffix}");
        }
    }

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
        frame.extend_from_slice(&[0_u8; 16]);
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
        let mut descriptor = frame_bytes(b's', b"/tmp/input");
        descriptor[1] = 3;
        descriptor[50..58].copy_from_slice(&7_u64.to_le_bytes());
        descriptor[58..66].copy_from_slice(&11_u64.to_le_bytes());
        assert_eq!(
            read_frame(&mut descriptor.as_slice())
                .unwrap()
                .unwrap()
                .identity,
            Some(crate::record::FileIdentity::Inode {
                device: 7,
                inode: 11
            })
        );
        assert!(read_frame(&mut frame_bytes(b'h', b"").as_slice()).is_err());
        let mut hello_with_path = frame_bytes(b'h', b"/tmp/input");
        hello_with_path[1] = 0;
        assert!(read_frame(&mut hello_with_path.as_slice()).is_err());
        let mut hello = frame_bytes(b'h', b"");
        hello[1] = 0;
        let frame = read_frame(&mut hello.as_slice()).unwrap().unwrap();
        let mut ledger = FrameLedger::new(2, 4096);
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
    fn wire_accepts_only_closed_metadata_resolution_values() {
        for policy in [0_i64, 1] {
            let mut bytes = frame_bytes(b's', b"/tmp/link");
            bytes[1] = 7;
            bytes[34..42].copy_from_slice(&policy.to_le_bytes());
            assert_eq!(
                read_frame(&mut bytes.as_slice()).unwrap().unwrap().result,
                policy
            );
        }
        for policy in [-1_i64, 2] {
            let mut bytes = frame_bytes(b's', b"/tmp/link");
            bytes[1] = 7;
            bytes[34..42].copy_from_slice(&policy.to_le_bytes());
            assert!(read_frame(&mut bytes.as_slice()).is_err());
        }
    }

    #[test]
    fn ledger_rejects_unpaired_and_duplicate_operations() {
        let start = read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
            .unwrap()
            .unwrap();
        let mut completion = read_frame(&mut frame_bytes(b'e', b"").as_slice())
            .unwrap()
            .unwrap();
        let mut ledger = FrameLedger::new(2, 4096);
        assert!(ledger.push(completion.clone()).is_err());
        ledger.push(start.clone()).unwrap();
        assert!(ledger.finish().is_err());
        let mut ledger = FrameLedger::new(2, 4096);
        ledger.push(start.clone()).unwrap();
        assert!(ledger.push(start).is_err());
        let mut ledger = FrameLedger::new(2, 4096);
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
    fn classified_paths_exhaust_the_budget_before_retention() {
        use crate::record::{AccessPath, NativePath, PathClass};

        let mut path = vec![b'/'];
        path.extend(std::iter::repeat_n(b'x', 1000));
        let mut frame = read_frame(&mut frame_bytes(b's', &path).as_slice())
            .unwrap()
            .unwrap();
        frame.access_path = Some(AccessPath {
            class: PathClass::Project,
            logical: NativePath::UnixBytes(path.clone()),
            resolved: Some(NativePath::UnixBytes(path.clone())),
            project_relative: Some(NativePath::UnixBytes(path)),
            identity: None,
        });
        let mut ledger = FrameLedger::new(2, 6000);
        let error = ledger.push(frame).unwrap_err();
        assert_eq!(error.to_string(), "byte_limit");
        assert_eq!(
            ReceiverFailure::from_error(&error),
            ReceiverFailure::ByteLimit
        );
        assert!(ledger.pending.is_empty());
    }

    #[test]
    fn ledger_keeps_start_ancestry_after_reparenting() {
        let start = read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
            .unwrap()
            .unwrap();
        let mut completion = read_frame(&mut frame_bytes(b'e', b"").as_slice())
            .unwrap()
            .unwrap();
        completion.parent_pid = 42;
        let mut ledger = FrameLedger::new(2, 4096);
        ledger.push(start).unwrap();
        ledger.push(completion).unwrap();
        let pair = &ledger.finish().unwrap().pairs[0];
        assert_eq!(pair.0.parent_pid, 1);
        assert_eq!(pair.1.parent_pid, 42);
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
        let mut ledger = FrameLedger::new(2, 4096);
        ledger.push(hello).unwrap();
        ledger.push(start).unwrap();
        ledger.push(completion).unwrap();
        let result = ledger.finish().unwrap();
        assert_eq!(result.pairs.len(), 1);
        assert_eq!(result.pairs[0].0.sequence, 2);
        assert_eq!(result.pairs[0].1.sequence, 3);
    }

    #[test]
    fn replacement_hello_completes_the_pending_exec() {
        let mut start = frame_bytes(b's', b"/tmp/tool");
        start[1] = 11;
        let mut start = read_frame(&mut start.as_slice()).unwrap().unwrap();
        start.image_id = Some((789, 0));
        let mut old_hello = frame_bytes(b'h', b"");
        old_hello[1] = 0;
        old_hello[26..34].copy_from_slice(&123_457_u64.to_le_bytes());
        let old_hello = read_frame(&mut old_hello.as_slice()).unwrap().unwrap();
        let mut successor_hello = frame_bytes(b'h', b"");
        successor_hello[1] = 0;
        successor_hello[26..34].copy_from_slice(&123_458_u64.to_le_bytes());
        successor_hello[34..42].copy_from_slice(&1_i64.to_le_bytes());
        let successor_hello = read_frame(&mut successor_hello.as_slice())
            .unwrap()
            .unwrap();
        let mut ledger = FrameLedger::new(2, 4096);
        ledger.push(start).unwrap();
        ledger.push(old_hello).unwrap();
        assert_eq!(ledger.pending.len(), 1);
        assert!(ledger.completed.is_empty());
        ledger.push(successor_hello).unwrap();
        let collected = ledger.finish().unwrap();
        assert_eq!(collected.pairs.len(), 1);
        assert_eq!(collected.pairs[0].0.operation, 11);
        assert_eq!(collected.pairs[0].1.result, 0);
        assert_eq!(collected.pairs[0].1.sequence, 4);
    }

    #[test]
    fn zero_receiver_event_budget_accepts_hello_but_rejects_an_operation() {
        let mut ledger = FrameLedger::new(0, 4096);
        let mut hello = frame_bytes(b'h', b"");
        hello[1] = 0;
        let hello = read_frame(&mut hello.as_slice()).unwrap().unwrap();
        ledger.push(hello).unwrap();
        let start = read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
            .unwrap()
            .unwrap();
        assert_eq!(
            ReceiverFailure::from_error(&ledger.push(start).unwrap_err()),
            ReceiverFailure::EventLimit
        );
    }

    fn wait_for_failure(receiver: &OperationReceiver, expected: ReceiverFailure) {
        let deadline = Instant::now() + Duration::from_secs(2);
        while receiver.failure().is_none() && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(10));
        }
        assert_eq!(receiver.failure(), Some(expected));
    }

    fn send_hello(stream: &mut UnixStream) {
        let mut hello = frame_bytes(b'h', b"");
        hello[1] = 0;
        stream.write_all(&hello).unwrap();
        let mut ack = [0];
        stream.read_exact(&mut ack).unwrap();
        assert_eq!(&ack, b"g");
    }

    #[test]
    fn receiver_preserves_event_limit_across_a_later_transport_failure() {
        let receiver = OperationReceiver::bind(0, 4096).unwrap();
        // This earlier connection fails only after the budget failure. Joining
        // connections in acceptance order must not replace the original cause.
        let mut interrupted = UnixStream::connect(receiver.socket_path()).unwrap();
        interrupted.write_all(b"h").unwrap();
        let mut stream = UnixStream::connect(receiver.socket_path()).unwrap();
        send_hello(&mut stream);
        stream.write_all(&frame_bytes(b's', b"/tmp/input")).unwrap();
        wait_for_failure(&receiver, ReceiverFailure::EventLimit);
        drop(interrupted);
        drop(stream);
        assert_eq!(
            ReceiverFailure::from_error(&receiver.finish().err().unwrap()),
            ReceiverFailure::EventLimit
        );
    }

    #[test]
    fn receiver_charges_hello_bytes_before_retention() {
        let hello_charge = crate::record::retained_frame_charge(0, std::iter::empty()).unwrap();
        for budget in [hello_charge - 1, hello_charge] {
            let receiver = OperationReceiver::bind(2, budget).unwrap();
            let mut stream = UnixStream::connect(receiver.socket_path()).unwrap();
            if budget == hello_charge {
                send_hello(&mut stream);
                assert_eq!(receiver.failure(), None);
                stream.write_all(&frame_bytes(b's', b"/tmp/input")).unwrap();
            } else {
                let mut hello = frame_bytes(b'h', b"");
                hello[1] = 0;
                stream.write_all(&hello).unwrap();
            }
            wait_for_failure(&receiver, ReceiverFailure::ByteLimit);
            drop(stream);
            assert_eq!(
                ReceiverFailure::from_error(&receiver.finish().err().unwrap()),
                ReceiverFailure::ByteLimit
            );
        }
    }

    #[test]
    fn receiver_accepts_exact_pair_and_hello_budgets() {
        let path = b"/tmp/input";
        let hello_charge = crate::record::retained_frame_charge(0, std::iter::empty()).unwrap();
        let start_charge =
            crate::record::retained_frame_charge(path.len(), std::iter::empty()).unwrap();
        let receiver = OperationReceiver::bind(2, hello_charge * 2 + start_charge).unwrap();
        let mut stream = UnixStream::connect(receiver.socket_path()).unwrap();
        send_hello(&mut stream);
        stream.write_all(&frame_bytes(b's', path)).unwrap();
        let mut ack = [0];
        stream.read_exact(&mut ack).unwrap();
        assert_eq!(&ack, b"g");
        stream.write_all(&frame_bytes(b'e', b"")).unwrap();
        drop(stream);
        assert_eq!(receiver.finish().unwrap().pairs.len(), 1);
    }

    #[test]
    fn receiver_reports_interrupted_transport_as_trace_loss() {
        let receiver = OperationReceiver::bind(2, 4096).unwrap();
        let mut stream = UnixStream::connect(receiver.socket_path()).unwrap();
        stream.write_all(b"h").unwrap();
        drop(stream);
        wait_for_failure(&receiver, ReceiverFailure::TraceLoss);
        assert_eq!(
            ReceiverFailure::from_error(&receiver.finish().err().unwrap()),
            ReceiverFailure::TraceLoss
        );
    }

    #[test]
    fn receiver_reports_corrupt_injected_input() {
        let receiver = OperationReceiver::bind(2, 256).unwrap();
        let mut stream = UnixStream::connect(receiver.socket_path()).unwrap();
        stream.write_all(&frame_bytes(b'x', b"/tmp/input")).unwrap();
        drop(stream);
        wait_for_failure(&receiver, ReceiverFailure::TraceLoss);
        assert!(receiver.failed());
        assert_eq!(
            ReceiverFailure::from_error(&receiver.finish().err().unwrap()),
            ReceiverFailure::TraceLoss
        );
    }

    #[test]
    fn candidate_encoding_preserves_typed_limits() {
        use std::os::unix::process::ExitStatusExt;

        let directory = tempfile::tempdir().unwrap();
        let mut start = read_frame(&mut frame_bytes(b's', b"/tmp/input").as_slice())
            .unwrap()
            .unwrap();
        start.sequence = 1;
        let mut completion = read_frame(&mut frame_bytes(b'e', b"").as_slice())
            .unwrap()
            .unwrap();
        completion.sequence = 2;
        for (events, bytes, expected) in [
            (1, 4096, ReceiverFailure::EventLimit),
            (2, 1, ReceiverFailure::ByteLimit),
        ] {
            let error = assemble_candidate_record(
                directory.path(),
                vec![(start.clone(), completion.clone())],
                std::process::ExitStatus::from_raw(0),
                events,
                bytes,
            )
            .unwrap_err();
            assert_eq!(ReceiverFailure::from_error(&error), expected);
        }
    }

    fn compile_tls_fixture(directory: &std::path::Path) -> PathBuf {
        let source = directory.join("tls.rs");
        let binary = directory.join("tls");
        fs::write(
            &source,
            r#"
use std::{cell::RefCell, fs::File, io::Read, os::fd::IntoRawFd};
thread_local! { static FILE: RefCell<Option<File>> = const { RefCell::new(None) }; }
thread_local! { static NATIVE: RefCell<Option<NativeFile>> = const { RefCell::new(None) }; }
unsafe extern "C" { fn close(fd: i32) -> i32; fn __error() -> *mut i32; }
struct NativeFile(i32);
impl Drop for NativeFile {
    fn drop(&mut self) {
        // SAFETY: this fixture owns the descriptor and the current errno slot.
        unsafe {
            *__error() = 123;
            assert_eq!(close(self.0), 0);
            assert_eq!(*__error(), 123);
        }
        std::fs::rename("data", "moved").unwrap();
    }
}
fn main() {
    let mode = std::env::args().nth(1).unwrap();
    std::thread::spawn(move || {
        if mode == "native" {
            NATIVE.with(|slot| {
                let mut f = File::open("data").unwrap();
                f.read(&mut [0; 8]).unwrap();
                *slot.borrow_mut() = Some(NativeFile(f.into_raw_fd()));
            });
        } else if mode == "drop" {
            let mut f = File::open("data").unwrap();
            f.read(&mut [0; 8]).unwrap();
        } else if mode == "after" {
            let mut f = File::open("data").unwrap();
            f.read(&mut [0; 8]).unwrap();
            FILE.with(|slot| *slot.borrow_mut() = Some(f));
        } else {
            FILE.with(|slot| {
                let mut f = File::open("data").unwrap();
                f.read(&mut [0; 8]).unwrap();
                *slot.borrow_mut() = Some(f);
            });
            if mode == "lost" {
                std::fs::remove_file(std::env::var_os("CLIBOX_FSPY_SOCKET").unwrap()).unwrap();
            }
        }
    }).join().unwrap();
}
"#,
        )
        .unwrap();
        assert!(std::process::Command::new("rustc")
            .arg("-O")
            .arg("-o")
            .arg(&binary)
            .arg(&source)
            .status()
            .unwrap()
            .success());
        binary
    }

    #[test]
    fn injected_thread_local_file_teardown_keeps_paired_close() {
        // Keep the executable outside the selected root, matching the issue's
        // standalone Rust fixture rather than relying on this test harness TLS.
        let directory = tempfile::tempdir().unwrap();
        let binary = compile_tls_fixture(directory.path());
        let root = directory.path().join("root");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("data"), b"fixture\n").unwrap();
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        for mode in ["drop", "before", "after", "native"] {
            assert!(std::process::Command::new(&binary)
                .arg(mode)
                .current_dir(&root)
                .status()
                .unwrap()
                .success());
            if mode == "native" {
                fs::rename(root.join("moved"), root.join("data")).unwrap();
            }
            let receiver =
                OperationReceiver::bind_for_root(&root, 1_000_000, 256 * 1024 * 1024).unwrap();
            let mut command = fspy::Command::new(&binary);
            command
                .arg(mode)
                .current_dir(&root)
                .envs(std::env::vars_os())
                .env("CLIBOX_FSPY_SOCKET", receiver.socket_path().as_os_str())
                .stdout(Stdio::null())
                .stderr(Stdio::inherit());
            let child = runtime
                .block_on(command.spawn(CancellationToken::new()))
                .unwrap();
            let root_pid = child.root_pid;
            let status = runtime.block_on(child.wait_handle).unwrap();
            assert!(status.status.success(), "{mode}: {:?}", status.status);
            assert!(
                status.path_accesses.is_ok(),
                "{mode}: incomplete path channel"
            );
            let collected = receiver.finish().unwrap();
            assert!(collected.hello_pids.contains(&root_pid));
            let read = collected
                .pairs
                .iter()
                .find(|(start, end)| {
                    start.operation == 3 && start.path.ends_with(b"/data") && end.result == 8
                })
                .unwrap();
            assert!(
                collected.pairs.iter().any(|(start, end)| {
                    start.operation == 2
                        && start.path == read.0.path
                        && start.tid == read.0.tid
                        && start.id > read.0.id
                        && start.image_id == read.0.image_id
                        && end.result == 0
                        && end.error == 0
                }),
                "{mode}: missing paired successful close"
            );
            if mode == "native" {
                assert_eq!(fs::read(root.join("moved")).unwrap(), b"fixture\n");
                for suffix in [b"/data".as_slice(), b"/moved".as_slice()] {
                    assert!(
                        collected.pairs.iter().any(|(start, end)| {
                            start.operation == 9
                                && start.path.ends_with(suffix)
                                && start.tid == read.0.tid
                                && start.id > read.0.id
                                && start.image_id == read.0.image_id
                                && end.result == 0
                                && end.error == 0
                        }),
                        "late rename lost a paired path"
                    );
                }
            }
            assemble_candidate_record(
                &root,
                collected.pairs,
                status.status,
                1_000_000,
                256 * 1024 * 1024,
            )
            .unwrap();
        }
    }

    #[test]
    fn injected_late_thread_channel_loss_preserves_child_and_rejects_trace() {
        let directory = tempfile::tempdir().unwrap();
        let binary = compile_tls_fixture(directory.path());
        let root = directory.path().join("root");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("data"), b"fixture\n").unwrap();
        let receiver =
            OperationReceiver::bind_for_root(&root, 1_000_000, 256 * 1024 * 1024).unwrap();
        let mut command = fspy::Command::new(&binary);
        command
            .arg("lost")
            .current_dir(&root)
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_SOCKET", receiver.socket_path().as_os_str())
            .stdout(Stdio::null())
            .stderr(Stdio::inherit());
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        let child = runtime
            .block_on(command.spawn(CancellationToken::new()))
            .unwrap();
        let status = runtime.block_on(child.wait_handle).unwrap();
        assert!(status.status.success(), "{:?}", status.status);
        assert!(
            status.path_accesses.is_err(),
            "late close loss must invalidate the execution"
        );
        // An intact operation receiver alone cannot authorize a complete record:
        // the preload's shared completeness flag must reject the missing close.
        let collected = receiver.finish().unwrap();
        assert!(!collected
            .pairs
            .iter()
            .any(|(start, _)| { start.operation == 2 && start.path.ends_with(b"/data") }));
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
            if let Some(marker) = std::env::var_os("CLIBOX_FSPY_TEST_DETACH_MARKER") {
                // SAFETY: the fixture descendant is not the root group leader.
                assert!(unsafe { libc::setsid() } > 0);
                fs::write(marker, b"detached").unwrap();
            }
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
            if let Some(marker) = std::env::var_os("CLIBOX_FSPY_TEST_DETACH_MARKER") {
                let deadline = Instant::now() + Duration::from_secs(3);
                while !PathBuf::from(&marker).exists() && Instant::now() < deadline {
                    std::thread::sleep(Duration::from_millis(10));
                }
                assert!(PathBuf::from(&marker).exists());
            }
            return;
        }
        let path = PathBuf::from(path);
        assert_eq!(fs::read(&path).unwrap(), b"fixture");
        if std::env::var_os("CLIBOX_FSPY_TEST_FTRUNCATE").is_some() {
            let file = fs::OpenOptions::new().write(true).open(&path).unwrap();
            // SAFETY: the opened regular-file descriptor remains live.
            assert_eq!(unsafe { libc::ftruncate(file.as_raw_fd(), 3) }, 0);
            return;
        }
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

    #[test]
    fn exec_replacement_child() {
        let Some(stage) = std::env::var_os("CLIBOX_FSPY_TEST_REPLACE") else {
            return;
        };
        if stage == "done" {
            return;
        }
        use std::ffi::CString;
        let executable =
            CString::new(std::env::current_exe().unwrap().as_os_str().as_bytes()).unwrap();
        let exact = c"--exact";
        let test = c"macos::tests::exec_replacement_child";
        let args = [
            executable.as_ptr(),
            exact.as_ptr(),
            test.as_ptr(),
            std::ptr::null(),
        ];
        // SAFETY: this isolated fixture is the only test running in its child
        // process, and the replacement inherits the marker.
        unsafe { std::env::set_var("CLIBOX_FSPY_TEST_REPLACE", "done") };
        // SAFETY: the executable and argv C strings live until exec replaces
        // the process; the final argv slot is null.
        unsafe { libc::execv(executable.as_ptr(), args.as_ptr()) };
        panic!("execv failed: {}", io::Error::last_os_error());
    }
}
