//! Executable admission shared by the supervisor and descendant hooks.
#[cfg(target_os = "macos")]
use std::io::Read;
use std::{fs, path::Path};

use crate::diagnostic::{Code, Error, Result};

pub fn validate(path: &Path) -> Result<()> {
    let path = fs::canonicalize(path).map_err(|e| {
        Error::new(
            if e.kind() == std::io::ErrorKind::NotFound {
                Code::PnportCommandNotFound
            } else {
                Code::PnportCommandNotExecutable
            },
            "Cannot access the requested executable.",
        )
    })?;
    #[cfg(not(unix))]
    let _ = &path;
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let meta = fs::metadata(&path).map_err(|_| invalid())?;
        if meta.mode() & 0o6000 != 0 || meta.mode() & 0o111 == 0 {
            return Err(invalid());
        }
    }
    #[cfg(target_os = "macos")]
    {
        if ["/bin", "/sbin", "/usr/bin", "/usr/sbin", "/System"]
            .iter()
            .any(|prefix| path.starts_with(prefix))
        {
            return Err(protected());
        }
        let mut file = fs::File::open(path).map_err(|_| invalid())?;
        let mut header = [0u8; 32];
        file.read_exact(&mut header).map_err(|_| invalid())?;
        // A direct script would pass through an unchecked kernel-selected
        // interpreter. Admit only native images until the complete shebang
        // chain can be checked without executable substitution.
        if &header[..4] != b"\xcf\xfa\xed\xfe" {
            return Err(protected());
        }
        let cpu = u32::from_le_bytes(header[4..8].try_into().unwrap());
        let expected = if cfg!(target_arch = "aarch64") {
            0x0100000c
        } else {
            0x01000007
        };
        if cpu != expected {
            return Err(protected());
        }
        let count = u32::from_le_bytes(header[16..20].try_into().unwrap()) as usize;
        let size = u32::from_le_bytes(header[20..24].try_into().unwrap()) as usize;
        if size > 16 * 1024 * 1024 || count > size / 8 {
            return Err(invalid());
        }
        let mut commands = vec![0; size];
        file.read_exact(&mut commands).map_err(|_| invalid())?;
        let mut offset = 0usize;
        for _ in 0..count {
            let cmd = commands.get(offset..offset + 8).ok_or_else(invalid)?;
            let id = u32::from_le_bytes(cmd[..4].try_into().unwrap());
            let length = u32::from_le_bytes(cmd[4..8].try_into().unwrap()) as usize;
            if length < 8 {
                return Err(invalid());
            }
            let command = commands
                .get(offset..offset.checked_add(length).ok_or_else(invalid)?)
                .ok_or_else(invalid)?;
            if id == 0x19 && command.get(8..18) == Some(b"__RESTRICT") {
                return Err(protected());
            }
            if id == 0x1d {
                use std::io::{Seek, SeekFrom};
                if command.len() < 16 {
                    return Err(invalid());
                }
                let start = u32::from_le_bytes(command[8..12].try_into().unwrap());
                let length = u32::from_le_bytes(command[12..16].try_into().unwrap()) as usize;
                if length > 16 * 1024 * 1024 {
                    return Err(invalid());
                }
                file.seek(SeekFrom::Start(start as u64))
                    .map_err(|_| invalid())?;
                let mut signature = vec![0u8; length];
                file.read_exact(&mut signature).map_err(|_| invalid())?;
                check_signature(&signature)?;
            }
            offset += length;
        }
    }
    Ok(())
}
#[cfg(unix)]
fn invalid() -> Error {
    Error::new(
        Code::PnportCommandNotExecutable,
        "The executable is malformed, protected, or has no execute permission.",
    )
}
#[cfg(target_os = "macos")]
fn protected() -> Error {
    Error::new(
        Code::PnportUnsupportedOperation,
        "This executable or interpreter cannot be safely injected by this build; use a compatible \
         native executable. System executables are never replaced.",
    )
}
#[cfg(target_os = "macos")]
fn check_signature(bytes: &[u8]) -> Result<()> {
    fn number(bytes: &[u8], offset: usize) -> Result<u32> {
        Ok(u32::from_be_bytes(
            bytes
                .get(offset..offset + 4)
                .ok_or_else(invalid)?
                .try_into()
                .unwrap(),
        ))
    }
    if number(bytes, 0)? != 0xfade0cc0 {
        return Err(invalid());
    }
    let count = number(bytes, 8)? as usize;
    if count > bytes.len() / 8 {
        return Err(invalid());
    }
    for i in 0..count {
        let start = number(bytes, 16 + i * 8)? as usize;
        if number(bytes, start)? == 0xfade0c02 {
            let flags = number(bytes, start + 12)?;
            // Hardened runtime, restricted image or library validation requires
            // entitlement-specific admission; never optimistically inject it.
            if flags & (0x10000 | 0x800 | 0x2000) != 0 {
                return Err(protected());
            }
        }
    }
    Ok(())
}
