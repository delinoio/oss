#![cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]

use std::{fs, process::Command};

use clibox_fspy::record::{self, NativePath, Operation};

#[test]
fn mutating_open_without_write_is_recorded() {
    if let Some(root) = std::env::var_os("CLIBOX_FSPY_OPEN_FIXTURE") {
        let root = std::path::PathBuf::from(root);
        assert_eq!(fs::read(root.join("input.txt")).unwrap(), b"fixture");
        drop(fs::File::create(root.join("stamp")).unwrap());
        return;
    }
    let directory = tempfile::tempdir().unwrap();
    fs::write(directory.path().join("input.txt"), b"fixture").unwrap();
    let report = directory.path().join("trace.ndjson");
    let status = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["fspy", "record", "--root"])
        .arg(directory.path())
        .arg("--output")
        .arg(&report)
        .arg("--")
        .arg(std::env::current_exe().unwrap())
        .args(["--exact", "mutating_open_without_write_is_recorded"])
        .env("CLIBOX_FSPY_OPEN_FIXTURE", directory.path())
        .status()
        .unwrap();
    assert!(status.success());

    let record = record::parse(
        std::io::BufReader::new(fs::File::open(&report).unwrap()),
        1_000_000,
        256 * 1024 * 1024,
    )
    .unwrap();
    #[cfg(unix)]
    let expected = NativePath::UnixBytes(b"stamp".to_vec());
    #[cfg(windows)]
    let expected = NativePath::WindowsUtf16("stamp".encode_utf16().collect());
    assert!(record.operations.iter().any(|pair| {
        pair.start.operation == Operation::Open
            && pair.start.open_mutates
            && pair.completion.native_error.is_none()
            && pair
                .start
                .paths
                .iter()
                .any(|path| path.project_relative.as_ref() == Some(&expected))
    }));
}
