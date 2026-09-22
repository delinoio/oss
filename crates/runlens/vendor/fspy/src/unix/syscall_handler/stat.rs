use fspy_shared::ipc::{AccessMode, PathAccess};
use std::{io, ffi::c_int};

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd, Ignored};

use super::SyscallHandler;

impl SyscallHandler {
    fn stat_path(&mut self, caller: Caller, fd: Fd, path: CStrPtr, flags: c_int) -> io::Result<()> {
        // Linux 6.11 permits NULL with AT_EMPTY_PATH, which Rust's metadata
        // implementation uses for held descriptors. Do not read address zero.
        if path.is_null() {
            // Rust/libc also probe statx availability with NULL and no
            // AT_EMPTY_PATH. That has no filesystem operand and must fail in
            // the kernel; it is not lost evidence of a path access.
            if flags & libc::AT_EMPTY_PATH == 0 { return Ok(()); }
            let path = fd.get_path(caller)?;
            self.record(fspy_shared::ipc::PathAccess {
                mode: if flags & libc::AT_SYMLINK_NOFOLLOW != 0 { AccessMode::READ_NOFOLLOW } else { AccessMode::READ },
                path: path.as_os_str().into(),
            });
            Ok(())
        } else if flags & libc::AT_SYMLINK_NOFOLLOW != 0 {
            self.handle_read_nofollow(caller, fd, path)
        } else {
            self.handle_open(caller, fd, path, libc::O_RDONLY)
        }
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn stat(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn lstat(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_read_nofollow(caller, Fd::cwd(), path)
    }

    #[cfg(target_arch = "aarch64")]
    pub(super) fn fstatat(
        &mut self,
        caller: Caller,
        (dir_fd, path_ptr, _, flags): (Fd, CStrPtr, Ignored, c_int),
    ) -> io::Result<()> {
        self.stat_path(caller, dir_fd, path_ptr, flags)
    }

    #[cfg(target_arch = "x86_64")]
    pub(super) fn newfstatat(
        &mut self,
        caller: Caller,
        (dir_fd, path_ptr, _, flags): (Fd, CStrPtr, Ignored, c_int),
    ) -> io::Result<()> {
        self.stat_path(caller, dir_fd, path_ptr, flags)
    }

    /// statx(2) — modern replacement for stat/fstatat used by newer glibc.
    pub(super) fn statx(
        &mut self,
        caller: Caller,
        (dir_fd, path_ptr, flags): (Fd, CStrPtr, c_int),
    ) -> io::Result<()> {
        self.stat_path(caller, dir_fd, path_ptr, flags)
    }

    /// access(2) — check file accessibility (e.g. existsSync in Node.js).
    #[cfg(target_arch = "x86_64")]
    pub(super) fn access(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }

    /// faccessat(2) — check file accessibility relative to directory fd.
    pub(super) fn faccessat(
        &mut self,
        caller: Caller,
        (dir_fd, path_ptr): (Fd, CStrPtr),
    ) -> io::Result<()> {
        self.handle_open(caller, dir_fd, path_ptr, libc::O_RDONLY)
    }

    /// faccessat2(2) — extended faccessat with flags parameter.
    pub(super) fn faccessat2(
        &mut self,
        caller: Caller,
        (dir_fd, path_ptr): (Fd, CStrPtr),
    ) -> io::Result<()> {
        self.handle_open(caller, dir_fd, path_ptr, libc::O_RDONLY)
    }
}

// Filesystem capacity/type/flags are metadata inputs too; retain only access.
impl SyscallHandler {
    pub(super) fn statfs(&mut self, caller: Caller, (path,): (CStrPtr,)) -> io::Result<()> {
        self.handle_open(caller, Fd::cwd(), path, libc::O_RDONLY)
    }
    pub(super) fn fstatfs(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess { mode: AccessMode::READ, path: path.as_os_str().into() });
        Ok(())
    }
}

impl SyscallHandler {
    pub(super) fn name_to_handle_at(
        &mut self, caller: Caller, (fd, path): (Fd, CStrPtr),
    ) -> io::Result<()> {
        // Handle/mount identity is an input even on size probes and failed
        // lookups. The shared resolver handles relative and empty fd paths;
        // returned handle bytes and mount IDs never enter evidence.
        self.handle_open(caller, fd, path, libc::O_RDONLY)
    }
}

impl SyscallHandler {
    pub(super) fn fstat(&mut self, caller: Caller, (fd,): (Fd,)) -> io::Result<()> {
        use std::os::unix::ffi::OsStrExt;
        let path = fd.get_path(caller)?;
        let bytes = path.as_bytes();
        // Anonymous pipes/sockets have kernel-generated procfs identities, not
        // filesystem paths. Keep named FIFOs and every absolute path observable.
        if [b"pipe:[".as_slice(), b"socket:[".as_slice()].iter().any(|prefix| {
            bytes.strip_prefix(*prefix).and_then(|tail| tail.strip_suffix(b"]"))
                .is_some_and(|inode| !inode.is_empty() && inode.iter().all(u8::is_ascii_digit))
        }) { return Ok(()); }
        self.record(PathAccess { mode: AccessMode::READ, path: path.as_os_str().into() });
        Ok(())
    }
}
