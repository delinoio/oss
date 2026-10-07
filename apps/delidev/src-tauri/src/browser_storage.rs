//! CEF key selection, isolated cookie paths and shared local deletion
//! ownership.
use std::{
    fs::{self, File, OpenOptions},
    path::{Path, PathBuf},
};

use crate::{NativeFailure, Result, browser};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum BrowserStorageMode {
    System,
    DevelopmentMock,
}

impl BrowserStorageMode {
    pub const fn current() -> Self {
        Self::for_build(cfg!(target_os = "macos"), cfg!(debug_assertions))
    }

    const fn for_build(macos: bool, debug: bool) -> Self {
        if macos && debug {
            Self::DevelopmentMock
        } else {
            Self::System
        }
    }

    pub fn cache_root(self, root: &Path) -> PathBuf {
        match self {
            Self::System => root.to_path_buf(),
            Self::DevelopmentMock => root.join("development"),
        }
    }

    #[cfg(feature = "desktop-host")]
    pub fn cef_secret_storage(self) -> tauri_runtime_cef::SecretStorage {
        // The embedded development entry enables custom-protocol, so Tauri's
        // Auto policy treats it as production. Use our compiled build policy.
        match self {
            Self::System => tauri_runtime_cef::SecretStorage::System,
            Self::DevelopmentMock => tauri_runtime_cef::SecretStorage::Mock,
        }
    }
}

pub struct BrowserStorage {
    root: PathBuf,
    mode: BrowserStorageMode,
    // Never unlink this file: replacing its inode would admit another host.
    // The host retains this lease through CEF return and durable local cleanup.
    lease: File,
}

impl BrowserStorage {
    pub fn open(root: PathBuf, mode: BrowserStorageMode) -> Result<Self> {
        browser::private_dir(&root)?;
        let mut options = OpenOptions::new();
        options.read(true).write(true).create(true).truncate(false);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.custom_flags(0x00200000); // FILE_FLAG_OPEN_REPARSE_POINT
        }
        let file = options
            .open(root.join("browser-host.lock"))
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        let metadata = file
            .metadata()
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        if !metadata.is_file() {
            return Err(NativeFailure::InvalidEvidence);
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            if metadata.nlink() != 1 {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::MetadataExt;
            if metadata.file_attributes() & 0x400 != 0 {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        file.try_lock().map_err(|error| match error {
            fs::TryLockError::WouldBlock => NativeFailure::Busy,
            fs::TryLockError::Error(_) => NativeFailure::StorageUnavailable,
        })?;
        let storage = Self {
            root,
            mode,
            lease: file,
        };
        browser::private_dir(&storage.cache_root())?;
        browser::private_dir(&storage.cache_root().join("profiles"))?;
        Ok(storage)
    }

    pub fn cache_root(&self) -> PathBuf {
        self.mode.cache_root(&self.root)
    }

    pub fn prepare_profile(&self, record: &browser::ProfileRecord) -> Result<PathBuf> {
        browser::profile_path(&self.cache_root().join("profiles"), record)
    }

    pub fn remove_profile(&self, record: &browser::ProfileRecord) -> Result<()> {
        record.validate()?;
        self.remove_scoped(&[
            &record.data.server_id,
            &record.data.device_id,
            &record.data.account_id,
        ])
    }

    pub fn remove_device(&self, server: &str, device: &str) -> Result<()> {
        crate::canonical_id(server)?;
        crate::canonical_id(device)?;
        self.remove_scoped(&[server, device])
    }

    fn remove_scoped(&self, ids: &[&str]) -> Result<()> {
        // Delete the development copy first. A failed purge retains the shared
        // metadata in the original System directory and its durable journal.
        for mode in [
            BrowserStorageMode::DevelopmentMock,
            BrowserStorageMode::System,
        ] {
            let cache = mode.cache_root(&self.root);
            let mut path = cache.clone();
            let mut absent = false;
            for segment in std::iter::once("profiles").chain(ids.iter().copied()) {
                // Inspect, never create, ancestors before walking into them.
                match fs::symlink_metadata(&path) {
                    Ok(_) => browser::private_dir(&path)?,
                    Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                        sync_parent(&path)?;
                        absent = true;
                        break;
                    }
                    Err(_) => return Err(NativeFailure::StorageUnavailable),
                }
                path.push(segment);
            }
            if absent {
                continue;
            }
            match fs::symlink_metadata(&path) {
                Ok(_) => browser::private_dir(&path)?,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                    sync_parent(&path)?;
                    continue;
                }
                Err(_) => return Err(NativeFailure::StorageUnavailable),
            }
            fs::remove_dir_all(&path).map_err(|_| NativeFailure::StorageUnavailable)?;
            sync_parent(&path)?;
        }
        Ok(())
    }
}

impl Drop for BrowserStorage {
    fn drop(&mut self) {
        // A concurrent fork can briefly inherit the open file description
        // before exec closes it. Explicitly release the original lease once
        // its host lifetime ends, rather than waiting for that child to exec.
        if self.lease.unlock().is_err() {
            tracing::warn!(operation = "browser_storage", code = "lease-release-failed");
        }
    }
}

