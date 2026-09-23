#[cfg(not(target_env = "musl"))]
use std::os::fd::AsRawFd;
#[cfg(not(target_env = "musl"))]
use std::{ffi::OsStr, os::unix::ffi::OsStrExt as _, path::Path};

use fspy_seccomp_unotify::{payload::SeccompPayload, target::install_target};
#[cfg(not(target_env = "musl"))]
use memmap2::Mmap;
#[cfg(not(target_env = "musl"))]
use nix::{errno::Errno, libc};

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

pub struct PreExec(SeccompPayload);
impl PreExec {
    /// Installs the seccomp unotify filter for the current process.
    ///
    /// # Errors
    ///
    /// Returns an error if the seccomp filter installation fails.
    pub fn run(&self) -> nix::Result<()> {
        install_target(&self.0)
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
        let executable_fd = open_executable(Path::new(OsStr::from_bytes(&command.program)))?;
        admit_preload(&executable_fd)?;
        // SAFETY: The file descriptor is valid and we only read from the mapping.
        let executable_mmap = unsafe { Mmap::map(&executable_fd) }.map_err(|io_error| {
            nix::Error::try_from(io_error).unwrap_or(nix::Error::UnknownErrno)
        })?;
        if elf::is_dynamically_linked_to_libc(executable_mmap)? {
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
            return Ok(None);
        }
    }

    command
        .envs
        .retain(|(name, _)| name != LD_PRELOAD && name != PAYLOAD_ENV_NAME);
    Ok(Some(PreExec(
        encoded_payload.payload.seccomp_payload.clone(),
    )))
}

#[cfg(all(test, not(target_env = "musl")))]
mod tests {
    use std::{fs, os::unix::fs::PermissionsExt};

    use nix::errno::Errno;

    use super::{admit_preload, open_executable};

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
}
