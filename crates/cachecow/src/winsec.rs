// SPDX-License-Identifier: Apache-2.0
//! Windows security helpers shared by private storage, ownership checks and
//! shell profile preservation (Go `internal/ownership` plus the
//! `golang.org/x/sys/windows` security calls the Go code used).

use std::{
    ffi::{OsStr, c_void},
    io,
    os::windows::ffi::OsStrExt,
    ptr,
};

use anyhow::{Result, anyhow};
use windows_sys::Win32::{
    Foundation::{CloseHandle, HANDLE, HLOCAL, LocalFree},
    Security::{
        ACL,
        Authorization::{
            ConvertSecurityDescriptorToStringSecurityDescriptorW, ConvertSidToStringSidW,
            ConvertStringSecurityDescriptorToSecurityDescriptorW, GetNamedSecurityInfoW,
            SDDL_REVISION_1, SE_FILE_OBJECT, SetNamedSecurityInfoW,
        },
        CheckTokenMembership, DACL_SECURITY_INFORMATION, GROUP_SECURITY_INFORMATION, GetAce,
        GetLengthSid, GetSecurityDescriptorControl, GetSecurityDescriptorDacl,
        GetSecurityDescriptorGroup, GetSecurityDescriptorOwner, GetTokenInformation,
        IsWellKnownSid, OBJECT_SECURITY_INFORMATION, OWNER_SECURITY_INFORMATION,
        PROTECTED_DACL_SECURITY_INFORMATION, PSECURITY_DESCRIPTOR, PSID, SE_DACL_PROTECTED,
        SECURITY_ATTRIBUTES, TOKEN_LINKED_TOKEN, TOKEN_QUERY, TOKEN_USER, TokenLinkedToken,
        TokenUser, UNPROTECTED_DACL_SECURITY_INFORMATION, WinBuiltinAdministratorsSid,
    },
    Storage::FileSystem::CreateDirectoryW,
    System::Threading::{GetCurrentProcess, OpenProcessToken},
};

/// An owned copy of a security identifier.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Sid(pub Vec<u8>);

impl Sid {
    fn as_psid(&self) -> PSID {
        self.0.as_ptr().cast_mut().cast()
    }

    /// Copies a SID from a valid pointer.
    ///
    /// # Safety
    ///
    /// `psid` must point to a valid SID that stays alive during the call.
    unsafe fn copy_from(psid: PSID) -> Self {
        // SAFETY: the caller guarantees a valid SID; GetLengthSid reads its
        // header.
        let len = unsafe { GetLengthSid(psid) } as usize;
        // SAFETY: a valid SID occupies exactly `len` readable bytes.
        let bytes = unsafe { std::slice::from_raw_parts(psid.cast::<u8>(), len) };
        Self(bytes.to_vec())
    }

    /// `S-1-5-...` string form.
    pub fn to_string_sid(&self) -> Result<String> {
        let mut out: *mut u16 = ptr::null_mut();
        // SAFETY: `self` holds a complete SID; `out` receives a LocalAlloc
        // string.
        if unsafe { ConvertSidToStringSidW(self.as_psid(), &mut out) } == 0 {
            return Err(io::Error::last_os_error().into());
        }
        // SAFETY: `out` is a NUL-terminated string allocated by the call above.
        let s = unsafe { take_local_string(out) };
        Ok(s)
    }

    pub fn is_builtin_administrators(&self) -> bool {
        // SAFETY: `self` holds a complete SID.
        unsafe { IsWellKnownSid(self.as_psid(), WinBuiltinAdministratorsSid) != 0 }
    }
}

/// One access-control entry.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Ace {
    pub ace_type: u8,
    pub flags: u8,
    pub mask: u32,
    pub sid: Sid,
}

