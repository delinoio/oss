use std::io;
#[cfg(unix)]
use std::os::unix::{fs::PermissionsExt, net::UnixStream};
#[cfg(unix)]
use std::path::PathBuf;
#[cfg(any(unix, windows))]
use std::time::{Duration, Instant};

pub const WINDOWS_PIPE_PATH: &str = r"\\.\pipe\io.delino.devhud.ipc";

#[cfg(any(unix, windows))]
fn remaining_until(deadline: Instant) -> io::Result<Duration> {
    let remaining = deadline
        .checked_duration_since(Instant::now())
        .ok_or_else(|| io::Error::new(io::ErrorKind::TimedOut, "IPC operation timed out"))?;
    if remaining.is_zero() {
        return Err(io::Error::new(
            io::ErrorKind::TimedOut,
            "IPC operation timed out",
        ));
    }
    Ok(remaining)
}

#[cfg(windows)]
fn remaining_millis(deadline: Instant) -> io::Result<u32> {
    let remaining = remaining_until(deadline)?;
    Ok(u32::try_from(remaining.as_millis().clamp(1, u128::from(u32::MAX))).unwrap_or(u32::MAX))
}

#[cfg(target_os = "linux")]
pub fn socket_path() -> io::Result<PathBuf> {
    let runtime = std::env::var_os("XDG_RUNTIME_DIR")
        .filter(|value| !value.is_empty())
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "XDG_RUNTIME_DIR is required"))?;
    let runtime = PathBuf::from(runtime);
    if !runtime.is_absolute() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "XDG_RUNTIME_DIR must be absolute",
        ));
    }
    Ok(runtime.join("devhud.sock"))
}

#[cfg(target_os = "macos")]
pub fn socket_path() -> io::Result<PathBuf> {
    dirs::home_dir()
        .map(|home| home.join("Library/Application Support/io.delino.devhud/run/devhud.sock"))
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "home directory is required"))
}

#[cfg(unix)]
pub struct IpcClientStream {
    stream: UnixStream,
    deadline: Option<Instant>,
}

#[cfg(unix)]
impl IpcClientStream {
    pub fn from_unix_stream(stream: UnixStream) -> Self {
        Self {
            stream,
            deadline: None,
        }
    }

    pub fn set_io_deadline(&mut self, timeout: Duration) {
        self.deadline = Some(Instant::now() + timeout);
    }

    fn remaining(&self) -> io::Result<Duration> {
        remaining_until(self.deadline.ok_or_else(|| {
            io::Error::new(io::ErrorKind::InvalidInput, "socket deadline is unset")
        })?)
    }
}

#[cfg(unix)]
impl io::Read for IpcClientStream {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        self.stream.set_read_timeout(Some(self.remaining()?))?;
        self.stream.read(buffer)
    }
}

#[cfg(unix)]
impl io::Write for IpcClientStream {
    fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
        self.stream.set_write_timeout(Some(self.remaining()?))?;
        self.stream.write(buffer)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.stream.set_write_timeout(Some(self.remaining()?))?;
        self.stream.flush()
    }
}

#[cfg(unix)]
pub fn connect(deadline: Instant) -> io::Result<IpcClientStream> {
    let path = socket_path()?;
    connect_unix(&path, deadline)
}

#[cfg(unix)]
fn connect_unix(path: &std::path::Path, deadline: Instant) -> io::Result<IpcClientStream> {
    use std::os::fd::OwnedFd;

    use socket2::{Domain, SockAddr, Socket, Type};

    let socket = Socket::new(Domain::UNIX, Type::STREAM, None)?;
    socket.connect_timeout(&SockAddr::unix(path)?, remaining_until(deadline)?)?;
    let descriptor: OwnedFd = socket.into();
    Ok(IpcClientStream {
        stream: descriptor.into(),
        deadline: Some(deadline),
    })
}

