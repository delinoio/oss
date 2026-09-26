//! Opt-in paired operation transport for the private clibox fspy workflow.
//!
//! The legacy shared-memory sender remains the fspy path-hint transport. A
//! separate authenticated loopback stream gives clibox a pre-operation ack and
//! a completion result without changing the vendor channel's public shape.

use std::{
    cell::{Cell, RefCell},
    io::{Read, Write},
    net::{SocketAddr, TcpStream},
    sync::OnceLock,
    time::{Duration, Instant},
};

use ntapi::ntpsapi::{
    NtQueryInformationProcess, PROCESS_BASIC_INFORMATION, ProcessBasicInformation,
};
use winapi::{
    shared::ntdef::NT_SUCCESS,
    um::processthreadsapi::{GetCurrentProcess, GetCurrentProcessId, GetCurrentThreadId},
};

use super::client::global_client;

const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);

struct State {
    busy: bool,
    enabled: bool,
    stream: Option<TcpStream>,
    next_id: u64,
}

impl State {
    fn new() -> Self {
        Self {
            busy: false,
            enabled: std::env::var_os("CLIBOX_FSPY_ENDPOINT").is_some(),
            stream: None,
            next_id: 0,
        }
    }

    fn stream(&mut self) -> std::io::Result<&mut TcpStream> {
        if self.stream.is_none() {
            let address = std::env::var("CLIBOX_FSPY_ENDPOINT")
                .map_err(|_| std::io::Error::other("missing_endpoint"))?
                .parse::<SocketAddr>()
                .map_err(|_| std::io::Error::other("invalid_endpoint"))?;
            if !address.ip().is_loopback() {
                return Err(std::io::Error::other("non_loopback_endpoint"));
            }
            let token = std::env::var("CLIBOX_FSPY_TOKEN")
                .map_err(|_| std::io::Error::other("missing_token"))?;
            if token.len() != 32 || !token.bytes().all(|byte| byte.is_ascii_hexdigit()) {
                return Err(std::io::Error::other("invalid_token"));
            }
            let mut stream = TcpStream::connect_timeout(&address, CONNECT_TIMEOUT)?;
            stream.set_read_timeout(Some(CONNECT_TIMEOUT))?;
            stream.set_write_timeout(Some(CONNECT_TIMEOUT))?;
            write_frame(&mut stream, b'h', 0, 0, 0, 0, token.as_bytes())?;
            let mut ack = [0_u8; 1];
            stream.read_exact(&mut ack)?;
            if ack != [b'g'] {
                return Err(std::io::Error::other("hello_rejected"));
            }
            self.stream = Some(stream);
        }
        self.stream
            .as_mut()
            .ok_or_else(|| std::io::Error::other("stream_unavailable"))
    }
}

thread_local! {
    static STATE: RefCell<State> = RefCell::new(State::new());
    static RESOLVING: Cell<bool> = const { Cell::new(false) };
}

pub(crate) fn with_resolution<R>(work: impl FnOnce() -> R) -> Option<R> {
    RESOLVING.with(|resolving| {
        if resolving.replace(true) {
            return None;
        }
        struct Reset<'a>(&'a Cell<bool>);
        impl Drop for Reset<'_> {
            fn drop(&mut self) {
                self.0.set(false);
            }
        }
        let _reset = Reset(resolving);
        Some(work())
    })
}

fn monotonic_ns() -> u64 {
    static ORIGIN: OnceLock<Instant> = OnceLock::new();
    u64::try_from(ORIGIN.get_or_init(Instant::now).elapsed().as_nanos()).unwrap_or(u64::MAX)
}

fn parent_pid() -> u32 {
    // SAFETY: a valid current-process pseudo handle, a writable struct, and
    // its exact length are passed to NtQueryInformationProcess.
    let mut basic: PROCESS_BASIC_INFORMATION = unsafe { std::mem::zeroed() };
    let status = unsafe {
        NtQueryInformationProcess(
            GetCurrentProcess(),
            ProcessBasicInformation,
            (&raw mut basic).cast(),
            std::mem::size_of::<PROCESS_BASIC_INFORMATION>() as u32,
            std::ptr::null_mut(),
        )
    };
    if NT_SUCCESS(status) {
        u32::try_from(basic.InheritedFromUniqueProcessId as usize).unwrap_or(0)
    } else {
        0
    }
}

