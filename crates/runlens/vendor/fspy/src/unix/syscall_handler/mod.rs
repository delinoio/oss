mod execve;
mod getdents;
mod open;
mod mutate;
mod rename;
mod remove;
mod stat;
mod readlink;
mod xattr;
mod lifecycle;
mod chdir;
mod io_uring;
mod inotify;
mod fanotify;
mod namespace;

use std::{
    borrow::Cow,
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

use std::sync::{Arc, Mutex};
use fspy_shared::ipc::channel::Sender;

const PATH_MAX: usize = libc::PATH_MAX as usize;

pub struct SyscallHandler {
    sender: Arc<Mutex<Sender>>,
    path_read_buf: [u8; PATH_MAX],
}

impl SyscallHandler {
    pub fn new(sender: Arc<Mutex<Sender>>) -> Self { Self { sender, path_read_buf: [0; PATH_MAX] } }
    fn record(&self, access: PathAccess<'_>) {
        if let Ok(sender) = self.sender.lock() { sender.send(&access); }
    }
    fn handle_open(
        &mut self,
        caller: Caller,
        dir_fd: Fd,
        path_ptr: CStrPtr,
        flags: c_int,
    ) -> io::Result<()> {
        let path = self.resolve_path(caller, dir_fd, path_ptr)?;
        self.record(PathAccess {
            mode: fspy_shared_unix::access::open_flags(flags),
            path: path.as_os_str().into(),
        });
        Ok(())
    }

    fn resolve_path(&mut self, caller: Caller, dir_fd: Fd, path_ptr: CStrPtr) -> io::Result<PathBuf> {
        let Some(path_len) = path_ptr.read(caller, &mut self.path_read_buf)? else {
            return Err(io::Error::other("unreadable syscall path"));
        };
        let mut path = Cow::Borrowed(Path::new(OsStr::from_bytes(&self.path_read_buf[..path_len])));
        if !path.is_absolute() {
            let mut resolved_path = PathBuf::from(dir_fd.get_path(caller)?);
            if !nix::NixPath::is_empty(path.as_ref()) {
                resolved_path.push(&path);
            }
            path = Cow::Owned(resolved_path);
        }
        Ok(path.into_owned())
    }

    fn handle_open_dir(&mut self, caller: Caller, fd: Fd) -> io::Result<()> {
        let path = fd.get_path(caller)?;
        self.record(PathAccess {
            mode: AccessMode::READ_DIR,
            path: OsStr::from_bytes(path.as_bytes()).into(),
        });
        Ok(())
    }
}

impl_handler!(
    SyscallHandler:

    #[cfg(target_arch = "x86_64")] open,
    openat,
    openat2,

    #[cfg(target_arch = "x86_64")] rename,
    renameat,
    renameat2,

    #[cfg(target_arch = "x86_64")] unlink,
    #[cfg(target_arch = "x86_64")] rmdir,
    unlinkat,

    #[cfg(target_arch = "x86_64")] getdents,
    getdents64,

    #[cfg(target_arch = "x86_64")] stat,
    #[cfg(target_arch = "x86_64")] lstat,
    #[cfg(target_arch = "x86_64")] newfstatat,
    #[cfg(target_arch = "aarch64")] fstatat,
    statx,
    fstat,
    name_to_handle_at,
    statfs,
    fstatfs,
    chdir,
    fchdir,

    #[cfg(target_arch = "x86_64")] access,
    faccessat,
    faccessat2,

    #[cfg(target_arch = "x86_64")] mkdir,
    #[cfg(target_arch = "x86_64")] mknod,
    #[cfg(target_arch = "x86_64")] chmod,
    #[cfg(target_arch = "x86_64")] chown,
    #[cfg(target_arch = "x86_64")] lchown,
    #[cfg(target_arch = "x86_64")] utime,
    #[cfg(target_arch = "x86_64")] utimes,
    truncate,
    getxattr,
    lgetxattr,
    fgetxattr,
    listxattr,
    llistxattr,
    flistxattr,
    setxattr,
    lsetxattr,
    removexattr,
    lremovexattr,
    mkdirat,
    mknodat,
    fchmodat,
    fchmodat2,
    fchownat,
    utimensat,
    #[cfg(target_arch = "x86_64")] futimesat,
    fchmod,
    fchown,
    ftruncate,
    fsetxattr,
    fremovexattr,
    #[cfg(target_arch = "x86_64")] link,
    linkat,
    #[cfg(target_arch = "x86_64")] symlink,
    symlinkat,

    #[cfg(target_arch = "x86_64")] readlink,
    readlinkat,

    inotify_add_watch,
    fanotify_mark,

    io_uring_setup,
    io_uring_enter,
    io_uring_register,

    mount, umount2, move_mount, mount_setattr, unshare, setns, chroot, pivot_root,
    fsopen, fsconfig, fsmount, open_tree, fspick, clone, clone3,

    setsid,
    setpgid,

    execve,
    execveat,
);
