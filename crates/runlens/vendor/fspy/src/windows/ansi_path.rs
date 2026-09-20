//! Detours accepts an ANSI DLL name. Never allow default-character or best-fit
//! conversion to name a different file; see PATCHES.md for removal criteria.
use std::{ffi::CString, io, os::windows::ffi::OsStrExt, path::Path};
use winsafe::co::{CP, MBC, WC};

fn encode_exact(wide: &[u16], code_page: CP) -> io::Result<CString> {
    let bytes = winsafe::WideCharToMultiByte(code_page, WC::NoValue, wide, None, None)
        .map_err(|e| io::Error::from_raw_os_error(e.raw().cast_signed()))?;
    let restored = winsafe::MultiByteToWideChar(code_page, MBC::NoValue, &bytes)
        .map_err(|e| io::Error::from_raw_os_error(e.raw().cast_signed()))?;
    if restored != wide {
        return Err(io::Error::new(io::ErrorKind::Unsupported, "collector path is not representable in the active Windows code page"));
    }
    CString::new(bytes).map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "collector path contains NUL"))
}

pub(super) fn encode_path(path: &Path) -> io::Result<CString> {
    let wide = path.as_os_str().encode_wide().collect::<Vec<_>>();
    match encode_exact(&wide, CP::ACP) {
        Ok(encoded) => Ok(encoded),
        Err(error) if error.kind() == io::ErrorKind::Unsupported => {
            // A real short name can preserve Unicode locations on legacy ACPs.
            // Volumes may disable 8.3 names; absence is an explicit prelaunch
            // Unsupported error, never a lossy DLL path sent to the loader.
            let terminated = wide.iter().copied().chain([0]).collect::<Vec<_>>();
            let mut short = vec![0u16; 32768];
            let count = unsafe { winapi::um::fileapi::GetShortPathNameW(terminated.as_ptr(), short.as_mut_ptr(), short.len() as u32) } as usize;
            if count == 0 || count >= short.len() { return Err(error); }
            encode_exact(&short[..count], CP::ACP)
        }
        Err(error) => Err(error),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn dll_path_encoding_rejects_lossy_code_page_conversion() {
        for text in [r"C:\Users\ascii\collector.dll", r"C:\Users\René\collector.dll"] {
            let wide = text.encode_utf16().collect::<Vec<_>>();
            assert!(encode_exact(&wide, CP::WINDOWS_1252).is_ok());
        }
        for text in [r"C:\Users\한글\collector.dll", r"C:\Users\Ａ\collector.dll"] {
            let wide = text.encode_utf16().collect::<Vec<_>>();
            assert_eq!(encode_exact(&wide, CP::WINDOWS_1252).unwrap_err().kind(), io::ErrorKind::Unsupported);
            assert_eq!(encode_exact(&wide, CP::UTF8).unwrap().as_bytes(), text.as_bytes());
        }
        assert_eq!(encode_exact(&[65, 0, 66], CP::UTF8).unwrap_err().kind(), io::ErrorKind::InvalidInput);
    }
}
