use std::{
    ffi::OsString,
    fs::File,
    io,
    os::windows::{
        ffi::{OsStrExt, OsStringExt},
        io::{AsRawHandle, FromRawHandle},
    },
    path::{Path, PathBuf},
};

use windows_sys::{
    Wdk::{
        Foundation::OBJECT_ATTRIBUTES,
        Storage::FileSystem::{
            NtOpenFile, RtlDosPathNameToNtPathName_U_WithStatus, FILE_OPEN_FOR_BACKUP_INTENT,
        },
    },
    Win32::{
        Foundation::{
            RtlNtStatusToDosError, NTSTATUS, OBJ_CASE_INSENSITIVE, STATUS_DELETE_PENDING,
            UNICODE_STRING,
        },
        Storage::FileSystem::{
            FileStandardInfo, GetFileInformationByHandleEx, GetFinalPathNameByHandleW,
            FILE_NAME_NORMALIZED, FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE,
            FILE_STANDARD_INFO, VOLUME_NAME_DOS,
        },
        System::{WindowsProgramming::RtlFreeUnicodeString, IO::IO_STATUS_BLOCK},
    },
};

pub(super) fn canonicalize(path: &Path) -> io::Result<PathBuf> {
    let file = open_path(path)?;
    canonicalize_handle(&file)
}

fn nt_error(status: NTSTATUS) -> io::Error {
    if status == STATUS_DELETE_PENDING {
        io::Error::from(io::ErrorKind::NotFound)
    } else {
        io::Error::from_raw_os_error(unsafe { RtlNtStatusToDosError(status) } as i32)
    }
}

fn open_path(path: &Path) -> io::Result<File> {
    // CreateFile converts STATUS_DELETE_PENDING into ERROR_ACCESS_DENIED,
    // losing the distinction from a live ACL failure. Use the native open
    // status so removal races are recoverable without hiding permission errors.
    // Keep the successful handle for both final-path and deletion-state queries.
    let mut dos: Vec<u16> = path.as_os_str().encode_wide().collect();
    if dos.contains(&0) {
        return Err(io::Error::from(io::ErrorKind::InvalidInput));
    }
    dos.push(0);
    let mut name = UNICODE_STRING::default();
    let converted = unsafe {
        RtlDosPathNameToNtPathName_U_WithStatus(
            dos.as_ptr(),
            &mut name,
            std::ptr::null_mut(),
            std::ptr::null(),
        )
    };
    if converted < 0 {
        return Err(nt_error(converted));
    }
    let attributes = OBJECT_ATTRIBUTES {
        Length: std::mem::size_of::<OBJECT_ATTRIBUTES>() as u32,
        ObjectName: &name,
        Attributes: OBJ_CASE_INSENSITIVE,
        ..Default::default()
    };
    let mut handle = std::ptr::null_mut();
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtOpenFile(
            &mut handle,
            0,
            &attributes,
            &mut status_block,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            FILE_OPEN_FOR_BACKUP_INTENT,
        )
    };
    unsafe {
        RtlFreeUnicodeString(&mut name);
    }
    if status < 0 {
        return Err(nt_error(status));
    }
    // NtOpenFile transferred ownership of a successful, non-inheritable handle.
    Ok(unsafe { File::from_raw_handle(handle) })
}

fn canonicalize_handle(file: &File) -> io::Result<PathBuf> {
    let resolved = resolve_handle_path(file);
    validate_handle_path(file, resolved)
}

fn resolve_handle_path(file: &File) -> io::Result<PathBuf> {
    let handle = file.as_raw_handle();
    let mut buffer = vec![0; 512];
    let length = loop {
        let length = unsafe {
            GetFinalPathNameByHandleW(
                handle,
                buffer.as_mut_ptr(),
                buffer.len() as u32,
                FILE_NAME_NORMALIZED | VOLUME_NAME_DOS,
            )
        } as usize;
        if length == 0 {
            return Err(io::Error::last_os_error());
        }
        if length < buffer.len() {
            break length;
        }
        buffer.resize(length, 0);
    };
    Ok(PathBuf::from(OsString::from_wide(&buffer[..length])))
}

fn validate_handle_path(file: &File, resolved: io::Result<PathBuf>) -> io::Result<PathBuf> {
    let handle = file.as_raw_handle();
    // NTFS can move a concurrently unlinked file into $Extend/$Deleted while
    // its handle remains open. Check deletion on this same handle after path
    // resolution, including a failed lookup (the deletion path can deny access).
    // A second path open could observe a replacement file instead. Return
    // NotFound for this deleted handle before propagating a lookup error, but
    // preserve permission errors for live files and metadata-query failures.
    let mut info = FILE_STANDARD_INFO::default();
    if unsafe {
        GetFileInformationByHandleEx(
            handle,
            FileStandardInfo,
            &mut info as *mut _ as _,
            std::mem::size_of_val(&info) as u32,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    if info.DeletePending || info.NumberOfLinks == 0 {
        return Err(io::Error::from(io::ErrorKind::NotFound));
    }
    resolved
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn delete_pending_open_is_not_a_permission_failure() {
        use std::os::windows::fs::OpenOptionsExt;

        use windows_sys::Win32::{
            Foundation::STATUS_ACCESS_DENIED,
            Storage::FileSystem::{
                FileDispositionInfo, SetFileInformationByHandle, DELETE, FILE_DISPOSITION_INFO,
            },
        };
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("pending");
        std::fs::write(&path, "pending deletion").unwrap();
        let owner = std::fs::OpenOptions::new()
            .access_mode(DELETE)
            .open(&path)
            .unwrap();
        let disposition = FILE_DISPOSITION_INFO { DeleteFile: true };
        assert_ne!(
            unsafe {
                SetFileInformationByHandle(
                    owner.as_raw_handle(),
                    FileDispositionInfo,
                    &disposition as *const _ as _,
                    std::mem::size_of_val(&disposition) as u32,
                )
            },
            0
        );
        // Legacy deletion keeps the name until this owner closes; opening that
        // name through CreateFile would report access denied instead of missing.
        assert_eq!(
            canonicalize(&path).unwrap_err().kind(),
            io::ErrorKind::NotFound
        );
        assert_eq!(
            nt_error(STATUS_ACCESS_DENIED).kind(),
            io::ErrorKind::PermissionDenied
        );
        drop(owner);
        std::fs::write(&path, "replacement").unwrap();
        assert_eq!(canonicalize(&path).unwrap(), path.canonicalize().unwrap());
    }

    #[test]
    fn deleted_handle_does_not_resolve_to_ntfs_storage() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("transient");
        std::fs::write(&path, "old").unwrap();
        let handle = File::open(&path).unwrap();
        let denied = || {
            Err(io::Error::from_raw_os_error(
                windows_sys::Win32::Foundation::ERROR_ACCESS_DENIED as i32,
            ))
        };
        assert_eq!(
            validate_handle_path(&handle, denied()).unwrap_err().kind(),
            io::ErrorKind::PermissionDenied
        );
        std::fs::remove_file(&path).unwrap();
        // The original name can already refer to a new file while the old
        // object remains accessible through a handle in the deletion queue.
        std::fs::write(&path, "new").unwrap();
        assert_eq!(
            validate_handle_path(&handle, denied()).unwrap_err().kind(),
            io::ErrorKind::NotFound
        );
        assert_eq!(
            canonicalize_handle(&handle).unwrap_err().kind(),
            io::ErrorKind::NotFound
        );
        assert_eq!(canonicalize(&path).unwrap(), path.canonicalize().unwrap());
    }
}
