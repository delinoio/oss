use std::{
    fs,
    path::Path,
    process::{Command as ProcessCommand, Stdio},
    thread,
    time::Duration,
};

use assert_cmd::Command;
use predicates::prelude::*;

fn with_watch_command() -> Command {
    Command::new(assert_cmd::cargo::cargo_bin!("with-watch"))
}

#[test]
fn help_lists_shell_and_exec_modes() {
    with_watch_command()
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("--shell"))
        .stdout(predicate::str::contains("exec"))
        .stdout(predicate::str::contains("--no-hash"));
}

#[test]
fn pathless_passthrough_guides_users_to_exec_input() {
    with_watch_command()
        .args(["ls", "-l"])
        .assert()
        .failure()
        .stderr(predicate::str::contains(
            "No watch inputs could be inferred from the delegated command",
        ))
        .stderr(predicate::str::contains("with-watch exec --input"));
}

#[test]
fn shell_and_subcommand_cannot_be_combined() {
    with_watch_command()
        .args([
            "--shell",
            "echo hi",
            "exec",
            "--input",
            "src/**/*.rs",
            "--",
            "echo",
        ])
        .assert()
        .failure()
        .stderr(predicate::str::contains("cannot be combined"));
}

#[cfg(unix)]
#[test]
fn passthrough_mode_runs_a_posix_utility_once_with_test_hook() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "hello\n").expect("write input");

    with_watch_command()
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("cat")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("hello"));
}

#[cfg(unix)]
#[test]
fn shell_mode_supports_operators_and_exits_after_one_run_with_test_hook() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "hello\n").expect("write input");

    with_watch_command()
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .args([
            "--shell",
            &format!("cat '{}' | grep hello", input_path.display()),
        ])
        .assert()
        .success()
        .stdout(predicate::str::contains("hello"));
}

#[cfg(unix)]
#[test]
fn exec_mode_reruns_when_an_explicit_input_changes() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    let output_path = temp_dir.path().join("output.txt");
    fs::write(&input_path, "alpha\n").expect("write input");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(temp_dir.path())
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .args([
            "exec",
            "--input",
            input_path.to_string_lossy().as_ref(),
            "--",
            "sh",
            "-c",
            &format!(
                "cat '{}' >> '{}'",
                input_path.display(),
                output_path.display()
            ),
        ])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_file_lines(&output_path, 1);
    thread::sleep(Duration::from_millis(50));
    fs::write(&input_path, "beta\n").expect("rewrite input");

    let status = child.wait().expect("wait for child");
    assert!(status.success());

    let output = fs::read_to_string(&output_path).expect("read output");
    let lines = output.lines().collect::<Vec<_>>();
    assert_eq!(lines, vec!["alpha", "beta"]);
}

#[cfg(unix)]
fn wait_for_file_lines(path: &Path, expected_lines: usize) {
    for _ in 0..80 {
        if let Ok(contents) = fs::read_to_string(path) {
            if contents.lines().count() >= expected_lines {
                return;
            }
        }
        thread::sleep(Duration::from_millis(25));
    }
    panic!(
        "timed out waiting for {expected_lines} lines in {}",
        path.display()
    );
}
