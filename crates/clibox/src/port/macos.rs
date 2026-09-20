use std::{
    collections::BTreeSet,
    io,
    mem::size_of,
    net::{Ipv4Addr, Ipv6Addr},
};

use libproc::{
    net_info::SocketFDInfo,
    processes::{pids_by_type, ProcFilter},
};

use super::*;

#[derive(Default)]
pub struct Native {}

#[link(name = "proc")]
extern "C" {
    fn proc_pidfdinfo(pid: i32, fd: i32, flavor: i32, buffer: *mut libc::c_void, size: i32) -> i32;
}

fn process(pid: u32) -> io::Result<Option<libc::proc_bsdinfo>> {
    let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
    let size = size_of::<libc::proc_bsdinfo>() as i32;
    let n = unsafe {
        libc::proc_pidinfo(
            pid as i32,
            libc::PROC_PIDTBSDINFO,
            0,
            (&mut info as *mut libc::proc_bsdinfo).cast(),
            size,
        )
    };
    if n == size {
        Ok(Some(info))
    } else {
        let e = io::Error::last_os_error();
        if e.raw_os_error() == Some(libc::ESRCH) {
            Ok(None)
        } else {
            Err(e)
        }
    }
}
fn birth(info: &libc::proc_bsdinfo) -> u128 {
    (u128::from(info.pbi_start_tvsec) << 64) | u128::from(info.pbi_start_tvusec)
}
fn name(info: &libc::proc_bsdinfo) -> String {
    let chars = if info.pbi_name[0] == 0 {
        &info.pbi_comm[..]
    } else {
        &info.pbi_name[..]
    };
    String::from_utf8_lossy(
        &chars
            .iter()
            .take_while(|c| **c != 0)
            .map(|c| *c as u8)
            .collect::<Vec<_>>(),
    )
    .into_owned()
}

fn rows(pid: u32, info: &libc::proc_bsdinfo, ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
    let mut report = Report::default();
    // Reserve extra descriptors for concurrent opens. A full buffer is reported as
    // incomplete rather than silently truncating or retrying an operation snapshot.
    let count = info.pbi_nfiles as usize + 64;
    let mut fds = vec![
        libc::proc_fdinfo {
            proc_fd: 0,
            proc_fdtype: 0
        };
        count
    ];
    let size = std::mem::size_of_val(&fds[..]) as i32;
    let n = unsafe {
        libc::proc_pidinfo(
            pid as i32,
            libc::PROC_PIDLISTFDS,
            0,
            fds.as_mut_ptr().cast(),
            size,
        )
    };
    if n < 0 || n == 0 && info.pbi_nfiles != 0 {
        report
            .errors
            .push(enumeration_error(Some(pid), io::Error::last_os_error()));
        return report;
    }
    if n == size {
        report.errors.push(enumeration_error(
            Some(pid),
            io::ErrorKind::InvalidData.into(),
        ));
    }
    fds.truncate(n as usize / size_of::<libc::proc_fdinfo>());
    for fd in fds {
        if fd.proc_fdtype != 2 {
            continue;
        }
        let mut socket = SocketFDInfo::default();
        let size = size_of::<SocketFDInfo>() as i32;
        let n = unsafe {
            proc_pidfdinfo(
                pid as i32,
                fd.proc_fd,
                3,
                (&mut socket as *mut SocketFDInfo).cast(),
                size,
            )
        };
        if n != size {
            let e = io::Error::last_os_error();
            if !matches!(
                e.raw_os_error(),
                Some(libc::EBADF | libc::ESRCH | libc::ENOENT)
            ) {
                report.errors.push(enumeration_error(Some(pid), e));
            }
            continue;
        }
        let socket = socket.psi;
        if !matches!(socket.soi_family, libc::AF_INET | libc::AF_INET6) {
            continue;
        }
        // Read only the active union member after verifying the kernel's discriminator.
        let (kind, inet) = match (socket.soi_kind, socket.soi_protocol) {
            (2, libc::IPPROTO_TCP) => {
                let tcp = unsafe { socket.soi_proto.pri_tcp };
                if tcp.tcpsi_state != 1 {
                    continue;
                }
                (Protocol::Tcp, tcp.tcpsi_ini)
            }
            (1, libc::IPPROTO_UDP) => (Protocol::Udp, unsafe { socket.soi_proto.pri_in }),
            _ => continue,
        };
        let port = u16::from_be(inet.insi_lport as u16);
        if !protocol.accepts(kind) || !ports.contains(&port) {
            continue;
        }
        let address = unsafe {
            if socket.soi_family == libc::AF_INET {
                Ipv4Addr::from(inet.insi_laddr.ina_46.i46a_addr4.s_addr.to_ne_bytes()).to_string()
            } else {
                let ip = Ipv6Addr::from(inet.insi_laddr.ina_6.s6_addr);
                if ip.is_unicast_link_local() && inet.insi_v6.in6_ifindex != 0 {
                    format!("{ip}%{}", inet.insi_v6.in6_ifindex)
                } else {
                    ip.to_string()
                }
            }
        };
        report.results.push(Entry {
            pid: Some(pid),
            name: Some(name(info)),
            protocol: kind,
            address,
            port,
            status: None,
            identity: Some(Identity {
                birth: birth(info),
                socket: inet.insi_gencnt,
            }),
        });
    }
    report
}

