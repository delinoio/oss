// SPDX-License-Identifier: Apache-2.0
//! Windows ownership and ACL checks for lease and state storage. Creation
//! applies the private ACL atomically; existing state is validated without
//! taking ownership or repairing permissions.

use std::{
    fs,
    io::{self, Read},
    os::windows::fs::OpenOptionsExt,
};

use anyhow::{Result, anyhow};

use super::LeaseError;
use crate::winsec::{self, Ace, Parts};

const ACCESS_ALLOWED_ACE_TYPE: u8 = 0;
/// `FILE_FLAG_OPEN_REPARSE_POINT`: open a link itself instead of its target.
const FILE_FLAG_OPEN_REPARSE_POINT: u32 = 0x0020_0000;
const FILE_ALL_ACCESS: u32 = 0x001f_01ff;
/// OBJECT_INHERIT_ACE | CONTAINER_INHERIT_ACE, from `OICI` in the SDDL.
const INHERIT_FLAGS: u8 = 0x03;

/// Protected current-user-only descriptor shared by every private state
/// directory, so creation and `private_dir` validation cannot drift apart.
pub(super) fn state_sddl() -> Result<String> {
    let user = winsec::current_user()?.to_string_sid()?;
    Ok(format!("D:P(A;OICI;FA;;;{user})"))
}

fn owner_accepted(path: &str) -> Result<bool> {
    let info = winsec::read_security(
        path,
        Parts {
            owner: true,
            group: false,
            dacl: false,
        },
    )?;
    let owner = info
        .owner
        .ok_or_else(|| anyhow!("state path {path} has no owner"))?;
    winsec::accepted_owner(&owner)
}

/// The registry parent must exist.
pub(super) fn private_dir(path: &str) -> Result<()> {
    match winsec::create_directory(path, &state_sddl()?) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    let info = fs::symlink_metadata(path)?;
    if !info.is_dir() || info.file_type().is_symlink() {
        return Err(anyhow!("unsafe lease registry directory"));
    }
    if !owner_accepted(path)? {
        return Err(LeaseError::Unverified.into());
    }
    if !state_directory_private(path)? {
        return Err(LeaseError::Unverified.into());
    }
    Ok(())
}

pub(super) fn private_database(path: &str) -> Result<()> {
    // New SQLite files inherit the protected current-user-only directory ACL.
    match fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create_new(true)
        .open(path)
    {
        Ok(_) => return Ok(()),
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    check_private_file(path)
}

/// Opens an existing record without creating it. The handle is opened with
/// `OPEN_EXISTING` semantics and without following reparse points, so a
/// missing record keeps its `NotFound` I/O cause.
fn open_existing(path: &str) -> Result<fs::File> {
    let file = fs::OpenOptions::new()
        .read(true)
        .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)?;
    let info = file.metadata()?;
    if !info.is_file() || info.file_type().is_symlink() {
        return Err(LeaseError::Unverified.into());
    }
    Ok(file)
}

pub(super) fn validate_private_record(path: &str) -> Result<()> {
    open_existing(path)?;
    check_private_file(path)
}

pub(super) fn read_private_record(path: &str) -> Result<Vec<u8>> {
    let mut file = open_existing(path)?;
    // Residual race: the owner and ACL checks address the path because
    // `winsec::read_security` is path based. Remove this note once it reads
    // security descriptors from handles.
    check_private_file(path)?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes)?;
    Ok(bytes)
}

/// Checks the owner and the single current-user ACE of an existing file.
fn check_private_file(path: &str) -> Result<()> {
    let info = fs::symlink_metadata(path)?;
    if !info.is_file() || info.file_type().is_symlink() {
        return Err(LeaseError::Unverified.into());
    }
    let security = winsec::read_security(
        path,
        Parts {
            owner: true,
            group: false,
            dacl: true,
        },
    )?;
    let owner = security.owner.ok_or(LeaseError::Unverified)?;
    if !winsec::accepted_owner(&owner)? {
        return Err(LeaseError::Unverified.into());
    }
    let user = winsec::current_user()?;
    let Some(dacl) = security.dacl else {
        return Err(LeaseError::Unverified.into());
    };
    if dacl.len() != 1 {
        return Err(LeaseError::Unverified.into());
    }
    let ace = &dacl[0];
    if ace.ace_type != ACCESS_ALLOWED_ACE_TYPE || ace.mask != FILE_ALL_ACCESS || ace.sid != user {
        return Err(LeaseError::Unverified.into());
    }
    Ok(())
}

fn state_directory_private(path: &str) -> Result<bool> {
    let security = winsec::read_security(
        path,
        Parts {
            owner: false,
            group: false,
            dacl: true,
        },
    )?;
    if !security.dacl_protected {
        return Ok(false);
    }
    let Some(dacl) = security.dacl else {
        return Ok(false);
    };
    if dacl.len() != 1 {
        return Ok(false);
    }
    let want = Ace {
        ace_type: ACCESS_ALLOWED_ACE_TYPE,
        flags: INHERIT_FLAGS,
        mask: FILE_ALL_ACCESS,
        sid: winsec::current_user()?,
    };
    Ok(dacl[0] == want)
}

pub(super) fn path_contains(parent: &str, child: &str) -> bool {
    match crate::paths::rel(&parent.to_lowercase(), &child.to_lowercase()) {
        Some(r) => r != ".." && !r.starts_with("..\\"),
        None => false,
    }
}
