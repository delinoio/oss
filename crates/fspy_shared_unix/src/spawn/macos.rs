use std::{
    convert::Infallible,
    ffi::OsStr,
    os::unix::ffi::{OsStrExt, OsStringExt},
    path::{Path, PathBuf, absolute},
};

use nix::errno::Errno;
use pnport_core::{diagnostic::Code, executable::LaunchAdmission};

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

fn admit_injection(program: &Path) -> nix::Result<PathBuf> {
    // The shared pnport admission inspects the canonical Mach-O slice,
    // signature, and hardened-runtime entitlements before either fspy or
    // pnport claims a complete interposed trace.
    pnport_core::executable::validate(program).map_err(|error| match error.code {
        Code::PnportCommandNotFound => Errno::ENOENT,
        Code::PnportCommandNotExecutable => Errno::EACCES,
        Code::PnportUnsupportedOperation => Errno::ENOTSUP,
        _ => Errno::EIO,
    })
}

/// Configure the pnport supervisor's already-admitted macOS command with the
/// forked fspy preload. The caller owns exit and input-watch supervision.
///
/// # Errors
///
/// Returns `ENOTSUP` when the executable is protected from dyld interposition,
/// `ENOENT` when it cannot be found, or `EACCES` when it is not executable or
/// its signed image cannot be validated.
pub fn admit_pnport_program(program: &Path) -> nix::Result<LaunchAdmission> {
    LaunchAdmission::new(program).map_err(|error| match error.code {
        Code::PnportCommandNotFound => Errno::ENOENT,
        Code::PnportCommandNotExecutable => Errno::EACCES,
        Code::PnportUnsupportedOperation => Errno::ENOTSUP,
        _ => Errno::EIO,
    })
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
    command.program = admit_injection(program_path)?
        .into_os_string()
        .into_vec()
        .into();

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
    use std::{fs, os::unix::fs::symlink, path::Path, process::Command};

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

    #[test]
    fn hardened_executable_requires_injection_entitlements() {
        let directory = tempfile::tempdir().expect("create temporary directory");
        let source = directory.path().join("tool.c");
        let executable = directory.path().join("tool");
        fs::write(&source, "int main(void) { return 0; }\n").expect("write source");
        assert!(
            Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile fixture")
                .success()
        );
        let canonical = fs::canonicalize(&executable).expect("canonicalize fixture");
        let alias = directory.path().join("tool-alias");
        symlink(&executable, &alias).expect("create executable alias");
        assert_eq!(admit_injection(&alias), Ok(canonical.clone()));
        assert_eq!(admit_injection(&executable), Ok(canonical.clone()));

        assert!(
            Command::new("codesign")
                .args(["--force", "--sign", "-", "--options", "runtime"])
                .arg(&executable)
                .status()
                .expect("sign hardened fixture")
                .success()
        );
        assert_eq!(admit_injection(&executable), Err(Errno::ENOTSUP));

        let entitlements = directory.path().join("entitlements.plist");
        fs::write(
            &entitlements,
            "<?xml version=\"1.0\"?><plist \
             version=\"1.0\"><dict><key>com.apple.security.cs.allow-dyld-environment-variables</\
             key><true/><key>com.apple.security.cs.disable-library-validation</key><true/></\
             dict></plist>",
        )
        .expect("write entitlements");
        assert!(
            Command::new("codesign")
                .args([
                    "--force",
                    "--sign",
                    "-",
                    "--options",
                    "runtime",
                    "--entitlements"
                ])
                .arg(&entitlements)
                .arg(&executable)
                .status()
                .expect("sign injectable fixture")
                .success()
        );
        assert_eq!(admit_injection(&executable), Ok(canonical));
    }
}
