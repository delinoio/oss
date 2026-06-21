use std::{fs, path::Path};

use assert_cmd::Command;
use predicates::prelude::*;
use serde_json::Value;
use sha2::{Digest, Sha256};

fn binpm() -> Command {
    Command::new(env!("CARGO_BIN_EXE_binpm"))
}

fn bash_path(path: &Path) -> String {
    let raw = path.display().to_string();
    #[cfg(windows)]
    {
        windows_path_for_posix_shell(&raw).unwrap_or(raw)
    }
    #[cfg(not(windows))]
    {
        raw
    }
}

#[cfg(windows)]
fn windows_path_for_posix_shell(raw: &str) -> Option<String> {
    if let Some(unc) = raw
        .strip_prefix(r"\\?\UNC\")
        .or_else(|| raw.strip_prefix(r"\\.\UNC\"))
    {
        return Some(format!("//{}", unc.replace('\\', "/")));
    }

    let raw = raw
        .strip_prefix(r"\\?\")
        .or_else(|| raw.strip_prefix(r"\\.\"))
        .unwrap_or(raw);

    if let Some(unc) = raw.strip_prefix(r"\\") {
        return Some(format!("//{}", unc.replace('\\', "/")));
    }

    let bytes = raw.as_bytes();
    if bytes.len() >= 3
        && bytes[0].is_ascii_alphabetic()
        && bytes[1] == b':'
        && matches!(bytes[2], b'\\' | b'/')
    {
        let drive = (bytes[0] as char).to_ascii_lowercase();
        let rest = raw[2..].replace('\\', "/");
        return Some(format!("/{drive}{rest}"));
    }

    None
}

fn posix_single_quote(raw: &str) -> String {
    format!("'{}'", raw.replace('\'', "'\\''"))
}

fn bash_quote_path(path: &Path) -> String {
    posix_single_quote(&bash_path(path))
}

#[cfg(all(target_os = "linux", target_arch = "x86_64", target_env = "gnu"))]
fn write_locked_tool_project(project: &Path, sha256: &str) {
    fs::create_dir_all(project).expect("create project");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
version = "1.0.0"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join("binpm.lock"),
        format!(
            r#"version = 1

[tools.tool]
source = "github:owner/tool"

[tools.tool.targets.linux-x86_64-gnu]
package_spec = "github:owner/tool@1.0.0"
source = "github:owner/tool"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool"
requested_version = "1.0.0"
release_tag = "1.0.0"
asset_name = "tool-linux"
asset_url = "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
target_os = "linux"
target_arch = "x86_64"
target_libc = "gnu"
archive_format = "bare-executable"
selected_binary = "tool-linux"
installed_path = ".binpm/bin/tool"
sha256 = "{sha256}"
checksum_source = "github-digest"
provider_digest_sha256 = "{sha256}"
signature_available = false
signature_verified = false
"#
        ),
    )
    .expect("write lockfile");
}

#[cfg(all(target_os = "linux", target_arch = "x86_64", target_env = "gnu"))]
fn write_cache_asset(home: &Path, sha256: &str, bytes: &[u8]) {
    let entry = home.join("cache").join("sha256").join(sha256);
    fs::create_dir_all(&entry).expect("create cache entry");
    fs::write(entry.join("asset"), bytes).expect("write cache asset");
}

#[test]
fn help_includes_initial_command_surface() {
    let mut command = binpm();

    command
        .arg("--help")
        .assert()
        .success()
        .stdout(predicate::str::contains("install"))
        .stdout(predicate::str::contains(
            "Execute a local manifest command or one-off package command",
        ))
        .stdout(predicate::str::contains("exec"))
        .stdout(predicate::str::contains("run"))
        .stdout(predicate::str::contains("cache"))
        .stdout(predicate::str::contains("verify"))
        .stdout(predicate::str::contains("env"))
        .stdout(predicate::str::contains("--verbose"))
        .stdout(predicate::str::contains("--debug"));
}

#[test]
fn verbose_flag_overrides_binpm_log_env_filter() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_LOG", "binpm=off")
        .env("BINPM_LOG_COLOR", "never")
        .env("BINPM_HOME", &home)
        .args(["--verbose", "env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains("Rendered PATH environment commands").not())
        .stderr(predicate::str::contains(
            "Rendered PATH environment commands",
        ));
}

#[test]
fn add_and_x_help_include_explicit_bin_selection() {
    let mut add = binpm();
    add.args(["add", "--help"])
        .assert()
        .success()
        .stdout(predicate::str::contains("--bin <BIN>"))
        .stdout(predicate::str::contains("--also <CMD=BIN>"))
        .stdout(predicate::str::contains("--manifest-only"));

    let mut install = binpm();
    install
        .args(["install", "--help"])
        .assert()
        .success()
        .stdout(predicate::str::contains("--as <CMD>"))
        .stdout(predicate::str::contains("--bin <BIN>"));

    let mut exec = binpm();
    exec.args(["x", "--help"])
        .assert()
        .success()
        .stdout(predicate::str::contains("--bin <BIN>"));
}

#[test]
fn update_help_marks_global_update_pending() {
    let mut command = binpm();

    command
        .args(["update", "--help"])
        .assert()
        .success()
        .stdout(predicate::str::contains("Global update is pending"))
        .stdout(predicate::str::contains("--global"));
}

#[test]
fn global_update_reports_pending_workaround() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["update", "--global"])
        .assert()
        .failure()
        .code(2)
        .stdout(predicate::str::contains("update scope: global"))
        .stderr(predicate::str::contains("pending implementation"))
        .stderr(predicate::str::contains("binpm outdated --global"))
        .stderr(predicate::str::contains("binpm info --global <cmd>"))
        .stderr(predicate::str::contains(
            "binpm install <source> --as <cmd> --bin <selected_binary>",
        ))
        .stderr(predicate::str::contains("binpm update --local"));
}

#[test]
fn global_update_dry_run_reports_same_pending_workaround() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["update", "--global", "--dry-run"])
        .assert()
        .failure()
        .code(2)
        .stdout(predicate::str::contains("planned updates").not())
        .stderr(predicate::str::contains("pending implementation"))
        .stderr(predicate::str::contains("binpm outdated --global"));
}

#[test]
fn add_manifest_only_writes_only_manifest_and_supports_additional_commands() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args([
            "add",
            "foo",
            "github:owner/tools@v1.2.3",
            "--bin",
            "bin/foo",
            "--also",
            "bar=bin/bar",
            "--manifest-only",
        ])
        .assert()
        .success()
        .stdout(predicate::str::contains("declared foo, bar"))
        .stdout(predicate::str::contains("manifest-only: did not update"))
        .stdout(predicate::str::contains("next: run `binpm install`"));

    let manifest = fs::read_to_string(temp_dir.path().join("binpm.toml")).expect("read manifest");
    assert_eq!(
        manifest,
        r#"version = 1

[tools.bar]
source = "github:owner/tools"
version = "v1.2.3"
bin = "bin/bar"

[tools.foo]
source = "github:owner/tools"
version = "v1.2.3"
bin = "bin/foo"
"#
    );
    assert!(!temp_dir.path().join("binpm.lock").exists());
    assert!(!temp_dir.path().join(".binpm").exists());
}

#[test]
fn add_rejects_duplicate_additional_command_declarations() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args([
            "add",
            "foo",
            "github:owner/tools@v1.2.3",
            "--bin",
            "bin/foo",
            "--also",
            "foo=bin/other",
            "--manifest-only",
        ])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains(
            "Duplicate local command declaration `foo`",
        ));

    assert!(!temp_dir.path().join("binpm.toml").exists());
    assert!(!temp_dir.path().join("binpm.lock").exists());
    assert!(!temp_dir.path().join(".binpm").exists());
}

