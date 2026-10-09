use std::{
    collections::BTreeSet,
    io,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::PathBuf,
};

#[cfg(test)]
use super::linux_snapshot::endpoint;
use super::{
    linux_snapshot::{owned, process, sockets},
    *,
};

pub struct Native {
    root: PathBuf,
}
impl Default for Native {
    fn default() -> Self {
        Self {
            root: "/proc".into(),
        }
    }
}
impl Backend for Native {
    fn snapshot(&mut self, ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
        super::linux_snapshot::snapshot(&self.root, ports, protocol)
    }

    fn terminate(&mut self, pid: u32, expected: &[Entry]) -> Result<bool> {
        let identity = expected[0].identity.unwrap();
        let before = process(&self.root, pid).map_err(|_| {
            Failure::new(
                Code::IdentityUnverifiable,
                "Cannot verify process identity; no signal was sent.",
            )
        })?;
        let Some(before) = before.filter(|p| !p.dead) else {
            return Ok(false);
        };
        if before.birth != identity.birth {
            return Err(Failure::new(
                Code::IdentityChanged,
                "Process identity changed; no signal was sent.",
            ));
        }
        // A pidfd keeps signaling tied to this process even if its numeric PID
        // is reused.
        let fd = unsafe { libc::syscall(libc::SYS_pidfd_open, pid, 0) as i32 };
        let handle = if fd >= 0 {
            Some(unsafe { OwnedFd::from_raw_fd(fd) })
        } else {
            let e = io::Error::last_os_error();
            if e.raw_os_error() == Some(libc::ESRCH) {
                return Ok(false);
            }
            if !matches!(e.raw_os_error(), Some(libc::ENOSYS | libc::EINVAL)) {
                return Err(Failure::io(&e));
            }
            // Older kernels have no pidfd. Retain the same immediate
            // birth/socket revalidation contract before kill(2);
            // never chase replacement owners.
            None
        };
        let mut check = Report::default();
        let ports = expected.iter().map(|e| e.port).collect();
        let current = sockets(&self.root, &ports, Protocol::All, &mut check);
        let inodes = owned(&self.root, pid).map_err(|_| {
            Failure::new(
                Code::IdentityUnverifiable,
                "Cannot recheck socket ownership; no signal was sent.",
            )
        })?;
        if !check.errors.is_empty() {
            return Err(Failure::new(
                Code::IdentityUnverifiable,
                "Cannot recheck socket tables; no signal was sent.",
            ));
        }
        let matches = expected.iter().any(|e| {
            e.identity.is_some_and(|id| {
                inodes.contains(&id.socket)
                    && current.get(&id.socket).is_some_and(|now| {
                        now.port == e.port && now.protocol == e.protocol && now.address == e.address
                    })
            })
        });
        if !matches {
            return Err(Failure::new(
                Code::OwnershipChanged,
                "Process no longer owns an originally observed socket; no signal was sent.",
            ));
        }
        let after = process(&self.root, pid).map_err(|_| {
            Failure::new(
                Code::IdentityUnverifiable,
                "Cannot recheck process identity; no signal was sent.",
            )
        })?;
        let Some(after) = after.filter(|p| !p.dead) else {
            return Ok(false);
        };
        if after.birth != identity.birth {
            return Err(Failure::new(
                Code::IdentityChanged,
                "Process identity changed; no signal was sent.",
            ));
        }
        runtime::check_cancelled()?;
        let result = unsafe {
            if let Some(handle) = handle {
                libc::syscall(
                    libc::SYS_pidfd_send_signal,
                    handle.as_raw_fd(),
                    libc::SIGKILL,
                    std::ptr::null::<libc::siginfo_t>(),
                    0,
                ) as i32
            } else {
                libc::kill(pid as i32, libc::SIGKILL)
            }
        };
        if result == 0 {
            Ok(true)
        } else {
            let error = io::Error::last_os_error();
            if error.raw_os_error() == Some(libc::ESRCH) {
                Ok(false)
            } else {
                Err(Failure::io(&error))
            }
        }
    }

    fn alive(&mut self, pid: u32, birth: u128) -> Result<bool> {
        Ok(process(&self.root, pid)
            .map_err(|e| Failure::io(&e))?
            .is_some_and(|p| p.birth == birth && !p.dead))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn native_table_endianness() {
        assert_eq!(
            endpoint("0100007F:1F90", false),
            Some(("127.0.0.1".into(), 8080))
        );
        assert_eq!(
            endpoint("00000000000000000000000001000000:0035", true),
            Some(("::1".into(), 53))
        );
    }
    #[test]
    fn inaccessible_tables_never_become_verified_empty() {
        let temp = tempfile::tempdir().unwrap();
        let report = Native {
            root: temp.path().into(),
        }
        .snapshot(&[1234].into(), Protocol::All);
        assert!(!report.errors.is_empty());
    }
}
