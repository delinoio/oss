use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use super::SyscallHandler;

impl SyscallHandler {
    #[cfg(target_arch = "x86_64")]
    pub(super) fn readlink(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_read_nofollow(caller, Fd::cwd(), path)
    }

    pub(super) fn readlinkat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        // Record the named link, never its target text. An empty pathname uses
        // the O_PATH|O_NOFOLLOW descriptor identity through handle_open; unreadable
        // arguments become incomplete while the original syscall still proceeds.
        self.handle_read_nofollow(caller, fd, path)
    }
}
