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
fn long_help_lists_command_inventory_sections() {
    with_watch_command()
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("--shell"))
        .stdout(predicate::str::contains("exec"))
        .stdout(predicate::str::contains("--no-hash"))
        .stdout(predicate::str::contains("--clear"))
        .stdout(predicate::str::contains("Wrapper commands:"))
        .stdout(predicate::str::contains(
            "env, nice, nohup, stdbuf, timeout",
        ))
        .stdout(predicate::str::contains(
            "Dedicated built-in adapters and aliases:",
        ))
        .stdout(predicate::str::contains("cp, mv, install"))
        .stdout(predicate::str::contains("grep, egrep, fgrep, rg, ag"))
        .stdout(predicate::str::contains("fd, xargs"))
        .stdout(predicate::str::contains("protoc, flatc, thrift, capnp"))
        .stdout(predicate::str::contains("find, ls, dir, vdir, du"))
        .stdout(predicate::str::contains(
            "Recognized but not auto-watchable commands:",
        ))
        .stdout(predicate::str::contains("echo, printf, seq, yes, sleep"))
        .stdout(predicate::str::contains("exec --input escape hatch:"));
}

#[test]
fn short_help_stays_compact() {
    with_watch_command()
        .arg("-h")
        .assert()
        .success()
        .stdout(predicate::str::contains("--shell"))
        .stdout(predicate::str::contains("exec"))
        .stdout(predicate::str::contains("--no-hash"))
        .stdout(predicate::str::contains("--clear"))
        .stdout(predicate::str::contains("Wrapper commands:").not())
        .stdout(predicate::str::contains("Recognized but not auto-watchable commands:").not());
}

#[cfg(unix)]
#[test]
fn tracing_logs_are_off_by_default() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "hello\n").expect("write input");

    with_watch_command()
        .env_remove("WW_LOG")
        .env_remove("RUST_LOG")
        .env("WITH_WATCH_LOG_COLOR", "never")
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("cat")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("hello"))
        .stdout(predicate::str::contains("Starting with-watch run loop").not());
}

#[cfg(unix)]
#[test]
fn ww_log_can_enable_startup_logs() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "hello\n").expect("write input");

    with_watch_command()
        .env("WW_LOG", "with_watch=info")
        .env_remove("RUST_LOG")
        .env("WITH_WATCH_LOG_COLOR", "never")
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("cat")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("hello"))
        .stdout(predicate::str::contains("Starting with-watch run loop"));
}

#[cfg(unix)]
#[test]
fn rust_log_does_not_enable_startup_logs() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "hello\n").expect("write input");

    with_watch_command()
        .env_remove("WW_LOG")
        .env("RUST_LOG", "with_watch=info")
        .env("WITH_WATCH_LOG_COLOR", "never")
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("cat")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("hello"))
        .stdout(predicate::str::contains("Starting with-watch run loop").not());
}

#[cfg(unix)]
#[test]
fn sort_control_options_do_not_inflate_runtime_inferred_input_count() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "b 2\na 1\n").expect("write input");

    with_watch_command()
        .env("WW_LOG", "with_watch=debug")
        .env("WITH_WATCH_LOG_COLOR", "never")
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("sort")
        .arg("-k")
        .arg("2,2")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("Built command analysis"))
        .stdout(predicate::str::contains("adapter_id=\"sort\""))
        .stdout(predicate::str::contains("inferred_input_count=1"));
}

#[cfg(unix)]
#[test]
fn uniq_control_options_do_not_inflate_runtime_inferred_input_count() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "aa hello\naa hello\n").expect("write input");

    with_watch_command()
        .env("WW_LOG", "with_watch=debug")
        .env("WITH_WATCH_LOG_COLOR", "never")
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("uniq")
        .arg("-f")
        .arg("1")
        .arg("-s")
        .arg("2")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("Built command analysis"))
        .stdout(predicate::str::contains("adapter_id=\"uniq\""))
        .stdout(predicate::str::contains("inferred_input_count=1"));
}

#[cfg(unix)]
#[test]
fn touch_time_option_does_not_inflate_runtime_inferred_input_count() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");

    with_watch_command()
        .current_dir(temp_dir.path())
        .env("WW_LOG", "with_watch=debug")
        .env("WITH_WATCH_LOG_COLOR", "never")
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .arg("touch")
        .arg("-t")
        .arg("202401010101")
        .arg(&input_path)
        .assert()
        .success()
        .stdout(predicate::str::contains("Built command analysis"))
        .stdout(predicate::str::contains("adapter_id=\"touch\""))
        .stdout(predicate::str::contains("inferred_input_count=1"));
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
fn passthrough_mode_runs_immediately_once_at_startup_with_test_hook() {
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
fn clear_flag_keeps_non_terminal_output_clean_during_initial_run() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    fs::write(&input_path, "hello\n").expect("write input");

    with_watch_command()
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .args(["--clear", "cat", input_path.to_string_lossy().as_ref()])
        .assert()
        .success()
        .stdout(predicate::str::contains("hello"))
        .stdout(predicate::str::contains("\u{1b}[2J").not())
        .stdout(predicate::str::contains("\u{1b}[H").not());
}

