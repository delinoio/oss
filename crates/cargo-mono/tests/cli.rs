use std::{
    fs,
    path::Path,
    process::{Command as ProcessCommand, Output},
};

use assert_cmd::Command;
use predicates::prelude::*;
use serde_json::{json, Value};
use toml_edit::{DocumentMut, Item, Value as TomlValue};

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
fn changed_excludes_agents_docs_by_default() {
    let temp_dir = init_library_workspace();
    fs::write(
        temp_dir.path().join("crates/alpha/AGENTS.md"),
        "agent docs\n",
    )
    .expect("failed to write AGENTS.md");
    run_git(temp_dir.path(), &["add", "crates/alpha/AGENTS.md"]);
    run_git(
        temp_dir.path(),
        &[
            "-c",
            "user.name=test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-q",
            "-m",
            "docs",
        ],
    );

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args([
            "--output",
            "json",
            "changed",
            "--base",
            "HEAD~1",
            "--direct-only",
        ])
        .assert()
        .success()
        .stdout(predicate::str::contains("\"packages\": []"));
}

#[test]
fn changed_include_path_reincludes_agents_docs() {
    let temp_dir = init_library_workspace();
    fs::write(
        temp_dir.path().join("crates/alpha/AGENTS.md"),
        "agent docs\n",
    )
    .expect("failed to write AGENTS.md");
    run_git(temp_dir.path(), &["add", "crates/alpha/AGENTS.md"]);
    run_git(
        temp_dir.path(),
        &[
            "-c",
            "user.name=test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-q",
            "-m",
            "docs",
        ],
    );

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args([
            "--output",
            "json",
            "changed",
            "--base",
            "HEAD~1",
            "--direct-only",
            "--include-path",
            "**/AGENTS.md",
        ])
        .assert()
        .success()
        .stdout(predicate::str::contains("\"alpha\""));
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
    command.env("CARGO_MONO_LOG_COLOR", "never");

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

#[test]
fn list_human_output_marks_publishability() {
    let temp_dir = init_mixed_publishability_workspace();

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .arg("list")
        .assert()
        .success()
        .stdout(predicate::str::contains("Workspace packages: 3"))
        .stdout(predicate::str::contains("alpha 0.1.0 (publishable)"))
        .stdout(predicate::str::contains("beta 0.2.0 (publishable)"))
        .stdout(predicate::str::contains("gamma 0.3.0 (non-publishable)"));
}

#[test]
fn changed_include_uncommitted_controls_untracked_file_detection() {
    let temp_dir = init_mixed_publishability_workspace();

    let without_uncommitted =
        run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
            "--output",
            "json",
            "changed",
            "--base",
            "HEAD",
            "--direct-only",
        ]));
    let without_uncommitted_json = parse_stdout_json(&without_uncommitted);
    assert_eq!(without_uncommitted_json["packages"], json!([]));

    // cargo metadata may create an untracked Cargo.lock; track it so the next
    // include-uncommitted assertion only reflects our explicit test change.
    let generated_lockfile = temp_dir.path().join("Cargo.lock");
    if generated_lockfile.exists() {
        run_git(temp_dir.path(), &["add", "Cargo.lock"]);
        run_git(
            temp_dir.path(),
            &[
                "-c",
                "user.name=test",
                "-c",
                "user.email=test@example.com",
                "commit",
                "-q",
                "-m",
                "chore: track generated lockfile",
            ],
        );
    }

    fs::write(
        temp_dir.path().join("crates/alpha/src/untracked.rs"),
        "pub fn untracked() {}\n",
    )
    .expect("failed to write untracked source");

    let with_uncommitted = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "changed",
        "--base",
        "HEAD",
        "--direct-only",
        "--include-uncommitted",
    ]));
    let with_uncommitted_json = parse_stdout_json(&with_uncommitted);
    assert_eq!(with_uncommitted_json["packages"], json!(["alpha"]));
}