/// Owner, group and DACL of a filesystem object.
#[derive(Debug, Clone, Default)]
pub struct SecurityInfo {
    pub owner: Option<Sid>,
    pub group: Option<Sid>,
    /// `None` for a NULL DACL.
    pub dacl: Option<Vec<Ace>>,
    pub dacl_protected: bool,
    /// SDDL string of the requested parts, for exact comparison.
    pub sddl: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Parts {
    pub owner: bool,
    pub group: bool,
    pub dacl: bool,
}

impl Parts {
    const fn flags(self) -> OBJECT_SECURITY_INFORMATION {
        let mut f = 0;
        if self.owner {
            f |= OWNER_SECURITY_INFORMATION;
        }
        if self.group {
            f |= GROUP_SECURITY_INFORMATION;
        }
        if self.dacl {
            f |= DACL_SECURITY_INFORMATION;
        }
        f
    }
}

fn wide(s: &str) -> Vec<u16> {
    OsStr::new(s).encode_wide().chain(Some(0)).collect()
}

/// Reads and frees a LocalAlloc'd UTF-16 string.
///
/// # Safety
///
/// `p` must be null or a NUL-terminated string owned by the caller and
/// allocated with `LocalAlloc`.
unsafe fn take_local_string(p: *mut u16) -> String {
    if p.is_null() {
        return String::new();
    }
    let mut len = 0;
    // SAFETY: the string is NUL-terminated per the caller contract.
    while unsafe { *p.add(len) } != 0 {
        len += 1;
    }
    // SAFETY: `len` UTF-16 units precede the terminator.
    let s = String::from_utf16_lossy(unsafe { std::slice::from_raw_parts(p, len) });
    // SAFETY: `p` was allocated with LocalAlloc and is freed exactly once.
    unsafe { LocalFree(p.cast::<c_void>() as HLOCAL) };
    s
}

/// A security descriptor allocated by `LocalAlloc`, freed on drop.
struct LocalDescriptor(PSECURITY_DESCRIPTOR);

impl Drop for LocalDescriptor {
    fn drop(&mut self) {
        if !self.0.is_null() {
            // SAFETY: the descriptor was allocated by a Win32 API with
            // LocalAlloc.
            unsafe { LocalFree(self.0 as HLOCAL) };
        }
    }
}

impl LocalDescriptor {
    fn from_sddl(sddl: &str) -> Result<Self> {
        let text = wide(sddl);
        let mut sd: PSECURITY_DESCRIPTOR = ptr::null_mut();
        // SAFETY: `text` is NUL-terminated; `sd` receives a LocalAlloc
        // descriptor.
        let ok = unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                text.as_ptr(),
                SDDL_REVISION_1,
                &mut sd,
                ptr::null_mut(),
            )
        };
        if ok == 0 {
            return Err(anyhow!(
                "invalid security descriptor {sddl}: {}",
                io::Error::last_os_error()
            ));
        }
        Ok(Self(sd))
    }

    fn to_sddl(&self, flags: OBJECT_SECURITY_INFORMATION) -> Result<String> {
        let mut out: *mut u16 = ptr::null_mut();
        // SAFETY: `self.0` is a valid descriptor; `out` receives a LocalAlloc
        // string.
        let ok = unsafe {
            ConvertSecurityDescriptorToStringSecurityDescriptorW(
                self.0,
                SDDL_REVISION_1,
                flags,
                &mut out,
                ptr::null_mut(),
            )
        };
        if ok == 0 {
            return Err(io::Error::last_os_error().into());
        }
        // SAFETY: `out` was allocated by the call above.
        Ok(unsafe { take_local_string(out) })
    }

    fn dacl_protected(&self) -> Result<bool> {
        let mut control = 0u16;
        let mut revision = 0u32;
        // SAFETY: `self.0` is a valid descriptor and both outputs are valid.
        if unsafe { GetSecurityDescriptorControl(self.0, &mut control, &mut revision) } == 0 {
            return Err(io::Error::last_os_error().into());
        }
        Ok(control & SE_DACL_PROTECTED != 0)
    }
}

struct Token(HANDLE);

impl Drop for Token {
    fn drop(&mut self) {
        // SAFETY: the handle is owned by this value and closed exactly once.
        unsafe { CloseHandle(self.0) };
    }
}

