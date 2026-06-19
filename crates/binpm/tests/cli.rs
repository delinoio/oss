use assert_cmd::Command;
use predicates::prelude::*;

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
fn env_prints_shell_path_exports() {
    let mut command = binpm();

    command
        .env("BINPM_HOME", "/tmp/binpm-home")
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(
            ".binpm/bin:/tmp/binpm-home/bin:$PATH",
        ));
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
