//! Preserve access before committing a Windows configuration file with one
//! rename.
use std::{
    fs::{File, OpenOptions},
    os::windows::{ffi::OsStrExt, fs::OpenOptionsExt, io::AsRawHandle},
    path::Path,
};

use windows_sys::Win32::{
    Foundation::{LocalFree, ERROR_SUCCESS},
    Security::{
        Authorization::{GetSecurityInfo, SetSecurityInfo, SE_FILE_OBJECT},
        GetSecurityDescriptorControl, DACL_SECURITY_INFORMATION,
        PROTECTED_DACL_SECURITY_INFORMATION, SE_DACL_PROTECTED,
        UNPROTECTED_DACL_SECURITY_INFORMATION,
    },
    Storage::FileSystem::{
        FileBasicInfo, FileDispositionInfo, FileRenameInfo, SetFileInformationByHandle, DELETE,
        FILE_ATTRIBUTE_NORMAL, FILE_BASIC_INFO, FILE_DISPOSITION_INFO, FILE_RENAME_INFO,
        FILE_WRITE_ATTRIBUTES, WRITE_DAC,
    },
};

use crate::config_runtime::{Failure, Result};

pub(super) struct Publication {
    file: File,
    committed: bool,
}

impl Publication {
    pub(super) fn prepare(path: &Path, preserve_dacl: bool) -> Result<Self> {
        // Retain rename, ACL and cleanup authority before applying a DACL that
        // may deny new handles. The staging writer shares deletion access.
        let file = OpenOptions::new()
            .access_mode(DELETE | FILE_WRITE_ATTRIBUTES | if preserve_dacl { WRITE_DAC } else { 0 })
            .open(path)
            .map_err(|_| Failure::Permissions)?;
        let publication = Self {
            file,
            committed: false,
        };
        let information = FILE_BASIC_INFO {
            FileAttributes: FILE_ATTRIBUTE_NORMAL,
            ..Default::default()
        };
        // SAFETY: the held staging handle has attribute access and the complete
        // initialized SDK structure remains live for this synchronous call.
        if unsafe {
            SetFileInformationByHandle(
                publication.file.as_raw_handle(),
                FileBasicInfo,
                (&information as *const FILE_BASIC_INFO).cast(),
                std::mem::size_of_val(&information) as u32,
            )
        } == 0
        {
            return Err(Failure::Permissions.into());
        }
        Ok(publication)
    }

