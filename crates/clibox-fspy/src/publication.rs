//! Native publication for fspy reports.
//!
//! Keep this purpose-local copy aligned with the clibox transformation
//! publication rules while companion crates must not depend on one another.
//! Remove the duplication only if a separately owned shared publication
//! primitive is introduced under an updated repository contract.

use std::{
    fs::{self, File},
    io,
    path::{Path, PathBuf},
};

use tempfile::{Builder, TempPath};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Code {
    ReadFailed,
    WriteFailed,
    OutputExists,
    UnsafeDestination,
    Permissions,
    PublishFailed,
}

#[derive(Debug)]
struct Error {
    code: Code,
}

type Result<T> = std::result::Result<T, Error>;

impl Error {
    fn runtime(code: Code) -> Self {
        Self { code }
    }
}

struct Publication {
    temporary: Option<TempPath>,
    destination: Option<PathBuf>,
    replace: bool,
}

impl Publication {
    fn prepare(destination: Option<PathBuf>, replace: bool) -> Result<(Self, Option<File>)> {
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

    fn publish(mut self, before_commit: impl FnOnce() -> Result<()>) -> Result<()> {
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
        #[cfg(windows)]
        let mut windows = WindowsPublication::prepare(temporary)?;
        #[cfg(windows)]
        let mut readonly = false;
        // Recheck link/type policy and copy current permissions at publication,
        // without comparing file identities or providing lost-update protection.
        if let Some(original) = inspect(path, self.replace)? {
            #[cfg(windows)]
            {
                readonly = original.metadata.permissions().readonly();
            }
            preserve_permissions(&original, temporary)?;
        }
        // Cancellation during flushing/permission work must still prevent publication.
        before_commit()?;
        #[cfg(windows)]
        {
            windows.persist(path, self.replace, readonly)?;
            self.temporary.as_mut().unwrap().disable_cleanup(true);
        }
        #[cfg(not(windows))]
        {
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
        }
        Ok(())
    }
}

#[cfg(windows)]
struct WindowsPublication {
    file: File,
    committed: bool,
}

#[cfg(windows)]
impl WindowsPublication {
    fn prepare(path: &Path) -> Result<Self> {
        use std::os::windows::fs::OpenOptionsExt;

        use windows_sys::Win32::Storage::FileSystem::{DELETE, FILE_WRITE_ATTRIBUTES};

        // Retain rename/cleanup authority before copying any restrictive ACL.
        let file = fs::OpenOptions::new()
            .access_mode(DELETE | FILE_WRITE_ATTRIBUTES)
            .open(path)
            .map_err(|_| Error::runtime(Code::Permissions))?;
        let publication = Self {
            file,
            committed: false,
        };
        publication
            .clear_temporary_attributes()
            .map_err(|_| Error::runtime(Code::Permissions))?;
        Ok(publication)
    }

