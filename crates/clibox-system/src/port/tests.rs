use super::*;
#[derive(Default)]
struct Fake {
    outcomes: BTreeMap<u32, Result<bool>>,
    live: BTreeSet<u32>,
    calls: Vec<u32>,
}
impl Backend for Fake {
    fn snapshot(&mut self, _: &BTreeSet<u16>, _: Protocol) -> Report {
        unreachable!()
    }

    fn terminate(&mut self, pid: u32, _: &[Entry]) -> Result<bool> {
        self.calls.push(pid);
        self.outcomes.remove(&pid).unwrap_or(Ok(true))
    }

    fn alive(&mut self, pid: u32, _: u128) -> Result<bool> {
        Ok(self.live.contains(&pid))
    }
}
#[derive(Default)]
struct Time(Duration);
impl Clock for Time {
    fn elapsed(&self) -> Duration {
        self.0
    }

    fn pause(&mut self) {
        self.0 += Duration::from_millis(100);
    }
}
fn row(pid: u32, port: u16) -> Entry {
    Entry {
        pid: Some(pid),
        name: Some("fixture".into()),
        protocol: Protocol::Tcp,
        address: "127.0.0.1".into(),
        port,
        status: None,
        identity: Some(Identity {
            birth: 123,
            socket: port as u64,
        }),
    }
}
#[test]
fn partial_failures_deduplicate_pids_and_share_one_deadline() {
    let mut report = Report {
        results: vec![
            row(1, 3000),
            row(1, 3001),
            row(2, 3000),
            row(3, 3000),
            row(4, 3000),
            row(5, 3000),
            row(6, 3000),
        ],
        errors: vec![],
    };
    let mut backend = Fake {
        outcomes: [
            (2, Ok(false)),
            (3, Err(Failure::new(Code::PermissionDenied, "Denied."))),
            (4, Err(Failure::new(Code::IdentityChanged, "Changed."))),
        ]
        .into(),
        live: [5, 6].into(),
        ..Default::default()
    };
    let mut time = Time::default();
    kill(&mut report, &mut backend, &mut time);
    assert_eq!(backend.calls, vec![1, 2, 3, 4, 5, 6]);
    assert_eq!(
        report
            .results
            .iter()
            .map(|r| r.status.unwrap())
            .collect::<Vec<_>>(),
        vec![
            Status::Killed,
            Status::Killed,
            Status::AlreadyExited,
            Status::Failed,
            Status::Skipped,
            Status::Failed,
            Status::Failed
        ]
    );
    assert_eq!(time.0, Duration::from_secs(5));
    assert_eq!(report.errors.len(), 4);
}
#[test]
fn unverified_or_changed_ownership_never_chases_replacements() {
    let mut unknown = row(7, 8000);
    unknown.pid = None;
    unknown.identity = None;
    let mut changed = row(8, 8000);
    changed.identity = None;
    let mut report = Report {
        results: vec![unknown, changed, row(9, 8000)],
        errors: vec![],
    };
    let mut backend = Fake {
        outcomes: [(9, Err(Failure::new(Code::OwnershipChanged, "Changed.")))].into(),
        ..Default::default()
    };
    kill(&mut report, &mut backend, &mut Time::default());
    assert_eq!(backend.calls, vec![9]);
    assert!(report
        .results
        .iter()
        .all(|r| r.status == Some(Status::Skipped)));
    assert_eq!(report.errors.len(), 3);
}
#[test]
fn output_contracts_and_decimal_validation() {
    for raw in ["", "0", "65536", "+80", "-1", "1-3", "http", "１２"] {
        assert!(parse_port(raw).is_err());
    }
    assert_eq!(parse_port("00080"), Ok(80));
    let report = Report {
        results: vec![row(10, 80), row(2, 82), row(10, 81)],
        errors: vec![Failure::new(Code::PermissionDenied, "Incomplete.").port(80)],
    };
    let mut out = vec![];
    write_report(&report, OutputMode::Pids, false, &mut out).unwrap();
    assert_eq!(out, b"2\n10\n");
    out.clear();
    for terminate in [false, true] {
        write_report(&report, OutputMode::Quiet, terminate, &mut out).unwrap();
        assert!(out.is_empty());
        assert!(!report.errors.is_empty());
    }
    out.clear();
    write_report(&report, OutputMode::Json, false, &mut out).unwrap();
    let json: serde_json::Value = serde_json::from_slice(&out).unwrap();
    assert!(json["results"][0].get("identity").is_none());
    assert!(json["results"][0].get("status").is_none());
    assert_eq!(json["errors"][0]["code"], "permission-denied");
    out.clear();
    write_report(&report, OutputMode::Human, false, &mut out).unwrap();
    assert!(String::from_utf8(out)
        .unwrap()
        .starts_with("PID\tNAME\tPROTOCOL\tADDRESS\tPORT\n"));
}
#[test]
fn empty_kill_is_successful_noop() {
    let mut b = Fake::default();
    let mut r = Report::default();
    kill(&mut r, &mut b, &mut Time::default());
    assert!(b.calls.is_empty());
    assert!(r.errors.is_empty());
}

