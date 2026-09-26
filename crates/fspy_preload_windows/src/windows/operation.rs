//! Opt-in paired operation transport for the private clibox fspy workflow.
//!
//! The legacy shared-memory sender remains the fspy path-hint transport. A
//! separate authenticated loopback stream gives clibox a pre-operation ack and
//! a completion result without changing the vendor channel's public shape.

use std::{
    cell::Cell,
    collections::HashSet,
    io::{Read, Write},
    net::{Shutdown, SocketAddr, TcpStream},
    sync::{
        Arc, Mutex, OnceLock,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use ntapi::ntpsapi::{
    NtQueryInformationProcess, PROCESS_BASIC_INFORMATION, ProcessBasicInformation,
};
use winapi::{
    shared::ntdef::NT_SUCCESS,
    um::{
        processthreadsapi::{
            GetCurrentProcess, GetCurrentProcessId, GetCurrentThreadId, TerminateProcess,
        },
        winsock2::{WSACleanup, WSADATA, WSAStartup},
    },
};

use super::client::global_client;

const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const WSAENOTSOCK: i32 = 10038;
const WSANOTINITIALISED: i32 = 10093;
static PROCESS_EXITING: AtomicBool = AtomicBool::new(false);

pub(crate) fn set_process_exiting(exiting: bool) {
    PROCESS_EXITING.store(exiting, Ordering::Release);
}

struct WinsockLease;

impl WinsockLease {
    fn acquire() -> std::io::Result<Self> {
        // TLS teardown can outlive the host's last Winsock user. A late
        // fallback State acquires its own balanced reference before opening a
        // fresh connection. Never call WSAStartup from DllMain.
        let mut data: WSADATA = unsafe { std::mem::zeroed() };
        // SAFETY: the writable buffer has the exact WSADATA size.
        let status = unsafe { WSAStartup(0x0202, &mut data) };
        if status == 0 {
            Ok(Self)
        } else {
            Err(std::io::Error::from_raw_os_error(status))
        }
    }
}

impl Drop for WinsockLease {
    fn drop(&mut self) {
        // SAFETY: this lease represents one successful WSAStartup call.
        unsafe { WSACleanup() };
    }
}

struct State {
    enabled: bool,
    winsock: Option<WinsockLease>,
    stream: Option<TcpStream>,
    next_id: u64,
}

impl State {
    fn new() -> Self {
        Self {
            enabled: std::env::var_os("CLIBOX_FSPY_ENDPOINT").is_some(),
            winsock: None,
            stream: None,
            next_id: 0,
        }
    }

    fn stream(&mut self) -> std::io::Result<&mut TcpStream> {
        if self.winsock.is_none() {
            self.winsock = Some(WinsockLease::acquire()?);
        }
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

impl Drop for State {
    fn drop(&mut self) {
        if let Some(stream) = self.stream.take() {
            let _ = stream.shutdown(Shutdown::Write);
            drop(stream);
        }
        // Release the balanced Winsock lease only after its socket closes.
        self.winsock = None;
    }
}

thread_local! {
    static STATE: Arc<Mutex<State>> = Arc::new(Mutex::new(State::new()));
    static RESOLVING: Cell<bool> = const { Cell::new(false) };
}

pub(crate) fn with_resolution<R>(work: impl FnOnce() -> R) -> Option<R> {
    let mut work = Some(work);
    match RESOLVING.try_with(|resolving| {
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
        Some(work.take().expect("work is available")())
    }) {
        Ok(result) => result,
        Err(_) => {
            // Rust TLS is inaccessible from native detours during thread
            // teardown. Keep a separate native-thread reentrancy guard so
            // those final operations still receive paired observations.
            static ACTIVE: OnceLock<Mutex<HashSet<u32>>> = OnceLock::new();
            let active = ACTIVE.get_or_init(|| Mutex::new(HashSet::new()));
            let tid = unsafe { GetCurrentThreadId() };
            {
                let mut active = active.lock().ok()?;
                if !active.insert(tid) {
                    return None;
                }
            }
            struct ResetLate<'a>(&'a Mutex<HashSet<u32>>, u32);
            impl Drop for ResetLate<'_> {
                fn drop(&mut self) {
                    if let Ok(mut active) = self.0.lock() {
                        active.remove(&self.1);
                    }
                }
            }
            let _reset = ResetLate(active, tid);
            Some(work.take().expect("work is available")())
        }
    }
}

fn state_handle() -> Arc<Mutex<State>> {
    STATE
        .try_with(Arc::clone)
        .unwrap_or_else(|_| Arc::new(Mutex::new(State::new())))
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

pub(crate) fn mark_loss(stage: &'static str) {
    #[cfg(debug_assertions)]
    {
        let _ = writeln!(
            std::io::stderr(),
            "fspy preload: stage={stage} trace_incomplete"
        );
    }
    #[cfg(not(debug_assertions))]
    let _ = stage;
    // SAFETY: detours are installed only after the global fspy client is set.
    unsafe { global_client() }.mark_incomplete();
}

pub struct OperationGuard {
    id: u64,
    operation: u8,
    state: Arc<Mutex<State>>,
}

/// Send the start frame and wait for the parent's decision before forwarding
/// the native call. A recursive call made by this transport is excluded.
pub fn begin(operation: u8, path: &[u16]) -> Option<OperationGuard> {
    let mut encoded = Vec::with_capacity(path.len().saturating_mul(2));
    for unit in path {
        encoded.extend_from_slice(&unit.to_le_bytes());
    }
    begin_encoded(operation, &encoded)
}

/// Mutation starts carry both native paths as byte-counted UTF-16. A length
/// prefix keeps either pathname lossless even when it contains separator-like
/// code units; the receiving side validates both lengths before admission.
pub fn begin_paths(source: &[u16], destination: &[u16]) -> Option<OperationGuard> {
    let Some(source_bytes) = source.len().checked_mul(2) else {
        mark_loss("mutation_source_length");
        return None;
    };
    let Some(destination_bytes) = destination.len().checked_mul(2) else {
        mark_loss("mutation_destination_length");
        return None;
    };
    if source_bytes == 0
        || destination_bytes == 0
        || source_bytes > u16::MAX as usize
        || destination_bytes > u16::MAX as usize
        || source_bytes + destination_bytes + 4 > MAX_PATH_BYTES
    {
        mark_loss("mutation_path_limit");
        return None;
    }
    let mut encoded = Vec::with_capacity(source_bytes + destination_bytes + 4);
    encoded.extend_from_slice(&(source_bytes as u16).to_le_bytes());
    encoded.extend_from_slice(&(destination_bytes as u16).to_le_bytes());
    for unit in source.iter().chain(destination) {
        encoded.extend_from_slice(&unit.to_le_bytes());
    }
    begin_encoded(9, &encoded)
}

fn begin_encoded(operation: u8, encoded: &[u8]) -> Option<OperationGuard> {
    // ExitProcess runs DLL detach routines after user execution has ended.
    // Their file hooks can run after Winsock is unavailable. They are outside
    // this execution's observation interval and must not open a new channel.
    if PROCESS_EXITING.load(Ordering::Acquire) {
        return None;
    }
    let handle = state_handle();
    // Socket I/O can recursively enter an NT detour. A nonblocking lock
    // excludes those internal calls without blocking another native thread.
    let Ok(mut state) = handle.try_lock() else {
        return None;
    };
    if !state.enabled {
        return None;
    }
    let id = state.next_id.saturating_add(1);
    state.next_id = id;
    let mut send = |state: &mut State| {
        state.stream().and_then(|stream| {
            write_frame(stream, b's', operation, id, 0, 0, encoded)?;
            let mut ack = [0_u8; 1];
            stream.read_exact(&mut ack)?;
            match ack[0] {
                b'g' => Ok(()),
                b'q' => {
                    // The supervisor has already decided to stop this exact
                    // operation. End the process without forwarding the call.
                    unsafe { TerminateProcess(GetCurrentProcess(), 130) };
                    Err(std::io::Error::other("operation_cancelled"))
                }
                _ => Err(std::io::Error::other("start_rejected")),
            }
        })
    };
    let mut result = send(&mut state);
    if result
        .as_ref()
        .err()
        .and_then(std::io::Error::raw_os_error)
        .is_some_and(|code| code == WSAENOTSOCK || code == WSANOTINITIALISED)
    {
        // A host cleanup invalidates the prior socket before it can send a
        // byte. Reconnect with a fresh hello and retry this exact start once.
        state.stream = None;
        state.winsock = None;
        result = send(&mut state);
    }
    if let Err(error) = result {
        #[cfg(debug_assertions)]
        let _ = writeln!(
            std::io::stderr(),
            "fspy preload: stage=transport_begin kind={:?} reason={error}",
            error.kind(),
        );
        #[cfg(not(debug_assertions))]
        let _ = error;
        state.stream = None;
        mark_loss("transport_begin");
        None
    } else {
        drop(state);
        Some(OperationGuard {
            id,
            operation,
            state: handle,
        })
    }
}

impl OperationGuard {
    /// The result is a successful byte count or zero for other successful
    /// calls. NTSTATUS failures are recorded as their stable native value.
    pub fn complete(self, result: i64, status: i32) {
        let Ok(mut state) = self.state.try_lock() else {
            mark_loss("transport_complete_lock");
            return;
        };
        let outcome = state.stream().and_then(|stream| {
            write_frame(stream, b'e', self.operation, self.id, result, status, &[])?;
            // Complete frames do not require an acknowledgment.
            Ok(())
        });
        if let Err(error) = outcome {
            #[cfg(debug_assertions)]
            let _ = writeln!(
                std::io::stderr(),
                "fspy preload: stage=transport_complete kind={:?}",
                error.kind()
            );
            #[cfg(not(debug_assertions))]
            let _ = error;
            // A native failure is still a complete observation. Only the
            // transport failure poisons the legacy trace channel.
            state.stream = None;
            mark_loss("transport_complete");
        }
    }
}
