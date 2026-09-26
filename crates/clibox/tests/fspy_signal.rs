#![cfg(unix)]

use std::{os::unix::process::ExitStatusExt, process::Command};

#[test]
fn fspy_preserves_child_signal_after_publishing_results() {
    if std::env::var_os("CLIBOX_FSPY_SIGNAL_FIXTURE").is_some() {
        // Use this test image because macOS protects system binaries from injection.
        unsafe { libc::raise(libc::SIGTERM) };
        panic!("signal fixture unexpectedly continued");
    }
    let directory = tempfile::tempdir().unwrap();
    let input = directory.path().join("input.txt");
    std::fs::write(&input, b"fixture").unwrap();
    let record = directory.path().join("record.ndjson");
    let executable = std::env::current_exe().unwrap();
    let status = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["fspy", "record", "--root"])
        .arg(directory.path())
        .arg("--output")
        .arg(&record)
        .arg("--")
        .arg(&executable)
        .args([
            "--exact",
            "fspy_preserves_child_signal_after_publishing_results",
        ])
        .env("CLIBOX_FSPY_SIGNAL_FIXTURE", "1")
        .status()
        .unwrap();
    assert_eq!(status.signal(), Some(libc::SIGTERM));
    assert!(record.is_file());

    let status = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["fspy", "assetcov", "--root"])
        .arg(directory.path())
        .args(["--include", "input.txt", "--quiet", "--"])
        .arg(&executable)
        .args([
            "--exact",
            "fspy_preserves_child_signal_after_publishing_results",
        ])
        .env("CLIBOX_FSPY_SIGNAL_FIXTURE", "1")
        .status()
        .unwrap();
    assert_eq!(status.signal(), Some(libc::SIGTERM));
}
