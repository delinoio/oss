// SPDX-License-Identifier: Apache-2.0
//! Private native launch tokens shared by the supervisor and injected images.

#[cfg(unix)]
use std::{
    fs, io,
    path::{Path, PathBuf},
};

#[cfg(unix)]
use fs2::FileExt;

#[cfg(unix)]
pub struct Entry {
    _lease: fs::File,
    pending: PathBuf,
    entered: PathBuf,
}

#[cfg(unix)]
impl Entry {
    pub fn begin(session: &Path, token: &str) -> io::Result<Self> {
        if !valid_token(token) {
            return Err(io::Error::from(io::ErrorKind::InvalidInput));
        }
        let pending = session.join("pending").join(token);
        let entered = session.join("launch-starting").join(token);
        let lease = fs::File::open(&pending)?;
        lease.try_lock_exclusive()?;
        fs::create_dir_all(session.join("launch-starting"))?;
        // Publish only after acquiring the lease. Constructor exit releases
        // this open-description lock even when no failure record is written.
        fs::hard_link(&pending, &entered)?;
        Ok(Self {
            _lease: lease,
            pending,
            entered,
        })
    }

    pub fn acknowledge(&self) -> io::Result<()> {
        // Readiness removes the pending marker before releasing the lease.
        // An observer that finds an unlocked, still-pending inode must fail.
        fs::remove_file(&self.pending)?;
        fs::remove_file(&self.entered)
    }
}

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum EntryState {
    Missing,
    Initializing,
    Abandoned,
}

#[cfg(unix)]
pub fn entry_state(path: &Path, pending: &fs::Metadata) -> io::Result<EntryState> {
    use std::os::unix::fs::MetadataExt;
    let file = match fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(EntryState::Missing),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file() || (metadata.dev(), metadata.ino()) != (pending.dev(), pending.ino()) {
        return Ok(EntryState::Missing);
    }
    match file.try_lock_exclusive() {
        Ok(()) => Ok(EntryState::Abandoned),
        Err(error) if error.kind() == io::ErrorKind::WouldBlock => Ok(EntryState::Initializing),
        Err(error) => Err(error),
    }
}

pub fn valid_token(token: &str) -> bool {
    token.starts_with("pnport-")
        && token.len() > "pnport-".len()
        && token.len() <= 255
        && token
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
}

#[cfg(test)]
mod tests {
    #[cfg(unix)]
    #[test]
    fn constructor_lease_distinguishes_waiting_abandoned_and_completed_images() {
        const ISOLATED: &str = "PNPORT_TEST_LAUNCH_LEASE";
        if std::env::var_os(ISOLATED).as_deref() != Some(std::ffi::OsStr::new("1")) {
            // Concurrent Command::spawn in another test can briefly inherit
            // this open-description lease between fork and CLOEXEC closure.
            // Run only this scenario in a fresh process; keep the parent suite
            // parallel and prove inherited-lease behavior explicitly below.
            let output = std::process::Command::new(std::env::current_exe().unwrap())
                .args(["launch::tests::constructor_lease_distinguishes_waiting_abandoned_and_completed_images", "--exact"])
                .env(ISOLATED, "1").output().unwrap();
            assert!(
                output.status.success(),
                "isolated constructor lease control failed: {}{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            return;
        }
        let session = tempfile::tempdir().unwrap();
        let pending = session.path().join("pending/pnport-test");
        let entered = session.path().join("launch-starting/pnport-test");
        std::fs::create_dir(session.path().join("pending")).unwrap();
        std::fs::write(&pending, b"").unwrap();
        let metadata = std::fs::metadata(&pending).unwrap();
        assert!(super::entry_state(&entered, &metadata).unwrap() == super::EntryState::Missing);
        let entry = super::Entry::begin(session.path(), "pnport-test").unwrap();
        assert!(
            super::entry_state(&entered, &metadata).unwrap() == super::EntryState::Initializing
        );
        use std::{
            io::{Read, Write},
            os::{
                fd::AsRawFd,
                unix::{net::UnixStream, process::CommandExt},
            },
        };
        let (mut controller, inherited) = UnixStream::pair().unwrap();
        controller
            .set_read_timeout(Some(std::time::Duration::from_secs(3)))
            .unwrap();
        let worker = std::thread::spawn(move || {
            let fd = inherited.as_raw_fd();
            let mut command = std::process::Command::new("/usr/bin/true");
            // Only async-signal-safe descriptor operations run before exec.
            unsafe {
                command.pre_exec(move || {
                    let mut byte = 0u8;
                    if libc::write(fd, b"R".as_ptr().cast(), 1) != 1
                        || libc::read(fd, (&raw mut byte).cast(), 1) != 1
                    {
                        return Err(std::io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
            command.spawn().unwrap()
        });
        let mut ready = [0];
        controller.read_exact(&mut ready).unwrap();
        assert_eq!(ready, *b"R");
        drop(entry);
        // The child still holds the forked open description before exec.
        assert!(
            super::entry_state(&entered, &metadata).unwrap() == super::EntryState::Initializing
        );
        controller.write_all(b"X").unwrap();
        assert!(worker.join().unwrap().wait().unwrap().success());
        assert!(super::entry_state(&entered, &metadata).unwrap() == super::EntryState::Abandoned);
        std::fs::remove_file(&entered).unwrap();
        let entry = super::Entry::begin(session.path(), "pnport-test").unwrap();
        entry.acknowledge().unwrap();
        assert!(!pending.exists() && !entered.exists());
    }
    #[test]
    fn tokens_are_bounded_single_native_filename_components() {
        assert!(super::valid_token("pnport-launch-123"));
        for invalid in ["", "pnport-", "other-123", "pnport-../outside", "pnport-雪"] {
            assert!(!super::valid_token(invalid));
        }
        assert!(!super::valid_token(&format!("pnport-{}", "a".repeat(249))));
    }
}
