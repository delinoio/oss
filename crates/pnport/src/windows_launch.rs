// SPDX-License-Identifier: Apache-2.0
//! Shared Windows injection preparation, included by the host and its DLL.
//! Adapted from the MIT fspy Windows runner; retain LICENSE.fspy and
//! LICENSE.detours in distributions containing this adapter.

use std::{
    ffi::CString,
    io,
    os::windows::ffi::{OsStrExt, OsStringExt},
    path::{Path, PathBuf},
    ptr,
};

use fspy_detours_sys::{
    DetourCopyPayloadToProcess, DetourFindPayloadEx, DetourUpdateProcessWithDll,
};
use pnport_core::{executable, windows::LaunchContext};
use winapi::{
    shared::{guiddef::GUID, ntdef::HANDLE},
    um::{
        fileapi::GetShortPathNameW,
        stringapiset::{MultiByteToWideChar, WideCharToMultiByte},
        winbase::QueryFullProcessImageNameW,
        winnls::CP_ACP,
    },
};

const PAYLOAD: GUID = GUID {
    Data1: 0x45c84510,
    Data2: 0x48ae,
    Data3: 0x4239,
    Data4: [0x8c, 0x42, 0xb2, 0xac, 0x72, 0xd4, 0x12, 0x7e],
};

fn ansi(units: &[u16]) -> io::Result<Option<CString>> {
    let count = i32::try_from(units.len()).map_err(|_| io::ErrorKind::InvalidInput)?;
    let length = unsafe {
        WideCharToMultiByte(
            CP_ACP,
            0,
            units.as_ptr(),
            count,
            ptr::null_mut(),
            0,
            ptr::null(),
            ptr::null_mut(),
        )
    };
    if length <= 0 {
        return Err(io::Error::last_os_error());
    }
    let mut bytes = vec![0; length as usize];
    if unsafe {
        WideCharToMultiByte(
            CP_ACP,
            0,
            units.as_ptr(),
            count,
            bytes.as_mut_ptr().cast(),
            length,
            ptr::null(),
            ptr::null_mut(),
        )
    } != length
    {
        return Err(io::Error::last_os_error());
    }
    let needed = unsafe {
        MultiByteToWideChar(CP_ACP, 0, bytes.as_ptr().cast(), length, ptr::null_mut(), 0)
    };
    if needed <= 0 {
        return Err(io::Error::last_os_error());
    }
    let mut roundtrip = vec![0; needed as usize];
    if unsafe {
        MultiByteToWideChar(
            CP_ACP,
            0,
            bytes.as_ptr().cast(),
            length,
            roundtrip.as_mut_ptr(),
            needed,
        )
    } != needed
    {
        return Err(io::Error::last_os_error());
    }
    if roundtrip != units {
        return Ok(None);
    }
    Ok(Some(
        CString::new(bytes).map_err(|_| io::ErrorKind::InvalidInput)?,
    ))
}

pub fn dll_path(path: &Path) -> io::Result<CString> {
    let mut wide: Vec<_> = path.as_os_str().encode_wide().collect();
    if let Some(value) = ansi(&wide)? {
        return Ok(value);
    }
    wide.push(0);
    let required = unsafe { GetShortPathNameW(wide.as_ptr(), ptr::null_mut(), 0) };
    if required == 0 || required > 32768 {
        return Err(io::Error::last_os_error());
    }
    let mut short = vec![0; required as usize];
    let written = unsafe { GetShortPathNameW(wide.as_ptr(), short.as_mut_ptr(), required) };
    if written == 0 || written >= required {
        return Err(io::Error::last_os_error());
    }
    ansi(&short[..written as usize])?.ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::Unsupported,
            "Detours requires a lossless ANSI or short DLL path",
        )
    })
}

#[allow(
    clippy::not_unsafe_ptr_arg_deref,
    reason = "Windows validates opaque process handles; no caller memory is dereferenced"
)]
pub fn inject(process: HANDLE, context: &LaunchContext) -> io::Result<()> {
    let mut name = vec![0; 32768];
    let mut size = name.len() as u32;
    if unsafe { QueryFullProcessImageNameW(process, 0, name.as_mut_ptr(), &mut size) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let image = PathBuf::from(std::ffi::OsString::from_wide(&name[..size as usize]));
    executable::validate(&image).map_err(|_| io::ErrorKind::Unsupported)?;
    let dll = dll_path(&context.artifact)?;
    let mut pointer = dll.as_ptr();
    if unsafe { DetourUpdateProcessWithDll(process, &mut pointer, 1) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let bytes = serde_json::to_vec(context).map_err(|_| io::ErrorKind::InvalidData)?;
    if unsafe {
        DetourCopyPayloadToProcess(
            process,
            &PAYLOAD,
            bytes.as_ptr().cast(),
            u32::try_from(bytes.len()).map_err(|_| io::ErrorKind::InvalidData)?,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

#[allow(
    dead_code,
    reason = "The shared payload reader is used by the DLL, not its host"
)]
pub fn context() -> io::Result<LaunchContext> {
    let mut size = 0;
    let payload = unsafe { DetourFindPayloadEx(&PAYLOAD, &mut size) };
    if payload.is_null() || size == 0 || size > 1024 * 1024 {
        return Err(io::ErrorKind::InvalidData.into());
    }
    let context: LaunchContext = serde_json::from_slice(unsafe {
        std::slice::from_raw_parts(payload.cast(), size as usize)
    })
    .map_err(|_| io::ErrorKind::InvalidData)?;
    if context.format != 1
        || !context.session.is_absolute()
        || !context.cache.is_absolute()
        || !context.artifact.is_absolute()
        || !pnport_core::launch::valid_token(&context.launch)
    {
        return Err(io::ErrorKind::InvalidData.into());
    }
    Ok(context)
}