    fn clear_temporary_attributes(&self) -> io::Result<()> {
        use std::os::windows::io::AsRawHandle;

        use windows_sys::Win32::Storage::FileSystem::{
            FileBasicInfo, SetFileInformationByHandle, FILE_ATTRIBUTE_NORMAL, FILE_BASIC_INFO,
        };
        let information = FILE_BASIC_INFO {
            FileAttributes: FILE_ATTRIBUTE_NORMAL,
            ..Default::default()
        };
        if unsafe {
            SetFileInformationByHandle(
                self.file.as_raw_handle(),
                FileBasicInfo,
                (&information as *const FILE_BASIC_INFO).cast(),
                std::mem::size_of_val(&information) as u32,
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    }

    fn persist(&mut self, destination: &Path, replace: bool, readonly: bool) -> Result<()> {
        use std::os::windows::{ffi::OsStrExt, io::AsRawHandle};

        use windows_sys::Win32::Storage::FileSystem::{
            FileRenameInfoEx, SetFileInformationByHandle, FILE_RENAME_INFO,
        };

        let destination =
            std::path::absolute(destination).map_err(|_| Error::runtime(Code::PublishFailed))?;
        let name: Vec<u16> = destination.as_os_str().encode_wide().collect();
        let bytes = std::mem::size_of::<FILE_RENAME_INFO>()
            .checked_add(
                name.len()
                    .checked_mul(2)
                    .ok_or_else(|| Error::runtime(Code::PublishFailed))?,
            )
            .and_then(|size| u32::try_from(size).ok())
            .ok_or_else(|| Error::runtime(Code::PublishFailed))?;
        // usize backing storage supplies the SDK structure's pointer alignment.
        let mut buffer = vec![0usize; (bytes as usize).div_ceil(std::mem::size_of::<usize>())];
        let information = buffer.as_mut_ptr().cast::<FILE_RENAME_INFO>();
        // FileRenameInfoEx can replace a read-only destination without temporarily
        // changing the original's attributes. The OS still requires target write-
        // attribute permission. Unsupported filesystems fail without modifying it.
        // https://learn.microsoft.com/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information
        const REPLACE_IF_EXISTS: u32 = 0x1;
        const IGNORE_READONLY_ATTRIBUTE: u32 = 0x40;
        let success = unsafe {
            (*information).Anonymous.Flags = if replace {
                REPLACE_IF_EXISTS
                    | if readonly {
                        IGNORE_READONLY_ATTRIBUTE
                    } else {
                        0
                    }
            } else {
                0
            };
            (*information).FileNameLength = (name.len() * 2) as u32;
            std::ptr::copy_nonoverlapping(
                name.as_ptr(),
                std::ptr::addr_of_mut!((*information).FileName).cast::<u16>(),
                name.len(),
            );
            SetFileInformationByHandle(
                self.file.as_raw_handle(),
                FileRenameInfoEx,
                information.cast(),
                bytes,
            )
        };
        if success == 0 {
            let error = io::Error::last_os_error();
            tracing::debug!(
                action = "publish",
                os_code = error.raw_os_error(),
                "file publication failed"
            );
            return Err(Error::runtime(
                if !replace && error.kind() == io::ErrorKind::AlreadyExists {
                    Code::OutputExists
                } else {
                    Code::PublishFailed
                },
            ));
        }
        self.committed = true;
        Ok(())
    }
}

#[cfg(windows)]
impl Drop for WindowsPublication {
    fn drop(&mut self) {
        use std::os::windows::io::AsRawHandle;

        use windows_sys::Win32::Storage::FileSystem::{
            FileDispositionInfo, SetFileInformationByHandle, FILE_DISPOSITION_INFO,
        };
        if self.committed {
            return;
        }
        // Only the unpublished temporary inode is changed. Held access survives
        // a copied restrictive ACL and permits cleanup of read-only output.
        let result = self.clear_temporary_attributes().and_then(|()| {
            let information = FILE_DISPOSITION_INFO { DeleteFile: true };
            if unsafe {
                SetFileInformationByHandle(
                    self.file.as_raw_handle(),
                    FileDispositionInfo,
                    (&information as *const FILE_DISPOSITION_INFO).cast(),
                    std::mem::size_of_val(&information) as u32,
                )
            } == 0
            {
                Err(io::Error::last_os_error())
            } else {
                Ok(())
            }
        });
        if let Err(error) = result {
            tracing::debug!(
                action = "cleanup",
                os_code = error.raw_os_error(),
                "temporary cleanup failed"
            );
        }
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
    // Set access attributes while the temporary file still has its creation ACL.
    // The original DACL may legitimately deny FILE_WRITE_ATTRIBUTES, while its
    // parent still grants replacement. No pathname attribute writes follow it.
    fs::set_permissions(temporary, original.metadata.permissions())
        .map_err(|_| Error::runtime(Code::Permissions))?;
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
    Ok(())
}

#[cfg(all(unix, not(any(target_os = "linux", target_os = "macos"))))]
fn preserve_acl(_: &Original, _: &File) -> Result<()> {
    Err(Error::runtime(Code::Permissions))
}

/// Exercise the same destination and parent checks before launching a child.
pub(crate) fn preflight_file_destination(
    destination: &Path,
    replace: bool,
) -> std::result::Result<(), Code> {
    let (_publication, _file) = Publication::prepare(Some(destination.to_path_buf()), replace)
        .map_err(|error| error.code)?;
    Ok(())
}

/// Publish a completed report through the native replacement path.
pub(crate) fn publish_file_bytes(
    destination: &Path,
    replace: bool,
    bytes: &[u8],
) -> std::result::Result<(), Code> {
    use std::io::Write;

    let (publication, file) = Publication::prepare(Some(destination.to_path_buf()), replace)
        .map_err(|error| error.code)?;
    let mut file = file.ok_or(Code::WriteFailed)?;
    file.write_all(bytes).map_err(|_| Code::WriteFailed)?;
    drop(file);
    publication.publish(|| Ok(())).map_err(|error| error.code)
}
