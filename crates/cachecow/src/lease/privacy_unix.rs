// SPDX-License-Identifier: Apache-2.0
//! POSIX ownership and permission checks for lease and state storage.

use std::{
    fs, io,
    os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt, PermissionsExt},
};

use anyhow::{Result, anyhow};

pub(super) fn private_dir(path: &str) -> Result<()> {
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(path)?;
    private_path(path, true)
}

pub(super) fn private_database(path: &str) -> Result<()> {
    match fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
    {
        Ok(f) => drop(f),
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    private_path(path, false)
}

fn private_path(path: &str, directory: bool) -> Result<()> {
    let info = fs::symlink_metadata(path)?;
    // SAFETY: geteuid has no preconditions and cannot fail.
    let euid = unsafe { libc::geteuid() };
    let ft = info.file_type();
    let unsafe_entry = info.uid() != euid
        || info.permissions().mode() & 0o077 != 0
        || ft.is_symlink()
        || ft.is_dir() != directory
        || (!directory && (!ft.is_file() || info.nlink() != 1));
    if unsafe_entry {
        return Err(anyhow!(
            "lease state is not a private owned {path}; select a current-user-only registry"
        ));
    }
    Ok(())
}

pub(super) fn path_contains(parent: &str, child: &str) -> bool {
    // APFS may be case insensitive. Conservative case folding also covers
    // absent cache leaves without creating probes in a cache before it is
    // protected.
    let (parent, child) = if cfg!(target_os = "macos") {
        (parent.to_lowercase(), child.to_lowercase())
    } else {
        (parent.to_string(), child.to_string())
    };
    match crate::paths::rel(&parent, &child) {
        Some(r) => r != ".." && !r.starts_with("../"),
        None => false,
    }
}
