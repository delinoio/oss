use std::{
    io::{Read, Write},
    net::TcpListener,
    process::{Command, Output, Stdio},
    thread,
    time::{Duration, Instant},
};

use serde_json::Value;

const MARKER: &str = "PRIVATE-MARKER-919";
fn accept(listener: &TcpListener) -> (std::net::TcpStream, std::net::SocketAddr) {
    listener.set_nonblocking(true).unwrap();
    let start = Instant::now();
    loop {
        match listener.accept() {
            Ok(pair) => {
                pair.0.set_nonblocking(false).unwrap();
                return pair;
            }
            Err(error)
                if error.kind() == std::io::ErrorKind::WouldBlock
                    && start.elapsed() < Duration::from_secs(6) =>
            {
                thread::sleep(Duration::from_millis(5))
            }
            Err(error) => panic!("fixture accept failed: {error}"),
        }
    }
}

fn cli(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(args)
        .env("NO_COLOR", "1")
        .env("RUST_LOG", "trace,reqwest=trace,hickory_resolver=trace")
        .stdin(Stdio::piped())
        .output()
        .unwrap()
}
fn json(output: &Output, code: i32) -> Value {
    assert_eq!(
        output.status.code(),
        Some(code),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(value.as_object().unwrap().len(), 5);
    assert!(value["elapsed_ms"].is_u64());
    assert!(value["attempts"].is_u64());
    assert!(!String::from_utf8_lossy(&output.stdout).contains(MARKER));
    assert!(!String::from_utf8_lossy(&output.stderr).contains(MARKER));
    assert!(!output.stderr.contains(&0x1b));
    value
}

#[test]
fn invalid_inputs_are_redacted_and_never_emit_json() {
    let invalid: Vec<Vec<&str>> = vec![
        vec!["wait"],
        vec!["wait", "tcp"],
        vec!["wait", "file", ""],
        vec!["wait", "tcp", "localhost"],
        vec!["wait", "tcp", "localhost:0"],
        vec!["wait", "tcp", "localhost:65536"],
        vec!["wait", "tcp", "localhost:+1"],
        vec!["wait", "tcp", "::1:80"],
        vec!["wait", "tcp", "[bad]:80"],
        vec!["wait", "tcp", "127.1:80"],
        vec!["wait", "tcp", "localhost..:80"],
        vec!["wait", "tcp", "127.0.0.1:80", MARKER],
        vec!["wait", "file", MARKER, "--quiet", "--json"],
        vec!["wait", "file", MARKER, "--attempt-timeout", "1s"],
        vec!["wait", "http", "https://PRIVATE-MARKER-919@example.com/"],
        vec!["wait", "http", "https://@example.com/"],
        vec!["wait", "http", "https://example.com:0/"],
        vec!["wait", "http", "https://example.com:/"],
        vec!["wait", "http", "example.com"],
        vec!["wait", "http", "ftp://example.com"],
        vec!["wait", "http", "http://localhost", "--method", "post"],
        vec!["wait", "http", "http://localhost", "--method", "GET"],
        vec!["wait", "http", "http://localhost", "--status", "199"],
        vec!["wait", "http", "http://localhost", "--status", "600"],
        vec!["wait", "http", "http://localhost", "--status", "+200"],
        vec!["wait", "file", MARKER, "--timeout", "1.5s"],
        vec!["wait", "file", MARKER, "--timeout", "-1s"],
        vec!["wait", "file", MARKER, "--timeout", "1"],
        vec!["wait", "file", MARKER, "--timeout", "18446744073709551615h"],
        vec!["wait", "file", MARKER, "--interval", "0"],
        vec!["wait", "file", MARKER, "--interval", "0ms"],
        vec!["wait", "tcp", "localhost:80", "--attempt-timeout", "0s"],
        vec!["wait", "file", MARKER, "--timeout", MARKER],
        vec![MARKER],
    ];
    for args in invalid {
        let output = cli(&args);
        assert_eq!(output.status.code(), Some(2), "{args:?}");
        assert!(output.stdout.is_empty(), "{args:?}");
        assert!(String::from_utf8_lossy(&output.stderr).contains("error:"));
        assert!(!String::from_utf8_lossy(&output.stderr).contains(MARKER));
    }
}

#[test]
fn files_output_modes_units_and_relative_unicode_paths() {
    let directory = tempfile::tempdir().unwrap();
    let name = format!("{MARKER} 한글 empty file");
    std::fs::write(directory.path().join(&name), b"").unwrap();
    for duration in ["0", "0ms", "1ms", "2s", "1m", "1h"] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(["wait", "file", &name, "--timeout", duration, "--json"])
            .current_dir(directory.path())
            .output()
            .unwrap();
        // 1ms can expire on a loaded CI host; duration parsing must still succeed.
        let value = json(
            &output,
            if duration == "1ms" {
                output.status.code().unwrap()
            } else {
                0
            },
        );
        assert_eq!(value["kind"], "file");
        assert_eq!(value["attempts"], 1);
    }
    let path = directory.path().join(&name);
    let output = cli(&["wait", "file", path.to_str().unwrap()]);
    assert!(output.status.success());
    let stdout = String::from_utf8(output.stdout).unwrap();
    assert!(stdout.contains("file ready after") && stdout.contains("attempts"));
    assert!(!stdout.contains(MARKER));
    let output = cli(&["wait", "file", path.to_str().unwrap(), "--quiet"]);
    assert!(output.status.success());
    assert!(output.stdout.is_empty());
    let value = json(
        &cli(&["wait", "file", directory.path().to_str().unwrap(), "--json"]),
        1,
    );
    assert_eq!(value["status"], "failed");
    assert_eq!(value["error"]["code"], "not_regular_file");
    std::fs::remove_file(&path).unwrap();
    let output = cli(&[
        "wait",
        "file",
        path.to_str().unwrap(),
        "--timeout",
        "30ms",
        "--json",
    ]);
    let value = json(&output, 1);
    assert_eq!(value["status"], "timeout");
    assert!(value["error"]["message"]
        .as_str()
        .unwrap()
        .contains("file_missing"));
    let output = cli(&[
        "wait",
        "file",
        path.to_str().unwrap(),
        "--timeout",
        "5ms",
        "--quiet",
    ]);
    assert!(output.stdout.is_empty() && !output.stderr.is_empty());
}

