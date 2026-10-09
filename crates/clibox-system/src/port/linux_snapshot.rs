//! Private proc-table observation, independent of Linux signaling APIs.

#![cfg_attr(not(target_os = "linux"), allow(dead_code))]

use std::{
    collections::{BTreeMap, BTreeSet},
    fs, io,
    net::{Ipv4Addr, Ipv6Addr},
    path::Path,
};

use super::*;

pub(super) struct Process {
    pub(super) birth: u128,
    name: String,
    pub(super) dead: bool,
}

pub(super) fn process(root: &Path, pid: u32) -> io::Result<Option<Process>> {
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

pub(super) fn endpoint(raw: &str, ipv6: bool) -> Option<(String, u16)> {
    let (address, port) = raw.split_once(':')?;
    let port = u16::from_str_radix(port, 16).ok()?;
    let address = if ipv6 {
        if address.len() != 32 {
            return None;
        }
        let mut bytes = [0; 16];
        for (i, chunk) in address.as_bytes().as_chunks::<8>().0.iter().enumerate() {
            let n = u32::from_str_radix(std::str::from_utf8(chunk).ok()?, 16).ok()?;
            bytes[i * 4..i * 4 + 4].copy_from_slice(&n.to_ne_bytes());
        }
        Ipv6Addr::from(bytes).to_string()
    } else {
        Ipv4Addr::from(u32::from_str_radix(address, 16).ok()?.to_ne_bytes()).to_string()
    };
    Some((address, port))
}

pub(super) fn sockets(
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

pub(super) fn owned(root: &Path, pid: u32) -> io::Result<BTreeSet<u64>> {
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

struct Owner {
    before: Process,
    inodes: BTreeSet<u64>,
    stable: bool,
}

fn inspect_owner(root: &Path, pid: u32) -> io::Result<Option<Owner>> {
    let Some(before) = process(root, pid)?.filter(|p| !p.dead) else {
        return Ok(None);
    };
    let inodes = match owned(root, pid) {
        Ok(inodes) => inodes,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    let stable = matches!(process(root, pid), Ok(Some(p)) if p.birth == before.birth && !p.dead);
    Ok(Some(Owner {
        before,
        inodes,
        stable,
    }))
}

fn append_unmatched(sockets: BTreeMap<u64, Entry>, found: &BTreeSet<u64>, report: &mut Report) {
    for (inode, socket) in sockets {
        if !found.contains(&inode) {
            report.errors.push(
                Failure::new(
                    Code::IdentityUnverifiable,
                    "Socket owner is unavailable; port enumeration is incomplete.",
                )
                .port(socket.port),
            );
            report.results.push(socket);
        }
    }
}

pub(super) fn snapshot(root: &Path, ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
    snapshot_with(root, ports, protocol, inspect_owner)
}

fn snapshot_with(
    root: &Path,
    ports: &BTreeSet<u16>,
    protocol: Protocol,
    mut inspect: impl FnMut(&Path, u32) -> io::Result<Option<Owner>>,
) -> Report {
    let mut report = Report::default();
    let sockets = sockets(root, ports, protocol, &mut report);
    if sockets.is_empty() {
        return report;
    }
    let entries = match fs::read_dir(root) {
        Ok(e) => e,
        Err(e) => {
            report.errors.push(enumeration_error(None, e));
            append_unmatched(sockets, &BTreeSet::new(), &mut report);
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
        let owner = match inspect(root, pid) {
            Ok(Some(owner)) => owner,
            Ok(None) => continue,
            Err(e) => {
                report.errors.push(enumeration_error(Some(pid), e));
                continue;
            }
        };
        let Owner {
            before,
            inodes,
            stable,
        } = owner;
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
    append_unmatched(sockets, &found, &mut report);
    report
}

#[cfg(all(test, unix))]
pub(super) mod tests {
    use super::*;

    pub(crate) fn fixture() -> tempfile::TempDir {
        let temp = tempfile::tempdir().unwrap();
        fs::create_dir_all(temp.path().join("self/net")).unwrap();
        for table in ["tcp", "tcp6", "udp", "udp6"] {
            fs::write(temp.path().join("self/net").join(table), "header\n").unwrap();
        }
        temp
    }

    pub(crate) fn listener(root: &Path, table: &str, address: &str, inode: u64) {
        use std::io::Write;
        let mut file = fs::OpenOptions::new()
            .append(true)
            .open(root.join("self/net").join(table))
            .unwrap();
        writeln!(
            file,
            "0: {address} 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 {inode} 1"
        )
        .unwrap();
    }

    pub(crate) fn owner(root: &Path, pid: u32, inode: Option<u64>) {
        let process = root.join(pid.to_string());
        fs::create_dir_all(process.join("fd")).unwrap();
        // tail[19] is field 22 (starttime) in the proc stat format.
        fs::write(
            process.join("stat"),
            format!("{pid} (fixture owner) S {} 123\n", vec!["0"; 18].join(" ")),
        )
        .unwrap();
        if let Some(inode) = inode {
            #[cfg(unix)]
            std::os::unix::fs::symlink(format!("socket:[{inode}]"), process.join("fd/7")).unwrap();
        }
    }

    fn unknown(report: &Report) {
        assert_eq!(report.results.len(), 1);
        let row = &report.results[0];
        assert_eq!(
            (row.pid, row.name.as_ref(), row.identity),
            (None, None, None)
        );
        assert_eq!((&*row.address, row.port), ("127.0.0.1", 8080));
        let error = report
            .errors
            .iter()
            .find(|e| e.code == Code::IdentityUnverifiable)
            .unwrap();
        assert_eq!(error.port, Some(8080));
        assert_eq!(error.pid, None);
        assert_eq!(
            error.message,
            "Socket owner is unavailable; port enumeration is incomplete."
        );
        assert!(!serde_json::to_string(&report.errors)
            .unwrap()
            .contains("private-owner-marker"));
    }

    #[test]
    fn missing_and_empty_owner_directories_keep_unknown_endpoint() {
        for state in 0..3 {
            let temp = fixture();
            listener(temp.path(), "tcp", "0100007F:1F90", 4242);
            match state {
                1 => owner(temp.path(), 42, None),
                2 => fs::create_dir(temp.path().join("42")).unwrap(),
                _ => (),
            }
            unknown(&snapshot(temp.path(), &[8080].into(), Protocol::Tcp));
        }
    }

    #[test]
    fn denied_and_disappearing_process_observations_remain_incomplete() {
        let temp = fixture();
        listener(temp.path(), "tcp", "0100007F:1F90", 4242);
        owner(temp.path(), 42, Some(4242));
        let denied = snapshot_with(temp.path(), &[8080].into(), Protocol::Tcp, |_, _| {
            Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "private-owner-marker",
            ))
        });
        unknown(&denied);
        assert!(denied
            .errors
            .iter()
            .any(|e| e.code == Code::PermissionDenied && e.pid == Some(42)));
        let vanished = snapshot_with(temp.path(), &[8080].into(), Protocol::Tcp, |root, pid| {
            fs::remove_file(root.join(pid.to_string()).join("fd/7")).unwrap();
            // The socket table was already read. An fd disappearing before
            // ownership observation cannot supply an original verified PID.
            inspect_owner(root, pid)
        });
        unknown(&vanished);
        fs::write(temp.path().join("self/net/tcp"), "header\n").unwrap();
        let empty = snapshot(temp.path(), &[8080].into(), Protocol::Tcp);
        assert!(empty.results.is_empty());
        assert!(empty.errors.is_empty());
    }

    #[test]
    fn verified_ipv4_ipv6_udp_owners_and_scope_remain_complete() {
        let temp = fixture();
        listener(temp.path(), "tcp", "0100007F:1F90", 4242);
        listener(
            temp.path(),
            "tcp6",
            "00000000000000000000000001000000:1F90",
            4243,
        );
        listener(temp.path(), "udp", "0100007F:0035", 4244);
        listener(temp.path(), "tcp", "0100007F:2328", 4245);
        owner(temp.path(), 42, Some(4242));
        owner(temp.path(), 43, Some(4243));
        owner(temp.path(), 44, Some(4244));
        let report = snapshot(temp.path(), &[53, 8080].into(), Protocol::All);
        assert!(report.errors.is_empty());
        assert_eq!(report.results.len(), 3);
        for row in &report.results {
            assert_eq!(row.name.as_deref(), Some("fixture owner"));
            assert_eq!(row.identity.unwrap().birth, 123);
            assert!(row.pid.is_some());
        }
        assert!(report
            .results
            .iter()
            .any(|r| r.address == "::1" && r.pid == Some(43)));
        assert!(snapshot(temp.path(), &[53].into(), Protocol::Tcp)
            .results
            .is_empty());
    }

    #[test]
    fn changed_owner_identity_is_retained_without_authority() {
        let temp = fixture();
        listener(temp.path(), "tcp", "0100007F:1F90", 4242);
        owner(temp.path(), 42, Some(4242));
        let report = snapshot_with(temp.path(), &[8080].into(), Protocol::Tcp, |root, pid| {
            let mut owner = inspect_owner(root, pid)?.unwrap();
            owner.stable = false;
            Ok(Some(owner))
        });
        assert_eq!(report.results[0].pid, Some(42));
        assert!(report.results[0].identity.is_none());
        assert_eq!(report.errors[0].code, Code::IdentityUnverifiable);
        assert_eq!(report.errors[0].pid, Some(42));
    }
}
