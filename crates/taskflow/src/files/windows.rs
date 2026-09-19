use std::{
    ffi::OsString,
    fs::{File, OpenOptions},
    io,
    os::windows::{ffi::OsStringExt, fs::OpenOptionsExt, io::AsRawHandle},
    path::{Path, PathBuf},
};

use windows_sys::Win32::{
    Foundation::ERROR_DELETE_PENDING,
    Storage::FileSystem::{
        FileStandardInfo, GetFileInformationByHandleEx, GetFinalPathNameByHandleW,
        FILE_FLAG_BACKUP_SEMANTICS, FILE_NAME_NORMALIZED, FILE_STANDARD_INFO, VOLUME_NAME_DOS,
    },
};

pub(super) fn canonicalize(path: &Path) -> io::Result<PathBuf> {
    let file = OpenOptions::new()
        .access_mode(0)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS)
        .open(path)
        .map_err(|error| {
            if error.raw_os_error() == Some(ERROR_DELETE_PENDING as i32) {
                io::Error::from(io::ErrorKind::NotFound)
            } else {
                error
            }
        })?;
    canonicalize_handle(&file)
}

fn canonicalize_handle(file: &File) -> io::Result<PathBuf> {
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
    // NTFS can move a concurrently unlinked file into $Extend/$Deleted while
    // its handle remains open. Check deletion on this same handle after path
    // resolution; a second path open can observe a replacement file instead.
    // Return NotFound so the notification resolver uses the original parent.
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
    Ok(PathBuf::from(OsString::from_wide(&buffer[..length])))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn deleted_handle_does_not_resolve_to_ntfs_storage() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("transient");
        std::fs::write(&path, "old").unwrap();
        let handle = File::open(&path).unwrap();
        std::fs::remove_file(&path).unwrap();
        // The original name can already refer to a new file while the old
        // object remains accessible through a handle in the deletion queue.
        std::fs::write(&path, "new").unwrap();
        assert_eq!(
            canonicalize_handle(&handle).unwrap_err().kind(),
            io::ErrorKind::NotFound
        );
        assert_eq!(canonicalize(&path).unwrap(), path.canonicalize().unwrap());
    }
}
