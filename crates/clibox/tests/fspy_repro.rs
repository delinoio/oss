#![cfg(any(target_os = "linux", target_os = "macos", target_os = "windows"))]

use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Output},
};

use clibox_fspy::record::NativePath;

// Keep native snapshot/candidate fixtures from competing for injection startup
// and cleanup deadlines while the enclosing Rust suite runs in parallel.
static REPRO_TEST_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

#[test]
fn directory_reproduction_workload() {
    let Some(directory) = std::env::var_os("CLIBOX_FSPY_DIRECTORY_REPRO") else {
        return;
    };
    if !directory.is_empty() {
        for entry in fs::read_dir(directory).unwrap() {
            entry.unwrap();
        }
    }
    if let Some(input) = std::env::var_os("CLIBOX_FSPY_DIRECTORY_INPUT") {
        assert_eq!(fs::read(input).unwrap(), b"x");
    }
    eprintln!("EXPECTED");
    std::process::exit(42);
}

fn reproduction_command(
    root: &Path,
    bundle: &Path,
    args: &[&str],
    directory: &str,
    input: Option<&str>,
) -> Command {
    // A test-owned image permits injection on macOS, where system tools are
    // protected.
    let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
    command
        .args(["fspy", "min-repro", "--root"])
        .arg(root)
        .arg("--bundle-dir")
        .arg(bundle)
        .args([
            "--expect-exit",
            "42",
            "--expect-stderr",
            "EXPECTED",
            "--timeout",
            "60s",
            "--json",
        ])
        .args(args)
        .arg("--")
        .arg(std::env::current_exe().unwrap())
        .args(["--exact", "directory_reproduction_workload", "--nocapture"])
        .env("CLIBOX_FSPY_DIRECTORY_REPRO", directory)
        .env_remove("CLIBOX_FSPY_DIRECTORY_INPUT")
        .current_dir(root);
    if let Some(input) = input {
        command.env("CLIBOX_FSPY_DIRECTORY_INPUT", input);
    }
    command
}

fn reproduce(
    root: &Path,
    bundle: &Path,
    args: &[&str],
    directory: &str,
    input: Option<&str>,
) -> Output {
    reproduction_command(root, bundle, args, directory, input)
        .output()
        .unwrap()
}

fn assert_verified(output: &Output) {
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        String::from_utf8_lossy(&output.stderr)
            .matches("EXPECTED")
            .count(),
        2
    );
    serde_json::from_slice::<serde_json::Value>(&output.stdout).unwrap();
}

fn manifest_directories(bundle: &Path) -> Vec<PathBuf> {
    let manifest: serde_json::Value =
        serde_json::from_slice(&fs::read(bundle.join(".clibox-fspy-repro/manifest.json")).unwrap())
            .unwrap();
    manifest["directories"]
        .as_array()
        .unwrap()
        .iter()
        .map(
            |path| match serde_json::from_value::<NativePath>(path.clone()).unwrap() {
                #[cfg(unix)]
                NativePath::UnixBytes(bytes) => {
                    use std::os::unix::ffi::OsStringExt;
                    PathBuf::from(std::ffi::OsString::from_vec(bytes))
                }
                #[cfg(windows)]
                NativePath::WindowsUtf16(units) => {
                    use std::os::windows::ffi::OsStringExt;
                    PathBuf::from(std::ffi::OsString::from_wide(&units))
                }
                _ => panic!("unexpected native path platform"),
            },
        )
        .collect()
}

#[test]
fn directory_queries_publish_only_selected_entries() {
    let _fixture = REPRO_TEST_LOCK
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("project");
    fs::create_dir_all(root.join("unused")).unwrap();
    fs::create_dir_all(root.join("excluded/empty")).unwrap();
    fs::create_dir_all(root.join("nested")).unwrap();
    fs::create_dir_all(root.join("selected-empty")).unwrap();
    fs::write(root.join("nested/input"), b"x").unwrap();
    let bundle = temporary.path().join("bundle");
    assert_verified(&reproduce(
        &root,
        &bundle,
        &[
            "--include",
            "**",
            "--exclude",
            "unused",
            "--exclude",
            "excluded",
            "--exclude",
            "excluded/**",
            "--exclude",
            "nested",
            "--max-snapshot-files",
            "2",
        ],
        ".",
        Some("nested/input"),
    ));
    assert!(!bundle.join("unused").exists());
    assert!(!bundle.join("excluded").exists());
    assert!(bundle.join("nested").is_dir());
    assert_eq!(fs::read(bundle.join("nested/input")).unwrap(), b"x");
    assert!(bundle.join("selected-empty").is_dir());
    assert_eq!(
        manifest_directories(&bundle),
        [PathBuf::from("selected-empty")]
    );
}

#[test]
fn one_file_snapshot_ignores_unrelated_directories() {
    let _fixture = REPRO_TEST_LOCK
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("project");
    fs::create_dir(&root).unwrap();
    fs::write(root.join("input"), b"x").unwrap();
    for extra in [false, true] {
        if extra {
            fs::create_dir_all(root.join("unused/nested/empty")).unwrap();
        }
        let bundle = temporary.path().join(format!("bundle-{extra}"));
        assert_verified(&reproduce(
            &root,
            &bundle,
            &["--include", "input", "--max-snapshot-files", "1"],
            "",
            Some("input"),
        ));
        assert_eq!(fs::read(bundle.join("input")).unwrap(), b"x");
        assert!(!bundle.join("unused").exists());
        assert!(manifest_directories(&bundle).is_empty());
    }
}

