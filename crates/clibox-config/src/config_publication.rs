//! One-file publication: private staging, permission preservation, then rename.
use std::{
    fs::{self, File, Metadata, OpenOptions},
    path::Path,
};

use crate::config_runtime::{self, Cancellation, Failure, Result};

fn read_options() -> OpenOptions {
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
    options
}

pub fn regular_input(path: &Path) -> Result<File> {
    // Validate the opened object and return that same handle to the reader.
    // Checking metadata and then reopening the path would let a replacement
    // symlink redirect in-place reads to a different file before publication.
    let file = read_options().open(path).map_err(|_| Failure::Read)?;
    regular_metadata(&file, Failure::Read)?;
    Ok(file)
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
    let options = read_options();
    let file = match options.open(path) {
        Ok(file) => file,
        #[cfg(unix)]
        Err(error) if error.kind() == std::io::ErrorKind::PermissionDenied => {
            // Replacement reads metadata/ACLs, never destination content. A
            // write-only descriptor supports those operations too, so retain
            // write-only outputs without requiring read access. Keep NOFOLLOW
            // and NONBLOCK, and never truncate or write through this handle.
            let mut options = options;
            options
                .read(false)
                .write(true)
                .open(path)
                .map_err(|_| Failure::Permissions)?
        }
        Err(_) => return Err(Failure::Permissions.into()),
    };
    let metadata = regular_metadata(&file, Failure::Permissions)?;
    Ok(Some((file, metadata)))
}

fn regular_metadata(file: &File, failure: Failure) -> Result<Metadata> {
    let metadata = file.metadata().map_err(|_| failure)?;
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
            return Err(failure.into());
        }
        let info = unsafe { info.assume_init() };
        if info.nNumberOfLinks != 1 || info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
            return Err(Failure::UnsafeDestination.into());
        }
    }
    Ok(metadata)
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

// The directory guard outlives the file even after its permissions are copied.
// Renaming from this private child directory stays on the destination
// filesystem.
struct Staging {
    file: tempfile::NamedTempFile,
    #[cfg(unix)]
    _directory: tempfile::TempDir,
}
impl Staging {
    fn new(parent: &Path) -> Result<Self> {
        #[cfg(unix)]
        let directory = {
            use std::os::unix::fs::PermissionsExt;
            let directory = tempfile::Builder::new()
                .prefix(".clibox-")
                .tempdir_in(parent)
                .map_err(|_| Failure::Publish)?;
            fs::set_permissions(directory.path(), fs::Permissions::from_mode(0o700))
                .map_err(|_| Failure::Permissions)?;
            #[cfg(target_os = "macos")]
            clear_directory_acl(directory.path())?;
            if fs::metadata(directory.path())
                .map_err(|_| Failure::Permissions)?
                .permissions()
                .mode()
                & 0o777
                != 0o700
            {
                return Err(Failure::Permissions.into());
            }
            directory
        };
        #[cfg(unix)]
        let parent = directory.path();
        let file = tempfile::Builder::new()
            .prefix(".clibox-")
            .tempfile_in(parent)
            .map_err(|_| Failure::Publish)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            // Creation's 0600 is filtered by umask. Restore owner access through
            // the open handle before writing, even with caller umask 0777.
            file.as_file()
                .set_permissions(fs::Permissions::from_mode(0o600))
                .map_err(|_| Failure::Permissions)?;
        }
        Ok(Self {
            file,
            #[cfg(unix)]
            _directory: directory,
        })
    }
}

