use std::io;

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd, Ignored};

use super::SyscallHandler;

impl SyscallHandler {
    fn write_path(&mut self, caller: Caller, fd: Fd, path: CStrPtr) -> io::Result<()> {
        self.handle_open(caller, fd, path, libc::O_WRONLY)
    }

    fn move_path(
        &mut self,
        caller: Caller,
        old_fd: Fd,
        old_path: CStrPtr,
        new_fd: Fd,
        new_path: CStrPtr,
    ) -> io::Result<()> {
        self.write_path(caller, old_fd, old_path)?;
        self.write_path(caller, new_fd, new_path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn unlink(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    pub(super) fn unlinkat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn rename(
        &mut self,
        caller: Caller,
        (old_path, new_path): (CStrPtr, CStrPtr),
    ) -> io::Result<()> {
        self.move_path(caller, Fd::cwd(), old_path, Fd::cwd(), new_path)
    }

    pub(super) fn renameat(
        &mut self,
        caller: Caller,
        (old_fd, old_path, new_fd, new_path): (Fd, CStrPtr, Fd, CStrPtr),
    ) -> io::Result<()> {
        self.move_path(caller, old_fd, old_path, new_fd, new_path)
    }

    pub(super) fn renameat2(
        &mut self,
        caller: Caller,
        paths: (Fd, CStrPtr, Fd, CStrPtr),
    ) -> io::Result<()> {
        self.renameat(caller, paths)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn mkdir(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    pub(super) fn mkdirat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn rmdir(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn symlink(
        &mut self,
        caller: Caller,
        (_target, path): (Ignored, CStrPtr),
    ) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    pub(super) fn symlinkat(
        &mut self,
        caller: Caller,
        (_target, fd, path): (Ignored, Fd, CStrPtr),
    ) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn link(
        &mut self,
        caller: Caller,
        (old_path, new_path): (CStrPtr, CStrPtr),
    ) -> io::Result<()> {
        self.move_path(caller, Fd::cwd(), old_path, Fd::cwd(), new_path)
    }

    pub(super) fn linkat(
        &mut self,
        caller: Caller,
        paths: (Fd, CStrPtr, Fd, CStrPtr),
    ) -> io::Result<()> {
        self.renameat(caller, paths)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn mknod(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    pub(super) fn mknodat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn truncate(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn chmod(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    pub(super) fn fchmodat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn chown(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn lchown(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.write_path(caller, Fd::cwd(), path)
    }

    pub(super) fn fchownat(&mut self, caller: Caller, (fd, path): (Fd, CStrPtr)) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }

    pub(super) fn utimensat(
        &mut self,
        caller: Caller,
        (fd, path): (Fd, CStrPtr),
    ) -> io::Result<()> {
        self.write_path(caller, fd, path)
    }
}
