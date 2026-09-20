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
#[cfg(windows)]
#[test]
fn windows_information_mutations_cannot_pass_external_write_boundaries() {
    for mode in ["windows-rename", "windows-delete"] {
        let root = tempfile::tempdir().unwrap();
        let external = tempfile::tempdir().unwrap();
        let source = external.path().join("mutation-source");
        let destination = external.path().join("mutation-destination");
        fs::write(&source, "original").unwrap();
        fs::write(
            root.path().join("runlens.toml"),
            "schema_version = 1\n[policy]\ndeny_writes = [\"**/mutation-source\"]\n",
        )
        .unwrap();
        let result = invoke(
            root.path(),
            &[
                "run",
                "--save",
                "mutation.json",
                "--",
                fixture(),
                mode,
                source.to_str().unwrap(),
                destination.to_str().unwrap(),
            ],
        );
        assert_eq!(
            result.status.code(),
            Some(if mode == "windows-rename" { 4 } else { 0 }),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(!source.exists());
        assert_eq!(destination.exists(), mode == "windows-rename");
        let report = parse(root.path(), "mutation.json");
        let access = report["executions"][0]["accesses"]
            .as_object()
            .unwrap()
            .iter()
            .find(|(path, _)| path.ends_with("/mutation-source"))
            .unwrap()
            .1;
        assert_eq!(access["write"], true);
        let policy = invoke(root.path(), &["policy", "check", "mutation.json", "--json"]);
        assert_eq!(
            policy.status.code(),
            Some(5),
            "{}",
            String::from_utf8_lossy(&policy.stderr)
        );
    }
}
#[cfg(windows)]
#[test]
fn windows_readonly_creation_dispositions_record_external_write_attempts() {
    for mode in [
        "windows-create-readonly",
        "windows-open-if-readonly",
        "windows-overwrite-readonly",
        "windows-open-readonly",
    ] {
        let root = tempfile::tempdir().unwrap();
        let external = tempfile::tempdir().unwrap();
        let path = external.path().join("creation-target");
        if mode == "windows-open-readonly" {
            fs::write(&path, "existing").unwrap();
        }
        let output = invoke(
            root.path(),
            &[
                "run",
                "--save",
                "creation.json",
                "--",
                fixture(),
                mode,
                path.to_str().unwrap(),
            ],
        );
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let report = parse(root.path(), "creation.json");
        let accesses = report["executions"][0]["accesses"].as_object().unwrap();
        let access = accesses
            .iter()
            .find(|(path, _)| path.ends_with("/creation-target"))
            .unwrap()
            .1;
        assert_eq!(access["write"], mode != "windows-open-readonly");
    }
}
#[test]
fn cache_declarations_require_the_selected_command_identity() {
    let root = repository("read");
    assert!(
        invoke(
            root.path(),
            &["run", "--command", "build", "--save", "original.json"]
        )
        .status
        .success()
    );
    let mut original = parse(root.path(), "original.json");
    // Isolate identity from dependency coverage. This remains a valid report;
    // an unrelated invocation must not borrow these empty observations.
    original["executions"][0]["accesses"] = serde_json::json!({});
    for (index, field, value, expected) in [
        (0, "name", serde_json::json!("build"), 0),
        (1, "name", serde_json::Value::Null, 0),
        (2, "name", serde_json::json!("different"), 4),
        (3, "argv", serde_json::json!([fixture(), "other"]), 4),
        (
            4,
            "argv",
            serde_json::json!(["different-executable", "read"]),
            4,
        ),
        (5, "cwd", serde_json::json!("${workspace}/other"), 4),
        (6, "argv", serde_json::json!([fixture(), "[redacted]"]), 4),
    ] {
        let mut report = original.clone();
        report["executions"][0]["command"][field] = value;
        let path = format!("identity-{index}.json");
        fs::write(
            root.path().join(&path),
            serde_json::to_vec(&report).unwrap(),
        )
        .unwrap();
        let output = invoke(
            root.path(),
            &["cache", "check", &path, "--command", "build", "--json"],
        );
        assert_eq!(
            output.status.code(),
            Some(expected),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let result: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(
            result["verdict"],
            if expected == 0 {
                "passed"
            } else {
                "inconclusive"
            }
        );
    }
    // Even equal masking placeholders cannot establish equality.
    let mut report: runlens::model::Report = serde_json::from_value(original).unwrap();
    report.executions[0].command.argv[1] = "[redacted]".into();
    let command = runlens::config::Command::direct(report.executions[0].command.argv.clone());
    assert_eq!(
        runlens::analysis::cache(&report, &command, &report.executions[0].command)
            .unwrap()
            .verdict,
        Some(runlens::model::Verdict::Inconclusive)
    );
    assert!(!root.path().join("out").exists());
}
#[tokio::test]
async fn clean_preflights_combined_execution_capacity_before_source_preparation() {
    use runlens::{clean, config, error::ErrorCode, model::MAX_EXECUTIONS};
    let root = tempfile::tempdir().unwrap();
    assert!(run(root.path(), "baseline.json", "read").status.success());
    let mut baseline = runlens::report::read(&root.path().join("baseline.json")).unwrap();
    let template = baseline.executions[0].clone();
    let mut config = config::Config::default();
    let mut command = config::Command::direct(vec![fixture().into(), "read".into()]);
    command.outputs = vec!["out/**".into()];
    for (baseline_count, preparations, runs, exceeds) in [
        (MAX_EXECUTIONS, 0, 1, true),
        (MAX_EXECUTIONS - 1, 0, 1, false),
        (MAX_EXECUTIONS - 1, 1, 1, true),
        (MAX_EXECUTIONS - 3, 0, 3, false),
        (MAX_EXECUTIONS - 2, 0, 3, true),
    ] {
        baseline.executions = (0..baseline_count)
            .map(|_| {
                let mut execution = template.clone();
                execution.id = uuid::Uuid::now_v7();
                execution
            })
            .collect();
        command.prepare = vec![command.argv.clone(); preparations];
        config.commands.insert("build".into(), command.clone());
        let error = clean::verify(
            root.path(),
            "build",
            &config,
            runs,
            false,
            Some(&baseline),
            tokio_util::sync::CancellationToken::new(),
        )
        .await
        .unwrap_err();
        assert_eq!(error.code, ErrorCode::InvalidInput);
        assert_eq!(
            error.message,
            if exceeds {
                "baseline and requested executions exceed the report execution limit"
            } else {
                "clean verification requires a repository HEAD"
            }
        );
    }
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
fn selected_environment_names_do_not_prove_baseline_compatibility() {
    let root = repository("read");
    let path = root.path().join("runlens.toml");
    let config = fs::read_to_string(&path).unwrap();
    fs::write(&path, format!("{config}\nenv = [\"RUNLENS_FLAVOR\"]\n")).unwrap();
    git(root.path(), &["add", "runlens.toml"]);
    git(root.path(), &["commit", "-qm", "select environment input"]);
    let execute = |args: &[&str], flavor: &str| {
        Command::new(binary())
            .args(args)
            .env("RUNLENS_FLAVOR", flavor)
            .current_dir(root.path())
            .output()
            .unwrap()
    };
    let baseline = execute(
        &["verify", "clean", "build", "--save", "baseline.json"],
        "FIRST-PRIVATE-VALUE",
    );
    assert!(
        baseline.status.success(),
        "{}",
        String::from_utf8_lossy(&baseline.stderr)
    );
    for (index, flavor) in ["FIRST-PRIVATE-VALUE", "SECOND-PRIVATE-VALUE"]
        .iter()
        .enumerate()
    {
        let name = format!("result-{index}.json");
        let current = execute(
            &[
                "verify",
                "clean",
                "build",
                "--baseline",
                "baseline.json",
                "--save",
                &name,
            ],
            flavor,
        );
        assert_eq!(
            current.status.code(),
            Some(4),
            "{}",
            String::from_utf8_lossy(&current.stderr)
        );
        let report = fs::read_to_string(root.path().join(&name)).unwrap();
        assert!(!report.contains("PRIVATE-VALUE"));
        assert_eq!(parse(root.path(), &name)["verification"], "inconclusive");
    }
    let compared = invoke(
        root.path(),
        &["compare", "baseline.json", "baseline.json", "--json"],
    );
    assert_eq!(compared.status.code(), Some(4));
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
fn historical_baseline_errors_do_not_become_current_exit_codes() {
    let root = repository("read");
    assert!(
        invoke(
            root.path(),
            &["verify", "clean", "build", "--save", "baseline.json"]
        )
        .status
        .success()
    );
    let original = parse(root.path(), "baseline.json");
    for (index, error) in ["timeout", "cancelled", "cleanup-failed"]
        .iter()
        .enumerate()
    {
        let mut baseline = original.clone();
        baseline["verification"] = "inconclusive".into();
        baseline["executions"][0]["outcome"]["collection_complete"] = false.into();
        baseline["executions"][0]["outcome"]["errors"] = serde_json::json!([error]);
        let input = format!("historical-{index}.json");
        let output = format!("current-{index}.json");
        fs::write(
            root.path().join(&input),
            serde_json::to_vec(&baseline).unwrap(),
        )
        .unwrap();
        let result = invoke(
            root.path(),
            &[
                "verify",
                "clean",
                "build",
                "--baseline",
                &input,
                "--save",
                &output,
            ],
        );
        assert_eq!(
            result.status.code(),
            Some(4),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        let saved = runlens::report::read(&root.path().join(output)).unwrap();
        assert_eq!(
            saved.verification,
            Some(runlens::model::Verdict::Inconclusive)
        );
        assert!(saved.current_executions().all(|e| e.outcome.success()));
        let historical = saved
            .executions
            .iter()
            .find(|e| e.role == runlens::model::Role::Baseline)
            .unwrap();
        assert_eq!(
            historical.id.to_string(),
            baseline["executions"][0]["id"].as_str().unwrap()
        );
        assert_eq!(historical.outcome.errors.len(), 1);
    }
}
#[test]
fn comparison_distinguishes_linux_distributions_with_equal_versions() {
    let root = repository("read");
    assert!(run(root.path(), "record.json", "read").status.success());
    let mut left = runlens::report::read(&root.path().join("record.json")).unwrap();
    left.executions[0].environment.os = "linux".into();
    left.executions[0].environment.os_version = Some("ubuntu:22.04".into());
    let mut right = left.clone();
    assert!(runlens::analysis::compatible(
        &left.executions[0],
        &right.executions[0]
    ));
    right.executions[0].environment.os_version = Some("pop:22.04".into());
    assert_eq!(
        runlens::analysis::compare(&left, &right, &[])
            .unwrap()
            .verdict,
        Some(runlens::model::Verdict::Inconclusive)
    );
    for version in [Some("22.04".into()), None, Some("ubuntu:".into())] {
        left.executions[0].environment.os_version = version.clone();
        right.executions[0].environment.os_version = version;
        assert!(!runlens::analysis::compatible(
            &left.executions[0],
            &right.executions[0]
        ));
    }
}
#[test]
fn new_access_policy_requires_comparable_complete_baseline() {
    use runlens::{analysis, config::Policy, entries::Entries, model::Verdict};
    let root = repository("read");
    assert!(run(root.path(), "current.json", "read").status.success());
    let current = runlens::report::read(&root.path().join("current.json")).unwrap();
    let mut baseline = current.clone();
    baseline.executions[0].accesses = Entries::default();
    let mut rules = Policy {
        fail_new_accesses: true,
        ..Default::default()
    };
    let commands = Default::default();
    assert_eq!(
        analysis::policy(
            &current,
            &rules,
            &commands,
            &Default::default(),
            Some(&baseline)
        )
        .unwrap()
        .verdict,
        Some(Verdict::Failed)
    );
    for variant in 0..4 {
        let mut old = baseline.clone();
        match variant {
            0 => old.executions[0].environment.source_revision = Some("0".repeat(40)),
            1 => old.executions[0].environment.os_version = Some("other-version".into()),
            2 => old.executions[0].environment.executable_sha256 = Some("0".repeat(64)),
            _ => old.executions[0].outcome.collection_complete = false,
        }
        let result =
            analysis::policy(&current, &rules, &commands, &Default::default(), Some(&old)).unwrap();
        assert_eq!(result.verdict, Some(Verdict::Inconclusive));
        assert!(
            !result
                .findings
                .iter()
                .any(|item| item.unwrap().1.code == runlens::model::FindingCode::NewAccess)
        );
        rules.deny_reads = vec!["input.txt".into()];
        assert_eq!(
            analysis::policy(&current, &rules, &commands, &Default::default(), Some(&old))
                .unwrap()
                .verdict,
            Some(Verdict::Failed)
        );
        rules.deny_reads.clear();
    }
}
#[test]
fn selected_redaction_names_follow_environment_key_case_rules() {
    let root = repository("read");
    let canary = "redaction-case-canary-7a24";
    fs::write(root.path().join(canary), "content is never retained").unwrap();
    fs::write(
        root.path().join("runlens.toml"),
        r#"schema_version = 1
[redaction]
environment_names = ["CUSTOM_REDACT"]
"#,
    )
    .unwrap();
    for (index, name) in ["CUSTOM_REDACT", "custom_redact"].iter().enumerate() {
        let saved = format!("redaction-{index}.json");
        let result = Command::new(binary())
            .current_dir(root.path())
            .env_remove("CUSTOM_REDACT")
            .env_remove("custom_redact")
            .env(name, canary)
            .args(["run", "--save", &saved, "--", fixture(), "read", canary])
            .output()
            .unwrap();
        let masked = cfg!(windows) || index == 0;
        assert_eq!(
            result.status.code(),
            Some(if masked { 4 } else { 0 }),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        let bytes = fs::read_to_string(root.path().join(&saved)).unwrap();
        assert_eq!(bytes.contains(canary), !masked);
        assert!(!String::from_utf8_lossy(&result.stderr).contains(canary));
        let value = parse(root.path(), &saved);
        assert_eq!(
            value["executions"][0]["command"]["argv"][2],
            if masked { "[redacted]" } else { canary }
        );
    }
}
#[test]
#[cfg(windows)]
fn differently_cased_workspace_accesses_remain_private_and_policy_visible() {
    let root = repository("read");
    let root_path = root.path().canonicalize().unwrap();
    fs::create_dir(root.path().join("private")).unwrap();
    fs::write(root.path().join("private/file"), "private contents").unwrap();
    fs::write(
        root.path().join("runlens.toml"),
        "schema_version = 1\n[policy]\ndeny_reads = [\"private/**\"]\n",
    )
    .unwrap();
    let alternate = format!(
        "{}/private/file",
        root_path.to_string_lossy().to_uppercase()
    );
    let observed = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "casing.json",
            "--",
            fixture(),
            "read",
            &alternate,
        ],
    );
    assert!(
        observed.status.success(),
        "{}",
        String::from_utf8_lossy(&observed.stderr)
    );
    let value = parse(root.path(), "casing.json");
    let accesses = value["executions"][0]["accesses"].as_object().unwrap();
    let (_, access) = accesses
        .iter()
        .find(|(key, _)| key.eq_ignore_ascii_case("${workspace}/private/file"))
        .unwrap();
    assert_eq!(access["in_scope"], true);
    let serialized = fs::read_to_string(root.path().join("casing.json")).unwrap();
    assert!(
        !serialized
            .to_uppercase()
            .contains(&runlens::privacy::normalized(&root_path).to_uppercase())
    );
    assert_eq!(
        invoke(root.path(), &["policy", "check", "casing.json", "--json"])
            .status
            .code(),
        Some(5)
    );
}
#[test]
fn offline_policy_and_cache_follow_windows_case_rules() {
    let root = repository("read");
    fs::create_dir(root.path().join("Private")).unwrap();
    fs::write(root.path().join("Private/file"), "contents").unwrap();
    assert!(
        invoke(
            root.path(),
            &[
                "run",
                "--save",
                "mixed.json",
                "--",
                fixture(),
                "read",
                "Private/file"
            ]
        )
        .status
        .success()
    );
    let mut report = runlens::report::read(&root.path().join("mixed.json")).unwrap();
    let rules = runlens::config::Policy {
        deny_reads: vec!["private/**".into()],
        ..Default::default()
    };
    let commands = std::collections::BTreeMap::new();
    for os in ["windows", "linux"] {
        report.executions[0].environment.os = os.into();
        let result =
            runlens::analysis::policy(&report, &rules, &commands, &Default::default(), None)
                .unwrap();
        assert_eq!(
            result.verdict,
            Some(if os == "windows" {
                runlens::model::Verdict::Failed
            } else {
                runlens::model::Verdict::Passed
            })
        );
        let mut command = runlens::config::Command::direct(vec![]);
        command.inputs = vec!["private/**".into()];
        let cache =
            runlens::analysis::cache(&report, &command, &report.executions[0].command).unwrap();
        let missed_private = cache.findings.iter().any(|entry| {
            let (_, finding) = entry.unwrap();
            finding.code == runlens::model::FindingCode::UndeclaredInput
                && finding
                    .evidence
                    .iter()
                    .any(|e| e.path.as_deref() == Some("${workspace}/Private/file"))
        });
        assert_eq!(missed_private, os != "windows");
    }
}
#[test]
fn excluded_descendant_reads_cannot_look_like_new_outputs() {
    let root = repository("read");
    fs::create_dir(root.path().join("generated")).unwrap();
    fs::write(root.path().join("generated/input"), "pre-existing input").unwrap();
    let tool = fixture().replace('\\', "/");
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            r#"schema_version = 1
exclusions = ["generated"]
[commands.build]
argv = [{tool:?}, "read", "generated/input"]
outputs = ["generated/**"]
"#
        ),
    )
    .unwrap();
    let result = invoke(
        root.path(),
        &["run", "--command", "build", "--save", "excluded.json"],
    );
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let report = runlens::report::read(&root.path().join("excluded.json")).unwrap();
    let target = report.targets().next().unwrap();
    let key = "${workspace}/generated/input";
    assert!(target.before.get(key).unwrap().is_none());
    assert!(!target.accesses.get(key).unwrap().unwrap().in_scope);
    let checked = invoke(
        root.path(),
        &[
            "cache",
            "check",
            "excluded.json",
            "--command",
            "build",
            "--json",
        ],
    );
    assert!(matches!(checked.status.code(), Some(4 | 5)));
    let value: Value = serde_json::from_slice(&checked.stdout).unwrap();
    assert!(value["findings"].as_object().unwrap().values().any(|f| {
        f["code"] == "unknown-evidence"
            && f["evidence"]
                .as_array()
                .unwrap()
                .iter()
                .any(|e| e["path"] == key)
    }));
}
#[test]
fn preparation_obeys_global_policy_boundaries_in_clean_and_repeat() {
    let root = repository("read");
    fs::write(root.path().join("private.txt"), "private").unwrap();
    fs::write(root.path().join("result.txt"), "stable output").unwrap();
    git(root.path(), &["add", "."]);
    git(root.path(), &["commit", "-qm", "preparation fixtures"]);
    let tool = fixture().replace('\\', "/");
    for (index, (prepare, policy, code)) in [
        (
            format!("[{tool:?}, \"read\", \"private.txt\"]"),
            "deny_reads = [\"private.txt\"]",
            "read-boundary",
        ),
        (
            format!("[{tool:?}, \"read\", \"private.txt\"]"),
            "allow_reads = [\"input.txt\", \"${home}/**\", \"/**\", \"C:/**\"]",
            "read-boundary",
        ),
        (
            format!("[{tool:?}, \"read-write\"]"),
            "deny_writes = [\"out/**\"]",
            "write-boundary",
        ),
        (
            format!("[{tool:?}, \"read-write\"]"),
            "allow_writes = [\"result.txt\"]",
            "write-boundary",
        ),
    ]
    .into_iter()
    .enumerate()
    {
        fs::write(
            root.path().join("runlens.toml"),
            format!(
                r#"schema_version = 1
[commands.build]
argv = [{tool:?}, "read"]
prepare = [{prepare}]
inputs = ["input.txt"]
outputs = ["result.txt"]
[policy]
{policy}
"#
            ),
        )
        .unwrap();
        for mode in ["clean", "repeat"] {
            let name = format!("preparation-{index}-{mode}.json");
            let result = invoke(root.path(), &["verify", mode, "build", "--save", &name]);
            assert_eq!(
                result.status.code(),
                Some(5),
                "{}",
                String::from_utf8_lossy(&result.stderr)
            );
            let value = parse(root.path(), &name);
            let preparation_ids = value["executions"]
                .as_array()
                .unwrap()
                .iter()
                .filter(|e| e["role"] == "preparation")
                .map(|e| &e["id"])
                .collect::<Vec<_>>();
            assert!(value["findings"].as_object().unwrap().values().any(|f| {
                f["code"] == code
                    && f["evidence"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .any(|e| preparation_ids.contains(&&e["execution_id"]))
            }));
        }
    }
}
#[test]
#[cfg(unix)]
fn deny_only_policy_cannot_pass_unresolved_path_aliases() {
    let root = repository("read");
    fs::create_dir(root.path().join("sub")).unwrap();
    fs::create_dir(root.path().join("private")).unwrap();
    fs::write(root.path().join("private/input"), "private contents").unwrap();
    fs::write(
        root.path().join("runlens.toml"),
        "schema_version = 1\n[policy]\ndeny_reads = [\"private/**\"]\n",
    )
    .unwrap();
    let observed = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "alias.json",
            "--",
            fixture(),
            "read",
            "sub/../private/input",
        ],
    );
    assert!(
        observed.status.success(),
        "{}",
        String::from_utf8_lossy(&observed.stderr)
    );
    let result = invoke(root.path(), &["policy", "check", "alias.json", "--json"]);
    assert!(
        matches!(result.status.code(), Some(4 | 5)),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let value: Value = serde_json::from_slice(&result.stdout).unwrap();
    assert_ne!(value["verdict"], "passed");
    assert!(
        value["findings"]
            .as_object()
            .unwrap()
            .values()
            .any(|finding| finding["code"] == "unknown-evidence"
                || finding["code"] == "read-boundary")
    );
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
fn consecutive_sensitive_flags_stay_redacted_in_saved_and_exported_reports() {
    let root = tempfile::tempdir().unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "report.json",
            "--",
            fixture(),
            "read",
            "input.txt",
            "--token",
            "--password",
            "consecutive-flag-canary",
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!String::from_utf8_lossy(&output.stderr).contains("consecutive-flag-canary"));
    for (format, name) in [("json", "export.json"), ("html", "export.html")] {
        assert!(
            invoke(
                root.path(),
                &[
                    "export",
                    "report.json",
                    "--format",
                    format,
                    "--output",
                    name
                ]
            )
            .status
            .success()
        );
        assert!(
            !fs::read_to_string(root.path().join(name))
                .unwrap()
                .contains("consecutive-flag-canary")
        );
    }
    assert!(
        !fs::read_to_string(root.path().join("report.json"))
            .unwrap()
            .contains("consecutive-flag-canary")
    );
}
#[test]
fn tracing_preserves_unset_and_selected_fspy_values_in_children() {
    let root = tempfile::tempdir().unwrap();
    for present in [false, true] {
        let label = if present { "present" } else { "absent" };
        for traced in [false, true] {
            let mut command = Command::new(if traced { binary() } else { fixture() });
            if traced {
                command.args(["run", "--", fixture()]);
            }
            command
                .args(["fspy-environment-child", label])
                .env_remove("FSPY")
                .current_dir(root.path());
            if present {
                command.env("FSPY", "caller-selected");
            }
            let output = command.output().unwrap();
            assert!(
                output.status.success(),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
    }
    let root = repository("read");
    let tool = fixture().replace('\\', "/");
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            r#"schema_version = 1
[commands.build]
argv = [{tool:?}, "fspy-environment-child", "present"]
env = ["FSPY"]
"#
        ),
    )
    .unwrap();
    git(root.path(), &["add", "runlens.toml"]);
    git(root.path(), &["commit", "-qm", "select FSPY explicitly"]);
    let output = Command::new(binary())
        .args(["verify", "clean", "build"])
        .env("FSPY", "caller-selected")
        .current_dir(root.path())
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}
#[test]
fn cache_policy_and_overflow_fail_closed() {
    let root = repository("read-write");
    // Use the configured argv verbatim. Windows separator spelling is part
    // of the recorded command identity and must not bypass cache binding.
    let recorded = invoke(
        root.path(),
        &["run", "--command", "build", "--save", "report.json"],
    );
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
fn cancellation_during_after_snapshot_retains_cancelled_status() {
    use std::io::BufRead;
    let root = tempfile::tempdir().unwrap();
    let mut child = Command::new(binary())
        .args([
            "--log-level",
            "info",
            "run",
            "--save",
            "cancelled.json",
            "--",
            fixture(),
            "large-output",
        ])
        .current_dir(root.path())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut reached = false;
    for line in std::io::BufReader::new(child.stderr.take().unwrap()).lines() {
        if !reached && line.unwrap().contains("after-snapshot") {
            reached = true;
            // SAFETY: signal only our still-running CLI process.
            assert_eq!(unsafe { libc::kill(child.id() as i32, libc::SIGTERM) }, 0);
        }
    }
    assert!(reached, "after-snapshot stage was not reached");
    assert_eq!(child.wait().unwrap().code(), Some(7));
    let report = parse(root.path(), "cancelled.json");
    let outcome = &report["executions"][0]["outcome"];
    assert_eq!(outcome["child_exit_code"], 0);
    assert_eq!(outcome["collection_complete"], false);
    assert!(
        outcome["errors"]
            .as_array()
            .unwrap()
            .contains(&serde_json::json!("cancelled"))
    );
    assert!(
        outcome["errors"]
            .as_array()
            .unwrap()
            .contains(&serde_json::json!("incomplete"))
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
    // Repeated usage rows may exceed the input envelope's size without being
    // a new external report. This stays below the input limit on every OS.
    let mut execution = execution;
    execution["command"]["argv"]
        .as_array_mut()
        .unwrap()
        .push("x".repeat(256).into());
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

#[cfg(unix)]
fn check_rename_attempt(executable: &str, mode: &str, missing: bool) {
    let root = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    let outside = outside.path().canonicalize().unwrap();
    let destination = outside.join("renamed.txt");
    fs::write(root.path().join("input.txt"), "moved contents").unwrap();
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            "schema_version = 1\n[policy]\ndeny_writes = [{:?}]\n",
            destination.to_str().unwrap()
        ),
    )
    .unwrap();
    let source = if missing { "missing.txt" } else { "input.txt" };
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "rename.json",
            "--",
            executable,
            mode,
            source,
            "renamed.txt",
            outside.to_str().unwrap(),
        ],
    );
    assert!(
        output.status.success(),
        "{mode}: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let report = parse(root.path(), "rename.json");
    let execution = &report["executions"][0];
    assert_eq!(execution["outcome"]["collection_complete"], true);
    assert_eq!(
        execution["accesses"][format!("${{workspace}}/{source}")]["write"],
        true
    );
    assert_eq!(
        execution["accesses"][destination.to_str().unwrap()]["write"],
        true
    );
    assert_eq!(destination.exists(), !missing);
    if missing {
        assert!(
            execution["changes"]
                .get("${workspace}/missing.txt")
                .is_none()
        );
    } else {
        assert_eq!(execution["changes"]["${workspace}/input.txt"], "deleted");
    }
    assert_eq!(
        invoke(root.path(), &["policy", "check", "rename.json", "--json"])
            .status
            .code(),
        Some(5)
    );
}

#[test]
#[cfg(unix)]
fn rename_attempts_cover_both_endpoints_and_external_write_policy() {
    let modes = [
        "rename",
        "renameat",
        #[cfg(target_os = "linux")]
        "renameat2",
        #[cfg(target_os = "macos")]
        "renamex",
        #[cfg(target_os = "macos")]
        "renameatx",
    ];
    for mode in modes {
        for missing in [false, true] {
            check_rename_attempt(fixture(), mode, missing);
        }
    }
}

#[test]
#[cfg(target_os = "linux")]
fn static_linux_rename_syscalls_cover_both_endpoints() {
    let Ok(executable) = std::env::var("RUNLENS_STATIC_FIXTURE") else {
        assert!(
            std::env::var_os("CI").is_none(),
            "CI requires a real static Linux fixture"
        );
        return;
    };
    for mode in [
        #[cfg(target_arch = "x86_64")]
        "rename",
        "renameat",
        "renameat2",
    ] {
        for missing in [false, true] {
            check_rename_attempt(&executable, mode, missing);
        }
    }
}

#[test]
fn write_allowlists_cover_ancestor_membership_but_not_forbidden_siblings() {
    for (pattern, path, extra, deny, expected) in [
        ("out/**", "out/result", None, "", 0),
        ("build/out/**", "build/out/result", None, "", 0),
        ("**/out/**", "build/out/result", None, "", 0),
        ("out/**", "out/result", Some("forbidden-file"), "", 5),
        ("out/**", "out/result", Some("forbidden-dir/"), "", 5),
        (
            "build/out/**",
            "build/out/result",
            None,
            "deny_writes = [\"build\"]",
            5,
        ),
        ("out/**", "out/result", None, "deny_writes = [\".\"]", 5),
    ] {
        let root = tempfile::tempdir().unwrap();
        fs::write(
            root.path().join("runlens.toml"),
            format!(
                "schema_version = 1\n[policy]\nallow_writes = [\"build\", {pattern:?}]\n{deny}\n"
            ),
        )
        .unwrap();
        let mut args = vec![
            "run",
            "--save",
            "allowed.json",
            "--",
            fixture(),
            "policy-write",
            path,
        ];
        if let Some(extra) = extra {
            args.push(extra);
        }
        let output = invoke(root.path(), &args);
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert_eq!(
            parse(root.path(), "allowed.json")["executions"][0]["changes"]["${workspace}"],
            "modified"
        );
        let checked = invoke(root.path(), &["policy", "check", "allowed.json", "--json"]);
        assert_eq!(
            checked.status.code(),
            Some(expected),
            "{pattern} / {extra:?} / {deny}: {}",
            String::from_utf8_lossy(&checked.stdout)
        );
    }
}

#[cfg(unix)]
fn check_removal_attempt(executable: &str, mode: &str, missing: bool) {
    let root = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    let outside = outside.path().canonicalize().unwrap();
    let path = outside.join("blocked");
    if !missing {
        if mode == "delete-rmdir" || mode == "delete-directory-at" {
            fs::create_dir(&path).unwrap();
        } else {
            fs::write(&path, "external content").unwrap();
        }
    }
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            "schema_version=1\n[policy]\ndeny_writes=[{:?}]\n",
            path.to_str().unwrap()
        ),
    )
    .unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "removed.json",
            "--",
            executable,
            mode,
            path.to_str().unwrap(),
            outside.to_str().unwrap(),
            if missing { "missing" } else { "present" },
        ],
    );
    assert!(
        output.status.success(),
        "{mode}: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = parse(root.path(), "removed.json");
    let execution = &value["executions"][0];
    assert_eq!(execution["outcome"]["collection_complete"], true);
    assert_eq!(execution["accesses"][path.to_str().unwrap()]["write"], true);
    assert_eq!(
        execution["accesses"][path.to_str().unwrap()]["in_scope"],
        false
    );
    assert!(execution["changes"].get(path.to_str().unwrap()).is_none());
    assert!(!path.exists());
    assert_eq!(
        invoke(root.path(), &["policy", "check", "removed.json", "--json"])
            .status
            .code(),
        Some(5)
    );
}