#[cfg(unix)]
pub fn prepare_unix_parent(path: &std::path::Path) -> io::Result<()> {
    let parent = path
        .parent()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "socket has no parent"))?;
    #[cfg(target_os = "macos")]
    {
        std::fs::create_dir_all(parent)?;
        std::fs::set_permissions(parent, std::fs::Permissions::from_mode(0o700))?;
    }
    #[cfg(target_os = "linux")]
    {
        let metadata = std::fs::metadata(parent)?;
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "XDG_RUNTIME_DIR is not private",
            ));
        }
    }
    Ok(())
}

#[cfg(unix)]
pub fn set_socket_permissions(path: &std::path::Path) -> io::Result<()> {
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))
}

#[cfg(target_os = "linux")]
pub fn peer_is_current_user(stream: &UnixStream) -> io::Result<bool> {
    let mut credentials = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut length = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
    // SAFETY: the output pointer and length describe a live `ucred` value and the
    // stream owns a valid fd.
    let result = unsafe {
        libc::getsockopt(
            std::os::fd::AsRawFd::as_raw_fd(stream),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&raw mut credentials).cast(),
            &raw mut length,
        )
    };
    if result != 0 {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: geteuid has no preconditions.
    Ok(credentials.uid == unsafe { libc::geteuid() })
}

#[cfg(target_os = "macos")]
pub fn peer_is_current_user(stream: &UnixStream) -> io::Result<bool> {
    let mut uid = 0;
    let mut gid = 0;
    // SAFETY: pointers reference live uid/gid values and the stream owns a valid
    // fd.
    let result = unsafe {
        libc::getpeereid(
            std::os::fd::AsRawFd::as_raw_fd(stream),
            &raw mut uid,
            &raw mut gid,
        )
    };
    if result != 0 {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: geteuid has no preconditions.
    Ok(uid == unsafe { libc::geteuid() })
}

#[cfg(windows)]
pub fn connect(deadline: Instant) -> io::Result<IpcClientStream> {
    use std::ptr::{null, null_mut};

    use windows_sys::Win32::{
        Foundation::{ERROR_PIPE_BUSY, ERROR_SEM_TIMEOUT, INVALID_HANDLE_VALUE},
        Storage::FileSystem::{
            CreateFileW, FILE_FLAG_OVERLAPPED, FILE_GENERIC_READ, FILE_GENERIC_WRITE, OPEN_EXISTING,
        },
        System::Pipes::WaitNamedPipeW,
    };

    let pipe_name = wide(WINDOWS_PIPE_PATH);
    loop {
        remaining_until(deadline)?;
        // SAFETY: the pipe name is NUL-terminated, optional pointers are null,
        // and ownership of a successful handle transfers to WindowsPipeStream.
        let handle = unsafe {
            CreateFileW(
                pipe_name.as_ptr(),
                FILE_GENERIC_READ | FILE_GENERIC_WRITE,
                0,
                null(),
                OPEN_EXISTING,
                FILE_FLAG_OVERLAPPED,
                null_mut(),
            )
        };
        if handle != INVALID_HANDLE_VALUE {
            return Ok(WindowsPipeStream {
                handle,
                deadline: Some(deadline),
            });
        }
        let error = io::Error::last_os_error();
        if error.raw_os_error().map(|code| code as u32) != Some(ERROR_PIPE_BUSY) {
            return Err(error);
        }
        // A successful wait is only a hint: another client can claim the
        // available instance before CreateFileW, so retry against the same
        // absolute deadline.
        // SAFETY: the pipe name is NUL-terminated and remains live for the call.
        if unsafe { WaitNamedPipeW(pipe_name.as_ptr(), remaining_millis(deadline)?) } == 0 {
            let error = io::Error::last_os_error();
            return if error.raw_os_error().map(|code| code as u32) == Some(ERROR_SEM_TIMEOUT) {
                Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "pipe connection timed out",
                ))
            } else {
                Err(error)
            };
        }
    }
}

