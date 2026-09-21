use std::{
    fs::{File, OpenOptions},
    path::{Path, PathBuf},
};

use anyhow::{ensure, Context, Result};

use crate::files;

/// Empty coordination files are persistent kernel-lock identities, never cache
/// entries. Do not unlink them during task cleanup, cache cleaning, or Drop.
pub(crate) fn open(root: &Path, name: &str) -> Result<File> {
    let root = root.canonicalize()?;
    let directory = directory()?;
    ensure!(
        !directory.starts_with(&root),
        "coordination storage must be outside the workspace"
    );
    let mut builder = std::fs::DirBuilder::new();
    builder.recursive(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        builder.mode(0o700);
    }
    builder
        .create(&directory)
        .context("create task coordination directory")?;
    ensure!(
        std::fs::symlink_metadata(&directory)?.is_dir(),
        "coordination directory must not be a link"
    );
    let directory = directory.canonicalize()?;
    ensure!(
        !directory.starts_with(&root),
        "coordination storage must be outside the workspace"
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        let metadata = std::fs::metadata(&directory)?;
        ensure!(
            metadata.uid() == nix::unistd::Uid::effective().as_raw()
                && metadata.permissions().mode() & 0o077 == 0,
            "coordination directory must be private to the current account"
        );
    }
    let identity = files::digest(&serde_json::to_vec(&(files::slash(&root)?, name))?);
    let mut options = OpenOptions::new();
    options.create(true).truncate(false).read(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options
            .mode(0o600)
            .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        options.custom_flags(windows_sys::Win32::Storage::FileSystem::FILE_FLAG_OPEN_REPARSE_POINT);
    }
    let file = options.open(directory.join(identity))?;
    let metadata = file.metadata()?;
    ensure!(
        metadata.is_file(),
        "coordination lock must be a regular file"
    );
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        ensure!(
            metadata.file_attributes()
                & windows_sys::Win32::Storage::FileSystem::FILE_ATTRIBUTE_REPARSE_POINT
                == 0,
            "coordination lock must not be a reparse point"
        );
    }
    tracing::trace!(lock = name, "Opened persistent workspace coordination lock");
    Ok(file)
}

#[cfg(unix)]
fn directory() -> Result<PathBuf> {
    // Consult the OS account database, not HOME/TMPDIR/XDG overrides. Two
    // invocations of one checkout must share identity even with different task
    // environments. Unmapped container UIDs use a fixed private system path.
    let uid = nix::unistd::Uid::effective();
    let Some(user) = nix::unistd::User::from_uid(uid)? else {
        return Ok(PathBuf::from(format!(
            "/tmp/taskflow-coordination-v1-{}",
            uid.as_raw()
        )));
    };
    ensure!(user.dir.is_absolute(), "account home must be absolute");
    Ok(user.dir.join(if cfg!(target_os = "macos") {
        "Library/Application Support/taskflow/coordination-v1"
    } else {
        ".local/state/taskflow/coordination-v1"
    }))
}

#[cfg(windows)]
fn directory() -> Result<PathBuf> {
    use std::os::windows::ffi::OsStringExt;

    use windows_sys::Win32::{
        System::Com::CoTaskMemFree,
        UI::Shell::{FOLDERID_LocalAppData, SHGetKnownFolderPath},
    };
    let mut value = std::ptr::null_mut();
    let result = unsafe {
        SHGetKnownFolderPath(&FOLDERID_LocalAppData, 0, std::ptr::null_mut(), &mut value)
    };
    if result < 0 {
        if !value.is_null() {
            unsafe { CoTaskMemFree(value.cast()) };
        }
        anyhow::bail!("resolve account coordination directory failed: {result}");
    }
    ensure!(
        !value.is_null(),
        "account coordination directory is missing"
    );
    let mut length = 0;
    unsafe {
        while *value.add(length) != 0 {
            length += 1;
        }
    }
    let path = PathBuf::from(std::ffi::OsString::from_wide(unsafe {
        std::slice::from_raw_parts(value, length)
    }));
    unsafe { CoTaskMemFree(value.cast()) };
    Ok(path.join("taskflow/coordination-v1"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn coordination_child_probe() {
        let Ok(root) = std::env::var("TFLOW_COORDINATION_PROBE") else {
            return;
        };
        let name = std::env::var("TFLOW_COORDINATION_NAME").unwrap();
        let file = open(Path::new(&root), &name).unwrap();
        let expected = std::env::var("TFLOW_COORDINATION_BLOCKED").unwrap() == "yes";
        match file.try_lock() {
            Err(std::fs::TryLockError::WouldBlock) => assert!(expected),
            Ok(()) => assert!(!expected),
            Err(error) => panic!("{error}"),
        }
    }

    #[test]
    fn coordination_locks_survive_workspace_state_deletion() {
        let root = tempfile::tempdir().unwrap();
        for name in ["task:app#build", "resource:shared", "cache"] {
            std::fs::create_dir_all(root.path().join(".taskflow/locks")).unwrap();
            let held = open(root.path(), name).unwrap();
            held.lock().unwrap();
            std::fs::remove_dir_all(root.path().join(".taskflow")).unwrap();
            std::fs::create_dir_all(root.path().join(".taskflow/locks")).unwrap();
            let probe = |blocked: bool| {
                let status = std::process::Command::new(std::env::current_exe().unwrap())
                    .args([
                        "--exact",
                        "coordination::tests::coordination_child_probe",
                        "--nocapture",
                    ])
                    .env("TFLOW_COORDINATION_PROBE", root.path())
                    .env("TFLOW_COORDINATION_NAME", name)
                    .env(
                        "TFLOW_COORDINATION_BLOCKED",
                        if blocked { "yes" } else { "no" },
                    )
                    .env("HOME", root.path())
                    .env("TMPDIR", root.path())
                    .env("LOCALAPPDATA", root.path())
                    .status()
                    .unwrap();
                assert!(status.success(), "{name}: blocked={blocked}");
            };
            probe(true);
            drop(held);
            probe(false);
        }
    }
}
