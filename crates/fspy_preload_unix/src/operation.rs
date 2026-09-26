//! Optional clibox operation side channel for injected macOS processes.
//!
//! The legacy fspy path channel remains authoritative for injection coverage.
//! This channel carries syscall results and before-operation control only when
//! a clibox supervisor explicitly supplies its private socket. It is separate
//! from pnport's preload mode.

use std::{
    cell::{Cell, RefCell},
    ffi::CStr,
    os::{fd::AsRawFd, unix::net::UnixStream},
    path::PathBuf,
    sync::{
        OnceLock,
        atomic::{AtomicBool, Ordering},
    },
};

use libc::{c_char, c_int};

use crate::client::global_client;

const MAX_PATH: usize = 4096;
static SOCKET: OnceLock<Option<PathBuf>> = OnceLock::new();
static READY: AtomicBool = AtomicBool::new(false);

#[cfg_attr(
    test,
    expect(
        dead_code,
        reason = "the production client constructor is disabled in unit tests"
    )
)]
pub fn init_ready() {
    let _ = SOCKET.set(std::env::var_os("CLIBOX_FSPY_SOCKET").map(PathBuf::from));
    READY.store(true, Ordering::Release);
    if socket_path().is_some() && !matches!(preserve_errno(|| with_stream(|_| true)), Some(true)) {
        mark_incomplete();
    }
}

thread_local! {
    static STREAM: RefCell<Option<(u32, UnixStream)>> = const { RefCell::new(None) };
    static ACTIVE: Cell<bool> = const { Cell::new(false) };
    static RESOLVING: Cell<bool> = const { Cell::new(false) };
    static NEXT_ID: Cell<u64> = const { Cell::new(1) };
    static MUTATIONS: RefCell<Vec<Vec<Token>>> = const { RefCell::new(Vec::new()) };
}

#[derive(Clone, Copy)]
pub enum Kind {
    Hello = 0,
    Open = 1,
    Close = 2,
    Read = 3,
    Write = 4,
    PositionalRead = 5,
    PositionalWrite = 6,
    Metadata = 7,
    Directory = 8,
    Mutation = 9,
}

pub struct Token {
    id: u64,
    kind: Kind,
}

struct Reset<'a>(&'a Cell<bool>);

impl Drop for Reset<'_> {
    fn drop(&mut self) {
        self.0.set(false);
    }
}

fn mark_incomplete() {
    if let Some(client) = global_client() {
        client.mark_incomplete();
    }
}

fn preserve_errno<T>(action: impl FnOnce() -> T) -> T {
    // SAFETY: __error provides the current injected thread's errno slot.
    let saved = unsafe { *libc::__error() };
    let result = action();
    // SAFETY: instrumentation before the native call must not change errno.
    unsafe { *libc::__error() = saved };
    result
}

fn with_resolution<T>(action: impl FnOnce() -> Option<T>) -> Option<T> {
    // macOS may bind pathname-resolution helpers back through an interposed
    // libc entry point. Exclude only these instrumentation-created nested
    // calls; remove the guard if resolution becomes raw-syscall-only.
    RESOLVING.with(|resolving| {
        if resolving.replace(true) {
            return None;
        }
        let _reset = Reset(resolving);
        action()
    })
}

fn monotonic_ns() -> u64 {
    let mut time = libc::timespec {
        tv_sec: 0,
        tv_nsec: 0,
    };
    // SAFETY: time is writable for the duration of the native clock call.
    if unsafe { libc::clock_gettime(libc::CLOCK_MONOTONIC, &raw mut time) } != 0 {
        mark_incomplete();
        return 0;
    }
    u64::try_from(time.tv_sec)
        .ok()
        .and_then(|seconds| seconds.checked_mul(1_000_000_000))
        .and_then(|base| u64::try_from(time.tv_nsec).ok()?.checked_add(base))
        .unwrap_or_else(|| {
            mark_incomplete();
            0
        })
}

fn send_all(socket: &UnixStream, bytes: &[u8]) -> bool {
    let mut sent = 0;
    while sent < bytes.len() {
        // SAFETY: socket is valid and the slice remains live during send.
        let count = unsafe {
            libc::send(
                socket.as_raw_fd(),
                bytes[sent..].as_ptr().cast(),
                bytes.len() - sent,
                0,
            )
        };
        if count < 0 && std::io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
            continue;
        }
        if count <= 0 {
            return false;
        }
        sent += count.cast_unsigned();
    }
    true
}

