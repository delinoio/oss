// SPDX-License-Identifier: Apache-2.0
//! ReFS block cloning with `FSCTL_DUPLICATE_EXTENTS_TO_FILE`.

use std::{
    ffi::OsStr,
    fs::{File, OpenOptions},
    io,
    os::windows::{ffi::OsStrExt, io::AsRawHandle},
};

use anyhow::Result;
use windows_sys::Win32::{
    Foundation::{ERROR_INVALID_FUNCTION, ERROR_NOT_SAME_DEVICE, ERROR_NOT_SUPPORTED, HANDLE},
    Storage::FileSystem::{
        BY_HANDLE_FILE_INFORMATION, FILE_ATTRIBUTE_SPARSE_FILE, GetDiskFreeSpaceW,
        GetFileInformationByHandle, GetVolumeInformationByHandleW, GetVolumePathNameW,
    },
    System::{
        IO::DeviceIoControl,
        Ioctl::{
            FSCTL_DUPLICATE_EXTENTS_TO_FILE, FSCTL_GET_INTEGRITY_INFORMATION,
            FSCTL_SET_INTEGRITY_INFORMATION, FSCTL_SET_SPARSE,
        },
    },
};

use super::{
    Unsupported,
    chunks::{aligned_length, clone_chunks},
};
use crate::{Context, paths};

/// `DUPLICATE_EXTENTS_DATA`.
#[repr(C)]
pub(super) struct DuplicateExtents {
    pub file: HANDLE,
    pub source_offset: i64,
    pub target_offset: i64,
    pub byte_count: i64,
}

/// `FSCTL_GET_INTEGRITY_INFORMATION_BUFFER`.
#[repr(C)]
#[derive(Default)]
pub(super) struct IntegrityInformation {
    pub algorithm: u16,
    pub reserved: u16,
    pub flags: u32,
    pub chunk_size: u32,
    pub cluster_size: u32,
}

/// `FSCTL_SET_INTEGRITY_INFORMATION_BUFFER`.
#[repr(C)]
struct IntegritySettings {
    algorithm: u16,
    reserved: u16,
    flags: u32,
}

const SUPPORTS_BLOCK_REFCOUNTING: u32 = 0x0800_0000;

fn wide(path: &str) -> Vec<u16> {
    OsStr::new(path).encode_wide().chain(Some(0)).collect()
}

fn handle(file: &File) -> HANDLE {
    file.as_raw_handle() as HANDLE
}

/// Serial number of a ReFS volume with block refcounting.
fn volume(h: HANDLE) -> Result<u32> {
    let mut name = [0_u16; 32];
    let mut serial = 0_u32;
    let mut flags = 0_u32;
    // SAFETY: `h` is a live handle and every output pointer is valid.
    let ok = unsafe {
        GetVolumeInformationByHandleW(
            h,
            std::ptr::null_mut(),
            0,
            &raw mut serial,
            std::ptr::null_mut(),
            &raw mut flags,
            name.as_mut_ptr(),
            u32::try_from(name.len()).unwrap_or(0),
        )
    };
    if ok == 0 {
        return Err(io::Error::last_os_error().into());
    }
    let len = name.iter().position(|c| *c == 0).unwrap_or(name.len());
    let name = String::from_utf16_lossy(&name[..len]);
    if !name.eq_ignore_ascii_case("ReFS") || flags & SUPPORTS_BLOCK_REFCOUNTING == 0 {
        return Err(Unsupported.into());
    }
    Ok(serial)
}

fn ioctl(
    h: HANDLE,
    code: u32,
    input: *const core::ffi::c_void,
    input_len: usize,
    output: *mut core::ffi::c_void,
    output_len: usize,
) -> io::Result<()> {
    let mut returned = 0_u32;
    // SAFETY: the caller supplies valid buffers of the stated sizes for `code`.
    let ok = unsafe {
        DeviceIoControl(
            h,
            code,
            input,
            u32::try_from(input_len).unwrap_or(0),
            output,
            u32::try_from(output_len).unwrap_or(0),
            &raw mut returned,
            std::ptr::null_mut(),
        )
    };
    if ok == 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}

pub(super) fn clone_file(ctx: &Context, src: &File, dst: &str) -> Result<()> {
    ctx.err()?;
    let source = handle(src);
    let source_volume = volume(source)?;
    let out = OpenOptions::new()
        .read(true)
        .write(true)
        .create_new(true)
        .open(dst)?;
    let cloned = clone_into(ctx, src, source, source_volume, &out, dst);
    // Rust closes handles on drop without reporting errors; CloseHandle
    // failures on a freshly written private file are not recoverable here.
    drop(out);
    cloned
}

