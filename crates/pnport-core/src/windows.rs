// SPDX-License-Identifier: Apache-2.0
//! Windows native identities and private storage. No shell or external ACL
//! tool.

use std::{
    ffi::OsString,
    fs::{self, File, OpenOptions},
    io,
    mem::size_of,
    os::windows::{
        ffi::{OsStrExt, OsStringExt},
        fs::{MetadataExt, OpenOptionsExt},
        io::AsRawHandle,
    },
    path::{Path, PathBuf},
    ptr,
};

use windows_sys::Win32::{
    Foundation::{CloseHandle, LocalFree, HANDLE, INVALID_HANDLE_VALUE},
    Security::{
        Authorization::{
            ConvertSidToStringSidW, ConvertStringSecurityDescriptorToSecurityDescriptorW,
            GetSecurityInfo, SE_FILE_OBJECT,
        },
        *,
    },
    Storage::FileSystem::{
        self, FILE_ATTRIBUTE_REPARSE_POINT, FILE_FLAG_BACKUP_SEMANTICS,
        FILE_FLAG_OPEN_REPARSE_POINT,
    },
    System::Threading::{GetCurrentProcess, OpenProcessToken},
};

pub struct Handle(pub HANDLE);
impl Drop for Handle {
    fn drop(&mut self) {
        if !self.0.is_null() && self.0 != INVALID_HANDLE_VALUE {
            unsafe {
                CloseHandle(self.0);
            }
        }
    }
}

#[derive(Clone, serde::Serialize, serde::Deserialize)]
pub struct LaunchContext {
    pub format: u32,
    pub session: PathBuf,
    pub cache: PathBuf,
    pub artifact: PathBuf,
    pub launch: String,
    pub debug: bool,
}