fn process_token() -> Result<Token> {
    let mut token: HANDLE = ptr::null_mut();
    // SAFETY: GetCurrentProcess returns a pseudo-handle; `token` receives a
    // handle.
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) } == 0 {
        return Err(io::Error::last_os_error().into());
    }
    Ok(Token(token))
}

/// Token information in an 8-byte-aligned buffer.
fn token_information(token: HANDLE, class: i32) -> io::Result<Vec<u64>> {
    let mut needed = 0u32;
    // SAFETY: a null buffer with zero length only queries the required size.
    unsafe { GetTokenInformation(token, class, ptr::null_mut(), 0, &mut needed) };
    if needed == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut buffer = vec![0u64; (needed as usize).div_ceil(8)];
    // SAFETY: `buffer` provides at least `needed` writable, aligned bytes.
    let ok = unsafe {
        GetTokenInformation(
            token,
            class,
            buffer.as_mut_ptr().cast(),
            needed,
            &mut needed,
        )
    };
    if ok == 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(buffer)
}

/// User SID of the current process token.
pub fn current_user() -> Result<Sid> {
    let token = process_token()?;
    let buffer = token_information(token.0, TokenUser)?;
    // SAFETY: the buffer holds a TOKEN_USER whose SID points into the buffer.
    let user = unsafe { &*buffer.as_ptr().cast::<TOKEN_USER>() };
    // SAFETY: the SID pointer is valid while `buffer` is alive.
    Ok(unsafe { Sid::copy_from(user.User.Sid) })
}

fn is_member(token: HANDLE, sid: &Sid) -> Result<bool> {
    let mut member = 0;
    // SAFETY: `sid` holds a complete SID; a null token selects the effective
    // thread token, as Go's `windows.Token(0).IsMember` does.
    if unsafe { CheckTokenMembership(token, sid.as_psid(), &mut member) } == 0 {
        return Err(io::Error::last_os_error().into());
    }
    Ok(member != 0)
}

/// Whether `owner` identifies the current user. The current user SID is always
/// accepted. Windows assigns BUILTIN\Administrators as the default owner of
/// objects created by an elevated administrator, including directories created
/// by CacheCow itself, temporary directories and Git checkouts. That owner is
/// accepted only while the current token, or its linked token for a filtered
/// administrator session, is a member of Administrators. Administrators can
/// already take ownership of any object, so this does not extend trust beyond
/// the local trust boundary. Git for Windows applies the same rule.
pub fn accepted_owner(owner: &Sid) -> Result<bool> {
    if *owner == current_user()? {
        return Ok(true);
    }
    if !owner.is_builtin_administrators() {
        return Ok(false);
    }
    if is_member(ptr::null_mut(), owner)? {
        return Ok(true);
    }
    let token = process_token()?;
    // Tokens without a linked administrator token are not administrators.
    let Ok(buffer) = token_information(token.0, TokenLinkedToken) else {
        return Ok(false);
    };
    // SAFETY: the buffer holds a TOKEN_LINKED_TOKEN with an owned handle.
    let linked = Token(unsafe { (*buffer.as_ptr().cast::<TOKEN_LINKED_TOKEN>()).LinkedToken });
    is_member(linked.0, owner)
}

/// Parses the ACEs of a DACL.
///
/// # Safety
///
/// `acl` must point to a valid ACL that outlives the call.
unsafe fn read_acl(acl: *const ACL) -> Result<Vec<Ace>> {
    // SAFETY: the caller guarantees a valid ACL header.
    let count = u32::from(unsafe { (*acl).AceCount });
    let mut out = Vec::with_capacity(count as usize);
    for i in 0..count {
        let mut ace: *mut c_void = ptr::null_mut();
        // SAFETY: `i` is below the ACL's ACE count.
        if unsafe { GetAce(acl, i, &mut ace) } == 0 {
            return Err(io::Error::last_os_error().into());
        }
        let bytes = ace.cast::<u8>();
        // SAFETY: every ACE starts with a 4-byte ACE_HEADER followed by a mask.
        let (ace_type, flags, mask) = unsafe {
            (
                *bytes,
                *bytes.add(1),
                bytes.add(4).cast::<u32>().read_unaligned(),
            )
        };
        // Allowed (0) and denied (1) ACEs store the SID right after the mask.
        let sid = if ace_type <= 1 {
            // SAFETY: simple allow/deny ACEs embed a complete SID at offset 8.
            unsafe { Sid::copy_from(bytes.add(8).cast()) }
        } else {
            Sid(Vec::new())
        };
        out.push(Ace {
            ace_type,
            flags,
            mask,
            sid,
        });
    }
    Ok(out)
}

