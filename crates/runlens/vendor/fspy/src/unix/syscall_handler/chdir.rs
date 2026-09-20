use std::io;

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use fspy_shared::ipc::{AccessMode, PathAccess};

use super::SyscallHandler;

impl SyscallHandler {
    // Changing cwd depends on directory existence/accessibility, not its entries.
    // Resolve before the kernel changes cwd, including failed attempts.
    pub(super) fn chdir(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }

    pub(super) fn fchdir(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::READ, path: path.as_os_str().into() });
        Ok(())
    }
}
