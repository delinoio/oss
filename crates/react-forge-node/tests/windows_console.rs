#![cfg(windows)]

use std::{
    fs,
    os::windows::process::CommandExt,
    path::PathBuf,
    process::{Command, Stdio},
    thread,
    time::{Duration, Instant},
};

use windows_sys::Win32::System::{
    Console::{CTRL_BREAK_EVENT, CTRL_C_EVENT, GenerateConsoleCtrlEvent, SetConsoleCtrlHandler},
    Threading::CREATE_NEW_CONSOLE,
};

#[test]
fn cli_console_events_cancel_and_dispose_before_exit() {
    if std::env::var_os("REACT_FORGE_WINDOWS_CONSOLE_TEST").is_none() {
        // Ordinary root Cargo tests do not build the workspace Node package.
        // The six-host React Forge job explicitly enables this after its build.
        return;
    }
    let result = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "isolated_console_helper", "--nocapture"])
        .env("REACT_FORGE_CONSOLE_HELPER", "1")
        .creation_flags(CREATE_NEW_CONSOLE)
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "{}\n{}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
}

#[test]
fn isolated_console_helper() {
    if std::env::var_os("REACT_FORGE_CONSOLE_HELPER").is_none() {
        return;
    }
    unsafe extern "system" fn ignore(_: u32) -> i32 {
        1
    }
    // The helper has its own console. Broadcast events must never reach the CI
    // runner; use a real handler rather than an inherited Ctrl+C ignore flag.
    assert_ne!(unsafe { SetConsoleCtrlHandler(None, 0) }, 0);
    assert_ne!(unsafe { SetConsoleCtrlHandler(Some(ignore), 1) }, 0);
    let package = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../packages/react-forge");
    let fixture = package.join("tests/fixtures/pending-console.mjs");
    for event in [CTRL_C_EVENT, CTRL_BREAK_EVENT] {
        let directory = tempfile::tempdir().unwrap();
        let ready = directory.path().join("ready");
        let cleaned = directory.path().join("cleaned");
        let mut child = Command::new("node")
            .arg(&fixture)
            .arg(directory.path())
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let deadline = Instant::now() + Duration::from_secs(30);
        while !ready.is_file() {
            if child.try_wait().unwrap().is_some() || Instant::now() >= deadline {
                let _ = child.kill();
                let output = child.wait_with_output().unwrap();
                panic!(
                    "CLI did not become ready: {} {}",
                    String::from_utf8_lossy(&output.stdout),
                    String::from_utf8_lossy(&output.stderr)
                );
            }
            thread::sleep(Duration::from_millis(20));
        }
        assert_ne!(unsafe { GenerateConsoleCtrlEvent(event, 0) }, 0);
        while child.try_wait().unwrap().is_none() {
            if Instant::now() >= deadline {
                child.kill().unwrap();
                child.wait().unwrap();
                panic!("CLI cancellation timed out");
            }
            thread::sleep(Duration::from_millis(20));
        }
        let result = child.wait_with_output().unwrap();
        assert_eq!(
            result.status.code(),
            Some(130),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(result.stderr.is_empty());
        let output: serde_json::Value = serde_json::from_slice(&result.stdout).unwrap();
        assert_eq!(output["error"]["code"], "cancelled");
        assert_eq!(fs::read_to_string(cleaned).unwrap(), "cleaned");
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 3);
    }
}