#[test]
fn delayed_file_and_tcp_listener_become_ready() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join(MARKER);
    let other = path.clone();
    let writer = thread::spawn(move || {
        thread::sleep(Duration::from_millis(100));
        std::fs::write(other, []).unwrap();
    });
    let value = json(
        &cli(&[
            "wait",
            "file",
            path.to_str().unwrap(),
            "--interval",
            "10ms",
            "--timeout",
            "5s",
            "--json",
        ]),
        0,
    );
    assert_eq!(value["status"], "ready");
    writer.join().unwrap();
    // Retain ownership while readiness is delayed. Dropping a listener before
    // rebinding lets another parallel fixture reuse its ephemeral port, making
    // the CLI observe the wrong service and leaving this server unconnected.
    let socket = socket2::Socket::new(socket2::Domain::IPV4, socket2::Type::STREAM, None).unwrap();
    socket
        .bind(
            &"127.0.0.1:0"
                .parse::<std::net::SocketAddr>()
                .unwrap()
                .into(),
        )
        .unwrap();
    let address = socket.local_addr().unwrap().as_socket().unwrap();
    let server = thread::spawn(move || {
        thread::sleep(Duration::from_millis(100));
        socket.listen(128).unwrap();
        let listener = TcpListener::from(socket);
        let (mut stream, _) = accept(&listener);
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        assert_eq!(stream.read(&mut [0; 1]).unwrap(), 0);
    });
    let value = json(
        &cli(&[
            "wait",
            "tcp",
            &address.to_string(),
            "--interval",
            "10ms",
            "--timeout",
            "5s",
            "--json",
        ]),
        0,
    );
    assert_eq!(value["kind"], "tcp");
    server.join().unwrap();
}

