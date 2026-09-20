use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use super::SyscallHandler;

impl SyscallHandler {
    #[cfg(target_arch = "x86_64")]
    pub(super) fn unlink(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn rmdir(&mut self, caller: Caller, path: (CStrPtr,)) -> io::Result<()> {
        self.unlink(caller, path)
    }

    pub(super) fn unlinkat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        // Both file and AT_REMOVEDIR calls mutate the named directory entry.
        // Argument resolution errors propagate as incomplete collection while
        // the supervisor continues the original syscall. See PATCHES.md.
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
}
