//! Optional clibox operation side channel for injected macOS processes.
//!
//! The legacy fspy path channel remains authoritative for injection coverage.
//! This channel carries syscall results and before-operation control only when
//! a clibox supervisor explicitly supplies its private socket. It is separate
//! from pnport's preload mode.

use std::{
    cell::{Cell, RefCell},
    ffi::CString,
    os::{fd::AsRawFd, unix::net::UnixStream},
    path::{Path, PathBuf},
    rc::Rc,
    sync::{
        OnceLock,
        atomic::{AtomicBool, Ordering},
    },
};

use libc::{c_char, c_int};

use crate::client::global_client;

const MAX_PATH: usize = 4096;

unsafe extern "C" {
    fn mach_vm_read_overwrite(
        target_task: libc::mach_port_t,
        address: libc::mach_vm_address_t,
        size: libc::mach_vm_size_t,
        data: libc::mach_vm_address_t,
        outsize: *mut libc::mach_vm_size_t,
    ) -> libc::kern_return_t;
}

/// Copy a caller pathname without dereferencing its pointer in the preload.
/// Invalid and unterminated buffers must reach libc unchanged for native errno.
pub fn safe_path(path: *const c_char) -> Option<CString> {
    preserve_errno(|| {
        if path.is_null() {
            return None;
        }
        // SAFETY: sysconf reads one constant and has no caller pointer.
        let page_size = usize::try_from(unsafe { libc::sysconf(libc::_SC_PAGESIZE) }).ok()?;
        if page_size == 0 {
            return None;
        }
        let mut bytes = Vec::with_capacity(MAX_PATH);
        let mut chunk = [0_u8; MAX_PATH];
        while bytes.len() < MAX_PATH {
            let address = (path as usize).checked_add(bytes.len())?;
            let length = (page_size - address % page_size).min(MAX_PATH - bytes.len());
            let mut copied = 0;
            // Read only to the next page boundary. A later unreadable page then
            // yields an unavailable path instead of crashing inside CStr.
            // libc marks the task port alias deprecated in favor of mach2.
            // Keep this narrow ABI call while the injected client has no mach2
            // dependency; remove the allowance if that dependency is added.
            #[expect(deprecated, reason = "the injected client avoids a mach2 dependency")]
            // SAFETY: the kernel validates the untrusted source address; chunk
            // is writable for the full requested length and copied is local.
            let status = unsafe {
                mach_vm_read_overwrite(
                    libc::mach_task_self_,
                    address as u64,
                    length as u64,
                    chunk.as_mut_ptr() as u64,
                    &raw mut copied,
                )
            };
            if status != libc::KERN_SUCCESS || copied != length as u64 {
                return None;
            }
            if let Some(end) = chunk[..length].iter().position(|byte| *byte == 0) {
                bytes.extend_from_slice(&chunk[..end]);
                return CString::new(bytes).ok();
            }
            bytes.extend_from_slice(&chunk[..length]);
        }
        None
    })
}
static SOCKET: OnceLock<Option<PathBuf>> = OnceLock::new();
static IMAGE_ID: OnceLock<(u64, u64)> = OnceLock::new();
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
    let mut nonce = [0_u8; 16];
    // SAFETY: arc4random_buf initializes this local array without file I/O.
    unsafe { libc::arc4random_buf(nonce.as_mut_ptr().cast(), nonce.len()) };
    let _ = IMAGE_ID.set((
        u64::from_le_bytes(nonce[..8].try_into().expect("fixed nonce")) | 1,
        u64::from_le_bytes(nonce[8..].try_into().expect("fixed nonce")),
    ));
    READY.store(true, Ordering::Release);
    if socket_path().is_some() && !matches!(preserve_errno(|| with_stream(|_| true)), Some(true)) {
        mark_incomplete();
    }
}

