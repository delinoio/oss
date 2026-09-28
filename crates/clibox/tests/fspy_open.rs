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

#[cfg(target_os = "macos")]
#[test]
fn invalid_path_pointers_keep_native_failures_paired() {
    if std::env::var_os("CLIBOX_FSPY_INVALID_PATH_FIXTURE").is_some() {
        let invalid = std::ptr::dangling::<libc::c_char>();
        let mut metadata = std::mem::MaybeUninit::<libc::stat>::uninit();
        assert_eq!(unsafe { libc::open(invalid, libc::O_RDONLY) }, -1);
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(libc::EFAULT)
        );
        assert_eq!(unsafe { libc::stat(invalid, metadata.as_mut_ptr()) }, -1);
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(libc::EFAULT)
        );
        let mut overlong = vec![b'a'; 4096];
        overlong.push(0);
        assert_eq!(
            unsafe { libc::open(overlong.as_ptr().cast(), libc::O_RDONLY) },
            -1
        );
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(libc::ENAMETOOLONG)
        );
        return;
    }
    let directory = tempfile::tempdir().unwrap();
    let report = directory.path().join("trace.ndjson");
    let status = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["fspy", "record", "--root"])
        .arg(directory.path())
        .arg("--output")
        .arg(&report)
        .arg("--")
        .arg(std::env::current_exe().unwrap())
        .args([
            "--exact",
            "invalid_path_pointers_keep_native_failures_paired",
        ])
        .env("CLIBOX_FSPY_INVALID_PATH_FIXTURE", "1")
        .status()
        .unwrap();
    assert!(status.success());
    let record = record::parse(
        std::io::BufReader::new(fs::File::open(&report).unwrap()),
        1_000_000,
        256 * 1024 * 1024,
    )
    .unwrap();
    for (operation, error, expected) in [
        (Operation::Open, libc::EFAULT, 1),
        (Operation::Metadata, libc::EFAULT, 1),
        (Operation::Open, libc::ENAMETOOLONG, 1),
    ] {
        assert!(
            record
                .operations
                .iter()
                .filter(|pair| pair.start.operation == operation
                    && pair.start.path_unavailable
                    && pair.completion.native_error == Some(error))
                .count()
                >= expected
        );
    }
}
