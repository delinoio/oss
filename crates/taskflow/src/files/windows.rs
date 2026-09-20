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
            FileStandardInformation, NtOpenFile, NtQueryInformationFile,
            RtlDosPathNameToNtPathName_U_WithStatus, FILE_OPEN_FOR_BACKUP_INTENT,
            FILE_STANDARD_INFORMATION, FILE_SYNCHRONOUS_IO_NONALERT,
        },
    },
    Win32::{
        Foundation::{
            RtlNtStatusToDosError, NTSTATUS, OBJ_CASE_INSENSITIVE, STATUS_DELETE_PENDING,
            STATUS_FILE_DELETED, UNICODE_STRING,
        },
        Storage::FileSystem::{
            GetFinalPathNameByHandleW, FILE_NAME_NORMALIZED, FILE_READ_ATTRIBUTES,
            FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE, SYNCHRONIZE, VOLUME_NAME_DOS,
        },
        System::{WindowsProgramming::RtlFreeUnicodeString, IO::IO_STATUS_BLOCK},
    },
};

pub(super) fn canonicalize(path: &Path) -> io::Result<PathBuf> {
    let file = open_path(path)?;
    canonicalize_handle(&file)
}

fn nt_error(status: NTSTATUS) -> io::Error {
    if matches!(status, STATUS_DELETE_PENDING | STATUS_FILE_DELETED) {
        io::Error::from(io::ErrorKind::NotFound)
    } else {
        io::Error::from_raw_os_error(unsafe { RtlNtStatusToDosError(status) } as i32)
    }
}

fn native_error(operation: &str, status: NTSTATUS) -> io::Error {
    let error = nt_error(status);
    tracing::debug!(operation, status, "Windows native path operation failed");
    // Keep the stage and native status in returned errors as well: callers and
    // test harnesses may not have a tracing subscriber installed.
    io::Error::new(
        error.kind(),
        format!("Windows {operation} failed (NTSTATUS {status:#010x}): {error}"),
    )
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
        return Err(native_error("convert-path", converted));
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
            // Native opens do not supply CreateFile's metadata access defaults.
            // Both final-path resolution and deletion inspection need an
            // attribute-readable handle. Keep synchronous query completion,
            // with its required SYNCHRONIZE right; no file-data access is requested.
            FILE_READ_ATTRIBUTES | SYNCHRONIZE,
            &attributes,
            &mut status_block,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            FILE_OPEN_FOR_BACKUP_INTENT | FILE_SYNCHRONOUS_IO_NONALERT,
        )
    };
    unsafe {
        RtlFreeUnicodeString(&mut name);
    }
    if status < 0 {
        return Err(native_error("open-path", status));
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
            let error = io::Error::last_os_error();
            tracing::debug!(
                operation = "final-path",
                code = error.raw_os_error(),
                "Windows handle path lookup failed"
            );
            return Err(io::Error::new(
                error.kind(),
                format!("Windows final-path lookup failed: {error}"),
            ));
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
    // The query can itself race deletion after a successful open. The Win32
    // wrapper also maps native deletion errors to ERROR_ACCESS_DENIED, so retain
    // NTSTATUS here just as we do for NtOpenFile. Never infer deletion from an
    // undifferentiated access-denied error or from a second path open.
    let mut info = FILE_STANDARD_INFORMATION::default();
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtQueryInformationFile(
            handle,
            &mut status_block,
            &mut info as *mut _ as _,
            std::mem::size_of_val(&info) as u32,
            FileStandardInformation,
        )
    };
    if status < 0 {
        return Err(native_error("deletion-state", status));
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
    fn native_deletion_errors_remain_distinct_from_access_denial() {
        use windows_sys::Win32::Foundation::{ERROR_ACCESS_DENIED, STATUS_ACCESS_DENIED};

        // All three statuses lose their distinction at the Win32 boundary.
        // Both native operations must preserve missing-file recovery without
        // allowing live permission failures to fall back to the parent path.
        for operation in ["open-path", "deletion-state"] {
            for status in [
                STATUS_DELETE_PENDING,
                STATUS_FILE_DELETED,
                STATUS_ACCESS_DENIED,
            ] {
                assert_eq!(
                    unsafe { RtlNtStatusToDosError(status) },
                    ERROR_ACCESS_DENIED
                );
                let error = native_error(operation, status);
                assert_eq!(
                    error.kind(),
                    if status == STATUS_ACCESS_DENIED {
                        io::ErrorKind::PermissionDenied
                    } else {
                        io::ErrorKind::NotFound
                    }
                );
                assert!(error.to_string().contains(operation));
                assert!(error.to_string().contains(&format!("{status:#010x}")));
            }
        }
    }

    #[test]
    fn native_metadata_open_resolves_live_files_and_directories() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("live");
        std::fs::write(&path, "metadata only").unwrap();
        for path in [directory.path(), path.as_path()] {
            let handle = open_path(path).expect("native metadata open must succeed");
            let resolved =
                resolve_handle_path(&handle).expect("metadata handle must resolve its name");
            let validated = validate_handle_path(&handle, Ok(resolved))
                .expect("metadata handle must allow deletion-state inspection");
            assert_eq!(validated, path.canonicalize().unwrap());
        }
    }

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
        let metadata = open_path(&path).unwrap();
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
            canonicalize_handle(&metadata).unwrap_err().kind(),
            io::ErrorKind::NotFound
        );
        assert_eq!(
            nt_error(STATUS_ACCESS_DENIED).kind(),
            io::ErrorKind::PermissionDenied
        );
        drop(owner);
        drop(metadata);
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
