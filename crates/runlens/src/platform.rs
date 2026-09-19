use std::{
    ffi::OsString,
    io::Read,
    path::{Path, PathBuf},
};

use serde::Serialize;

use crate::error::{Error, ErrorCode, Result};
#[derive(Debug, Serialize)]
pub struct Doctor {
    pub supported: bool,
    pub os: &'static str,
    pub architecture: &'static str,
    pub os_version: Option<String>,
    pub engine_revision: &'static str,
    pub checks: Vec<Check>,
}
#[derive(Debug, Serialize)]
pub struct Check {
    pub name: &'static str,
    pub passed: bool,
    pub guidance: &'static str,
}
pub fn doctor() -> Doctor {
    let mut checks = vec![
        Check {
            name: "host-family",
            passed: cfg!(any(
                target_os = "macos",
                target_os = "windows",
                all(target_os = "linux", target_env = "gnu")
            )),
            guidance: "Use macOS 13+, Windows 10 22H2+, or Ubuntu 22.04+ with glibc on x64/arm64.",
        },
        Check {
            name: "architecture",
            passed: cfg!(any(target_arch = "x86_64", target_arch = "aarch64")),
            guidance: "Install the native architecture artifact; mixed-architecture execution is \
                       unsupported.",
        },
    ];
    #[cfg(target_os = "linux")]
    {
        let available = std::fs::read_to_string("/proc/sys/kernel/seccomp/actions_avail")
            .is_ok_and(|s| s.split_whitespace().any(|s| s == "user_notif"));
        checks.push(Check {
            name: "seccomp-user-notification",
            passed: available,
            guidance: "The glibc host must permit seccomp user notifications and same-user \
                       process memory reads for static children; privileged tracing deployment is \
                       not supported.",
        });
    }
    #[cfg(target_os = "macos")]
    {
        let mut translated = 0i32;
        let mut len = std::mem::size_of_val(&translated);
        // SAFETY: sysctl writes at most the supplied integer buffer size.
        let result = unsafe {
            libc::sysctlbyname(
                c"sysctl.proc_translated".as_ptr(),
                (&mut translated as *mut i32).cast(),
                &mut len,
                std::ptr::null_mut(),
                0,
            )
        };
        checks.push(Check {
            name: "native-process",
            passed: result != 0 || translated == 0,
            guidance: "Run the native binary, not Rosetta; protected system tools cannot be \
                       substituted.",
        });
    }
    let os_version = os_version();
    #[cfg(target_os = "macos")]
    checks.push(Check {
        name: "minimum-os",
        passed: os_version
            .as_ref()
            .and_then(|s| s.split('.').next()?.parse::<u32>().ok())
            .is_some_and(|v| v >= 13),
        guidance: "macOS 13 or newer is required.",
    });
    #[cfg(target_os = "windows")]
    checks.push(Check {
        name: "minimum-os",
        passed: os_version
            .as_ref()
            .and_then(|s| s.rsplit('.').next()?.parse::<u32>().ok())
            .is_some_and(|v| v >= 19045),
        guidance: "Windows 10 22H2 (build 19045) or newer is required.",
    });
    Doctor {
        supported: checks.iter().all(|c| c.passed),
        os: std::env::consts::OS,
        architecture: std::env::consts::ARCH,
        os_version,
        engine_revision: crate::model::ENGINE_REVISION,
        checks,
    }
}
pub fn os_version() -> Option<String> {
    #[cfg(target_os = "macos")]
    {
        let mut buffer = [0u8; 128];
        let mut size = buffer.len();
        // SAFETY: the writable byte array is exactly size bytes long.
        if unsafe {
            libc::sysctlbyname(
                c"kern.osproductversion".as_ptr(),
                buffer.as_mut_ptr().cast(),
                &mut size,
                std::ptr::null_mut(),
                0,
            )
        } == 0
        {
            return Some(
                String::from_utf8_lossy(&buffer[..size.saturating_sub(1).min(buffer.len())])
                    .into_owned(),
            );
        }
        None
    }
    #[cfg(target_os = "linux")]
    {
        let text = std::fs::read_to_string("/etc/os-release").ok()?;
        text.lines().find_map(|line| {
            line.strip_prefix("VERSION_ID=")
                .map(|s| s.trim_matches('"').to_owned())
        })
    }
    #[cfg(target_os = "windows")]
    {
        #[repr(C)]
        struct Version {
            size: u32,
            major: u32,
            minor: u32,
            build: u32,
            platform: u32,
            text: [u16; 128],
        }
        #[link(name = "ntdll")]
        unsafe extern "system" {
            fn RtlGetVersion(info: *mut Version) -> i32;
        }
        let mut info = Version {
            size: std::mem::size_of::<Version>() as u32,
            major: 0,
            minor: 0,
            build: 0,
            platform: 0,
            text: [0; 128],
        };
        // SAFETY: RtlGetVersion receives the documented writable OSVERSIONINFOW.
        if unsafe { RtlGetVersion(&mut info) } == 0 {
            Some(format!("{}.{}.{}", info.major, info.minor, info.build))
        } else {
            None
        }
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows", target_os = "linux")))]
    {
        None
    }
}
pub fn resolve(program: &str, env: &[(OsString, OsString)], cwd: &Path) -> Result<PathBuf> {
    let path = env
        .iter()
        .find(|(key, _)| key.to_string_lossy().eq_ignore_ascii_case("PATH"))
        .map(|(_, value)| value);
    which::which_in(program, path, cwd).map_err(|_| {
        Error::input("executable was not found in the selected PATH and working directory")
    })
}
pub fn preflight(program: &Path) -> Result<()> {
    if !doctor().supported {
        return Err(Error::new(
            ErrorCode::Unsupported,
            "host prerequisites are unavailable; run runlens doctor",
        ));
    }
    inspect_executable(program, 0)
}
fn inspect_executable(program: &Path, depth: usize) -> Result<()> {
    if depth > 8 {
        return Err(Error::new(
            ErrorCode::Unsupported,
            "interpreter nesting is unsupported",
        ));
    }
    let metadata =
        std::fs::metadata(program).map_err(|_| Error::input("executable cannot be inspected"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if metadata.permissions().mode() & 0o6000 != 0 {
            return Err(Error::new(
                ErrorCode::Unsupported,
                "set-user-ID and set-group-ID executables cannot be tracked",
            ));
        }
    }
    #[cfg(target_os = "macos")]
    {
        let path = std::fs::canonicalize(program)
            .map_err(|_| Error::input("executable path cannot be resolved"))?;
        if ["/bin", "/sbin", "/usr/bin", "/usr/sbin", "/System"]
            .iter()
            .any(|p| path.starts_with(p))
        {
            return Err(Error::new(
                ErrorCode::Unsupported,
                "protected macOS executable or interpreter; select an explicitly installed \
                 injectable tool",
            ));
        }
    }
    let mut file =
        std::fs::File::open(program).map_err(|_| Error::input("executable cannot be inspected"))?;
    let mut bytes = [0u8; 4096];
    let count = file
        .read(&mut bytes)
        .map_err(|_| Error::input("executable cannot be inspected"))?;
    let bytes = &bytes[..count];
    if bytes.starts_with(b"#!") {
        let line = bytes[2..].split(|b| *b == b'\n').next().unwrap_or_default();
        let interpreter = line
            .split(|b| b.is_ascii_whitespace())
            .find(|s| !s.is_empty())
            .ok_or_else(|| Error::input("invalid interpreter declaration"))?;
        let path = std::str::from_utf8(interpreter).map_err(|_| {
            Error::new(
                ErrorCode::Unsupported,
                "non-UTF-8 interpreter is unsupported",
            )
        })?;
        return inspect_executable(Path::new(path), depth + 1);
    }
    #[cfg(target_os = "linux")]
    {
        let machine = if cfg!(target_arch = "x86_64") {
            62
        } else {
            183
        };
        if bytes.len() < 20
            || !bytes.starts_with(b"\x7fELF")
            || bytes[4] != 2
            || bytes[5] != 1
            || u16::from_le_bytes([bytes[18], bytes[19]]) != machine
        {
            return Err(Error::new(
                ErrorCode::Unsupported,
                "executable is not a native 64-bit ELF image",
            ));
        }
    }
    #[cfg(target_os = "macos")]
    {
        let machine = if cfg!(target_arch = "x86_64") {
            0x01000007u32
        } else {
            0x0100000c
        };
        if crate::macho::protected(&mut file, machine).map_err(|_| {
            Error::new(
                ErrorCode::Unsupported,
                "executable Mach-O architecture or metadata is unsupported",
            )
        })? {
            return Err(Error::new(
                ErrorCode::Unsupported,
                "restricted, library-validated, or hardened macOS executable is unsupported",
            ));
        }
    }
    #[cfg(target_os = "windows")]
    {
        use std::io::{Seek, SeekFrom};
        if !bytes.starts_with(b"MZ") || bytes.len() < 64 {
            return Err(Error::new(
                ErrorCode::Unsupported,
                "use an explicit interpreter for scripts; a native PE executable is required",
            ));
        }
        let offset = u32::from_le_bytes(bytes[60..64].try_into().unwrap());
        let mut header = [0u8; 6];
        file.seek(SeekFrom::Start(offset.into()))
            .and_then(|_| file.read_exact(&mut header))
            .map_err(|_| Error::input("invalid PE executable"))?;
        let expected = if cfg!(target_arch = "x86_64") {
            0x8664
        } else {
            0xaa64
        };
        if header[..4] != [b'P', b'E', 0, 0]
            || u16::from_le_bytes([header[4], header[5]]) != expected
        {
            return Err(Error::new(
                ErrorCode::Unsupported,
                "mixed-architecture execution is unsupported",
            ));
        }
    }
    let _ = metadata;
    Ok(())
}

/// A passive executable identity: never run an extra tool-version command.
pub fn executable_sha256(
    path: &Path,
    cancel: &tokio_util::sync::CancellationToken,
) -> Option<String> {
    use sha2::{Digest, Sha256};
    let mut file = std::fs::File::open(path).ok()?;
    let before = file.metadata().ok()?;
    let mut hash = Sha256::new();
    let mut buffer = [0u8; 65536];
    loop {
        if cancel.is_cancelled() {
            return None;
        }
        let count = file.read(&mut buffer).ok()?;
        if count == 0 {
            break;
        }
        hash.update(&buffer[..count]);
    }
    let after = file.metadata().ok()?;
    if before.len() != after.len() || before.modified().ok() != after.modified().ok() {
        return None;
    }
    Some(hex::encode(hash.finalize()))
}