thread_local! {
    static STREAM: RefCell<Option<(u32, Rc<UnixStream>)>> = const { RefCell::new(None) };
    static ACTIVE: Cell<bool> = const { Cell::new(false) };
    static RESOLVING: Cell<bool> = const { Cell::new(false) };
    // Const-initialized cells without Drop never register Rust TLS destructors.
    // Guards and correlation IDs must remain available after STREAM is destroyed.
    static NEXT_ID: Cell<(u32, u64)> = const { Cell::new((0, 1)) };
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
    Exec = 10,
    ExecReplace = 11,
}

/// Resolution of the final pathname component for metadata operations.
#[derive(Clone, Copy)]
#[repr(i64)]
pub enum FinalSymlink {
    Follow = 0,
    NoFollow = 1,
}

pub struct Token {
    id: u64,
    kind: Kind,
    socket: Rc<UnixStream>,
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

enum Ack {
    Proceed,
    Quit,
    Lost,
}

fn receive_ack(socket: &UnixStream) -> Ack {
    let mut ack = 0_u8;
    loop {
        // SAFETY: socket is valid and ack is writable.
        let count = unsafe { libc::recv(socket.as_raw_fd(), (&raw mut ack).cast(), 1, 0) };
        if count < 0 && std::io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
            continue;
        }
        return match (count, ack) {
            (1, b'g') => Ack::Proceed,
            (1, b'q') => Ack::Quit,
            _ => Ack::Lost,
        };
    }
}

fn socket_path() -> Option<&'static PathBuf> {
    if !READY.load(Ordering::Acquire) {
        return None;
    }
    SOCKET.get().and_then(Option::as_ref)
}

fn connect_stream(path: &Path) -> Option<Rc<UnixStream>> {
    let image_id = IMAGE_ID.get()?;
    let socket = UnixStream::connect(path).ok()?;
    let enabled: c_int = 1;
    let option_length = libc::socklen_t::try_from(std::mem::size_of_val(&enabled)).ok()?;
    // SAFETY: socket is owned by this thread and the option value points to a
    // live native integer of the declared length. Late connections must also
    // suppress SIGPIPE so transport loss never changes the child's behavior.
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
    if !send_frame(
        &socket,
        b'h',
        Kind::Hello,
        FramePayload {
            id: image_id.0,
            result: image_id.1.cast_signed(),
            error: 0,
            path: &[],
            identity: None,
        },
    ) {
        return None;
    }
    // Capture the process start identity before allowing any operation. Every
    // connection, including late-thread and fork connections, binds the same
    // process-image nonce and waits for the receiver's acknowledgment.
    if !matches!(receive_ack(&socket), Ack::Proceed) {
        return None;
    }
    Some(Rc::new(socket))
}

fn with_active<R>(action: impl FnOnce() -> Option<R>) -> Option<R> {
    ACTIVE.with(|active| {
        if active.replace(true) {
            return None;
        }
        let _reset = Reset(active);
        action()
    })
}

fn with_stream<R>(callback: impl FnOnce(&Rc<UnixStream>) -> R) -> Option<R> {
    let path = socket_path()?;
    with_active(|| {
        let mut callback = Some(callback);
        if let Ok(result) = STREAM.try_with(|slot| {
            let mut stream = slot.try_borrow_mut().ok()?;
            let pid = std::process::id();
            if stream.as_ref().is_some_and(|(owner, _)| *owner != pid) {
                // A fork inherits descriptors but must use separate framing.
                *stream = None;
            }
            if stream.is_none() {
                *stream = Some((pid, connect_stream(path)?));
            }
            Some(callback.take()?(&stream.as_ref()?.1))
        }) {
            result
        } else {
            // A native hook may run after Rust has destroyed STREAM. Open
            // one bounded, hello-admitted connection for that operation;
            // its Token retains the same socket through completion. Remove
            // this fallback only if the transport outlives all native TLS
            // destructors. Never skip a late operation in a complete trace.
            let socket = connect_stream(path)?;
            Some(callback.take()?(&socket))
        }
    })
}

#[derive(Clone, Copy)]
struct FramePayload<'a> {
    id: u64,
    result: i64,
    error: i32,
    path: &'a [u8],
    identity: Option<(u64, u64)>,
}