#[test]
fn package_shortcut_without_command_keeps_source_explicit() {
    let mut command = binpm();

    command
        .args(["x", "--package", "not-a-source"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("Invalid source spec"));
}

#[test]
fn execution_aliases_accept_package_and_forwarded_flags() {
    for alias in ["exec", "run"] {
        let mut command = binpm();

        command
            .args([
                alias,
                "--package",
                "not-a-source",
                "tool",
                "--",
                "--package",
                "literal",
            ])
            .assert()
            .failure()
            .code(2)
            .stderr(predicate::str::contains("Invalid source spec"))
            .stderr(predicate::str::contains("literal").not());
    }
}

#[test]
fn init_writes_minimal_manifest() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let manifest_path = temp_dir.path().join("binpm.toml");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .arg("init")
        .assert()
        .success()
        .stdout(predicate::str::contains(format!(
            "manifest destination: {}",
            manifest_path.display()
        )))
        .stdout(predicate::str::contains(format!(
            "created manifest: {}",
            manifest_path.display()
        )));

    let manifest = std::fs::read_to_string(manifest_path).expect("read manifest");
    assert_eq!(manifest, "version = 1\n");
}

#[test]
fn init_from_nested_directory_writes_manifest_at_git_root() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let manifest_path = temp_dir.path().join("binpm.toml");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("init")
        .assert()
        .success()
        .stdout(predicate::str::contains(format!(
            "manifest destination: {}",
            manifest_path.display()
        )))
        .stdout(predicate::str::contains(format!(
            "created manifest: {}",
            manifest_path.display()
        )));

    let manifest = fs::read_to_string(manifest_path).expect("read manifest");
    assert_eq!(manifest, "version = 1\n");
    assert!(!nested_dir.join("binpm.toml").exists());
}

