#![cfg(feature = "test-support")]
use std::{
    fs,
    io::Write,
    path::Path,
    process::{Command, Output, Stdio},
};

use serde_json::Value;
fn binary() -> &'static str {
    env!("CARGO_BIN_EXE_runlens")
}

#[tokio::test]
async fn tracing_initialization_failure_does_not_launch_the_target() {
    let root = tempfile::tempdir().unwrap();
    let occupied = root.path().join("not-a-directory");
    fs::write(&occupied, "occupied").unwrap();
    let mut command = fspy::Command::new(fixture());
    command
        .args(["read-write"])
        .current_dir(root.path())
        .envs(std::env::vars_os());
    let result = command
        .spawn_in(
            &occupied,
            8192,
            16,
            tokio_util::sync::CancellationToken::new(),
        )
        .await;
    assert!(result.is_err());
    assert!(!root.path().join("out").exists());
    assert_eq!(fs::read_to_string(occupied).unwrap(), "occupied");
}
fn fixture() -> &'static str {
    env!("CARGO_BIN_EXE_runlens-test-command")
}
fn invoke(root: &Path, args: &[&str]) -> Output {
    Command::new(binary())
        .args(args)
        .current_dir(root)
        .output()
        .unwrap()
}
fn run(root: &Path, report: &str, mode: &str) -> Output {
    invoke(
        root,
        &[
            "--log-level",
            "off",
            "run",
            "--save",
            report,
            "--",
            fixture(),
            mode,
        ],
    )
}
fn parse(root: &Path, path: &str) -> Value {
    serde_json::from_slice(&fs::read(root.join(path)).unwrap()).unwrap()
}
fn git(root: &Path, args: &[&str]) {
    let status = Command::new("git")
        .args([
            "-c",
            "core.hooksPath=/dev/null",
            "-c",
            "user.name=Runlens fixture",
            "-c",
            "user.email=runlens@example.invalid",
        ])
        .args(args)
        .current_dir(root)
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(status.success());
}
fn repository(mode: &str) -> tempfile::TempDir {
    let root = tempfile::tempdir().unwrap();
    fs::write(
        root.path().join("input.txt"),
        "input content must not be retained",
    )
    .unwrap();
    fs::write(root.path().join(".gitignore"), "ignored\n*.json\n").unwrap();
    let tool = fixture().replace('\\', "/");
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            "schema_version = 1\n[commands.build]\nargv = [{tool:?}, {mode:?}]\ninputs = \
             [\"input.txt\"]\noutputs = [\"out/**\"]\n"
        ),
    )
    .unwrap();
    git(root.path(), &["init", "-q"]);
    git(root.path(), &["add", "."]);
    git(root.path(), &["commit", "-qm", "source"]);
    root
}
#[test]
fn real_tracing_receipt_privacy_and_offline_queries() {
    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "FILE-BODY-CANARY").unwrap();
    fs::write(root.path().join(".gitignore"), "out\n").unwrap();
    let output = run(root.path(), "report.json", "child");
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        String::from_utf8_lossy(&output.stdout),
        "child stdout preserved\n"
    );
    assert!(String::from_utf8_lossy(&output.stderr).contains("child stderr preserved"));
    let value = parse(root.path(), "report.json");
    let execution = &value["executions"][0];
    assert_eq!(execution["outcome"]["collection_complete"], true);
    assert_eq!(
        execution["accesses"]["${workspace}/input.txt"]["read"],
        true
    );
    assert_eq!(
        execution["accesses"]["${workspace}/missing.txt"]["read"],
        true
    );
    assert_eq!(
        execution["changes"]["${workspace}/out/result.txt"],
        "created"
    );
    let bytes = fs::read(root.path().join("report.json")).unwrap();
    assert!(!String::from_utf8_lossy(&bytes).contains("FILE-BODY-CANARY"));
    assert!(!String::from_utf8_lossy(&bytes).contains("child stdout preserved"));
    for args in [
        vec!["receipt", "report.json", "--json"],
        vec!["compare", "report.json", "report.json", "--json"],
        vec!["explain", "input.txt", "--report", "report.json", "--json"],
        vec![
            "conflicts",
            "--report",
            "report.json",
            "--report",
            "report.json",
            "--json",
        ],
    ] {
        let result = invoke(root.path(), &args);
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        serde_json::from_slice::<Value>(&result.stdout).unwrap();
    }
    let exported = invoke(
        root.path(),
        &[
            "export",
            "report.json",
            "--format",
            "html",
            "--output",
            "report.html",
        ],
    );
    assert!(exported.status.success());
    let html = fs::read_to_string(root.path().join("report.html")).unwrap();
    assert!(html.contains("default-src 'none'"));
    assert!(html.contains("<main id=\"main\">"));
    assert!(!html.contains("<script"));
}
#[test]
fn failures_timeouts_and_lingering_children_are_not_success() {
    let root = tempfile::tempdir().unwrap();
    let failed = run(root.path(), "failed.json", "fail");
    assert_eq!(failed.status.code(), Some(1));
    assert_eq!(
        parse(root.path(), "failed.json")["executions"][0]["outcome"]["child_exit_code"],
        23
    );
    let timed = invoke(
        root.path(),
        &[
            "--log-level",
            "off",
            "run",
            "--timeout-ms",
            "50",
            "--save",
            "timed.json",
            "--",
            fixture(),
            "sleep",
        ],
    );
    assert_eq!(
        timed.status.code(),
        Some(6),
        "{}",
        String::from_utf8_lossy(&timed.stderr)
    );
    assert_eq!(
        parse(root.path(), "timed.json")["executions"][0]["outcome"]["collection_complete"],
        false
    );
    let started = std::time::Instant::now();
    let linger = run(root.path(), "linger.json", "linger");
    assert!(!linger.status.success());
    assert!(started.elapsed().as_secs() < 15);
    assert_eq!(
        parse(root.path(), "linger.json")["executions"][0]["outcome"]["collection_complete"],
        false
    );
}
#[test]
fn default_run_leaves_no_report_and_collision_does_not_execute() {
    let root = tempfile::tempdir().unwrap();
    let output = invoke(
        root.path(),
        &["--log-level", "off", "run", "--", fixture(), "read"],
    );
    assert!(output.status.success());
    assert_eq!(fs::read_dir(root.path()).unwrap().count(), 0);
    fs::write(root.path().join("existing.json"), "preserved").unwrap();
    let output = run(root.path(), "existing.json", "read-write");
    assert_eq!(output.status.code(), Some(8));
    assert_eq!(
        fs::read_to_string(root.path().join("existing.json")).unwrap(),
        "preserved"
    );
    assert!(!root.path().join("out").exists());
}
#[test]
fn clean_and_repeat_use_fresh_environments_without_worktree_changes() {
    let root = repository("env");
    fs::write(root.path().join("input.txt"), "uncommitted").unwrap();
    fs::write(root.path().join("ignored"), "ignored state").unwrap();
    let output = Command::new(binary())
        .args([
            "--log-level",
            "off",
            "verify",
            "repeat",
            "build",
            "--save",
            "repeat.json",
        ])
        .env("RUNLENS_AMBIENT_SECRET", "MUST-NOT-COPY")
        .current_dir(root.path())
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = parse(root.path(), "repeat.json");
    assert_eq!(value["verification"], "passed");
    assert_eq!(value["executions"].as_array().unwrap().len(), 3);
    assert!(!root.path().join("out").exists());
    assert_eq!(
        fs::read_to_string(root.path().join("input.txt")).unwrap(),
        "uncommitted"
    );
    assert_eq!(
        fs::read_to_string(root.path().join("ignored")).unwrap(),
        "ignored state"
    );
    for execution in value["executions"].as_array().unwrap() {
        assert_eq!(execution["environment"]["working_tree_included"], false);
        assert!(execution["before"].get("${workspace}/ignored").is_none());
    }
    let output = invoke(
        root.path(),
        &[
            "--log-level",
            "off",
            "verify",
            "clean",
            "build",
            "--include-working-tree",
            "--save",
            "clean.json",
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = parse(root.path(), "clean.json");
    assert_eq!(
        value["executions"][0]["environment"]["working_tree_included"],
        true
    );
    assert!(
        value["executions"][0]["before"]
            .get("${workspace}/ignored")
            .is_none()
    );
}
#[test]
fn clean_baselines_require_matching_source_selection() {
    let root = repository("read");
    assert!(
        invoke(
            root.path(),
            &["verify", "clean", "build", "--save", "baseline.json"]
        )
        .status
        .success()
    );
    let verify = |extra: &[&str], report: &str| {
        let mut args = vec![
            "verify",
            "clean",
            "build",
            "--baseline",
            "baseline.json",
            "--save",
            report,
        ];
        args.extend_from_slice(extra);
        invoke(root.path(), &args)
    };
    let matching = verify(&[], "matching.json");
    assert!(
        matching.status.success(),
        "{}",
        String::from_utf8_lossy(&matching.stderr)
    );
    assert_eq!(
        parse(root.path(), "matching.json")["verification"],
        "passed"
    );

    // Even identical source bytes cannot establish equivalence across source
    // policies.
    let included = verify(&["--include-working-tree"], "included.json");
    assert_eq!(
        included.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&included.stderr)
    );
    assert_eq!(
        parse(root.path(), "included.json")["verification"],
        "inconclusive"
    );

    git(
        root.path(),
        &[
            "commit",
            "--allow-empty",
            "-qm",
            "same tree, new source revision",
        ],
    );
    let revised = verify(&[], "revised.json");
    assert_eq!(
        revised.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&revised.stderr)
    );
    assert_eq!(
        parse(root.path(), "revised.json")["verification"],
        "inconclusive"
    );
    assert!(!root.path().join("out").exists());
}
#[test]
fn clean_policy_failures_survive_inconclusive_baselines() {
    let root = repository("read");
    assert!(
        invoke(
            root.path(),
            &["verify", "clean", "build", "--save", "baseline.json"]
        )
        .status
        .success()
    );
    let baseline = parse(root.path(), "baseline.json");
    let mut incompatible = baseline.clone();
    incompatible["executions"][0]["environment"]["architecture"] = "different-architecture".into();
    let mut incomplete = baseline;
    incomplete["executions"][0]["outcome"]["collection_complete"] = false.into();
    incomplete["verification"] = "inconclusive".into();
    let config_path = root.path().join("runlens.toml");
    let config = fs::read_to_string(&config_path).unwrap();
    fs::write(
        &config_path,
        format!("{config}\n[policy]\ndeny_reads = [\"input.txt\"]\n"),
    )
    .unwrap();
    for (index, baseline) in [incompatible, incomplete].into_iter().enumerate() {
        let baseline_name = format!("baseline-{index}.json");
        fs::write(
            root.path().join(&baseline_name),
            serde_json::to_vec(&baseline).unwrap(),
        )
        .unwrap();
        let result_name = format!("result-{index}.json");
        let output = invoke(
            root.path(),
            &[
                "verify",
                "clean",
                "build",
                "--baseline",
                &baseline_name,
                "--save",
                &result_name,
            ],
        );
        assert_eq!(
            output.status.code(),
            Some(5),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let result = runlens::report::read(&root.path().join(result_name)).unwrap();
        assert_eq!(result.verification, Some(runlens::model::Verdict::Failed));
        assert!(result.findings.iter().any(|entry| {
            let (_, finding) = entry.unwrap();
            finding.code == runlens::model::FindingCode::ReadBoundary
                && finding.classification == runlens::model::Classification::Violation
        }));
    }
}
#[test]
#[cfg(unix)]
fn non_unicode_environment_entries_do_not_abort_observation() {
    use std::{ffi::OsString, os::unix::ffi::OsStringExt};

    let root = repository("read");
    for args in [
        vec![
            "run",
            "--save",
            "direct.json",
            "--",
            fixture(),
            "read",
            "input.txt",
            "ENVIRONMENT-SECRET-CANARY",
        ],
        vec!["verify", "clean", "build", "--save", "clean.json"],
    ] {
        let output = Command::new(binary())
            .args(args)
            .env(
                OsString::from_vec(b"RUNLENS_INVALID_\xff".to_vec()),
                "NONUNICODE-KEY-CANARY",
            )
            .env(
                "RUNLENS_INVALID_VALUE",
                OsString::from_vec(b"NONUNICODE-VALUE-\xff".to_vec()),
            )
            .env("RUNLENS_SECRET", "ENVIRONMENT-SECRET-CANARY")
            .current_dir(root.path())
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(!String::from_utf8_lossy(&output.stderr).contains("CANARY"));
    }
    for name in ["direct.json", "clean.json"] {
        let bytes = fs::read_to_string(root.path().join(name)).unwrap();
        assert!(!bytes.contains("CANARY"));
        assert!(!bytes.contains("NONUNICODE-VALUE"));
        assert!(runlens::report::read(&root.path().join(name)).is_ok());
    }
    let report = parse(root.path(), "direct.json");
    assert_eq!(report["executions"][0]["command"]["argv"][3], "[redacted]");
}
#[test]
fn cache_policy_and_overflow_fail_closed() {
    let root = repository("read-write");
    let recorded = run(root.path(), "report.json", "read-write");
    assert!(recorded.status.success());
    let output = invoke(
        root.path(),
        &[
            "cache",
            "check",
            "report.json",
            "--command",
            "build",
            "--json",
        ],
    );
    assert_eq!(output.status.code(), Some(5));
    let value: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(
        value["findings"]
            .as_object()
            .unwrap()
            .values()
            .any(|finding| finding["code"] == "undeclared-input")
    );
    fs::write(
        root.path().join("runlens.toml"),
        "schema_version=1\n[policy]\ndeny_writes=[\"out/**\"]\n",
    )
    .unwrap();
    let output = invoke(root.path(), &["policy", "check", "report.json", "--json"]);
    assert_eq!(output.status.code(), Some(5));
    fs::write(
        root.path().join("runlens.toml"),
        "schema_version=1\n[limits]\nmemory_bytes=8192\ntotal_bytes=12288\nmax_paths=1000000\n",
    )
    .unwrap();
    let overflow = run(root.path(), "overflow.json", "overflow");
    assert_eq!(
        overflow.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&overflow.stderr)
    );
    let value = parse(root.path(), "overflow.json");
    assert_eq!(value["executions"][0]["outcome"]["child_exit_code"], 0);
    assert_eq!(
        value["executions"][0]["outcome"]["collection_complete"],
        false
    );
    assert!(
        !value["executions"][0]["accesses"]
            .as_object()
            .unwrap()
            .is_empty()
    );
}
#[test]
fn piped_stdin_is_forwarded_without_capture() {
    let root = tempfile::tempdir().unwrap();
    let mut child = Command::new(binary())
        .args([
            "--log-level",
            "off",
            "run",
            "--save",
            "report.json",
            "--",
            fixture(),
            "stdin",
        ])
        .current_dir(root.path())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(b"STDIN-CANARY")
        .unwrap();
    let result = child.wait_with_output().unwrap();
    assert!(result.status.success());
    assert_eq!(result.stdout, b"STDIN-CANARY");
    assert!(
        !fs::read_to_string(root.path().join("report.json"))
            .unwrap()
            .contains("STDIN-CANARY")
    );
}
#[cfg(target_os = "macos")]
#[test]
fn protected_program_is_rejected_before_side_effects() {
    let root = tempfile::tempdir().unwrap();
    let result = invoke(
        root.path(),
        &["run", "--", "/bin/sh", "-c", "touch forbidden"],
    );
    assert_eq!(result.status.code(), Some(3));
    assert!(!root.path().join("forbidden").exists());
}
#[cfg(target_os = "macos")]
#[test]
fn unsupported_child_continues_and_preserves_incomplete_evidence() {
    let root = tempfile::tempdir().unwrap();
    let output = run(root.path(), "partial.json", "protected-child");
    assert_eq!(
        output.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        parse(root.path(), "partial.json")["executions"][0]["outcome"]["child_exit_code"],
        0,
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        fs::read_to_string(root.path().join("protected-child-result")).unwrap(),
        "original"
    );
    let value = parse(root.path(), "partial.json");
    assert_eq!(value["executions"][0]["outcome"]["child_exit_code"], 0);
    assert_eq!(
        value["executions"][0]["outcome"]["collection_complete"],
        false
    );
    assert!(
        value["executions"][0]["accesses"]
            .as_object()
            .unwrap()
            .values()
            .any(|access| access["unsupported"] == true)
    );
}