pub fn wide(path: &Path) -> io::Result<Vec<u16>> {
    let mut value: Vec<_> = path.as_os_str().encode_wide().collect();
    if value.contains(&0) {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    value.push(0);
    Ok(value)
}

/// Convert Win32 extended and NT DOS spellings to the same graph namespace.
/// Keep UTF-16 code units, including unpaired surrogates, intact.
pub fn dos_path(units: &[u16]) -> PathBuf {
    let strip = units.starts_with(&[92, 92, 63, 92]) || units.starts_with(&[92, 63, 63, 92]);
    let mut value = if strip {
        units[4..].to_vec()
    } else {
        units.to_vec()
    };
    if strip && value.len() >= 4 && value[..4].eq_ignore_ascii_case(&[85, 78, 67, 92]) {
        value.splice(..4, [92, 92]);
    }
    PathBuf::from(OsString::from_wide(&value))
}

/// Recover native casing/reparse prefixes one component at a time. Check a
/// dependency namespace before following its reparse point, so a replacement
/// junction cannot erase the original conflict boundary.
pub fn lookup_spelling(
    path: &Path,
    graph: &crate::graph::Graph,
) -> crate::diagnostic::Result<PathBuf> {
    use std::{collections::VecDeque, path::Component};

    use crate::diagnostic::{Code, Error};
    let unsupported = || {
        Error::new(
            Code::PnportUnsupportedOperation,
            "The Windows pathname uses an unsupported reparse traversal.",
        )
    };
    let mut remaining: VecDeque<OsString> = path
        .components()
        .map(|part| part.as_os_str().to_owned())
        .collect();
    let mut result = PathBuf::new();
    let mut links = 0;
    while let Some(part) = remaining.pop_front() {
        match Path::new(&part).components().next() {
            Some(Component::Prefix(_) | Component::RootDir) => result.push(part),
            Some(Component::CurDir) => {}
            Some(Component::ParentDir) => {
                result.pop();
            }
            Some(Component::Normal(name)) => {
                let name = if name
                    .to_str()
                    .is_some_and(|name| name.eq_ignore_ascii_case("node_modules"))
                {
                    std::ffi::OsStr::new("node_modules")
                } else {
                    name
                };
                let candidate = result.join(name);
                if !candidate.is_absolute() {
                    tracing::debug!(action = "windows_lookup_invalid_prefix", path = %candidate.display(), "A native lookup lost its absolute prefix");
                    return Err(unsupported());
                }
                graph.check_path_conflicts(&candidate)?;
                match fs::symlink_metadata(&candidate) {
                    Ok(metadata) if metadata.file_type().is_symlink() => {
                        links += 1;
                        if links > 40 {
                            return Err(unsupported());
                        }
                        let target = fs::read_link(&candidate).map_err(|_| unsupported())?;
                        let target =
                            dos_path(&target.as_os_str().encode_wide().collect::<Vec<_>>());
                        let mut expanded: VecDeque<_> = target
                            .components()
                            .map(|part| part.as_os_str().to_owned())
                            .collect();
                        expanded.append(&mut remaining);
                        remaining = expanded;
                    }
                    Ok(metadata)
                        if metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0 =>
                    {
                        return Err(unsupported())
                    }
                    Ok(_) => {
                        result = fs::canonicalize(&candidate)
                            .map(|path| {
                                dos_path(&path.as_os_str().encode_wide().collect::<Vec<_>>())
                            })
                            .unwrap_or(candidate);
                    }
                    Err(_) => result = candidate,
                }
            }
            None => {}
        }
    }
    Ok(result)
}

trait WideAscii {
    fn eq_ignore_ascii_case(&self, other: &[u16]) -> bool;
}
impl WideAscii for [u16] {
    fn eq_ignore_ascii_case(&self, other: &[u16]) -> bool {
        self.len() == other.len()
            && self.iter().zip(other).all(|(a, b)| {
                let upper = |c: u16| if (97..=122).contains(&c) { c - 32 } else { c };
                upper(*a) == upper(*b)
            })
    }
}

#[allow(
    clippy::not_unsafe_ptr_arg_deref,
    reason = "Windows validates opaque kernel handles; no caller memory is dereferenced"
)]
pub fn file_path(handle: HANDLE) -> io::Result<PathBuf> {
    let mut capacity = 512;
    for _ in 0..4 {
        let mut value = vec![0; capacity];
        let length = unsafe {
            FileSystem::GetFinalPathNameByHandleW(handle, value.as_mut_ptr(), capacity as u32, 0)
        } as usize;
        if length == 0 {
            return Err(io::Error::last_os_error());
        }
        if length < capacity {
            return Ok(dos_path(&value[..length]));
        }
        capacity = length
            .checked_add(1)
            .filter(|n| *n <= 32768)
            .ok_or(io::ErrorKind::InvalidData)?;
    }
    Err(io::ErrorKind::InvalidData.into())
}

pub struct User {
    _token: Handle,
    bytes: Vec<usize>,
    owner: Vec<usize>,
}
impl User {
    pub fn current() -> io::Result<Self> {
        let mut token = ptr::null_mut();
        if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) } == 0 {
            return Err(io::Error::last_os_error());
        }
        let token = Handle(token);
        let query = |class| -> io::Result<Vec<usize>> {
            let mut size = 0;
            unsafe {
                GetTokenInformation(token.0, class, ptr::null_mut(), 0, &mut size);
            }
            if size == 0 {
                return Err(io::Error::last_os_error());
            }
            let mut bytes = vec![0usize; (size as usize).div_ceil(size_of::<usize>())];
            if unsafe {
                GetTokenInformation(token.0, class, bytes.as_mut_ptr().cast(), size, &mut size)
            } == 0
            {
                return Err(io::Error::last_os_error());
            }
            Ok(bytes)
        };
        let bytes = query(TokenUser)?;
        let owner = query(TokenOwner)?;
        Ok(Self {
            _token: token,
            bytes,
            owner,
        })
    }

    pub fn sid(&self) -> PSID {
        unsafe { (*self.bytes.as_ptr().cast::<TOKEN_USER>()).User.Sid }
    }

    fn owner(&self) -> PSID {
        unsafe { (*self.owner.as_ptr().cast::<TOKEN_OWNER>()).Owner }
    }

    fn text(&self) -> io::Result<String> {
        let mut value = ptr::null_mut();
        if unsafe { ConvertSidToStringSidW(self.sid(), &mut value) } == 0 {
            return Err(io::Error::last_os_error());
        }
        let allocation = Local(value.cast());
        let mut length = 0;
        while unsafe { *value.add(length) } != 0 {
            length += 1;
        }
        let result = String::from_utf16(unsafe { std::slice::from_raw_parts(value, length) })
            .map_err(|_| io::Error::from(io::ErrorKind::InvalidData));
        drop(allocation);
        result
    }
}

