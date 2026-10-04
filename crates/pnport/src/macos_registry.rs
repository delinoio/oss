// SPDX-License-Identifier: Apache-2.0
//! Guardian-admitted process births, with authenticated crash recovery.
use std::{
    collections::{HashMap, HashSet},
    fs,
    io::{self, Read, Write},
    path::{Path, PathBuf},
    time::{Duration, Instant},
};

use hmac::{Hmac, Mac};
use pnport_core::macos_process::{inventory, Identity};
use serde::{Deserialize, Serialize};
use sha2::Sha256;

type Authentication = Hmac<Sha256>;

#[derive(Debug, Deserialize, Serialize)]
pub enum FailureStage {
    Registration,
    Cleanup,
    JournalWrite,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Accepted {
    identity: Identity,
    signature: [u8; 32],
}

pub struct Registry {
    directory: PathBuf,
    key: [u8; 32],
    births: HashMap<u64, Identity>,
    versions: HashMap<u32, u64>,
    journal_failed: bool,
}

fn invalid() -> io::Error {
    io::Error::other("Native process ownership is unavailable")
}

impl Registry {
    pub fn create(session: &Path, key: [u8; 32]) -> io::Result<Self> {
        let directory = session.join("owner");
        pnport_core::cache::private_dir(&directory).map_err(|_| invalid())?;
        for child in ["requests", "accepted"] {
            pnport_core::cache::private_dir(&directory.join(child)).map_err(|_| invalid())?;
        }
        Ok(Self {
            directory,
            key,
            births: HashMap::new(),
            versions: HashMap::new(),
            journal_failed: false,
        })
    }

    pub fn record_failure(&self, stage: FailureStage) {
        let Ok(mut file) = tempfile::NamedTempFile::new_in(&self.directory) else {
            return;
        };
        if serde_json::to_writer(&mut file, &stage).is_ok() && file.flush().is_ok() {
            let _ = file.persist_noclobber(self.directory.join("failure"));
        }
    }

    pub fn failure_stage(&self) -> Option<FailureStage> {
        serde_json::from_slice(&bounded_read(&self.directory.join("failure")).ok()?).ok()
    }

    pub fn empty(&self) -> bool {
        self.births.is_empty()
    }

    pub fn recover(&mut self) -> io::Result<()> {
        pnport_core::cache::private_dir(&self.directory.join("accepted")).map_err(|_| invalid())?;
        let mut failed = false;
        // Accepted files can be reached by the injected workload. Only the
        // supervisor/guardian holds this key; requests or forged records must
        // never grant signalling authority. Continue loading valid records so
        // an invalid sibling does not prevent best-effort owned cleanup.
        for (index, entry) in fs::read_dir(self.directory.join("accepted"))?.enumerate() {
            if index >= 65536 {
                return Err(invalid());
            }
            let result = (|| {
                let entry = entry?;
                if entry.file_name().as_encoded_bytes().starts_with(b".") {
                    return Ok(());
                }
                let accepted: Accepted = serde_json::from_slice(&bounded_read(&entry.path())?)?;
                let bytes = serde_json::to_vec(&accepted.identity)?;
                let mut authentication =
                    Authentication::new_from_slice(&self.key).map_err(|_| invalid())?;
                authentication.update(&bytes);
                authentication
                    .verify_slice(&accepted.signature)
                    .map_err(|_| invalid())?;
                if entry.file_name() != accepted.identity.name().as_str() {
                    return Err(invalid());
                }
                self.remember(accepted.identity)
            })();
            failed |= result.is_err();
        }
        if failed {
            Err(invalid())
        } else {
            Ok(())
        }
    }

    fn remember(&mut self, identity: Identity) -> io::Result<()> {
        if identity.pid <= 0 || identity.birth == 0 {
            return Err(invalid());
        }
        if self
            .versions
            .get(&identity.version)
            .is_some_and(|birth| *birth != identity.birth)
            || self
                .births
                .get(&identity.birth)
                .is_some_and(|previous| previous.pid != identity.pid)
        {
            return Err(invalid());
        }
        self.versions.insert(identity.version, identity.birth);
        self.births.insert(identity.birth, identity);
        Ok(())
    }