#[test]
fn unicode_argv_and_directory_conflicts_keep_concrete_evidence() {
    let root = tempfile::Builder::new()
        .prefix("runlens space 한국어-")
        .tempdir()
        .unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "writer.json",
            "--",
            fixture(),
            "read-write",
            "argument with spaces and 日本語",
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        fs::read_to_string(root.path().join("out/result.txt")).unwrap(),
        "argument with spaces and 日本語"
    );
    assert!(run(root.path(), "reader.json", "list").status.success());
    let output = invoke(
        root.path(),
        &[
            "conflicts",
            "--report",
            "writer.json",
            "--report",
            "reader.json",
            "--json",
        ],
    );
    assert!(output.status.success());
    let value: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(
        value["findings"]
            .as_object()
            .unwrap()
            .values()
            .any(|finding| finding["code"] == "potential-read-write-conflict"
                && finding["evidence"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .all(|reference| reference["source"] != "outcome")
                && finding["evidence"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|reference| reference["path"] == "${workspace}/out"))
    );
}
#[cfg(unix)]
#[test]
fn cancellation_is_reaped_and_saved_as_incomplete() {
    let root = tempfile::tempdir().unwrap();
    let mut child = Command::new(binary())
        .args([
            "--log-level",
            "off",
            "run",
            "--save",
            "cancelled.json",
            "--",
            fixture(),
            "ready-sleep",
        ])
        .current_dir(root.path())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(30);
    while !root.path().join("child-ready").exists() {
        assert!(std::time::Instant::now() < deadline, "target did not start");
        assert!(child.try_wait().unwrap().is_none(), "tracer exited early");
        std::thread::sleep(std::time::Duration::from_millis(20));
    }
    unsafe {
        libc::kill(child.id() as i32, libc::SIGTERM);
    };
    let status = child.wait().unwrap();
    assert_eq!(status.code(), Some(7));
    assert_eq!(
        parse(root.path(), "cancelled.json")["executions"][0]["outcome"]["collection_complete"],
        false
    );
}
#[test]
fn schemas_and_hostile_html_are_validated_without_execution() {
    let root = tempfile::tempdir().unwrap();
    assert!(run(root.path(), "report.json", "read").status.success());
    let mut value = parse(root.path(), "report.json");
    value["executions"][0]["command"]["argv"] = serde_json::json!(["<script>alert('x')</script>"]);
    fs::write(
        root.path().join("hostile.json"),
        serde_json::to_vec(&value).unwrap(),
    )
    .unwrap();
    assert!(
        invoke(
            root.path(),
            &[
                "export",
                "hostile.json",
                "--format",
                "html",
                "--output",
                "safe.html"
            ]
        )
        .status
        .success()
    );
    let html = fs::read_to_string(root.path().join("safe.html")).unwrap();
    assert!(html.contains("&lt;script&gt;"));
    assert!(!html.contains("<script>"));
    value["schema_version"] = 2.into();
    fs::write(
        root.path().join("invalid.json"),
        serde_json::to_vec(&value).unwrap(),
    )
    .unwrap();
    assert_eq!(
        invoke(root.path(), &["receipt", "invalid.json", "--json"])
            .status
            .code(),
        Some(2)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn static_linux_child_uses_seccomp_collection() {
    let Some(fixture) = std::env::var_os("RUNLENS_STATIC_FIXTURE") else {
        assert!(
            std::env::var_os("CI").is_none(),
            "CI requires a real static Linux fixture"
        );
        return;
    };
    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "static input").unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "static.json",
            "--",
            fixture.to_str().unwrap(),
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let report = parse(root.path(), "static.json");
    assert_eq!(
        report["executions"][0]["accesses"]["${workspace}/input.txt"]["read"],
        true
    );
    assert_eq!(
        report["executions"][0]["changes"]["${workspace}/static-output.txt"],
        "created"
    );
}

#[cfg(target_os = "macos")]
#[test]
fn hardened_macos_image_is_blocked_before_execution() {
    let root = tempfile::tempdir().unwrap();
    let protected = root.path().join("protected-tool");
    fs::copy(fixture(), &protected).unwrap();
    let status = Command::new("/usr/bin/codesign")
        .args(["--force", "--sign", "-", "--options", "runtime"])
        .arg(&protected)
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(status.success());
    let output = invoke(
        root.path(),
        &["run", "--", protected.to_str().unwrap(), "read-write"],
    );
    assert_eq!(
        output.status.code(),
        Some(3),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!root.path().join("out").exists());
    let child = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "hardened-child.json",
            "--",
            fixture(),
            "native-child",
            protected.to_str().unwrap(),
        ],
    );
    assert_eq!(
        child.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&child.stderr)
    );
    assert!(root.path().join("out/result.txt").exists());
    assert_eq!(
        parse(root.path(), "hardened-child.json")["executions"][0]["outcome"]["child_exit_code"],
        0
    );
}

