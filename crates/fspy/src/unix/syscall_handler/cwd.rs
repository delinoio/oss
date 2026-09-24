use std::{ffi::OsStr, io, os::unix::ffi::OsStrExt};

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use fspy_shared::ipc::{AccessMode, PathAccess};

use super::SyscallHandler;

impl SyscallHandler {
    pub(super) fn chdir(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        let Some(path) = self.resolve_path(caller, Fd::cwd(), path)? else {
            return Ok(());
        };
        self.arena.add(PathAccess {
            mode: AccessMode::READ,
            path: path.as_os_str().into(),
        });
        Ok(())
    }

    pub(super) fn fchdir(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.arena.add(PathAccess {
            mode: AccessMode::READ,
            path: OsStr::from_bytes(path.as_bytes()).into(),
        });
        Ok(())
    }
}
