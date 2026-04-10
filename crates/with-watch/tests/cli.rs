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
fn commands_without_filesystem_inputs_guide_users_to_exec_input() {
    with_watch_command()
        .args(["echo", "hello"])
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
fn pathless_allowlist_command_runs_once_with_test_hook() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");

    with_watch_command()
        .current_dir(temp_dir.path())
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .args(["ls", "-l"])
        .assert()
        .success();
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
fn passthrough_cp_does_not_rerun_from_its_own_output_write() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    let output_path = temp_dir.path().join("output.txt");
    fs::write(&input_path, "alpha\n").expect("write input");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(temp_dir.path())
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .args([
            "cp",
            input_path.to_string_lossy().as_ref(),
            output_path.to_string_lossy().as_ref(),
        ])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_file_contents(&output_path, "alpha\n");
    thread::sleep(Duration::from_millis(150));
    assert!(child.try_wait().expect("poll child").is_none());

    fs::write(&input_path, "beta\n").expect("rewrite input");

    let status = wait_for_child_exit(&mut child, Duration::from_secs(10));
    assert!(status.success());
    assert_eq!(
        fs::read_to_string(&output_path).expect("read output"),
        "beta\n"
    );
}

#[cfg(unix)]
#[test]
fn passthrough_sed_in_place_does_not_loop_on_its_own_write() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    let marker_dir = temp_dir.path().join("markers");
    fs::write(&input_path, "alpha\n").expect("write input");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(temp_dir.path())
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .env("WITH_WATCH_TEST_RUN_MARKER_DIR", &marker_dir)
        .args([
            "sed",
            "-i.bak",
            "-e",
            "s/alpha/beta/",
            input_path.to_string_lossy().as_ref(),
        ])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_file_contents(&input_path, "beta\n");
    wait_for_path(&marker_dir.join("run-1.done"));
    assert!(child.try_wait().expect("poll child").is_none());

    fs::write(&input_path, "alpha\n").expect("rewrite input");
    wait_for_path(&marker_dir.join("run-2.done"));

    let status = wait_for_child_exit(&mut child, Duration::from_secs(10));
    assert!(status.success());
    assert_eq!(
        fs::read_to_string(&input_path).expect("read input"),
        "beta\n"
    );
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

    let status = wait_for_child_exit(&mut child, Duration::from_secs(10));
    assert!(status.success());

    let output = fs::read_to_string(&output_path).expect("read output");
    let lines = output.lines().collect::<Vec<_>>();
    assert_eq!(lines, vec!["alpha", "beta"]);
}

#[cfg(unix)]
#[test]
fn self_mutating_shell_command_reruns_after_external_change_during_execution() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    let marker_dir = temp_dir.path().join("markers");
    fs::write(&input_path, "alpha\n").expect("write input");

    let expression = format!(
        "sed -i.bak -e 's/alpha/beta/' '{}' && sleep 1",
        input_path.display()
    );

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(temp_dir.path())
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .env("WITH_WATCH_TEST_RUN_MARKER_DIR", &marker_dir)
        .args(["--shell", &expression])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_file_contents(&input_path, "beta\n");
    assert!(child.try_wait().expect("poll child").is_none());
    thread::sleep(Duration::from_millis(150));

    fs::write(&input_path, "alpha\n").expect("rewrite input during sleep");
    wait_for_path(&marker_dir.join("run-2.done"));

    let status = wait_for_child_exit(&mut child, Duration::from_secs(10));
    assert!(status.success());
    assert_eq!(
        fs::read_to_string(&input_path).expect("read input"),
        "beta\n"
    );
}

#[cfg(unix)]
fn wait_for_file_contents(path: &Path, expected_contents: &str) {
    for _ in 0..80 {
        if let Ok(contents) = fs::read_to_string(path) {
            if contents == expected_contents {
                return;
            }
        }
        thread::sleep(Duration::from_millis(25));
    }
    panic!(
        "timed out waiting for contents `{expected_contents}` in {}",
        path.display()
    );
}

#[cfg(unix)]
fn wait_for_path(path: &Path) {
    for _ in 0..400 {
        if path.exists() {
            return;
        }
        thread::sleep(Duration::from_millis(25));
    }
    panic!("timed out waiting for {}", path.display());
}

#[cfg(unix)]
fn wait_for_child_exit(
    child: &mut std::process::Child,
    timeout: Duration,
) -> std::process::ExitStatus {
    let deadline = std::time::Instant::now() + timeout;
    loop {
        if let Some(status) = child.try_wait().expect("poll child") {
            return status;
        }
        if std::time::Instant::now() >= deadline {
            child.kill().expect("kill child after timeout");
            panic!("timed out waiting for child process to exit");
        }
        thread::sleep(Duration::from_millis(25));
    }
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
