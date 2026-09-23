use std::{
    convert::Infallible,
    ffi::OsStr,
    fs,
    os::unix::ffi::{OsStrExt, OsStringExt},
    path::{Path, absolute},
    process::Command,
};

use nix::errno::Errno;

use crate::{
    exec::{Exec, append_path_env, ensure_env},
    payload::{EncodedPayload, PAYLOAD_ENV_NAME},
};

pub struct PreExec(Infallible);
impl PreExec {
    /// Runs pre-exec operations.
    ///
    /// # Errors
    ///
    /// This type is uninhabited, so the method can never return an error.
    pub const fn run(&self) -> nix::Result<()> {
        match self.0 {}
    }
}

fn admit_injection(program: &Path) -> nix::Result<()> {
    // Resolve symlinks and parent components before checking whether dyld will
    // ignore the preload for the executable that the kernel actually opens.
    let program = fs::canonicalize(program)
        .map_err(|error| error.raw_os_error().map_or(Errno::EIO, Errno::from_raw))?;
    if ["/bin", "/sbin", "/usr/bin", "/usr/sbin", "/System"]
        .iter()
        .any(|prefix| program.starts_with(prefix))
    {
        return Err(Errno::ENOTSUP);
    }
    Ok(())
}

/// Configure the pnport supervisor's already-admitted macOS command with the
/// forked fspy preload. The caller owns exit and input-watch supervision.
///
/// # Errors
///
/// Returns `ENOTSUP` when the executable is protected from dyld interposition,
/// or the filesystem error when its canonical path cannot be resolved.
pub fn configure_pnport_command(
    command: &mut Command,
    program: &Path,
    preload: &Path,
) -> nix::Result<()> {
    admit_injection(program)?;
    command.env("DYLD_INSERT_LIBRARIES", preload);
    Ok(())
}

pub fn handle_exec(
    command: &mut Exec,
    encoded_payload: &EncodedPayload,
) -> nix::Result<Option<PreExec>> {
    const DYLD_INSERT_LIBRARIES: &[u8] = b"DYLD_INSERT_LIBRARIES";

    if command.program.first() != Some(&b'/') {
        let program =
            absolute(OsStr::from_bytes(&command.program)).expect("Failed to get absolute path");
        command.program = program.into_os_string().into_vec().into();
    }

    let program_path = Path::new(OsStr::from_bytes(&command.program));
    // Protected system executables cannot accept DYLD interposition. Running
    // them without a hook would claim a complete access trace.
    admit_injection(program_path)?;

    append_path_env(
        &mut command.envs,
        DYLD_INSERT_LIBRARIES,
        encoded_payload.payload.preload_path.as_os_str().as_bytes(),
    );
    ensure_env(
        &mut command.envs,
        PAYLOAD_ENV_NAME,
        encoded_payload.encoded_string,
    )?;
    Ok(None)
}

#[cfg(test)]
mod tests {
    use std::{os::unix::fs::symlink, path::Path};

    use nix::errno::Errno;

    use super::admit_injection;

    #[test]
    fn protected_executable_aliases_cannot_bypass_injection_admission() {
        let directory = tempfile::tempdir().expect("create temporary directory");
        let alias = directory.path().join("protected-executable");
        symlink("/usr/bin/env", &alias).expect("create executable symlink");

        assert_eq!(admit_injection(&alias), Err(Errno::ENOTSUP));
        assert_eq!(
            admit_injection(Path::new("/private/tmp/../../usr/bin/env")),
            Err(Errno::ENOTSUP)
        );
        assert_eq!(
            admit_injection(&directory.path().join("missing")),
            Err(Errno::ENOENT)
        );
    }
}
