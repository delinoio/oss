// SPDX-License-Identifier: Apache-2.0
//! Current-user ownership checks (Go `owner_unix.go`, `owner_windows.go`).

use anyhow::{Result, bail};

use super::{io_not_found, path_error};
use crate::paths;

const NOT_OWNED: &str = "path is not owned by the current user; select current-user storage";

/// Checks that the existing object (or nearest existing parent) is owned by
/// the current user.
#[cfg(unix)]
pub fn check_owner(path: &str) -> Result<()> {
    use std::os::unix::fs::MetadataExt;
    let mut path = path.to_string();
    loop {
        match std::fs::metadata(&path) {
            Ok(m) => {
                // SAFETY: geteuid has no preconditions and cannot fail.
                let euid = unsafe { libc::geteuid() };
                if m.uid() != euid {
                    bail!(NOT_OWNED);
                }
                return Ok(());
            }
            Err(e) if io_not_found(&e) => {
                let parent = paths::dir(&path);
                if parent == path {
                    return Err(path_error("stat", &path, e));
                }
                path = parent;
            }
            Err(e) => return Err(path_error("stat", &path, e)),
        }
    }
}

/// Checks that the existing object (or nearest existing parent) is owned by
/// the current user, or by Administrators while the token is a member.
#[cfg(windows)]
pub fn check_owner(path: &str) -> Result<()> {
    let mut path = path.to_string();
    loop {
        match std::fs::metadata(&path) {
            Ok(_) => break,
            Err(e) if io_not_found(&e) => {
                let parent = paths::dir(&path);
                if parent == path {
                    return Err(path_error("stat", &path, e));
                }
                path = parent;
            }
            Err(e) => return Err(path_error("stat", &path, e)),
        }
    }
    let info = crate::winsec::read_security(
        &path,
        crate::winsec::Parts {
            owner: true,
            group: false,
            dacl: false,
        },
    )?;
    let Some(owner) = info.owner else {
        bail!(NOT_OWNED);
    };
    if !crate::winsec::accepted_owner(&owner)? {
        bail!(NOT_OWNED);
    }
    Ok(())
}

#[cfg(not(any(unix, windows)))]
pub fn check_owner(_path: &str) -> Result<()> {
    bail!("current-user ownership verification is unavailable on this platform")
}
