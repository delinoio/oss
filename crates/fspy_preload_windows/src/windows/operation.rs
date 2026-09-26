//! Opt-in paired operation transport for the private clibox fspy workflow.
//!
//! The legacy shared-memory sender remains the fspy path-hint transport. A
//! separate authenticated loopback stream gives clibox a pre-operation ack and
//! a completion result without changing the vendor channel's public shape.

use std::{
    cell::Cell,
    collections::HashSet,
    io::{Read, Write},
    net::{SocketAddr, TcpStream},
    sync::{Arc, Mutex, OnceLock},
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
        winsock2::{WSADATA, WSAStartup},
    },
};

use super::client::global_client;

const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);

struct State {
    enabled: bool,
    stream: Option<TcpStream>,
    next_id: u64,
}

impl State {
    fn new() -> Self {
        Self {
            enabled: std::env::var_os("CLIBOX_FSPY_ENDPOINT").is_some(),
            stream: None,
            next_id: 0,
        }
    }

    fn stream(&mut self) -> std::io::Result<&mut TcpStream> {
        if self.stream.is_none() {
            // Keep one transport-owned Winsock reference for the injected
            // process lifetime. The host runtime may release its own reference
            // before its final file operations; pairing must still work then.
            // Process teardown releases this reference. Do not call WSAStartup
            // from DllMain, where loader-lock reentrancy can deadlock.
            static WINSOCK: OnceLock<i32> = OnceLock::new();
            let status = *WINSOCK.get_or_init(|| {
                // SAFETY: WSAStartup initializes this process and writes the
                // provided exact WSADATA buffer on success.
                let mut data: WSADATA = unsafe { std::mem::zeroed() };
                unsafe { WSAStartup(0x0202, &mut data) }
            });
            if status != 0 {
                return Err(std::io::Error::from_raw_os_error(status));
            }
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
    let mut encoded = Vec::with_capacity(path.len().saturating_mul(2));
    for unit in path {
        encoded.extend_from_slice(&unit.to_le_bytes());
    }
    let result = state.stream().and_then(|stream| {
        write_frame(stream, b's', operation, id, 0, 0, &encoded)?;
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
    });
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
