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
