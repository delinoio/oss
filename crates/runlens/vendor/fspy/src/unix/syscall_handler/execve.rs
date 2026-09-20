use std::{fs::OpenOptions, io::{self, Read}, os::unix::fs::OpenOptionsExt};
use fspy_shared::ipc::{AccessMode, PathAccess};

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};

use super::SyscallHandler;

impl SyscallHandler {
    fn handle_execve(&mut self, caller: Caller, fd: Fd, path_ptr: CStrPtr) -> io::Result<()> {
        let path = self.resolve_path(caller, fd, path_ptr)?;
        self.record(PathAccess { mode: AccessMode::READ, path: path.as_os_str().into() });
        // The kernel opens shebang interpreters without another exec notification.
        // Until that chain can be bound, do not certify a script's dependencies.
        // NONBLOCK and the type check prevent malformed exec attempts from hanging
        // the supervisor while preserving the child's original syscall result.
        let mut file = OpenOptions::new().read(true).custom_flags(libc::O_NONBLOCK).open(&path)?;
        let mut header = [0u8; 2];
        if !file.metadata()?.is_file() || file.read_exact(&mut header).is_err() || header == *b"#!" {
            self.record(PathAccess { mode: AccessMode::UNSUPPORTED, path: path.as_os_str().into() });
        }
        Ok(())
    }

    pub(super) fn execveat(
        &mut self,
        caller: Caller,
        (fd, path_ptr): (Fd, CStrPtr),
    ) -> io::Result<()> {
        self.handle_execve(caller, fd, path_ptr)
    }

    pub(super) fn execve(&mut self, caller: Caller, (path_ptr,): (CStrPtr,)) -> io::Result<()> {
        self.handle_execve(caller, Fd::cwd(), path_ptr)
    }
}