struct Local(*mut std::ffi::c_void);
impl Drop for Local {
    fn drop(&mut self) {
        unsafe {
            LocalFree(self.0);
        }
    }
}

/// Create a private leaf atomically. Existing user data is validated, never
/// adopted by rewriting its DACL. Parent directories remain caller-owned.
pub fn private_directory(path: &Path) -> io::Result<()> {
    if !path.exists() {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let user = User::current()?;
        let sddl: Vec<_> = format!("O:{}D:P(A;OICI;FA;;;{})", user.text()?, user.text()?)
            .encode_utf16()
            .chain([0])
            .collect();
        let mut descriptor = ptr::null_mut();
        if unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                sddl.as_ptr(),
                1,
                &mut descriptor,
                ptr::null_mut(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        let allocation = Local(descriptor);
        let attributes = SECURITY_ATTRIBUTES {
            nLength: size_of::<SECURITY_ATTRIBUTES>() as u32,
            lpSecurityDescriptor: descriptor,
            bInheritHandle: 0,
        };
        let result = unsafe { FileSystem::CreateDirectoryW(wide(path)?.as_ptr(), &attributes) };
        let error = io::Error::last_os_error();
        drop(allocation);
        if result == 0 && error.kind() != io::ErrorKind::AlreadyExists {
            return Err(error);
        }
    }
    let file = OpenOptions::new()
        .access_mode(FileSystem::READ_CONTROL | FileSystem::FILE_READ_ATTRIBUTES)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)?;
    validate_private(&file, true)
}