#[test]
fn repeat_detects_content_sets_permissions_and_failed_preparation() {
    let mut modes = vec!["vary-content", "vary-set"];
    if cfg!(unix) {
        modes.push("vary-permissions");
    }
    for mode in modes {
        let root = repository(mode);
        let result = invoke(
            root.path(),
            &[
                "verify",
                "repeat",
                "build",
                "--runs",
                "2",
                "--save",
                "different.json",
            ],
        );
        assert_eq!(
            result.status.code(),
            Some(5),
            "{mode}: {}",
            String::from_utf8_lossy(&result.stderr)
        );
        let report = parse(root.path(), "different.json");
        assert_eq!(report["verification"], "failed");
        assert!(
            report["findings"]
                .as_object()
                .unwrap()
                .values()
                .any(|item| item["code"] == "different-output")
        );
        assert!(!root.path().join("out").exists());
    }
    let root = repository("read-write");
    let tool = fixture().replace('\\', "/");
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            r#"schema_version = 1
[commands.build]
argv = [{tool:?}, "read-write"]
outputs = ["out/**"]
prepare = [[{tool:?}, "fail"], [{tool:?}, "read-write"]]
"#
        ),
    )
    .unwrap();
    let result = invoke(
        root.path(),
        &["verify", "clean", "build", "--save", "setup.json"],
    );
    assert!(!result.status.success());
    assert!(
        root.path().join("setup.json").exists(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let report = parse(root.path(), "setup.json");
    assert_eq!(report["executions"].as_array().unwrap().len(), 1);
    assert_eq!(report["executions"][0]["role"], "preparation");
    assert_eq!(report["executions"][0]["outcome"]["child_exit_code"], 23);
    for args in [
        vec![
            "cache",
            "check",
            "setup.json",
            "--command",
            "build",
            "--json",
        ],
        vec!["policy", "check", "setup.json", "--json"],
        vec!["compare", "setup.json", "setup.json", "--json"],
    ] {
        let output = invoke(root.path(), &args);
        assert!(
            !output.status.success(),
            "preparation-only reports cannot pass"
        );
        let analysis: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(analysis["verdict"], "inconclusive");
    }
    assert!(!root.path().join("out").exists());
    fs::write(
        root.path().join("runlens.toml"),
        format!("schema_version=1\n[commands.build]\nargv=[{tool:?},'read-write']\n"),
    )
    .unwrap();
    assert_eq!(
        invoke(root.path(), &["verify", "repeat", "build"])
            .status
            .code(),
        Some(2)
    );
}

#[test]
fn many_execution_reports_remain_readable_with_few_file_descriptors() {
    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "input").unwrap();
    assert!(run(root.path(), "original.json", "read").status.success());
    let mut report = parse(root.path(), "original.json");
    let execution = report["executions"][0].clone();
    report["executions"] = Value::Array(
        (0..1056)
            .map(|_| {
                let mut execution = execution.clone();
                execution["id"] = uuid::Uuid::now_v7().to_string().into();
                execution
            })
            .collect(),
    );
    fs::write(
        root.path().join("many.json"),
        serde_json::to_vec(&report).unwrap(),
    )
    .unwrap();
    let mut command = Command::new(binary());
    command
        .args(["receipt", "many.json", "--json"])
        .current_dir(root.path())
        .stdout(Stdio::null());
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        // Restrict only this disposable reader child, never the test runner.
        unsafe {
            command.pre_exec(|| {
                let mut limit = std::mem::zeroed::<libc::rlimit>();
                if libc::getrlimit(libc::RLIMIT_NOFILE, &mut limit) != 0 {
                    return Err(std::io::Error::last_os_error());
                }
                limit.rlim_cur = limit.rlim_cur.min(128);
                if libc::setrlimit(libc::RLIMIT_NOFILE, &limit) != 0 {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
    }
    let output = command.output().unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}
#[test]
fn conflict_analysis_bounds_aggregate_target_pairs() {
    use runlens::{analysis, error::ErrorCode, model::Role, report};

    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "input").unwrap();
    assert!(run(root.path(), "original.json", "read").status.success());
    let original = report::read(&root.path().join("original.json")).unwrap();
    let repeated = |count| {
        let mut report = original.clone();
        report.executions = (0..count)
            .map(|_| {
                let mut execution = original.executions[0].clone();
                execution.id = uuid::Uuid::now_v7();
                execution
            })
            .collect();
        report::validate(&report).unwrap();
        report
    };
    let mut left = repeated(1056);
    for execution in &mut left.executions[256..] {
        execution.role = Role::Preparation;
    }
    // 256 * 256 is accepted; preparation executions consume no target-pair work.
    assert!(analysis::conflicts(&[left.clone(), repeated(256)]).is_ok());
    let right = repeated(257);
    assert_eq!(
        analysis::conflicts(&[left.clone(), right.clone()])
            .unwrap_err()
            .code,
        ErrorCode::InvalidInput
    );
    // Every individual report pair is small, but their aggregate exceeds the cap.
    let many = (0..64).map(|_| repeated(6)).collect::<Vec<_>>();
    assert_eq!(
        analysis::conflicts(&many).unwrap_err().code,
        ErrorCode::InvalidInput
    );
    report::save(&root.path().join("left.json"), &left, false).unwrap();
    report::save(&root.path().join("right.json"), &right, false).unwrap();
    let output = invoke(
        root.path(),
        &[
            "conflicts",
            "--report",
            "left.json",
            "--report",
            "right.json",
            "--json",
        ],
    );
    assert_eq!(output.status.code(), Some(2));
    assert!(
        output.stdout.is_empty(),
        "rejected analysis must not emit a partial result"
    );
    assert!(String::from_utf8_lossy(&output.stderr).contains("65,536 target pairs"));
}
#[test]
fn explain_uses_report_platform_path_syntax() {
    use runlens::{entries::Entries, model::Access, report};
    let root = tempfile::tempdir().unwrap();
    assert!(run(root.path(), "original.json", "read").status.success());
    let mut value = report::read(&root.path().join("original.json")).unwrap();
    let execution = &mut value.executions[0];
    execution.environment.os = "windows".into();
    execution.accesses = Entries::default();
    for path in ["C:/cache/input", "//server/share/input"] {
        execution
            .accesses
            .insert(
                path.into(),
                Access {
                    read: true,
                    write: false,
                    read_directory: false,
                    unsupported: false,
                    in_scope: false,
                },
            )
            .unwrap();
    }
    report::save(&root.path().join("windows.json"), &value, false).unwrap();
    for query in [
        "C:/cache/input",
        r"C:\cache\input",
        r"\\?\C:\cache\input",
        r"\\server\share\input",
        r"\\?\UNC\server\share\input",
    ] {
        let output = invoke(
            root.path(),
            &["explain", query, "--report", "windows.json", "--json"],
        );
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let result: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(result["usages"].as_object().unwrap().len(), 1, "{query}");
    }
    let execution = &mut value.executions[0];
    execution.environment.os = "linux".into();
    execution.accesses = Entries::default();
    execution
        .accesses
        .insert(
            r"${workspace}/C:\cache\input".into(),
            Access {
                read: true,
                write: false,
                read_directory: false,
                unsupported: false,
                in_scope: true,
            },
        )
        .unwrap();
    assert_eq!(
        runlens::analysis::explain(r"C:\cache\input", &[value])
            .unwrap()
            .usages
            .len(),
        1
    );
}
#[test]
fn hostile_envelopes_and_forged_changes_are_rejected() {
    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "input").unwrap();
    assert!(
        run(root.path(), "original.json", "read-write")
            .status
            .success()
    );
    let report = parse(root.path(), "original.json");
    let mut changed = report.clone();
    changed["executions"][0]["changes"]["${workspace}/out/result.txt"] = "deleted".into();
    let mut uppercase = report.clone();
    uppercase["executions"][0]["id"] = "019a1234-ABCD-7000-8000-123456789ABC".into();
    let mut oversized = report.clone();
    oversized["executions"][0]["command"]["argv"] =
        serde_json::json!(vec!["large".repeat(7000); 64]);
    let mut wrong_scope = report.clone();
    wrong_scope["executions"][0]["scope"]["before_complete"] = false.into();
    let mut omitted = report.clone();
    omitted["executions"][0]["changes"] = serde_json::json!({});
    let mut wrong_digest = report.clone();
    wrong_digest["executions"][0]["environment"]["executable_sha256"] = "invalid".into();
    for (index, value) in [
        changed,
        uppercase,
        oversized,
        wrong_scope,
        omitted,
        wrong_digest,
    ]
    .iter()
    .enumerate()
    {
        let name = format!("hostile-{index}.json");
        fs::write(root.path().join(&name), serde_json::to_vec(value).unwrap()).unwrap();
        assert_eq!(
            invoke(root.path(), &["receipt", &name, "--json"])
                .status
                .code(),
            Some(2)
        );
    }
    let mut cross_environment = report.clone();
    cross_environment["executions"][0]["environment"]["architecture"] =
        "different-architecture".into();
    fs::write(
        root.path().join("cross.json"),
        serde_json::to_vec(&cross_environment).unwrap(),
    )
    .unwrap();
    let comparison = invoke(
        root.path(),
        &["compare", "original.json", "cross.json", "--json"],
    );
    assert_eq!(comparison.status.code(), Some(4));
    let comparison: Value = serde_json::from_slice(&comparison.stdout).unwrap();
    assert_eq!(comparison["verdict"], "inconclusive");
    assert!(
        !comparison["environment_differences"]
            .as_object()
            .unwrap()
            .is_empty()
    );
    assert_eq!(
        invoke(
            root.path(),
            &[
                "compare",
                "original.json",
                "original.json",
                "--map",
                "${workspace}/out=${workspace}"
            ]
        )
        .status
        .code(),
        Some(2)
    );
    let oversized = fs::File::create(root.path().join("oversized.json")).unwrap();
    oversized
        .set_len(runlens::report::MAX_REPORT_BYTES + 1)
        .unwrap();
    assert_eq!(
        invoke(root.path(), &["receipt", "oversized.json"])
            .status
            .code(),
        Some(2)
    );
}

#[cfg(unix)]
#[test]
fn unix_collection_failure_and_large_variadic_exec_preserve_child_semantics() {
    let root = tempfile::tempdir().unwrap();
    let output = run(root.path(), "cwd.json", "removed-cwd");
    // Darwin can still resolve a removed cwd through its open directory vnode;
    // Linux reports lost scope. Both must preserve the child's syscall result.
    assert!(
        matches!(output.status.code(), Some(0 | 4)),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(root.path().join("continued").exists());
    assert_eq!(
        parse(root.path(), "cwd.json")["executions"][0]["outcome"]["child_exit_code"],
        0
    );
    let output = run(root.path(), "payload.json", "invalid-payload-child");
    assert_eq!(
        output.status.code(),
        Some(4),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        parse(root.path(), "payload.json")["executions"][0]["outcome"]["child_exit_code"],
        0
    );
    assert!(root.path().join("out/result.txt").exists());
    fs::write(root.path().join("input.txt"), "input").unwrap();
    let output = run(root.path(), "exec.json", "execl-many");
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        parse(root.path(), "exec.json")["executions"][0]["accesses"]["${workspace}/input.txt"]
            ["read"],
        true
    );
}
