use std::{
    fs::{self, File},
    io,
    path::{Path, PathBuf},
};

use tempfile::{Builder, TempPath};

use crate::transform_error::{Code, Error, Result};

pub struct Publication {
    temporary: Option<TempPath>,
    destination: Option<PathBuf>,
    replace: bool,
}

impl Publication {
    pub fn prepare(destination: Option<PathBuf>, replace: bool) -> Result<(Self, Option<File>)> {
        let mut publication = Self {
            temporary: None,
            destination,
            replace,
        };
        let Some(path) = &publication.destination else {
            return Ok((publication, None));
        };
        inspect(path, replace)?;
        let parent = path
            .parent()
            .filter(|p| !p.as_os_str().is_empty())
            .unwrap_or(Path::new("."));
        let file = Builder::new()
            .prefix(".clibox-")
            .tempfile_in(parent)
            .map_err(|_| Error::runtime(Code::WriteFailed))?;
        let (file, temporary) = file.into_parts();
        publication.temporary = Some(temporary);
        Ok((publication, Some(file)))
    }

    pub fn publish(mut self, before_commit: impl FnOnce() -> Result<()>) -> Result<()> {
        let Some(path) = &self.destination else {
            return Ok(());
        };
        let temporary = self.temporary.as_ref().unwrap();
        // Flush through a writable handle before restoring a read-only mode/ACL.
        // FlushFileBuffers on Windows does not accept a read-only handle.
        fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(temporary)
            .and_then(|file| file.sync_all())
            .map_err(|_| Error::runtime(Code::WriteFailed))?;
        // Recheck link/type policy and copy current permissions at publication,
        // without comparing file identities or providing lost-update protection.
        if let Some(original) = inspect(path, self.replace)? {
            preserve_permissions(&original, temporary)?;
        }
        // Cancellation during flushing/permission work must still prevent publication.
        before_commit()?;
        let temporary = self.temporary.take().unwrap();
        if self.replace {
            temporary
                .persist(path)
                .map_err(|_| Error::runtime(Code::PublishFailed))?;
        } else {
            temporary.persist_noclobber(path).map_err(|error| {
                Error::runtime(if error.error.kind() == io::ErrorKind::AlreadyExists {
                    Code::OutputExists
                } else {
                    Code::PublishFailed
                })
            })?;
        }
        Ok(())
    }
}

struct Original {
    metadata: fs::Metadata,
    #[cfg(not(target_os = "macos"))]
    file: File,
    #[cfg(target_os = "macos")]
    path: std::ffi::CString,
}

fn inspect(path: &Path, replace: bool) -> Result<Option<Original>> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(Error::runtime(Code::ReadFailed)),
    };
    if !replace {
        return Err(Error::runtime(Code::OutputExists));
    }
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return Err(Error::runtime(Code::UnsafeDestination));
    }
    #[cfg(not(target_os = "macos"))]
    let original = {
        let file = open_original(path).map_err(|error| {
            tracing::debug!(
                action = "inspect-output",
                os_code = error.raw_os_error(),
                "permission inspection failed"
            );
            Error::runtime(Code::Permissions)
        })?;
        let metadata = file
            .metadata()
            .map_err(|_| Error::runtime(Code::Permissions))?;
        Original { metadata, file }
    };
    #[cfg(target_os = "macos")]
    let original = {
        use std::os::unix::ffi::OsStrExt;
        // Darwin has no O_PATH equivalent: even O_EVTONLY requires data access.
        // lstat and acl_get_link_np inspect metadata/security without opening
        // content or following the final symlink. As with replacement itself,
        // these observations do not lock against concurrent changes.
        let path = std::ffi::CString::new(path.as_os_str().as_bytes())
            .map_err(|_| Error::runtime(Code::Permissions))?;
        Original { metadata, path }
    };
    if !original.metadata.is_file() || multiple_links(&original)? {
        return Err(Error::runtime(Code::UnsafeDestination));
    }
    Ok(Some(original))
}

#[cfg(target_os = "linux")]
fn open_original(path: &Path) -> io::Result<File> {
    use std::os::{fd::FromRawFd, unix::ffi::OsStrExt};
    let path = std::ffi::CString::new(path.as_os_str().as_bytes())
        .map_err(|_| io::Error::from(io::ErrorKind::InvalidInput))?;
    // OpenOptions masks custom flags with !O_ACCMODE. musl includes O_PATH in
    // O_ACCMODE, so that route silently requests content access instead. Use
    // libc directly until Rust preserves metadata-only opens on musl too.
    let fd = unsafe {
        libc::open(
            path.as_ptr(),
            libc::O_PATH | libc::O_NOFOLLOW | libc::O_CLOEXEC,
        )
    };
    if fd < 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(unsafe { File::from_raw_fd(fd) })
    }
}