#[cfg(windows)]
pub struct WindowsPipeListener {
    current_user_sid: String,
}

#[cfg(windows)]
pub struct WindowsPipeStream {
    handle: windows_sys::Win32::Foundation::HANDLE,
    deadline: Option<Instant>,
}

#[cfg(windows)]
pub type IpcClientStream = WindowsPipeStream;

#[cfg(windows)]
// SAFETY: the stream owns one pipe handle and all access requires `&mut self`.
unsafe impl Send for WindowsPipeStream {}

#[cfg(windows)]
impl WindowsPipeStream {
    pub fn set_io_deadline(&mut self, timeout: Duration) {
        self.deadline = Some(Instant::now() + timeout);
    }

    fn remaining_millis(&self) -> io::Result<u32> {
        remaining_millis(
            self.deadline.ok_or_else(|| {
                io::Error::new(io::ErrorKind::InvalidInput, "pipe deadline is unset")
            })?,
        )
    }

    fn overlapped_io(&mut self, buffer: *mut u8, length: u32, write: bool) -> io::Result<usize> {
        use std::ptr::null;

        use windows_sys::Win32::{
            Foundation::{
                CloseHandle, ERROR_BROKEN_PIPE, ERROR_IO_PENDING, ERROR_PIPE_NOT_CONNECTED,
                GetLastError, WAIT_OBJECT_0, WAIT_TIMEOUT,
            },
            Storage::FileSystem::{ReadFile, WriteFile},
            System::{
                IO::{CancelIoEx, GetOverlappedResult, OVERLAPPED},
                Threading::{CreateEventW, INFINITE, WaitForSingleObject},
            },
        };

        let timeout = self.remaining_millis()?;
        // SAFETY: null attributes/name create an unnamed event owned by this operation.
        let event = unsafe { CreateEventW(null(), 1, 0, null()) };
        if event.is_null() {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: zero is a valid initial OVERLAPPED state before assigning its event.
        let mut overlapped: OVERLAPPED = unsafe { std::mem::zeroed() };
        overlapped.hEvent = event;
        let mut transferred = 0_u32;
        // SAFETY: the buffer remains live until the overlapped operation has completed
        // or cancellation has been observed, and this stream uniquely owns the
        // pipe handle.
        let started = unsafe {
            if write {
                WriteFile(
                    self.handle,
                    buffer.cast(),
                    length,
                    &raw mut transferred,
                    &raw mut overlapped,
                )
            } else {
                ReadFile(
                    self.handle,
                    buffer.cast(),
                    length,
                    &raw mut transferred,
                    &raw mut overlapped,
                )
            }
        };
        if started == 0 {
            // SAFETY: read immediately after the failed Win32 call on this thread.
            let error = unsafe { GetLastError() };
            if !write && matches!(error, ERROR_BROKEN_PIPE | ERROR_PIPE_NOT_CONNECTED) {
                // SAFETY: event is owned by this operation and closed exactly once.
                unsafe { CloseHandle(event) };
                return Ok(0);
            }
            if error != ERROR_IO_PENDING {
                // SAFETY: event is owned by this operation and closed exactly once.
                unsafe { CloseHandle(event) };
                return Err(io::Error::from_raw_os_error(error as i32));
            }
            // SAFETY: event remains valid until the wait and any cancellation complete.
            let wait = unsafe { WaitForSingleObject(event, timeout) };
            if wait != WAIT_OBJECT_0 {
                let wait_error = io::Error::last_os_error();
                // SAFETY: the OVERLAPPED value remains live until the cancellation completes.
                unsafe {
                    CancelIoEx(self.handle, &raw const overlapped);
                    WaitForSingleObject(event, INFINITE);
                    CloseHandle(event);
                }
                return if wait == WAIT_TIMEOUT {
                    Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "pipe operation timed out",
                    ))
                } else {
                    Err(wait_error)
                };
            }
            // SAFETY: the event signaled completion and all output pointers are valid.
            if unsafe {
                GetOverlappedResult(self.handle, &raw const overlapped, &raw mut transferred, 0)
            } == 0
            {
                let error = io::Error::last_os_error();
                // SAFETY: event is owned by this operation and closed exactly once.
                unsafe { CloseHandle(event) };
                return if !write
                    && matches!(
                        error.raw_os_error().map(|code| code as u32),
                        Some(ERROR_BROKEN_PIPE | ERROR_PIPE_NOT_CONNECTED)
                    ) {
                    Ok(0)
                } else {
                    Err(error)
                };
            }
        }
        // SAFETY: an immediate successful operation has completed before this close.
        unsafe { CloseHandle(event) };
        Ok(transferred as usize)
    }
}

