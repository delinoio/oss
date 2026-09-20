use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd, Ignored};
use super::SyscallHandler;

impl SyscallHandler {
    // A watch depends on the named object even when registration fails. The
    // kernel collector covers libc, direct, and static callers alike.
    pub(super) fn inotify_add_watch(
        &mut self, caller: Caller, (_, path, _): (Ignored, CStrPtr, Ignored),
    ) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }
}