fn http_fixture(
    responses: Vec<&'static str>,
    extra: &[&str],
    delay_headers: bool,
) -> (Output, Vec<String>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        let mut requests = Vec::new();
        for response in responses {
            let (mut stream, _) = accept(&listener);
            stream
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let mut bytes = Vec::new();
            while !bytes.ends_with(b"\r\n\r\n") {
                let mut byte = [0];
                if stream.read(&mut byte).unwrap() == 0 {
                    break;
                }
                bytes.push(byte[0]);
            }
            requests.push(String::from_utf8(bytes).unwrap());
            if delay_headers {
                thread::sleep(Duration::from_millis(200));
            }
            let _ = stream.write_all(response.as_bytes());
        }
        requests
    });
    let url = format!("http://{address}/{MARKER}?token={MARKER}");
    let mut args = vec!["wait", "http", &url, "--json"];
    args.extend(extra);
    let output = cli(&args);
    (output, server.join().unwrap())
}

#[test]
fn http_methods_status_transitions_redirects_and_header_deadlines() {
    let (output, requests) = http_fixture(
        vec![
            "HTTP/1.1 503 Pending\r\nContent-Length: 0\r\n\r\n",
            "HTTP/1.1 201 Created\r\nContent-Length: 0\r\n\r\n",
        ],
        &["--timeout", "3s", "--interval", "10ms"],
        false,
    );
    let value = json(&output, 0);
    assert_eq!(value["attempts"], 2);
    assert!(requests
        .iter()
        .all(|r| r.starts_with("GET ") && !r.to_lowercase().contains("authorization")));
    let (output, requests) = http_fixture(vec!["HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/PRIVATE-MARKER-919\r\nContent-Length: 0\r\n\r\n"], &["--status", "302", "--method", "head", "--timeout", "3s"], false);
    assert_eq!(json(&output, 0)["attempts"], 1);
    assert!(requests[0].starts_with("HEAD "));
    let (output, _) = http_fixture(
        vec!["HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/\r\nContent-Length: 0\r\n\r\n"],
        &["--timeout", "100ms"],
        false,
    );
    assert!(json(&output, 1)["error"]["message"]
        .as_str()
        .unwrap()
        .contains("unexpected_status"));
    let (output, _) = http_fixture(
        vec!["HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"],
        &["--attempt-timeout", "30ms", "--timeout", "100ms"],
        true,
    );
    assert!(json(&output, 1)["error"]["message"]
        .as_str()
        .unwrap()
        .contains("attempt_timeout"));
}

#[test]
fn proxy_and_log_environment_cannot_expose_or_redirect_requests() {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let proxy = TcpListener::bind("127.0.0.1:0").unwrap();
    proxy.set_nonblocking(true).unwrap();
    let url = format!(
        "http://localhost:{}/{MARKER}",
        listener.local_addr().unwrap().port()
    );
    let proxy_url = format!("http://{MARKER}:{MARKER}@{}", proxy.local_addr().unwrap());
    let server = thread::spawn(move || {
        let (mut stream, _) = accept(&listener);
        let mut bytes = [0; 4096];
        let n = stream.read(&mut bytes).unwrap();
        assert!(!String::from_utf8_lossy(&bytes[..n])
            .to_lowercase()
            .contains("authorization"));
        stream.write_all(b"HTTP/1.1 204 OK\r\n\r\n").unwrap();
    });
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["wait", "http", &url, "--json", "--timeout", "5s"])
        .env("HTTP_PROXY", &proxy_url)
        .env("HTTPS_PROXY", &proxy_url)
        .env("ALL_PROXY", &proxy_url)
        .env("http_proxy", &proxy_url)
        .env("https_proxy", &proxy_url)
        .env("all_proxy", &proxy_url)
        .env("NO_PROXY", "")
        .env("no_proxy", "")
        .env("RUST_LOG", "trace,reqwest=trace,hickory_resolver=trace")
        .output()
        .unwrap();
    assert_eq!(json(&output, 0)["status"], "ready");
    assert_eq!(
        proxy.accept().unwrap_err().kind(),
        std::io::ErrorKind::WouldBlock
    );
    server.join().unwrap();
    let directory = tempfile::tempdir().unwrap();
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["wait", "file", directory.path().to_str().unwrap(), "--json"])
        .env("RUST_LOG", "PRIVATE-MARKER-919=invalid-level")
        .output()
        .unwrap();
    json(&output, 1);
}