#[cfg(windows)]
impl io::Read for WindowsPipeStream {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        if buffer.is_empty() {
            return Ok(0);
        }
        let length = u32::try_from(buffer.len()).unwrap_or(u32::MAX);
        self.overlapped_io(buffer.as_mut_ptr(), length, false)
    }
}

#[cfg(windows)]
impl io::Write for WindowsPipeStream {
    fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
        if buffer.is_empty() {
            return Ok(0);
        }
        let length = u32::try_from(buffer.len()).unwrap_or(u32::MAX);
        self.overlapped_io(buffer.as_ptr().cast_mut(), length, true)
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[cfg(windows)]
impl Drop for WindowsPipeStream {
    fn drop(&mut self) {
        // SAFETY: the stream owns this handle and closes it exactly once.
        unsafe { windows_sys::Win32::Foundation::CloseHandle(self.handle) };
    }
}

#[cfg(windows)]
impl WindowsPipeListener {
    pub fn new() -> io::Result<Self> {
        Ok(Self {
            current_user_sid: process_user_sid()?,
        })
    }

    pub fn accept(&self) -> io::Result<WindowsPipeStream> {
        use std::ptr::{null, null_mut};

        use windows_sys::Win32::{
            Foundation::{
                CloseHandle, ERROR_IO_PENDING, ERROR_PIPE_CONNECTED, GetLastError,
                INVALID_HANDLE_VALUE, LocalFree, WAIT_OBJECT_0,
            },
            Security::{
                Authorization::{
                    ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1,
                },
                PSECURITY_DESCRIPTOR, SECURITY_ATTRIBUTES,
            },
            Storage::FileSystem::{FILE_FLAG_OVERLAPPED, PIPE_ACCESS_DUPLEX},
            System::{
                IO::{GetOverlappedResult, OVERLAPPED},
                Pipes::{
                    ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE,
                    PIPE_REJECT_REMOTE_CLIENTS, PIPE_TYPE_BYTE, PIPE_WAIT,
                },
                Threading::{CreateEventW, INFINITE, WaitForSingleObject},
            },
        };

        let descriptor_text = wide(&format!("D:P(A;;GA;;;{})", self.current_user_sid));
        let mut descriptor: PSECURITY_DESCRIPTOR = null_mut();
        // SAFETY: the string is NUL-terminated and the output pointer is valid.
        if unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                descriptor_text.as_ptr(),
                SDDL_REVISION_1,
                &raw mut descriptor,
                null_mut(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        let attributes = SECURITY_ATTRIBUTES {
            nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
            lpSecurityDescriptor: descriptor,
            bInheritHandle: 0,
        };
        let pipe_name = wide(WINDOWS_PIPE_PATH);
        // SAFETY: all pointers remain valid for the duration of the call; Windows
        // copies the descriptor.
        let handle = unsafe {
            CreateNamedPipeW(
                pipe_name.as_ptr(),
                PIPE_ACCESS_DUPLEX | FILE_FLAG_OVERLAPPED,
                PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS,
                16,
                64 * 1024,
                64 * 1024,
                5_000,
                &raw const attributes,
            )
        };
        // SAFETY: descriptor was allocated by LocalAlloc through the conversion API.
        unsafe { LocalFree(descriptor.cast()) };
        if handle == INVALID_HANDLE_VALUE {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: null attributes/name create an unnamed event owned by this accept
        // call.
        let event = unsafe { CreateEventW(null(), 1, 0, null()) };
        if event.is_null() {
            // SAFETY: the server handle is live and has not transferred ownership.
            unsafe { CloseHandle(handle) };
            return Err(io::Error::last_os_error());
        }
        // SAFETY: zero is a valid initial OVERLAPPED state before assigning its event.
        let mut overlapped: OVERLAPPED = unsafe { std::mem::zeroed() };
        overlapped.hEvent = event;
        // SAFETY: handle and OVERLAPPED remain live until connection completion.
        let connected_immediately = unsafe { ConnectNamedPipe(handle, &raw mut overlapped) } != 0;
        // SAFETY: read immediately after the failed Win32 call on this thread.
        let connect_error = if connected_immediately {
            0
        } else {
            unsafe { GetLastError() }
        };
        let mut transferred = 0_u32;
        let connected = connected_immediately
            || connect_error == ERROR_PIPE_CONNECTED
            || (connect_error == ERROR_IO_PENDING
                // SAFETY: the event remains live and the OVERLAPPED operation is pending.
                && unsafe { WaitForSingleObject(event, INFINITE) } == WAIT_OBJECT_0
                // SAFETY: the event signaled and the output pointer is valid.
                && unsafe {
                    GetOverlappedResult(
                        handle,
                        &raw const overlapped,
                        &raw mut transferred,
                        0,
                    )
                } != 0);
        let connection_error = (!connected).then(io::Error::last_os_error);
        // SAFETY: connection completion was observed before closing the event.
        unsafe { CloseHandle(event) };
        if !connected {
            // SAFETY: the server handle is live and has not transferred ownership.
            unsafe { CloseHandle(handle) };
            return Err(connection_error.expect("failed connection has an error"));
        }
        if !matches!(client_user_sid(handle), Ok(ref sid) if sid == &self.current_user_sid) {
            // SAFETY: the server handle is live and has not transferred ownership.
            unsafe { CloseHandle(handle) };
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "named-pipe client SID was rejected",
            ));
        }
        Ok(WindowsPipeStream {
            handle,
            deadline: None,
        })
    }
}

#[cfg(windows)]
fn wide(value: &str) -> Vec<u16> {
    value.encode_utf16().chain(std::iter::once(0)).collect()
}

#[cfg(windows)]
fn process_user_sid() -> io::Result<String> {
    use std::ptr::null_mut;

    use windows_sys::Win32::{
        Foundation::{CloseHandle, HANDLE},
        Security::TOKEN_QUERY,
        System::Threading::{GetCurrentProcess, OpenProcessToken},
    };
    let mut token: HANDLE = null_mut();
    // SAFETY: token is a valid output pointer and the pseudo process handle is
    // always valid.
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &raw mut token) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let result = token_user_sid(token);
    // SAFETY: token was returned by OpenProcessToken and is closed exactly once.
    unsafe { CloseHandle(token) };
    result
}