#[cfg(target_os = "macos")]
fn clear_directory_acl(path: &Path) -> Result<()> {
    use std::os::fd::AsRawFd;
    // Darwin ACL grants can bypass POSIX mode bits. Remove inherited grants
    // before staging any bytes; Linux's 0700 mode also masks POSIX ACL grants.
    // libc does not expose these macOS SDK <sys/acl.h> declarations.
    unsafe extern "C" {
        fn acl_init(count: libc::c_int) -> *mut libc::c_void;
        fn acl_set_fd(fd: libc::c_int, acl: *mut libc::c_void) -> libc::c_int;
        fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
    }
    let directory = File::open(path).map_err(|_| Failure::Permissions)?;
    // SAFETY: the initialized empty ACL and live directory descriptor remain
    // valid through acl_set_fd, and the allocation is freed exactly once.
    unsafe {
        let acl = acl_init(0);
        if acl.is_null() {
            return Err(Failure::Permissions.into());
        }
        let result = acl_set_fd(directory.as_raw_fd(), acl);
        acl_free(acl);
        if result != 0 {
            return Err(Failure::Permissions.into());
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
    let mut temporary = Staging::new(parent)?;
    // Windows inherits the parent's ACL. Both guards clean up on handled errors.
    config_runtime::write(temporary.file.as_file_mut(), bytes, cancel)?;
    publish_prepared(temporary, path, replace, cancel)
}

fn publish_prepared(
    temporary: Staging,
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
        permissions(source, metadata, temporary.file.as_file())?;
    }
    temporary
        .file
        .as_file()
        .sync_all()
        .map_err(|_| Failure::Write)?;
    cancel.check()?;
    #[cfg(windows)]
    {
        let mut publication = crate::config_windows_publication::Publication::prepare(
            temporary.file.path(),
            existing.is_some(),
        )?;
        if let Some((source, _)) = &existing {
            publication.preserve_dacl(source)?;
        }
        // FileRenameInfo cannot replace a destination with an open data handle,
        // including our own inspection handle even though it shares deletion.
        // Keep that handle only until its DACL has been copied to staging.
        // https://learn.microsoft.com/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information
        drop(existing);
        cancel.check()?;
        publication.commit(path, replace)?;
        // The held handle now owns the destination. Never clean up by its old name.
        temporary.file.into_temp_path().disable_cleanup(true);
        Ok(())
    }
    #[cfg(not(windows))]
    {
        if replace {
            temporary.file.persist(path).map_err(|_| Failure::Publish)?;
        } else {
            temporary.file.persist_noclobber(path).map_err(|error| {
                if error.error.kind() == std::io::ErrorKind::AlreadyExists {
                    Failure::DestinationExists
                } else {
                    Failure::Publish
                }
            })?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(unix)]
    #[test]
    fn destination_replaced_after_open_rejects_the_unlinked_handle() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("out");
        fs::write(&path, "original").unwrap();
        let opened = File::open(&path).unwrap();
        let replacement = dir.path().join("replacement");
        fs::write(&replacement, "complete winner").unwrap();
        fs::rename(&replacement, &path).unwrap();

        assert_eq!(
            regular_metadata(&opened, Failure::Permissions)
                .unwrap_err()
                .kind,
            Failure::UnsafeDestination
        );
        assert_eq!(fs::read(&path).unwrap(), b"complete winner");
    }

    #[test]
    fn in_place_reader_retains_the_validated_file_after_path_replacement() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("input");
        fs::write(&path, "authorized: original\n").unwrap();
        let file = regular_input(&path).unwrap();
        fs::rename(&path, dir.path().join("moved")).unwrap();
        fs::write(&path, "different: replacement\n").unwrap();

        let bytes = config_runtime::read(file, 1024, &Cancellation::default()).unwrap();
        assert_eq!(bytes, b"authorized: original\n");
        assert_eq!(fs::read(&path).unwrap(), b"different: replacement\n");
    }

    #[cfg(unix)]
    #[test]
    fn in_place_open_never_follows_a_replacement_symlink() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("input");
        fs::write(&path, "authorized: original\n").unwrap();
        assert!(fs::symlink_metadata(&path).unwrap().is_file());

        // Replace the entry after a pathname-based check, reproducing the
        // former validation/open gap without relying on thread scheduling.
        fs::rename(&path, dir.path().join("moved")).unwrap();
        let target = dir.path().join("unrelated");
        fs::write(&target, "unrelated: private\n").unwrap();
        std::os::unix::fs::symlink(&target, &path).unwrap();
        assert_eq!(regular_input(&path).unwrap_err().kind, Failure::Read);
        assert_eq!(fs::read(&target).unwrap(), b"unrelated: private\n");
    }

    #[cfg(unix)]
    #[test]
    fn replacement_permissions_stay_private_until_publish_or_cancel() {
        use std::os::unix::fs::PermissionsExt;
        for cancelled in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            let path = dir.path().join("output");
            fs::write(&path, "original").unwrap();
            fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
            let mut staged = Staging::new(dir.path()).unwrap();
            let private = staged.file.path().parent().unwrap().to_path_buf();
            let cancel = Cancellation::default();
            config_runtime::write(staged.file.as_file_mut(), b"replacement", &cancel).unwrap();
            let (source, metadata) = destination(&path).unwrap().unwrap();
            permissions(&source, &metadata, staged.file.as_file()).unwrap();
            assert_eq!(
                staged
                    .file
                    .as_file()
                    .metadata()
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o644
            );
            assert_eq!(
                fs::metadata(&private).unwrap().permissions().mode() & 0o777,
                0o700
            );
            assert_eq!(private.parent().unwrap(), dir.path());
            if cancelled {
                cancel.cancel();
                assert_eq!(
                    publish_prepared(staged, &path, true, &cancel)
                        .unwrap_err()
                        .kind,
                    Failure::Cancelled
                );
                assert_eq!(fs::read(&path).unwrap(), b"original");
            } else {
                publish_prepared(staged, &path, true, &cancel).unwrap();
                assert_eq!(fs::read(&path).unwrap(), b"replacement");
            }
            assert!(!private.exists());
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o644
            );
        }
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn private_directory_removes_inherited_macos_acl_grants() {
        use std::os::fd::AsRawFd;
        unsafe extern "C" {
            fn acl_get_fd(fd: libc::c_int) -> *mut libc::c_void;
            fn acl_get_entry(
                acl: *mut libc::c_void,
                id: libc::c_int,
                entry: *mut *mut libc::c_void,
            ) -> libc::c_int;
            fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
        }
        let has_entries = |path: &Path| {
            let file = File::open(path).unwrap();
            // SAFETY: both pointers belong to this call, and the live ACL is
            // released after examining its first entry (ACL_FIRST_ENTRY = 0).
            unsafe {
                let acl = acl_get_fd(file.as_raw_fd());
                if acl.is_null() {
                    // Darwin reports a missing ACL as ENOENT on a live file.
                    assert_eq!(
                        std::io::Error::last_os_error().raw_os_error(),
                        Some(libc::ENOENT)
                    );
                    return false;
                }
                let mut entry = std::ptr::null_mut();
                let result = acl_get_entry(acl, 0, &mut entry);
                acl_free(acl);
                result == 0
            }
        };
        let dir = tempfile::tempdir().unwrap();
        // Configure only a disposable fixture with the platform's ACL utility.
        assert!(std::process::Command::new("/bin/chmod")
            .args([
                "+a",
                "everyone allow list,search,file_inherit,directory_inherit"
            ])
            .arg(dir.path())
            .status()
            .unwrap()
            .success());
        assert!(has_entries(dir.path()));
        let staged = Staging::new(dir.path()).unwrap();
        assert!(!has_entries(staged.file.path().parent().unwrap()));
        assert!(!has_entries(staged.file.path()));
    }

    #[test]
    fn cancellation_after_staging_cleans_temporary_and_preserves_destination() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("output");
        fs::write(&path, "original").unwrap();
        let staged = Staging::new(dir.path()).unwrap();
        let staged_path = staged.file.path().to_path_buf();
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
