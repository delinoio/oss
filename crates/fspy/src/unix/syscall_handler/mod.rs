mod cwd;
mod execve;
mod getdents;
mod mutate;
mod open;
mod readlink;
mod stat;

use std::{
    ffi::{OsStr, c_int},
    io,
    os::unix::ffi::OsStrExt,
    path::{Path, PathBuf},
};

use fspy_seccomp_unotify::{
    impl_handler,
    supervisor::handler::arg::{CStrPtr, Caller, Fd},
};
use fspy_shared::ipc::{AccessMode, PathAccess};

use crate::arena::PathAccessArena;

const PATH_MAX: usize = libc::PATH_MAX as usize;

#[derive(Debug)]
pub struct SyscallHandler {
    arena: PathAccessArena,
    path_read_buf: [u8; PATH_MAX],
}

impl Default for SyscallHandler {
    fn default() -> Self {
        Self {
            arena: PathAccessArena::default(),
            path_read_buf: [0; PATH_MAX],
        }
    }
}

impl SyscallHandler {
    pub fn into_arena(self) -> PathAccessArena {
        self.arena
    }

    fn resolve_path(
        &mut self,
        caller: Caller,
        dir_fd: Fd,
        path_ptr: CStrPtr,
    ) -> io::Result<Option<PathBuf>> {
        let Some(path_len) = path_ptr.read(caller, &mut self.path_read_buf)? else {
            // Ignore paths that are too long to fit in PATH_MAX
            return Ok(None);
        };
        let path = Path::new(OsStr::from_bytes(&self.path_read_buf[..path_len]));
        let path = if path.is_absolute() {
            path.to_path_buf()
        } else {
            let mut resolved_path = PathBuf::from(dir_fd.get_path(caller)?);
            if !nix::NixPath::is_empty(path) {
                resolved_path.push(path);
            }
            resolved_path
        };
        Ok(Some(path))
    }

    fn handle_open(
        &mut self,
        caller: Caller,
        dir_fd: Fd,
        path_ptr: CStrPtr,
        flags: c_int,
    ) -> io::Result<()> {
        let Some(path) = self.resolve_path(caller, dir_fd, path_ptr)? else {
            return Ok(());
        };
        self.arena.add(PathAccess {
            mode: fspy_shared_unix::open_mode::from_flags(flags),
            path: path.as_os_str().into(),
        });
        Ok(())
    }

    fn handle_open_dir(&mut self, caller: Caller, fd: Fd) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.arena.add(PathAccess {
            mode: AccessMode::READ_DIR,
            path: OsStr::from_bytes(path.as_bytes()).into(),
        });
        Ok(())
    }
}

impl_handler!(
    SyscallHandler:

    #[cfg(target_arch = "x86_64")] open,
    #[cfg(target_arch = "x86_64")] creat,
    openat,
    openat2,

    chdir,
    fchdir,

    #[cfg(target_arch = "x86_64")] readlink,
    readlinkat,

    #[cfg(target_arch = "x86_64")] unlink,
    unlinkat,
    #[cfg(target_arch = "x86_64")] rename,
    renameat,
    renameat2,
    #[cfg(target_arch = "x86_64")] mkdir,
    mkdirat,
    #[cfg(target_arch = "x86_64")] rmdir,
    #[cfg(target_arch = "x86_64")] symlink,
    symlinkat,
    #[cfg(target_arch = "x86_64")] link,
    linkat,
    #[cfg(target_arch = "x86_64")] mknod,
    mknodat,
    #[cfg(target_arch = "x86_64")] truncate,
    #[cfg(target_arch = "x86_64")] chmod,
    fchmodat,
    #[cfg(target_arch = "x86_64")] chown,
    #[cfg(target_arch = "x86_64")] lchown,
    fchownat,
    utimensat,

    #[cfg(target_arch = "x86_64")] getdents,
    getdents64,

    #[cfg(target_arch = "x86_64")] stat,
    #[cfg(target_arch = "x86_64")] lstat,
    #[cfg(target_arch = "x86_64")] newfstatat,
    #[cfg(target_arch = "aarch64")] fstatat,
    statx,

    #[cfg(target_arch = "x86_64")] access,
    faccessat,
    faccessat2,

    execve,
    execveat,
);