fn receive_ack(socket: &UnixStream) -> bool {
    let mut ack = 0_u8;
    loop {
        // SAFETY: socket is valid and ack is writable.
        let count = unsafe { libc::recv(socket.as_raw_fd(), (&raw mut ack).cast(), 1, 0) };
        if count < 0 && std::io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
            continue;
        }
        return count == 1 && ack == b'g';
    }
}

fn socket_path() -> Option<&'static PathBuf> {
    if !READY.load(Ordering::Acquire) {
        return None;
    }
    SOCKET.get().and_then(Option::as_ref)
}

fn with_stream<R>(callback: impl FnOnce(&UnixStream) -> R) -> Option<R> {
    let path = socket_path()?;
    ACTIVE.with(|active| {
        if active.replace(true) {
            return None;
        }
        let _reset = Reset(active);
        STREAM.with(|slot| {
            let mut stream = slot.borrow_mut();
            let pid = std::process::id();
            if stream.as_ref().is_some_and(|(owner, _)| *owner != pid) {
                // A fork inherits thread-local descriptors, but both processes
                // must have separate framing streams and correlation counters.
                *stream = None;
                NEXT_ID.with(|next| next.set(1));
                MUTATIONS.with(|stack| stack.borrow_mut().clear());
            }
            if stream.is_none() {
                let socket = UnixStream::connect(path).ok()?;
                let enabled: c_int = 1;
                // SAFETY: the option value has the expected native size.
                let option_length =
                    libc::socklen_t::try_from(std::mem::size_of_val(&enabled)).ok()?;
                // SAFETY: socket is owned by this thread and the option value
                // points to a live native integer of the declared length.
                if unsafe {
                    libc::setsockopt(
                        socket.as_raw_fd(),
                        libc::SOL_SOCKET,
                        libc::SO_NOSIGPIPE,
                        (&raw const enabled).cast(),
                        option_length,
                    )
                } != 0
                {
                    return None;
                }
                if !send_frame(&socket, b'h', Kind::Hello, 1, 0, 0, &[]) {
                    return None;
                }
                *stream = Some((pid, socket));
            }
            Some(callback(&stream.as_ref()?.1))
        })
    })
}

fn send_frame(
    socket: &UnixStream,
    frame: u8,
    kind: Kind,
    id: u64,
    result: i64,
    error: i32,
    path: &[u8],
) -> bool {
    let Ok(length) = u32::try_from(path.len()) else {
        return false;
    };
    let mut packet = Vec::with_capacity(50 + path.len());
    packet.push(frame);
    packet.push(kind as u8);
    packet.extend_from_slice(&std::process::id().to_le_bytes());
    // SAFETY: getppid has no pointer arguments and does not intercept file I/O.
    let parent = unsafe { libc::getppid() };
    packet.extend_from_slice(&parent.to_le_bytes());
    // SAFETY: pthread_self is valid for the current injected thread.
    let tid = unsafe { libc::pthread_mach_thread_np(libc::pthread_self()) };
    packet.extend_from_slice(&u64::from(tid).to_le_bytes());
    packet.extend_from_slice(&id.to_le_bytes());
    packet.extend_from_slice(&monotonic_ns().to_le_bytes());
    packet.extend_from_slice(&result.to_le_bytes());
    packet.extend_from_slice(&error.to_le_bytes());
    packet.extend_from_slice(&length.to_le_bytes());
    packet.extend_from_slice(path);
    send_all(socket, &packet)
}

pub unsafe fn enter_path(kind: Kind, path: *const c_char) -> Option<Token> {
    socket_path()?;
    with_resolution(|| {
        preserve_errno(|| {
            let bytes = if path.is_null() {
                &[][..]
            } else {
                // SAFETY: the caller supplies the same valid pathname pointer to libc.
                unsafe { CStr::from_ptr(path) }.to_bytes()
            };
            enter(kind, &absolute_path(libc::AT_FDCWD, bytes))
        })
    })
}

pub unsafe fn enter_at(kind: Kind, dirfd: c_int, path: *const c_char) -> Option<Token> {
    socket_path()?;
    with_resolution(|| {
        preserve_errno(|| {
            let bytes = if path.is_null() {
                &[][..]
            } else {
                // SAFETY: the caller supplies the same valid pathname pointer to libc.
                unsafe { CStr::from_ptr(path) }.to_bytes()
            };
            enter(kind, &absolute_path(dirfd, bytes))
        })
    })
}

