//! Executable admission shared by the supervisor and descendant hooks.
use std::{
    ffi::{OsStr, OsString},
    fs,
    io::Read as _,
    path::{Path, PathBuf},
};

use crate::{
    diagnostic::{Code, Error, Result},
    view::{Translation, View},
};

/// A resolved execution keeps script arguments in the logical PnP namespace.
pub struct Prepared {
    pub program: PathBuf,
    pub args: Vec<OsString>,
}

pub fn prepare(
    view: &mut View,
    path: &Path,
    args: &[OsString],
    search_path: Option<&OsStr>,
) -> Result<Prepared> {
    prepare_with_translation(path, args, search_path, |path| view.translate(path))
}

/// Resolve a native image while allowing an interposer to release its runtime
/// lock before filesystem and signature inspection call back into libc hooks.
pub fn prepare_with_translation(
    path: &Path,
    args: &[OsString],
    search_path: Option<&OsStr>,
    mut translate: impl FnMut(&Path) -> Result<Translation>,
) -> Result<Prepared> {
    let mut path = path.to_owned();
    let mut args = args.to_vec();
    for _ in 0..8 {
        let translation = translate(&path)?;
        let mut prefix = Vec::new();
        fs::File::open(&translation.physical)
            .map_err(access_error)?
            .take(4097)
            .read_to_end(&mut prefix)
            .map_err(|_| invalid())?;
        if !prefix.starts_with(b"#!") {
            validate(&translation.physical)?;
            return Ok(Prepared {
                program: translation.physical,
                args,
            });
        }
        executable_permissions(&translation.physical)?;
        let end = prefix
            .iter()
            .position(|b| *b == b'\n')
            .ok_or_else(invalid)?;
        let line = std::str::from_utf8(&prefix[2..end])
            .map_err(|_| invalid())?
            .trim();
        let mut fields = line.splitn(2, char::is_whitespace);
        let interpreter = fields
            .next()
            .filter(|s| !s.is_empty())
            .ok_or_else(invalid)?;
        let argument = fields.next().map(str::trim).filter(|s| !s.is_empty());
        let mut interpreter_args = Vec::new();
        path = if interpreter == "/usr/bin/env" {
            // env in a shebang declares PATH-based interpreter selection. Resolve
            // that declaration directly; never execute or replace protected env.
            let argument = argument.ok_or_else(invalid)?;
            let words: Vec<_> = if let Some(split) = argument.strip_prefix("-S ") {
                shell_words::split(split).map_err(|_| invalid())?
            } else {
                vec![argument.to_owned()]
            };
            let name = words
                .first()
                .filter(|s| {
                    !s.starts_with('-') && !s.contains('=') && !s.contains(char::is_whitespace)
                })
                .ok_or_else(invalid)?;
            interpreter_args.extend(words.iter().skip(1).map(OsString::from));
            find_interpreter(name.as_ref(), search_path)?
        } else {
            if !Path::new(interpreter).is_absolute() {
                return Err(invalid());
            }
            if let Some(argument) = argument {
                interpreter_args.push(argument.into());
            }
            PathBuf::from(interpreter)
        };
        interpreter_args.push(translation.logical.into_os_string());
        interpreter_args.extend(args);
        args = interpreter_args;
    }
    Err(Error::new(
        Code::PnportUnsupportedOperation,
        "The interpreter chain is cyclic or too deep.",
    ))
}

pub fn find_interpreter(name: &OsStr, search_path: Option<&OsStr>) -> Result<PathBuf> {
    find_on_path(
        name,
        search_path,
        &std::env::current_dir().map_err(|_| invalid())?,
    )
    .ok_or_else(|| {
        Error::new(
            Code::PnportCommandNotFound,
            "The requested interpreter is not executable on PATH.",
        )
    })
}

pub fn find_on_path(name: &OsStr, search_path: Option<&OsStr>, cwd: &Path) -> Option<PathBuf> {
    for directory in std::env::split_paths(search_path?) {
        let candidate = cwd.join(directory).join(name);
        if !candidate.is_file() {
            continue;
        }
        #[cfg(unix)]
        {
            use std::{ffi::CString, os::unix::ffi::OsStrExt};
            let Ok(path) = CString::new(candidate.as_os_str().as_bytes()) else {
                continue;
            };
            if unsafe { libc::access(path.as_ptr(), libc::X_OK) } != 0 {
                continue;
            }
        }
        // Injection admission happens after PATH selection. A protected, but
        // executable, candidate must fail explicitly rather than select a
        // different program from a later directory.
        return Some(candidate);
    }
    None
}

