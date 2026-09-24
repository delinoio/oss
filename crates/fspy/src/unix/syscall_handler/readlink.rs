use std::io;

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};

use super::SyscallHandler;

impl SyscallHandler {
    #[cfg(target_arch = "x86_64")]
    pub(super) fn readlink(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }

    pub(super) fn readlinkat(
        &mut self,
        caller: Caller,
        (dir_fd, path): (Fd, CStrPtr),
    ) -> io::Result<()> {
        self.handle_open(caller, dir_fd, path, libc::O_RDONLY)
    }
}