#[test]
#[cfg(unix)]
fn removal_attempts_cover_external_file_and_directory_boundaries() {
    for mode in [
        "delete-unlink",
        "delete-unlinkat",
        "delete-unlinkat-relative",
        "delete-rmdir",
        "delete-directory-at",
        "delete-remove",
    ] {
        for missing in [false, true] {
            check_removal_attempt(fixture(), mode, missing);
        }
    }
}

#[test]
#[cfg(target_os = "linux")]
fn static_linux_removal_syscalls_cannot_pass_external_write_denials() {
    let Ok(executable) = std::env::var("RUNLENS_STATIC_FIXTURE") else {
        assert!(
            std::env::var_os("CI").is_none(),
            "CI requires a real static Linux fixture"
        );
        return;
    };
    for mode in [
        #[cfg(target_arch = "x86_64")]
        "delete-unlink",
        #[cfg(target_arch = "x86_64")]
        "delete-rmdir",
        "delete-unlinkat",
        "delete-unlinkat-relative",
        "delete-directory-at",
    ] {
        for missing in [false, true] {
            check_removal_attempt(&executable, mode, missing);
        }
    }
}

#[cfg(unix)]
fn check_open_mode(executable: &str, mode: &str, write: bool) {
    let root = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    let path = outside.path().canonicalize().unwrap().join("blocked");
    if mode != "open-create" {
        fs::write(&path, "existing contents").unwrap();
    }
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            "schema_version=1\n[policy]\ndeny_writes=[{:?}]\n",
            path.to_str().unwrap()
        ),
    )
    .unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "modes.json",
            "--",
            executable,
            mode,
            path.to_str().unwrap(),
        ],
    );
    assert!(
        output.status.success(),
        "{mode}: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = parse(root.path(), "modes.json");
    let execution = &value["executions"][0];
    assert_eq!(execution["outcome"]["collection_complete"], true);
    assert_eq!(
        execution["accesses"][path.to_str().unwrap()]["read"],
        true,
        "{mode}"
    );
    assert_eq!(
        execution["accesses"][path.to_str().unwrap()]["write"],
        write,
        "{mode}"
    );
    assert_eq!(
        invoke(root.path(), &["policy", "check", "modes.json", "--json"])
            .status
            .code(),
        Some(if write { 5 } else { 0 })
    );
    if mode == "open-create" {
        assert!(path.exists());
    }
    if mode.starts_with("stream-") && write {
        assert!(fs::read(&path).unwrap().contains(&65));
    }
}

