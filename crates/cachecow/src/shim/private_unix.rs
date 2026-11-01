// SPDX-License-Identifier: Apache-2.0
//! Private shim directory checks for POSIX permission bits.

use std::{
    fs,
    os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt},
};

use anyhow::{Result, anyhow};

pub fn private_dir(dir: &str) -> Result<()> {
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(dir)?;
    let st = fs::symlink_metadata(dir)?;
    // SAFETY: getuid has no preconditions and cannot fail.
    let uid = unsafe { libc::getuid() };
    if !st.is_dir()
        || st.file_type().is_symlink()
        || st.permissions().mode() & 0o077 != 0
        || st.uid() != uid
    {
        return Err(anyhow!(
            "shim directory {dir} must be owned by the current user with mode 0700; select or \
             create a private directory"
        ));
    }
    Ok(())
}
