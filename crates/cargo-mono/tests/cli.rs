use assert_cmd::Command;
use predicates::prelude::*;

#[test]
fn help_lists_top_level_commands() {
    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("list"))
        .stdout(predicate::str::contains("changed"))
        .stdout(predicate::str::contains("bump"))
        .stdout(predicate::str::contains("publish"));
}

#[test]
fn help_succeeds_outside_workspace() {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");

    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .current_dir(temp_dir.path())
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("list"))
        .stdout(predicate::str::contains("changed"))
        .stdout(predicate::str::contains("bump"))
        .stdout(predicate::str::contains("publish"));
}

#[test]
fn version_succeeds_outside_workspace() {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");

    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .current_dir(temp_dir.path())
        .arg("--version")
        .assert()
        .success()
        .stdout(predicate::str::contains(env!("CARGO_PKG_VERSION")));
}

#[test]
fn list_still_requires_workspace() {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");

    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .current_dir(temp_dir.path())
        .arg("list")
        .assert()
        .failure()
        .stderr(predicate::str::contains("cargo metadata error"));
}

#[test]
fn list_outputs_workspace_packages() {
    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .args(["--output", "json", "list"])
        .assert()
        .success()
        .stdout(predicate::str::contains("\"packages\""))
        .stdout(predicate::str::contains("\"nodeup\""));
}

#[test]
fn changed_accepts_base_override() {
    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .args([
            "--output",
            "json",
            "changed",
            "--base",
            "HEAD",
            "--direct-only",
        ])
        .assert()
        .success()
        .stdout(predicate::str::contains("\"base_ref\": \"HEAD\""));
}
