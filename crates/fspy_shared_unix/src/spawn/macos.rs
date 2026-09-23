use std::{
    convert::Infallible,
    ffi::OsStr,
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
    pub const fn run(&self) -> nix::Result<()> {
        match self.0 {}
    }
}

fn admit_injection(program: &Path) -> nix::Result<()> {
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
