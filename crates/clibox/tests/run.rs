#![cfg(unix)]

use std::{
    fs,
    io::{Read, Write},
    net::TcpListener,
    process::{Command, Stdio},
    thread,
    time::Duration,
};

fn command(home: &std::path::Path, args: &[&str]) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
    command.args(args).env("HOME", home);
    #[cfg(target_os = "linux")]
    command.env("XDG_STATE_HOME", home.join("state"));
    #[cfg(target_os = "macos")]
    command.env_remove("XDG_STATE_HOME");
    command
}

#[test]
fn rate_limit_uses_shared_hashed_state_and_zero_wait_is_immediate() {
    let home = tempfile::tempdir().unwrap();
    let first = command(
        home.path(),
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
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(first.status.success());
    let second = command(
        home.path(),
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
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(second.status.code(), Some(124));
    #[cfg(target_os = "linux")]
    let state = home.path().join("state/clibox/run/buckets");
    #[cfg(target_os = "macos")]
    let state = home
        .path()
        .join("Library/Application Support/clibox/run/buckets");
    if state.exists() {
        for entry in fs::read_dir(state).unwrap() {
            assert!(!entry
                .unwrap()
                .file_name()
                .to_string_lossy()
                .contains("shared.bucket"));
        }
    }
}

#[test]
fn lock_fail_never_starts_a_second_workload() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("lock-owner-ready");
    let assignment = format!("MARKER={}", marker.display());
    let mut owner = command(
        home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "migration",
            &assignment,
            "--",
            "sh",
            "-c",
            "printf ready > \"$MARKER\"; sleep 1",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    for _ in 0..50 {
        if marker.is_file() {
            break;
        }
        thread::sleep(Duration::from_millis(20));
    }
    assert!(marker.is_file(), "lock owner did not start its workload");
    let contender = command(
        home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "migration",
            "--on-locked",
            "fail",
            "--",
            "sh",
            "-c",
            "exit 99",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(contender.status.code(), Some(75));
    assert!(owner.wait().unwrap().success());
}

#[test]
fn retry_preserves_the_last_child_status_after_exhaustion() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("attempts");
    let script = format!(
        "test -f '{}' && exit 7; : > '{}'; exit 7",
        marker.display(),
        marker.display()
    );
    let output = command(
        home.path(),
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
            "sh",
            "-c",
            &script,
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(7));
    assert!(marker.is_file());
}

#[test]
fn timeout_terminates_a_silent_workload() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "50ms",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "sleep 1",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
}

#[test]
fn timeout_terminates_owned_descendants() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("descendant-pid");
    let assignment = format!("MARKER={}", marker.display());
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "50ms",
            "--kill-after",
            "0",
            &assignment,
            "--",
            "sh",
            "-c",
            "sleep 30 & echo $! > \"$MARKER\"; wait",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("timeout left an owned descendant running");
}

#[test]
fn completed_workload_cleans_up_its_background_descendants() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("completed-descendant-pid");
    let assignment = format!("MARKER={}", marker.display());
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--idle-timeout",
            "30s",
            "--kill-after",
            "0",
            &assignment,
            "--",
            "sh",
            "-c",
            "sleep 30 & echo $! > \"$MARKER\"",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    let deadline = std::time::Instant::now() + Duration::from_secs(3);
    while wrapper.try_wait().unwrap().is_none() {
        if std::time::Instant::now() >= deadline {
            let _ = wrapper.kill();
            let _ = wrapper.wait();
            if let Ok(pid) = fs::read_to_string(&marker)
                .and_then(|value| value.trim().parse::<i32>().map_err(std::io::Error::other))
            {
                unsafe {
                    libc::kill(pid, libc::SIGKILL);
                }
            }
            panic!("completed workload left a descendant supervising its output");
        }
        thread::sleep(Duration::from_millis(20));
    }
    assert!(wrapper.wait().unwrap().success());
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("completed workload left an owned descendant running");
}

#[test]
fn wrappers_preserve_a_leading_literal_workload_separator() {
    use std::os::unix::fs::PermissionsExt;

    let home = tempfile::tempdir().unwrap();
    let executable = home.path().join("tool=value");
    fs::write(&executable, "#!/bin/sh\nexit 0\n").unwrap();
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "1s",
            "--",
            executable.to_str().unwrap(),
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
}

#[test]
fn external_service_is_observed_without_becoming_owned() {
    let home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = [0u8; 1024];
        let _ = stream.read(&mut request);
        stream
            .write_all(b"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")
            .unwrap();
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success());
    server.join().unwrap();
}

#[test]
fn external_service_waits_for_delayed_readiness() {
    let home = tempfile::tempdir().unwrap();
    let reservation = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = reservation.local_addr().unwrap();
    drop(reservation);
    let server = thread::spawn(move || {
        thread::sleep(Duration::from_millis(100));
        let listener = TcpListener::bind(address).unwrap();
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = [0u8; 1024];
        let _ = stream.read(&mut request);
        stream
            .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
            .unwrap();
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--ready-timeout",
            "2s",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success());
    server.join().unwrap();
}

#[test]
fn managed_service_delimiter_accepts_independent_workload_arguments() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            "http://127.0.0.1:9/health",
            "--ready-timeout",
            "50ms",
            "--service",
            "true",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    // The endpoint can time out while the short-lived service is still being
    // scheduled. The important parser boundary is that this reaches managed
    // service runtime handling, not a CLI usage error that treats the
    // workload as service arguments.
    assert_ne!(output.status.code(), Some(2));
}

#[test]
fn invalid_wrapper_arguments_stay_redacted() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "PRIVATE-RUN-MARKER",
            "--scope",
            "user",
            "--project-dir",
            "PRIVATE-RUN-MARKER",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(2));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("PRIVATE-RUN-MARKER"));
}