#[test]
fn changed_includes_dependents_unless_direct_only() {
    let temp_dir = init_mixed_publishability_workspace();
    fs::write(
        temp_dir.path().join("crates/alpha/src/lib.rs"),
        "pub fn alpha() -> &'static str { \"alpha-updated\" }\n",
    )
    .expect("failed to update alpha source");
    run_git(temp_dir.path(), &["add", "crates/alpha/src/lib.rs"]);
    run_git(
        temp_dir.path(),
        &[
            "-c",
            "user.name=test",
            "-c",
            "user.email=test@example.com",
            "commit",
            "-q",
            "-m",
            "feat: update alpha",
        ],
    );

    let include_dependents = run_success(
        cargo_mono_command()
            .current_dir(temp_dir.path())
            .args(["--output", "json", "changed", "--base", "HEAD~1"]),
    );
    let include_dependents_json = parse_stdout_json(&include_dependents);
    assert_eq!(
        include_dependents_json["packages"],
        json!(["alpha", "beta", "gamma"])
    );

    let direct_only = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "changed",
        "--base",
        "HEAD~1",
        "--direct-only",
    ]));
    let direct_only_json = parse_stdout_json(&direct_only);
    assert_eq!(direct_only_json["packages"], json!(["alpha"]));
}

#[test]
fn changed_global_impact_files_cannot_be_excluded() {
    let temp_dir = init_mixed_publishability_workspace();
    fs::write(temp_dir.path().join("Cargo.lock"), "# lock\n").expect("failed to write lockfile");

    let output = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "changed",
        "--base",
        "HEAD",
        "--direct-only",
        "--include-uncommitted",
        "--exclude-path",
        "Cargo.lock",
    ]));

    let parsed = parse_stdout_json(&output);
    assert_eq!(parsed["packages"], json!(["alpha", "beta", "gamma"]));
}

#[test]
fn changed_rejects_invalid_include_path_pattern() {
    let temp_dir = init_mixed_publishability_workspace();

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args(["changed", "--base", "HEAD", "--include-path", "["])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("Invalid --include-path pattern"));
}

#[test]
fn bump_updates_manifests_and_creates_commit_and_tag() {
    let temp_dir = init_mixed_publishability_workspace();

    let output = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "bump",
        "--level",
        "patch",
        "--package",
        "alpha",
    ]));

    let result = parse_stdout_json(&output);
    assert_eq!(
        result["bumped_packages"],
        json!([{
            "name": "alpha",
            "previous_version": "0.1.0",
            "new_version": "0.1.1",
            "source": "selected"
        }])
    );
    assert_eq!(result["commit"].as_str().is_some(), true);
    assert!(contains_json_string(&result["tags"], "alpha-v0.1.1"));

    let alpha_manifest = temp_dir.path().join("crates/alpha/Cargo.toml");
    let beta_manifest = temp_dir.path().join("crates/beta/Cargo.toml");
    let gamma_manifest = temp_dir.path().join("crates/gamma/Cargo.toml");
    let root_manifest = temp_dir.path().join("Cargo.toml");

    assert_eq!(
        manifest_package_version(&alpha_manifest),
        Some("0.1.1".to_string())
    );
    assert_eq!(
        manifest_dependency_version(&beta_manifest, "dependencies", "alpha"),
        Some("0.1.1".to_string())
    );
    assert_eq!(
        manifest_dependency_version(&gamma_manifest, "dependencies", "alpha"),
        Some("0.1.1".to_string())
    );
    assert_eq!(
        manifest_workspace_dependency_version(&root_manifest, "alpha"),
        Some("0.1.1".to_string())
    );

    assert_eq!(
        run_git_capture(temp_dir.path(), &["log", "-1", "--pretty=%s"]),
        "chore(release): bump 1 crate(s)"
    );
    assert_eq!(git_tags(temp_dir.path()), vec!["alpha-v0.1.1".to_string()]);
}