#[test]
fn init_from_nested_directory_detects_existing_manifest_without_git() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let manifest_path = temp_dir.path().join("binpm.toml");
    fs::write(&manifest_path, "version = 1\n").expect("write manifest");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("init")
        .assert()
        .failure()
        .code(2)
        .stdout(predicate::str::contains(format!(
            "manifest destination: {}",
            manifest_path.display()
        )))
        .stdout(predicate::str::contains("created manifest:").not())
        .stderr(predicate::str::contains(
            "Refusing to overwrite existing manifest",
        ));

    assert!(!nested_dir.join("binpm.toml").exists());
}

#[cfg(unix)]
#[test]
fn init_treats_broken_manifest_symlink_as_existing_manifest() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let manifest_path = temp_dir.path().join("binpm.toml");
    std::os::unix::fs::symlink(
        temp_dir.path().join("missing-manifest-target"),
        &manifest_path,
    )
    .expect("create broken manifest symlink");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("init")
        .assert()
        .failure()
        .code(2)
        .stdout(predicate::str::contains(format!(
            "manifest destination: {}",
            manifest_path.display()
        )))
        .stdout(predicate::str::contains("created manifest:").not())
        .stderr(predicate::str::contains(
            "Refusing to overwrite existing manifest",
        ));
}

#[test]
fn env_prints_shell_path_exports() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let local_bin = fs::canonicalize(temp_dir.path())
        .expect("canonical temp dir")
        .join(".binpm")
        .join("bin");
    let global_bin = home.join("bin");
    let expected_global = format!(
        "export PATH={}${{PATH:+:$PATH}}",
        bash_quote_path(&global_bin)
    );
    let expected_local = format!(
        "export PATH={}${{PATH:+:$PATH}}",
        bash_quote_path(&local_bin)
    );
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(
            "# Global bin: persist this line in shell profiles",
        ))
        .stdout(predicate::str::contains(expected_global))
        .stdout(predicate::str::contains(
            "# Project-local bin: use for the current project/session only",
        ))
        .stdout(predicate::str::contains(expected_local));
}

#[test]
fn env_bash_avoids_empty_path_segment_when_path_is_unset() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains("${PATH:+:$PATH}"))
        .stdout(predicate::str::contains(":\"$PATH\"").not());
}

#[test]
fn env_ignores_empty_home_overrides() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let fallback_home = temp_dir.path().join("fallback-home");
    let fallback_bin = fallback_home.join(".binpm").join("bin");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("BINPM_HOME", "")
        .env("HOME", &fallback_home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(bash_quote_path(&fallback_bin)))
        .stdout(predicate::str::contains("''").not());
}

#[test]
fn env_rejects_relative_binpm_home() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("BINPM_HOME", "tmp/binpm-home")
        .args(["env", "--shell", "bash"])
        .assert()
        .failure()
        .code(1)
        .stderr(predicate::str::contains("Invalid BINPM_HOME"))
        .stderr(predicate::str::contains("absolute path"));
}

#[test]
fn env_rejects_relative_home_fallback() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("HOME", "relative-home")
        .args(["env", "--shell", "bash"])
        .assert()
        .failure()
        .code(1)
        .stderr(predicate::str::contains("Invalid HOME"))
        .stderr(predicate::str::contains("absolute path"));
}

#[test]
fn env_fails_when_global_home_cannot_be_resolved() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .args(["env", "--shell", "bash"])
        .assert()
        .failure()
        .code(1)
        .stderr(predicate::str::contains(
            "Failed to determine binpm global home",
        ));
}

#[test]
fn env_routes_enabled_logs_to_stderr() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_LOG", "binpm=info")
        .env("BINPM_LOG_COLOR", "never")
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains("Rendered PATH environment commands").not())
        .stderr(predicate::str::contains(
            "Rendered PATH environment commands",
        ));
}

#[test]
fn env_from_nested_directory_uses_git_root_local_bin() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let canonical_root = fs::canonicalize(temp_dir.path()).expect("canonical temp dir");
    let canonical_nested = fs::canonicalize(&nested_dir).expect("canonical nested dir");
    let root_bin = canonical_root.join(".binpm").join("bin");
    let nested_bin = canonical_nested.join(".binpm").join("bin");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(bash_path(&root_bin)))
        .stdout(predicate::str::contains(bash_path(&nested_bin)).not());
}

#[test]
fn env_from_nested_directory_uses_manifest_ancestor_without_git() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let canonical_root = fs::canonicalize(temp_dir.path()).expect("canonical temp dir");
    let canonical_nested = fs::canonicalize(&nested_dir).expect("canonical nested dir");
    let root_bin = canonical_root.join(".binpm").join("bin");
    let nested_bin = canonical_nested.join(".binpm").join("bin");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(bash_path(&root_bin)))
        .stdout(predicate::str::contains(bash_path(&nested_bin)).not());
}

