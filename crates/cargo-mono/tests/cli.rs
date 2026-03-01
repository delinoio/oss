use std::{fs, path::Path, process::Command as ProcessCommand};

use assert_cmd::Command;
use predicates::prelude::*;

#[test]
fn help_lists_top_level_commands() {
    cargo_mono_command()
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

    cargo_mono_command()
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

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .arg("--version")
        .assert()
        .success()
        .stdout(predicate::str::contains(env!("CARGO_PKG_VERSION")));
}

#[test]
fn cargo_external_mode_help_succeeds_outside_workspace() {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");

    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .current_dir(temp_dir.path())
        .args(["mono", "--help"])
        .assert()
        .success()
        .stdout(predicate::str::contains("list"))
        .stdout(predicate::str::contains("changed"))
        .stdout(predicate::str::contains("bump"))
        .stdout(predicate::str::contains("publish"));
}

#[test]
fn cargo_external_mode_version_succeeds_outside_workspace() {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");

    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .current_dir(temp_dir.path())
        .args(["mono", "--version"])
        .assert()
        .success()
        .stdout(predicate::str::contains(env!("CARGO_PKG_VERSION")));
}

#[test]
fn list_still_requires_workspace() {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .arg("list")
        .assert()
        .failure()
        .stderr(predicate::str::contains("cargo metadata error"));
}

#[test]
fn list_outputs_workspace_packages() {
    cargo_mono_command()
        .args(["--output", "json", "list"])
        .assert()
        .success()
        .stdout(predicate::str::contains("\"packages\""))
        .stdout(predicate::str::contains("\"nodeup\""));
}

#[test]
fn cargo_external_mode_list_outputs_workspace_packages() {
    Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"))
        .args(["mono", "--output", "json", "list"])
        .assert()
        .success()
        .stdout(predicate::str::contains("\"packages\""))
        .stdout(predicate::str::contains("\"nodeup\""));
}

#[test]
fn changed_accepts_base_override() {
    cargo_mono_command()
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

#[test]
fn bump_succeeds_when_metadata_creates_untracked_lockfile() {
    let temp_dir = init_library_workspace();

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args(["bump", "--level", "patch", "--package", "alpha"])
        .assert()
        .success()
        .stderr(predicate::str::contains("Working tree is dirty").not());

    let status = git_status_short(temp_dir.path());
    assert!(
        status.contains("?? Cargo.lock"),
        "expected untracked Cargo.lock after metadata load, got:\n{status}"
    );
}

#[test]
fn publish_succeeds_when_metadata_creates_untracked_lockfile() {
    let temp_dir = init_library_workspace();

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args(["publish", "--dry-run", "--package", "alpha"])
        .assert()
        .success()
        .stderr(predicate::str::contains("Working tree is dirty").not());

    let status = git_status_short(temp_dir.path());
    assert!(
        status.contains("?? Cargo.lock"),
        "expected untracked Cargo.lock after metadata load, got:\n{status}"
    );
}

#[test]
fn bump_fails_on_preexisting_dirty_tree_without_allow_dirty() {
    let temp_dir = init_library_workspace();
    fs::write(temp_dir.path().join("scratch.txt"), "dirty\n").expect("failed to write scratch");

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args(["bump", "--level", "patch", "--package", "alpha"])
        .assert()
        .failure()
        .stderr(predicate::str::contains(
            "Working tree is dirty; re-run with --allow-dirty to bypass this check",
        ));

    let status = git_status_short(temp_dir.path());
    assert!(
        status.contains("?? scratch.txt"),
        "expected pre-existing dirty file in status, got:\n{status}"
    );
    assert!(
        !status.contains("Cargo.lock"),
        "did not expect Cargo.lock when preflight fails before metadata load, got:\n{status}"
    );
}

#[test]
fn publish_fails_on_preexisting_dirty_tree_without_allow_dirty() {
    let temp_dir = init_library_workspace();
    fs::write(temp_dir.path().join("scratch.txt"), "dirty\n").expect("failed to write scratch");

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args(["publish", "--dry-run", "--package", "alpha"])
        .assert()
        .failure()
        .stderr(predicate::str::contains(
            "Working tree is dirty; re-run with --allow-dirty to bypass this check",
        ));

    let status = git_status_short(temp_dir.path());
    assert!(
        status.contains("?? scratch.txt"),
        "expected pre-existing dirty file in status, got:\n{status}"
    );
    assert!(
        !status.contains("Cargo.lock"),
        "did not expect Cargo.lock when preflight fails before metadata load, got:\n{status}"
    );
}

#[test]
fn bump_dirty_tree_failure_still_logs_command_invocation() {
    let temp_dir = init_library_workspace();
    fs::write(temp_dir.path().join("scratch.txt"), "dirty\n").expect("failed to write scratch");

    let mut command = Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"));
    command.env("RUST_LOG", "cargo_mono=info");

    command
        .current_dir(temp_dir.path())
        .args(["bump", "--level", "patch", "--package", "alpha"])
        .assert()
        .failure()
        .stdout(predicate::str::contains("action=\"invoke-command\""))
        .stdout(predicate::str::contains("command_path=\"bump\""))
        .stderr(predicate::str::contains(
            "Working tree is dirty; re-run with --allow-dirty to bypass this check",
        ));
}

fn cargo_mono_command() -> Command {
    let mut command = Command::new(assert_cmd::cargo::cargo_bin!("cargo-mono"));
    command.env("RUST_LOG", "off");
    command
}

fn init_library_workspace() -> tempfile::TempDir {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");
    let root = temp_dir.path();
    let crate_dir = root.join("crates/alpha/src");

    fs::create_dir_all(&crate_dir).expect("failed to create crate directory");
    fs::write(
        root.join("Cargo.toml"),
        "[workspace]\nmembers = [\"crates/alpha\"]\nresolver = \"2\"\n",
    )
    .expect("failed to write workspace manifest");
    fs::write(
        root.join("crates/alpha/Cargo.toml"),
        "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\nedition = \"2021\"\npublish = false\n",
    )
    .expect("failed to write crate manifest");
    fs::write(
        root.join("crates/alpha/src/lib.rs"),
        "pub fn alpha() -> &'static str { \"alpha\" }\n",
    )
    .expect("failed to write crate source");

    run_git(root, &["init", "-q"]);
    run_git(root, &["add", "."]);
    run_git(
        root,
        &[
            "-c",
            "user.name=test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-q",
            "-m",
            "init",
        ],
    );

    temp_dir
}

fn git_status_short(working_dir: &Path) -> String {
    let output = ProcessCommand::new("git")
        .current_dir(working_dir)
        .args(["status", "--short"])
        .output()
        .expect("failed to run git status");

    assert!(
        output.status.success(),
        "git status failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );

    String::from_utf8_lossy(&output.stdout).to_string()
}

fn run_git(working_dir: &Path, args: &[&str]) {
    let output = ProcessCommand::new("git")
        .current_dir(working_dir)
        .args(args)
        .output()
        .expect("failed to run git command");

    assert!(
        output.status.success(),
        "git {} failed: {}",
        args.join(" "),
        String::from_utf8_lossy(&output.stderr)
    );
}