#[test]
fn bump_with_bump_dependents_adds_patch_bumps_for_reverse_dependencies() {
    let temp_dir = init_mixed_publishability_workspace();

    let output = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "bump",
        "--level",
        "patch",
        "--package",
        "alpha",
        "--bump-dependents",
    ]));

    let result = parse_stdout_json(&output);
    let bumped = result["bumped_packages"]
        .as_array()
        .expect("bumped_packages must be an array");
    assert_eq!(bumped.len(), 2);

    let alpha_result = bumped_package(&result, "alpha");
    assert_eq!(alpha_result["new_version"], json!("0.1.1"));
    assert_eq!(alpha_result["source"], json!("selected"));

    let beta_result = bumped_package(&result, "beta");
    assert_eq!(beta_result["new_version"], json!("0.2.1"));
    assert_eq!(beta_result["source"], json!("dependent"));

    let skipped = result["skipped_packages"]
        .as_array()
        .expect("skipped_packages must be an array");
    assert!(skipped.iter().any(|item| {
        item["name"] == json!("gamma") && item["reason"] == json!("non-publishable")
    }));

    assert_eq!(
        manifest_package_version(&temp_dir.path().join("crates/beta/Cargo.toml")),
        Some("0.2.1".to_string())
    );
    assert_eq!(
        run_git_capture(temp_dir.path(), &["log", "-1", "--pretty=%s"]),
        "chore(release): bump 2 crate(s)"
    );
    assert_eq!(
        git_tags(temp_dir.path()),
        vec!["alpha-v0.1.1".to_string(), "beta-v0.2.1".to_string()]
    );
}

#[test]
fn bump_reports_skip_when_only_non_publishable_packages_are_selected() {
    let temp_dir = init_mixed_publishability_workspace();
    let initial_head = run_git_capture(temp_dir.path(), &["rev-parse", "HEAD"]);

    let output = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "bump",
        "--level",
        "patch",
        "--package",
        "gamma",
    ]));
    let result = parse_stdout_json(&output);

    assert_eq!(result["bumped_packages"], json!([]));
    assert_eq!(
        result["skipped_packages"],
        json!([{ "name": "gamma", "reason": "non-publishable" }])
    );
    assert_eq!(
        run_git_capture(temp_dir.path(), &["rev-parse", "HEAD"]),
        initial_head
    );
    assert!(git_tags(temp_dir.path()).is_empty());
}

#[test]
fn publish_reports_skip_for_non_publishable_packages() {
    let temp_dir = init_mixed_publishability_workspace();

    let output = run_success(cargo_mono_command().current_dir(temp_dir.path()).args([
        "--output",
        "json",
        "publish",
        "--dry-run",
        "--package",
        "gamma",
    ]));

    let result = parse_stdout_json(&output);
    assert_eq!(result["mode"], json!("dry-run"));
    assert_eq!(result["published"], json!([]));
    assert_eq!(
        result["skipped"],
        json!([{ "name": "gamma", "reason": "non-publishable" }])
    );
    assert_eq!(result["failed"], json!([]));
}