#[test]
fn cache_key_from_nested_directory_uses_git_root_lockfile() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let expected_digest = format!("{:x}", Sha256::digest(b"root lock\n"));
    let empty_digest = format!("{:x}", Sha256::digest([]));
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .args(["cache", "key"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected_digest))
        .stdout(predicate::str::contains(empty_digest).not());
}

#[test]
fn cache_key_routes_enabled_logs_to_stderr() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let expected_digest = format!("{:x}", Sha256::digest(b"root lock\n"));
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_LOG", "binpm=info")
        .env("BINPM_LOG_COLOR", "never")
        .args(["cache", "key"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected_digest))
        .stdout(predicate::str::contains("Computed binpm cache key").not())
        .stderr(predicate::str::contains("Computed binpm cache key"));
}

#[test]
fn cache_key_from_nested_directory_uses_manifest_ancestor_lockfile_without_git() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let expected_digest = format!("{:x}", Sha256::digest(b"root lock\n"));
    let empty_digest = format!("{:x}", Sha256::digest([]));
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .args(["cache", "key"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected_digest))
        .stdout(predicate::str::contains(empty_digest).not());
}

#[test]
fn cache_key_warns_when_lockfile_is_missing_without_mutating_state() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let empty_digest = format!("{:x}", Sha256::digest([]));
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .args(["cache", "key"])
        .assert()
        .success()
        .stdout(predicate::str::contains(empty_digest))
        .stderr(predicate::str::contains(
            "cache key uses the empty lockfile digest",
        ));

    assert!(!temp_dir.path().join("binpm.lock").exists());
}

#[test]
fn cache_key_json_reports_lockfile_status() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let output = binpm()
        .current_dir(temp_dir.path())
        .args(["cache", "key", "--json"])
        .output()
        .expect("cache key --json");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let payload: Value = serde_json::from_slice(&output.stdout).expect("parse cache key json");
    assert_eq!(payload["command"], "cache key");
    assert_eq!(payload["lockfile"], "missing");
    assert_eq!(payload["read_only"], true);
    assert!(payload["cache_key"]
        .as_str()
        .expect("cache key string")
        .starts_with("binpm-v1-"));
}

#[test]
fn cache_clean_output_states_removed_and_preserved_boundaries() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let entry = home.join("cache").join("sha256").join("abc");
    fs::create_dir_all(&entry).expect("create cache entry");
    fs::write(entry.join("asset"), b"bytes").expect("write cache asset");

    binpm()
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["cache", "clean"])
        .assert()
        .success()
        .stdout(predicate::str::contains("removed cache entries: 1"))
        .stdout(predicate::str::contains("preserved:"))
        .stdout(predicate::str::contains("/cache/refs"))
        .stdout(predicate::str::contains("/packages"))
        .stdout(predicate::str::contains("/bin"));
}

#[test]
fn doctor_from_nested_directory_reports_git_root_state() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .env("BINPM_HOME", &home)
        .arg("doctor")
        .assert()
        .success()
        .stdout(predicate::str::contains("manifest: present"))
        .stdout(predicate::str::contains("lockfile: present"));
}

#[test]
fn doctor_json_reports_path_states() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    let output = binpm()
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["doctor", "--json"])
        .output()
        .expect("doctor --json");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).expect("parse doctor json");
    assert_eq!(payload["command"], "doctor");
    assert_eq!(payload["manifest"], "present");
    assert_eq!(payload["lockfile"], "missing");
    assert_eq!(payload["global_home"], home.display().to_string());
    assert_eq!(
        payload["global_bin"],
        home.join("bin").display().to_string()
    );
    assert_eq!(
        payload["local_bin"],
        temp_dir
            .path()
            .join(".binpm")
            .join("bin")
            .display()
            .to_string()
    );
    assert_eq!(payload["local_bin_on_path"], false);
    assert_eq!(payload["global_bin_on_path"], false);
}

#[test]
fn list_json_reports_declared_local_tools_with_stable_fields() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    fs::write(
        temp_dir.path().join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
version = "1.0.0"
"#,
    )
    .expect("write manifest");
    let output = binpm()
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["list", "--local", "--json"])
        .output()
        .expect("list --json");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    assert!(!String::from_utf8_lossy(&output.stdout).contains("\u{1b}["));
    let payload: Value = serde_json::from_slice(&output.stdout).expect("parse list json");
    assert_eq!(payload["command"], "list");
    assert_eq!(payload["scope"], "local");
    assert_eq!(payload["tools"][0]["cmd"], "tool");
    assert_eq!(payload["tools"][0]["state"], "declared");
    assert_eq!(payload["tools"][0]["source"], "github:owner/tool");
    assert_eq!(payload["tools"][0]["requested_version"], "1.0.0");
    assert!(payload["tools"][0]["release_tag"].is_null());
}