fn absolute_path(dirfd: c_int, path: &[u8]) -> Vec<u8> {
    if path.is_empty() || path.starts_with(b"/") {
        return path.to_vec();
    }
    let mut base = [0_u8; MAX_PATH];
    let prefix = if dirfd == libc::AT_FDCWD {
        // SAFETY: getcwd writes a NUL-terminated path within the buffer.
        let found = unsafe { libc::getcwd(base.as_mut_ptr().cast(), base.len()) };
        if found.is_null() {
            return Vec::new();
        }
        base.iter().position(|byte| *byte == 0)
    } else {
        // SAFETY: F_GETPATH writes a NUL-terminated pathname on success.
        let status = unsafe { libc::fcntl(dirfd, libc::F_GETPATH, base.as_mut_ptr()) };
        if status != 0 {
            return Vec::new();
        }
        base.iter().position(|byte| *byte == 0)
    };
    let Some(length) = prefix else {
        return Vec::new();
    };
    let mut absolute = base[..length].to_vec();
    absolute.push(b'/');
    absolute.extend_from_slice(path);
    absolute
}

pub fn enter_fd(kind: Kind, fd: c_int) -> Option<Token> {
    socket_path()?;
    with_resolution(|| {
        preserve_errno(|| {
            let mut bytes = [0_u8; MAX_PATH];
            // SAFETY: F_GETPATH writes a NUL-terminated pathname into this buffer
            // on success and does not call an interposed file operation.
            let status = unsafe { libc::fcntl(fd, libc::F_GETPATH, bytes.as_mut_ptr()) };
            let path = if status == 0 {
                bytes
                    .iter()
                    .position(|byte| *byte == 0)
                    .map(|end| &bytes[..end])
            } else {
                None
            };
            // F_GETPATH is unavailable for pipes, sockets, and invalid file
            // descriptors. They are outside this file-operation boundary; sending
            // a pathless event for every child stdout write would exhaust the
            // bounded record without adding file evidence.
            path.and_then(|path| enter(kind, path))
        })
    })
}

fn enter(kind: Kind, path: &[u8]) -> Option<Token> {
    if ACTIVE.with(Cell::get) {
        return None;
    }
    let id = NEXT_ID.with(|next| {
        let id = next.get();
        next.set(id.wrapping_add(1));
        id
    });
    let outcome =
        with_stream(|socket| send_frame(socket, b's', kind, id, 0, 0, path) && receive_ack(socket));
    if matches!(outcome, Some(true)) {
        Some(Token { id, kind })
    } else {
        if socket_path().is_some() {
            mark_incomplete();
        }
        None
    }
}

pub fn leave(token: Option<Token>, result: i64, error: i32) {
    let Some(token) = token else {
        return;
    };
    if !matches!(
        with_stream(|socket| send_frame(socket, b'e', token.kind, token.id, result, error, &[])),
        Some(true)
    ) {
        mark_incomplete();
    }
}

pub fn finish(token: Option<Token>, result: i64) {
    // SAFETY: __error returns this thread's writable native errno slot.
    let error = unsafe { *libc::__error() };
    leave(token, result, if result < 0 { error } else { 0 });
    // SAFETY: side-channel I/O must not alter the intercepted call's errno.
    unsafe { *libc::__error() = error };
}

pub fn begin_mutation() {
    if socket_path().is_some() {
        MUTATIONS.with(|stack| stack.borrow_mut().push(Vec::new()));
    }
}

pub unsafe fn mutation_path(dirfd: c_int, path: *const c_char) {
    if socket_path().is_none() {
        return;
    }
    // SAFETY: this is the caller's pathname passed unchanged to libc.
    if let Some(token) = unsafe { enter_at(Kind::Mutation, dirfd, path) } {
        MUTATIONS.with(|stack| {
            if let Some(tokens) = stack.borrow_mut().last_mut() {
                tokens.push(token);
            } else {
                mark_incomplete();
            }
        });
    }
}

pub fn end_mutation(result: i64) {
    if socket_path().is_none() {
        return;
    }
    let tokens = MUTATIONS.with(|stack| stack.borrow_mut().pop());
    if let Some(tokens) = tokens {
        for token in tokens {
            finish(Some(token), result);
        }
    } else {
        mark_incomplete();
    }
}