/// `GetNamedSecurityInfo` for a file or directory.
pub fn read_security(path: &str, parts: Parts) -> Result<SecurityInfo> {
    let name = wide(path);
    let mut owner: PSID = ptr::null_mut();
    let mut group: PSID = ptr::null_mut();
    let mut dacl: *mut ACL = ptr::null_mut();
    let mut sd: PSECURITY_DESCRIPTOR = ptr::null_mut();
    let flags = parts.flags();
    // SAFETY: `name` is NUL-terminated; requested outputs point into `sd`,
    // which this function frees through `LocalDescriptor`.
    let status = unsafe {
        GetNamedSecurityInfoW(
            name.as_ptr(),
            SE_FILE_OBJECT,
            flags,
            if parts.owner {
                &mut owner
            } else {
                ptr::null_mut()
            },
            if parts.group {
                &mut group
            } else {
                ptr::null_mut()
            },
            if parts.dacl {
                &mut dacl
            } else {
                ptr::null_mut()
            },
            ptr::null_mut(),
            &mut sd,
        )
    };
    if status != 0 {
        return Err(io::Error::from_raw_os_error(status as i32).into());
    }
    let descriptor = LocalDescriptor(sd);
    let mut info = SecurityInfo {
        // SAFETY: non-null SIDs point into the live descriptor.
        owner: (!owner.is_null()).then(|| unsafe { Sid::copy_from(owner) }),
        // SAFETY: as above.
        group: (!group.is_null()).then(|| unsafe { Sid::copy_from(group) }),
        dacl: None,
        dacl_protected: descriptor.dacl_protected()?,
        sddl: descriptor.to_sddl(flags)?,
    };
    if parts.dacl && !dacl.is_null() {
        // SAFETY: `dacl` points into the live descriptor.
        info.dacl = Some(unsafe { read_acl(dacl) }?);
    }
    Ok(info)
}