fn sync_parent(path: &Path) -> Result<()> {
    // An earlier attempt may have removed a directory before fsync failed.
    // Retry must synchronize its surviving parent before acknowledging absence.
    #[cfg(unix)]
    File::open(path.parent().ok_or(NativeFailure::InvalidEvidence)?)
        .and_then(|parent| parent.sync_all())
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    #[cfg(not(unix))]
    let _ = path;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::browser::{Profile, ProfileRecord, ProfileState};

    fn record() -> ProfileRecord {
        ProfileRecord {
            id: uuid::Uuid::now_v7().to_string(),
            revision: 1,
            data: Profile {
                server_id: uuid::Uuid::now_v7().to_string(),
                device_id: uuid::Uuid::now_v7().to_string(),
                account_id: uuid::Uuid::now_v7().to_string(),
                state: ProfileState::Active,
                deletion_request_id: String::new(),
            },
        }
    }

    #[test]
    fn only_macos_debug_builds_use_the_development_key() {
        for (macos, debug, expected) in [
            (true, true, BrowserStorageMode::DevelopmentMock),
            (true, false, BrowserStorageMode::System),
            (false, true, BrowserStorageMode::System),
            (false, false, BrowserStorageMode::System),
        ] {
            assert_eq!(BrowserStorageMode::for_build(macos, debug), expected);
        }
        assert_eq!(
            BrowserStorageMode::current(),
            BrowserStorageMode::for_build(cfg!(target_os = "macos"), cfg!(debug_assertions))
        );
    }

    #[cfg(feature = "desktop-host")]
    #[test]
    fn embedded_build_selects_explicit_cef_storage() {
        assert_eq!(
            BrowserStorageMode::System.cef_secret_storage(),
            tauri_runtime_cef::SecretStorage::System
        );
        assert_eq!(
            BrowserStorageMode::DevelopmentMock.cef_secret_storage(),
            tauri_runtime_cef::SecretStorage::Mock
        );
    }

    #[test]
    fn rebuilding_and_switching_modes_preserves_each_cookie_copy() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("browser-data");
        let record = record();
        let system = BrowserStorage::open(root.clone(), BrowserStorageMode::System).unwrap();
        let original = system.prepare_profile(&record).unwrap();
        fs::write(original.join("Cookies"), b"system fixture").unwrap();
        drop(system);
        let dev = BrowserStorage::open(root.clone(), BrowserStorageMode::DevelopmentMock).unwrap();
        let development = dev.prepare_profile(&record).unwrap();
        assert!(development.starts_with(root.join("development")));
        assert_ne!(development, original);
        assert!(!development.join("Cookies").exists());
        fs::write(development.join("Cookies"), b"development fixture").unwrap();
        drop(dev);
        let dev = BrowserStorage::open(root.clone(), BrowserStorageMode::DevelopmentMock).unwrap();
        assert_eq!(dev.prepare_profile(&record).unwrap(), development);
        assert_eq!(
            fs::read(development.join("Cookies")).unwrap(),
            b"development fixture"
        );
        drop(dev);
        let system = BrowserStorage::open(root, BrowserStorageMode::System).unwrap();
        assert_eq!(system.prepare_profile(&record).unwrap(), original);
        assert_eq!(
            fs::read(original.join("Cookies")).unwrap(),
            b"system fixture"
        );
    }

    #[test]
    fn modes_share_one_lease_until_storage_is_dropped() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("browser-data");
        let first = BrowserStorage::open(root.clone(), BrowserStorageMode::System).unwrap();
        assert!(matches!(
            BrowserStorage::open(root.clone(), BrowserStorageMode::DevelopmentMock),
            Err(NativeFailure::Busy)
        ));
        first.remove_profile(&record()).unwrap();
        assert!(matches!(
            BrowserStorage::open(root.clone(), BrowserStorageMode::System),
            Err(NativeFailure::Busy)
        ));
        drop(first);
        BrowserStorage::open(root, BrowserStorageMode::DevelopmentMock).unwrap();
    }

    #[test]
    fn lease_child() {
        let Some(root) = std::env::var_os("DELIDEV_BROWSER_STORAGE_TEST_ROOT") else {
            return;
        };
        let result = BrowserStorage::open(root.into(), BrowserStorageMode::DevelopmentMock);
        if std::env::var_os("DELIDEV_BROWSER_STORAGE_TEST_BUSY").is_some() {
            assert!(matches!(result, Err(NativeFailure::Busy)));
        } else {
            assert!(result.is_ok());
        }
    }

    #[test]
    fn a_separate_host_cannot_enter_until_the_original_lease_closes() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("browser-data");
        let storage = BrowserStorage::open(root.clone(), BrowserStorageMode::System).unwrap();
        let child = |busy: bool| {
            let mut command = std::process::Command::new(std::env::current_exe().unwrap());
            command
                .args([
                    "--exact",
                    "browser_storage::tests::lease_child",
                    "--nocapture",
                ])
                .env("DELIDEV_BROWSER_STORAGE_TEST_ROOT", &root)
                .env_remove("DELIDEV_BROWSER_STORAGE_TEST_BUSY");
            if busy {
                command.env("DELIDEV_BROWSER_STORAGE_TEST_BUSY", "1");
            }
            let output = command.output().unwrap();
            assert!(
                output.status.success(),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
        };
        child(true);
        drop(storage);
        child(false);
    }

    #[cfg(unix)]
    #[test]
    fn ending_the_lease_does_not_wait_for_an_unrelated_fork_to_exec() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("browser-data");
        let storage = BrowserStorage::open(root.clone(), BrowserStorageMode::System).unwrap();
        let mut pipe = [0; 2];
        assert_eq!(unsafe { libc::pipe(pipe.as_mut_ptr()) }, 0);
        let pid = unsafe { libc::fork() };
        assert!(pid >= 0);
        if pid == 0 {
            // A multithreaded test child must use only async-signal-safe calls
            // before exec/_exit. Retain the inherited lock description until
            // the parent has independently tried its replacement lease.
            unsafe {
                libc::close(pipe[1]);
                let mut byte = 0u8;
                libc::read(pipe[0], (&mut byte as *mut u8).cast(), 1);
                libc::close(pipe[0]);
                libc::_exit(0);
            }
        }
        unsafe {
            libc::close(pipe[0]);
        }
        drop(storage);
        let replacement = BrowserStorage::open(root, BrowserStorageMode::DevelopmentMock);
        let mut status = 0;
        let waited = unsafe {
            let byte = 1u8;
            libc::write(pipe[1], (&byte as *const u8).cast(), 1);
            libc::close(pipe[1]);
            libc::waitpid(pid, &mut status, 0)
        };
        assert_eq!(waited, pid);
        assert_eq!(status, 0);
        assert!(replacement.is_ok());
    }

    #[test]
    fn account_and_device_purges_remove_both_modes_and_preserve_other_owners() {
        for device in [false, true] {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().join("browser-data");
            let record = record();
            let mut other = record.clone();
            other.data.device_id = uuid::Uuid::now_v7().to_string();
            let mut paths = Vec::new();
            let mut preserved = Vec::new();
            for mode in [
                BrowserStorageMode::System,
                BrowserStorageMode::DevelopmentMock,
            ] {
                let storage = BrowserStorage::open(root.clone(), mode).unwrap();
                paths.push(storage.prepare_profile(&record).unwrap());
                preserved.push(storage.prepare_profile(&other).unwrap());
            }
            let storage = BrowserStorage::open(root, BrowserStorageMode::System).unwrap();
            for _ in 0..2 {
                if device {
                    storage
                        .remove_device(&record.data.server_id, &record.data.device_id)
                        .unwrap();
                } else {
                    storage.remove_profile(&record).unwrap();
                }
                assert!(paths.iter().all(|path| !path.exists()));
                assert!(preserved.iter().all(|path| path.exists()));
            }
        }
    }

    #[cfg(unix)]
    #[test]
    fn existing_shared_development_profile_can_be_removed() {
        use std::os::unix::fs::PermissionsExt;
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("browser-data");
        let record = record();
        let dev = BrowserStorage::open(root.clone(), BrowserStorageMode::DevelopmentMock).unwrap();
        let development = dev.prepare_profile(&record).unwrap();
        drop(dev);
        let storage = BrowserStorage::open(root, BrowserStorageMode::System).unwrap();
        let system = storage.prepare_profile(&record).unwrap();
        fs::write(system.join("Cookies"), b"system fixture").unwrap();
        fs::set_permissions(&development, fs::Permissions::from_mode(0o755)).unwrap();
        storage.remove_profile(&record).unwrap();
        assert!(!development.exists());
        assert!(!system.exists());
    }

    #[cfg(unix)]
    #[test]
    fn shared_lock_permissions_allow_admission_but_links_do_not() {
        use std::os::unix::fs::{PermissionsExt, symlink};
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("browser-data");
        browser::private_dir(&root).unwrap();
        let target = temp.path().join("outside");
        fs::write(&target, b"untouched").unwrap();
        let lock = root.join("browser-host.lock");
        symlink(&target, &lock).unwrap();
        assert!(BrowserStorage::open(root.clone(), BrowserStorageMode::System).is_err());
        fs::remove_file(&lock).unwrap();
        fs::write(&lock, []).unwrap();
        fs::set_permissions(&lock, fs::Permissions::from_mode(0o644)).unwrap();
        let storage = BrowserStorage::open(root.clone(), BrowserStorageMode::System).unwrap();
        assert_eq!(
            fs::metadata(&lock).unwrap().permissions().mode() & 0o777,
            0o644
        );
        drop(storage);
        fs::set_permissions(&lock, fs::Permissions::from_mode(0o600)).unwrap();
        fs::hard_link(&lock, temp.path().join("alias")).unwrap();
        assert!(matches!(
            BrowserStorage::open(root, BrowserStorageMode::System),
            Err(NativeFailure::InvalidEvidence)
        ));
        assert_eq!(fs::read(target).unwrap(), b"untouched");
    }
}