#[cfg(all(unix, not(any(target_os = "linux", target_os = "macos"))))]
fn open_original(path: &Path) -> io::Result<File> {
    use std::os::unix::fs::OpenOptionsExt;
    fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
        .open(path)
}

#[cfg(windows)]
fn open_original(path: &Path) -> io::Result<File> {
    use std::os::windows::fs::OpenOptionsExt;

    use windows_sys::Win32::Storage::FileSystem::{
        FILE_FLAG_OPEN_REPARSE_POINT, FILE_READ_ATTRIBUTES, READ_CONTROL,
    };
    fs::OpenOptions::new()
        .access_mode(FILE_READ_ATTRIBUTES | READ_CONTROL)
        .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)
}

#[cfg(unix)]
fn multiple_links(original: &Original) -> Result<bool> {
    use std::os::unix::fs::MetadataExt;
    Ok(original.metadata.nlink() != 1)
}

#[cfg(windows)]
fn multiple_links(original: &Original) -> Result<bool> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::Storage::FileSystem::{
        GetFileInformationByHandle, BY_HANDLE_FILE_INFORMATION, FILE_ATTRIBUTE_REPARSE_POINT,
    };
    let mut info: BY_HANDLE_FILE_INFORMATION = unsafe { std::mem::zeroed() };
    if unsafe { GetFileInformationByHandle(original.file.as_raw_handle(), &mut info) } == 0 {
        return Err(Error::runtime(Code::Permissions));
    }
    Ok(info.nNumberOfLinks != 1 || info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT != 0)
}

#[cfg(unix)]
fn preserve_permissions(original: &Original, temporary: &Path) -> Result<()> {
    use std::os::{fd::AsRawFd, unix::fs::MetadataExt};
    let metadata = &original.metadata;
    let target = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(temporary)
        .map_err(|_| Error::runtime(Code::Permissions))?;
    let target_metadata = target
        .metadata()
        .map_err(|_| Error::runtime(Code::Permissions))?;
    // Ownership affects effective access too. Do not silently widen permissions
    // by publishing a file under another owner/group. chown can clear mode bits,
    // so mode and ACL restoration follows it.
    if (metadata.uid(), metadata.gid()) != (target_metadata.uid(), target_metadata.gid())
        && unsafe { libc::fchown(target.as_raw_fd(), metadata.uid(), metadata.gid()) } != 0
    {
        return Err(Error::runtime(Code::Permissions));
    }
    target
        .set_permissions(metadata.permissions())
        .map_err(|_| Error::runtime(Code::Permissions))?;
    preserve_acl(original, &target)
}

