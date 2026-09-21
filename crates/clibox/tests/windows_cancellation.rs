#![cfg(windows)]

use std::{
    fs,
    io::Write,
    os::windows::process::CommandExt,
    process::{Command, Stdio},
    thread,
    time::{Duration, Instant},
};

use windows_sys::Win32::System::{
    Console::{AllocConsole, GenerateConsoleCtrlEvent, CTRL_BREAK_EVENT},
    Threading::{CREATE_NEW_PROCESS_GROUP, DETACHED_PROCESS},
};

#[test]
fn cancellation_cleans_output_in_an_isolated_windows_console() {
    // CI runners need not have an interactive console. A separate test process
    // owns one so the control event cannot reach the runner or another test.
    let output = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "windows_console_helper", "--nocapture"])
        .env("CLIBOX_TEST_CONSOLE_CHILD", "1")
        .creation_flags(DETACHED_PROCESS)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

#[test]
fn windows_console_helper() {
    if std::env::var_os("CLIBOX_TEST_CONSOLE_CHILD").is_none() {
        return;
    }
    assert_ne!(unsafe { AllocConsole() }, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("output");
    fs::write(&path, b"original").unwrap();
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args([
            "base64",
            "encode",
            "--output",
            path.to_str().unwrap(),
            "--force",
        ])
        .creation_flags(CREATE_NEW_PROCESS_GROUP)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdin = child.stdin.take().unwrap();
    stdin.write_all(b"waiting for EOF").unwrap();
    let deadline = Instant::now() + Duration::from_secs(10);
    while fs::read_dir(dir.path()).unwrap().count() < 2 {
        assert!(Instant::now() < deadline, "temporary file was not prepared");
        thread::sleep(Duration::from_millis(20));
    }
    assert_ne!(
        unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, child.id()) },
        0
    );
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            child.kill().unwrap();
            child.wait().unwrap();
            panic!("cancellation blocked on stdin");
        }
        thread::sleep(Duration::from_millis(20));
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(
        output.status.code(),
        Some(130),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stdout.is_empty());
    assert_eq!(fs::read(&path).unwrap(), b"original");
    assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    drop(stdin);
}