fn send_frame(socket: &UnixStream, frame: u8, kind: Kind, payload: FramePayload<'_>) -> bool {
    let FramePayload {
        id,
        result,
        error,
        path,
        identity,
    } = payload;
    let Ok(length) = u32::try_from(path.len()) else {
        return false;
    };
    let mut packet = Vec::with_capacity(66 + path.len());
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
    let (device, inode) = identity.unwrap_or((0, 0));
    packet.extend_from_slice(&device.to_le_bytes());
    packet.extend_from_slice(&inode.to_le_bytes());
    packet.extend_from_slice(path);
    send_all(socket, &packet)
}

pub unsafe fn enter_path(kind: Kind, path: *const c_char) -> Option<Token> {
    // SAFETY: the pointer is forwarded unchanged and copied fault-tolerantly.
    unsafe { enter_path_with_result(kind, path, 0) }
}

pub unsafe fn enter_open_path(path: *const c_char, mutates: bool) -> Option<Token> {
    // SAFETY: the pointer is forwarded unchanged and copied fault-tolerantly.
    unsafe { enter_path_with_result(Kind::Open, path, i64::from(mutates)) }
}

pub unsafe fn enter_metadata_path(path: *const c_char, policy: FinalSymlink) -> Option<Token> {
    // SAFETY: the pointer is forwarded unchanged and copied fault-tolerantly.
    unsafe { enter_path_with_result(Kind::Metadata, path, policy as i64) }
}

unsafe fn enter_path_with_result(kind: Kind, path: *const c_char, result: i64) -> Option<Token> {
    socket_path()?;
    with_resolution(|| {
        preserve_errno(|| {
            let copied = safe_path(path);
            let bytes = copied.as_ref().map_or(&[][..], |path| path.as_bytes());
            enter_with_result(kind, &absolute_path(libc::AT_FDCWD, bytes), result)
        })
    })
}

pub unsafe fn enter_at(kind: Kind, dirfd: c_int, path: *const c_char) -> Option<Token> {
    // SAFETY: the descriptor is unchanged and the pointer is copied
    // fault-tolerantly.
    unsafe { enter_at_with_result(kind, dirfd, path, 0) }
}

pub unsafe fn enter_open_at(dirfd: c_int, path: *const c_char, mutates: bool) -> Option<Token> {
    // SAFETY: the descriptor is unchanged and the pointer is copied
    // fault-tolerantly.
    unsafe { enter_at_with_result(Kind::Open, dirfd, path, i64::from(mutates)) }
}

pub unsafe fn enter_metadata_at(
    dirfd: c_int,
    path: *const c_char,
    policy: FinalSymlink,
) -> Option<Token> {
    // SAFETY: the descriptor is unchanged and the pointer is copied
    // fault-tolerantly.
    unsafe { enter_at_with_result(Kind::Metadata, dirfd, path, policy as i64) }
}

