// SPDX-License-Identifier: Apache-2.0
//! Darwin process identities used for admission and generation-bound signals.
//!
//! The combined libproc flavor is Apple's fixed-size API in macOS 15 XNU
//! `sys/proc_info_private.h`. libc does not expose it yet. Keep this binding
//! local until libc provides the combined structure and audit-signal function.
use std::{
    io, mem,
    path::Path,
    time::{Duration, Instant},
};

use serde::{Deserialize, Serialize};

#[repr(C)]
struct UniqueInfo {
    uuid: [u8; 16],
    birth: u64,
    parent_birth: u64,
    version: i32,
    original_parent_version: i32,
    reserved: [u64; 2],
}
const _: () = assert!(mem::size_of::<UniqueInfo>() == 56);
#[repr(C)]
struct BsdUniqueInfo {
    bsd: libc::proc_bsdinfo,
    unique: UniqueInfo,
}

unsafe extern "C" {
    fn proc_signal_with_audittoken(token: *mut [u32; 8], signal: i32) -> i32;
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    pub pid: i32,
    pub birth: u64,
    pub parent_birth: u64,
    pub version: u32,
    pub original_parent_version: u32,
    pub zombie: bool,
}

impl Identity {
    pub fn capture(pid: i32) -> io::Result<Self> {
        if pid <= 0 {
            return Err(io::Error::from_raw_os_error(libc::EINVAL));
        }
        const BSD_WITH_UNIQUE: i32 = 18;
        let mut info = mem::MaybeUninit::<BsdUniqueInfo>::zeroed();
        let size = mem::size_of::<BsdUniqueInfo>() as i32;
        let count =
            unsafe { libc::proc_pidinfo(pid, BSD_WITH_UNIQUE, 1, info.as_mut_ptr().cast(), size) };
        if count != size {
            return Err(io::Error::last_os_error());
        }
        let info = unsafe { info.assume_init() };
        if info.bsd.pbi_pid != pid as u32 || info.unique.birth == 0 {
            return Err(io::Error::other("Native process identity is unavailable"));
        }
        Ok(Self {
            pid,
            birth: info.unique.birth,
            parent_birth: info.unique.parent_birth,
            version: info.unique.version as u32,
            original_parent_version: info.unique.original_parent_version as u32,
            zombie: info.bsd.pbi_status == 5,
        })
    }

    pub fn name(self) -> String {
        format!("{}-{}", self.birth, self.version)
    }

    pub fn refresh(self) -> io::Result<Option<Self>> {
        match Self::capture(self.pid) {
            Ok(current) if current.birth == self.birth && !current.zombie => Ok(Some(current)),
            Ok(_) => Ok(None),
            Err(error) if error.raw_os_error() == Some(libc::ESRCH) => Ok(None),
            Err(error) => Err(error),
        }
    }

    pub fn signal(self, signal: i32) -> io::Result<()> {
        // XNU's proc_find_audit_token consumes PID and PID version from these
        // slots, then checks real caller credentials and holds a native process
        // reference through psignal. A stale/recycled generation is rejected by
        // the kernel; a check followed by kill(pid) would reopen that race.
        let mut token = [0; 8];
        token[5] = self.pid as u32;
        token[7] = self.version;
        if unsafe { proc_signal_with_audittoken(&raw mut token, signal) } == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }
}

pub fn registration(session: &Path, identity: Identity) -> io::Result<()> {
    use std::io::Write;
    let owner = session.join("owner");
    let accepted = owner.join("accepted").join(identity.name());
    if accepted.is_file() {
        return Ok(());
    }
    let requests = owner.join("requests");
    let request = requests.join(identity.name());
    let mut file = tempfile::NamedTempFile::new_in(&requests)?;
    serde_json::to_writer(&mut file, &identity)?;
    file.flush()?;
    match file.persist_noclobber(request) {
        Ok(_) => (),
        Err(error) if error.error.kind() == io::ErrorKind::AlreadyExists => (),
        Err(error) => return Err(error.error),
    }
    let deadline = Instant::now() + Duration::from_secs(7);
    while !accepted.is_file() {
        if Instant::now() >= deadline || !owner.join("alive").is_file() {
            return Err(io::Error::other(
                "Native process ownership was not acknowledged",
            ));
        }
        std::thread::sleep(Duration::from_millis(2));
    }
    Ok(())
}

pub fn inventory() -> io::Result<Vec<Identity>> {
    let mut pids = vec![0i32; 1024];
    loop {
        let size = (pids.len() * mem::size_of::<i32>()) as i32;
        let count = unsafe { libc::proc_listallpids(pids.as_mut_ptr().cast(), size) };
        if count <= 0 {
            return Err(io::Error::last_os_error());
        }
        if count as usize >= pids.len() {
            if pids.len() >= 65536 {
                return Err(io::Error::other("Process inventory is too large"));
            }
            pids.resize(pids.len() * 2, 0);
            continue;
        }
        // Unrelated processes can legitimately reject inspection. Inventory is
        // discovery only; previously admitted births use strict refresh instead.
        return Ok(pids[..count as usize]
            .iter()
            .filter_map(|pid| Identity::capture(*pid).ok())
            .collect());
    }
}

#[cfg(test)]
mod tests {
    use std::process::{Command, Stdio};

    use super::*;

    #[test]
    fn stale_audit_versions_cannot_signal_an_owned_child() {
        let mut child = Command::new("/bin/sleep")
            .arg("30")
            .stdout(Stdio::null())
            .spawn()
            .unwrap();
        let identity = Identity::capture(child.id() as i32).unwrap();
        let mut stale = identity;
        stale.version = stale.version.wrapping_sub(1);
        assert_eq!(
            stale.signal(libc::SIGKILL).unwrap_err().raw_os_error(),
            Some(libc::ESRCH)
        );
        assert!(child.try_wait().unwrap().is_none());
        identity.signal(libc::SIGSTOP).unwrap();
        let mut status = 0;
        assert_eq!(
            unsafe { libc::waitpid(identity.pid, &raw mut status, libc::WUNTRACED) },
            identity.pid
        );
        assert!(libc::WIFSTOPPED(status));
        identity.signal(libc::SIGTERM).unwrap();
        identity.signal(libc::SIGCONT).unwrap();
        assert!(!child.wait().unwrap().success());
        assert!(identity.refresh().unwrap().is_none());
        assert_eq!(
            identity.signal(libc::SIGKILL).unwrap_err().raw_os_error(),
            Some(libc::ESRCH)
        );
    }
}