#[test]
fn explicitly_selected_observed_empty_directory_is_published() {
    let _fixture = REPRO_TEST_LOCK
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("project");
    fs::create_dir_all(root.join("empty")).unwrap();
    let bundle = temporary.path().join("bundle");
    assert_verified(&reproduce(
        &root,
        &bundle,
        &["--include", "empty", "--max-snapshot-files", "1"],
        "empty",
        None,
    ));
    assert!(bundle.join("empty").is_dir());
    assert_eq!(manifest_directories(&bundle), [PathBuf::from("empty")]);
}

#[test]
fn selected_input_limit_failures_leave_no_bundle_or_staging() {
    let _fixture = REPRO_TEST_LOCK
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("project");
    fs::create_dir_all(root.join("nested")).unwrap();
    fs::write(root.join("input"), b"x").unwrap();
    fs::write(root.join("nested/other"), b"x").unwrap();
    for (name, args, failure, runs) in [
        (
            "snapshot",
            vec![
                "--include",
                "input",
                "--include",
                "nested/other",
                "--max-snapshot-files",
                "1",
            ],
            "snapshot_file_limit",
            0,
        ),
        (
            "result",
            vec![
                "--include",
                "input",
                "--include",
                "nested/other",
                "--max-result-files",
                "1",
            ],
            "result_file_limit",
            2,
        ),
    ] {
        let bundle = temporary.path().join(name);
        let output = reproduce(&root, &bundle, &args, ".", Some("input"));
        assert_eq!(output.status.code(), Some(1));
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(stderr.contains(failure), "{stderr}");
        assert_eq!(stderr.matches("EXPECTED").count(), runs);
        assert!(!bundle.exists());
        assert_eq!(fs::read_dir(temporary.path()).unwrap().count(), 1);
    }
}

#[cfg(unix)]
#[test]
fn cancellation_during_large_snapshot_removes_private_staging() {
    let _fixture = REPRO_TEST_LOCK
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    use std::{
        process::Stdio,
        thread,
        time::{Duration, Instant},
    };

    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("project");
    let private = temporary.path().join("private");
    fs::create_dir(&root).unwrap();
    fs::create_dir(&private).unwrap();
    fs::File::create(root.join("large"))
        .unwrap()
        .set_len(512 * 1024 * 1024)
        .unwrap();
    let bundle = temporary.path().join("bundle");
    let mut child = reproduction_command(
        &root,
        &bundle,
        &["--include", "large", "--max-snapshot-files", "1"],
        "",
        None,
    )
    .env("TMPDIR", &private)
    .stdout(Stdio::piped())
    .stderr(Stdio::piped())
    .spawn()
    .unwrap();
    let deadline = Instant::now() + Duration::from_secs(30);
    // Wait for actual copied bytes, so this checks cancellation during the
    // snapshot rather than a signal sent before command handlers are installed.
    loop {
        let copying = fs::read_dir(&private).unwrap().any(|entry| {
            entry
                .unwrap()
                .path()
                .join("large")
                .metadata()
                .is_ok_and(|metadata| metadata.len() > 0)
        });
        if copying {
            break;
        }
        if Instant::now() >= deadline || child.try_wait().unwrap().is_some() {
            child.kill().ok();
            let output = child.wait_with_output().unwrap();
            panic!(
                "snapshot did not start: {}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
        thread::sleep(Duration::from_millis(2));
    }
    // SAFETY: this live test-owned child has a valid PID, and SIGINT is its
    // installed command-scoped cancellation signal.
    assert_eq!(unsafe { libc::kill(child.id() as i32, libc::SIGINT) }, 0);
    let deadline = Instant::now() + Duration::from_secs(10);
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            child.kill().unwrap();
            child.wait().unwrap();
            panic!("snapshot cancellation did not finish");
        }
        thread::sleep(Duration::from_millis(2));
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(
        output.status.code(),
        Some(130),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(String::from_utf8_lossy(&output.stderr).contains("cancelled"));
    assert!(!bundle.exists());
    assert_eq!(fs::read_dir(&private).unwrap().count(), 0);
    assert_eq!(fs::read_dir(temporary.path()).unwrap().count(), 2);
}

#[cfg(unix)]
#[test]
fn selected_directory_alias_preserves_unselected_target() {
    let _fixture = REPRO_TEST_LOCK
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("project");
    fs::create_dir_all(root.join("real")).unwrap();
    std::os::unix::fs::symlink("real", root.join("alias")).unwrap();
    let bundle = temporary.path().join("bundle");
    assert_verified(&reproduce(
        &root,
        &bundle,
        &["--include", "alias", "--max-snapshot-files", "2"],
        "alias",
        None,
    ));
    assert!(bundle.join("alias").is_symlink());
    assert!(bundle.join("real").is_dir());
    assert_eq!(manifest_directories(&bundle), [PathBuf::from("alias")]);
}
