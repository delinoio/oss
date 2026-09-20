use std::{
    collections::{BTreeMap, BTreeSet},
    io,
    mem::{size_of, zeroed},
    net::{Ipv4Addr, Ipv6Addr},
    ptr,
};

use windows_sys::Win32::{
    Foundation::*,
    NetworkManagement::IpHelper::*,
    Networking::WinSock::{AF_INET, AF_INET6},
    System::Threading::*,
};

use super::*;

#[derive(Default)]
pub struct Native {}
struct Handle(HANDLE);
impl Drop for Handle {
    fn drop(&mut self) {
        unsafe {
            CloseHandle(self.0);
        }
    }
}

fn handle(pid: u32, terminate: bool) -> io::Result<Option<Handle>> {
    let value = unsafe {
        OpenProcess(
            PROCESS_QUERY_LIMITED_INFORMATION
                | PROCESS_SYNCHRONIZE
                | if terminate { PROCESS_TERMINATE } else { 0 },
            0,
            pid,
        )
    };
    if value.is_null() {
        let e = io::Error::last_os_error();
        if e.raw_os_error() == Some(ERROR_INVALID_PARAMETER as i32) {
            Ok(None)
        } else {
            Err(e)
        }
    } else {
        Ok(Some(Handle(value)))
    }
}
fn birth(handle: &Handle) -> io::Result<u128> {
    let mut creation = unsafe { zeroed::<FILETIME>() };
    let mut exit = creation;
    let mut kernel = creation;
    let mut user = creation;
    if unsafe { GetProcessTimes(handle.0, &mut creation, &mut exit, &mut kernel, &mut user) } == 0 {
        return Err(io::Error::last_os_error());
    }
    Ok((u128::from(creation.dwHighDateTime) << 32) | u128::from(creation.dwLowDateTime))
}
fn name(handle: &Handle) -> io::Result<String> {
    let mut buffer = vec![0u16; 32768];
    let mut len = buffer.len() as u32;
    if unsafe { QueryFullProcessImageNameW(handle.0, 0, buffer.as_mut_ptr(), &mut len) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let text = String::from_utf16_lossy(&buffer[..len as usize]);
    Ok(text.rsplit(['\\', '/']).next().unwrap_or("").into())
}
fn alive_handle(handle: &Handle) -> io::Result<bool> {
    match unsafe { WaitForSingleObject(handle.0, 0) } {
        WAIT_OBJECT_0 => Ok(false),
        WAIT_TIMEOUT => Ok(true),
        _ => Err(io::Error::last_os_error()),
    }
}

fn table<T: Copy>(family: u16, protocol: Protocol) -> io::Result<Vec<T>> {
    let mut bytes = 0u32;
    let call = |buffer: *mut std::ffi::c_void, bytes: &mut u32| unsafe {
        if protocol == Protocol::Tcp {
            GetExtendedTcpTable(
                buffer,
                bytes,
                0,
                family as u32,
                TCP_TABLE_OWNER_PID_LISTENER,
                0,
            )
        } else {
            GetExtendedUdpTable(buffer, bytes, 0, family as u32, UDP_TABLE_OWNER_PID, 0)
        }
    };
    let status = call(ptr::null_mut(), &mut bytes);
    if status != ERROR_INSUFFICIENT_BUFFER && status != NO_ERROR {
        return Err(io::Error::from_raw_os_error(status as i32));
    }
    if bytes < 4 {
        return Err(io::ErrorKind::InvalidData.into());
    }
    // Aligned backing storage for either native table; the second call is the
    // requested enumeration, not an automatic retry if the table subsequently
    // grows.
    let mut storage = vec![0u64; (bytes as usize).div_ceil(8)];
    let status = call(storage.as_mut_ptr().cast(), &mut bytes);
    if status != NO_ERROR {
        return Err(io::Error::from_raw_os_error(status as i32));
    }
    let count = unsafe { ptr::read_unaligned(storage.as_ptr().cast::<u32>()) } as usize;
    if count
        .checked_mul(size_of::<T>())
        .and_then(|n| n.checked_add(4))
        .is_none_or(|n| n > bytes as usize || n > storage.len() * 8)
    {
        return Err(io::ErrorKind::InvalidData.into());
    }
    let rows = unsafe { storage.as_ptr().cast::<u8>().add(4).cast::<T>() };
    Ok((0..count)
        .map(|i| unsafe { ptr::read_unaligned(rows.add(i)) })
        .collect())
}

fn endpoints(ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
    let mut report = Report::default();
    let mut add = |pid: u32, kind, address, raw_port| {
        let port = u16::from_be(raw_port as u16);
        if ports.contains(&port) {
            report.results.push(Entry {
                pid: if pid == 0 { None } else { Some(pid) },
                name: None,
                protocol: kind,
                address,
                port,
                status: None,
                identity: None,
            });
        }
    };
    let mut errors = Vec::new();
    if protocol.accepts(Protocol::Tcp) {
        match table::<MIB_TCPROW_OWNER_PID>(AF_INET, Protocol::Tcp) {
            Ok(rows) => {
                for r in rows {
                    add(
                        r.dwOwningPid,
                        Protocol::Tcp,
                        Ipv4Addr::from(r.dwLocalAddr.to_ne_bytes()).to_string(),
                        r.dwLocalPort,
                    );
                }
            }
            Err(e) => errors.push(enumeration_error(None, e)),
        }
        match table::<MIB_TCP6ROW_OWNER_PID>(AF_INET6, Protocol::Tcp) {
            Ok(rows) => {
                for r in rows {
                    add(
                        r.dwOwningPid,
                        Protocol::Tcp,
                        ipv6(r.ucLocalAddr, r.dwLocalScopeId),
                        r.dwLocalPort,
                    );
                }
            }
            Err(e) => errors.push(enumeration_error(None, e)),
        }
    }
    if protocol.accepts(Protocol::Udp) {
        match table::<MIB_UDPROW_OWNER_PID>(AF_INET, Protocol::Udp) {
            Ok(rows) => {
                for r in rows {
                    add(
                        r.dwOwningPid,
                        Protocol::Udp,
                        Ipv4Addr::from(r.dwLocalAddr.to_ne_bytes()).to_string(),
                        r.dwLocalPort,
                    );
                }
            }
            Err(e) => errors.push(enumeration_error(None, e)),
        }
        match table::<MIB_UDP6ROW_OWNER_PID>(AF_INET6, Protocol::Udp) {
            Ok(rows) => {
                for r in rows {
                    add(
                        r.dwOwningPid,
                        Protocol::Udp,
                        ipv6(r.ucLocalAddr, r.dwLocalScopeId),
                        r.dwLocalPort,
                    );
                }
            }
            Err(e) => errors.push(enumeration_error(None, e)),
        }
    }
    report.errors = errors;
    report
}
fn ipv6(bytes: [u8; 16], scope: u32) -> String {
    let ip = Ipv6Addr::from(bytes);
    if scope == 0 {
        ip.to_string()
    } else {
        format!("{ip}%{scope}")
    }
}

impl Backend for Native {
    fn snapshot(&mut self, ports: &BTreeSet<u16>, protocol: Protocol) -> Report {
        // Endpoint tables contain numeric PIDs, not process handles. A PID born
        // after enumeration began could be a replacement for a stale table row.
        let began = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() / 100 + 116_444_736_000_000_000)
            .unwrap_or(0);
        let mut report = endpoints(ports, protocol);
        let mut processes = BTreeMap::new();
        for pid in report
            .results
            .iter()
            .filter_map(|e| e.pid)
            .collect::<BTreeSet<_>>()
        {
            match handle(pid, false) {
                Ok(Some(h)) => {
                    let stamp = match birth(&h) {
                        Ok(b) if b <= began => Some(b),
                        Ok(_) => {
                            report.errors.push(
                                Failure::new(
                                    Code::IdentityUnverifiable,
                                    "Process began after socket enumeration; ownership is \
                                     unverified.",
                                )
                                .pid(pid),
                            );
                            None
                        }
                        Err(e) => {
                            report.errors.push(enumeration_error(Some(pid), e));
                            None
                        }
                    };
                    let name = match name(&h) {
                        Ok(n) => Some(n),
                        Err(e) => {
                            report.errors.push(enumeration_error(Some(pid), e));
                            None
                        }
                    };
                    processes.insert(pid, (stamp, name));
                }
                Ok(None) => report.errors.push(
                    Failure::new(
                        Code::IdentityUnverifiable,
                        "Process exited during enumeration; ownership is unverified.",
                    )
                    .pid(pid),
                ),
                Err(e) => report.errors.push(enumeration_error(Some(pid), e)),
            }
        }
        for row in &mut report.results {
            if let Some((stamp, name)) = row.pid.and_then(|pid| processes.get(&pid)) {
                row.name = name.clone();
                row.identity = stamp.map(|birth| Identity { birth, socket: 0 });
            }
        }
        report
    }

    fn terminate(&mut self, pid: u32, expected: &[Entry]) -> Result<bool> {
        let Some(h) = handle(pid, true).map_err(|e| Failure::io(&e))? else {
            return Ok(false);
        };
        if !alive_handle(&h).map_err(|e| Failure::io(&e))? {
            return Ok(false);
        }
        let identity = birth(&h).map_err(|_| {
            Failure::new(
                Code::IdentityUnverifiable,
                "Cannot verify process creation time; no termination was requested.",
            )
        })?;
        if expected[0].identity.unwrap().birth != identity {
            return Err(Failure::new(
                Code::IdentityChanged,
                "Process identity changed; no termination was requested.",
            ));
        }
        let current = endpoints(&expected.iter().map(|r| r.port).collect(), Protocol::All);
        if !current.errors.is_empty() {
            return Err(Failure::new(
                Code::IdentityUnverifiable,
                "Socket ownership could not be rechecked; no termination was requested.",
            ));
        }
        if !current.results.iter().any(|r| {
            r.pid == Some(pid)
                && expected
                    .iter()
                    .any(|e| e.protocol == r.protocol && e.port == r.port && e.address == r.address)
        }) {
            if !alive_handle(&h).map_err(|e| Failure::io(&e))? {
                return Ok(false);
            }
            return Err(Failure::new(
                Code::OwnershipChanged,
                "Process no longer owns an originally observed endpoint; no termination was \
                 requested.",
            ));
        }
        runtime::check_cancelled()?;
        if unsafe { TerminateProcess(h.0, 1) } == 0 {
            if !alive_handle(&h).map_err(|e| Failure::io(&e))? {
                Ok(false)
            } else {
                Err(Failure::io(&io::Error::last_os_error()))
            }
        } else {
            Ok(true)
        }
    }

    fn alive(&mut self, pid: u32, stamp: u128) -> Result<bool> {
        let Some(h) = handle(pid, false).map_err(|e| Failure::io(&e))? else {
            return Ok(false);
        };
        Ok(birth(&h).map_err(|e| Failure::io(&e))? == stamp
            && alive_handle(&h).map_err(|e| Failure::io(&e))?)
    }
}