    pub(super) fn preserve_dacl(&self, source: &File) -> Result<()> {
        let mut dacl = std::ptr::null_mut();
        let mut descriptor = std::ptr::null_mut();
        // SAFETY: source is a live validated destination handle; Windows owns
        // the returned descriptor until LocalFree below. The DACL points into it.
        let status = unsafe {
            GetSecurityInfo(
                source.as_raw_handle(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                &mut dacl,
                std::ptr::null_mut(),
                &mut descriptor,
            )
        };
        if status != ERROR_SUCCESS {
            return Err(Failure::Permissions.into());
        }
        let mut control = 0;
        let mut revision = 0;
        let valid =
            unsafe { GetSecurityDescriptorControl(descriptor, &mut control, &mut revision) } != 0;
        let information = DACL_SECURITY_INFORMATION
            | if control & SE_DACL_PROTECTED != 0 {
                PROTECTED_DACL_SECURITY_INFORMATION
            } else {
                UNPROTECTED_DACL_SECURITY_INFORMATION
            };
        let status = if valid {
            // The handle's WRITE_DAC right remains valid after the copied DACL.
            unsafe {
                SetSecurityInfo(
                    self.file.as_raw_handle(),
                    SE_FILE_OBJECT,
                    information,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    dacl,
                    std::ptr::null(),
                )
            }
        } else {
            1
        };
        unsafe { LocalFree(descriptor) };
        if status != ERROR_SUCCESS {
            return Err(Failure::Permissions.into());
        }
        Ok(())
    }

    pub(super) fn commit(&mut self, destination: &Path, replace: bool) -> Result<()> {
        let destination = std::path::absolute(destination).map_err(|_| Failure::Publish)?;
        let name: Vec<u16> = destination.as_os_str().encode_wide().collect();
        let bytes = std::mem::size_of::<FILE_RENAME_INFO>()
            .checked_add(name.len().checked_mul(2).ok_or(Failure::Publish)?)
            .and_then(|size| u32::try_from(size).ok())
            .ok_or(Failure::Publish)?;
        // usize storage retains pointer alignment for the variable-size SDK structure.
        let mut buffer = vec![0usize; (bytes as usize).div_ceil(std::mem::size_of::<usize>())];
        let information = buffer.as_mut_ptr().cast::<FILE_RENAME_INFO>();
        // ReplaceFileW performs multiple namespace changes, which can interfere
        // with another publisher and return failure after changing names. A
        // single handle-based rename keeps success tied to one commit instead.
        // https://learn.microsoft.com/windows/win32/api/fileapi/nf-fileapi-setfileinformationbyhandle
        // SAFETY: buffer is aligned, zero-initialized, and large enough for the
        // complete name. Both buffer and staging handle live through the call.
        let success = unsafe {
            (*information).Anonymous.ReplaceIfExists = replace;
            (*information).FileNameLength = (name.len() * 2) as u32;
            std::ptr::copy_nonoverlapping(
                name.as_ptr(),
                std::ptr::addr_of_mut!((*information).FileName).cast::<u16>(),
                name.len(),
            );
            SetFileInformationByHandle(
                self.file.as_raw_handle(),
                FileRenameInfo,
                information.cast(),
                bytes,
            )
        };
        if success == 0 {
            let error = std::io::Error::last_os_error();
            tracing::debug!(
                operation = "publish",
                os_code = error.raw_os_error(),
                "configuration rename failed"
            );
            return Err(
                if !replace && error.kind() == std::io::ErrorKind::AlreadyExists {
                    Failure::DestinationExists
                } else {
                    Failure::Publish
                }
                .into(),
            );
        }
        self.committed = true;
        Ok(())
    }
}

impl Drop for Publication {
    fn drop(&mut self) {
        if self.committed {
            return;
        }
        // The held DELETE right survives restrictive copied permissions. Mark
        // only our unpublished staging object for removal when its handles close.
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
            tracing::debug!(
                operation = "cleanup",
                os_code = std::io::Error::last_os_error().raw_os_error(),
                "configuration staging cleanup failed"
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use windows_sys::Win32::Security::{
        Authorization::ConvertStringSecurityDescriptorToSecurityDescriptorW,
        GetSecurityDescriptorDacl,
    };

    use super::*;

    #[test]
    fn no_clobber_commit_preserves_a_destination_created_after_preparation() {
        let dir = tempfile::tempdir().unwrap();
        let staging = dir.path().join("unpublished");
        let destination = dir.path().join("output");
        std::fs::write(&staging, b"replacement").unwrap();
        let mut publication = Publication::prepare(&staging, false).unwrap();
        std::fs::write(&destination, b"concurrent writer").unwrap();
        assert_eq!(
            publication.commit(&destination, false).unwrap_err().kind,
            Failure::DestinationExists
        );
        drop(publication);
        assert_eq!(std::fs::read(&destination).unwrap(), b"concurrent writer");
        assert!(!staging.exists());
    }

    #[test]
    fn retained_delete_access_cleans_staging_after_a_restrictive_dacl() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("unpublished");
        std::fs::write(&path, b"unpublished fixture").unwrap();
        let publication = Publication::prepare(&path, true).unwrap();
        // Deny DELETE and WRITE_DAC on future handles while the existing handle
        // retains both rights, as it does after copying restrictive permissions.
        let sddl: Vec<u16> = "D:P(D;;SDWD;;;WD)(A;;FA;;;OW)"
            .encode_utf16()
            .chain([0])
            .collect();
        let mut descriptor = std::ptr::null_mut();
        unsafe {
            assert_ne!(
                ConvertStringSecurityDescriptorToSecurityDescriptorW(
                    sddl.as_ptr(),
                    1,
                    &mut descriptor,
                    std::ptr::null_mut(),
                ),
                0
            );
            let mut dacl = std::ptr::null_mut();
            let mut present = 0;
            let mut defaulted = 0;
            assert_ne!(
                GetSecurityDescriptorDacl(descriptor, &mut present, &mut dacl, &mut defaulted),
                0
            );
            assert_ne!(present, 0);
            assert_eq!(
                SetSecurityInfo(
                    publication.file.as_raw_handle(),
                    SE_FILE_OBJECT,
                    DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    dacl,
                    std::ptr::null(),
                ),
                ERROR_SUCCESS
            );
            LocalFree(descriptor);
        }
        assert_eq!(
            OpenOptions::new()
                .access_mode(DELETE | WRITE_DAC)
                .open(&path)
                .unwrap_err()
                .kind(),
            std::io::ErrorKind::PermissionDenied
        );
        drop(publication);
        assert!(
            !path.exists(),
            "the retained handle must remove its unpublished file"
        );
    }
}
