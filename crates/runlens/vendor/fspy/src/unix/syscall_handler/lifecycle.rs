use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{Caller, Ignored};
use super::SyscallHandler;

impl SyscallHandler {
    // These calls can move a descendant outside the group owned by wait_unix.
    // Report lost lifecycle coverage before forwarding; never block the call.
    pub(super) fn setsid(&mut self, _caller: Caller, _: (Ignored,)) -> io::Result<()> {
        Err(io::Error::other("unsupported process session change"))
    }
    pub(super) fn setpgid(&mut self, _caller: Caller, _: (Ignored, Ignored)) -> io::Result<()> {
        Err(io::Error::other("unsupported process group change"))
    }
}