#[test]
#[cfg(unix)]
fn mutating_open_modes_cannot_pass_external_write_denials() {
    for mode in [
        "open-create",
        "open-truncate",
        "stream-rplus",
        "stream-wplus",
        "stream-aplus",
        "open-read",
        "stream-read",
    ] {
        check_open_mode(
            fixture(),
            mode,
            mode != "open-read" && mode != "stream-read",
        );
    }
}

#[test]
#[cfg(target_os = "linux")]
fn static_linux_open_modes_cannot_pass_external_write_denials() {
    let Ok(executable) = std::env::var("RUNLENS_STATIC_FIXTURE") else {
        assert!(
            std::env::var_os("CI").is_none(),
            "CI requires a real static Linux fixture"
        );
        return;
    };
    for mode in ["open-create", "open-truncate", "open-read"] {
        check_open_mode(&executable, mode, mode != "open-read");
    }
}

#[test]
fn historical_only_reports_cannot_pass_cache_or_policy_checks() {
    let root = repository("read");
    assert!(run(root.path(), "original.json", "read").status.success());
    let mut value = parse(root.path(), "original.json");
    value["executions"][0]["role"] = "baseline".into();
    value["verification"] = Value::Null;
    fs::write(
        root.path().join("historical.json"),
        serde_json::to_vec(&value).unwrap(),
    )
    .unwrap();
    for args in [
        vec![
            "cache",
            "check",
            "historical.json",
            "--command",
            "build",
            "--json",
        ],
        vec!["policy", "check", "historical.json", "--json"],
    ] {
        let output = invoke(root.path(), &args);
        assert_eq!(
            output.status.code(),
            Some(4),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let result: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(result["verdict"], "inconclusive");
        assert!(
            result["findings"]
                .as_object()
                .unwrap()
                .values()
                .any(|f| f["code"] == "failed-execution")
        );
    }
}

#[cfg(unix)]
fn check_path_mutation(executable: &str, mode: &str, failed: bool) {
    let root = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    let outside = outside.path().canonicalize().unwrap();
    let path = outside.join(if failed { "missing/blocked" } else { "blocked" });
    let creation = mode.contains("mkdir") || mode.contains("link");
    if !failed && !creation {
        fs::write(&path, "metadata target").unwrap();
    }
    fs::write(root.path().join("input.txt"), "hardlink source").unwrap();
    fs::write(
        root.path().join("runlens.toml"),
        format!(
            "schema_version=1\n[policy]\ndeny_writes=[{:?}]\n",
            path.to_str().unwrap()
        ),
    )
    .unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "mutation.json",
            "--",
            executable,
            mode,
            path.to_str().unwrap(),
            outside.to_str().unwrap(),
        ],
    );
    assert!(
        output.status.success(),
        "{mode}: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let report = parse(root.path(), "mutation.json");
    let execution = &report["executions"][0];
    assert_eq!(
        execution["accesses"][path.to_str().unwrap()]["write"],
        true,
        "{mode}: {}",
        execution["accesses"]
    );
    assert!(execution["changes"].get(path.to_str().unwrap()).is_none());
    assert_eq!(fs::symlink_metadata(&path).is_ok(), !failed);
    if mode.contains("symlink") {
        assert!(
            !execution["accesses"]
                .as_object()
                .unwrap()
                .keys()
                .any(|p| p.ends_with("opaque-target"))
        );
    }
    assert_eq!(
        invoke(root.path(), &["policy", "check", "mutation.json", "--json"])
            .status
            .code(),
        Some(5)
    );
}