#[cfg(target_os = "linux")]
fn preserve_acl(original: &Original, target: &File) -> Result<()> {
    use std::os::fd::AsRawFd;
    // Linux POSIX access ACLs are kernel xattrs; using libc avoids a new libacl
    // dependency in the self-contained musl artifacts.
    let key = c"system.posix_acl_access";
    // fgetxattr does not accept O_PATH on supported kernels. The procfs magic
    // link keeps lookup bound to our held inode even if its pathname changes;
    // getxattr requests ACL metadata, not read access to file contents. Missing
    // procfs or inaccessible security metadata fails before replacement.
    let source = std::ffi::CString::new(format!("/proc/self/fd/{}", original.file.as_raw_fd()))
        .map_err(|_| Error::runtime(Code::Permissions))?;
    let length = unsafe { libc::getxattr(source.as_ptr(), key.as_ptr(), std::ptr::null_mut(), 0) };
    if length < 0 {
        let error = io::Error::last_os_error().raw_os_error();
        if error == Some(libc::ENODATA) || error == Some(libc::ENOTSUP) {
            let removed = unsafe { libc::fremovexattr(target.as_raw_fd(), key.as_ptr()) };
            if removed == 0
                || matches!(
                    io::Error::last_os_error().raw_os_error(),
                    Some(libc::ENODATA | libc::ENOTSUP)
                )
            {
                return Ok(());
            }
        }
        tracing::debug!(
            action = "read-acl",
            os_code = io::Error::last_os_error().raw_os_error(),
            "permission preservation failed"
        );
        return Err(Error::runtime(Code::Permissions));
    }
    let mut value = vec![0u8; length as usize];
    let read = unsafe {
        libc::getxattr(
            source.as_ptr(),
            key.as_ptr(),
            value.as_mut_ptr().cast(),
            value.len(),
        )
    };
    if read != length
        || unsafe {
            libc::fsetxattr(
                target.as_raw_fd(),
                key.as_ptr(),
                value.as_ptr().cast(),
                value.len(),
                0,
            )
        } != 0
    {
        tracing::debug!(
            action = "copy-acl",
            os_code = io::Error::last_os_error().raw_os_error(),
            "permission preservation failed"
        );
        return Err(Error::runtime(Code::Permissions));
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn preserve_acl(original: &Original, target: &File) -> Result<()> {
    use std::os::fd::AsRawFd;
    // Darwin's extended ACL API is supplied by libSystem, not an external tool.
    // libc does not expose these declarations; their ABI is in sys/acl.h.
    unsafe extern "C" {
        fn acl_init(count: libc::c_int) -> *mut libc::c_void;
        fn acl_get_link_np(path: *const libc::c_char, kind: libc::c_int) -> *mut libc::c_void;
        fn acl_set_fd_np(fd: libc::c_int, acl: *mut libc::c_void, kind: libc::c_int)
            -> libc::c_int;
        fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
    }
    const ACL_TYPE_EXTENDED: libc::c_int = 0x100;
    unsafe {
        let mut acl = acl_get_link_np(original.path.as_ptr(), ACL_TYPE_EXTENDED);
        // Darwin represents an absent extended ACL as ENOENT even for an existing
        // regular file. Apply an empty ACL to remove any inherited temp ACL.
        if acl.is_null() && io::Error::last_os_error().raw_os_error() == Some(libc::ENOENT) {
            acl = acl_init(0);
        }
        if acl.is_null() {
            tracing::debug!(
                action = "read-acl",
                os_code = io::Error::last_os_error().raw_os_error(),
                "permission preservation failed"
            );
            return Err(Error::runtime(Code::Permissions));
        }
        let result = acl_set_fd_np(target.as_raw_fd(), acl, ACL_TYPE_EXTENDED);
        acl_free(acl);
        if result != 0 {
            tracing::debug!(
                action = "write-acl",
                os_code = io::Error::last_os_error().raw_os_error(),
                "permission preservation failed"
            );
            return Err(Error::runtime(Code::Permissions));
        }
    }
    Ok(())
}

#[cfg(windows)]
fn preserve_permissions(original: &Original, temporary: &Path) -> Result<()> {
    use std::os::windows::{ffi::OsStrExt, io::AsRawHandle};

    use windows_sys::Win32::{
        Foundation::{LocalFree, ERROR_SUCCESS},
        Security::{
            Authorization::{GetSecurityInfo, SetNamedSecurityInfoW, SE_FILE_OBJECT},
            GetSecurityDescriptorControl, DACL_SECURITY_INFORMATION, GROUP_SECURITY_INFORMATION,
            OWNER_SECURITY_INFORMATION, PROTECTED_DACL_SECURITY_INFORMATION, SE_DACL_PROTECTED,
            UNPROTECTED_DACL_SECURITY_INFORMATION,
        },
    };
    let mut owner = std::ptr::null_mut();
    let mut group = std::ptr::null_mut();
    let mut dacl = std::ptr::null_mut();
    let mut descriptor = std::ptr::null_mut();
    let information =
        OWNER_SECURITY_INFORMATION | GROUP_SECURITY_INFORMATION | DACL_SECURITY_INFORMATION;
    let status = unsafe {
        GetSecurityInfo(
            original.file.as_raw_handle(),
            SE_FILE_OBJECT,
            information,
            &mut owner,
            &mut group,
            &mut dacl,
            std::ptr::null_mut(),
            &mut descriptor,
        )
    };
    if status != ERROR_SUCCESS {
        return Err(Error::runtime(Code::Permissions));
    }
    let mut control = 0;
    let mut revision = 0;
    let valid =
        unsafe { GetSecurityDescriptorControl(descriptor, &mut control, &mut revision) } != 0;
    let mut path: Vec<u16> = temporary.as_os_str().encode_wide().chain(Some(0)).collect();
    let flags = information
        | if control & SE_DACL_PROTECTED != 0 {
            PROTECTED_DACL_SECURITY_INFORMATION
        } else {
            UNPROTECTED_DACL_SECURITY_INFORMATION
        };
    let status = if valid {
        unsafe {
            SetNamedSecurityInfoW(
                path.as_mut_ptr(),
                SE_FILE_OBJECT,
                flags,
                owner,
                group,
                dacl,
                std::ptr::null(),
            )
        }
    } else {
        1
    };
    unsafe { LocalFree(descriptor) };
    if status != ERROR_SUCCESS {
        return Err(Error::runtime(Code::Permissions));
    }
    let permissions = original.metadata.permissions();
    fs::set_permissions(temporary, permissions).map_err(|_| Error::runtime(Code::Permissions))
}

#[cfg(all(unix, not(any(target_os = "linux", target_os = "macos"))))]
fn preserve_acl(_: &Original, _: &File) -> Result<()> {
    Err(Error::runtime(Code::Permissions))
}

#[cfg(test)]
mod tests {
    use std::io::Write;

    use super::*;

    #[test]
    fn publication_is_last_writer_wins_without_lost_update_detection() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("output");
        fs::write(&path, b"original").unwrap();
        let (first, mut first_file) = Publication::prepare(Some(path.clone()), true).unwrap();
        let (second, mut second_file) = Publication::prepare(Some(path.clone()), true).unwrap();
        first_file.as_mut().unwrap().write_all(b"first").unwrap();
        second_file.as_mut().unwrap().write_all(b"second").unwrap();
        drop(first_file);
        drop(second_file);
        second.publish(|| Ok(())).unwrap();
        first.publish(|| Ok(())).unwrap();
        assert_eq!(fs::read(path).unwrap(), b"first");
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[test]
    fn a_concurrently_created_destination_is_not_clobbered_without_force() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("output");
        let (publication, file) = Publication::prepare(Some(path.clone()), false).unwrap();
        drop(file);
        fs::write(&path, b"concurrent").unwrap();
        assert_eq!(
            publication.publish(|| Ok(())).unwrap_err().code,
            Code::OutputExists
        );
        assert_eq!(fs::read(path).unwrap(), b"concurrent");
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[test]
    fn a_failed_publication_cleans_up_its_temporary_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("output");
        let (publication, file) = Publication::prepare(Some(path.clone()), true).unwrap();
        drop(file);
        fs::create_dir(&path).unwrap();
        assert_eq!(
            publication.publish(|| Ok(())).unwrap_err().code,
            Code::UnsafeDestination
        );
        assert!(path.is_dir());
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[test]
    fn cancellation_at_the_publication_boundary_keeps_the_original() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("output");
        fs::write(&path, b"original").unwrap();
        let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
        file.as_mut().unwrap().write_all(b"changed").unwrap();
        drop(file);
        assert_eq!(
            publication
                .publish(|| Err(Error::runtime(Code::Cancelled)))
                .unwrap_err()
                .code,
            Code::Cancelled
        );
        assert_eq!(fs::read(path).unwrap(), b"original");
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_posix_access_acl_is_preserved() {
        use std::os::fd::AsRawFd;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("acl");
        let original = File::create(&path).unwrap();
        // Linux UAPI posix_acl_xattr: LE version, then tag/permissions/id entries.
        // A named user and mask make this an extended ACL, not just mode bits.
        let mut acl = 2u32.to_le_bytes().to_vec();
        for (tag, permissions, id) in [
            (1u16, 2u16, u32::MAX),
            (2, 4, 65534),
            (4, 0, u32::MAX),
            (16, 4, u32::MAX),
            (32, 0, u32::MAX),
        ] {
            acl.extend_from_slice(&tag.to_le_bytes());
            acl.extend_from_slice(&permissions.to_le_bytes());
            acl.extend_from_slice(&id.to_le_bytes());
        }
        let key = c"system.posix_acl_access";
        assert_eq!(
            unsafe {
                libc::fsetxattr(
                    original.as_raw_fd(),
                    key.as_ptr(),
                    acl.as_ptr().cast(),
                    acl.len(),
                    0,
                )
            },
            0
        );
        let (publication, mut output) = Publication::prepare(Some(path.clone()), true).unwrap();
        output.as_mut().unwrap().write_all(b"changed").unwrap();
        drop(output);
        publication.publish(|| Ok(())).unwrap();
        let replacement = fs::OpenOptions::new().write(true).open(&path).unwrap();
        let mut actual = vec![0u8; acl.len()];
        assert_eq!(
            unsafe {
                libc::fgetxattr(
                    replacement.as_raw_fd(),
                    key.as_ptr(),
                    actual.as_mut_ptr().cast(),
                    actual.len(),
                )
            },
            acl.len() as isize
        );
        assert_eq!(actual, acl);
    }

    #[cfg(windows)]
    #[test]
    fn windows_protected_dacl_is_preserved() {
        use std::os::windows::ffi::OsStrExt;

        use windows_sys::Win32::{
            Foundation::LocalFree,
            Security::{
                Authorization::{
                    ConvertSecurityDescriptorToStringSecurityDescriptorW,
                    ConvertStringSecurityDescriptorToSecurityDescriptorW, GetNamedSecurityInfoW,
                    SE_FILE_OBJECT,
                },
                SetFileSecurityW, SetSecurityDescriptorControl, DACL_SECURITY_INFORMATION,
                GROUP_SECURITY_INFORMATION, OWNER_SECURITY_INFORMATION,
                PROTECTED_DACL_SECURITY_INFORMATION, SE_DACL_AUTO_INHERITED,
            },
        };
        fn security(path: &[u16]) -> Vec<u16> {
            unsafe {
                let info = DACL_SECURITY_INFORMATION
                    | OWNER_SECURITY_INFORMATION
                    | GROUP_SECURITY_INFORMATION;
                let mut descriptor = std::ptr::null_mut();
                assert_eq!(
                    GetNamedSecurityInfoW(
                        path.as_ptr(),
                        SE_FILE_OBJECT,
                        info,
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                        &mut descriptor
                    ),
                    0
                );
                let mut text = std::ptr::null_mut();
                let mut length = 0;
                // SetNamedSecurityInfo records that the descriptor uses Windows'
                // current inheritance model by adding AUTO_INHERITED, including
                // to protected ACLs. It does not change access semantics:
                // https://learn.microsoft.com/windows/win32/secauthz/automatic-propagation-of-inheritable-aces
                // Compare owner/group, every ACE, and DACL protection exactly,
                // excluding only this bookkeeping bit in the retrieved copy.
                assert_ne!(
                    SetSecurityDescriptorControl(descriptor, SE_DACL_AUTO_INHERITED, 0),
                    0
                );
                assert_ne!(
                    ConvertSecurityDescriptorToStringSecurityDescriptorW(
                        descriptor,
                        1,
                        info,
                        &mut text,
                        &mut length
                    ),
                    0
                );
                let text_copy = std::slice::from_raw_parts(text, length as usize).to_vec();
                LocalFree(text.cast());
                LocalFree(descriptor);
                text_copy
            }
        }
        fn set_dacl(path: &[u16], sddl: &str) {
            let sddl: Vec<u16> = sddl.encode_utf16().chain(Some(0)).collect();
            unsafe {
                let mut descriptor = std::ptr::null_mut();
                assert_ne!(
                    ConvertStringSecurityDescriptorToSecurityDescriptorW(
                        sddl.as_ptr(),
                        1,
                        &mut descriptor,
                        std::ptr::null_mut()
                    ),
                    0
                );
                assert_ne!(
                    SetFileSecurityW(
                        path.as_ptr(),
                        DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                        descriptor
                    ),
                    0
                );
                LocalFree(descriptor);
            }
        }
        for deny_data in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            let path = dir.path().join("acl");
            fs::write(&path, b"original").unwrap();
            let wide: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
            let readable = "D:P(A;;FA;;;OW)(A;;FR;;;WD)";
            // Deny FILE_READ_DATA while retaining attributes/security access.
            set_dacl(
                &wide,
                if deny_data {
                    "D:P(D;;0x1;;;WD)(A;;FA;;;OW)(A;;FR;;;WD)"
                } else {
                    readable
                },
            );
            if deny_data {
                assert_eq!(
                    fs::read(&path).unwrap_err().kind(),
                    io::ErrorKind::PermissionDenied
                );
            }
            let before = security(&wide);
            let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
            file.as_mut().unwrap().write_all(b"changed").unwrap();
            drop(file);
            publication.publish(|| Ok(())).unwrap();
            assert_eq!(security(&wide), before);
            if deny_data {
                assert_eq!(
                    fs::read(&path).unwrap_err().kind(),
                    io::ErrorKind::PermissionDenied
                );
                set_dacl(&wide, readable);
            }
            assert_eq!(fs::read(path).unwrap(), b"changed");
        }
    }
}