#[cfg(windows)]
fn client_user_sid(pipe: windows_sys::Win32::Foundation::HANDLE) -> io::Result<String> {
    use std::ptr::null_mut;

    use windows_sys::Win32::{
        Foundation::{CloseHandle, HANDLE},
        Security::{RevertToSelf, TOKEN_QUERY},
        System::{
            Pipes::ImpersonateNamedPipeClient,
            Threading::{GetCurrentThread, OpenThreadToken},
        },
    };
    // SAFETY: pipe is connected; impersonation applies only to this listener
    // thread.
    if unsafe { ImpersonateNamedPipeClient(pipe) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut token: HANDLE = null_mut();
    // SAFETY: the thread is impersonating and token is a valid output pointer.
    let opened =
        unsafe { OpenThreadToken(GetCurrentThread(), TOKEN_QUERY, 0, &raw mut token) } != 0;
    let result = if opened {
        token_user_sid(token)
    } else {
        Err(io::Error::last_os_error())
    };
    if opened {
        // SAFETY: token was returned by OpenThreadToken and is closed exactly once.
        unsafe { CloseHandle(token) };
    }
    // SAFETY: always end the temporary pipe-client impersonation before returning.
    if unsafe { RevertToSelf() } == 0 {
        return Err(io::Error::last_os_error());
    }
    result
}

#[cfg(windows)]
fn token_user_sid(token: windows_sys::Win32::Foundation::HANDLE) -> io::Result<String> {
    use std::ptr::null_mut;

    use windows_sys::{
        Win32::{
            Foundation::LocalFree,
            Security::{
                Authorization::ConvertSidToStringSidW, GetTokenInformation, TOKEN_USER, TokenUser,
            },
        },
        core::PWSTR,
    };
    let mut required = 0;
    // SAFETY: the null-buffer call obtains the required size.
    unsafe { GetTokenInformation(token, TokenUser, null_mut(), 0, &raw mut required) };
    if required == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut bytes = vec![0u8; required as usize];
    // SAFETY: the allocated buffer has the exact size requested by Windows.
    if unsafe {
        GetTokenInformation(
            token,
            TokenUser,
            bytes.as_mut_ptr().cast(),
            required,
            &raw mut required,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: TokenUser guarantees the buffer begins with TOKEN_USER and its SID
    // remains live with bytes.
    let user = unsafe { &*bytes.as_ptr().cast::<TOKEN_USER>() };
    let mut sid_text: PWSTR = null_mut();
    // SAFETY: SID comes from a successful TokenUser query and output is valid.
    if unsafe { ConvertSidToStringSidW(user.User.Sid, &raw mut sid_text) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut length = 0;
    // SAFETY: the conversion API returns a NUL-terminated UTF-16 allocation.
    while unsafe { *sid_text.add(length) } != 0 {
        length += 1;
    }
    // SAFETY: sid_text and length identify the allocation's initialized UTF-16
    // content.
    let result = String::from_utf16(unsafe { std::slice::from_raw_parts(sid_text, length) })
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid user SID"));
    // SAFETY: sid_text was allocated by LocalAlloc through ConvertSidToStringSidW.
    unsafe { LocalFree(sid_text.cast()) };
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(windows)]
    static WINDOWS_PIPE_TEST_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[test]
    fn windows_pipe_name_is_exact_and_user_scoped_by_server_acl() {
        assert_eq!(WINDOWS_PIPE_PATH, r"\\.\pipe\io.delino.devhud.ipc");
    }

    #[cfg(windows)]
    #[test]
    fn partial_windows_frame_is_cancelled_at_the_deadline() {
        use std::{
            io::{Read, Write},
            thread,
            time::Duration,
        };

        let _guard = WINDOWS_PIPE_TEST_LOCK.lock().unwrap();
        let listener = WindowsPipeListener::new().unwrap();
        let client = thread::spawn(|| {
            let mut stream = (0..100)
                .find_map(|_| match connect(Instant::now() + Duration::from_secs(1)) {
                    Ok(stream) => Some(stream),
                    Err(_) => {
                        thread::sleep(Duration::from_millis(10));
                        None
                    }
                })
                .expect("connect to test pipe");
            stream.set_io_deadline(Duration::from_secs(1));
            stream.write_all(&[16, 0]).unwrap();
            thread::sleep(Duration::from_millis(250));
        });
        let mut stream = listener.accept().unwrap();
        stream.set_io_deadline(Duration::from_millis(50));
        let mut prefix = [0_u8; 4];
        assert_eq!(
            stream.read_exact(&mut prefix).unwrap_err().kind(),
            io::ErrorKind::TimedOut
        );
        drop(stream);
        client.join().unwrap();
    }

    #[cfg(windows)]
    #[test]
    fn busy_windows_pipe_waits_for_the_next_instance() {
        use std::{thread, time::Duration};

        let _guard = WINDOWS_PIPE_TEST_LOCK.lock().unwrap();
        let listener = WindowsPipeListener::new().unwrap();
        let first_client = thread::spawn(|| {
            (0..100)
                .find_map(|_| match connect(Instant::now() + Duration::from_secs(1)) {
                    Ok(stream) => Some(stream),
                    Err(_) => {
                        thread::sleep(Duration::from_millis(10));
                        None
                    }
                })
                .expect("connect to first test pipe instance")
        });
        let first_server = listener.accept().unwrap();
        let first_client = first_client.join().unwrap();
        let deadline = Instant::now() + Duration::from_secs(1);
        let second_client = thread::spawn(move || connect(deadline));

        thread::sleep(Duration::from_millis(50));
        assert!(!second_client.is_finished());
        let second_server = listener.accept().unwrap();
        let second_client = second_client.join().unwrap().unwrap();

        assert_eq!(second_client.deadline, Some(deadline));
        drop((first_client, first_server, second_client, second_server));
    }

    #[cfg(unix)]
    #[test]
    fn partial_unix_client_frame_is_bounded_by_one_absolute_deadline() {
        use std::io::{Read, Write};

        let (mut peer, stream) = UnixStream::pair().unwrap();
        let mut client = IpcClientStream {
            stream,
            deadline: None,
        };
        peer.write_all(&[16, 0]).unwrap();
        client.set_io_deadline(Duration::from_millis(50));
        let mut prefix = [0_u8; 4];
        assert!(matches!(
            client.read_exact(&mut prefix).unwrap_err().kind(),
            io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
        ));
    }

    #[test]
    fn expired_connection_deadline_is_rejected() {
        assert_eq!(
            remaining_until(Instant::now() - Duration::from_millis(1))
                .unwrap_err()
                .kind(),
            io::ErrorKind::TimedOut
        );
    }

    #[cfg(unix)]
    #[test]
    fn unix_connect_carries_its_absolute_deadline() {
        use std::os::unix::net::UnixListener;

        let root = std::env::temp_dir().join(format!("devhud-ipc-connect-{}", std::process::id()));
        std::fs::create_dir_all(&root).unwrap();
        let path = root.join("devhud.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let deadline = Instant::now() + Duration::from_secs(1);

        let client = connect_unix(&path, deadline).unwrap();
        let (server, _) = listener.accept().unwrap();

        assert_eq!(client.deadline, Some(deadline));
        drop((client, server, listener));
        std::fs::remove_file(path).unwrap();
        std::fs::remove_dir(root).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_endpoint_has_no_tmp_fallback() {
        let previous = std::env::var_os("XDG_RUNTIME_DIR");
        // SAFETY: this test is single-threaded with respect to this crate's endpoint
        // tests.
        unsafe { std::env::remove_var("XDG_RUNTIME_DIR") };
        assert!(socket_path().is_err());
        if let Some(previous) = previous {
            unsafe { std::env::set_var("XDG_RUNTIME_DIR", previous) };
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn unix_socket_is_private_and_authorizes_the_current_uid() {
        use std::os::unix::{fs::PermissionsExt, net::UnixListener};
        let root = std::env::temp_dir().join(format!("devhud-ipc-{}", std::process::id()));
        std::fs::create_dir_all(&root).unwrap();
        let path = root.join("devhud.sock");
        let listener = UnixListener::bind(&path).unwrap();
        set_socket_permissions(&path).unwrap();
        let client = UnixStream::connect(&path).unwrap();
        let (server, _) = listener.accept().unwrap();
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        assert!(peer_is_current_user(&server).unwrap());
        drop((client, server, listener));
        std::fs::remove_file(path).unwrap();
        std::fs::remove_dir(root).unwrap();
    }
}