#[test]
#[cfg(unix)]
fn path_only_mutations_cannot_pass_external_write_denials() {
    for mode in [
        "mutate-mkdir",
        "mutate-mkdirat",
        "mutate-chmod",
        "mutate-chmodat",
        "mutate-chown",
        "mutate-chownat",
        "mutate-truncate",
        "mutate-utimes",
        "mutate-utimensat",
        #[cfg(target_os = "linux")]
        "mutate-futimesat",
        "mutate-link",
        "mutate-linkat",
        "mutate-symlink",
        "mutate-symlinkat",
    ] {
        for failed in [false, true] {
            check_path_mutation(fixture(), mode, failed);
        }
    }
}

#[test]
#[cfg(target_os = "linux")]
fn static_linux_path_only_mutations_cannot_pass_external_write_denials() {
    let Ok(executable) = std::env::var("RUNLENS_STATIC_FIXTURE") else {
        assert!(
            std::env::var_os("CI").is_none(),
            "CI requires a real static Linux fixture"
        );
        return;
    };
    for mode in [
        "mutate-mkdirat",
        "mutate-chmodat",
        "mutate-chownat",
        "mutate-truncate",
        "mutate-utimensat",
        "mutate-linkat",
        "mutate-symlinkat",
    ] {
        for failed in [false, true] {
            check_path_mutation(&executable, mode, failed);
        }
    }
}