#[test]
fn native_owner_fixture() {
    if std::env::var_os("CLIBOX_PORT_FIXTURE").is_none() {
        return;
    }
    let socket = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    println!("OWNER_PORT={}", socket.local_addr().unwrap().port());
    std::io::stdout().flush().unwrap();
    loop {
        std::thread::sleep(Duration::from_millis(50));
    }
}

#[test]
fn native_termination_rechecks_birth_and_endpoint_before_killing_owned_child() {
    use std::{
        io::{BufRead, BufReader},
        process::{Child, Command, Stdio},
    };
    struct Fixture(Child);
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
    let mut child = Fixture(
        Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "port::tests::native_owner_fixture",
                "--nocapture",
            ])
            .env("CLIBOX_PORT_FIXTURE", "1")
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap(),
    );
    let mut input = BufReader::new(child.0.stdout.take().unwrap());
    let port = loop {
        let mut line = String::new();
        assert_ne!(input.read_line(&mut line).unwrap(), 0);
        if let Some((_, port)) = line.trim().split_once("OWNER_PORT=") {
            break port.parse::<u16>().unwrap();
        }
    };
    let pid = child.0.id();
    let mut backend = Native::default();
    let report = backend.snapshot(&[port].into(), Protocol::Tcp);
    let rows: Vec<_> = report
        .results
        .into_iter()
        .filter(|e| e.pid == Some(pid))
        .collect();
    assert!(!rows.is_empty());
    assert!(rows.iter().all(|e| e.identity.is_some()));
    let mut changed = rows.clone();
    for row in &mut changed {
        row.identity.as_mut().unwrap().birth += 1;
    }
    assert_eq!(
        backend.terminate(pid, &changed).unwrap_err().code,
        Code::IdentityChanged
    );
    assert!(child.0.try_wait().unwrap().is_none());
    let mut changed = rows.clone();
    for row in &mut changed {
        row.address = "192.0.2.1".into();
    }
    assert_eq!(
        backend.terminate(pid, &changed).unwrap_err().code,
        Code::OwnershipChanged
    );
    assert!(child.0.try_wait().unwrap().is_none());
    assert!(backend.terminate(pid, &rows).unwrap());
    let deadline = Instant::now() + Duration::from_secs(5);
    while backend.alive(pid, rows[0].identity.unwrap().birth).unwrap() {
        assert!(Instant::now() < deadline);
        std::thread::sleep(Duration::from_millis(20));
    }
    child.0.wait().unwrap();
    assert!(!backend.terminate(pid, &rows).unwrap());
}

fn windows_udp_projection(addresses: Vec<(windows_projection::Address, u32)>) -> Report {
    let mut report = Report::default();
    for (address, pid) in addresses {
        windows_projection::add_endpoint(
            &mut report,
            &[5353].into(),
            pid,
            Protocol::Udp,
            address,
            u32::from(5353u16.to_be()),
        );
    }
    windows_projection::diagnose_unavailable_udp_owners(&mut report);
    report.normalize();
    report
}

