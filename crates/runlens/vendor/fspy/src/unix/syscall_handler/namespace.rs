use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{Caller, Ignored, Ptr};
use super::SyscallHandler;

// Parent snapshots and /proc fd names cannot bind objects after filesystem
// namespace/root remapping. Preserve the kernel operation but report loss before
// it runs, including attempts denied by the host. Ordinary thread clones remain
// supported; only clones selecting namespace creation take this path.
const NAMESPACE_FLAGS: u64 = (libc::CLONE_NEWNS | libc::CLONE_NEWUSER | libc::CLONE_NEWPID
    | libc::CLONE_NEWNET | libc::CLONE_NEWIPC | libc::CLONE_NEWUTS | libc::CLONE_NEWCGROUP) as u64;
fn unsupported() -> io::Result<()> { Err(io::Error::other("unsupported filesystem namespace or root change")) }
macro_rules! remapping {
    ($($name:ident),* $(,)?) => { impl SyscallHandler {
        $(pub(super) fn $name(&mut self, _: Caller, _: (Ignored,)) -> io::Result<()> { unsupported() })*
    } };
}
remapping!(mount, umount2, move_mount, mount_setattr, unshare, setns, chroot, pivot_root,
    fsopen, fsconfig, fsmount, open_tree, fspick);
impl SyscallHandler {
    pub(super) fn clone(&mut self, _: Caller, (flags,): (u64,)) -> io::Result<()> {
        if flags & NAMESPACE_FLAGS != 0 { unsupported() } else { Ok(()) }
    }
    pub(super) fn clone3(&mut self, caller: Caller, (args, size): (Ptr<u64>, u64)) -> io::Result<()> {
        if size < 8 { return unsupported(); }
        // SAFETY: the remote reader copies exactly the first u64 flags field;
        // every bit pattern is valid. Unreadable caller memory returns an error.
        let flags = unsafe { args.read(caller)? };
        if flags & NAMESPACE_FLAGS != 0 { unsupported() } else { Ok(()) }
    }
}