#[tokio::test]
async fn launched_image_identity_survives_or_detects_path_replacement() {
    use runlens::{config, execute, model::Role};
    for replace in [false, true] {
        let root = tempfile::tempdir().unwrap();
        let images = tempfile::tempdir().unwrap();
        let executable = images.path().join("original.exe");
        let replacement = images.path().join("replacement.exe");
        fs::copy(fixture(), &executable).unwrap();
        fs::copy(fixture(), &replacement).unwrap();
        let root = root.path().canonicalize().unwrap();
        fs::write(root.join("input"), "input").unwrap();
        let command = config::Command::direct(vec![
            executable.to_str().unwrap().into(),
            "read-write".into(),
        ]);
        let config = config::Config::default();
        let execution = execute::observe_with_launch_hook(
            execute::Request {
                root: &root,
                command: &command,
                name: None,
                config: &config,
                environment: std::env::vars_os().collect(),
                temporary: vec![],
                revision: None,
                working_tree_included: false,
                role: Role::Target,
                repetition: 1,
                cancellation: tokio_util::sync::CancellationToken::new(),
            },
            || {
                if replace {
                    let moved = fs::rename(&executable, images.path().join("retained.exe"));
                    #[cfg(windows)]
                    assert!(moved.is_err(), "launch guard must prevent replacement");
                    #[cfg(unix)]
                    {
                        moved.unwrap();
                        fs::rename(&replacement, &executable).unwrap();
                    }
                }
            },
        )
        .await
        .unwrap();
        assert!(root.join("out").exists());
        if replace && cfg!(unix) {
            assert!(execution.environment.executable_sha256.is_none());
            assert!(!execution.outcome.collection_complete);
        } else {
            assert!(execution.environment.executable_sha256.is_some());
            // This in-process API test inherits the test runner's OS streams.
            // Redirected test logs are regular files, so image identity can
            // remain known while independent descriptor coverage is incomplete.
            use std::io::IsTerminal;
            assert_eq!(
                execution.outcome.collection_complete,
                runlens::platform::inherited_stdio_complete(!std::io::stdin().is_terminal()),
                "{:?}",
                execution.outcome
            );
        }
    }
}

#[test]
#[cfg(target_os = "linux")]
fn dynamic_linux_inline_syscalls_cannot_pass_external_write_denials() {
    for missing in [false, true] {
        check_removal_attempt(fixture(), "delete-raw-syscall", missing);
    }
}

#[test]
#[cfg(target_os = "linux")]
fn dynamic_linux_syscall_arities_preserve_results_and_observations() {
    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "input").unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "syscalls.json",
            "--",
            fixture(),
            "syscall-arities",
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let report = parse(root.path(), "syscalls.json");
    assert_eq!(
        report["executions"][0]["accesses"]["${workspace}/input.txt"]["read"],
        true
    );
}

