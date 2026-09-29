use std::{
    fs::{self, File},
    io,
    path::{Path, PathBuf},
};

use tempfile::Builder;
#[cfg(not(windows))]
use tempfile::NamedTempFile;
#[cfg(windows)]
use tempfile::TempPath;

use crate::transform_error::{Code, Error, Result};

pub struct Publication {
    #[cfg(not(windows))]
    temporary: Option<NamedTempFile>,
    #[cfg(windows)]
    temporary: Option<TempPath>,
    #[cfg(unix)]
    staging_directory: Option<StagingDirectory>,
    destination: Option<PathBuf>,
    replace: bool,
}

impl Publication {
    pub fn prepare(destination: Option<PathBuf>, replace: bool) -> Result<(Self, Option<File>)> {
        let mut publication = Self {
            #[cfg(not(windows))]
            temporary: None,
            #[cfg(windows)]
            temporary: None,
            #[cfg(unix)]
            staging_directory: None,
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
        #[cfg(unix)]
        let file = {
            let directory = StagingDirectory::new(parent)?;
            let temporary = Builder::new()
                .prefix(".clibox-")
                .tempfile_in(directory.path())
                .map_err(|error| {
                    staging_io_error("create-staging-file", error, Code::WriteFailed)
                })?;
            publication.staging_directory = Some(directory);
            publication.temporary = Some(temporary);

            use std::os::unix::fs::PermissionsExt;
            let temporary = publication.temporary.as_ref().unwrap();
            #[cfg(target_os = "macos")]
            clear_extended_acl_fd(temporary.as_file())
                .map_err(|error| staging_io_error("clear-staging-acl", error, Code::Permissions))?;
            // tempfile's requested mode is subject to umask. Reset it through
            // the open handle so the stage file remains owner-only until final
            // permissions are applied immediately before publication.
            temporary
                .as_file()
                .set_permissions(fs::Permissions::from_mode(0o600))
                .map_err(|error| {
                    staging_io_error("set-staging-file-mode", error, Code::Permissions)
                })?;

            temporary.as_file().try_clone().map_err(|error| {
                staging_io_error("open-staging-writer", error, Code::WriteFailed)
            })?
        };
        #[cfg(all(not(unix), not(windows)))]
        let file = {
            let temporary = Builder::new()
                .prefix(".clibox-")
                .tempfile_in(parent)
                .map_err(|_| Error::runtime(Code::WriteFailed))?;
            let file = temporary
                .as_file()
                .try_clone()
                .map_err(|_| Error::runtime(Code::WriteFailed))?;
            publication.temporary = Some(temporary);
            file
        };
        #[cfg(windows)]
        let file = {
            let temporary = Builder::new()
                .prefix(".clibox-")
                .tempfile_in(parent)
                .map_err(|_| Error::runtime(Code::WriteFailed))?;
            let (file, temporary) = temporary.into_parts();
            publication.temporary = Some(temporary);
            file
        };
        Ok((publication, Some(file)))
    }

    fn temporary_path(&self) -> &Path {
        #[cfg(windows)]
        {
            self.temporary.as_ref().unwrap()
        }
        #[cfg(not(windows))]
        {
            self.temporary.as_ref().unwrap().path()
        }
    }

    pub fn publish(mut self, before_commit: impl FnOnce() -> Result<()>) -> Result<()> {
        let Some(path) = &self.destination else {
            return Ok(());
        };
        let temporary_path = self.temporary_path().to_path_buf();
        // Flush through a writable handle before restoring a read-only mode/ACL.
        // FlushFileBuffers on Windows does not accept a read-only handle.
        #[cfg(windows)]
        let flush = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(&temporary_path)
            .and_then(|file| file.sync_all());
        #[cfg(not(windows))]
        let flush = self.temporary.as_ref().unwrap().as_file().sync_all();
        flush.map_err(|_| Error::runtime(Code::WriteFailed))?;
        #[cfg(windows)]
        let mut windows = WindowsPublication::prepare(&temporary_path)?;
        #[cfg(windows)]
        let mut readonly = false;
        // Recheck link/type policy and copy current permissions at publication,
        // without comparing file identities or providing lost-update protection.
        if let Some(original) = inspect(path, self.replace)? {
            #[cfg(windows)]
            {
                readonly = original.metadata.permissions().readonly();
            }
            preserve_permissions(&original, &temporary_path)?;
        } else {
            #[cfg(any(target_os = "macos", target_os = "linux"))]
            {
                // The destination may have disappeared after prepare. Match
                // direct-parent creation semantics from the final state, not
                // the state observed before transformation bytes were written.
                let parent = path
                    .parent()
                    .filter(|p| !p.as_os_str().is_empty())
                    .unwrap_or(Path::new("."));
                inherit_new_output_permissions(parent, self.temporary.as_ref().unwrap())?;
            }
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
                if let Err(error) = temporary.persist(path) {
                    self.temporary = Some(error.file);
                    return Err(Error::runtime(Code::PublishFailed));
                }
            } else {
                if let Err(error) = temporary.persist_noclobber(path) {
                    let code = if error.error.kind() == io::ErrorKind::AlreadyExists {
                        Code::OutputExists
                    } else {
                        Code::PublishFailed
                    };
                    self.temporary = Some(error.file);
                    return Err(Error::runtime(code));
                }
            }
        }
        Ok(())
    }
}

