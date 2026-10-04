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
}

fn invalid() -> io::Error {
    io::Error::other("Native process ownership is unavailable")
}

impl Registry {
    pub fn create(session: &Path, key: [u8; 32]) -> io::Result<Self> {
        let directory = session.join("owner");
        for child in ["requests", "accepted"] {
            fs::create_dir_all(directory.join(child))?;
        }
        Ok(Self {
            directory,
            key,
            births: HashMap::new(),
            versions: HashMap::new(),
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

    fn owned(&self, identity: Identity) -> bool {
        self.births.contains_key(&identity.birth)
            || self.births.contains_key(&identity.parent_birth)
            || self
                .versions
                .contains_key(&identity.original_parent_version)
    }

    pub fn requests(&mut self) -> io::Result<()> {
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
                    self.admit(*identity)?;
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
                    self.admit(current)?;
                }
                candidates.push(current);
            }
        }
        Ok(candidates)
    }

    pub fn resume(&mut self, group: i32) -> io::Result<()> {
        unsafe {
            libc::kill(-group, libc::SIGCONT);
        }
        for identity in self.live()? {
            send(identity, libc::SIGCONT)?;
        }
        Ok(())
    }

    pub fn stop(&mut self, group: i32, signal: i32) -> io::Result<bool> {
        unsafe {
            // The still-unreaped guardian PID reserves this original group.
            // Additional groups are never addressed through kill(-snapshot).
            libc::kill(-group, signal);
            libc::kill(-group, libc::SIGCONT);
        }
        let deadline = Instant::now() + Duration::from_secs(5);
        let mut signalled = HashSet::new();
        loop {
            let live = self.live()?;
            if live.is_empty() {
                // A final parent can fork between inventory and its refresh.
                // Scan again after all known parents are gone so that last
                // child is discovered before accepting an empty tree.
                if self.live()?.is_empty() {
                    return Ok(false);
                }
                continue;
            }
            for identity in live {
                if signalled.insert((identity.birth, identity.version)) {
                    send(identity, signal)?;
                    send(identity, libc::SIGCONT)?;
                }
            }
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
                    return Ok(true);
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