#[test]
fn report_parser_requires_kind_specific_known_metadata() {
    use runlens::{model::*, report};
    let root = tempfile::tempdir().unwrap();
    fs::write(root.path().join("input.txt"), "input").unwrap();
    assert!(run(root.path(), "original.json", "read").status.success());
    let original = report::read(&root.path().join("original.json")).unwrap();
    for kind in [
        FileKind::File,
        FileKind::Directory,
        FileKind::Symlink,
        FileKind::Other,
    ] {
        let mut value = original.clone();
        let state = FileState {
            knowledge: Knowledge::Known,
            kind: Some(kind),
            size: None,
            sha256: None,
            executable: None,
            link_target: None,
            reason: None,
        };
        // Identical forged states have no derived change; metadata validation
        // must reject them before they can establish output equality.
        value.executions[0]
            .before
            .insert("${workspace}/forged".into(), state.clone())
            .unwrap();
        value.executions[0]
            .after
            .insert("${workspace}/forged".into(), state)
            .unwrap();
        assert!(report::validate(&value).is_err(), "{kind:?}");
    }
    if cfg!(unix) {
        let mut value = original.clone();
        let key = "${workspace}/input.txt";
        let mut state = value.executions[0].before.get(key).unwrap().unwrap();
        state.executable = None;
        value.executions[0]
            .before
            .insert(key.into(), state.clone())
            .unwrap();
        value.executions[0].after.insert(key.into(), state).unwrap();
        assert!(report::validate(&value).is_err());
    }
}

#[test]
#[cfg(unix)]
fn vanished_symlink_ancestor_cannot_establish_workspace_scope() {
    let root = tempfile::tempdir().unwrap();
    let external = tempfile::tempdir().unwrap();
    fs::write(external.path().join("input.txt"), "external").unwrap();
    let output = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "alias.json",
            "--",
            fixture(),
            "transient-symlink",
            external.path().to_str().unwrap(),
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = parse(root.path(), "alias.json");
    assert_eq!(
        value["executions"][0]["accesses"]["${workspace}/transient/input.txt"]["in_scope"],
        false
    );
    assert!(!root.path().join("transient").exists());
    assert_eq!(
        invoke(root.path(), &["policy", "check", "alias.json", "--json"])
            .status
            .code(),
        Some(4)
    );
}