#[test]
fn cache_list_json_is_parseable_without_entries() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let output = binpm()
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["cache", "list", "--json"])
        .output()
        .expect("cache list --json");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).expect("parse cache list json");
    assert_eq!(payload["command"], "cache list");
    assert_eq!(
        payload["entries"].as_array().expect("entries array").len(),
        0
    );
}

#[test]
fn explain_command_json_reuses_contract_enum_values() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(project.join(".binpm").join("packages")).expect("create packages");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join(".binpm").join("packages").join("tool.toml"),
        format!(
            r#"package_spec = "github:owner/tool@1.0.0"
source = "github:owner/tool"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool"
requested_version = "1.0.0"
release_tag = "1.0.0"
asset_name = "tool-linux-x64"
asset_url = "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
target_os = "linux"
target_arch = "x86_64"
target_libc = "gnu"
archive_format = "bare-executable"
selected_binary = "tool-linux-x64"
installed_path = "{}"
cache_key = "sha256-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
cache_path = "{}"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
            project.join(".binpm").join("bin").join("tool").display(),
            home.join("cache")
                .join("sha256-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
                .join("asset")
                .display()
        ),
    )
    .expect("write package record");
    let output = binpm()
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["explain", "tool", "--local", "--json"])
        .output()
        .expect("explain --json");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).expect("parse explain json");
    assert_eq!(payload["kind"], "package");
    assert_eq!(payload["command"], "explain");
    assert_eq!(payload["scope"], "local");
    assert_eq!(payload["record"]["target"]["os"], "linux");
    assert_eq!(payload["record"]["target"]["arch"], "x86_64");
    assert_eq!(payload["record"]["target"]["libc"], "gnu");
    assert_eq!(payload["record"]["archive_format"], "bare-executable");
    assert_eq!(payload["record"]["checksum_source"], "local");
    assert_eq!(payload["record"]["verification"], "unverified");
    let override_snippet = payload["override_snippet"]
        .as_str()
        .expect("override snippet");
    assert_eq!(
        override_snippet,
        "[tools.tool.targets.linux-x86_64-gnu]\nasset = \"tool-linux-x64\"\nbin = \
         \"tool-linux-x64\""
    );
}

#[test]
fn verbose_verify_json_failure_emits_parseable_error_envelope() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let output = binpm()
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .env("BINPM_LOG", "binpm=info")
        .args(["--verbose", "verify", "--local", "--json"])
        .output()
        .expect("verify --json");

    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    let payload: Value = serde_json::from_slice(&output.stderr).expect("parse error json");
    assert_eq!(payload["error"]["exit_code"], 2);
    assert!(payload["error"]["message"]
        .as_str()
        .expect("error message")
        .contains("No local binpm.toml manifest found"));
}

#[test]
fn verify_local_json_suppresses_lockfile_progress_before_error_envelope() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(&project).expect("create project");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"

[tools.tool.targets.linux-x86_64-gnu]
asset = "tool-linux-x64"
bin = "tool-linux-x64"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join("binpm.lock"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"

[tools.tool.targets.linux-x86_64-gnu]
package_spec = "github:owner/tool@1.0.0"
source = "github:owner/tool"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool"
requested_version = "1.0.0"
release_tag = "1.0.0"
asset_name = "tool-linux-x64"
asset_url = "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
target_os = "linux"
target_arch = "x86_64"
target_libc = "gnu"
archive_format = "bare-executable"
selected_binary = "tool-linux-x64"
installed_path = ".binpm/bin/tool"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
    )
    .expect("write lockfile");
    let output = binpm()
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["verify", "--local", "--json"])
        .output()
        .expect("verify --json");

    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    let payload: Value = serde_json::from_slice(&output.stderr).expect("parse error json");
    assert_eq!(payload["error"]["exit_code"], 2);
}

#[test]
fn parse_error_with_json_flag_emits_parseable_error_envelope() {
    let output = binpm()
        .args(["explain", "--json"])
        .output()
        .expect("explain --json parse error");

    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    let payload: Value = serde_json::from_slice(&output.stderr).expect("parse error json");
    assert_eq!(payload["error"]["exit_code"], 2);
    assert!(payload["error"]["message"]
        .as_str()
        .expect("error message")
        .contains("required"));
}

#[test]
fn doctor_guides_path_setup_when_global_bin_is_absent_from_path() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("BINPM_HOME", &home)
        .arg("doctor")
        .assert()
        .success()
        .stdout(predicate::str::contains("global_bin_on_path: no"))
        .stdout(predicate::str::contains("binpm env --shell"))
        .stdout(predicate::str::contains("profile changes are opt-in"))
        .stdout(predicate::str::contains("persist only the global bin line"))
        .stdout(predicate::str::contains(
            "project-local PATH line is for the current project/session only",
        ));
}

