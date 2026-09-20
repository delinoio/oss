//! One-file publication: private staging, permission preservation, then rename.
use std::{
    fs::{self, File, Metadata, OpenOptions},
    path::Path,
};

use crate::runtime::{self, Cancellation, Failure, Result};

pub fn regular_input(path: &Path) -> Result<()> {
    let metadata = fs::symlink_metadata(path).map_err(|_| Failure::Read)?;
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return Err(Failure::UnsafeDestination.into());
    }
    Ok(())
}

fn destination(path: &Path) -> Result<Option<(File, Metadata)>> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(Failure::Publish.into()),
    };
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return Err(Failure::UnsafeDestination.into());
    }
    let mut options = OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        options.custom_flags(windows_sys::Win32::Storage::FileSystem::FILE_FLAG_OPEN_REPARSE_POINT);
    }
    let file = options.open(path).map_err(|_| Failure::Permissions)?;
    let metadata = file.metadata().map_err(|_| Failure::Permissions)?;
    if !metadata.is_file() {
        return Err(Failure::UnsafeDestination.into());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.nlink() != 1 {
            return Err(Failure::UnsafeDestination.into());
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::io::AsRawHandle;

        use windows_sys::Win32::Storage::FileSystem::*;
        let mut info = std::mem::MaybeUninit::<BY_HANDLE_FILE_INFORMATION>::uninit();
        // SAFETY: the file handle is live and info points to writable storage.
        if unsafe { GetFileInformationByHandle(file.as_raw_handle(), info.as_mut_ptr()) } == 0 {
            return Err(Failure::Permissions.into());
        }
        let info = unsafe { info.assume_init() };
        if info.nNumberOfLinks != 1 || info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
            return Err(Failure::UnsafeDestination.into());
        }
    }
    Ok(Some((file, metadata)))
}

#[cfg(unix)]
fn permissions(source: &File, metadata: &Metadata, target: &File) -> Result<()> {
    use std::os::unix::{fs::MetadataExt, io::AsRawFd};
    let current = target.metadata().map_err(|_| Failure::Permissions)?;
    if (current.uid(), current.gid()) != (metadata.uid(), metadata.gid()) {
        // Preserve the identities to which owner/group permission bits apply.
        if unsafe { libc::fchown(target.as_raw_fd(), metadata.uid(), metadata.gid()) } != 0 {
            return Err(Failure::Permissions.into());
        }
    }
    target
        .set_permissions(metadata.permissions())
        .map_err(|_| Failure::Permissions)?;
    #[cfg(target_os = "macos")]
    {
        // Copy only the ACL. Never copy content, resource forks, or other state.
        if unsafe {
            libc::fcopyfile(
                source.as_raw_fd(),
                target.as_raw_fd(),
                std::ptr::null_mut(),
                libc::COPYFILE_ACL,
            )
        } != 0
        {
            return Err(Failure::Permissions.into());
        }
    }
    #[cfg(target_os = "linux")]
    {
        let name = c"system.posix_acl_access";
        // Linux POSIX ACLs are kernel-owned xattrs; this avoids a libacl runtime
        // dependency in standalone/musl packages. ENODATA means mode bits only.
        let size =
            unsafe { libc::fgetxattr(source.as_raw_fd(), name.as_ptr(), std::ptr::null_mut(), 0) };
        if size < 0 {
            let code = std::io::Error::last_os_error().raw_os_error();
            if !matches!(code, Some(libc::ENODATA | libc::ENOTSUP)) {
                return Err(Failure::Permissions.into());
            }
            let removed = unsafe { libc::fremovexattr(target.as_raw_fd(), name.as_ptr()) };
            if removed != 0
                && !matches!(
                    std::io::Error::last_os_error().raw_os_error(),
                    Some(libc::ENODATA | libc::ENOTSUP)
                )
            {
                return Err(Failure::Permissions.into());
            }
        } else {
            let mut acl = vec![0u8; size as usize];
            let actual = unsafe {
                libc::fgetxattr(
                    source.as_raw_fd(),
                    name.as_ptr(),
                    acl.as_mut_ptr().cast(),
                    acl.len(),
                )
            };
            if actual != size
                || unsafe {
                    libc::fsetxattr(
                        target.as_raw_fd(),
                        name.as_ptr(),
                        acl.as_ptr().cast(),
                        acl.len(),
                        0,
                    )
                } != 0
            {
                return Err(Failure::Permissions.into());
            }
        }
    }
    Ok(())
}

pub fn publish(path: &Path, bytes: &[u8], replace: bool, cancel: &Cancellation) -> Result<()> {
    cancel.check()?;
    if fs::symlink_metadata(path).is_ok() && !replace {
        return Err(Failure::DestinationExists.into());
    }
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let mut temporary = tempfile::Builder::new()
        .prefix(".clibox-")
        .tempfile_in(parent)
        .map_err(|_| Failure::Publish)?;
    // NamedTempFile creates mode 0600 on Unix, and inherits the parent's ACL on
    // Windows. It removes unpublished files on every handled return path.
    runtime::write(temporary.as_file_mut(), bytes, cancel)?;
    publish_prepared(temporary, path, replace, cancel)
}

fn publish_prepared(
    temporary: tempfile::NamedTempFile,
    path: &Path,
    replace: bool,
    cancel: &Cancellation,
) -> Result<()> {
    cancel.check()?;
    let existing = destination(path)?;
    if existing.is_some() && !replace {
        return Err(Failure::DestinationExists.into());
    }
    #[cfg(unix)]
    if let Some((source, metadata)) = &existing {
        permissions(source, metadata, temporary.as_file())?;
    }
    temporary.as_file().sync_all().map_err(|_| Failure::Write)?;
    cancel.check()?;
    #[cfg(windows)]
    if existing.is_some() {
        use std::os::windows::ffi::OsStrExt;
        let old: Vec<u16> = path.as_os_str().encode_wide().chain([0]).collect();
        let new: Vec<u16> = temporary
            .path()
            .as_os_str()
            .encode_wide()
            .chain([0])
            .collect();
        // ReplaceFile preserves the destination DACL. Do not set IGNORE_ACL_ERRORS
        // or IGNORE_MERGE_ERRORS: inability to preserve access must fail closed.
        let success = unsafe {
            windows_sys::Win32::Storage::FileSystem::ReplaceFileW(
                old.as_ptr(),
                new.as_ptr(),
                std::ptr::null(),
                0,
                std::ptr::null(),
                std::ptr::null(),
            )
        };
        if success == 0 {
            return Err(Failure::Publish.into());
        }
        return Ok(());
    }
    if replace {
        temporary.persist(path).map_err(|_| Failure::Publish)?;
    } else {
        temporary.persist_noclobber(path).map_err(|error| {
            if error.error.kind() == std::io::ErrorKind::AlreadyExists {
                Failure::DestinationExists
            } else {
                Failure::Publish
            }
        })?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn cancellation_after_staging_cleans_temporary_and_preserves_destination() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("output");
        fs::write(&path, "original").unwrap();
        let staged = tempfile::Builder::new()
            .prefix(".clibox-")
            .tempfile_in(dir.path())
            .unwrap();
        let staged_path = staged.path().to_path_buf();
        let cancel = Cancellation::default();
        cancel.cancel();
        assert_eq!(
            publish_prepared(staged, &path, true, &cancel)
                .unwrap_err()
                .kind,
            Failure::Cancelled
        );
        assert_eq!(fs::read(&path).unwrap(), b"original");
        assert!(!staged_path.exists());
    }
}