unsafe fn enter_at_with_result(
    kind: Kind,
    dirfd: c_int,
    path: *const c_char,
    result: i64,
) -> Option<Token> {
    socket_path()?;
    with_resolution(|| {
        preserve_errno(|| {
            let copied = safe_path(path);
            let bytes = copied.as_ref().map_or(&[][..], |path| path.as_bytes());
            enter_with_result(kind, &absolute_path(dirfd, bytes), result)
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
    enter_fd_with_result(kind, fd, 0)
}

pub fn enter_fd_requested(kind: Kind, fd: c_int, requested: Option<u64>) -> Option<Token> {
    let result = requested
        .and_then(|bytes| i64::try_from(bytes).ok())
        .unwrap_or(-1);
    enter_fd_with_result(kind, fd, result)
}

pub fn requested_vector_bytes(vectors: *const libc::iovec, count: c_int) -> Option<u64> {
    preserve_errno(|| {
        let count = usize::try_from(count).ok()?;
        if count > 1024 {
            return None;
        }
        if count == 0 {
            return Some(0);
        }
        let mut copied = std::iter::repeat_with(|| libc::iovec {
            iov_base: std::ptr::null_mut(),
            iov_len: 0,
        })
        .take(count)
        .collect::<Vec<_>>();
        let bytes = count.checked_mul(std::mem::size_of::<libc::iovec>())?;
        let mut received = 0_u64;
        // SAFETY: the kernel validates the caller pointer and writes only to
        // this bounded local vector allocation.
        #[expect(deprecated, reason = "the injected client avoids a mach2 dependency")]
        if unsafe {
            mach_vm_read_overwrite(
                libc::mach_task_self_,
                vectors as u64,
                bytes as u64,
                copied.as_mut_ptr() as u64,
                &raw mut received,
            )
        } != libc::KERN_SUCCESS
            || received != bytes as u64
        {
            return None;
        }
        copied.iter().try_fold(0_u64, |sum, vector| {
            sum.checked_add(u64::try_from(vector.iov_len).ok()?)
        })
    })
}

fn enter_fd_with_result(kind: Kind, fd: c_int, start_result: i64) -> Option<Token> {
    socket_path()?;
    with_resolution(|| {
        preserve_errno(|| {
            let mut bytes = [0_u8; MAX_PATH];
            // SAFETY: F_GETPATH writes a NUL-terminated pathname into this
            // buffer on success and does not call an interposed
            // file operation.
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
            // descriptors. They are outside this file-operation boundary;
            // sending a pathless event for every child stdout write
            // would exhaust the bounded record without adding file
            // evidence.
            path.and_then(|path| {
                // Inspect the same live descriptor before its native operation.
                // A later rename or replacement of F_GETPATH must not replace
                // the inode identity with that of a new pathname occupant.
                // SAFETY: fstat writes the exact initialized native structure.
                let mut stat = unsafe { std::mem::zeroed::<libc::stat>() };
                // SAFETY: fd remains borrowed by the intercepted call and
                // stat points to writable storage of the exact native type.
                if unsafe { libc::fstat(fd, &raw mut stat) } != 0 {
                    mark_incomplete();
                    return None;
                }
                let device = u64::from(stat.st_dev.cast_unsigned());
                let inode = stat.st_ino;
                enter_with_result_and_identity(kind, path, start_result, Some((device, inode)))
            })
        })
    })
}

fn enter_with_result(kind: Kind, path: &[u8], start_result: i64) -> Option<Token> {
    enter_with_result_and_identity(kind, path, start_result, None)
}

fn enter_with_result_and_identity(
    kind: Kind,
    path: &[u8],
    start_result: i64,
    identity: Option<(u64, u64)>,
) -> Option<Token> {
    if ACTIVE.with(Cell::get) {
        return None;
    }
    let id = NEXT_ID.with(|next| {
        let pid = std::process::id();
        let (owner, id) = next.get();
        let id = if owner == pid { id } else { 1 };
        next.set((pid, id.wrapping_add(1)));
        id
    });
    let outcome = with_stream(|socket| {
        let ack = if send_frame(
            socket,
            b's',
            kind,
            FramePayload {
                id,
                result: start_result,
                error: 0,
                path,
                identity,
            },
        ) {
            receive_ack(socket)
        } else {
            Ack::Lost
        };
        (ack, Rc::clone(socket))
    });
    if matches!(outcome, Some((Ack::Quit, _))) {
        // The supervisor explicitly rejected this start before its native
        // call. Terminate the calling process so the operation cannot run.
        // SAFETY: _exit is async-signal-safe and does not return.
        unsafe { libc::_exit(130) };
    }
    if let Some((Ack::Proceed, socket)) = outcome {
        Some(Token { id, kind, socket })
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
        with_active(|| {
            Some(send_frame(
                &token.socket,
                b'e',
                token.kind,
                FramePayload {
                    id: token.id,
                    result,
                    error,
                    path: &[],
                    identity: None,
                },
            ))
        }),
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

pub fn finish_spawn(token: Option<Token>, status: c_int) {
    // posix_spawn returns an errno value instead of setting errno. Preserve
    // the caller's errno while recording the equivalent signed native result.
    preserve_errno(|| {
        leave(
            token,
            if status == 0 { 0 } else { -i64::from(status) },
            status,
        );
    });
}
