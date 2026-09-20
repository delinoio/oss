use std::{
    collections::{BTreeMap, BTreeSet},
    fs, io,
    net::{Ipv4Addr, Ipv6Addr},
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::{Path, PathBuf},
};

use super::*;

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
struct Process {
    birth: u128,
    name: String,
    dead: bool,
}

fn process(root: &Path, pid: u32) -> io::Result<Option<Process>> {
    let stat = match fs::read(root.join(pid.to_string()).join("stat")) {
        Ok(s) => s,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    let text = String::from_utf8_lossy(&stat);
    let start = text.find('(').ok_or(io::ErrorKind::InvalidData)?;
    let end = text.rfind(')').ok_or(io::ErrorKind::InvalidData)?;
    let tail: Vec<_> = text
        .get(end + 1..)
        .ok_or(io::ErrorKind::InvalidData)?
        .split_whitespace()
        .collect();
    let birth = tail
        .get(19)
        .ok_or(io::ErrorKind::InvalidData)?
        .parse()
        .map_err(|_| io::ErrorKind::InvalidData)?;
    Ok(Some(Process {
        birth,
        name: text[start + 1..end].into(),
        dead: matches!(tail.first(), Some(&"Z" | &"X")),
    }))
}

fn endpoint(raw: &str, ipv6: bool) -> Option<(String, u16)> {
    let (address, port) = raw.split_once(':')?;
    let port = u16::from_str_radix(port, 16).ok()?;
    let address = if ipv6 {
        if address.len() != 32 {
            return None;
        }
        let mut bytes = [0; 16];
        for (i, chunk) in address.as_bytes().chunks_exact(8).enumerate() {
            let n = u32::from_str_radix(std::str::from_utf8(chunk).ok()?, 16).ok()?;
            bytes[i * 4..i * 4 + 4].copy_from_slice(&n.to_ne_bytes());
        }
        Ipv6Addr::from(bytes).to_string()
    } else {
        Ipv4Addr::from(u32::from_str_radix(address, 16).ok()?.to_ne_bytes()).to_string()
    };
    Some((address, port))
}

fn sockets(
    root: &Path,
    ports: &BTreeSet<u16>,
    protocol: Protocol,
    report: &mut Report,
) -> BTreeMap<u64, Entry> {
    let mut sockets = BTreeMap::new();
    for (file, kind, ipv6) in [
        ("tcp", Protocol::Tcp, false),
        ("tcp6", Protocol::Tcp, true),
        ("udp", Protocol::Udp, false),
        ("udp6", Protocol::Udp, true),
    ] {
        if !protocol.accepts(kind) {
            continue;
        }
        let text = match fs::read_to_string(root.join("self/net").join(file)) {
            Ok(text) => text,
            Err(e)
                if ipv6
                    && e.kind() == io::ErrorKind::NotFound
                    && !root.join("sys/net/ipv6").exists() =>
            {
                continue
            }
            Err(e) => {
                report.errors.push(enumeration_error(None, e));
                continue;
            }
        };
        for line in text.lines().skip(1) {
            let fields: Vec<_> = line.split_whitespace().collect();
            let parsed = (|| {
                if fields.len() < 10 {
                    return None;
                }
                let (address, port) = endpoint(fields[1], ipv6)?;
                let inode = fields[9].parse::<u64>().ok()?;
                Some((address, port, inode))
            })();
            let Some((address, port, inode)) = parsed else {
                report
                    .errors
                    .push(enumeration_error(None, io::ErrorKind::InvalidData.into()));
                continue;
            };
            if !ports.contains(&port) || kind == Protocol::Tcp && fields[3] != "0A" {
                continue;
            }
            sockets.insert(
                inode,
                Entry {
                    pid: None,
                    name: None,
                    protocol: kind,
                    address,
                    port,
                    status: None,
                    identity: None,
                },
            );
        }
    }
    sockets
}

fn owned(root: &Path, pid: u32) -> io::Result<BTreeSet<u64>> {
    let mut inodes = BTreeSet::new();
    for file in fs::read_dir(root.join(pid.to_string()).join("fd"))? {
        let path = file?.path();
        let target = match fs::read_link(path) {
            Ok(t) => t,
            Err(e) if e.kind() == io::ErrorKind::NotFound => continue,
            Err(e) => return Err(e),
        };
        if let Some(inode) = target
            .to_str()
            .and_then(|s| s.strip_prefix("socket:["))
            .and_then(|s| s.strip_suffix(']'))
            .and_then(|s| s.parse().ok())
        {
            inodes.insert(inode);
        }
    }
    Ok(inodes)
}

impl Backend for Native {
    fn snapshot(&mut self, ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
        let mut report = Report::default();
        let sockets = sockets(&self.root, ports, protocol, &mut report);
        if sockets.is_empty() {
            return report;
        }
        let entries = match fs::read_dir(&self.root) {
            Ok(e) => e,
            Err(e) => {
                report.errors.push(enumeration_error(None, e));
                report.results.extend(sockets.into_values());
                return report;
            }
        };
        let mut found = BTreeSet::new();
        for entry in entries {
            if runtime::cancelled() {
                report.errors.push(Failure::new(
                    Code::Cancelled,
                    "Port enumeration interrupted; results are incomplete.",
                ));
                break;
            }
            let entry = match entry {
                Ok(e) => e,
                Err(e) => {
                    report.errors.push(enumeration_error(None, e));
                    continue;
                }
            };
            let Some(pid) = entry.file_name().to_str().and_then(|s| s.parse().ok()) else {
                continue;
            };
            let before = match process(&self.root, pid) {
                Ok(Some(p)) if !p.dead => p,
                Ok(_) => continue,
                Err(e) => {
                    report.errors.push(enumeration_error(Some(pid), e));
                    continue;
                }
            };
            let inodes = match owned(&self.root, pid) {
                Ok(i) => i,
                Err(e) if e.kind() == io::ErrorKind::NotFound => continue,
                Err(e) => {
                    report.errors.push(enumeration_error(Some(pid), e));
                    continue;
                }
            };
            let stable = matches!(process(&self.root, pid), Ok(Some(p)) if p.birth == before.birth && !p.dead);
            for inode in inodes {
                if let Some(socket) = sockets.get(&inode) {
                    let mut row = socket.clone();
                    row.pid = Some(pid);
                    row.name = Some(before.name.clone());
                    if stable {
                        row.identity = Some(Identity {
                            birth: before.birth,
                            socket: inode,
                        });
                    } else {
                        report.errors.push(
                            Failure::new(
                                Code::IdentityUnverifiable,
                                "Process changed during enumeration; ownership is unverified.",
                            )
                            .pid(pid),
                        );
                    }
                    found.insert(inode);
                    report.results.push(row);
                }
            }
        }
        for (inode, socket) in sockets {
            if !found.contains(&inode) {
                report.results.push(socket);
            }
        }
        report
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
        // A pidfd keeps signaling tied to this process even if its numeric PID is
        // reused.
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
            // Older kernels have no pidfd. Retain the same immediate birth/socket
            // revalidation contract before kill(2); never chase replacement owners.
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
