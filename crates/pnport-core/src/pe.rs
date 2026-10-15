// SPDX-License-Identifier: Apache-2.0
//! Bounded PE admission shared by the Windows host and injected runtime.

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Architecture {
    X64,
    Arm64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ImageKind {
    Executable,
    Library,
}

/// Admit native PE32+ images only. ARM64EC/ARM64X, managed-only images and
/// cross-architecture helper injection are outside the pnport contract.
pub fn admits(bytes: &[u8], architecture: Architecture, kind: ImageKind) -> bool {
    fn word(bytes: &[u8], offset: usize) -> Option<u16> {
        Some(u16::from_le_bytes(
            bytes.get(offset..offset.checked_add(2)?)?.try_into().ok()?,
        ))
    }
    fn dword(bytes: &[u8], offset: usize) -> Option<u32> {
        Some(u32::from_le_bytes(
            bytes.get(offset..offset.checked_add(4)?)?.try_into().ok()?,
        ))
    }
    let check = || -> Option<bool> {
        if bytes.get(..2)? != b"MZ" {
            return Some(false);
        }
        let offset = usize::try_from(dword(bytes, 0x3c)?).ok()?;
        if offset < 64 || bytes.get(offset..offset.checked_add(4)?)? != b"PE\0\0" {
            return Some(false);
        }
        let coff = offset.checked_add(4)?;
        let machine = word(bytes, coff)?;
        let sections = usize::from(word(bytes, coff + 2)?);
        let optional_size = usize::from(word(bytes, coff + 16)?);
        let characteristics = word(bytes, coff + 18)?;
        let optional = coff.checked_add(20)?;
        let section_table = optional.checked_add(optional_size)?;
        let end = section_table.checked_add(sections.checked_mul(40)?)?;
        if machine
            != match architecture {
                Architecture::X64 => 0x8664,
                Architecture::Arm64 => 0xaa64,
            }
            || sections == 0
            || sections > 128
            || optional_size < 112
            || end > bytes.len()
            || word(bytes, optional)? != 0x20b
            || characteristics & 0x0002 == 0
            || (characteristics & 0x2000 != 0) != (kind == ImageKind::Library)
        {
            return Some(false);
        }
        // CLR images can transfer control through a runtime with different
        // architecture/loader semantics. Do not imply support from an MZ
        // header.
        let directories = dword(bytes, optional + 108)?;
        if directories > 16 || optional_size < 112 + usize::try_from(directories).ok()? * 8 {
            return Some(false);
        }
        if directories > 14
            && (dword(bytes, optional + 112 + 14 * 8)? != 0
                || dword(bytes, optional + 116 + 14 * 8)? != 0)
        {
            return Some(false);
        }
        for section in 0..sections {
            let header = section_table + section * 40;
            let size = usize::try_from(dword(bytes, header + 16)?).ok()?;
            let start = usize::try_from(dword(bytes, header + 20)?).ok()?;
            if size != 0 && start.checked_add(size)? > bytes.len() {
                return Some(false);
            }
        }
        Some(true)
    };
    check().unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn image(machine: u16, library: bool) -> Vec<u8> {
        let mut bytes = vec![0; 512];
        bytes[..2].copy_from_slice(b"MZ");
        bytes[0x3c..0x40].copy_from_slice(&64u32.to_le_bytes());
        bytes[64..68].copy_from_slice(b"PE\0\0");
        bytes[68..70].copy_from_slice(&machine.to_le_bytes());
        bytes[70..72].copy_from_slice(&1u16.to_le_bytes());
        bytes[84..86].copy_from_slice(&240u16.to_le_bytes());
        bytes[86..88].copy_from_slice(&(if library { 0x2002u16 } else { 2 }).to_le_bytes());
        bytes[88..90].copy_from_slice(&0x20bu16.to_le_bytes());
        bytes[196..200].copy_from_slice(&16u32.to_le_bytes());
        bytes
    }

    #[test]
    fn admits_only_the_selected_native_image_kind() {
        for (machine, arch) in [(0x8664, Architecture::X64), (0xaa64, Architecture::Arm64)] {
            for (library, kind) in [(false, ImageKind::Executable), (true, ImageKind::Library)] {
                let bytes = image(machine, library);
                assert!(admits(&bytes, arch, kind));
                assert!(!admits(
                    &bytes,
                    if arch == Architecture::X64 {
                        Architecture::Arm64
                    } else {
                        Architecture::X64
                    },
                    kind
                ));
                assert!(!admits(
                    &bytes,
                    arch,
                    if library {
                        ImageKind::Executable
                    } else {
                        ImageKind::Library
                    }
                ));
                for length in 0..368 {
                    assert!(!admits(&bytes[..length], arch, kind));
                }
            }
        }
    }

    #[test]
    fn malformed_offsets_sections_and_managed_images_fail_closed() {
        for (offset, value) in [(0x3c, u32::MAX), (196, 17), (312, 1), (344, u32::MAX)] {
            let mut bytes = image(0x8664, false);
            bytes[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
            assert!(
                !admits(&bytes, Architecture::X64, ImageKind::Executable),
                "offset {offset}"
            );
        }
        assert!(!admits(
            &image(0xa641, false),
            Architecture::Arm64,
            ImageKind::Executable
        ));
    }
}