impl Backend for Native {
    fn snapshot(&mut self, ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
        let mut report = Report::default();
        let pids = match pids_by_type(ProcFilter::All) {
            Ok(p) => p,
            Err(e) => {
                report.errors.push(enumeration_error(None, e));
                return report;
            }
        };
        for pid in pids.into_iter().filter(|pid| *pid != 0) {
            if runtime::cancelled() {
                report.errors.push(Failure::new(
                    Code::Cancelled,
                    "Port enumeration interrupted; results are incomplete.",
                ));
                break;
            }
            let info = match process(pid) {
                Ok(Some(p)) if p.pbi_status != libc::SZOMB => p,
                Ok(_) => continue,
                Err(e) => {
                    report.errors.push(enumeration_error(Some(pid), e));
                    continue;
                }
            };
            let mut current = rows(pid, &info, ports, protocol);
            if !current.results.is_empty()
                && !matches!(process(pid), Ok(Some(p)) if birth(&p) == birth(&info))
            {
                for row in &mut current.results {
                    row.identity = None;
                }
                current.errors.push(
                    Failure::new(
                        Code::IdentityUnverifiable,
                        "Process changed during enumeration; ownership is unverified.",
                    )
                    .pid(pid),
                );
            }
            report.results.extend(current.results);
            report.errors.extend(current.errors);
        }
        report
    }

    fn terminate(&mut self, pid: u32, expected: &[Entry]) -> Result<bool> {
        let identity = expected[0].identity.unwrap();
        let before = process(pid).map_err(|_| {
            Failure::new(
                Code::IdentityUnverifiable,
                "Cannot verify process identity; no signal was sent.",
            )
        })?;
        let Some(before) = before.filter(|p| p.pbi_status != libc::SZOMB) else {
            return Ok(false);
        };
        if birth(&before) != identity.birth {
            return Err(Failure::new(
                Code::IdentityChanged,
                "Process identity changed; no signal was sent.",
            ));
        }
        let current = rows(
            pid,
            &before,
            &expected.iter().map(|e| e.port).collect(),
            Protocol::All,
        );
        if !current.errors.is_empty() {
            return Err(Failure::new(
                Code::IdentityUnverifiable,
                "Socket ownership could not be fully rechecked; no signal was sent.",
            ));
        }
        if !current.results.iter().any(|now| {
            expected.iter().any(|e| {
                e.identity == now.identity
                    && e.port == now.port
                    && e.protocol == now.protocol
                    && e.address == now.address
            })
        }) {
            return Err(Failure::new(
                Code::OwnershipChanged,
                "Process no longer owns an originally observed socket; no signal was sent.",
            ));
        }
        let after = process(pid).map_err(|_| {
            Failure::new(
                Code::IdentityUnverifiable,
                "Cannot recheck process identity; no signal was sent.",
            )
        })?;
        let Some(after) = after.filter(|p| p.pbi_status != libc::SZOMB) else {
            return Ok(false);
        };
        if birth(&after) != identity.birth {
            return Err(Failure::new(
                Code::IdentityChanged,
                "Process identity changed; no signal was sent.",
            ));
        }
        runtime::check_cancelled()?;
        if unsafe { libc::kill(pid as i32, libc::SIGKILL) } == 0 {
            Ok(true)
        } else {
            let e = io::Error::last_os_error();
            if e.raw_os_error() == Some(libc::ESRCH) {
                Ok(false)
            } else {
                Err(Failure::io(&e))
            }
        }
    }

    fn alive(&mut self, pid: u32, expected: u128) -> Result<bool> {
        Ok(process(pid)
            .map_err(|e| Failure::io(&e))?
            .is_some_and(|p| birth(&p) == expected && p.pbi_status != libc::SZOMB))
    }
}
