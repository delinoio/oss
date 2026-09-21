use std::{
    io::{BufRead, BufReader, Write},
    net::{TcpListener, UdpSocket},
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

use serde_json::{json, Value};
const CLI: &str = env!("CARGO_BIN_EXE_clibox");
const MARKER: &str = "CLIBOX_FIXTURE=";

#[test]
fn environment_delegates_transform_commands_with_platform_variable_conversion() {
    for (arguments, expected) in [
        (
            vec![
                "text", "replace", "(hello)", "$1 🦀", "--regex", "--text", "hello",
            ],
            // Windows env run applies cross-env's command-variable conversion,
            // including numeric references; direct text replace keeps captures.
            if cfg!(windows) { " 🦀" } else { "hello 🦀" },
        ),
        (vec!["text", "replace", "hello", "", "--text", "hello"], ""),
        (
            vec![
                "time", "format", "-1", "--from", "unix-ms", "--to", "unix-s",
            ],
            "-1\n",
        ),
    ] {
        let output = Command::new(CLI)
            .args(["env", "run", "--"])
            .arg(CLI)
            .args(arguments)
            .env_remove("1")
            .output()
            .unwrap();
        assert!(output.status.success(), "{:?}", output.stderr);
        assert_eq!(output.stdout, expected.as_bytes());
        assert!(output.stderr.is_empty());
    }
    let output = Command::new(CLI)
        .args(["env", "run", "--"])
        .arg(CLI)
        .args([
            "hash",
            "verify",
            "SECRET_INVALID_DIGEST",
            "--text",
            "SECRET_INPUT",
        ])
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());
    assert!(!String::from_utf8_lossy(&output.stderr).contains("SECRET_"));
}

// The integration-test executable is also a disposable process fixture. It is
// never installed or shipped as an extra public executable and uses no GUI
// APIs.
#[test]
fn fixture() {
    let Ok(mode) = std::env::var("CLIBOX_TEST_FIXTURE") else {
        return;
    };
    if mode == "inspect" {
        println!(
            "{MARKER}{}",
            json!({"args": std::env::args().skip(1).collect::<Vec<_>>(), "value": std::env::var("CLIBOX_TEST_VALUE").ok(), "parent": std::env::var("CLIBOX_TEST_PARENT").ok(), "ca_file": std::env::var("SSL_CERT_FILE").ok(), "ca_dir": std::env::var("SSL_CERT_DIR").ok(), "cwd": std::env::current_dir().unwrap(), "pid": std::process::id()})
        );
        eprintln!("FIXTURE_STDERR");
        return;
    }
    if mode == "exit" {
        std::process::exit(37);
    }
    let mut tcp = Vec::new();
    let mut udp = Vec::new();
    let mut endpoints = Vec::new();
    if mode == "serve" {
        for address in ["127.0.0.1:0", "[::1]:0"] {
            if let Ok(socket) = TcpListener::bind(address) {
                endpoints.push(json!({"protocol": "tcp", "port": socket.local_addr().unwrap().port(), "address": socket.local_addr().unwrap().ip().to_string()}));
                tcp.push(socket);
            }
            if let Ok(socket) = UdpSocket::bind(address) {
                endpoints.push(json!({"protocol": "udp", "port": socket.local_addr().unwrap().port(), "address": socket.local_addr().unwrap().ip().to_string()}));
                udp.push(socket);
            }
        }
    }
    println!(
        "{MARKER}{}",
        json!({"pid": std::process::id(), "endpoints": endpoints})
    );
    std::io::stdout().flush().unwrap();
    loop {
        std::thread::sleep(Duration::from_millis(50));
    }
}