#[cfg(unix)]
#[test]
fn symlinks_special_files_loops_and_metadata_only_access() {
    use std::os::unix::{
        fs::{symlink, PermissionsExt},
        net::UnixListener,
    };
    let dir = tempfile::tempdir().unwrap();
    let file = dir.path().join(MARKER);
    let link = dir.path().join("link");
    symlink(&file, &link).unwrap();
    assert_eq!(
        json(
            &cli(&[
                "wait",
                "file",
                link.to_str().unwrap(),
                "--json",
                "--timeout",
                "10ms"
            ]),
            1
        )["status"],
        "timeout"
    );
    std::fs::write(&file, []).unwrap();
    std::fs::set_permissions(&file, std::fs::Permissions::from_mode(0o000)).unwrap();
    assert_eq!(
        json(&cli(&["wait", "file", link.to_str().unwrap(), "--json"]), 0)["status"],
        "ready"
    );
    std::fs::remove_file(&file).unwrap();
    symlink(&link, &file).unwrap();
    assert_eq!(
        json(&cli(&["wait", "file", link.to_str().unwrap(), "--json"]), 1)["error"]["code"],
        "filesystem"
    );
    let socket = dir.path().join("socket");
    let _socket = UnixListener::bind(&socket).unwrap();
    assert_eq!(
        json(
            &cli(&["wait", "file", socket.to_str().unwrap(), "--json"]),
            1
        )["error"]["code"],
        "not_regular_file"
    );
}

#[cfg(unix)]
#[test]
fn handled_signals_produce_one_final_result_during_checks_and_delays() {
    use std::io::{BufRead, BufReader};
    for (signal, exit, kind) in [
        (libc::SIGINT, 130, "file"),
        (libc::SIGTERM, 143, "file"),
        (libc::SIGINT, 130, "http"),
        (libc::SIGTERM, 143, "http"),
    ] {
        let dir = tempfile::tempdir().unwrap();
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let target = if kind == "file" {
            dir.path().join(MARKER).to_str().unwrap().to_owned()
        } else {
            format!("http://{}/{MARKER}", listener.local_addr().unwrap())
        };
        let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args([
                "wait",
                kind,
                &target,
                "--json",
                "--interval",
                "10s",
                "--timeout",
                "5s",
            ])
            .env("RUST_LOG", "clibox=debug")
            .env("NO_COLOR", "1")
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let mut stderr = BufReader::new(child.stderr.take().unwrap());
        let mut line = String::new();
        loop {
            assert!(stderr.read_line(&mut line).unwrap() > 0);
            if line.contains(if kind == "file" {
                "wait_observation"
            } else {
                "wait_attempt"
            }) {
                break;
            }
            line.clear();
        }
        // For HTTP wait until the request is pending at the test-owned socket.
        let _connection = if kind == "http" {
            Some(accept(&listener))
        } else {
            None
        };
        assert_eq!(unsafe { libc::kill(child.id() as i32, signal) }, 0);
        let output = child.wait_with_output().unwrap();
        let value = json(&output, exit);
        assert_eq!(value["status"], "cancelled");
        let mut rest = String::new();
        stderr.read_to_string(&mut rest).unwrap();
        assert!(rest.contains("Wait cancelled"));
        assert!(!rest.contains(MARKER));
    }
}

#[cfg(windows)]
#[test]
fn windows_ctrl_c_is_handled_in_an_isolated_console() {
    use std::os::windows::process::CommandExt;

    use windows_sys::Win32::System::Threading::CREATE_NEW_CONSOLE;
    let output = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "windows_ctrl_c_helper", "--nocapture"])
        .env("CLIBOX_TEST_CONSOLE_HELPER", "1")
        .creation_flags(CREATE_NEW_CONSOLE)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
}