/// Validate the opened object rather than a second pathname lookup. Deny
/// reparse leaves, multiply linked state files and grants to other principals.
pub fn validate_private(file: &File, directory: bool) -> io::Result<()> {
    let denied = || io::Error::from(io::ErrorKind::PermissionDenied);
    let handle = file.as_raw_handle();
    let mut info = FileSystem::BY_HANDLE_FILE_INFORMATION::default();
    if unsafe { FileSystem::GetFileInformationByHandle(handle, &mut info) } == 0 {
        return Err(io::Error::last_os_error());
    }
    if info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT != 0
        || (!directory && info.nNumberOfLinks != 1)
        || (info.dwFileAttributes & FileSystem::FILE_ATTRIBUTE_DIRECTORY != 0) != directory
    {
        return Err(denied());
    }
    let user = User::current()?;
    let mut owner = ptr::null_mut();
    let mut dacl = ptr::null_mut();
    let mut descriptor = ptr::null_mut();
    let status = unsafe {
        GetSecurityInfo(
            handle,
            SE_FILE_OBJECT,
            OWNER_SECURITY_INFORMATION | DACL_SECURITY_INFORMATION,
            &mut owner,
            ptr::null_mut(),
            &mut dacl,
            ptr::null_mut(),
            &mut descriptor,
        )
    };
    if status != 0 {
        return Err(io::Error::from_raw_os_error(status as i32));
    }
    let _allocation = Local(descriptor);
    // Elevated tokens can default new file ownership to their owner group.
    // Accept only this token's own owner SID; grants still must name the
    // invoking user's SID exclusively, including for such group-owned files.
    if owner.is_null()
        || dacl.is_null()
        || (unsafe { EqualSid(owner, user.sid()) } == 0
            && unsafe { EqualSid(owner, user.owner()) } == 0)
    {
        return Err(denied());
    }
    let count = unsafe { (*dacl).AceCount };
    if count == 0 {
        return Err(denied());
    }
    for index in 0..u32::from(count) {
        let mut entry = ptr::null_mut();
        if unsafe { GetAce(dacl, index, &mut entry) } == 0 {
            return Err(io::Error::last_os_error());
        }
        let ace = unsafe { &*entry.cast::<ACCESS_ALLOWED_ACE>() };
        if ace.Header.AceType != 0
            || unsafe { EqualSid((&raw const ace.SidStart).cast_mut().cast(), user.sid()) } == 0
        {
            return Err(denied());
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn extended_drive_unc_and_nt_paths_share_the_dos_namespace() {
        for (input, expected) in [
            (r"\\?\C:\한글\file", r"C:\한글\file"),
            (r"\??\C:\file", r"C:\file"),
            (r"\\?\UNC\server\share\file", r"\\server\share\file"),
        ] {
            assert_eq!(
                dos_path(&input.encode_utf16().collect::<Vec<_>>()),
                PathBuf::from(expected)
            );
        }
    }
    #[test]
    fn private_files_reject_hardlink_aliases() {
        let parent = tempfile::tempdir().unwrap();
        let private = parent.path().join("private");
        private_directory(&private).unwrap();
        private_directory(&private).unwrap();
        let file = private.join("state");
        fs::write(&file, b"state").unwrap();
        validate_private(&File::open(&file).unwrap(), false).unwrap();
        let alias = private.join("alias");
        fs::hard_link(&file, &alias).unwrap();
        assert!(validate_private(&File::open(&file).unwrap(), false).is_err());
    }

    #[test]
    fn existing_shared_directory_is_rejected_without_changing_its_acl() {
        let parent = tempfile::tempdir().unwrap();
        let shared = parent.path().join("shared");
        fs::create_dir(&shared).unwrap();
        let user = User::current().unwrap();
        let sddl: Vec<_> = format!("D:P(A;OICI;FA;;;{})(A;OICI;FR;;;WD)", user.text().unwrap())
            .encode_utf16()
            .chain([0])
            .collect();
        let mut descriptor = ptr::null_mut();
        assert_ne!(
            unsafe {
                ConvertStringSecurityDescriptorToSecurityDescriptorW(
                    sddl.as_ptr(),
                    1,
                    &mut descriptor,
                    ptr::null_mut(),
                )
            },
            0
        );
        let _allocation = Local(descriptor);
        assert_ne!(
            unsafe {
                SetFileSecurityW(
                    wide(&shared).unwrap().as_ptr(),
                    DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                    descriptor,
                )
            },
            0
        );
        assert_eq!(
            private_directory(&shared).unwrap_err().kind(),
            io::ErrorKind::PermissionDenied
        );
        let file = OpenOptions::new()
            .access_mode(FileSystem::READ_CONTROL)
            .custom_flags(FILE_FLAG_BACKUP_SEMANTICS)
            .open(&shared)
            .unwrap();
        let mut acl = ptr::null_mut();
        let mut observed = ptr::null_mut();
        assert_eq!(
            unsafe {
                GetSecurityInfo(
                    file.as_raw_handle(),
                    SE_FILE_OBJECT,
                    DACL_SECURITY_INFORMATION,
                    ptr::null_mut(),
                    ptr::null_mut(),
                    &mut acl,
                    ptr::null_mut(),
                    &mut observed,
                )
            },
            0
        );
        let _observed = Local(observed);
        assert_eq!(
            unsafe { (*acl).AceCount },
            2,
            "validation rewrote caller-owned access grants"
        );
    }
}