#[test]
fn clean_retains_masked_home_and_cache_accesses_for_policy() {
    let root = repository("env");
    let output = invoke(
        root.path(),
        &["verify", "clean", "build", "--save", "env.json"],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let value = parse(root.path(), "env.json");
    let execution = &value["executions"][0];
    for path in [
        "${temporary}/HOME/previous-run",
        "${temporary}/XDG_CACHE_HOME/entry",
    ] {
        assert_eq!(execution["accesses"][path]["write"], true, "{path}");
        assert_eq!(execution["accesses"][path]["in_scope"], false);
        assert!(execution["before"].get(path).is_none());
        assert!(execution["after"].get(path).is_none());
    }
    assert_eq!(
        execution["accesses"]["${temporary}/XDG_CACHE_HOME/entry"]["read"],
        true
    );
    let config_path = root.path().join("runlens.toml");
    let mut config = fs::read_to_string(&config_path).unwrap();
    config.push_str("\n[policy]\ndeny_writes = [\"**/HOME/**\"]\n");
    fs::write(config_path, config).unwrap();
    assert_eq!(
        invoke(root.path(), &["policy", "check", "env.json", "--json"])
            .status
            .code(),
        Some(5)
    );
    let serialized = fs::read_to_string(root.path().join("env.json")).unwrap();
    assert!(!serialized.contains("cache content"));
    assert!(!serialized.contains(root.path().to_str().unwrap()));
}

#[test]
fn cache_globs_cover_concrete_membership_ancestors_only() {
    use runlens::{analysis, config, entries::Entries, model::*, report};
    for extra in [None, Some("generated/a/uncovered.txt"), Some("uncovered/")] {
        let root = tempfile::tempdir().unwrap();
        let mut args = vec![
            "run",
            "--save",
            "outputs.json",
            "--",
            fixture(),
            "policy-write",
            "generated/a/result.js",
        ];
        if let Some(extra) = extra {
            args.push(extra);
        }
        assert!(invoke(root.path(), &args).status.success());
        let mut value = report::read(&root.path().join("outputs.json")).unwrap();
        let expected = value.executions[0].command.clone();
        let mut command = config::Command::direct(expected.argv.clone());
        command.outputs = vec!["generated/*/*.js".into()];
        // Direct ancestor writes must still fail independently. Then isolate
        // the snapshot membership rule from those explicit mkdir attempts.
        let direct = analysis::cache(&value, &command, &expected).unwrap();
        assert_eq!(direct.verdict, Some(Verdict::Failed));
        value.executions[0].accesses = Entries::default();
        let checked = analysis::cache(&value, &command, &expected).unwrap();
        assert_eq!(
            checked.verdict,
            Some(if extra.is_some() {
                Verdict::Failed
            } else {
                Verdict::Passed
            })
        );
        for finding in checked.findings.iter() {
            let (_, finding) = finding.unwrap();
            if finding.code == FindingCode::UncoveredOutput {
                assert!(extra.is_some());
                assert!(
                    !finding
                        .evidence
                        .iter()
                        .any(|e| e.path.as_deref() == Some("${workspace}/generated/a"))
                );
            }
        }
    }
}

#[test]
fn clean_and_repeat_collection_loss_is_inconclusive() {
    for (kind, mode, expected) in [
        ("clean", "overflow", 4),
        ("repeat", "overflow", 4),
        ("clean", "fail", 5),
    ] {
        let root = repository(mode);
        let config_path = root.path().join("runlens.toml");
        let mut config = fs::read_to_string(&config_path).unwrap();
        config.push_str("\n[limits]\nmemory_bytes = 8192\ntotal_bytes = 8192\nmax_paths = 32\n");
        fs::write(config_path, config).unwrap();
        git(root.path(), &["add", "runlens.toml"]);
        git(root.path(), &["commit", "-qm", "bounded collector"]);
        let output = invoke(
            root.path(),
            &["verify", kind, "build", "--save", "limited.json"],
        );
        assert_eq!(
            output.status.code(),
            Some(expected),
            "{kind}/{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        let report = parse(root.path(), "limited.json");
        assert_eq!(
            report["verification"],
            if expected == 4 {
                "inconclusive"
            } else {
                "failed"
            }
        );
        if expected == 4 {
            assert_eq!(report["executions"][0]["outcome"]["child_exit_code"], 0);
            assert_eq!(
                report["executions"][0]["outcome"]["collection_complete"],
                false
            );
        }
    }
}

#[cfg(target_os = "linux")]
#[test]
fn shebang_execution_preserves_output_without_claiming_native_identity() {
    use std::os::unix::fs::PermissionsExt;
    for header in ["#!/bin/sh", "#!/usr/bin/env sh"] {
        let root = tempfile::tempdir().unwrap();
        let script = root.path().join("script");
        fs::write(&script, format!("{header}\nprintf output > result\n")).unwrap();
        fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
        let result = invoke(
            root.path(),
            &["run", "--save", "script.json", "--", "./script"],
        );
        assert_eq!(
            result.status.code(),
            Some(4),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(
            fs::read_to_string(root.path().join("result")).unwrap(),
            "output"
        );
        let report = parse(root.path(), "script.json");
        let execution = &report["executions"][0];
        assert!(execution["environment"]["executable_sha256"].is_null());
        assert_eq!(execution["outcome"]["child_exit_code"], 0);
        assert_eq!(execution["outcome"]["collection_complete"], false);
    }
}

#[test]
fn policy_declarations_require_the_recorded_argv_and_working_directory() {
    let root = repository("read");
    assert!(
        invoke(
            root.path(),
            &["run", "--command", "build", "--save", "identity.json"]
        )
        .status
        .success()
    );
    let mut original = parse(root.path(), "identity.json");
    original["executions"][0]["accesses"] = serde_json::json!({});
    let config = root.path().join("runlens.toml");
    let original_config = fs::read_to_string(&config).unwrap();
    fs::write(
        &config,
        format!("{original_config}\n[policy]\nrequire_inputs = true\nrequire_outputs = true\n"),
    )
    .unwrap();
    for (field, value, expected) in [
        ("name", serde_json::json!("build"), 0),
        ("argv", serde_json::json!([fixture(), "read-write"]), 4),
        ("cwd", serde_json::json!("${workspace}/other"), 4),
        ("argv", serde_json::json!([fixture(), "[redacted]"]), 4),
    ] {
        let mut report = original.clone();
        report["executions"][0]["command"][field] = value;
        fs::write(
            root.path().join("identity.json"),
            serde_json::to_vec(&report).unwrap(),
        )
        .unwrap();
        let output = invoke(root.path(), &["policy", "check", "identity.json", "--json"]);
        assert_eq!(
            output.status.code(),
            Some(expected),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(
            !root.path().join("out").exists(),
            "offline policy must not execute configured argv"
        );
    }
}

#[test]
fn explain_matches_windows_case_aliases_in_accesses_and_changes() {
    use runlens::{analysis, entries::Entries, model::*};
    let root = repository("read");
    assert!(run(root.path(), "query.json", "read").status.success());
    let mut report = runlens::report::read(&root.path().join("query.json")).unwrap();
    let execution = &mut report.executions[0];
    execution.accesses = Entries::default();
    execution.changes = Entries::default();
    for path in [
        "${workspace}/Private/File",
        "C:/Private/File",
        "//Server/Share/File",
        "${workspace}/Ä/Datei",
    ] {
        execution
            .accesses
            .insert(
                path.into(),
                Access {
                    read: true,
                    write: false,
                    read_directory: false,
                    unsupported: false,
                    in_scope: true,
                },
            )
            .unwrap();
        execution
            .changes
            .insert(format!("{path}.out"), ChangeKind::Created)
            .unwrap();
    }
    for os in ["windows", "linux"] {
        report.executions[0].environment.os = os.into();
        for query in [
            "private/file",
            "c:/private/file",
            "//server/share/file",
            "ä/datei",
        ] {
            let result = analysis::explain(query, &[report.clone()]).unwrap();
            assert_eq!(
                result.usages.len(),
                usize::from(os == "windows"),
                "{os}: {query}"
            );
            if os == "windows" {
                assert!(
                    result
                        .usages
                        .iter()
                        .next()
                        .unwrap()
                        .unwrap()
                        .1
                        .consumer_candidate
                );
            }
            let result = analysis::explain(&format!("{query}.out"), &[report.clone()]).unwrap();
            assert_eq!(result.usages.len(), usize::from(os == "windows"));
            if os == "windows" {
                assert!(
                    result
                        .usages
                        .iter()
                        .next()
                        .unwrap()
                        .unwrap()
                        .1
                        .producer_candidate
                );
            }
        }
    }
}

#[test]
fn cache_rejects_declared_overlap_without_an_observed_intersection() {
    use runlens::{analysis, config::Command, entries::Entries, model::*};
    let root = repository("read");
    assert!(run(root.path(), "overlap.json", "read").status.success());
    let mut report = runlens::report::read(&root.path().join("overlap.json")).unwrap();
    report.executions[0].accesses = Entries::default();
    report.executions[0].changes = Entries::default();
    let expected = report.executions[0].command.clone();
    let mut command = Command::direct(expected.argv.clone());
    command.inputs = vec!["src/**".into()];
    command.outputs = vec!["src/generated/**".into()];
    let check = analysis::cache(&report, &command, &expected).unwrap();
    assert_eq!(check.verdict, Some(Verdict::Failed));
    assert!(
        check
            .findings
            .iter()
            .any(|entry| entry.unwrap().1.code == FindingCode::InputOutputOverlap)
    );
    command.outputs = vec!["build/**".into()];
    assert_eq!(
        analysis::cache(&report, &command, &expected)
            .unwrap()
            .verdict,
        Some(Verdict::Passed)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_readlink_attempts_cannot_bypass_read_denials() {
    let mut executables = vec![fixture().to_owned()];
    match std::env::var("RUNLENS_STATIC_FIXTURE") {
        Ok(path) => executables.push(path),
        Err(_) => assert!(
            std::env::var_os("CI").is_none(),
            "CI must build the static fixture"
        ),
    }
    for executable in executables {
        for mode in [
            "readlinkat",
            "readlinkat-empty",
            #[cfg(target_arch = "x86_64")]
            "readlink",
        ] {
            for present in [true, false] {
                if !present && mode == "readlinkat-empty" {
                    continue;
                }
                let root = tempfile::tempdir().unwrap();
                let external = tempfile::tempdir().unwrap();
                let path = external.path().join("dependency-link");
                if present {
                    std::os::unix::fs::symlink("opaque-target-text", &path).unwrap();
                }
                fs::write(
                    root.path().join("runlens.toml"),
                    "schema_version = 1\n[policy]\ndeny_reads = ['**/dependency-link']\n",
                )
                .unwrap();
                let result = invoke(
                    root.path(),
                    &[
                        "run",
                        "--save",
                        "readlink.json",
                        "--",
                        &executable,
                        mode,
                        path.to_str().unwrap(),
                    ],
                );
                assert!(
                    result.status.success(),
                    "{}",
                    String::from_utf8_lossy(&result.stderr)
                );
                let report = parse(root.path(), "readlink.json");
                let accesses = report["executions"][0]["accesses"].as_object().unwrap();
                assert!(accesses.iter().any(
                    |(key, access)| key.ends_with("/dependency-link") && access["read"] == true
                ));
                assert!(
                    !accesses
                        .keys()
                        .any(|key| key.ends_with("/opaque-target-text"))
                );
                assert_eq!(
                    invoke(root.path(), &["policy", "check", "readlink.json", "--json"])
                        .status
                        .code(),
                    Some(5)
                );
            }
        }
    }
}

#[test]
fn stopped_clean_reports_retain_referenced_baseline_evidence() {
    for (mode, expected) in [("fail", 5), ("overflow", 4)] {
        let root = repository(mode);
        let config_path = root.path().join("runlens.toml");
        let config = fs::read_to_string(&config_path).unwrap();
        fs::write(
            &config_path,
            format!(
                "{config}\n[limits]\nmemory_bytes = 8192\ntotal_bytes = 8192\nmax_paths = 32\n"
            ),
        )
        .unwrap();
        let initial = invoke(
            root.path(),
            &["run", "--command", "build", "--save", "baseline.json"],
        );
        assert!(
            matches!(initial.status.code(), Some(1 | 4)),
            "{}",
            String::from_utf8_lossy(&initial.stderr)
        );
        let baseline_path = root.path().join("baseline.json");
        let mut baseline = runlens::report::read(&baseline_path).unwrap();
        baseline.executions[0].environment.architecture = "different-architecture".into();
        baseline.executions[0].outcome.collection_complete = false;
        fs::write(&baseline_path, serde_json::to_vec(&baseline).unwrap()).unwrap();
        let config = fs::read_to_string(&config_path).unwrap();
        fs::write(
            &config_path,
            format!("{config}\n[policy]\nfail_new_accesses = true\n"),
        )
        .unwrap();
        let output = invoke(
            root.path(),
            &[
                "verify",
                "clean",
                "build",
                "--baseline",
                "baseline.json",
                "--save",
                "stopped.json",
            ],
        );
        assert_eq!(
            output.status.code(),
            Some(expected),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        let report = runlens::report::read(&root.path().join("stopped.json")).unwrap();
        let historical = report
            .executions
            .iter()
            .find(|execution| execution.id == baseline.executions[0].id)
            .unwrap();
        assert_eq!(historical.role, runlens::model::Role::Baseline);
        assert_eq!(report.current_executions().count(), 1);
        assert!(report.findings.iter().any(|entry| {
            entry
                .unwrap()
                .1
                .evidence
                .iter()
                .any(|item| item.execution_id == historical.id)
        }));
        assert!(
            invoke(root.path(), &["receipt", "stopped.json", "--json"])
                .status
                .success()
        );
    }
}

#[cfg(target_os = "linux")]
#[test]
fn linux_null_empty_path_metadata_retains_directory_read_evidence() {
    let root = tempfile::tempdir().unwrap();
    let result = run(root.path(), "stat.json", "stat-empty-path");
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let report = parse(root.path(), "stat.json");
    assert_eq!(
        report["executions"][0]["accesses"]["${workspace}"]["read"],
        true
    );
    assert_eq!(
        report["executions"][0]["outcome"]["collection_complete"],
        true
    );
}

#[test]
fn inherited_standard_files_preserve_io_but_cannot_certify_policy() {
    for stream in ["stdin", "stdout", "stderr"] {
        let root = tempfile::tempdir().unwrap();
        let external = tempfile::tempdir().unwrap();
        let path = external.path().join("standard-file");
        fs::write(&path, "STDIN-CANARY").unwrap();
        fs::write(
            root.path().join("runlens.toml"),
            "schema_version = 1\n[policy]\ndeny_reads = [\"**/standard-file\"]\ndeny_writes = \
             [\"**/standard-file\"]\n",
        )
        .unwrap();
        let mut command = Command::new(binary());
        command.current_dir(root.path()).args([
            "--log-level",
            "off",
            "run",
            "--save",
            "stdio.json",
            "--",
            fixture(),
            if stream == "stdin" { "stdin" } else { "stdio" },
        ]);
        match stream {
            "stdin" => {
                command.stdin(fs::File::open(&path).unwrap());
            }
            "stdout" => {
                command.stdout(fs::File::create(&path).unwrap());
            }
            "stderr" => {
                command.stderr(fs::File::create(&path).unwrap());
            }
            _ => unreachable!(),
        }
        let result = command.output().unwrap();
        assert_eq!(result.status.code(), Some(4), "{stream}: {result:?}");
        if stream == "stdin" {
            assert_eq!(result.stdout, b"STDIN-CANARY");
        } else {
            assert!(
                fs::read_to_string(&path)
                    .unwrap()
                    .contains(if stream == "stdout" {
                        "STDOUT-CANARY"
                    } else {
                        "STDERR-CANARY"
                    })
            );
        }
        let report = parse(root.path(), "stdio.json");
        assert_eq!(report["executions"][0]["outcome"]["child_exit_code"], 0);
        assert_eq!(
            report["executions"][0]["outcome"]["collection_complete"],
            false
        );
        assert!(
            !fs::read_to_string(root.path().join("stdio.json"))
                .unwrap()
                .contains("CANARY")
        );
        let policy = invoke(root.path(), &["policy", "check", "stdio.json", "--json"]);
        assert_eq!(policy.status.code(), Some(4));
    }
}

#[cfg(target_os = "linux")]
#[test]
fn raw_shebang_execs_cannot_claim_complete_interpreter_coverage() {
    use std::os::unix::fs::PermissionsExt;
    let mut executables = vec![fixture().to_owned()];
    match std::env::var("RUNLENS_STATIC_FIXTURE") {
        Ok(path) => executables.push(path),
        Err(_) => assert!(
            std::env::var_os("CI").is_none(),
            "static fixture required in CI"
        ),
    }
    for executable in executables {
        for mode in ["execve-script", "execveat-script", "execveat-empty-script"] {
            let root = tempfile::tempdir().unwrap();
            fs::write(
                root.path().join("script"),
                "#!/bin/sh\nprintf executed > result\n",
            )
            .unwrap();
            fs::set_permissions(
                root.path().join("script"),
                fs::Permissions::from_mode(0o700),
            )
            .unwrap();
            fs::write(
                root.path().join("runlens.toml"),
                "schema_version = 1\n[policy]\ndeny_reads = [\"**/bin/sh\"]\n",
            )
            .unwrap();
            let result = invoke(
                root.path(),
                &[
                    "run",
                    "--save",
                    "script.json",
                    "--",
                    &executable,
                    mode,
                    "./script",
                ],
            );
            assert_eq!(
                result.status.code(),
                Some(4),
                "{executable}/{mode}: {result:?}"
            );
            assert_eq!(
                fs::read_to_string(root.path().join("result")).unwrap(),
                "executed"
            );
            let report = parse(root.path(), "script.json");
            let execution = &report["executions"][0];
            assert_eq!(execution["outcome"]["child_exit_code"], 0);
            assert_eq!(execution["outcome"]["collection_complete"], false);
            assert!(
                execution["accesses"]
                    .as_object()
                    .unwrap()
                    .iter()
                    .any(|(path, access)| path.ends_with("/script")
                        && access["read"] == true
                        && access["unsupported"] == true)
            );
            let policy = invoke(root.path(), &["policy", "check", "script.json", "--json"]);
            assert!(matches!(policy.status.code(), Some(4 | 5)), "{policy:?}");
        }
    }
}

#[cfg(target_os = "linux")]
#[test]
fn linux_extended_attribute_reads_cannot_bypass_read_denials() {
    let mut executables = vec![fixture().to_owned()];
    match std::env::var("RUNLENS_STATIC_FIXTURE") {
        Ok(path) => executables.push(path),
        Err(_) => assert!(
            std::env::var_os("CI").is_none(),
            "static fixture required in CI"
        ),
    }
    for executable in executables {
        for mode in [
            "getxattr",
            "lgetxattr",
            "fgetxattr",
            "listxattr",
            "llistxattr",
            "flistxattr",
        ] {
            for present in [true, false] {
                if !present && mode.starts_with('f') {
                    continue;
                }
                let root = tempfile::tempdir().unwrap();
                let external = tempfile::tempdir().unwrap();
                let path = external.path().join("attribute-input");
                if present {
                    fs::write(&path, "payload").unwrap();
                    let name = std::ffi::CString::new(path.to_str().unwrap()).unwrap();
                    // SAFETY: live strings and bounded value bytes.
                    assert_eq!(
                        unsafe {
                            libc::setxattr(
                                name.as_ptr(),
                                c"user.runlens".as_ptr(),
                                b"XATTR-CANARY".as_ptr().cast(),
                                12,
                                0,
                            )
                        },
                        0
                    );
                }
                fs::write(
                    root.path().join("runlens.toml"),
                    "schema_version = 1\n[policy]\ndeny_reads = [\"**/attribute-input\"]\n",
                )
                .unwrap();
                let result = invoke(
                    root.path(),
                    &[
                        "run",
                        "--save",
                        "attributes.json",
                        "--",
                        &executable,
                        mode,
                        path.to_str().unwrap(),
                    ],
                );
                assert!(
                    result.status.success(),
                    "{executable}/{mode}/{present}: {result:?}"
                );
                let report = parse(root.path(), "attributes.json");
                assert!(
                    report["executions"][0]["accesses"]
                        .as_object()
                        .unwrap()
                        .iter()
                        .any(|(path, access)| path.ends_with("/attribute-input")
                            && access["read"] == true)
                );
                assert!(
                    !fs::read_to_string(root.path().join("attributes.json"))
                        .unwrap()
                        .contains("XATTR-CANARY")
                );
                let policy = invoke(
                    root.path(),
                    &["policy", "check", "attributes.json", "--json"],
                );
                assert_eq!(policy.status.code(), Some(5), "{policy:?}");
            }
        }
    }
}

#[cfg(unix)]
#[test]
fn detached_descendants_cannot_certify_a_complete_lifecycle() {
    let mut executables = vec![fixture().to_owned()];
    #[cfg(target_os = "linux")]
    match std::env::var("RUNLENS_STATIC_FIXTURE") {
        Ok(path) => executables.push(path),
        Err(_) => assert!(
            std::env::var_os("CI").is_none(),
            "static fixture required in CI"
        ),
    }
    for executable in executables.drain(..) {
        for mode in ["detach-setsid", "detach-setpgid", "detach-spawn"] {
            if mode == "detach-spawn" && executable != fixture() {
                continue;
            }
            let root = tempfile::tempdir().unwrap();
            let external = tempfile::tempdir().unwrap();
            let output = external.path().join("detached-output");
            let result = invoke(
                root.path(),
                &[
                    "run",
                    "--save",
                    "detached.json",
                    "--",
                    &executable,
                    mode,
                    output.to_str().unwrap(),
                ],
            );
            assert_eq!(
                result.status.code(),
                Some(4),
                "{executable}/{mode}: {result:?}"
            );
            let report = parse(root.path(), "detached.json");
            assert_eq!(report["executions"][0]["outcome"]["child_exit_code"], 0);
            assert_eq!(
                report["executions"][0]["outcome"]["collection_complete"],
                false
            );
            let policy = invoke(root.path(), &["policy", "check", "detached.json", "--json"]);
            assert_eq!(policy.status.code(), Some(4));
            // The unsupported descendant is finite by construction. Await its
            // final write before dropping the fixture's external directory.
            let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
            while fs::read(&output).unwrap_or_default() != b"done"
                && std::time::Instant::now() < deadline
            {
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
            assert_eq!(fs::read(&output).unwrap(), b"done");
        }
    }
}

#[cfg(windows)]
#[test]
fn direct_windows_native_creation_cannot_claim_child_coverage() {
    let root = tempfile::tempdir().unwrap();
    let external = tempfile::tempdir().unwrap();
    let output = external.path().join("native-output");
    fs::write(
        root.path().join("runlens.toml"),
        "schema_version = 1\n[policy]\ndeny_writes = [\"**/native-output\"]\n",
    )
    .unwrap();
    let result = invoke(
        root.path(),
        &[
            "run",
            "--save",
            "native.json",
            "--",
            fixture(),
            "windows-native-child",
            output.to_str().unwrap(),
        ],
    );
    assert_eq!(result.status.code(), Some(4), "{result:?}");
    assert_eq!(fs::read_to_string(&output).unwrap(), "native child output");
    let report = parse(root.path(), "native.json");
    assert_eq!(report["executions"][0]["outcome"]["child_exit_code"], 0);
    assert_eq!(
        report["executions"][0]["outcome"]["collection_complete"],
        false
    );
    let policy = invoke(root.path(), &["policy", "check", "native.json", "--json"]);
    assert_eq!(policy.status.code(), Some(4), "{policy:?}");
}