impl Drop for Publication {
    fn drop(&mut self) {
        #[cfg(target_os = "macos")]
        if let Some(temporary) = &self.temporary {
            // The final ACL may deny unlink after permission restoration. Clear
            // it through the retained descriptor before NamedTempFile attempts
            // cleanup; the owner-only staging directory still contains access.
            if let Err(error) = clear_extended_acl_fd(temporary.as_file()) {
                tracing::debug!(
                    action = "cleanup-acl",
                    os_code = error.raw_os_error(),
                    "temporary ACL cleanup failed"
                );
            }
        }
    }
}

#[cfg(unix)]
struct StagingDirectory(tempfile::TempDir);

#[cfg(unix)]
impl StagingDirectory {
    fn new(parent: &Path) -> Result<Self> {
        use std::os::unix::fs::PermissionsExt;

        let directory = Self(
            Builder::new()
                .prefix(".clibox-")
                .tempdir_in(parent)
                .map_err(|error| {
                    staging_io_error("create-staging-directory", error, Code::WriteFailed)
                })?,
        );
        #[cfg(target_os = "macos")]
        clear_extended_acl_path(directory.path()).map_err(|error| {
            staging_io_error("clear-staging-directory-acl", error, Code::Permissions)
        })?;
        fs::set_permissions(directory.path(), fs::Permissions::from_mode(0o700)).map_err(
            |error| staging_io_error("set-staging-directory-mode", error, Code::Permissions),
        )?;
        let mode = fs::metadata(directory.path())
            .map_err(|error| {
                staging_io_error("inspect-staging-directory-mode", error, Code::Permissions)
            })?
            .permissions()
            .mode()
            & 0o777;
        if mode != 0o700 {
            tracing::debug!(
                action = "verify-staging-directory-mode",
                observed_mode = mode,
                "owner-only staging directory could not be established"
            );
            return Err(Error::runtime(Code::Permissions));
        }
        Ok(directory)
    }

    fn path(&self) -> &Path {
        self.0.path()
    }
}

#[cfg(unix)]
impl Drop for StagingDirectory {
    fn drop(&mut self) {
        #[cfg(target_os = "macos")]
        if let Err(error) = clear_extended_acl_path(self.path()) {
            tracing::debug!(
                action = "cleanup-acl",
                os_code = error.raw_os_error(),
                "staging directory ACL cleanup failed"
            );
        }
    }
}