#[test]
fn doctor_omits_path_setup_guidance_when_global_bin_is_on_path() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let global_bin = home.join("bin");
    fs::create_dir_all(&global_bin).expect("create global bin");
    let path = std::env::join_paths([global_bin.as_path()]).expect("join PATH");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("BINPM_HOME", &home)
        .env("PATH", path)
        .arg("doctor")
        .assert()
        .success()
        .stdout(predicate::str::contains("global_bin_on_path: yes"))
        .stdout(predicate::str::contains("path_setup:").not());
}

#[test]
fn env_escapes_bash_paths_before_printing_shell_code() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm $(touch x) `cmd` 'home'");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains("'\\''home'\\''/bin'"))
        .stdout(predicate::str::contains("${PATH:+:$PATH}"));
}

#[test]
fn env_fish_preserves_paths_before_directories_exist() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let local_bin = fs::canonicalize(temp_dir.path())
        .expect("canonical temp dir")
        .join(".binpm")
        .join("bin");
    let global_bin = home.join("bin");
    let expected_global = format!("set -gx PATH '{}' $PATH", global_bin.display());
    let expected_local = format!("set -gx PATH '{}' $PATH", local_bin.display());
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "fish"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected_global))
        .stdout(predicate::str::contains(expected_local))
        .stdout(predicate::str::contains("fish_add_path").not());
}

#[test]
fn env_powershell_uses_runtime_path_separator() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "powershell"])
        .assert()
        .success()
        .stdout(predicate::str::contains("[System.IO.Path]::PathSeparator"))
        .stdout(predicate::str::contains(" + ';' + ").not());
}

#[test]
fn env_powershell_avoids_trailing_separator_when_path_is_unset() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env_clear()
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "powershell"])
        .assert()
        .success()
        .stdout(predicate::str::contains("if ($env:PATH)"))
        .stdout(predicate::str::contains("else { '' }"))
        .stdout(predicate::str::contains(
            "[System.IO.Path]::PathSeparator + $env:PATH",
        ));
}

#[test]
fn env_cmd_reports_explicitly_deferred_shell() {
    let mut command = binpm();

    command
        .args(["env", "--shell", "cmd"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("Unsupported shell `cmd`"))
        .stderr(predicate::str::contains(
            "Supported shells: bash, zsh, fish, powershell",
        ))
        .stderr(predicate::str::contains("Deferred shell: cmd"));
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

#[test]
fn install_rejects_empty_source_version() {
    let mut command = binpm();

    command
        .args(["install", "github:owner/repo@"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("source version cannot be empty"));
}

#[test]
fn frozen_local_update_allows_empty_manifest_without_lockfile() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(&project).expect("create project");
    fs::write(project.join("binpm.toml"), "version = 1\n").expect("write manifest");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env_clear()
        .env("BINPM_HOME", &home)
        .args(["update", "--local", "--frozen-lockfile"])
        .assert()
        .success();

    assert!(!project.join("binpm.lock").exists());
}

#[test]
fn ci_local_update_allows_empty_manifest_without_lockfile() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(&project).expect("create project");
    fs::write(project.join("binpm.toml"), "version = 1\n").expect("write manifest");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env_clear()
        .env("BINPM_HOME", &home)
        .env("CI", "true")
        .args(["update", "--local"])
        .assert()
        .success();

    assert!(!project.join("binpm.lock").exists());
}

#[test]
fn frozen_local_update_rejects_declared_tool_without_lockfile() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(&project).expect("create project");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
version = "1.0.0"
"#,
    )
    .expect("write manifest");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env_clear()
        .env("BINPM_HOME", &home)
        .args(["update", "--local", "--frozen-lockfile"])
        .assert()
        .failure()
        .stderr(predicate::str::contains("Frozen lockfile"));

    assert!(!project.join("binpm.lock").exists());
}

#[cfg(all(target_os = "linux", target_arch = "x86_64", target_env = "gnu"))]
#[test]
fn frozen_local_install_restores_missing_runtime_from_verified_cache() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    let tool_bytes = b"#!/bin/sh\nprintf 'restored install\\n'\n";
    let sha256 = format!("{:x}", Sha256::digest(tool_bytes));
    write_locked_tool_project(&project, &sha256);
    write_cache_asset(&home, &sha256, tool_bytes);
    let lock_before = fs::read_to_string(project.join("binpm.lock")).expect("read lockfile");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env_clear()
        .env("BINPM_HOME", &home)
        .args([
            "install",
            "--local",
            "--frozen-lockfile",
            "--require-verified",
        ])
        .assert()
        .success()
        .stdout(predicate::str::contains("installed tool"));

    assert!(project.join(".binpm").join("bin").join("tool").exists());
    assert!(project
        .join(".binpm")
        .join("packages")
        .join("tool.toml")
        .exists());
    assert_eq!(
        fs::read_to_string(project.join("binpm.lock")).expect("read lockfile"),
        lock_before
    );
}