#[test]
fn windows_udp_unavailable_owners_remain_partial_in_every_list_mode() {
    use windows_projection::Address;
    for (address, expected) in [
        (Address::V4(u32::from_ne_bytes([127, 0, 0, 1])), "127.0.0.1"),
        (
            Address::V6(std::net::Ipv6Addr::LOCALHOST.octets(), 0),
            "::1",
        ),
    ] {
        let report = windows_udp_projection(vec![(address, 0)]);
        assert_eq!(report.results.len(), 1);
        let entry = &report.results[0];
        assert_eq!(entry.address, expected);
        assert_eq!(entry.port, 5353);
        assert!(entry.pid.is_none() && entry.name.is_none() && entry.identity.is_none());
        assert_eq!(report.errors.len(), 1);
        let error = &report.errors[0];
        assert_eq!(error.code, Code::IdentityUnverifiable);
        assert_eq!(error.port, Some(5353));
        assert_eq!(error.pid, None);
        assert_eq!(
            error.message,
            "Socket owner is unavailable; port enumeration is incomplete."
        );
        for mode in [
            OutputMode::Human,
            OutputMode::Json,
            OutputMode::Quiet,
            OutputMode::Pids,
        ] {
            let mut out = Vec::new();
            assert_eq!(finish_report(&report, mode, false, &mut out).unwrap(), 1);
            match mode {
                OutputMode::Quiet | OutputMode::Pids => assert!(out.is_empty()),
                OutputMode::Human => assert!(String::from_utf8(out).unwrap().contains(expected)),
                OutputMode::Json => {
                    let json: serde_json::Value = serde_json::from_slice(&out).unwrap();
                    assert!(json["results"][0]["pid"].is_null());
                    assert!(json["results"][0]["name"].is_null());
                    assert_eq!(json["errors"][0]["code"], "identity-unverifiable");
                    assert_eq!(json["errors"][0]["port"], 5353);
                }
            }
        }
        let mut report = report;
        let mut backend = Fake::default();
        kill(&mut report, &mut backend, &mut Time::default());
        assert!(backend.calls.is_empty());
        assert_eq!(report.results[0].status, Some(Status::Skipped));
    }
}

#[test]
fn windows_udp_mixed_empty_and_known_owner_controls() {
    use windows_projection::Address;
    for unknown in [false, true] {
        let mut rows = vec![(Address::V4(u32::from_ne_bytes([127, 0, 0, 1])), 42)];
        if unknown {
            rows.push((Address::V6(std::net::Ipv6Addr::LOCALHOST.octets(), 0), 0));
        }
        let mut report = windows_udp_projection(rows);
        // Supply the successful metadata result that the native adapter obtains
        // for a known PID; unknown PIDs never enter that process lookup.
        let known = report
            .results
            .iter_mut()
            .find(|r| r.pid == Some(42))
            .unwrap();
        known.name = Some("fixture".into());
        known.identity = Some(Identity {
            birth: 123,
            socket: 0,
        });
        assert_eq!(report.results.len(), if unknown { 2 } else { 1 });
        assert_eq!(report.errors.len(), usize::from(unknown));
        for mode in [
            OutputMode::Human,
            OutputMode::Json,
            OutputMode::Quiet,
            OutputMode::Pids,
        ] {
            let mut out = Vec::new();
            assert_eq!(
                finish_report(&report, mode, false, &mut out).unwrap(),
                i32::from(unknown)
            );
            if matches!(mode, OutputMode::Pids) {
                assert_eq!(out, b"42\n");
            }
            if matches!(mode, OutputMode::Quiet) {
                assert!(out.is_empty());
            }
        }
        let mut backend = Fake::default();
        kill(&mut report, &mut backend, &mut Time::default());
        assert_eq!(backend.calls, vec![42]);
        assert_eq!(
            report
                .results
                .iter()
                .find(|r| r.pid == Some(42))
                .unwrap()
                .status,
            Some(Status::Killed)
        );
    }
    let report = windows_udp_projection(vec![]);
    assert!(report.results.is_empty() && report.errors.is_empty());
    for mode in [
        OutputMode::Human,
        OutputMode::Json,
        OutputMode::Quiet,
        OutputMode::Pids,
    ] {
        assert_eq!(
            finish_report(&report, mode, false, &mut Vec::new()).unwrap(),
            0
        );
    }
}

