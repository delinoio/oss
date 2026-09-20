use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use fspy_shared::ipc::{AccessMode, PathAccess};
use super::SyscallHandler;

// Attribute names/values are not collected; only the file access attempt is.
impl SyscallHandler {
    pub(super) fn getxattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }
    pub(super) fn lgetxattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }
    pub(super) fn listxattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }
    pub(super) fn llistxattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }
    pub(super) fn fgetxattr(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::READ, path: path.as_os_str().into() });
        Ok(())
    }
    pub(super) fn flistxattr(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        self.fgetxattr(caller, (fd,))
    }
}
