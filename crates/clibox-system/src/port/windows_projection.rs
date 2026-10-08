//! Project native Windows owner-table fields without acquiring process access.

use std::{
    collections::BTreeSet,
    net::{Ipv4Addr, Ipv6Addr},
};

use super::{Code, Entry, Failure, Protocol, Report};

pub(super) enum Address {
    V4(u32),
    V6([u8; 16], u32),
}

pub(super) fn add_endpoint(
    report: &mut Report,
    ports: &BTreeSet<u16>,
    pid: u32,
    protocol: Protocol,
    address: Address,
    raw_port: u32,
) {
    let port = u16::from_be(raw_port as u16);
    if !ports.contains(&port) {
        return;
    }
    let address = match address {
        Address::V4(raw) => Ipv4Addr::from(raw.to_ne_bytes()).to_string(),
        Address::V6(bytes, scope) => {
            let ip = Ipv6Addr::from(bytes);
            if scope == 0 {
                ip.to_string()
            } else {
                format!("{ip}%{scope}")
            }
        }
    };
    report.results.push(Entry {
        pid: (pid != 0).then_some(pid),
        name: None,
        protocol,
        address,
        port,
        status: None,
        identity: None,
    });
}

pub(super) fn diagnose_unavailable_udp_owners(report: &mut Report) {
    // Windows UDP owner tables explicitly use PID zero for unavailable owner
    // information. Preserve the endpoint without inventing an access failure.
    for row in &report.results {
        if row.protocol != Protocol::Udp || row.pid.is_some() {
            continue;
        }
        report.errors.push(
            Failure::new(
                Code::IdentityUnverifiable,
                "Socket owner is unavailable; port enumeration is incomplete.",
            )
            .port(row.port),
        );
    }
}
