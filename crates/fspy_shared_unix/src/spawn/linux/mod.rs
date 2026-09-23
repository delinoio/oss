#[cfg(not(target_env = "musl"))]
use std::os::fd::AsRawFd;
use std::os::fd::OwnedFd;
#[cfg(not(target_env = "musl"))]
use std::{
    ffi::{CString, OsStr},
    os::unix::ffi::OsStrExt as _,
    path::Path,
};

use fspy_seccomp_unotify::{payload::SeccompPayload, target::install_target};
#[cfg(not(target_env = "musl"))]
use memmap2::Mmap;
#[cfg(not(target_env = "musl"))]
use nix::{
    errno::Errno,
    fcntl::{OFlag, open},
    libc,
    sys::stat::Mode,
    unistd::{AccessFlags, access},
};

#[cfg(not(target_env = "musl"))]
use crate::{
    elf,
    exec::{append_path_env, ensure_env},
    open_exec::open_executable,
};
use crate::{
    exec::Exec,
    payload::{EncodedPayload, PAYLOAD_ENV_NAME},
};

const LD_PRELOAD: &str = "LD_PRELOAD";

#[cfg(not(target_env = "musl"))]
fn admit_preload(fd: &std::os::fd::OwnedFd) -> nix::Result<()> {
    if nix::sys::stat::fstat(fd)?.st_mode & 0o6000 != 0 {
        return Err(Errno::ENOTSUP);
    }
    // A file capability also puts the loader into secure-execution mode.
    // Fail closed if its presence cannot be determined from the opened image.
    // SAFETY: fd is open and the fixed capability key is a valid C string;
    // a null value buffer with zero length requests only the attribute size.
    let capability_len = unsafe {
        libc::fgetxattr(
            fd.as_raw_fd(),
            c"security.capability".as_ptr(),
            std::ptr::null_mut(),
            0,
        )
    };
    if capability_len > 0 {
        return Err(Errno::ENOTSUP);
    }
    if capability_len < 0 && !matches!(Errno::last(), Errno::ENODATA | Errno::ENOTSUP) {
        return Err(Errno::ENOTSUP);
    }
    Ok(())
}

pub struct PreExec {
    filter: Option<SeccompPayload>,
    // Keep the inspected inode open through execve/posix_spawn. The kernel
    // resolves /proc/self/fd/N before CLOEXEC closes the descriptor.
    _image: Option<OwnedFd>,
}

#[cfg(not(target_env = "musl"))]
fn bind_open_image(command: &mut Exec, executable_fd: &OwnedFd) {
    command.program = format!("/proc/self/fd/{}", executable_fd.as_raw_fd()).into();
}

#[cfg(not(target_env = "musl"))]
fn admit_execute_only(command: &mut Exec) -> nix::Result<OwnedFd> {
    let path = Path::new(OsStr::from_bytes(&command.program));
    let fd = open(path, OFlag::O_PATH | OFlag::O_CLOEXEC, Mode::empty())?;
    if nix::sys::stat::fstat(&fd)?.st_mode & 0o6000 != 0 {
        return Err(Errno::ENOTSUP);
    }
    // O_PATH cannot be passed to fgetxattr. Resolve its procfd link so the
    // capability query and subsequent exec stay bound to the same inode.
    let proc_path =
        CString::new(format!("/proc/self/fd/{}", fd.as_raw_fd())).map_err(|_| Errno::EINVAL)?;
    // SAFETY: the procfd path and attribute name are valid C strings, and a
    // null value buffer requests only the attribute size.
    let capability_len = unsafe {
        libc::getxattr(
            proc_path.as_ptr(),
            c"security.capability".as_ptr(),
            std::ptr::null_mut(),
            0,
        )
    };
    if capability_len > 0
        || (capability_len < 0 && !matches!(Errno::last(), Errno::ENODATA | Errno::ENOTSUP))
    {
        return Err(Errno::ENOTSUP);
    }
    bind_open_image(command, &fd);
    Ok(fd)
}

impl PreExec {
    /// Installs the seccomp unotify filter for the current process.
    ///
    /// # Errors
    ///
    /// Returns an error if the seccomp filter installation fails.
    pub fn run(&self) -> nix::Result<()> {
        if let Some(filter) = &self.filter {
            install_target(filter)?;
        }
        Ok(())
    }
}