fn executable_permissions(path: &Path) -> Result<()> {
    let meta = fs::metadata(path).map_err(access_error)?;
    if !meta.is_file() {
        return Err(invalid());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if meta.mode() & 0o6000 != 0 || meta.mode() & 0o111 == 0 {
            return Err(invalid());
        }
    }
    Ok(())
}

pub fn validate(path: &Path) -> Result<PathBuf> {
    let path = fs::canonicalize(path).map_err(access_error)?;
    executable_permissions(&path)?;
    #[cfg(target_os = "macos")]
    {
        if ["/bin", "/sbin", "/usr/bin", "/usr/sbin", "/System"]
            .iter()
            .any(|prefix| path.starts_with(prefix))
        {
            return Err(protected());
        }
        let mut file = fs::File::open(&path).map_err(access_error)?;
        let mut header = [0u8; 32];
        file.read_exact(&mut header).map_err(|_| invalid())?;
        use std::io::{Seek, SeekFrom};
        let expected = if cfg!(target_arch = "aarch64") {
            0x0100000c
        } else {
            0x01000007
        };
        let mut slice_offset = 0u64;
        let mut slice_length = file.metadata().map_err(|_| invalid())?.len();
        if matches!(&header[..4], b"\xca\xfe\xba\xbe" | b"\xca\xfe\xba\xbf") {
            let count = u32::from_be_bytes(header[4..8].try_into().unwrap());
            if count > 64 {
                return Err(invalid());
            }
            let wide = header[3] == 0xbf;
            file.seek(SeekFrom::Start(8)).map_err(|_| invalid())?;
            let mut selected = None;
            for _ in 0..count {
                let mut entry = vec![0u8; if wide { 32 } else { 20 }];
                file.read_exact(&mut entry).map_err(|_| invalid())?;
                if u32::from_be_bytes(entry[..4].try_into().unwrap()) == expected {
                    if selected.is_some() {
                        return Err(invalid());
                    }
                    let offset = if wide {
                        u64::from_be_bytes(entry[8..16].try_into().unwrap())
                    } else {
                        u32::from_be_bytes(entry[8..12].try_into().unwrap()) as u64
                    };
                    let length = if wide {
                        u64::from_be_bytes(entry[16..24].try_into().unwrap())
                    } else {
                        u32::from_be_bytes(entry[12..16].try_into().unwrap()) as u64
                    };
                    if offset
                        .checked_add(length)
                        .is_none_or(|end| end > slice_length)
                        || length < 32
                    {
                        return Err(invalid());
                    }
                    selected = Some((offset, length));
                }
            }
            (slice_offset, slice_length) = selected.ok_or_else(protected)?;
            file.seek(SeekFrom::Start(slice_offset))
                .map_err(|_| invalid())?;
            file.read_exact(&mut header).map_err(|_| invalid())?;
        }
        if &header[..4] != b"\xcf\xfa\xed\xfe"
            || u32::from_le_bytes(header[4..8].try_into().unwrap()) != expected
        {
            return Err(protected());
        }
        let count = u32::from_le_bytes(header[16..20].try_into().unwrap()) as usize;
        let size = u32::from_le_bytes(header[20..24].try_into().unwrap()) as usize;
        if size > 16 * 1024 * 1024 || count > size / 8 || 32 + size as u64 > slice_length {
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
                if length > 16 * 1024 * 1024 || u64::from(start) + length as u64 > slice_length {
                    return Err(invalid());
                }
                file.seek(SeekFrom::Start(slice_offset + start as u64))
                    .map_err(|_| invalid())?;
                let mut signature = vec![0u8; length];
                file.read_exact(&mut signature).map_err(|_| invalid())?;
                check_signature(&signature)?;
                use core_foundation::url::CFURL;
                use security_framework::os::macos::code_signing::{
                    Flags, SecRequirement, SecStaticCode,
                };
                let url = CFURL::from_path(&path, false).ok_or_else(invalid)?;
                let code = SecStaticCode::from_path(&url, Flags::NONE).map_err(|_| invalid())?;
                let requirement: SecRequirement = "true".parse().map_err(|_| invalid())?;
                code.check_validity(Flags::NO_NETWORK_ACCESS, &requirement)
                    .map_err(|_| invalid())?;
            }
            offset += length;
        }
    }
    Ok(path)
}

#[cfg(target_os = "macos")]
pub struct LaunchAdmission {
    pub path: PathBuf,
    device: u64,
    inode: u64,
}

