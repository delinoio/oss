use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd, Ignored};
use fspy_shared::ipc::{AccessMode, PathAccess};
use super::SyscallHandler;

impl SyscallHandler {
    pub(super) fn fanotify_mark(
        &mut self, caller: Caller, (_, flags, _, fd, path): (Ignored, libc::c_int, Ignored, Fd, CStrPtr),
    ) -> io::Result<()> {
        // FLUSH ignores the path; every other registration/removal depends on
        // the named object, even on failure. NULL selects dirfd itself.
        if (flags as u32) & libc::FAN_MARK_FLUSH != 0 { return Ok(()); }
        if path.is_null() {
            let path = fd.get_path(caller)?;
            self.record(PathAccess { mode: AccessMode::READ, path: path.as_os_str().into() });
            Ok(())
        } else {
            self.handle_open(caller, fd, path, libc::O_RDONLY)
        }
    }
}