/// Creates a directory with the security descriptor described by `sddl`.
/// An existing path yields an `AlreadyExists` I/O error.
pub fn create_directory(path: &str, sddl: &str) -> io::Result<()> {
    let descriptor = LocalDescriptor::from_sddl(sddl).map_err(io::Error::other)?;
    let attributes = SECURITY_ATTRIBUTES {
        nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
        lpSecurityDescriptor: descriptor.0,
        bInheritHandle: 0,
    };
    let name = wide(path);
    // Apply privacy at creation, never by replacing the ACL of an existing
    // path. SAFETY: `name` is NUL-terminated and `attributes` outlives the
    // call.
    if unsafe { CreateDirectoryW(name.as_ptr(), &attributes) } == 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

/// Canonical SDDL string of a descriptor created from `sddl`.
pub fn normalize_sddl(sddl: &str, parts: Parts) -> Result<String> {
    LocalDescriptor::from_sddl(sddl)?.to_sddl(parts.flags())
}

/// Applies the owner, group and DACL (with its protection flag) recorded in
/// `info.sddl` to `path`. A failure leaves the caller's original file intact.
pub fn apply_security(path: &str, info: &SecurityInfo) -> Result<()> {
    let descriptor = LocalDescriptor::from_sddl(&info.sddl)?;
    let mut owner: PSID = ptr::null_mut();
    let mut group: PSID = ptr::null_mut();
    let mut dacl: *mut ACL = ptr::null_mut();
    let mut defaulted = 0;
    let mut present = 0;
    // SAFETY: `descriptor` is valid; the outputs point into it while it lives.
    unsafe {
        if GetSecurityDescriptorOwner(descriptor.0, &mut owner, &mut defaulted) == 0
            || GetSecurityDescriptorGroup(descriptor.0, &mut group, &mut defaulted) == 0
            || GetSecurityDescriptorDacl(descriptor.0, &mut present, &mut dacl, &mut defaulted) == 0
        {
            return Err(io::Error::last_os_error().into());
        }
    }
    let mut flags = 0;
    if !owner.is_null() {
        flags |= OWNER_SECURITY_INFORMATION;
    }
    if !group.is_null() {
        flags |= GROUP_SECURITY_INFORMATION;
    }
    if present != 0 {
        flags |= DACL_SECURITY_INFORMATION;
        flags |= if descriptor.dacl_protected()? {
            PROTECTED_DACL_SECURITY_INFORMATION
        } else {
            UNPROTECTED_DACL_SECURITY_INFORMATION
        };
    }
    let name = wide(path);
    // SAFETY: `name` is NUL-terminated; SID and ACL pointers are null or point
    // into the live descriptor.
    let status = unsafe {
        SetNamedSecurityInfoW(
            name.as_ptr(),
            SE_FILE_OBJECT,
            flags,
            owner,
            group,
            dacl,
            ptr::null(),
        )
    };
    if status != 0 {
        return Err(io::Error::from_raw_os_error(status as i32).into());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use windows_sys::Win32::Security::{
        CreateWellKnownSid, SECURITY_MAX_SID_SIZE, WinAuthenticatedUserSid, WinBuiltinUsersSid,
        WinLocalSystemSid, WinWorldSid,
    };

    use super::*;

    fn well_known(kind: i32) -> Sid {
        let mut buffer = vec![0u8; SECURITY_MAX_SID_SIZE as usize];
        let mut size = SECURITY_MAX_SID_SIZE;
        // SAFETY: `buffer` holds `size` writable bytes.
        let ok = unsafe {
            CreateWellKnownSid(kind, ptr::null_mut(), buffer.as_mut_ptr().cast(), &mut size)
        };
        assert_ne!(ok, 0, "{}", io::Error::last_os_error());
        buffer.truncate(size as usize);
        Sid(buffer)
    }

    #[test]
    fn created_objects_are_accepted() {
        let dir = tempfile::tempdir().unwrap();
        let dir = dir.path().to_str().unwrap().to_string();
        let file = format!("{dir}\\file");
        std::fs::write(&file, b"").unwrap();
        for path in [&dir, &file] {
            let info = read_security(
                path,
                Parts {
                    owner: true,
                    group: false,
                    dacl: false,
                },
            )
            .unwrap();
            assert!(accepted_owner(&info.owner.unwrap()).unwrap(), "{path}");
        }
        assert!(accepted_owner(&current_user().unwrap()).unwrap());
    }

    #[test]
    fn other_principals_are_rejected() {
        for kind in [
            WinWorldSid,
            WinBuiltinUsersSid,
            WinAuthenticatedUserSid,
            WinLocalSystemSid,
        ] {
            assert!(!accepted_owner(&well_known(kind)).unwrap(), "{kind}");
        }
    }

    #[test]
    fn private_directory_round_trip() {
        let dir = tempfile::tempdir().unwrap();
        let path = format!("{}\\private", dir.path().to_str().unwrap());
        let user = current_user().unwrap().to_string_sid().unwrap();
        let sddl = format!("D:P(A;OICI;FA;;;{user})");
        create_directory(&path, &sddl).unwrap();
        assert_eq!(
            create_directory(&path, &sddl).unwrap_err().kind(),
            io::ErrorKind::AlreadyExists
        );
        let info = read_security(
            &path,
            Parts {
                owner: false,
                group: false,
                dacl: true,
            },
        )
        .unwrap();
        assert!(info.dacl_protected);
        assert_eq!(info.dacl.unwrap().len(), 1);
        assert_eq!(
            info.sddl,
            normalize_sddl(
                &sddl,
                Parts {
                    owner: false,
                    group: false,
                    dacl: true
                }
            )
            .unwrap()
        );
    }
}
