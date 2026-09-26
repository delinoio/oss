//! Strict receiver for the private macOS injected-operation side channel.
//!
//! The wire is intentionally independent of the legacy attempted-access
//! channel. A caller must validate both channels and owned-process cleanup
//! before it may publish a complete execution.

use std::{
    collections::HashMap,
    io::{self, Read},
};

const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FrameKind {
    Start,
    Completion,
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
}

fn invalid(reason: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, reason)
}

pub fn read_frame(reader: &mut impl Read) -> io::Result<Option<Frame>> {
    let mut header = [0_u8; HEADER_BYTES];
    match reader.read(&mut header[..1])? {
        0 => return Ok(None),
        1 => reader.read_exact(&mut header[1..])?,
        _ => unreachable!("one-byte read exceeded its buffer"),
    }
    let kind = match header[0] {
        b's' => FrameKind::Start,
        b'e' => FrameKind::Completion,
        _ => return Err(invalid("frame_kind")),
    };
    let operation = header[1];
    if !(1..=9).contains(&operation) {
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
        || (kind == FrameKind::Start && (result != 0 || error != 0))
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
    }))
}

#[derive(Default)]
pub struct FrameLedger {
    pending: HashMap<(u32, u64, u64), Frame>,
    completed: Vec<(Frame, Frame)>,
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

    pub fn push(&mut self, frame: Frame) -> io::Result<()> {
        self.event_count = self
            .event_count
            .checked_add(1)
            .ok_or_else(|| invalid("event_limit"))?;
        self.received_bytes = self
            .received_bytes
            .checked_add((HEADER_BYTES + frame.path.len()) as u64)
            .ok_or_else(|| invalid("byte_limit"))?;
        if self.event_count > self.max_events || self.received_bytes > self.max_bytes {
            return Err(invalid("frame_limit"));
        }
        let key = (frame.pid, frame.tid, frame.id);
        match frame.kind {
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

    pub fn finish(self) -> io::Result<Vec<(Frame, Frame)>> {
        if !self.pending.is_empty() {
            return Err(invalid("unpaired_start"));
        }
        Ok(self.completed)
    }
}

#[cfg(test)]
mod tests {
    use std::{
        fs,
        io::{self, Write},
        os::{
            fd::AsRawFd,
            unix::net::{UnixListener, UnixStream},
        },
        path::PathBuf,
        process::Stdio,
        sync::{Arc, Mutex},
        thread,
        time::Duration,
    };

    use tokio_util::sync::CancellationToken;

    use super::{read_frame, Frame, FrameKind, FrameLedger};

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

    fn receive_frames(mut stream: UnixStream, frames: Arc<Mutex<Vec<Frame>>>) -> io::Result<()> {
        stream.set_nonblocking(false)?;
        stream.set_read_timeout(Some(Duration::from_secs(10)))?;
        loop {
            let frame = match read_frame(&mut stream) {
                Ok(Some(frame)) => frame,
                Ok(None) => return Ok(()),
                Err(error)
                    if matches!(
                        error.kind(),
                        io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
                    ) =>
                {
                    return Ok(());
                }
                Err(error) => return Err(error),
            };
            let start = frame.kind == FrameKind::Start;
            frames.lock().unwrap().push(frame);
            if start {
                stream.write_all(b"g")?;
            }
        }
    }

    #[test]
    fn injected_child_reports_actual_read_results() {
        let directory = tempfile::Builder::new()
            .prefix("clibox-fspy-mac-")
            .tempdir_in("/tmp")
            .unwrap();
        let socket = directory.path().join("trace.sock");
        let listener = UnixListener::bind(&socket).unwrap();
        listener.set_nonblocking(true).unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let frames = Arc::new(Mutex::new(Vec::<Frame>::new()));
        let observed = Arc::clone(&frames);
        let accept = thread::spawn(move || {
            let mut connections = Vec::new();
            let deadline = std::time::Instant::now() + Duration::from_secs(10);
            while std::time::Instant::now() < deadline {
                match listener.accept() {
                    Ok((stream, _)) => {
                        let frames = Arc::clone(&observed);
                        connections.push(thread::spawn(move || receive_frames(stream, frames)));
                    }
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                        thread::sleep(Duration::from_millis(10));
                    }
                    Err(error) => panic!("accept failed: {error}"),
                }
                if observed.lock().unwrap().iter().any(|frame| {
                    frame.kind == FrameKind::Completion && frame.operation == 3 && frame.result == 7
                }) {
                    break;
                }
            }
            for connection in connections {
                connection.join().unwrap().unwrap();
            }
        });
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_SOCKET", socket.as_os_str())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
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
        accept.join().unwrap();
        let frames = frames.lock().unwrap();
        assert!(status.path_accesses.is_ok(), "frames: {frames:?}");
        let mut ledger = FrameLedger::new(1_000_000, 256 * 1024 * 1024);
        for frame in frames.iter() {
            ledger.push(frame.clone()).unwrap();
        }
        assert!(!ledger.finish().unwrap().is_empty());
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
    }

    #[test]
    fn read_fixture_child() {
        let Some(path) = std::env::var_os("CLIBOX_FSPY_TEST_INPUT") else {
            return;
        };
        let path = PathBuf::from(path);
        assert_eq!(fs::read(&path).unwrap(), b"fixture");
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
        fs::rename(&path, path.with_extension("moved")).unwrap();
    }
}