#[cfg(all(target_os = "linux", target_arch = "x86_64", target_env = "gnu"))]
#[test]
fn frozen_x_restores_missing_runtime_from_verified_cache() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    let tool_bytes = b"#!/bin/sh\nprintf 'tool args:%s\\n' \"$1\"\n";
    let sha256 = format!("{:x}", Sha256::digest(tool_bytes));
    write_locked_tool_project(&project, &sha256);
    write_cache_asset(&home, &sha256, tool_bytes);
    let lock_before = fs::read_to_string(project.join("binpm.lock")).expect("read lockfile");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env_clear()
        .env("BINPM_HOME", &home)
        .args(["x", "--frozen-lockfile", "tool", "--probe"])
        .assert()
        .success()
        .stdout(predicate::str::contains("tool args:--probe"));

    assert!(project.join(".binpm").join("bin").join("tool").exists());
    assert!(project
        .join(".binpm")
        .join("packages")
        .join("tool.toml")
        .exists());
    assert_eq!(
        fs::read_to_string(project.join("binpm.lock")).expect("read lockfile"),
        lock_before
    );
}

#[test]
fn explain_rejects_latest_selector_with_omitted_version_hint() {
    let mut command = binpm();

    command
        .args(["explain", "github:owner/repo@latest"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("`@latest` is not supported"))
        .stderr(predicate::str::contains("omit `@version`"));
}

#[test]
fn explain_rejects_semver_range_selector_with_exact_tag_hint() {
    let mut command = binpm();

    command
        .args(["explain", "github:owner/repo@^1"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains("semver ranges are not supported"))
        .stderr(predicate::str::contains("use an exact release tag"));
}

#[test]
fn local_remove_rejects_corrupt_package_record_with_unsafe_installed_path() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    let unsafe_installed_path = temp_dir.path().join("outside-binpm-tool");
    fs::create_dir_all(project.join(".binpm").join("packages")).expect("create packages");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join("binpm.lock"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write lockfile");
    let package_record = format!(
        r#"package_spec = "github:owner/tool@1.0.0"
source = "github:owner/tool"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool"
requested_version = "1.0.0"
release_tag = "1.0.0"
asset_name = "tool-linux-x64"
asset_url = "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
target_os = "linux"
target_arch = "x86_64"
target_libc = "gnu"
archive_format = "bare-executable"
selected_binary = "tool-linux-x64"
installed_path = "{}"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
        unsafe_installed_path.display()
    );
    fs::write(
        project.join(".binpm").join("packages").join("tool.toml"),
        package_record,
    )
    .expect("write package record");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["remove", "--local", "tool"])
        .assert()
        .failure()
        .stderr(predicate::str::contains("Unsafe installed path"));

    assert!(project
        .join(".binpm")
        .join("packages")
        .join("tool.toml")
        .exists());
    let manifest = fs::read_to_string(project.join("binpm.toml")).expect("read manifest");
    let lockfile = fs::read_to_string(project.join("binpm.lock")).expect("read lockfile");
    assert!(manifest.contains("tools.tool"));
    assert!(lockfile.contains("tools.tool"));
    assert!(!project.join(".binpm").join("bin").join("tool").exists());
}

#[test]
fn local_remove_without_package_record_preserves_unowned_binary() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(project.join(".binpm").join("bin")).expect("create bin");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join("binpm.lock"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write lockfile");
    fs::write(project.join(".binpm").join("bin").join("tool"), "manual")
        .expect("write manual executable");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["remove", "--local", "tool"])
        .assert()
        .success()
        .stdout(predicate::str::contains("removed tool"));

    assert_eq!(
        fs::read_to_string(project.join(".binpm").join("bin").join("tool"))
            .expect("read manual executable"),
        "manual"
    );
    let manifest = fs::read_to_string(project.join("binpm.toml")).expect("read manifest");
    let lockfile = fs::read_to_string(project.join("binpm.lock")).expect("read lockfile");
    assert!(!manifest.contains("tools.tool"));
    assert!(!lockfile.contains("tools.tool"));
}

#[test]
fn local_remove_dry_run_reports_scope_and_preserves_state() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(project.join(".binpm").join("bin")).expect("create bin");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join("binpm.lock"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"
"#,
    )
    .expect("write lockfile");
    fs::write(project.join(".binpm").join("bin").join("tool"), "manual")
        .expect("write manual executable");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["remove", "--local", "--dry-run", "tool"])
        .assert()
        .success()
        .stdout(predicate::str::contains("remove scope: local"))
        .stdout(predicate::str::contains(
            "would remove tool from local scope",
        ))
        .stdout(predicate::str::contains("dry run: no changes made"));

    assert_eq!(
        fs::read_to_string(project.join(".binpm").join("bin").join("tool"))
            .expect("read manual executable"),
        "manual"
    );
    let manifest = fs::read_to_string(project.join("binpm.toml")).expect("read manifest");
    let lockfile = fs::read_to_string(project.join("binpm.lock")).expect("read lockfile");
    assert!(manifest.contains("tools.tool"));
    assert!(lockfile.contains("tools.tool"));
}

