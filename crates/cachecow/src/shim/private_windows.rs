// SPDX-License-Identifier: Apache-2.0
//! Private shim directory checks with a protected current-user-only ACL.

use std::{fs, io};

use anyhow::{Result, anyhow};

use crate::winsec::{self, Parts};

/// Refuses a shared or inherited ACL rather than silently changing
/// permissions on a user-selected existing directory.
pub fn private_dir(dir: &str) -> Result<()> {
    let sid = winsec::current_user()?.to_string_sid()?;
    let sddl = format!("O:{sid}D:P(A;;FA;;;{sid})");
    match winsec::create_directory(dir, &sddl) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    let st = fs::symlink_metadata(dir)?;
    if !st.is_dir() || st.file_type().is_symlink() {
        return Err(anyhow!("shim directory must be a private real directory"));
    }
    let parts = Parts {
        owner: true,
        group: false,
        dacl: true,
    };
    let actual = winsec::read_security(dir, parts)?;
    // Compare the canonical descriptor to the private descriptor we create.
    if actual.sddl != winsec::normalize_sddl(&sddl, parts)? {
        return Err(anyhow!(
            "shim directory {dir} must have a protected ACL granting only the current user \
             access; choose a new directory"
        ));
    }
    Ok(())
}