    pub fn admit(&mut self, identity: Identity) -> io::Result<()> {
        self.remember(identity)?;
        self.commit(identity)
    }

    fn commit(&self, identity: Identity) -> io::Result<()> {
        pnport_core::cache::private_dir(&self.directory.join("accepted")).map_err(|_| invalid())?;
        let bytes = serde_json::to_vec(&identity)?;
        let mut authentication =
            Authentication::new_from_slice(&self.key).map_err(|_| invalid())?;
        authentication.update(&bytes);
        let accepted = Accepted {
            identity,
            signature: authentication.finalize().into_bytes().into(),
        };
        let path = self.directory.join("accepted").join(identity.name());
        let mut file = tempfile::NamedTempFile::new_in(self.directory.join("accepted"))?;
        serde_json::to_writer(&mut file, &accepted)?;
        file.flush()?;
        file.as_file().sync_all()?;
        file.persist(path).map_err(|error| error.error)?;
        Ok(())
    }

    fn observe_for_cleanup(&mut self, identity: Identity) -> io::Result<()> {
        self.remember(identity)?;
        // Native birth/ancestry authority is already established. A disk or
        // permission failure must not block signalling that known birth during
        // shutdown. Retain it in memory, attempt cleanup, and fail the outcome;
        // never acknowledge a new user image through this recovery-only path.
        if self.commit(identity).is_err() {
            self.journal_failed = true;
            self.record_failure(FailureStage::JournalWrite);
        }
        Ok(())
    }

    fn complete(&self, escalated: bool) -> io::Result<bool> {
        if self.journal_failed {
            Err(invalid())
        } else {
            Ok(escalated)
        }
    }

    fn owned(&self, identity: Identity) -> bool {
        self.births.contains_key(&identity.birth)
            || self.births.contains_key(&identity.parent_birth)
            || self
                .versions
                .contains_key(&identity.original_parent_version)
    }

    pub fn requests(&mut self) -> io::Result<()> {
        pnport_core::cache::private_dir(&self.directory.join("requests")).map_err(|_| invalid())?;
        // Parents wait for durable admission before fork/spawn. A child that
        // was stopped before its own callback is still discoverable by the
        // kernel's immutable original parent version after reparenting.
        for (index, entry) in fs::read_dir(self.directory.join("requests"))?.enumerate() {
            if index >= 65536 {
                return Err(invalid());
            }
            let entry = entry?;
            if entry.file_name().as_encoded_bytes().starts_with(b".") {
                continue;
            }
            let requested: Identity = serde_json::from_slice(&bounded_read(&entry.path())?)?;
            if entry.file_name() != requested.name().as_str() {
                return Err(invalid());
            }
            match requested.refresh()? {
                Some(current) if current.version == requested.version && self.owned(current) => {
                    self.admit(current)?
                }
                Some(current) if self.owned(current) => (),
                Some(_) if self.empty() => continue,
                Some(_) => return Err(invalid()),
                None => (),
            }
            fs::remove_file(entry.path())?;
        }
        Ok(())
    }

    fn live(&mut self) -> io::Result<Vec<Identity>> {
        let mut candidates = inventory()?;
        loop {
            let mut added = false;
            for identity in &candidates {
                if !identity.zombie
                    && self.owned(*identity)
                    && !self.versions.contains_key(&identity.version)
                {
                    self.observe_for_cleanup(*identity)?;
                    added = true;
                }
            }
            if !added {
                break;
            }
        }
        candidates.clear();
        for previous in self.births.values().copied().collect::<Vec<_>>() {
            if let Some(current) = previous.refresh()? {
                if !self.versions.contains_key(&current.version) {
                    self.observe_for_cleanup(current)?;
                }
                candidates.push(current);
            }
        }
        Ok(candidates)
    }

    pub fn resume(&mut self, reserved: i32, foreground: i32) -> io::Result<()> {
        let live = self.live()?;
        if self.journal_failed {
            return Err(invalid());
        }
        if foreground == reserved {
            unsafe {
                libc::kill(-reserved, libc::SIGCONT);
            }
        }
        for identity in live
            .into_iter()
            .filter(|identity| identity.group == foreground)
        {
            send(identity, libc::SIGCONT)?;
        }
        Ok(())
    }