#[cfg(windows)]
#[test]
fn windows_ctrl_c_helper() {
    use std::io::{BufRead, BufReader};

    use windows_sys::Win32::System::Console::{
        GenerateConsoleCtrlEvent, SetConsoleCtrlHandler, CTRL_C_EVENT,
    };
    if std::env::var_os("CLIBOX_TEST_CONSOLE_HELPER").is_none() {
        return;
    }
    unsafe extern "system" fn ignore(_: u32) -> i32 {
        1
    }
    // A real handler (rather than the inheritable ignore flag) protects only
    // this disposable helper. Its clibox child still receives real Ctrl+C.
    assert_ne!(unsafe { SetConsoleCtrlHandler(Some(ignore), 1) }, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join(MARKER);
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args([
            "wait",
            "file",
            path.to_str().unwrap(),
            "--json",
            "--timeout",
            "5s",
        ])
        .env("RUST_LOG", "clibox=debug")
        .env("NO_COLOR", "1")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stderr = BufReader::new(child.stderr.take().unwrap());
    let mut line = String::new();
    loop {
        assert!(stderr.read_line(&mut line).unwrap() > 0);
        if line.contains("wait_observation") {
            break;
        }
        line.clear();
    }
    assert_ne!(unsafe { GenerateConsoleCtrlEvent(CTRL_C_EVENT, 0) }, 0);
    let output = child.wait_with_output().unwrap();
    assert_eq!(json(&output, 130)["status"], "cancelled");
}

#[cfg(windows)]
#[test]
fn windows_file_symlinks_follow_and_dangling_links_retry() {
    use std::os::windows::fs::symlink_file;
    let dir = tempfile::tempdir().unwrap();
    let file = dir.path().join(MARKER);
    let link = dir.path().join("link");
    symlink_file(&file, &link).expect("Windows CI requires Developer Mode or symlink privilege");
    assert_eq!(
        json(
            &cli(&[
                "wait",
                "file",
                link.to_str().unwrap(),
                "--json",
                "--timeout",
                "10ms"
            ]),
            1
        )["status"],
        "timeout"
    );
    std::fs::write(&file, []).unwrap();
    assert_eq!(
        json(&cli(&["wait", "file", link.to_str().unwrap(), "--json"]), 0)["status"],
        "ready"
    );
}

#[test]
fn incomplete_http_response_retries_but_malformed_response_is_terminal() {
    let (output, _) = http_fixture(
        vec!["", "HTTP/1.1 204 No Content\r\n\r\n"],
        &["--timeout", "3s", "--interval", "10ms"],
        false,
    );
    assert_eq!(json(&output, 0)["attempts"], 2);
    let (output, _) = http_fixture(
        vec!["PRIVATE-MARKER-919\r\n\r\n"],
        &["--timeout", "3s"],
        false,
    );
    let value = json(&output, 1);
    assert_eq!(value["status"], "failed");
    assert_eq!(value["error"]["code"], "http_protocol");
    assert_eq!(value["attempts"], 1);
}

#[test]
fn tls_dependency_errors_and_ca_overrides_are_isolated_and_redacted() {
    let cert = rcgen::generate_simple_self_signed(vec!["localhost".to_owned()]).unwrap();
    let dir = tempfile::tempdir().unwrap();
    let ca = dir.path().join(MARKER);
    std::fs::write(&ca, cert.cert.pem()).unwrap();
    let config = rustls::ServerConfig::builder_with_provider(std::sync::Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .unwrap()
    .with_no_client_auth()
    .with_single_cert(
        vec![cert.cert.der().clone()],
        rustls::pki_types::PrivatePkcs8KeyDer::from(cert.signing_key.serialize_der()).into(),
    )
    .unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = listener.local_addr().unwrap().port();
    let server = thread::spawn(move || {
        let (stream, _) = accept(&listener);
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let mut stream = rustls::StreamOwned::new(
            rustls::ServerConnection::new(std::sync::Arc::new(config)).unwrap(),
            stream,
        );
        let _ = stream.read(&mut [0; 4096]);
        let _ = stream.write_all(b"HTTP/1.1 204 No Content\r\n\r\n");
    });
    let url = format!("https://localhost:{port}/{MARKER}");
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["wait", "http", &url, "--json", "--timeout", "5s"])
        .env("SSL_CERT_FILE", &ca)
        .env("SSL_CERT_DIR", dir.path())
        .env(
            "RUST_LOG",
            "trace,rustls=trace,reqwest=trace,hickory_resolver=trace",
        )
        .output()
        .unwrap();
    let value = json(&output, 1);
    assert_eq!(value["status"], "failed");
    assert_eq!(value["error"]["code"], "tls_certificate");
    assert_eq!(value["attempts"], 1);
    server.join().unwrap();
}
