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
        results: vec![row(10, 80), row(10, 81)],
        errors: vec![Failure::new(Code::PermissionDenied, "Incomplete.").port(80)],
    };
    let mut out = vec![];
    write_report(&report, false, true, false, &mut out).unwrap();
    assert_eq!(out, b"10\n");
    out.clear();
    write_report(&report, true, false, false, &mut out).unwrap();
    let json: serde_json::Value = serde_json::from_slice(&out).unwrap();
    assert!(json["results"][0].get("identity").is_none());
    assert!(json["results"][0].get("status").is_none());
    assert_eq!(json["errors"][0]["code"], "permission-denied");
    out.clear();
    write_report(&report, false, false, false, &mut out).unwrap();
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
