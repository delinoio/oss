//! Runlens patch: preserve the executable, including unsupported system children.
//! Remove this patch only when upstream provides a no-substitution capability.
use std::{convert::Infallible, ffi::OsStr, os::unix::ffi::OsStrExt, path::Path};
use crate::{exec::{Exec, append_path_env, ensure_env}, payload::{EncodedPayload, PAYLOAD_ENV_NAME}};

pub struct PreExec(Infallible);
impl PreExec { pub const fn run(&self) -> nix::Result<()> { match self.0 {} } }

pub fn unsupported(path: &Path) -> bool {
    // Canonicalization catches aliases into SIP-protected system locations.
    let path = std::fs::canonicalize(path).unwrap_or_else(|_| path.to_owned());
    if ["/bin", "/sbin", "/usr/bin", "/usr/sbin", "/System"].iter().any(|root| path.starts_with(root)) { return true; }
    let machine = if cfg!(target_arch = "aarch64") { 0x0100_000c } else { 0x0100_0007 };
    // The same passive parser used for root preflight also covers hardened or
    // mixed-architecture descendants. Failure preserves execution but marks loss.
    std::fs::File::open(path).and_then(|mut file| fspy_shared::macho::protected(&mut file, machine)).unwrap_or(true)
}

pub fn handle_exec(command: &mut Exec, payload: &EncodedPayload) -> nix::Result<Option<PreExec>> {
    let path = Path::new(OsStr::from_bytes(&command.program));
    if unsupported(path) {
        // The caller records UNSUPPORTED before this child executes unchanged.
        // Remove only our inherited preload: leaving an arm64 collector in an
        // arm64e system child's DYLD list aborts that otherwise valid child.
        // Preserve every user-supplied library and the requested executable.
        let owned = payload.payload.preload_path.as_os_str().as_bytes();
        command.envs.retain_mut(|(name, value)| {
            if name.as_slice() == b"DYLD_INSERT_LIBRARIES" {
                if let Some(value) = value {
                    let remaining = value.split(|byte| *byte == b':').filter(|entry| *entry != owned).collect::<Vec<_>>().join(&b':');
                    if remaining.is_empty() { return false; }
                    *value = remaining.into();
                }
            }
            true
        });
        return Ok(None);
    }
    append_path_env(&mut command.envs, &b"DYLD_INSERT_LIBRARIES"[..], payload.payload.preload_path.as_os_str().as_bytes());
    ensure_env(&mut command.envs, PAYLOAD_ENV_NAME, payload.encoded_string)?;
    Ok(None)
}
