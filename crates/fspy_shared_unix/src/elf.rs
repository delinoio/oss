use std::{
    ffi::{CStr, OsStr},
    os::unix::ffi::OsStrExt as _,
    path::Path,
};

use bstr::BStr;
use elf::{ElfBytes, abi::PT_INTERP, endian::AnyEndian};

/// Checks whether the given ELF executable is dynamically linked to libc.
///
/// # Errors
///
/// Returns `ENOEXEC` if the binary cannot be parsed as a valid ELF file.
pub fn is_dynamically_linked_to_libc(executable: impl AsRef<[u8]>) -> nix::Result<bool> {
    let executable = executable.as_ref();
    let Some(interp) = get_interp(executable)? else {
        return Ok(false);
    };
    let Some(interp_filename) = Path::new(OsStr::from_bytes(interp)).file_name() else {
        return Ok(false);
    };
    Ok(is_host_libc_loader(interp_filename.as_bytes()))
}

fn is_host_libc_loader(filename: &[u8]) -> bool {
    // The preload is built against this host's glibc ABI. A foreign loader
    // (notably ld-musl-*) must use seccomp instead of loading that library.
    #[cfg(target_arch = "x86_64")]
    const HOST_LOADER: &[u8] = b"ld-linux-x86-64.so.2";
    #[cfg(target_arch = "aarch64")]
    const HOST_LOADER: &[u8] = b"ld-linux-aarch64.so.1";
    #[cfg(not(any(target_arch = "x86_64", target_arch = "aarch64")))]
    const HOST_LOADER: &[u8] = b"";
    filename == HOST_LOADER
}

fn get_interp(executable: &[u8]) -> nix::Result<Option<&BStr>> {
    let elf =
        ElfBytes::<'_, AnyEndian>::minimal_parse(executable).map_err(|_| nix::Error::ENOEXEC)?;
    let Some(headers) = elf.segments() else {
        return Ok(None);
    };

    let Some(interp_header) = headers
        .into_iter()
        .find(|header| header.p_type == PT_INTERP)
    else {
        return Ok(None);
    };
    let Ok(interp) = elf.segment_data(&interp_header) else {
        return Err(nix::Error::ENOEXEC);
    };

    let interp = CStr::from_bytes_until_nul(interp).map_or(interp, CStr::to_bytes);
    Ok(Some(BStr::new(interp)))
}

#[cfg(test)]
mod tests {
    use std::fs::read;

    use super::*;
    #[test]
    fn dynamic_executable() {
        assert!(is_dynamically_linked_to_libc(read("/bin/sh").unwrap()).unwrap());
    }
    #[test]
    fn static_executable() {
        let cat = read("/bin/cat").unwrap();
        let ld_so_path = get_interp(&cat).unwrap().unwrap();

        assert!(
            !is_dynamically_linked_to_libc(read(OsStr::from_bytes(ld_so_path)).unwrap()).unwrap()
        );
    }

    #[test]
    fn foreign_libc_loaders_do_not_receive_the_host_preload() {
        assert!(!is_host_libc_loader(b"ld-musl-x86_64.so.1"));
        assert!(!is_host_libc_loader(b"ld-musl-aarch64.so.1"));
        assert!(!is_host_libc_loader(b"ld-linux-other.so.2"));
    }
}