#[test]
fn publish_rejects_unknown_packages() {
    let temp_dir = init_mixed_publishability_workspace();

    cargo_mono_command()
        .current_dir(temp_dir.path())
        .args(["publish", "--dry-run", "--package", "unknown"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("Unknown package(s): unknown"));
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

fn init_mixed_publishability_workspace() -> tempfile::TempDir {
    let temp_dir = tempfile::tempdir().expect("failed to create tempdir");
    let root = temp_dir.path();

    fs::create_dir_all(root.join("crates/alpha/src")).expect("failed to create alpha directory");
    fs::create_dir_all(root.join("crates/beta/src")).expect("failed to create beta directory");
    fs::create_dir_all(root.join("crates/gamma/src")).expect("failed to create gamma directory");

    fs::write(
        root.join("Cargo.toml"),
        r#"[workspace]
members = ["crates/alpha", "crates/beta", "crates/gamma"]
resolver = "2"

[workspace.dependencies]
alpha = { path = "crates/alpha", version = "0.1.0" }
"#,
    )
    .expect("failed to write workspace manifest");

    fs::write(
        root.join("crates/alpha/Cargo.toml"),
        r#"[package]
name = "alpha"
version = "0.1.0"
edition = "2021"
license = "MIT"
"#,
    )
    .expect("failed to write alpha manifest");
    fs::write(
        root.join("crates/beta/Cargo.toml"),
        r#"[package]
name = "beta"
version = "0.2.0"
edition = "2021"
license = "MIT"

[dependencies]
alpha = { path = "../alpha", version = "0.1.0" }
"#,
    )
    .expect("failed to write beta manifest");
    fs::write(
        root.join("crates/gamma/Cargo.toml"),
        r#"[package]
name = "gamma"
version = "0.3.0"
edition = "2021"
license = "MIT"
publish = false

[dependencies]
alpha = { path = "../alpha", version = "0.1.0" }
"#,
    )
    .expect("failed to write gamma manifest");

    fs::write(
        root.join("crates/alpha/src/lib.rs"),
        "pub fn alpha() -> &'static str { \"alpha\" }\n",
    )
    .expect("failed to write alpha source");
    fs::write(
        root.join("crates/beta/src/lib.rs"),
        "pub fn beta() -> &'static str { \"beta\" }\n",
    )
    .expect("failed to write beta source");
    fs::write(
        root.join("crates/gamma/src/lib.rs"),
        "pub fn gamma() -> &'static str { \"gamma\" }\n",
    )
    .expect("failed to write gamma source");

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

fn run_success(command: &mut Command) -> Output {
    let output = command.output().expect("failed to execute cargo-mono");
    assert!(
        output.status.success(),
        "command failed with status {}\nstdout:\n{}\nstderr:\n{}",
        output.status,
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    output
}

fn parse_stdout_json(output: &Output) -> Value {
    serde_json::from_slice::<Value>(&output.stdout).unwrap_or_else(|error| {
        panic!(
            "stdout was not valid JSON: {error}\nstdout:\n{}",
            String::from_utf8_lossy(&output.stdout)
        )
    })
}

fn contains_json_string(value: &Value, target: &str) -> bool {
    value
        .as_array()
        .is_some_and(|items| items.iter().any(|item| item.as_str() == Some(target)))
}

fn bumped_package<'a>(result: &'a Value, package_name: &str) -> &'a Value {
    result["bumped_packages"]
        .as_array()
        .and_then(|items| {
            items
                .iter()
                .find(|item| item["name"].as_str() == Some(package_name))
        })
        .unwrap_or_else(|| panic!("missing bumped package result for {package_name}"))
}

fn manifest_package_version(manifest_path: &Path) -> Option<String> {
    let document = parse_manifest(manifest_path);
    document["package"]["version"]
        .as_str()
        .map(ToString::to_string)
}

fn manifest_dependency_version(
    manifest_path: &Path,
    section: &str,
    dependency_name: &str,
) -> Option<String> {
    let document = parse_manifest(manifest_path);
    extract_dependency_version(&document[section][dependency_name])
}

fn manifest_workspace_dependency_version(
    manifest_path: &Path,
    dependency_name: &str,
) -> Option<String> {
    let document = parse_manifest(manifest_path);
    extract_dependency_version(&document["workspace"]["dependencies"][dependency_name])
}

fn parse_manifest(manifest_path: &Path) -> DocumentMut {
    let content = fs::read_to_string(manifest_path).unwrap_or_else(|error| {
        panic!(
            "failed to read manifest {}: {error}",
            manifest_path.display()
        )
    });
    content.parse::<DocumentMut>().unwrap_or_else(|error| {
        panic!(
            "failed to parse manifest {}: {error}",
            manifest_path.display()
        )
    })
}

fn extract_dependency_version(item: &Item) -> Option<String> {
    if let Some(value_item) = item.as_value() {
        return match value_item {
            TomlValue::String(value) => Some(value.value().to_string()),
            TomlValue::InlineTable(table) => table
                .get("version")
                .and_then(TomlValue::as_str)
                .map(ToString::to_string),
            _ => None,
        };
    }

    item.as_table().and_then(|table| {
        table
            .get("version")
            .and_then(Item::as_value)
            .and_then(TomlValue::as_str)
            .map(ToString::to_string)
    })
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

fn git_tags(working_dir: &Path) -> Vec<String> {
    let output = ProcessCommand::new("git")
        .current_dir(working_dir)
        .args(["tag", "--list"])
        .output()
        .expect("failed to run git tag --list");

    assert!(
        output.status.success(),
        "git tag --list failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );

    String::from_utf8_lossy(&output.stdout)
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .map(ToString::to_string)
        .collect()
}

fn run_git_capture(working_dir: &Path, args: &[&str]) -> String {
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

    String::from_utf8_lossy(&output.stdout).trim().to_string()
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