#[cfg(target_os = "macos")]
impl LaunchAdmission {
    pub fn new(path: &Path) -> Result<Self> {
        use std::os::unix::fs::MetadataExt;

        let path = validate(path)?;
        let metadata = fs::metadata(&path).map_err(access_error)?;
        Ok(Self {
            path,
            device: metadata.dev(),
            inode: metadata.ino(),
        })
    }

    /// Recheck the selected image after preparing argv and environment, at
    /// the last point before a pathname-based macOS exec or spawn call.
    pub fn verify_at_launch(&self) -> Result<()> {
        use std::os::unix::fs::MetadataExt;

        if validate(&self.path)? != self.path {
            return Err(invalid());
        }
        let metadata = fs::metadata(&self.path).map_err(access_error)?;
        if metadata.dev() != self.device || metadata.ino() != self.inode {
            return Err(invalid());
        }
        Ok(())
    }
}

fn access_error(error: std::io::Error) -> Error {
    Error::new(
        if error.kind() == std::io::ErrorKind::NotFound {
            Code::PnportCommandNotFound
        } else {
            Code::PnportCommandNotExecutable
        },
        "Cannot access the requested executable.",
    )
}

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
    let mut flags = 0;
    let mut entitlements = None;
    for i in 0..count {
        let start = number(bytes, 16 + i * 8)? as usize;
        let length = number(bytes, start + 4)? as usize;
        if length < 8 {
            return Err(invalid());
        }
        let blob = bytes
            .get(start..start.checked_add(length).ok_or_else(invalid)?)
            .ok_or_else(invalid)?;
        match number(blob, 0)? {
            0xfade0c02 => flags |= number(blob, 12)?,
            0xfade7171 => {
                entitlements =
                    Some(plist::Value::from_reader_xml(&blob[8..]).map_err(|_| invalid())?)
            }
            _ => {}
        }
    }
    if flags & 0x800 != 0 {
        return Err(protected());
    }
    let allowed = |key: &str| {
        entitlements
            .as_ref()
            .and_then(plist::Value::as_dictionary)
            .and_then(|d| d.get(key))
            .and_then(plist::Value::as_boolean)
            == Some(true)
    };
    if flags & 0x10000 != 0
        && (!allowed("com.apple.security.cs.allow-dyld-environment-variables")
            || !allowed("com.apple.security.cs.disable-library-validation"))
        || flags & 0x2000 != 0 && !allowed("com.apple.security.cs.disable-library-validation")
    {
        return Err(protected());
    }
    Ok(())
}

#[cfg(all(test, target_os = "macos"))]
mod tests {
    use std::os::unix::fs::symlink;

    use super::*;

    #[test]
    fn returns_the_canonical_admitted_image() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("tool.c");
        let executable = directory.path().join("tool");
        let alias = directory.path().join("tool-alias");
        fs::write(&source, "int main(void) { return 0; }\n").expect("write fixture");
        assert!(std::process::Command::new("cc")
            .arg(&source)
            .arg("-o")
            .arg(&executable)
            .status()
            .expect("compile fixture")
            .success());
        symlink(&executable, &alias).expect("create executable alias");
        assert_eq!(
            validate(&alias).expect("admit alias"),
            fs::canonicalize(executable).expect("canonicalize fixture")
        );
    }

    #[test]
    fn launch_admission_rejects_a_replaced_image() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("tool.c");
        let executable = directory.path().join("tool");
        let replacement = directory.path().join("replacement");
        fs::write(&source, "int main(void) { return 0; }\n").expect("write fixture");
        assert!(std::process::Command::new("cc")
            .arg(&source)
            .arg("-o")
            .arg(&executable)
            .status()
            .expect("compile fixture")
            .success());
        let admission = LaunchAdmission::new(&executable).expect("admit executable");
        fs::copy(&executable, &replacement).expect("copy valid replacement");
        fs::rename(&replacement, &executable).expect("replace admitted inode");
        assert!(validate(&executable).is_ok());
        assert_eq!(
            admission.verify_at_launch().unwrap_err().code,
            Code::PnportCommandNotExecutable
        );
    }

    #[test]
    fn truncated_entitlement_blob_is_rejected_without_panicking() {
        let signature: Vec<u8> = [0xfade0cc0u32, 28, 1, 5, 20, 0xfade7171, 4]
            .into_iter()
            .flat_map(u32::to_be_bytes)
            .collect();
        assert_eq!(
            check_signature(&signature).unwrap_err().code,
            Code::PnportCommandNotExecutable
        );
    }
}
