// SPDX-License-Identifier: Apache-2.0
//! POSIX ownership and permission checks for lease and state storage.

use std::{
    fs,
    io::{self, Read},
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

fn not_private(path: &str) -> anyhow::Error {
    anyhow!("lease state is not a private owned {path}; select a current-user-only registry")
}

/// Opens an existing private record without creating it and checks the open
/// descriptor, so validation and reading observe the same file. A missing
/// record keeps its `NotFound` I/O cause.
fn open_private_record(path: &str) -> Result<fs::File> {
    // O_NONBLOCK keeps a FIFO from blocking the open; it is rejected below.
    let file = match fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC | libc::O_NONBLOCK)
        .open(path)
    {
        Ok(f) => f,
        // O_NOFOLLOW reports a symbolic link as ELOOP.
        Err(e) if e.raw_os_error() == Some(libc::ELOOP) => return Err(not_private(path)),
        Err(e) => return Err(e.into()),
    };
    let info = file.metadata()?;
    // SAFETY: geteuid has no preconditions and cannot fail.
    let euid = unsafe { libc::geteuid() };
    // `nlink == 0` is an inode that a concurrent rename just replaced; it is
    // still the private record this descriptor reads. Only extra hard links
    // alias the record.
    let unsafe_entry = info.uid() != euid
        || info.permissions().mode() & 0o077 != 0
        || !info.file_type().is_file()
        || info.nlink() > 1;
    if unsafe_entry {
        return Err(not_private(path));
    }
    Ok(file)
}

pub(super) fn validate_private_record(path: &str) -> Result<()> {
    open_private_record(path).map(drop)
}

pub(super) fn read_private_record(path: &str) -> Result<Vec<u8>> {
    let mut file = open_private_record(path)?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes)?;
    Ok(bytes)
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
        return Err(not_private(path));
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