    fn terminate(
        &self,
        mut live: Vec<Identity>,
        signal: i32,
        signalled: &mut HashSet<(u64, u32)>,
    ) -> io::Result<()> {
        // XNU assigns increasing birth identifiers at fork. Younger descendants
        // receive termination before their parents can exit and orphan another
        // stopped group. Queue every signal before resuming any parked member;
        // per-process TERM/CONT pairs allowed parent exit to win this ordering.
        live.sort_unstable_by_key(|identity| std::cmp::Reverse(identity.birth));
        live.retain(|identity| signalled.insert((identity.birth, identity.version)));
        for identity in &live {
            send(*identity, signal)?;
        }
        for identity in live {
            send(identity, libc::SIGCONT)?;
        }
        Ok(())
    }

    pub fn stop(&mut self, group: i32, signal: i32) -> io::Result<bool> {
        let deadline = Instant::now() + Duration::from_secs(5);
        let mut signalled = HashSet::new();
        let initial = self.live()?;
        self.terminate(initial, signal, &mut signalled)?;
        unsafe {
            // The guardian PID still reserves the original group. Cover its
            // anchor/startup members after admitted descendants have received
            // termination; never address additional snapshot group IDs.
            libc::kill(-group, signal);
            libc::kill(-group, libc::SIGCONT);
        }
        loop {
            let live = self.live()?;
            if live.is_empty() {
                // A final parent can fork between inventory and its refresh.
                // Scan again after all known parents are gone so that last
                // child is discovered before accepting an empty tree.
                if self.live()?.is_empty() {
                    return self.complete(false);
                }
                continue;
            }
            self.terminate(live, signal, &mut signalled)?;
            if Instant::now() >= deadline {
                break;
            }
            std::thread::sleep(Duration::from_millis(25));
        }

        let deadline = Instant::now() + Duration::from_secs(1);
        loop {
            let live = self.live()?;
            if live.is_empty() {
                if self.live()?.is_empty() {
                    return self.complete(true);
                }
                continue;
            }
            for identity in live {
                send(identity, libc::SIGKILL)?;
            }
            if Instant::now() >= deadline {
                return Err(invalid());
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}

fn send(identity: Identity, signal: i32) -> io::Result<()> {
    match identity.signal(signal) {
        Ok(()) => Ok(()),
        // Exit or exec can invalidate an image after refresh. The next scan
        // refreshes the same birth and retries its new audit generation.
        Err(error) if error.raw_os_error() == Some(libc::ESRCH) => Ok(()),
        Err(error) => Err(error),
    }
}

pub fn bounded_read(path: &Path) -> io::Result<Vec<u8>> {
    use std::os::unix::fs::OpenOptionsExt;
    let file = fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
        .open(path)?;
    if !file.metadata()?.is_file() {
        return Err(invalid());
    }
    let mut bytes = Vec::new();
    file.take(1025).read_to_end(&mut bytes)?;
    if bytes.len() > 1024 {
        return Err(invalid());
    }
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use std::process::Command;

    use super::*;

    #[test]
    fn authenticated_recovery_preserves_an_unrelated_process() {
        let directory = tempfile::tempdir().unwrap();
        let key = [42; 32];
        let mut owned = Command::new("/bin/sleep").arg("30").spawn().unwrap();
        let mut unrelated = Command::new("/bin/sleep").arg("30").spawn().unwrap();
        let owned_identity = Identity::capture(owned.id() as i32).unwrap();
        let unrelated_identity = Identity::capture(unrelated.id() as i32).unwrap();
        let mut registry = Registry::create(directory.path(), key).unwrap();
        registry.admit(owned_identity).unwrap();
        fs::write(
            registry
                .directory
                .join("requests")
                .join(unrelated_identity.name()),
            serde_json::to_vec(&unrelated_identity).unwrap(),
        )
        .unwrap();
        assert!(registry.requests().is_err());
        assert_eq!(registry.births.len(), 1);

        let forged = Accepted {
            identity: unrelated_identity,
            signature: [0; 32],
        };
        fs::write(
            registry
                .directory
                .join("accepted")
                .join(unrelated_identity.name()),
            serde_json::to_vec(&forged).unwrap(),
        )
        .unwrap();
        let mut recovered = Registry::create(directory.path(), key).unwrap();
        assert!(recovered.recover().is_err());
        assert_eq!(recovered.births.len(), 1);
        // A non-existent group avoids touching any test-harness process group.
        assert!(!recovered.stop(i32::MAX, libc::SIGTERM).unwrap());
        assert!(!owned.wait().unwrap().success());
        assert!(unrelated.try_wait().unwrap().is_none());
        unrelated.kill().unwrap();
        unrelated.wait().unwrap();
    }
}

#[cfg(test)]
mod resume_tests {
    use std::{os::unix::process::CommandExt, process::Command};

    use super::*;

    #[test]
    fn job_resume_leaves_an_independent_owned_group_stopped() {
        let directory = tempfile::tempdir().unwrap();
        let mut registry = Registry::create(directory.path(), [23; 32]).unwrap();
        let mut foreground = Command::new("/bin/sleep")
            .arg("30")
            .process_group(0)
            .spawn()
            .unwrap();
        let mut background = Command::new("/bin/sleep")
            .arg("30")
            .process_group(0)
            .spawn()
            .unwrap();
        let foreground_identity = Identity::capture(foreground.id() as i32).unwrap();
        let background_identity = Identity::capture(background.id() as i32).unwrap();
        for identity in [foreground_identity, background_identity] {
            registry.admit(identity).unwrap();
            identity.signal(libc::SIGSTOP).unwrap();
            let mut status = 0;
            assert_eq!(
                unsafe { libc::waitpid(identity.pid, &raw mut status, libc::WUNTRACED) },
                identity.pid
            );
            assert!(libc::WIFSTOPPED(status));
        }
        registry
            .resume(i32::MAX, foreground_identity.group)
            .unwrap();
        let deadline = Instant::now() + Duration::from_secs(2);
        let mut status = 0;
        while unsafe {
            libc::waitpid(
                foreground_identity.pid,
                &raw mut status,
                libc::WCONTINUED | libc::WNOHANG,
            )
        } == 0
        {
            assert!(Instant::now() < deadline);
            std::thread::sleep(Duration::from_millis(10));
        }
        assert!(libc::WIFCONTINUED(status));
        assert_eq!(
            unsafe {
                libc::waitpid(
                    background_identity.pid,
                    &raw mut status,
                    libc::WCONTINUED | libc::WNOHANG,
                )
            },
            0
        );
        registry.stop(i32::MAX, libc::SIGTERM).unwrap();
        assert!(!foreground.wait().unwrap().success());
        assert!(!background.wait().unwrap().success());
    }
}

#[cfg(test)]
mod journal_failure_tests {
    use std::{
        os::unix::process::ExitStatusExt,
        process::{Command, Stdio},
    };

    use super::*;

    #[test]
    fn lost_journal_writes_do_not_abandon_a_known_birth_after_exec() {
        let directory = tempfile::tempdir().unwrap();
        let mut registry = Registry::create(directory.path(), [31; 32]).unwrap();
        let mut child = Command::new("/bin/sh")
            .args(["-c", "read ready; exec /bin/sleep 30"])
            .stdin(Stdio::piped())
            .spawn()
            .unwrap();
        let previous = Identity::capture(child.id() as i32).unwrap();
        registry.admit(previous).unwrap();
        let accepted = registry.directory.join("accepted");
        fs::rename(&accepted, registry.directory.join("accepted-saved")).unwrap();
        fs::write(&accepted, b"fixture directory conflict").unwrap();
        child.stdin.take().unwrap().write_all(b"ready\n").unwrap();
        let deadline = Instant::now() + Duration::from_secs(3);
        loop {
            let current = Identity::capture(previous.pid).unwrap();
            assert_eq!(current.birth, previous.birth);
            if current.version != previous.version {
                break;
            }
            assert!(Instant::now() < deadline);
            std::thread::sleep(Duration::from_millis(10));
        }
        assert!(registry.stop(i32::MAX, libc::SIGTERM).is_err());
        assert!(registry.journal_failed);
        assert_eq!(child.wait().unwrap().signal(), Some(libc::SIGTERM));
        assert!(previous.refresh().unwrap().is_none());
    }
}
