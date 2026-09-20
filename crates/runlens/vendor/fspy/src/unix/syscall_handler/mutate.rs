use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use fspy_shared::ipc::{AccessMode, PathAccess};
use super::SyscallHandler;

// Covers raw syscalls from static children as well as dynamic libc calls.
impl SyscallHandler {
    #[cfg(target_arch = "x86_64")]
    pub(super) fn mkdir(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn mknod(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn chmod(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn chown(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn lchown(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn utime(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn utimes(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn truncate(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn setxattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn lsetxattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn removexattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn lremovexattr(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn mkdirat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    pub(super) fn mknodat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    pub(super) fn fchmodat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    pub(super) fn fchmodat2(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    pub(super) fn fchownat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    pub(super) fn utimensat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        if path.is_null() { return self.fchmod(caller, (fd,)); }
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn futimesat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
    pub(super) fn fchmod(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::WRITE, path: path.as_os_str().into() });
        Ok(())
    }
    pub(super) fn fchown(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::WRITE, path: path.as_os_str().into() });
        Ok(())
    }
    pub(super) fn ftruncate(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::WRITE, path: path.as_os_str().into() });
        Ok(())
    }
    pub(super) fn fsetxattr(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::WRITE, path: path.as_os_str().into() });
        Ok(())
    }
    pub(super) fn fremovexattr(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::WRITE, path: path.as_os_str().into() });
        Ok(())
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn link(&mut self, caller: Caller, (source, destination): (CStrPtr, CStrPtr)) -> io::Result<()> {
        self.linkat(caller, (Fd::cwd(), source, Fd::cwd(), destination))
    }
    pub(super) fn linkat(&mut self, caller: Caller, (source_fd, source, destination_fd, destination): (Fd, CStrPtr, Fd, CStrPtr)) -> io::Result<()> {
        let source = self.handle_open(caller, source_fd, source, libc::O_RDWR);
        let destination = self.handle_open(caller, destination_fd, destination, libc::O_WRONLY);
        source.and(destination)
    }
    #[cfg(target_arch = "x86_64")]
    pub(super) fn symlink(&mut self, caller: Caller, (_target, path): (CStrPtr, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_WRONLY)
    }
    pub(super) fn symlinkat(&mut self, caller: Caller, (_target, fd, path): (CStrPtr, Fd, CStrPtr)) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }
}
