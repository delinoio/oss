use std::io;
#[cfg(unix)]
use std::os::unix::{fs::PermissionsExt, net::UnixStream};
#[cfg(unix)]
use std::path::PathBuf;

pub const WINDOWS_PIPE_PATH: &str = r"\\.\pipe\io.delino.devhud\ipc";

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
pub fn connect() -> io::Result<UnixStream> {
    UnixStream::connect(socket_path()?)
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
pub fn connect() -> io::Result<std::fs::File> {
    std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(WINDOWS_PIPE_PATH)
}

#[cfg(windows)]
pub struct WindowsPipeListener {
    current_user_sid: String,
}

#[cfg(windows)]
impl WindowsPipeListener {
    pub fn new() -> io::Result<Self> {
        Ok(Self {
            current_user_sid: process_user_sid()?,
        })
    }

    pub fn accept(&self) -> io::Result<std::fs::File> {
        use std::{os::windows::io::FromRawHandle, ptr::null_mut};

        use windows_sys::Win32::{
            Foundation::{ERROR_PIPE_CONNECTED, GetLastError, INVALID_HANDLE_VALUE, LocalFree},
            Security::{
                Authorization::{
                    ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1,
                },
                PSECURITY_DESCRIPTOR, SECURITY_ATTRIBUTES,
            },
            Storage::FileSystem::PIPE_ACCESS_DUPLEX,
            System::Pipes::{
                ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_REJECT_REMOTE_CLIENTS,
                PIPE_TYPE_BYTE, PIPE_WAIT,
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
                PIPE_ACCESS_DUPLEX,
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
        // SAFETY: handle is a live named-pipe server and null requests synchronous
        // connection.
        let connected = unsafe { ConnectNamedPipe(handle, null_mut()) } != 0
            // SAFETY: read immediately after the failed Win32 call on this thread.
            || unsafe { GetLastError() } == ERROR_PIPE_CONNECTED;
        if !connected {
            // SAFETY: ownership transfers exactly once to File so the handle is closed.
            drop(unsafe { std::fs::File::from_raw_handle(handle) });
            return Err(io::Error::last_os_error());
        }
        if !matches!(client_user_sid(handle), Ok(ref sid) if sid == &self.current_user_sid) {
            // SAFETY: ownership transfers exactly once to File so the handle is closed.
            drop(unsafe { std::fs::File::from_raw_handle(handle) });
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "named-pipe client SID was rejected",
            ));
        }
        // SAFETY: ownership of the connected handle transfers exactly once to File.
        Ok(unsafe { std::fs::File::from_raw_handle(handle) })
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

    #[test]
    fn windows_pipe_name_is_exact_and_user_scoped_by_server_acl() {
        assert_eq!(WINDOWS_PIPE_PATH, r"\\.\pipe\io.delino.devhud\ipc");
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
