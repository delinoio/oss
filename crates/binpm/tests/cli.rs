use std::fs;

use assert_cmd::Command;
use predicates::prelude::*;
use sha2::{Digest, Sha256};

fn binpm() -> Command {
    Command::new(env!("CARGO_BIN_EXE_binpm"))
}

#[test]
fn help_includes_initial_command_surface() {
    let mut command = binpm();

    command
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("install"))
        .stdout(predicate::str::contains("cache"))
        .stdout(predicate::str::contains("verify"))
        .stdout(predicate::str::contains("env"));
}

#[test]
fn init_writes_minimal_manifest() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .arg("init")
        .assert()
        .success()
        .stdout(predicate::str::contains("created"));

    let manifest =
        std::fs::read_to_string(temp_dir.path().join("binpm.toml")).expect("read manifest");
    assert_eq!(manifest, "version = 1\n");
}

#[test]
fn init_from_nested_directory_writes_manifest_at_git_root() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("init")
        .assert()
        .success()
        .stdout(predicate::str::contains("created"));

    let manifest = fs::read_to_string(temp_dir.path().join("binpm.toml")).expect("read manifest");
    assert_eq!(manifest, "version = 1\n");
    assert!(!nested_dir.join("binpm.toml").exists());
}

#[test]
fn env_prints_shell_path_exports() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let local_bin = fs::canonicalize(temp_dir.path())
        .expect("canonical temp dir")
        .join(".binpm")
        .join("bin");
    let expected = format!(
        "export PATH='{}':'/tmp/binpm-home/bin':\"$PATH\"",
        local_bin.display()
    );
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", "/tmp/binpm-home")
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected));
}

#[test]
fn env_from_nested_directory_uses_git_root_local_bin() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let canonical_root = fs::canonicalize(temp_dir.path()).expect("canonical temp dir");
    let canonical_nested = fs::canonicalize(&nested_dir).expect("canonical nested dir");
    let root_bin = canonical_root.join(".binpm").join("bin");
    let nested_bin = canonical_nested.join(".binpm").join("bin");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .env("BINPM_HOME", "/tmp/binpm-home")
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(root_bin.display().to_string()))
        .stdout(predicate::str::contains(nested_bin.display().to_string()).not());
}

#[test]
fn env_from_nested_directory_uses_manifest_ancestor_without_git() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let canonical_root = fs::canonicalize(temp_dir.path()).expect("canonical temp dir");
    let canonical_nested = fs::canonicalize(&nested_dir).expect("canonical nested dir");
    let root_bin = canonical_root.join(".binpm").join("bin");
    let nested_bin = canonical_nested.join(".binpm").join("bin");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .env("BINPM_HOME", "/tmp/binpm-home")
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(root_bin.display().to_string()))
        .stdout(predicate::str::contains(nested_bin.display().to_string()).not());
}

#[test]
fn cache_key_from_nested_directory_uses_git_root_lockfile() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let expected_digest = format!("{:x}", Sha256::digest(b"root lock\n"));
    let empty_digest = format!("{:x}", Sha256::digest([]));
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .args(["cache", "key"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected_digest))
        .stdout(predicate::str::contains(empty_digest).not());
}

#[test]
fn cache_key_from_nested_directory_uses_manifest_ancestor_lockfile_without_git() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let expected_digest = format!("{:x}", Sha256::digest(b"root lock\n"));
    let empty_digest = format!("{:x}", Sha256::digest([]));
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .args(["cache", "key"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected_digest))
        .stdout(predicate::str::contains(empty_digest).not());
}

#[test]
fn doctor_from_nested_directory_reports_git_root_state() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("doctor")
        .assert()
        .success()
        .stdout(predicate::str::contains("manifest: present"))
        .stdout(predicate::str::contains("lockfile: present"));
}

#[test]
fn env_escapes_bash_paths_before_printing_shell_code() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm $(touch x) `cmd` 'home'");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains("'\\''home'\\''/bin'"))
        .stdout(predicate::str::contains("\"$PATH\""));
}

#[test]
fn env_fish_preserves_paths_before_directories_exist() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let local_bin = fs::canonicalize(temp_dir.path())
        .expect("canonical temp dir")
        .join(".binpm")
        .join("bin");
    let expected = format!(
        "set -gx PATH '{}' '/tmp/binpm-home/bin' $PATH",
        local_bin.display()
    );
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", "/tmp/binpm-home")
        .args(["env", "--shell", "fish"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected))
        .stdout(predicate::str::contains("fish_add_path").not());
}

#[test]
fn env_powershell_uses_runtime_path_separator() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", "/tmp/binpm-home")
        .args(["env", "--shell", "powershell"])
        .assert()
        .success()
        .stdout(predicate::str::contains("[System.IO.Path]::PathSeparator"))
        .stdout(predicate::str::contains(" + ';' + ").not());
}

#[test]
fn install_validates_source_before_not_implemented_error() {
    let mut command = binpm();

    command
        .args(["install", "not-a-source"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("Invalid source spec"));
}
