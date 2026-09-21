use std::io;

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};

use super::SyscallHandler;

// Cover raw/static Linux callers as well as libc interposition. Both endpoints
// are write attempts, regardless of flags or syscall success. See PATCHES.md.
impl SyscallHandler {
    #[cfg(target_arch = "x86_64")]
    pub(super) fn rename(&mut self, caller: Caller, (old, new): (CStrPtr, CStrPtr)) -> io::Result<()> {
        self.renameat(caller, (Fd::cwd(), old, Fd::cwd(), new))
    }

    pub(super) fn renameat(&mut self, caller: Caller, (old_fd, old, new_fd, new): (Fd, CStrPtr, Fd, CStrPtr)) -> io::Result<()> {
        // Try both endpoints even if one path cannot be resolved; propagate
        // either loss so partial evidence cannot become a complete receipt.
        let source = self.handle_path_mutation(caller, old_fd, old);
        let destination = self.handle_path_mutation(caller, new_fd, new);
        source.and(destination)
    }

    pub(super) fn renameat2(&mut self, caller: Caller, paths: (Fd, CStrPtr, Fd, CStrPtr)) -> io::Result<()> {
        self.renameat(caller, paths)
    }
}