#[test]
fn windows_projection_filters_ports_and_preserves_raw_revalidation_rows() {
    use windows_projection::{add_endpoint, diagnose_unavailable_udp_owners, Address};
    let mut report = Report::default();
    add_endpoint(
        &mut report,
        &[5353].into(),
        0,
        Protocol::Udp,
        Address::V4(u32::from_ne_bytes([127, 0, 0, 1])),
        u32::from(9999u16.to_be()),
    );
    assert!(report.results.is_empty() && report.errors.is_empty());
    add_endpoint(
        &mut report,
        &[5353].into(),
        0,
        Protocol::Udp,
        Address::V6(std::net::Ipv6Addr::LOCALHOST.octets(), 7),
        u32::from(5353u16.to_be()),
    );
    assert_eq!(report.results[0].address, "::1%7");
    assert!(
        report.errors.is_empty(),
        "raw revalidation rows must not block unrelated verified owners"
    );
    diagnose_unavailable_udp_owners(&mut report);
    assert_eq!(report.errors.len(), 1);
    let mut tcp = Report::default();
    add_endpoint(
        &mut tcp,
        &[5353].into(),
        0,
        Protocol::Tcp,
        Address::V4(u32::from_ne_bytes([127, 0, 0, 1])),
        u32::from(5353u16.to_be()),
    );
    diagnose_unavailable_udp_owners(&mut tcp);
    assert!(
        tcp.errors.is_empty(),
        "UDP owner policy must not expand TCP behavior"
    );
}

#[test]
fn duplicate_endpoints_keep_all_original_sockets_until_termination() {
    struct Originals {
        survivor: Option<u64>,
        birth: u128,
        expected: Vec<u64>,
        signals: usize,
    }
    impl Backend for Originals {
        fn snapshot(&mut self, _: &BTreeSet<u16>, _: Protocol) -> Report {
            unreachable!()
        }

        fn terminate(&mut self, _: u32, rows: &[Entry]) -> Result<bool> {
            self.expected = rows.iter().map(|r| r.identity.unwrap().socket).collect();
            if rows.iter().any(|r| r.identity.unwrap().birth != self.birth) {
                return Err(Failure::new(Code::IdentityChanged, "Changed."));
            }
            if !self.survivor.is_some_and(|id| self.expected.contains(&id)) {
                return Err(Failure::new(Code::OwnershipChanged, "Changed."));
            }
            self.signals += 1;
            Ok(true)
        }

        fn alive(&mut self, _: u32, _: u128) -> Result<bool> {
            Ok(false)
        }
    }
    for order in [[100, 200], [200, 100]] {
        for (survivor, birth, authorized) in [
            (Some(100), 123, true),
            (Some(200), 123, true),
            (None, 123, false),
            (Some(300), 123, false),
            (Some(200), 124, false),
        ] {
            let mut report = Report {
                results: order
                    .into_iter()
                    .map(|id| {
                        let mut r = row(7, 8000);
                        r.identity.as_mut().unwrap().socket = id;
                        r
                    })
                    .collect(),
                errors: vec![],
            };
            let mut backend = Originals {
                survivor,
                birth,
                expected: vec![],
                signals: 0,
            };
            settle_snapshot(&mut report, true, &mut backend, &mut Time::default());
            assert_eq!(backend.expected, order);
            assert_eq!(backend.signals, usize::from(authorized));
            assert_eq!(report.results.len(), 1);
            assert_eq!(
                report.results[0].status,
                Some(if authorized {
                    Status::Killed
                } else {
                    Status::Skipped
                })
            );
            assert_eq!(report.errors.is_empty(), authorized);
            let mut out = vec![];
            write_report(&report, OutputMode::Json, true, &mut out).unwrap();
            let json: serde_json::Value = serde_json::from_slice(&out).unwrap();
            assert_eq!(json["results"].as_array().unwrap().len(), 1);
            assert!(json["results"][0].get("identity").is_none());
        }
    }
}

#[test]
fn listing_deduplicates_without_termination() {
    let mut report = Report {
        results: vec![row(7, 8000), row(7, 8000)],
        errors: vec![],
    };
    let mut backend = Fake::default();
    settle_snapshot(&mut report, false, &mut backend, &mut Time::default());
    assert_eq!(report.results.len(), 1);
    assert!(backend.calls.is_empty());
}