fn clone_into(
    ctx: &Context,
    src: &File,
    source: HANDLE,
    source_volume: u32,
    out: &File,
    dst: &str,
) -> Result<()> {
    let target = handle(out);
    if volume(target)? != source_volume {
        return Err(super::unsupported(io::Error::from_raw_os_error(
            ERROR_NOT_SAME_DEVICE as i32,
        )));
    }
    // ReFS requires identical integrity settings and sparse destinations for
    // sparse sources. Apply these before extending the new private file.
    let mut integrity = IntegrityInformation::default();
    ioctl(
        source,
        FSCTL_GET_INTEGRITY_INFORMATION,
        std::ptr::null(),
        0,
        (&raw mut integrity).cast(),
        size_of::<IntegrityInformation>(),
    )
    .map_err(capability_error)?;
    let settings = IntegritySettings {
        algorithm: integrity.algorithm,
        reserved: 0,
        flags: integrity.flags,
    };
    ioctl(
        target,
        FSCTL_SET_INTEGRITY_INFORMATION,
        (&raw const settings).cast(),
        size_of::<IntegritySettings>(),
        std::ptr::null_mut(),
        0,
    )
    .map_err(capability_error)?;
    // SAFETY: plain data filled by GetFileInformationByHandle.
    let mut info: BY_HANDLE_FILE_INFORMATION = unsafe { std::mem::zeroed() };
    // SAFETY: `source` is live and `info` is a valid output buffer.
    if unsafe { GetFileInformationByHandle(source, &raw mut info) } == 0 {
        return Err(io::Error::last_os_error().into());
    }
    if info.dwFileAttributes & FILE_ATTRIBUTE_SPARSE_FILE != 0 {
        ioctl(
            target,
            FSCTL_SET_SPARSE,
            std::ptr::null(),
            0,
            std::ptr::null_mut(),
            0,
        )
        .map_err(capability_error)?;
    }
    let cluster = cluster_size(&paths::dir(dst))?;
    let size = i64::try_from(src.metadata()?.len())?;
    let length = aligned_length(size, cluster)?;
    // Extend only the destination to a cluster boundary. The last source
    // cluster may contain EOF; restore the exact logical length afterwards.
    out.set_len(u64::try_from(length)?)?;
    clone_chunks(ctx, length, cluster, &mut |offset, count| {
        let data = DuplicateExtents {
            file: source,
            source_offset: offset,
            target_offset: offset,
            byte_count: count,
        };
        ioctl(
            target,
            FSCTL_DUPLICATE_EXTENTS_TO_FILE,
            (&raw const data).cast(),
            size_of::<DuplicateExtents>(),
            std::ptr::null_mut(),
            0,
        )
        .map_err(capability_error)
    })?;
    out.set_len(u64::try_from(size)?)?;
    Ok(())
}

fn cluster_size(path: &str) -> Result<i64> {
    let name = wide(path);
    let mut root = [0_u16; 261];
    // SAFETY: `name` is NUL-terminated and `root` has the stated capacity.
    if unsafe {
        GetVolumePathNameW(
            name.as_ptr(),
            root.as_mut_ptr(),
            u32::try_from(root.len()).unwrap_or(0),
        )
    } == 0
    {
        return Err(io::Error::last_os_error().into());
    }
    let (mut sectors, mut bytes, mut free, mut total) = (0_u32, 0_u32, 0_u32, 0_u32);
    // SAFETY: `root` is NUL-terminated and every output pointer is valid.
    let ok = unsafe {
        GetDiskFreeSpaceW(
            root.as_ptr(),
            &raw mut sectors,
            &raw mut bytes,
            &raw mut free,
            &raw mut total,
        )
    };
    if ok == 0 {
        return Err(io::Error::last_os_error().into());
    }
    Ok(i64::from(sectors) * i64::from(bytes))
}

pub(super) fn capability_error(error: io::Error) -> anyhow::Error {
    match error
        .raw_os_error()
        .and_then(|code| u32::try_from(code).ok())
    {
        Some(ERROR_NOT_SAME_DEVICE | ERROR_NOT_SUPPORTED | ERROR_INVALID_FUNCTION) => {
            super::unsupported(error)
        }
        _ => error.into(),
    }
}
