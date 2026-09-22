use std::{ffi::OsStr, io::{self, Read}, os::unix::ffi::OsStrExt, path::PathBuf};
use fspy_seccomp_unotify::supervisor::handler::arg::{Caller, Fd, Ignored};
use fspy_shared::ipc::{AccessMode, PathAccess};
use super::SyscallHandler;

impl SyscallHandler {
    pub(super) fn bind(&mut self, caller: Caller, (_, address, length): (Ignored, u64, u64)) -> io::Result<()> {
        // Copy only the bounded caller-provided address, never form references
        // to remote memory. The kernel receives the original call unchanged.
        if length < 2 { return Err(io::Error::other("unreadable socket family")); }
        let mut reader = caller.read_vm(usize::try_from(address).map_err(io::Error::other)?);
        let mut family = [0; 2];
        reader.read_exact(&mut family)?;
        if u16::from_ne_bytes(family) as i32 != libc::AF_UNIX { return Ok(()); }
        if length > std::mem::size_of::<libc::sockaddr_un>() as u64 {
            return Err(io::Error::other("unsupported Unix socket address length"));
        }
        let mut bytes = [0; 108];
        let bytes = &mut bytes[..length as usize - 2];
        reader.read_exact(bytes)?;
        // Autobind and Linux abstract addresses do not create filesystem nodes.
        if bytes.is_empty() || bytes[0] == 0 { return Ok(()); }
        let end = bytes.iter().position(|byte| *byte == 0).unwrap_or(bytes.len());
        let mut path = PathBuf::from(OsStr::from_bytes(&bytes[..end]));
        if !path.is_absolute() { path = PathBuf::from(Fd::cwd().get_path(caller)?).join(path); }
        self.record(PathAccess { mode: AccessMode::WRITE, path: path.as_os_str().into() });
        Ok(())
    }
}