fn write_frame(
    stream: &mut TcpStream,
    kind: u8,
    operation: u8,
    id: u64,
    result: i64,
    error: i32,
    path: &[u8],
) -> std::io::Result<()> {
    let length = u32::try_from(path.len()).map_err(|_| std::io::Error::other("path_length"))?;
    if path.len() > MAX_PATH_BYTES {
        return Err(std::io::Error::other("path_limit"));
    }
    let mut header = [0_u8; HEADER_BYTES];
    header[0] = kind;
    header[1] = operation;
    header[2..6].copy_from_slice(&unsafe { GetCurrentProcessId() }.to_le_bytes());
    header[6..10].copy_from_slice(&parent_pid().to_le_bytes());
    header[10..18].copy_from_slice(&u64::from(unsafe { GetCurrentThreadId() }).to_le_bytes());
    header[18..26].copy_from_slice(&id.to_le_bytes());
    header[26..34].copy_from_slice(&monotonic_ns().to_le_bytes());
    header[34..42].copy_from_slice(&result.to_le_bytes());
    header[42..46].copy_from_slice(&error.to_le_bytes());
    header[46..50].copy_from_slice(&length.to_le_bytes());
    stream.write_all(&header)?;
    stream.write_all(path)
}

pub(crate) fn mark_loss() {
    // SAFETY: detours are installed only after the global fspy client is set.
    unsafe { global_client() }.mark_incomplete();
}

pub struct OperationGuard {
    id: u64,
    operation: u8,
}

/// Send the start frame and wait for the parent's decision before forwarding
/// the native call. A recursive call made by this transport is excluded.
pub fn begin(operation: u8, path: &[u16]) -> Option<OperationGuard> {
    STATE.with(|state| {
        // Socket creation and I/O may internally touch NT handles. Exclude
        // those recursive detours instead of borrowing the TLS state twice.
        let Ok(mut state) = state.try_borrow_mut() else {
            return None;
        };
        if !state.enabled || state.busy {
            return None;
        }
        state.busy = true;
        let id = state.next_id.saturating_add(1);
        state.next_id = id;
        let mut encoded = Vec::with_capacity(path.len().saturating_mul(2));
        for unit in path {
            encoded.extend_from_slice(&unit.to_le_bytes());
        }
        let result = state.stream().and_then(|stream| {
            write_frame(stream, b's', operation, id, 0, 0, &encoded)?;
            let mut ack = [0_u8; 1];
            stream.read_exact(&mut ack)?;
            if ack == [b'g'] {
                Ok(())
            } else {
                Err(std::io::Error::other("start_rejected"))
            }
        });
        state.busy = false;
        if result.is_err() {
            state.stream = None;
            mark_loss();
            None
        } else {
            Some(OperationGuard { id, operation })
        }
    })
}

impl OperationGuard {
    /// The result is a successful byte count or zero for other successful
    /// calls. NTSTATUS failures are recorded as their stable native value.
    pub fn complete(self, result: i64, status: i32) {
        STATE.with(|state| {
            let Ok(mut state) = state.try_borrow_mut() else {
                mark_loss();
                return;
            };
            if state.busy {
                mark_loss();
                return;
            }
            state.busy = true;
            let outcome = state.stream().and_then(|stream| {
                write_frame(stream, b'e', self.operation, self.id, result, status, &[])?;
                // Complete frames do not require an acknowledgment.
                Ok(())
            });
            state.busy = false;
            if outcome.is_err() {
                // A native failure is still a complete observation. Only the
                // transport failure poisons the legacy trace channel.
                state.stream = None;
                mark_loss();
            }
        });
    }
}
