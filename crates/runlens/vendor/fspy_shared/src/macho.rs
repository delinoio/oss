//! Bounded inspection of native Mach-O signing flags before executing a target.
//! Constants follow Apple's public xnu `osfmk/kern/cs_blobs.h` and Mach-O
//! loader.h.
use std::io::{self, Read, Seek, SeekFrom};
const MAX_HEADER_BYTES: usize = 1024 * 1024;
fn invalid() -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, "unsupported Mach-O metadata")
}
fn word(bytes: &[u8], offset: usize, big: bool) -> io::Result<u32> {
    let data: [u8; 4] = bytes
        .get(offset..offset.checked_add(4).ok_or_else(invalid)?)
        .ok_or_else(invalid)?
        .try_into()
        .map_err(|_| invalid())?;
    Ok(if big {
        u32::from_be_bytes(data)
    } else {
        u32::from_le_bytes(data)
    })
}
fn read_at(file: &mut (impl Read + Seek), offset: u64, size: usize) -> io::Result<Vec<u8>> {
    if size > MAX_HEADER_BYTES {
        return Err(invalid());
    }
    file.seek(SeekFrom::Start(offset))?;
    let mut bytes = vec![0; size];
    file.read_exact(&mut bytes)?;
    Ok(bytes)
}
/// Conservatively reject restricted, library-validated, or hardened code even
/// when optional entitlements might permit injection on a particular machine.
pub fn protected(file: &mut (impl Read + Seek), machine: u32) -> io::Result<bool> {
    let header = read_at(file, 0, 8)?;
    let magic = word(&header, 0, true)?;
    let base = match magic {
        0xcffaedfe => 0,
        0xcafebabe | 0xcafebabf => {
            let count = word(&header, 4, true)? as usize;
            if count > 64 {
                return Err(invalid());
            }
            let stride = if magic == 0xcafebabe { 20 } else { 32 };
            let architectures = read_at(file, 8, count * stride)?;
            let mut selected = None;
            for entry in architectures.chunks(stride) {
                if word(entry, 0, true)? == machine {
                    if selected.is_some() {
                        return Err(invalid());
                    }
                    selected = Some(if stride == 20 {
                        word(entry, 8, true)? as u64
                    } else {
                        u64::from_be_bytes(entry[8..16].try_into().map_err(|_| invalid())?)
                    });
                }
            }
            selected.ok_or_else(invalid)?
        }
        _ => return Err(invalid()),
    };
    let header = read_at(file, base, 32)?;
    if word(&header, 0, false)? != 0xfeedfacf || word(&header, 4, false)? != machine {
        return Err(invalid());
    }
    // arm64e uses pointer-authenticated ABI and cannot load our ordinary arm64
    // collector, even though its CPU type is shared with arm64.
    if machine == 0x0100_000c && word(&header, 8, false)? & 0x00ff_ffff == 2 {
        return Ok(true);
    }
    let count = word(&header, 16, false)? as usize;
    let bytes = word(&header, 20, false)? as usize;
    if count > 16384 {
        return Err(invalid());
    }
    let commands = read_at(file, base.checked_add(32).ok_or_else(invalid)?, bytes)?;
    let mut cursor = 0usize;
    for _ in 0..count {
        let command = word(&commands, cursor, false)?;
        let size = word(&commands, cursor + 4, false)? as usize;
        if size < 8 {
            return Err(invalid());
        }
        let data = commands
            .get(cursor..cursor.checked_add(size).ok_or_else(invalid)?)
            .ok_or_else(invalid)?;
        if command == 0x19
            && data
                .get(8..24)
                .is_some_and(|name| name.starts_with(b"__RESTRICT\0"))
        {
            return Ok(true);
        }
        if command == 0x1d {
            let offset = word(data, 8, false)? as u64;
            let size = word(data, 12, false)? as usize;
            let signature = read_at(file, base.checked_add(offset).ok_or_else(invalid)?, size)?;
            if signature_protected(&signature)? {
                return Ok(true);
            }
        }
        cursor += size;
    }
    if cursor != commands.len() {
        return Err(invalid());
    }
    Ok(false)
}
fn signature_protected(bytes: &[u8]) -> io::Result<bool> {
    if word(bytes, 0, true)? != 0xfade0cc0 || word(bytes, 4, true)? as usize > bytes.len() {
        return Err(invalid());
    }
    let count = word(bytes, 8, true)? as usize;
    if count > 4096 {
        return Err(invalid());
    }
    for index in 0..count {
        let offset = word(bytes, 16 + index * 8, true)? as usize;
        if word(bytes, offset, true)? == 0xfade0c02 {
            let flags = word(bytes, offset + 12, true)?;
            // CS_RESTRICT, CS_REQUIRE_LV and CS_RUNTIME are file-backed flags.
            if flags & (0x800 | 0x2000 | 0x10000) != 0 {
                return Ok(true);
            }
        }
    }
    Ok(false)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn signature_flags_and_untrusted_offsets_are_bounded() {
        let mut blob = vec![0u8; 36];
        for (offset, value) in [
            (0, 0xfade0cc0u32),
            (4, 36),
            (8, 1),
            (16, 20),
            (20, 0xfade0c02),
            (32, 2),
        ] {
            blob[offset..offset + 4].copy_from_slice(&value.to_be_bytes());
        }
        assert!(!signature_protected(&blob).unwrap());
        blob[32..36].copy_from_slice(&0x10000u32.to_be_bytes());
        assert!(signature_protected(&blob).unwrap());
        blob[16..20].copy_from_slice(&u32::MAX.to_be_bytes());
        assert!(signature_protected(&blob).is_err());
    }
}
