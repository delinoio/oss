use std::{
    ffi::OsStr,
    fs::File,
    io::{self, Read},
    os::unix::ffi::OsStrExt,
    path::{Path, PathBuf},
};

use fspy_seccomp_unotify::supervisor::handler::arg::{CStrPtr, Caller, Fd};
use fspy_shared::ipc::{AccessMode, PathAccess};
use fspy_shared_unix::exec::{ParseShebangOptions, parse_shebang};

use super::SyscallHandler;

impl SyscallHandler {
    fn handle_execve(&mut self, caller: Caller, fd: Fd, path_ptr: CStrPtr) -> io::Result<()> {
        let Some(mut path) = self.resolve_path(caller, fd, path_ptr)? else {
            return Ok(());
        };
        // The kernel reads script interpreters without making another syscall.
        // Inspect its bounded shebang chain in the target's working directory.
        for depth in 0..=4 {
            self.arena.add(PathAccess {
                mode: AccessMode::READ,
                path: path.as_os_str().into(),
            });
            let shebang = parse_shebang(
                |path, buf| {
                    let mut file = File::open(path).map_err(|error| io_to_errno(&error))?;
                    let mut len = 0;
                    while len < buf.len() {
                        let read = file
                            .read(&mut buf[len..])
                            .map_err(|error| io_to_errno(&error))?;
                        if read == 0 {
                            break;
                        }
                        len += read;
                    }
                    Ok(len)
                },
                &path,
                ParseShebangOptions::default(),
            )
            .map_err(|error| io::Error::from_raw_os_error(error as i32))?;
            let Some(shebang) = shebang else {
                return Ok(());
            };
            if depth == 4 {
                return Err(io::Error::from_raw_os_error(libc::ELOOP));
            }
            let interpreter = Path::new(OsStr::from_bytes(&shebang.interpreter));
            path = if interpreter.is_absolute() {
                interpreter.to_path_buf()
            } else {
                let mut cwd = PathBuf::from(Fd::cwd().get_path(caller)?);
                cwd.push(interpreter);
                cwd
            };
        }
        Ok(())
    }

    pub(super) fn execveat(
        &mut self,
        caller: Caller,
        (fd, path_ptr): (Fd, CStrPtr),
    ) -> io::Result<()> {
        self.handle_execve(caller, fd, path_ptr)
    }

    pub(super) fn execve(&mut self, caller: Caller, (path_ptr,): (CStrPtr,)) -> io::Result<()> {
        self.handle_execve(caller, Fd::cwd(), path_ptr)
    }
}

fn io_to_errno(error: &io::Error) -> nix::errno::Errno {
    nix::errno::Errno::from_raw(error.raw_os_error().unwrap_or(libc::EIO))
}