#[cfg(unix)]
#[test]
fn pathless_allowlist_command_runs_immediately_once_at_startup_with_test_hook() {
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
fn ls_reruns_when_an_immediate_child_changes() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let watch_dir = temp_dir.path().join("watch");
    let marker_dir = temp_dir.path().join("aux").join("markers");
    fs::create_dir_all(&watch_dir).expect("create watch dir");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(&watch_dir)
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .env("WITH_WATCH_TEST_RUN_MARKER_DIR", &marker_dir)
        .arg("ls")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_path(&marker_dir.join("run-1.done"));
    assert!(child.try_wait().expect("poll child").is_none());

    fs::write(watch_dir.join("created.txt"), "alpha\n").expect("write immediate child");
    wait_for_path(&marker_dir.join("run-2.done"));

    let status = wait_for_child_exit(&mut child, Duration::from_secs(10));
    assert!(status.success());
}

#[cfg(unix)]
#[test]
fn ls_does_not_rerun_for_nested_descendant_changes() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let watch_dir = temp_dir.path().join("watch");
    let marker_dir = temp_dir.path().join("aux").join("markers");
    let subdir = watch_dir.join("subdir");
    fs::create_dir_all(&subdir).expect("create subdir");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(&watch_dir)
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .env("WITH_WATCH_TEST_RUN_MARKER_DIR", &marker_dir)
        .arg("ls")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_path(&marker_dir.join("run-1.done"));
    fs::write(subdir.join("nested.txt"), "alpha\n").expect("write nested file");

    assert_path_stays_absent(&marker_dir.join("run-2.done"), Duration::from_millis(300));
    assert!(child.try_wait().expect("poll child").is_none());

    child.kill().expect("kill child");
    child.wait().expect("wait for child");
}

#[cfg(unix)]
#[test]
fn ls_recursive_reruns_for_nested_descendant_changes() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let watch_dir = temp_dir.path().join("watch");
    let marker_dir = temp_dir.path().join("aux").join("markers");
    let subdir = watch_dir.join("subdir");
    fs::create_dir_all(&subdir).expect("create subdir");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(&watch_dir)
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .env("WITH_WATCH_TEST_RUN_MARKER_DIR", &marker_dir)
        .args(["ls", "-R"])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_path(&marker_dir.join("run-1.done"));
    fs::write(subdir.join("nested.txt"), "alpha\n").expect("write nested file");
    wait_for_path(&marker_dir.join("run-2.done"));

    let status = wait_for_child_exit(&mut child, Duration::from_secs(10));
    assert!(status.success());
}

#[cfg(unix)]
#[test]
fn ls_directory_mode_does_not_rerun_for_directory_contents_changes() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let root_dir = temp_dir.path().join("root");
    let marker_dir = temp_dir.path().join("aux").join("markers");
    let listed_dir = root_dir.join("dir");
    fs::create_dir_all(&listed_dir).expect("create listed dir");

    let mut child = ProcessCommand::new(assert_cmd::cargo::cargo_bin!("with-watch"))
        .current_dir(&root_dir)
        .env("WITH_WATCH_TEST_MAX_RUNS", "2")
        .env("WITH_WATCH_TEST_DEBOUNCE_MS", "25")
        .env("WITH_WATCH_TEST_RUN_MARKER_DIR", &marker_dir)
        .args(["ls", "-d", "dir"])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn with-watch");

    wait_for_path(&marker_dir.join("run-1.done"));
    fs::write(listed_dir.join("child.txt"), "alpha\n").expect("write nested child");

    assert_path_stays_absent(&marker_dir.join("run-2.done"), Duration::from_millis(300));
    assert!(child.try_wait().expect("poll child").is_none());

    child.kill().expect("kill child");
    child.wait().expect("wait for child");
}

#[cfg(unix)]
#[test]
fn shell_mode_runs_immediately_once_at_startup_with_test_hook() {
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
fn exec_mode_runs_immediately_once_at_startup_with_test_hook() {
    let temp_dir = tempfile::tempdir().expect("create tempdir");
    let input_path = temp_dir.path().join("input.txt");
    let output_path = temp_dir.path().join("output.txt");
    fs::write(&input_path, "alpha\n").expect("write input");

    with_watch_command()
        .current_dir(temp_dir.path())
        .env("WITH_WATCH_TEST_MAX_RUNS", "1")
        .args([
            "exec",
            "--input",
            input_path.to_string_lossy().as_ref(),
            "--",
            "sh",
            "-c",
            &format!(
                "cat '{}' > '{}'",
                input_path.display(),
                output_path.display()
            ),
        ])
        .assert()
        .success();

    assert_eq!(
        fs::read_to_string(&output_path).expect("read output"),
        "alpha\n"
    );
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
fn assert_path_stays_absent(path: &Path, duration: Duration) {
    let deadline = std::time::Instant::now() + duration;
    while std::time::Instant::now() < deadline {
        assert!(!path.exists(), "expected {} to stay absent", path.display());
        thread::sleep(Duration::from_millis(25));
    }
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