pub fn handle_exec(
    command: &mut Exec,
    encoded_payload: &EncodedPayload,
) -> nix::Result<Option<PreExec>> {
    // On musl targets, LD_PRELOAD is not available (cdylib not supported).
    // Always use seccomp-based tracking instead.
    #[cfg(not(target_env = "musl"))]
    {
        let executable_path = OsStr::from_bytes(&command.program);
        let executable_fd = match open_executable(Path::new(executable_path)) {
            Ok(fd) => fd,
            Err(Errno::EACCES) if access(executable_path, AccessFlags::X_OK).is_ok() => {
                // The kernel can execute this image, but its unreadable bytes
                // cannot be classified for preload admission. Reject images
                // whose privilege transitions no_new_privs would suppress.
                let image = admit_execute_only(command)?;
                command
                    .envs
                    .retain(|(name, _)| name != LD_PRELOAD && name != PAYLOAD_ENV_NAME);
                return Ok(Some(PreExec {
                    filter: Some(encoded_payload.payload.seccomp_payload.clone()),
                    _image: Some(image),
                }));
            }
            Err(error) => return Err(error),
        };
        admit_preload(&executable_fd)?;
        // SAFETY: The file descriptor is valid and we only read from the mapping.
        let executable_mmap = unsafe { Mmap::map(&executable_fd) }.map_err(|io_error| {
            nix::Error::try_from(io_error).unwrap_or(nix::Error::UnknownErrno)
        })?;
        let preload = elf::is_dynamically_linked_to_libc(executable_mmap)?;
        // The path may be atomically replaced after classification. Execute
        // the already opened inode instead of resolving its name again.
        bind_open_image(command, &executable_fd);
        if preload {
            // Append (don't overwrite) so a user-provided LD_PRELOAD keeps
            // working. fspy's shim goes last so user preloads that
            // short-circuit a libc call stay invisible to fspy — what the
            // OS actually executed is what we want to record.
            append_path_env(
                &mut command.envs,
                LD_PRELOAD,
                encoded_payload.payload.preload_path.as_os_str().as_bytes(),
            );
            ensure_env(
                &mut command.envs,
                PAYLOAD_ENV_NAME,
                encoded_payload.encoded_string,
            )?;
            return Ok(Some(PreExec {
                filter: None,
                _image: Some(executable_fd),
            }));
        }
        command
            .envs
            .retain(|(name, _)| name != LD_PRELOAD && name != PAYLOAD_ENV_NAME);
        Ok(Some(PreExec {
            filter: Some(encoded_payload.payload.seccomp_payload.clone()),
            _image: Some(executable_fd),
        }))
    }

    #[cfg(target_env = "musl")]
    {
        command
            .envs
            .retain(|(name, _)| name != LD_PRELOAD && name != PAYLOAD_ENV_NAME);
        Ok(Some(PreExec {
            filter: Some(encoded_payload.payload.seccomp_payload.clone()),
            _image: None,
        }))
    }
}

#[cfg(all(test, not(target_env = "musl")))]
mod tests {
    use std::{
        fs,
        os::unix::{ffi::OsStrExt, fs::PermissionsExt},
        process::Command,
    };

    use nix::errno::Errno;

    use super::{admit_execute_only, admit_preload, bind_open_image, open_executable};

    #[test]
    fn bound_image_survives_path_replacement() {
        let directory = tempfile::tempdir().expect("create temporary directory");
        let path = directory.path().join("image");
        let replacement = directory.path().join("replacement");
        fs::copy("/bin/true", &path).expect("copy admitted image");
        fs::copy("/bin/false", &replacement).expect("copy replacement image");
        let fd = open_executable(&path).expect("open admitted image");
        let mut command = crate::exec::Exec {
            program: path.as_os_str().as_bytes().into(),
            args: vec![],
            envs: vec![],
        };
        bind_open_image(&mut command, &fd);
        fs::rename(&replacement, &path).expect("replace executable path");
        assert!(
            Command::new(std::ffi::OsStr::from_bytes(&command.program))
                .status()
                .expect("run bound image")
                .success()
        );
    }

    #[test]
    fn set_id_images_cannot_use_preload_tracking() {
        let directory = tempfile::tempdir().expect("create temporary directory");
        let path = directory.path().join("image");
        fs::write(&path, b"fixture").expect("write image");
        fs::set_permissions(&path, fs::Permissions::from_mode(0o755))
            .expect("make image executable");
        let fd = open_executable(&path).expect("open image");
        assert_eq!(admit_preload(&fd), Ok(()));
        fs::set_permissions(&path, fs::Permissions::from_mode(0o4755)).expect("set image uid bit");
        assert_eq!(admit_preload(&fd), Err(Errno::ENOTSUP));
    }

    #[test]
    fn execute_only_fallback_rejects_set_id_images() {
        let directory = tempfile::tempdir().expect("create temporary directory");
        let path = directory.path().join("image");
        fs::copy("/bin/true", &path).expect("copy image");
        fs::set_permissions(&path, fs::Permissions::from_mode(0o4711))
            .expect("set executable and setuid mode");
        let mut command = crate::exec::Exec {
            program: path.as_os_str().as_bytes().into(),
            args: vec![],
            envs: vec![],
        };
        assert_eq!(admit_execute_only(&mut command).err(), Some(Errno::ENOTSUP));
    }
}
