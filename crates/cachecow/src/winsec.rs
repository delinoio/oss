// SPDX-License-Identifier: Apache-2.0
//! Windows security helpers shared by private storage, ownership checks and
//! shell profile preservation (Go `internal/ownership` plus the
//! `golang.org/x/sys/windows` security calls the Go code used).

use anyhow::Result;

/// An owned copy of a security identifier.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Sid(pub Vec<u8>);

impl Sid {
    /// `S-1-5-...` string form.
    pub fn to_string_sid(&self) -> Result<String> {
        todo!("winsec::Sid::to_string_sid")
    }

    pub fn is_builtin_administrators(&self) -> bool {
        todo!("winsec::Sid::is_builtin_administrators")
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

/// User SID of the current process token.
pub fn current_user() -> Result<Sid> {
    todo!("winsec::current_user")
}

/// Whether `owner` identifies the current user. BUILTIN\Administrators is
/// accepted only while the current token, or its linked token, is a member.
pub fn accepted_owner(owner: &Sid) -> Result<bool> {
    let _ = owner;
    todo!("winsec::accepted_owner")
}

/// `GetNamedSecurityInfo` for a file or directory.
pub fn read_security(path: &str, parts: Parts) -> Result<SecurityInfo> {
    let _ = (path, parts);
    todo!("winsec::read_security")
}

/// Creates a directory with the security descriptor described by `sddl`.
/// An existing path yields an `AlreadyExists` I/O error.
pub fn create_directory(path: &str, sddl: &str) -> std::io::Result<()> {
    let _ = (path, sddl);
    todo!("winsec::create_directory")
}

/// Canonical SDDL string of a descriptor created from `sddl`.
pub fn normalize_sddl(sddl: &str, parts: Parts) -> Result<String> {
    let _ = (sddl, parts);
    todo!("winsec::normalize_sddl")
}

/// Applies the owner, group and DACL (with its protection flag) to `path`.
pub fn apply_security(path: &str, info: &SecurityInfo) -> Result<()> {
    let _ = (path, info);
    todo!("winsec::apply_security")
}
