use std::{fs, path::Path};

use assert_cmd::Command;
use predicates::prelude::*;
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
fn init_from_nested_directory_writes_manifest_at_git_root() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("init")
        .assert()
        .success()
        .stdout(predicate::str::contains("created"));

    let manifest = fs::read_to_string(temp_dir.path().join("binpm.toml")).expect("read manifest");
    assert_eq!(manifest, "version = 1\n");
    assert!(!nested_dir.join("binpm.toml").exists());
}

#[test]
fn init_from_nested_directory_detects_existing_manifest_without_git() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("init")
        .assert()
        .failure()
        .code(2)
        .stderr(predicate::str::contains(
            "Refusing to overwrite existing manifest",
        ));

    assert!(!nested_dir.join("binpm.toml").exists());
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
    let expected = format!(
        "export PATH={}:{}${{PATH:+:$PATH}}",
        bash_quote_path(&local_bin),
        bash_quote_path(&global_bin)
    );
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "bash"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected));
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
fn doctor_from_nested_directory_reports_git_root_state() {
    let temp_dir = tempfile::tempdir().expect("tempdir");
    fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
    fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n").expect("write manifest");
    fs::write(temp_dir.path().join("binpm.lock"), "root lock\n").expect("write lockfile");
    let nested_dir = temp_dir.path().join("packages").join("cli");
    fs::create_dir_all(&nested_dir).expect("create nested dir");
    let mut command = binpm();

    command
        .current_dir(&nested_dir)
        .arg("doctor")
        .assert()
        .success()
        .stdout(predicate::str::contains("manifest: present"))
        .stdout(predicate::str::contains("lockfile: present"));
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
    let expected = format!(
        "set -gx PATH '{}' '{}' $PATH",
        local_bin.display(),
        global_bin.display()
    );
    let mut command = binpm();

    command
        .current_dir(temp_dir.path())
        .env("BINPM_HOME", &home)
        .args(["env", "--shell", "fish"])
        .assert()
        .success()
        .stdout(predicate::str::contains(expected))
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
fn local_remove_cleans_corrupt_package_record_with_unsafe_installed_path() {
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
        .success()
        .stdout(predicate::str::contains("removed tool"));

    assert!(!project
        .join(".binpm")
        .join("packages")
        .join("tool.toml")
        .exists());
    let manifest = fs::read_to_string(project.join("binpm.toml")).expect("read manifest");
    let lockfile = fs::read_to_string(project.join("binpm.lock")).expect("read lockfile");
    assert!(!manifest.contains("tools.tool"));
    assert!(!lockfile.contains("tools.tool"));
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
    fs::write(
        project.join(".binpm").join("bin").join("tool.exe"),
        "tool exe",
    )
    .expect("write tool.exe");
    fs::write(
        project.join(".binpm").join("packages").join("tool.toml"),
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
installed_path = ".binpm/bin/tool"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
    )
    .expect("write tool package record");
    fs::write(
        project
            .join(".binpm")
            .join("packages")
            .join("tool.exe.toml"),
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
installed_path = ".binpm/bin/tool.exe"
sha256 = "abcdefabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123"
checksum_source = "local"
signature_available = false
signature_verified = false
"#,
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