#[cfg(target_os = "macos")]
fn clear_extended_acl_path(path: &Path) -> io::Result<()> {
    use std::os::unix::ffi::OsStrExt;

    unsafe extern "C" {
        fn acl_init(count: libc::c_int) -> *mut libc::c_void;
        fn acl_set_file(
            path: *const libc::c_char,
            kind: libc::c_int,
            acl: *mut libc::c_void,
        ) -> libc::c_int;
        fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
    }
    const ACL_TYPE_EXTENDED: libc::c_int = 0x100;

    let path = std::ffi::CString::new(path.as_os_str().as_bytes())
        .map_err(|_| io::Error::from(io::ErrorKind::InvalidInput))?;
    // An empty extended ACL removes inherited grants and denials while the
    // stage is still empty. The mode is set independently after this call.
    unsafe {
        let acl = acl_init(0);
        if acl.is_null() {
            return Err(io::Error::last_os_error());
        }
        let result = acl_set_file(path.as_ptr(), ACL_TYPE_EXTENDED, acl);
        acl_free(acl);
        if result != 0 {
            return Err(io::Error::last_os_error());
        }
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn clear_extended_acl_fd(file: &File) -> io::Result<()> {
    use std::os::fd::AsRawFd;

    unsafe extern "C" {
        fn acl_init(count: libc::c_int) -> *mut libc::c_void;
        fn acl_set_fd_np(fd: libc::c_int, acl: *mut libc::c_void, kind: libc::c_int)
            -> libc::c_int;
        fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
    }
    const ACL_TYPE_EXTENDED: libc::c_int = 0x100;

    unsafe {
        let acl = acl_init(0);
        if acl.is_null() {
            return Err(io::Error::last_os_error());
        }
        let result = acl_set_fd_np(file.as_raw_fd(), acl, ACL_TYPE_EXTENDED);
        acl_free(acl);
        if result != 0 {
            return Err(io::Error::last_os_error());
        }
    }
    Ok(())
}

#[cfg(target_os = "macos")]
struct AclProbe(NamedTempFile);

#[cfg(target_os = "macos")]
impl AclProbe {
    fn clear_acl(&self) -> io::Result<()> {
        clear_extended_acl_fd(self.0.as_file())
    }
}

#[cfg(target_os = "macos")]
impl Drop for AclProbe {
    fn drop(&mut self) {
        if let Err(error) = self.clear_acl() {
            tracing::debug!(
                action = "cleanup-acl",
                os_code = error.raw_os_error(),
                "metadata probe ACL cleanup failed"
            );
        }
    }
}

#[cfg(any(target_os = "macos", target_os = "linux"))]
fn inherit_new_output_permissions(parent: &Path, temporary: &NamedTempFile) -> Result<()> {
    // Capture the mode and ACL a new file would inherit directly from its
    // destination parent. The empty probe never contains transformed bytes.
    // On macOS it is required because ACL inheritance happens at file creation;
    // on Linux it preserves umask and default-ACL behavior across the extra
    // private staging directory.
    let probe = Builder::new()
        .prefix(".clibox-")
        .tempfile_in(parent)
        .map_err(|error| staging_io_error("create-permission-probe", error, Code::Permissions))?;
    let metadata = probe
        .as_file()
        .metadata()
        .map_err(|error| staging_io_error("read-permission-probe", error, Code::Permissions))?;
    #[cfg(target_os = "macos")]
    {
        use std::os::unix::ffi::OsStrExt;

        let probe = AclProbe(probe);
        let path = std::ffi::CString::new(probe.0.path().as_os_str().as_bytes())
            .map_err(|_| Error::runtime(Code::Permissions))?;
        let original = Original { metadata, path };
        let copy = preserve_permissions(&original, temporary.path());
        let cleanup = probe
            .clear_acl()
            .map_err(|_| Error::runtime(Code::Permissions));
        copy?;
        cleanup
    }
    #[cfg(target_os = "linux")]
    {
        let original = Original {
            metadata,
            file: probe
                .as_file()
                .try_clone()
                .map_err(|_| Error::runtime(Code::Permissions))?,
        };
        preserve_permissions(&original, temporary.path())
    }
}

#[cfg(unix)]
fn staging_io_error(action: &'static str, error: io::Error, code: Code) -> Error {
    tracing::debug!(
        action,
        os_code = error.raw_os_error(),
        "transformation staging operation failed"
    );
    Error::runtime(code)
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

#[cfg(test)]
mod tests {
    use std::io::Write;

    use super::*;

    #[cfg(windows)]
    #[test]
    fn windows_readonly_publication_preserves_original_attributes_on_every_outcome() {
        use std::os::windows::fs::OpenOptionsExt;

        use windows_sys::Win32::Storage::FileSystem::FILE_SHARE_READ;

        for failure in [None, Some(Code::Cancelled), Some(Code::PublishFailed)] {
            let dir = tempfile::tempdir().unwrap();
            let path = dir.path().join("read only output");
            fs::write(&path, b"original").unwrap();
            let mut permissions = fs::metadata(&path).unwrap().permissions();
            permissions.set_readonly(true);
            fs::set_permissions(&path, permissions).unwrap();
            let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
            file.as_mut().unwrap().write_all(b"replacement").unwrap();
            drop(file);
            // Deny delete sharing only for this disposable fixture's failure case.
            let blocker = (failure == Some(Code::PublishFailed)).then(|| {
                fs::OpenOptions::new()
                    .read(true)
                    .share_mode(FILE_SHARE_READ)
                    .open(&path)
                    .unwrap()
            });
            let result = publication.publish(|| {
                assert!(fs::metadata(&path).unwrap().permissions().readonly());
                assert_eq!(fs::read(&path).unwrap(), b"original");
                if failure == Some(Code::Cancelled) {
                    Err(Error::runtime(Code::Cancelled))
                } else {
                    Ok(())
                }
            });
            match failure {
                Some(code) => assert_eq!(result.unwrap_err().code, code),
                None => result.unwrap(),
            }
            drop(blocker);
            assert!(fs::metadata(&path).unwrap().permissions().readonly());
            assert_eq!(
                fs::read(&path).unwrap(),
                if failure.is_none() {
                    b"replacement".as_slice()
                } else {
                    b"original".as_slice()
                }
            );
            assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
            let mut permissions = fs::metadata(&path).unwrap().permissions();
            permissions.set_readonly(false);
            fs::set_permissions(path, permissions).unwrap();
        }
    }

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

    #[cfg(target_os = "macos")]
    #[test]
    fn cancellation_cleans_a_temporary_after_restrictive_acl_copy() {
        use std::{
            ffi::{CStr, CString},
            os::unix::ffi::OsStrExt,
            process::Command,
        };

        unsafe extern "C" {
            fn acl_get_link_np(path: *const libc::c_char, kind: libc::c_int) -> *mut libc::c_void;
            fn acl_to_text(acl: *mut libc::c_void, length: *mut libc::ssize_t)
                -> *mut libc::c_char;
            fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
        }
        fn acl_text(path: &Path) -> String {
            let path = CString::new(path.as_os_str().as_bytes()).unwrap();
            unsafe {
                let acl = acl_get_link_np(path.as_ptr(), 0x100);
                assert!(
                    !acl.is_null(),
                    "ACL query failed: {}",
                    io::Error::last_os_error()
                );
                let text = acl_to_text(acl, std::ptr::null_mut());
                assert!(!text.is_null());
                let value = CStr::from_ptr(text).to_string_lossy().into_owned();
                acl_free(text.cast());
                acl_free(acl);
                value
            }
        }

        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("destination");
        fs::write(&path, b"original").unwrap();
        assert!(Command::new("/bin/chmod")
            .args(["+a", "everyone deny delete"])
            .arg(&path)
            .status()
            .unwrap()
            .success());
        let original_acl = acl_text(&path);
        let original_mode = fs::metadata(&path).unwrap().permissions();
        let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
        file.as_mut().unwrap().write_all(b"replacement").unwrap();
        drop(file);
        let temporary_path = publication.temporary.as_ref().unwrap().path().to_path_buf();

        let result = publication.publish(|| {
            assert_eq!(acl_text(&temporary_path), original_acl);
            Err(Error::runtime(Code::Cancelled))
        });

        assert_eq!(result.unwrap_err().code, Code::Cancelled);
        assert_eq!(fs::read(&path).unwrap(), b"original");
        assert_eq!(acl_text(&path), original_acl);
        assert_eq!(fs::metadata(&path).unwrap().permissions(), original_mode);
        assert!(!temporary_path.exists());
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 1);
        assert!(Command::new("/bin/chmod")
            .args(["-N"])
            .arg(&path)
            .status()
            .unwrap()
            .success());
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn missing_destination_at_publish_gets_parent_inherited_acl() {
        use std::{
            ffi::{CStr, CString},
            os::unix::ffi::OsStrExt,
            process::Command,
        };

        unsafe extern "C" {
            fn acl_get_link_np(path: *const libc::c_char, kind: libc::c_int) -> *mut libc::c_void;
            fn acl_to_text(acl: *mut libc::c_void, length: *mut libc::ssize_t)
                -> *mut libc::c_char;
            fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
        }
        fn acl_text(path: &Path) -> String {
            let path = CString::new(path.as_os_str().as_bytes()).unwrap();
            unsafe {
                let acl = acl_get_link_np(path.as_ptr(), 0x100);
                assert!(
                    !acl.is_null(),
                    "ACL query failed: {}",
                    io::Error::last_os_error()
                );
                let text = acl_to_text(acl, std::ptr::null_mut());
                assert!(!text.is_null());
                let value = CStr::from_ptr(text).to_string_lossy().into_owned();
                acl_free(text.cast());
                acl_free(acl);
                value
            }
        }

        let directory = tempfile::tempdir().unwrap();
        assert!(Command::new("/bin/chmod")
            .args([
                "+a",
                "everyone allow read,readattr,readextattr,readsecurity,file_inherit"
            ])
            .arg(directory.path())
            .status()
            .unwrap()
            .success());
        let expected_probe = Builder::new()
            .prefix(".clibox-expected-")
            .tempfile_in(directory.path())
            .unwrap();
        let expected_acl = acl_text(expected_probe.path());
        let expected_mode = fs::metadata(expected_probe.path()).unwrap().permissions();
        drop(expected_probe);
        assert!(expected_acl.contains("everyone:") && expected_acl.contains(":allow"));

        let path = directory.path().join("destination");
        fs::write(&path, b"original").unwrap();
        assert!(Command::new("/bin/chmod")
            .args(["-N"])
            .arg(&path)
            .status()
            .unwrap()
            .success());
        let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
        file.as_mut().unwrap().write_all(b"replacement").unwrap();
        drop(file);
        fs::remove_file(&path).unwrap();

        publication.publish(|| Ok(())).unwrap();

        assert_eq!(fs::read(&path).unwrap(), b"replacement");
        assert_eq!(acl_text(&path), expected_acl);
        assert_eq!(fs::metadata(&path).unwrap().permissions(), expected_mode);
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 1);
        assert!(Command::new("/bin/chmod")
            .args(["-RN"])
            .arg(&path)
            .status()
            .unwrap()
            .success());
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

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_missing_destination_at_publish_gets_parent_inherited_acl() {
        use std::{
            ffi::CString,
            os::{fd::AsRawFd, unix::ffi::OsStrExt},
        };

        let dir = tempfile::tempdir().unwrap();
        // Give direct children a named-user ACL. tempfile's 0600 creation mode
        // masks its effective group-class rights, but the inherited access ACL
        // remains observable and must be copied to the published new output.
        let mut default_acl = 2u32.to_le_bytes().to_vec();
        for (tag, permissions, id) in [
            (1u16, 7u16, u32::MAX),
            (2, 4, 65534),
            (4, 0, u32::MAX),
            (16, 4, u32::MAX),
            (32, 0, u32::MAX),
        ] {
            default_acl.extend_from_slice(&tag.to_le_bytes());
            default_acl.extend_from_slice(&permissions.to_le_bytes());
            default_acl.extend_from_slice(&id.to_le_bytes());
        }
        let directory_path = CString::new(dir.path().as_os_str().as_bytes()).unwrap();
        let default_key = c"system.posix_acl_default";
        assert_eq!(
            unsafe {
                libc::setxattr(
                    directory_path.as_ptr(),
                    default_key.as_ptr(),
                    default_acl.as_ptr().cast(),
                    default_acl.len(),
                    0,
                )
            },
            0,
            "could not set fixture default ACL: {}",
            io::Error::last_os_error()
        );

        let expected_probe = Builder::new()
            .prefix(".clibox-expected-")
            .tempfile_in(dir.path())
            .unwrap();
        let key = c"system.posix_acl_access";
        let expected_len = unsafe {
            libc::fgetxattr(
                expected_probe.as_file().as_raw_fd(),
                key.as_ptr(),
                std::ptr::null_mut(),
                0,
            )
        };
        assert!(expected_len > 0);
        let mut expected_acl = vec![0; expected_len as usize];
        assert_eq!(
            unsafe {
                libc::fgetxattr(
                    expected_probe.as_file().as_raw_fd(),
                    key.as_ptr(),
                    expected_acl.as_mut_ptr().cast(),
                    expected_acl.len(),
                )
            },
            expected_len
        );
        drop(expected_probe);

        let path = dir.path().join("destination");
        fs::write(&path, b"original").unwrap();
        let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
        file.as_mut().unwrap().write_all(b"replacement").unwrap();
        drop(file);
        fs::remove_file(&path).unwrap();

        publication.publish(|| Ok(())).unwrap();

        assert_eq!(fs::read(&path).unwrap(), b"replacement");
        let output = fs::OpenOptions::new().read(true).open(&path).unwrap();
        let mut actual = vec![0; expected_acl.len()];
        assert_eq!(
            unsafe {
                libc::fgetxattr(
                    output.as_raw_fd(),
                    key.as_ptr(),
                    actual.as_mut_ptr().cast(),
                    actual.len(),
                )
            },
            expected_acl.len() as isize
        );
        assert_eq!(actual, expected_acl);
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[cfg(windows)]
    #[test]
    fn windows_protected_dacl_is_preserved() {
        use std::os::windows::{ffi::OsStrExt, fs::OpenOptionsExt};

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
            Storage::FileSystem::FILE_WRITE_ATTRIBUTES,
        };
        fn assert_attribute_access(path: &Path, denied: bool, phase: &str) {
            // Reapplying unchanged attributes with SetFileAttributesW can succeed
            // despite a FILE_WRITE_ATTRIBUTES denial. Request that exact access
            // on a fresh handle to test the DACL without mutating the fixture.
            // https://learn.microsoft.com/windows/win32/fileio/file-access-rights-constants
            let result = fs::OpenOptions::new()
                .access_mode(FILE_WRITE_ATTRIBUTES)
                .open(path);
            if denied {
                assert_eq!(
                    result.expect_err(phase).kind(),
                    io::ErrorKind::PermissionDenied,
                    "{phase}"
                );
            } else {
                assert!(result.is_ok(), "{phase}: attribute access must be allowed");
            }
        }
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
        let readable = "D:P(A;;FA;;;OW)(A;;FR;;;WD)";
        for (deny_data, deny_attributes, acl) in [
            (false, false, readable),
            (true, false, "D:P(D;;0x1;;;WD)(A;;FA;;;OW)(A;;FR;;;WD)"),
            (false, true, "D:P(D;;0x100;;;WD)(A;;FA;;;OW)(A;;FR;;;WD)"),
        ] {
            let dir = tempfile::tempdir().unwrap();
            let path = dir.path().join("acl");
            fs::write(&path, b"original").unwrap();
            let wide: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
            // Deny data reads or attribute writes while retaining metadata/security reads.
            set_dacl(&wide, acl);
            if deny_data {
                assert_eq!(
                    fs::read(&path).unwrap_err().kind(),
                    io::ErrorKind::PermissionDenied
                );
            }
            assert_attribute_access(&path, deny_attributes, "before publication");
            let before = security(&wide);
            let (cancelled, file) = Publication::prepare(Some(path.clone()), true).unwrap();
            drop(file);
            assert_eq!(
                cancelled
                    .publish(|| Err(Error::runtime(Code::Cancelled)))
                    .unwrap_err()
                    .code,
                Code::Cancelled
            );
            assert_eq!(security(&wide), before);
            assert_attribute_access(&path, deny_attributes, "after cancellation");
            assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
            let (publication, mut file) = Publication::prepare(Some(path.clone()), true).unwrap();
            file.as_mut().unwrap().write_all(b"changed").unwrap();
            drop(file);
            publication.publish(|| Ok(())).unwrap();
            assert_eq!(security(&wide), before);
            assert_attribute_access(&path, deny_attributes, "after publication");
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
