#![cfg(windows)]

use std::{
    fs,
    net::TcpListener,
    process::Command,
    thread,
    time::{Duration, Instant},
};

fn command(state_home: &std::path::Path, args: &[&str]) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
    command.args(args).env("LOCALAPPDATA", state_home);
    command
}

#[test]
fn rate_limit_deducts_shared_windows_state() {
    let state_home = tempfile::tempdir().unwrap();
    let first = command(
        state_home.path(),
        &[
            "run",
            "with-rate-limit",
            "--name",
            "shared.bucket",
            "--limit",
            "1",
            "--period",
            "1m",
            "--",
            "cmd",
            "/C",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(first.status.success(), "{first:?}");

    let second = command(
        state_home.path(),
        &[
            "run",
            "with-rate-limit",
            "--name",
            "shared.bucket",
            "--limit",
            "1",
            "--period",
            "1m",
            "--wait-timeout",
            "0",
            "--",
            "cmd",
            "/C",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(second.status.code(), Some(124), "{second:?}");
}

#[test]
fn lock_fail_does_not_start_a_second_windows_workload() {
    let state_home = tempfile::tempdir().unwrap();
    let marker = state_home.path().join("lock-owner-ready");
    let owner_script = format!(
        "echo ready > \"{}\" & timeout /t 2 /nobreak > NUL",
        marker.display()
    );
    let mut owner = command(
        state_home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "migration",
            "--",
            "cmd",
            "/C",
            &owner_script,
        ],
    )
    .spawn()
    .unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    while !marker.is_file() {
        if owner.try_wait().unwrap().is_some() || Instant::now() >= deadline {
            let _ = owner.kill();
            let _ = owner.wait();
            panic!("lock owner did not start its workload");
        }
        thread::sleep(Duration::from_millis(20));
    }

    let contender = command(
        state_home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "migration",
            "--on-locked",
            "fail",
            "--",
            "cmd",
            "/C",
            "exit 99",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(contender.status.code(), Some(75), "{contender:?}");
    assert!(owner.wait().unwrap().success());
}

#[test]
fn retry_and_timeout_preserve_windows_wrapper_statuses() {
    let state_home = tempfile::tempdir().unwrap();
    let retry = command(
        state_home.path(),
        &[
            "run",
            "with-retry",
            "--max-attempts",
            "2",
            "--delay",
            "1ms",
            "--jitter",
            "none",
            "--",
            "cmd",
            "/C",
            "exit 7",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(retry.status.code(), Some(7), "{retry:?}");

    let timeout = command(
        state_home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "50ms",
            "--kill-after",
            "0",
            "--",
            "cmd",
            "/C",
            "timeout /t 2 /nobreak > NUL",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(timeout.status.code(), Some(124), "{timeout:?}");
}

#[test]
fn managed_service_is_cleaned_up_after_a_windows_workload() {
    let state_home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = listener.local_addr().unwrap().port();
    drop(listener);
    let service = concat!(
        "$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, \
         $args[0]); ",
        "$listener.Start(); ",
        "while ($true) { ",
        "$client = $listener.AcceptTcpClient(); ",
        "$stream = $client.GetStream(); ",
        "$response = [System.Text.Encoding]::ASCII.GetBytes(\"HTTP/1.1 204 No \
         Content`r`nContent-Length: 0`r`n`r`n\"); ",
        "$stream.Write($response, 0, $response.Length); ",
        "$client.Dispose() }"
    );
    let output = command(
        state_home.path(),
        &[
            "run",
            "with-service",
            &format!("http://127.0.0.1:{port}/health"),
            "--interval",
            "10ms",
            "--ready-timeout",
            "3s",
            "--service",
            "powershell",
            "-NoProfile",
            "-Command",
            service,
            &port.to_string(),
            "--",
            "cmd",
            "/C",
            "exit 0",
        ],
    )
    .output()
    .unwrap();

    assert!(output.status.success(), "{output:?}");
    assert!(
        fs::read_dir(state_home.path()).unwrap().next().is_some(),
        "the Windows state root was not used"
    );
}