#[test]
fn local_remove_missing_tool_does_not_create_lockfile() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(&project).expect("create project");
    fs::write(project.join("binpm.toml"), "version = 1\n").expect("write manifest");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["remove", "--local", "missing"])
        .assert()
        .failure()
        .stderr(predicate::str::contains("Tool `missing` is not declared"));

    assert!(!project.join("binpm.lock").exists());
}

#[test]
fn local_update_dry_run_reports_scope_and_planned_tools_without_mutation() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(&project).expect("create project");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.alpha]
source = "github:owner/alpha"

[tools.beta]
source = "github:owner/beta"
version = "1.0.0"
"#,
    )
    .expect("write manifest");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["update", "--local", "--dry-run"])
        .assert()
        .success()
        .stdout(predicate::str::contains("update scope: local"))
        .stdout(predicate::str::contains("planned updates: 2"))
        .stdout(predicate::str::contains(
            "would update alpha from github:owner/alpha <latest>",
        ))
        .stdout(predicate::str::contains(
            "would update beta from github:owner/beta 1.0.0",
        ))
        .stdout(predicate::str::contains("dry run: no changes made"));

    assert!(!project.join("binpm.lock").exists());
    assert!(!project.join(".binpm").exists());
}

#[test]
fn local_remove_preserves_exe_sibling_tool() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    let home = temp_dir.path().join("binpm-home");
    let project = temp_dir.path().join("project");
    fs::create_dir_all(project.join(".binpm").join("bin")).expect("create bin");
    fs::create_dir_all(project.join(".binpm").join("packages")).expect("create packages");
    fs::write(
        project.join("binpm.toml"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"

[tools."tool.exe"]
source = "github:owner/tool-exe"
"#,
    )
    .expect("write manifest");
    fs::write(
        project.join("binpm.lock"),
        r#"version = 1

[tools.tool]
source = "github:owner/tool"

[tools."tool.exe"]
source = "github:owner/tool-exe"
"#,
    )
    .expect("write lockfile");
    fs::write(project.join(".binpm").join("bin").join("tool"), "tool").expect("write tool");
    let tool_path = project.join(".binpm").join("bin").join("tool");
    let tool_exe_path = project.join(".binpm").join("bin").join("tool.exe");
    let canonical_tool_path = tool_path.canonicalize().expect("canonical tool path");
    fs::write(&tool_exe_path, "tool exe").expect("write tool.exe");
    let canonical_tool_exe_path = tool_exe_path
        .canonicalize()
        .expect("canonical tool.exe path");
    fs::write(
        project.join(".binpm").join("packages").join("tool.toml"),
        format!(
            r#"package_spec = "github:owner/tool@1.0.0"
source = "github:owner/tool"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool"
requested_version = "1.0.0"
release_tag = "1.0.0"
asset_name = "tool-linux-x64"
asset_url = "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
target_os = "linux"
target_arch = "x86_64"
target_libc = "gnu"
archive_format = "bare-executable"
selected_binary = "tool-linux-x64"
installed_path = "{}"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
            canonical_tool_path.display()
        ),
    )
    .expect("write tool package record");
    fs::write(
        project
            .join(".binpm")
            .join("packages")
            .join("tool.exe.toml"),
        format!(
            r#"package_spec = "github:owner/tool-exe@1.0.0"
source = "github:owner/tool-exe"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool-exe"
requested_version = "1.0.0"
release_tag = "1.0.0"
asset_name = "tool.exe"
asset_url = "https://github.com/owner/tool-exe/releases/download/1.0.0/tool.exe"
target_os = "linux"
target_arch = "x86_64"
target_libc = "gnu"
archive_format = "bare-executable"
selected_binary = "tool.exe"
installed_path = "{}"
sha256 = "abcdefabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
            canonical_tool_exe_path.display()
        ),
    )
    .expect("write tool.exe package record");
    let mut command = binpm();

    command
        .current_dir(&project)
        .env("BINPM_HOME", &home)
        .args(["remove", "--local", "tool"])
        .assert()
        .success()
        .stdout(predicate::str::contains("removed tool"));

    assert!(!project.join(".binpm").join("bin").join("tool").exists());
    assert_eq!(
        fs::read_to_string(project.join(".binpm").join("bin").join("tool.exe"))
            .expect("read sibling executable"),
        "tool exe"
    );
    assert!(!project
        .join(".binpm")
        .join("packages")
        .join("tool.toml")
        .exists());
    assert!(project
        .join(".binpm")
        .join("packages")
        .join("tool.exe.toml")
        .exists());
}
