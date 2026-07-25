use std::{
    fs::{self, File, OpenOptions},
    io,
    path::PathBuf,
};

use fs2::FileExt;

pub(crate) struct InstanceGuard {
    _lock: File,
}

#[derive(Debug)]
#[cfg_attr(not(feature = "desktop-cef"), allow(dead_code))]
pub(crate) enum InstanceGuardError {
    AlreadyRunning,
    Unavailable(io::Error),
}

impl InstanceGuard {
    #[cfg_attr(not(feature = "desktop-cef"), allow(dead_code))]
    pub(crate) fn acquire(application_id: &str) -> Result<Self, InstanceGuardError> {
        let directory = dirs::data_local_dir()
            .ok_or_else(|| {
                InstanceGuardError::Unavailable(io::Error::other(
                    "local data directory is unavailable",
                ))
            })?
            .join(application_id);
        Self::acquire_at(directory.join("instance.lock"))
    }

    fn acquire_at(path: PathBuf) -> Result<Self, InstanceGuardError> {
        let directory = path.parent().ok_or_else(|| {
            InstanceGuardError::Unavailable(io::Error::other(
                "instance lock directory is unavailable",
            ))
        })?;
        fs::create_dir_all(directory).map_err(InstanceGuardError::Unavailable)?;
        let lock = OpenOptions::new()
            .create(true)
            .read(true)
            .truncate(false)
            .write(true)
            .open(path)
            .map_err(InstanceGuardError::Unavailable)?;
        match lock.try_lock_exclusive() {
            Ok(()) => Ok(Self { _lock: lock }),
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                Err(InstanceGuardError::AlreadyRunning)
            }
            Err(error) => Err(InstanceGuardError::Unavailable(error)),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn lock_path(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "devhud-instance-{name}-{}-{:?}.lock",
            std::process::id(),
            std::thread::current().id()
        ))
    }

    #[test]
    fn only_one_guard_can_hold_the_instance_lock() {
        let path = lock_path("exclusive");
        let first = InstanceGuard::acquire_at(path.clone()).unwrap();
        assert!(matches!(
            InstanceGuard::acquire_at(path.clone()),
            Err(InstanceGuardError::AlreadyRunning)
        ));
        drop(first);
        assert!(InstanceGuard::acquire_at(path.clone()).is_ok());
        let _ = fs::remove_file(path);
    }

    #[test]
    fn lock_creation_failures_remain_distinct_from_duplicate_instances() {
        let directory = lock_path("unavailable");
        fs::write(&directory, b"not a directory").unwrap();
        let result = InstanceGuard::acquire_at(directory.join("instance.lock"));
        assert!(matches!(result, Err(InstanceGuardError::Unavailable(_))));
        fs::remove_file(directory).unwrap();
    }
}