fn helper(command: &mut Command, mode: &str) {
    command
        .args(["--exact", "fixture", "--nocapture"])
        .env("CLIBOX_TEST_FIXTURE", mode);
}
fn marker(output: &[u8]) -> Value {
    String::from_utf8_lossy(output)
        .lines()
        .find_map(|line| line.split_once(MARKER).map(|(_, json)| json))
        .map(|s| serde_json::from_str(s).unwrap())
        .expect("fixture marker missing")
}
struct Running {
    child: Child,
    info: Value,
}
impl Drop for Running {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
fn ready(mut child: Child) -> Running {
    let mut reader = BufReader::new(child.stdout.take().unwrap());
    loop {
        let mut line = String::new();
        assert_ne!(reader.read_line(&mut line).unwrap(), 0);
        if let Some(json) = line.trim_end().strip_prefix(MARKER) {
            return Running {
                child,
                info: serde_json::from_str(json).unwrap(),
            };
        }
    }
}
fn running(mode: &str) -> Running {
    let mut cmd = Command::new(std::env::current_exe().unwrap());
    helper(&mut cmd, mode);
    cmd.stdout(Stdio::piped()).stderr(Stdio::null());
    ready(cmd.spawn().unwrap())
}
fn bounded_wait(child: &mut Child) -> std::process::ExitStatus {
    let deadline = Instant::now() + Duration::from_secs(10);
    loop {
        if let Some(status) = child.try_wait().unwrap() {
            return status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            panic!("test process did not terminate");
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

#[test]
fn environment_inherits_streams_cwd_and_literal_arguments() {
    let directory = tempfile::tempdir().unwrap();
    let mut cmd = Command::new(CLI);
    cmd.args(["env", "run", "CLIBOX_TEST_VALUE=한글 🦀", "--"])
        .arg(std::env::current_exe().unwrap());
    helper(&mut cmd, "inspect");
    let literals = [
        "",
        "a b",
        "&|;$(literal)",
        "O'Reilly",
        r"a\\b",
        r"\\server\share\tool.exe",
        r#"\"quoted\""#,
    ];
    cmd.args(literals)
        .arg("--test-threads=1")
        .env("CLIBOX_TEST_PARENT", "inherited")
        .env("SSL_CERT_FILE", "inherited-ca-file")
        .env("SSL_CERT_DIR", "inherited-ca-dir")
        .current_dir(directory.path());
    let output = cmd.output().unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = marker(&output.stdout);
    assert_eq!(value["value"], "한글 🦀");
    assert_eq!(value["parent"], "inherited");
    assert_eq!(value["ca_file"], "inherited-ca-file");
    assert_eq!(value["ca_dir"], "inherited-ca-dir");
    let expected = std::fs::canonicalize(directory.path()).unwrap();
    assert_eq!(
        std::fs::canonicalize(value["cwd"].as_str().unwrap()).unwrap(),
        expected
    );
    let expected = [
        &["--exact", "fixture", "--nocapture"][..],
        &literals,
        &["--test-threads=1"],
    ]
    .concat();
    assert_eq!(value["args"], json!(expected));
    assert!(String::from_utf8_lossy(&output.stderr).contains("FIXTURE_STDERR"));
}
#[test]
fn environment_propagates_status_and_redacts_failures() {
    let mut cmd = Command::new(CLI);
    cmd.args(["env", "run", "--"])
        .arg(std::env::current_exe().unwrap());
    helper(&mut cmd, "exit");
    assert_eq!(cmd.status().unwrap().code(), Some(37));
    for args in [
        vec!["env", "run", "SECRET=PRIVATE_VALUE"],
        vec![
            "env",
            "run",
            "SECRET=PRIVATE_VALUE",
            "/not-existing/PRIVATE_PATH",
            "PRIVATE_ARG",
        ],
        vec!["open", "https://PRIVATE_URL", "--PRIVATE_OPTION"],
        vec!["clipboard", "copy", "PRIVATE_TEXT", "PRIVATE_EXTRA"],
    ] {
        let output = Command::new(CLI)
            .args(&args)
            .env("RUST_LOG", "trace")
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        assert!(!String::from_utf8_lossy(&output.stderr).contains("PRIVATE"));
    }
}
#[cfg(unix)]
#[test]
fn environment_forwards_and_reproduces_termination_signal() {
    use std::os::unix::process::ExitStatusExt;
    let mut cmd = Command::new(CLI);
    cmd.args(["env", "run", "--"])
        .arg(std::env::current_exe().unwrap());
    helper(&mut cmd, "wait");
    cmd.stdout(Stdio::piped()).stderr(Stdio::null());
    let mut run = ready(cmd.spawn().unwrap());
    unsafe {
        libc::kill(run.child.id() as i32, libc::SIGTERM);
    }
    assert_eq!(bounded_wait(&mut run.child).signal(), Some(libc::SIGTERM));
}

#[test]
fn ports_find_test_owned_tcp_udp_ipv4_ipv6_and_kill_once() {
    let mut owner = running("serve");
    let endpoints = owner.info["endpoints"].as_array().unwrap();
    assert!(endpoints.len() >= 2);
    let ports: Vec<_> = endpoints
        .iter()
        .map(|e| e["port"].as_u64().unwrap().to_string())
        .collect();
    let mut command = Command::new(CLI);
    command
        .args(["port", "list", "--protocol", "all", "--json"])
        .args(&ports)
        .args(&ports);
    let output = command.output().unwrap();
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(
        output.status.code(),
        Some(if report["errors"].as_array().unwrap().is_empty() {
            0
        } else {
            1
        })
    );
    let rows: Vec<_> = report["results"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|r| r["pid"] == owner.info["pid"])
        .collect();
    for endpoint in endpoints {
        assert!(
            rows.iter().any(|r| r["port"] == endpoint["port"]
                && r["protocol"] == endpoint["protocol"]
                && r["address"] == endpoint["address"]),
            "missing {endpoint}: {report}"
        );
    }
    let pids = Command::new(CLI)
        .args(["port", "list", "--protocol", "all", "--pids"])
        .args(&ports)
        .output()
        .unwrap();
    let expected_pid = owner.info["pid"].to_string();
    assert_eq!(
        String::from_utf8_lossy(&pids.stdout)
            .lines()
            .filter(|s| *s == expected_pid)
            .count(),
        1
    );
    let quiet = Command::new(CLI)
        .args(["port", "list", "--protocol", "all", "--quiet"])
        .args(&ports)
        .output()
        .unwrap();
    assert!(quiet.stdout.is_empty());
    assert_eq!(quiet.status.code(), output.status.code());
    if !report["errors"].as_array().unwrap().is_empty() {
        assert!(!quiet.stderr.is_empty());
    }
    let default = Command::new(CLI)
        .args(["port", "list", "--json"])
        .args(&ports)
        .output()
        .unwrap();
    let default: Value = serde_json::from_slice(&default.stdout).unwrap();
    assert!(default["results"]
        .as_array()
        .unwrap()
        .iter()
        .all(|r| r["protocol"] == "tcp"));
    // Never let a test terminate an unrelated or unverifiable owner. Restricted
    // hosts still exercise real enumeration and the mocked partial-kill contract;
    // isolated Linux CI additionally exercises the complete CLI kill boundary.
    if !report["errors"].as_array().unwrap().is_empty()
        || report["results"]
            .as_array()
            .unwrap()
            .iter()
            .any(|r| r["pid"] != owner.info["pid"])
    {
        eprintln!(
            "CLI kill fixture skipped because complete exclusive ownership could not be verified."
        );
        return;
    }
    let killed = Command::new(CLI)
        .args(["port", "kill", "--protocol", "all", "--json"])
        .args(&ports)
        .output()
        .unwrap();
    let killed: Value = serde_json::from_slice(&killed.stdout).unwrap();
    assert!(
        killed["results"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|r| r["pid"] == owner.info["pid"])
            .all(|r| r["status"] == "killed" || r["status"] == "already-exited"),
        "{killed}"
    );
    assert!(!bounded_wait(&mut owner.child).success());
}

#[test]
fn clipboard_invalid_stdin_is_rejected_before_backend_access() {
    for bytes in [
        vec![0xff],
        b"PRIVATE\0TEXT".to_vec(),
        vec![b'x'; 16 * 1024 * 1024 + 1],
    ] {
        let mut child = Command::new(CLI)
            .args(["clipboard", "copy"])
            .env("RUST_LOG", "trace")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let mut stdin = child.stdin.take().unwrap();
        let writer = std::thread::spawn(move || {
            let _ = stdin.write_all(&bytes);
        });
        let output = child.wait_with_output().unwrap();
        writer.join().unwrap();
        assert_eq!(output.status.code(), Some(1));
        assert!(output.stdout.is_empty());
        assert!(!String::from_utf8_lossy(&output.stderr).contains("PRIVATE"));
    }
}
#[cfg(unix)]
#[test]
fn blocked_stdin_can_be_interrupted() {
    let mut child = Command::new(CLI)
        .args(["clipboard", "copy"])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    std::thread::sleep(Duration::from_millis(100));
    unsafe {
        libc::kill(child.id() as i32, libc::SIGINT);
    }
    assert_eq!(bounded_wait(&mut child).code(), Some(130));
}

#[cfg(windows)]
#[test]
fn windows_npm_style_cmd_dispatch_preserves_argument_boundaries() {
    let temp = tempfile::tempdir().unwrap();
    // Like npm's shim, this forwards %* to a real executable. Node is installed
    // by every clibox CI/release job; no GUI or clipboard session is required.
    let script = temp.path().join("inspect.cjs");
    std::fs::write(
        &script,
        "process.stdout.write(JSON.stringify(process.argv.slice(2)))",
    )
    .unwrap();
    let shim = temp.path().join("test-shim.cmd");
    std::fs::write(&shim, "@echo off\r\nnode \"%~dp0inspect.cjs\" %*\r\n").unwrap();
    let args = [
        "",
        "a b",
        "한글",
        "safe&literal",
        "a|b",
        "a>b",
        "(x)",
        "O'Reilly",
        r"a\\b",
        r"\\server\share\tool.exe",
    ];
    let output = Command::new(CLI)
        .args(["env", "run", "--"])
        .arg(&shim)
        .args(args)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        serde_json::from_slice::<Vec<String>>(&output.stdout).unwrap(),
        args
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_tool_copy_preserves_background_owner_and_ignores_stdin_argument_override() {
    use std::os::unix::fs::PermissionsExt;
    let temp = tempfile::tempdir().unwrap();
    let tool = temp.path().join("wl-copy");
    let record = temp.path().join("record");
    std::fs::write(
        &tool,
        "#!/bin/sh\nset -eu\ntest \"$(cat)\" = literal\ntest -z \"${WAYLAND_DEBUG-}\"\nprintf \
         '%s\\n' \"$TMPDIR\" > \"$CLIBOX_TEST_LOG\"\nsleep 30 >/dev/null 2>&1 &\nprintf '%s\\n' \
         \"$!\" >> \"$CLIBOX_TEST_LOG\"\necho PRIVATE_TOOL_DIAGNOSTIC >&2\n",
    )
    .unwrap();
    std::fs::set_permissions(&tool, std::fs::Permissions::from_mode(0o700)).unwrap();
    let started = Instant::now();
    let output = Command::new(CLI)
        .args(["clipboard", "copy", "literal"])
        .env("PATH", format!("{}:/bin:/usr/bin", temp.path().display()))
        .env("WAYLAND_DISPLAY", "fake-test-session")
        .env("WAYLAND_DEBUG", "1")
        .env("CLIBOX_TEST_LOG", &record)
        .stdin(Stdio::piped())
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stdout.is_empty());
    assert!(output.stderr.is_empty());
    assert!(started.elapsed() < Duration::from_secs(5));
    let record = std::fs::read_to_string(record).unwrap();
    let mut lines = record.lines();
    let memory = std::path::PathBuf::from(lines.next().unwrap());
    let pid = lines.next().unwrap().parse::<i32>().unwrap();
    assert!(!memory.exists());
    assert_eq!(unsafe { libc::kill(pid, 0) }, 0);
    unsafe {
        libc::kill(pid, libc::SIGKILL);
    }
}

#[cfg(target_os = "linux")]
#[test]
fn linux_open_wait_cancellation_leaves_the_application_running() {
    use std::os::unix::fs::PermissionsExt;
    let temp = tempfile::tempdir().unwrap();
    let app = temp.path().join("application");
    let record = temp.path().join("pid");
    std::fs::write(
        &app,
        "#!/bin/sh\nprintf '%s' \"$$\" > \"$CLIBOX_TEST_LOG\"\nexec sleep 30\n",
    )
    .unwrap();
    std::fs::set_permissions(&app, std::fs::Permissions::from_mode(0o700)).unwrap();
    let mut child = Command::new(CLI)
        .args(["open", "fixture:target", "--wait", "--app"])
        .arg(app)
        .env("CLIBOX_TEST_LOG", &record)
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    while !record.exists() {
        if Instant::now() > deadline {
            let _ = child.kill();
            panic!("app fixture not started");
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    let pid: i32 = std::fs::read_to_string(record).unwrap().parse().unwrap();
    unsafe {
        libc::kill(child.id() as i32, libc::SIGINT);
    }
    assert_eq!(bounded_wait(&mut child).code(), Some(130));
    assert_eq!(unsafe { libc::kill(pid, 0) }, 0);
    unsafe {
        libc::kill(pid, libc::SIGKILL);
    }
}
