#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;
use std::{
    collections::HashMap,
    fs,
    io::Write,
    path::{Path, PathBuf},
};

use assert_cmd::{assert::OutputAssertExt, Command};
use httpmock::{Method::GET, MockServer};
use predicates::prelude::PredicateBooleanExt;
use serde_json::Value;
use serial_test::serial;
use sha2::{Digest, Sha256};
use xz2::write::XzEncoder;
use zip::{write::FileOptions, ZipWriter};

struct TestEnv {
    root: PathBuf,
    data_root: PathBuf,
    cache_root: PathBuf,
    config_root: PathBuf,
    index_url: String,
    download_base_url: String,
    server: MockServer,
}

fn write_runtime_executable(path: impl AsRef<Path>, content: &str) {
    let path = path.as_ref();
    fs::write(path, content).unwrap();
    set_executable(path);
}

fn set_executable(path: &Path) {
    #[cfg(unix)]
    {
        let mut permissions = fs::metadata(path).unwrap().permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(path, permissions).unwrap();
    }

    #[cfg(not(unix))]
    {
        let _ = path;
    }
}

impl TestEnv {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap().keep();
        let nodeup_root = root.join("nodeup");
        let data_root = nodeup_root.join("data");
        let cache_root = nodeup_root.join("cache");
        let config_root = nodeup_root.join("config");
        fs::create_dir_all(&data_root).unwrap();
        fs::create_dir_all(&cache_root).unwrap();
        fs::create_dir_all(&config_root).unwrap();

        let server = MockServer::start();
        let index_url = format!("{}/download/release/index.json", server.base_url());
        let download_base_url = format!("{}/download/release", server.base_url());

        Self {
            root,
            data_root,
            cache_root,
            config_root,
            index_url,
            download_base_url,
            server,
        }
    }

    fn command(&self) -> Command {
        let mut command = Command::new(assert_cmd::cargo::cargo_bin!("nodeup"));
        self.apply_env(&mut command);
        command
    }

    fn command_with_info_logs(&self) -> Command {
        let mut command = self.command();
        command.env("RUST_LOG", "nodeup=info");
        command
    }

    fn command_with_program(&self, program: &Path) -> std::process::Command {
        let mut command = std::process::Command::new(program);
        self.apply_env_std(&mut command);
        command
    }

    fn apply_env(&self, command: &mut Command) {
        command.env("NODEUP_DATA_HOME", &self.data_root);
        command.env("NODEUP_CACHE_HOME", &self.cache_root);
        command.env("NODEUP_CONFIG_HOME", &self.config_root);
        command.env("NODEUP_INDEX_URL", &self.index_url);
        command.env("NODEUP_DOWNLOAD_BASE_URL", &self.download_base_url);
        command.env("NODEUP_FORCE_PLATFORM", "linux-x64");
        command.env("NODEUP_LOG_COLOR", "never");
        command.env("RUST_LOG", "off");
    }

    fn apply_env_std(&self, command: &mut std::process::Command) {
        command.env("NODEUP_DATA_HOME", &self.data_root);
        command.env("NODEUP_CACHE_HOME", &self.cache_root);
        command.env("NODEUP_CONFIG_HOME", &self.config_root);
        command.env("NODEUP_INDEX_URL", &self.index_url);
        command.env("NODEUP_DOWNLOAD_BASE_URL", &self.download_base_url);
        command.env("NODEUP_FORCE_PLATFORM", "linux-x64");
        command.env("NODEUP_LOG_COLOR", "never");
        command.env("RUST_LOG", "off");
    }

    fn register_release(
        &self,
        version: &str,
        archive_bytes: Vec<u8>,
        shasums_override: Option<HashMap<String, String>>,
    ) {
        self.register_release_for_target(version, "linux-x64", archive_bytes, shasums_override);
    }

    fn register_release_for_target(
        &self,
        version: &str,
        target: &str,
        archive_bytes: Vec<u8>,
        shasums_override: Option<HashMap<String, String>>,
    ) {
        let version = normalize(version);
        let extension = if target.starts_with("win-") {
            "zip"
        } else {
            "tar.xz"
        };
        let archive_name = format!("node-{version}-{target}.{extension}");

        let digest = Sha256::digest(&archive_bytes);
        let mut table = HashMap::new();
        table.insert(archive_name.clone(), format!("{digest:x}"));

        if let Some(override_values) = shasums_override {
            for (key, value) in override_values {
                table.insert(key, value);
            }
        }

        let mut shasums_lines = Vec::new();
        for (name, checksum) in table {
            shasums_lines.push(format!("{checksum}  {name}"));
        }

        self.server.mock(|when, then| {
            when.method(GET)
                .path(format!("/download/release/{version}/{archive_name}"));
            then.status(200)
                .header("content-type", "application/octet-stream")
                .body(archive_bytes);
        });

        self.server.mock(|when, then| {
            when.method(GET)
                .path(format!("/download/release/{version}/SHASUMS256.txt"));
            then.status(200)
                .header("content-type", "text/plain")
                .body(shasums_lines.join("\n"));
        });
    }

    fn register_index(&self, versions: &[(&str, Option<&str>)]) {
        let payload = versions
            .iter()
            .map(|(version, lts)| {
                serde_json::json!({
                    "version": normalize(version),
                    "lts": lts.map_or(serde_json::Value::Bool(false), |value| serde_json::Value::String(value.to_string()))
                })
            })
            .collect::<Vec<_>>();

        self.server.mock(|when, then| {
            when.method(GET).path("/download/release/index.json");
            then.status(200)
                .header("content-type", "application/json")
                .json_body_obj(&payload);
        });
    }
}

fn normalize(version: &str) -> String {
    if version.starts_with('v') {
        version.to_string()
    } else {
        format!("v{version}")
    }
}

fn tracked_selectors_from_settings(settings_file: &Path) -> Vec<String> {
    let content = fs::read_to_string(settings_file).unwrap();
    let value = content.parse::<toml::Value>().unwrap();
    value["tracked_selectors"]
        .as_array()
        .unwrap()
        .iter()
        .map(|selector| selector.as_str().unwrap().to_string())
        .collect()
}

fn make_archive(version: &str, target: &str, scripts: &[(&str, &str)]) -> Vec<u8> {
    let version = normalize(version);
    let root_name = format!("node-{version}-{target}");

    let mut tar_payload = Vec::new();
    {
        let mut builder = tar::Builder::new(&mut tar_payload);

        for (script_name, script_body) in scripts {
            let path = format!("{root_name}/bin/{script_name}");
            let mut header = tar::Header::new_gnu();
            header.set_mode(0o755);
            header.set_size(script_body.len() as u64);
            header.set_cksum();
            builder
                .append_data(&mut header, path, script_body.as_bytes())
                .unwrap();
        }

        builder.finish().unwrap();
    }

    let mut encoder = XzEncoder::new(Vec::new(), 6);
    encoder.write_all(&tar_payload).unwrap();
    encoder.finish().unwrap()
}

fn make_windows_zip(files: &[(&str, &str)]) -> Vec<u8> {
    let mut cursor = std::io::Cursor::new(Vec::new());
    {
        let mut writer = ZipWriter::new(&mut cursor);
        let options = FileOptions::default().unix_permissions(0o755);

        for (file_name, file_body) in files {
            writer.start_file(*file_name, options).unwrap();
            writer.write_all(file_body.as_bytes()).unwrap();
        }

        writer.finish().unwrap();
    }

    cursor.into_inner()
}

fn make_npm_argv_script(prefix: &str) -> String {
    format!("#!/bin/sh\necho {prefix}:$*\n")
}

fn assert_json_parser_error(output: std::process::Output, expected_message: &str) {
    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!stderr.contains("\u{1b}["));

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains(expected_message));
}

#[test]
#[serial]
fn help_lists_top_level_subcommand_descriptions() {
    let env = TestEnv::new();

    env.command()
        .arg("--help")
        .assert()
        .success()
        .stdout(predicates::str::contains("Manage installed runtimes"))
        .stdout(predicates::str::contains(
            "Set or show the global default runtime",
        ))
        .stdout(predicates::str::contains(
            "Show runtime resolution details and nodeup directories",
        ))
        .stdout(predicates::str::contains(
            "Update selected runtimes or tracked selectors",
        ))
        .stdout(predicates::str::contains(
            "Manage directory-scoped runtime overrides",
        ))
        .stdout(predicates::str::contains(
            "Manage executable-name dispatch shims",
        ))
        .stdout(predicates::str::contains(
            "Generate shell completion scripts",
        ));
}

#[test]
#[serial]
fn help_lists_nested_subcommand_descriptions() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "--help"])
        .assert()
        .success()
        .stdout(predicates::str::contains("List installed runtimes"))
        .stdout(predicates::str::contains("Install one or more runtimes"))
        .stdout(predicates::str::contains("Uninstall one or more runtimes"))
        .stdout(predicates::str::contains(
            "Link an existing local runtime directory",
        ));

    env.command()
        .args(["override", "--help"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "List configured directory overrides",
        ))
        .stdout(predicates::str::contains(
            "Set a runtime override for a directory",
        ))
        .stdout(predicates::str::contains(
            "Remove a runtime override for a directory",
        ));

    env.command()
        .args(["shim", "--help"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Create or repair managed shims"));
}

#[test]
#[serial]
fn install_and_uninstall_help_show_required_runtime_arguments() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "install", "--help"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "Usage: nodeup toolchain install [OPTIONS] <RUNTIMES>...",
        ))
        .stdout(predicates::str::contains(
            "<RUNTIMES>...  Runtime selectors to install",
        ));

    env.command()
        .args(["toolchain", "uninstall", "--help"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "Usage: nodeup toolchain uninstall [OPTIONS] <RUNTIMES>...",
        ))
        .stdout(predicates::str::contains(
            "<RUNTIMES>...  Installed runtime selectors to remove",
        ));
}

#[test]
#[serial]
fn human_parser_errors_keep_clap_formatting() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "list", "--quiet", "--verbose"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "error: the argument '--quiet' cannot be used with '--verbose'",
        ))
        .stderr(predicates::str::contains(
            "Usage: nodeup toolchain list --quiet",
        ))
        .stderr(predicates::str::contains("nodeup error:").not());
}

#[test]
#[serial]
fn json_parser_errors_emit_error_envelopes() {
    let env = TestEnv::new();

    let root_missing = env
        .command()
        .args(["--output", "json"])
        .output()
        .expect("nodeup --output json without subcommand");
    assert_json_parser_error(root_missing, "requires a subcommand");

    let conflicting_flags = env
        .command()
        .args([
            "--output",
            "json",
            "toolchain",
            "list",
            "--quiet",
            "--verbose",
        ])
        .output()
        .expect("nodeup --output json toolchain list conflict");
    assert_json_parser_error(conflicting_flags, "cannot be used with '--verbose'");

    let missing_nested_arg = env
        .command()
        .args(["--output", "json", "toolchain", "link", "local-node"])
        .output()
        .expect("nodeup --output json toolchain link missing path");
    assert_json_parser_error(missing_nested_arg, "required arguments were not provided");

    let missing_install_runtime = env
        .command()
        .args(["--output", "json", "toolchain", "install"])
        .output()
        .expect("nodeup --output json toolchain install missing runtime");
    assert_json_parser_error(
        missing_install_runtime,
        "required arguments were not provided",
    );

    let missing_uninstall_runtime = env
        .command()
        .args(["--output", "json", "toolchain", "uninstall"])
        .output()
        .expect("nodeup --output json toolchain uninstall missing runtime");
    assert_json_parser_error(
        missing_uninstall_runtime,
        "required arguments were not provided",
    );

    let unknown_command = env
        .command()
        .args(["--output", "json", "unknown-command"])
        .output()
        .expect("nodeup --output json unknown command");
    assert_json_parser_error(unknown_command, "unrecognized subcommand");

    let unexpected_extra_arg = env
        .command()
        .args(["--output", "json", "show", "home", "extra"])
        .output()
        .expect("nodeup --output json show home extra argument");
    assert_json_parser_error(unexpected_extra_arg, "unexpected argument 'extra'");
}

#[test]
#[serial]
fn json_output_help_still_uses_clap_help_output() {
    let env = TestEnv::new();

    env.command()
        .args(["--output", "json", "--help"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "Rustup-like Node.js version manager",
        ))
        .stdout(predicates::str::contains(
            "Usage: nodeup [OPTIONS] <COMMAND>",
        ))
        .stderr(predicates::str::is_empty());
}

#[test]
#[serial]
fn delegated_run_arguments_do_not_request_json_parser_errors() {
    let env = TestEnv::new();

    env.command()
        .args(["run", "lts", "node", "--output", "json"])
        .assert()
        .failure()
        .code(4)
        .stderr(predicates::str::contains("nodeup error:"))
        .stderr(predicates::str::contains("Release index request failed"));
}

#[test]
#[serial]
fn install_list_uninstall_flow() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "list", "--output", "json"])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "uninstall", "22.1.0"])
        .assert()
        .success();
}

#[test]
#[serial]
fn toolchain_list_standard_prints_summary_counts_only() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let linked_runtime = env.root.join("linked-runtime-standard");
    let linked_runtime_bin = linked_runtime.join("bin");
    fs::create_dir_all(&linked_runtime_bin).unwrap();
    write_runtime_executable(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-standard\n",
    );

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-standard",
            linked_runtime.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .args(["toolchain", "list"])
        .output()
        .expect("toolchain list");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);

    assert!(stdout.contains("Installed runtimes: 1 | Linked runtimes: 1"));
    assert!(!stdout.contains("Installed runtimes (1):"));
    assert!(!stdout.contains("Linked runtimes (1):"));
    assert!(!stdout.contains("- v22.1.0 ->"));
    assert!(!stdout.contains("- linked-standard ->"));
}

#[test]
#[serial]
fn toolchain_list_quiet_prints_runtime_identifiers_only() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let linked_runtime = env.root.join("linked-runtime-quiet");
    let linked_runtime_bin = linked_runtime.join("bin");
    fs::create_dir_all(&linked_runtime_bin).unwrap();
    write_runtime_executable(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-quiet\n",
    );

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-quiet",
            linked_runtime.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .args(["toolchain", "list", "--quiet"])
        .output()
        .expect("toolchain list --quiet");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    let lines = stdout
        .lines()
        .filter(|line| !line.trim().is_empty())
        .collect::<Vec<_>>();

    assert_eq!(lines, vec!["v22.1.0", "linked-quiet"]);
}

#[test]
#[serial]
fn toolchain_list_verbose_includes_runtime_and_link_paths() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let linked_runtime = env.root.join("linked-runtime-verbose");
    let linked_runtime_bin = linked_runtime.join("bin");
    fs::create_dir_all(&linked_runtime_bin).unwrap();
    write_runtime_executable(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-verbose\n",
    );

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-verbose",
            linked_runtime.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .args(["toolchain", "list", "--verbose"])
        .output()
        .expect("toolchain list --verbose");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    let runtime_path = env.data_root.join("toolchains").join("v22.1.0");
    let linked_path = fs::canonicalize(&linked_runtime).unwrap();

    assert!(stdout.contains("Installed runtimes (1):"));
    assert!(stdout.contains(&format!("- v22.1.0 -> {}", runtime_path.display())));
    assert!(stdout.contains("Linked runtimes (1):"));
    assert!(stdout.contains(&format!("- linked-verbose -> {}", linked_path.display())));
}

#[test]
#[serial]
fn toolchain_list_json_output_is_stable_with_detail_flags() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let linked_runtime = env.root.join("linked-runtime-json");
    let linked_runtime_bin = linked_runtime.join("bin");
    fs::create_dir_all(&linked_runtime_bin).unwrap();
    write_runtime_executable(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-json\n",
    );

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-json",
            linked_runtime.to_str().unwrap(),
        ])
        .assert()
        .success();

    let baseline = env
        .command()
        .args(["--output", "json", "toolchain", "list"])
        .output()
        .expect("baseline toolchain list --output json");
    assert!(baseline.status.success());

    let quiet = env
        .command()
        .args(["--output", "json", "toolchain", "list", "--quiet"])
        .output()
        .expect("quiet toolchain list --output json");
    assert!(quiet.status.success());

    let verbose = env
        .command()
        .args(["--output", "json", "toolchain", "list", "--verbose"])
        .output()
        .expect("verbose toolchain list --output json");
    assert!(verbose.status.success());

    let baseline_json: Value = serde_json::from_slice(&baseline.stdout).unwrap();
    let quiet_json: Value = serde_json::from_slice(&quiet.stdout).unwrap();
    let verbose_json: Value = serde_json::from_slice(&verbose.stdout).unwrap();

    assert_eq!(baseline_json, quiet_json);
    assert_eq!(baseline_json, verbose_json);
}

#[test]
#[serial]
fn toolchain_link_rejects_reserved_channel_name_and_does_not_persist_selector() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-reserved-name");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n");

    let output = env
        .command()
        .args(["toolchain", "link", "lts", runtime_dir.to_str().unwrap()])
        .output()
        .expect("toolchain link with reserved channel selector");

    assert_eq!(output.status.code(), Some(2));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Invalid linked runtime name: lts"));
    assert!(stderr.contains(
        "Reserved channel selectors (`lts`, `current`, `latest`) cannot be used as linked runtime \
         names.",
    ));

    let list_output = env
        .command()
        .args(["--output", "json", "toolchain", "list"])
        .output()
        .expect("toolchain list after failed reserved-name link");
    assert!(list_output.status.success());

    let payload: Value = serde_json::from_slice(&list_output.stdout).unwrap();
    assert!(payload["linked"].get("lts").is_none());
}

#[test]
#[serial]
fn json_toolchain_link_reserved_name_failure_emits_invalid_input_error_envelope() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-reserved-name-json");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n");

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "toolchain",
            "link",
            "lts",
            runtime_dir.to_str().unwrap(),
        ])
        .output()
        .expect("toolchain link --output json with reserved channel selector");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Invalid linked runtime name: lts"));
}

#[test]
#[serial]
fn toolchain_link_rejects_case_variant_reserved_channel_name() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-reserved-case-name");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n");

    for name in ["LTS", "Current", "LATEST"] {
        let output = env
            .command()
            .args(["toolchain", "link", name, runtime_dir.to_str().unwrap()])
            .output()
            .expect("toolchain link with case-variant reserved channel selector");

        assert_eq!(output.status.code(), Some(2));
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(stderr.contains(&format!("Invalid linked runtime name: {name}")));
        assert!(stderr.contains("differ from reserved channel selectors"));
    }
}

#[test]
#[serial]
fn runtime_selector_commands_reject_case_variant_reserved_channel_names() {
    let env = TestEnv::new();
    let project_dir = env.root.join("case-variant-override");
    fs::create_dir_all(&project_dir).unwrap();

    let override_output = env
        .command()
        .args([
            "override",
            "set",
            "LTS",
            "--path",
            project_dir.to_str().unwrap(),
        ])
        .output()
        .expect("override set with case-variant reserved channel selector");
    assert_eq!(override_output.status.code(), Some(2));
    let override_stderr = String::from_utf8_lossy(&override_output.stderr);
    assert!(override_stderr.contains("Invalid runtime selector 'LTS'"));
    assert!(override_stderr.contains("Reserved channel selectors are case-sensitive"));

    let update_output = env
        .command()
        .args(["update", "LATEST"])
        .output()
        .expect("update with case-variant reserved channel selector");
    assert_eq!(update_output.status.code(), Some(2));
    let update_stderr = String::from_utf8_lossy(&update_output.stderr);
    assert!(update_stderr.contains("Invalid runtime selector 'LATEST'"));
    assert!(update_stderr.contains("Reserved channel selectors are case-sensitive"));
}

#[test]
#[serial]
fn toolchain_link_rejects_regular_file_path_and_does_not_persist_selector() {
    let env = TestEnv::new();
    let invalid_path = env.root.join("not-a-runtime-file");
    fs::write(&invalid_path, "not-a-runtime").unwrap();

    let output = env
        .command()
        .args([
            "toolchain",
            "link",
            "filelink",
            invalid_path.to_str().unwrap(),
        ])
        .output()
        .expect("toolchain link with regular file");

    assert_eq!(output.status.code(), Some(2));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Linked runtime path is not a directory"));

    let list_output = env
        .command()
        .args(["--output", "json", "toolchain", "list"])
        .output()
        .expect("toolchain list after failed file link");
    assert!(list_output.status.success());

    let payload: Value = serde_json::from_slice(&list_output.stdout).unwrap();
    assert!(payload["linked"].get("filelink").is_none());
}

#[test]
#[serial]
fn toolchain_link_rejects_directory_without_node_binary() {
    let env = TestEnv::new();
    let invalid_path = env.root.join("not-a-runtime-directory");
    fs::create_dir_all(&invalid_path).unwrap();

    let output = env
        .command()
        .args([
            "toolchain",
            "link",
            "dirlink",
            invalid_path.to_str().unwrap(),
        ])
        .output()
        .expect("toolchain link with directory missing node binary");

    assert_eq!(output.status.code(), Some(2));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Linked runtime path must contain a node executable under `bin/`"));
}

#[test]
#[serial]
fn json_toolchain_link_failure_emits_invalid_input_error_envelope() {
    let env = TestEnv::new();
    let invalid_path = env.root.join("not-a-runtime-json");
    fs::create_dir_all(&invalid_path).unwrap();

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "toolchain",
            "link",
            "jsonlink",
            invalid_path.to_str().unwrap(),
        ])
        .output()
        .expect("toolchain link --output json with invalid linked runtime path");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Linked runtime path must contain a node executable under `bin/`"));
}

#[cfg(unix)]
#[test]
#[serial]
fn toolchain_link_rejects_non_executable_node_file() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-not-executable");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho no-exec\n").unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-not-executable",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "Linked runtime node executable exists but is not runnable",
        ))
        .stderr(predicates::str::contains("executable bit is set"));
}

#[test]
#[serial]
fn toolchain_link_accepts_windows_node_exe_when_platform_is_forced() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-windows-node-exe");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    fs::write(runtime_bin.join("node.exe"), "windows node").unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args([
            "toolchain",
            "link",
            "linked-windows-node-exe",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();
}

#[test]
#[serial]
fn toolchain_link_rejects_windows_extensionless_node_when_platform_is_forced() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-windows-node");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho wrong-shape\n");

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args([
            "toolchain",
            "link",
            "linked-windows-node",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "Linked runtime path must contain a node executable under `bin/`",
        ))
        .stderr(predicates::str::contains("<path>/bin/node.exe"));
}

#[test]
#[serial]
fn toolchain_unlink_removes_record_without_deleting_external_runtime() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-unlink");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho unlink\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-unlink",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "unlink", "linked-unlink"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Removed 1 linked runtime(s)"));

    assert!(runtime_dir.exists());
    assert!(runtime_bin.join("node").exists());

    let list_output = env
        .command()
        .args(["--output", "json", "toolchain", "list"])
        .output()
        .expect("toolchain list after unlink");
    assert!(list_output.status.success());

    let payload: Value = serde_json::from_slice(&list_output.stdout).unwrap();
    assert!(payload["linked"].get("linked-unlink").is_none());

    let settings = fs::read_to_string(env.config_root.join("settings.toml")).unwrap();
    assert!(!settings.contains("linked-unlink"));
}

#[test]
#[serial]
fn toolchain_unlink_missing_link_returns_not_found() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "toolchain", "unlink", "missing-link"])
        .output()
        .expect("toolchain unlink missing link");

    assert_eq!(output.status.code(), Some(5));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "not-found");
    assert_eq!(payload["exit_code"], 5);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Linked runtime 'missing-link' does not exist"));
}

#[test]
#[serial]
fn toolchain_unlink_conflicts_when_link_is_default() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-unlink-default");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho default\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-unlink-default",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args(["default", "linked-unlink-default"])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "unlink", "linked-unlink-default"])
        .assert()
        .failure()
        .code(6)
        .stderr(predicates::str::contains(
            "Cannot unlink 'linked-unlink-default'; it is used as the default runtime",
        ));
}

#[test]
#[serial]
fn toolchain_unlink_conflicts_when_legacy_reserved_case_link_is_default() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("legacy-reserved-case-default");
    fs::create_dir_all(&runtime_dir).unwrap();
    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            "schema_version = 1\ndefault_selector = \"LTS\"\ntracked_selectors = \
             [\"LTS\"]\n\n[linked_runtimes]\nLTS = \"{}\"\n",
            runtime_dir.display()
        ),
    )
    .unwrap();

    env.command()
        .args(["toolchain", "unlink", "LTS"])
        .assert()
        .failure()
        .code(6)
        .stderr(predicates::str::contains(
            "Cannot unlink 'LTS'; it is used as the default runtime",
        ));
}

#[test]
#[serial]
fn toolchain_unlink_conflicts_when_link_is_used_by_override() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-unlink-override");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho override\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-unlink-override",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    let project_dir = env.root.join("project-unlink-override");
    fs::create_dir_all(&project_dir).unwrap();
    env.command()
        .args([
            "override",
            "set",
            "linked-unlink-override",
            "--path",
            project_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "unlink", "linked-unlink-override"])
        .assert()
        .failure()
        .code(6)
        .stderr(predicates::str::contains(
            "Cannot unlink 'linked-unlink-override'; it is referenced by a directory override",
        ));
}

#[test]
#[serial]
fn toolchain_unlink_conflicts_when_legacy_reserved_case_link_is_used_by_override() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("legacy-reserved-case-override");
    let project_dir = env.root.join("legacy-reserved-case-project");
    fs::create_dir_all(&runtime_dir).unwrap();
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            "schema_version = 1\ntracked_selectors = [\"LATEST\"]\n\n[linked_runtimes]\nLATEST = \
             \"{}\"\n",
            runtime_dir.display()
        ),
    )
    .unwrap();
    fs::write(
        env.config_root.join("overrides.toml"),
        format!(
            "schema_version = 1\n\n[[entries]]\npath = \"{}\"\nselector = \"LATEST\"\n",
            project_dir.display()
        ),
    )
    .unwrap();

    env.command()
        .args(["toolchain", "unlink", "LATEST"])
        .assert()
        .failure()
        .code(6)
        .stderr(predicates::str::contains(
            "Cannot unlink 'LATEST'; it is referenced by a directory override",
        ));
}

#[test]
#[serial]
fn toolchain_list_rejects_conflicting_detail_flags() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "list", "--quiet", "--verbose"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("cannot be used with '--verbose'"));
}

#[test]
#[serial]
fn toolchain_list_quiet_emits_no_blank_line_when_empty() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "list", "--quiet"])
        .assert()
        .success()
        .stdout(predicates::str::is_empty());
}

#[test]
#[serial]
fn uninstall_blocks_default_selector_with_mixed_version_spelling() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let output = env
        .command()
        .args(["--output", "json", "toolchain", "uninstall", "v22.1.0"])
        .output()
        .expect("uninstall default blocker");

    assert_eq!(output.status.code(), Some(6));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "conflict");
    assert_eq!(
        payload["diagnostics"]["blocked_versions"],
        serde_json::json!(["v22.1.0"])
    );
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["reference_type"],
        "global-default"
    );
    assert_eq!(payload["diagnostics"]["blockers"][0]["runtime"], "v22.1.0");
    assert_eq!(payload["diagnostics"]["blockers"][0]["selector"], "22.1.0");
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["path"],
        env.config_root.join("settings.toml").to_str().unwrap()
    );
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["change_command"],
        "nodeup default <runtime>"
    );
}

#[test]
#[serial]
fn uninstall_blocks_override_selector_with_mixed_version_spelling() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    let project_dir = env.root.join("project-mixed-override");
    fs::create_dir_all(&project_dir).unwrap();

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command()
        .current_dir(&project_dir)
        .args(["override", "set", "22.1.0"])
        .assert()
        .success();

    let output = env
        .command()
        .args(["--output", "json", "toolchain", "uninstall", "v22.1.0"])
        .output()
        .expect("uninstall override blocker");

    assert_eq!(output.status.code(), Some(6));

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["reference_type"],
        "directory-override"
    );
    assert_eq!(payload["diagnostics"]["blockers"][0]["runtime"], "v22.1.0");
    assert_eq!(payload["diagnostics"]["blockers"][0]["selector"], "v22.1.0");
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["path"],
        project_dir.to_str().unwrap()
    );
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["clear_command"],
        format!("nodeup override unset --path {}", project_dir.display())
    );
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["change_command"],
        format!(
            "nodeup override set <runtime> --path {}",
            project_dir.display()
        )
    );
}

#[test]
#[serial]
fn uninstall_reports_all_default_and_override_blockers_with_follow_up_commands() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    let project_dir = env.root.join("project-combined-blockers");
    fs::create_dir_all(&project_dir).unwrap();

    env.command().args(["default", "22.1.0"]).assert().success();
    env.command()
        .args([
            "override",
            "set",
            "v22.1.0",
            "--path",
            project_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "uninstall", "22.1.0"])
        .assert()
        .failure()
        .code(6)
        .stderr(predicates::str::contains("global-default path="))
        .stderr(predicates::str::contains("directory-override path="))
        .stderr(predicates::str::contains("nodeup default <runtime>"))
        .stderr(predicates::str::contains(format!(
            "nodeup override unset --path {}",
            project_dir.display()
        )))
        .stderr(predicates::str::contains(
            "nodeup toolchain uninstall v22.1.0",
        ));
}

#[test]
#[serial]
fn uninstall_removes_tracked_selector_across_version_spellings() {
    let env = TestEnv::new();
    env.register_index(&[("22.2.0", Some("Jod")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "uninstall", "v22.1.0"])
        .assert()
        .success();

    env.command()
        .args(["update"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "No runtimes are eligible for update",
        ));
}

#[test]
#[serial]
fn uninstall_is_atomic_when_later_target_conflicts_with_default() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod")), ("24.0.0", None)]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );
    env.register_release(
        "24.0.0",
        make_archive(
            "24.0.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-24\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0", "24.0.0"])
        .assert()
        .success();

    env.command().args(["default", "24.0.0"]).assert().success();

    env.command()
        .args(["toolchain", "uninstall", "22.1.0", "24.0.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("global-default path="))
        .stderr(predicates::str::contains("nodeup default <runtime>"));

    assert!(env.data_root.join("toolchains").join("v22.1.0").exists());
    assert!(env.data_root.join("toolchains").join("v24.0.0").exists());
}

#[test]
#[serial]
fn uninstall_is_atomic_when_any_target_is_not_installed() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "uninstall", "22.1.0", "24.0.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Runtime v24.0.0 is not installed",
        ));

    assert!(env.data_root.join("toolchains").join("v22.1.0").exists());
}

#[test]
#[serial]
fn uninstall_reports_reference_blockers_before_missing_targets() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command().args(["default", "22.1.0"]).assert().success();

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "toolchain",
            "uninstall",
            "22.1.0",
            "24.0.0",
        ])
        .output()
        .expect("uninstall blocked runtime with missing later target");

    assert_eq!(output.status.code(), Some(6));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "conflict");
    assert_eq!(
        payload["diagnostics"]["blocked_versions"],
        serde_json::json!(["v22.1.0"])
    );
    assert_eq!(
        payload["diagnostics"]["blockers"][0]["reference_type"],
        "global-default"
    );
    assert_eq!(payload["diagnostics"]["blockers"][0]["runtime"], "v22.1.0");
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Cannot uninstall v22.1.0"));
    assert!(!payload["message"]
        .as_str()
        .unwrap()
        .contains("Runtime v24.0.0 is not installed"));

    assert!(env.data_root.join("toolchains").join("v22.1.0").exists());
}

#[test]
#[serial]
fn uninstall_deduplicates_canonical_duplicate_targets() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "uninstall", "v22.1.0", "22.1.0"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Removed 1 runtime(s)"));

    assert!(!env.data_root.join("toolchains").join("v22.1.0").exists());
}

#[test]
#[serial]
fn default_override_show_precedence() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod")), ("24.0.0", None)]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );
    env.register_release(
        "24.0.0",
        make_archive(
            "24.0.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-24\n")],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();
    env.command()
        .args(["toolchain", "install", "24.0.0"])
        .assert()
        .success();

    let project_dir = env.root.join("project");
    fs::create_dir_all(&project_dir).unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["override", "set", "24.0.0"])
        .assert()
        .success();

    env.command()
        .current_dir(&project_dir)
        .args(["show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains("v24.0.0"));
}

#[test]
#[serial]
fn default_json_returns_selector_when_channel_resolution_is_offline() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
default_selector = "lts"
tracked_selectors = ["lts"]

[linked_runtimes]
"#,
    )
    .unwrap();

    let output = env
        .command()
        .env("NODEUP_INDEX_URL", "http://127.0.0.1:9/index.json")
        .args(["--output", "json", "default"])
        .output()
        .expect("default --output json with offline channel selector");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["default_selector"], "lts");
    assert!(payload["resolved_runtime"].is_null());
    assert_eq!(payload["resolution_error"]["kind"], "network");
}

#[test]
#[serial]
fn default_json_returns_selector_when_default_selector_is_invalid() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
default_selector = "invalid selector"
tracked_selectors = ["invalid selector"]

[linked_runtimes]
"#,
    )
    .unwrap();

    let output = env
        .command()
        .args(["--output", "json", "default"])
        .output()
        .expect("default --output json with invalid selector");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["default_selector"], "invalid selector");
    assert!(payload["resolved_runtime"].is_null());
    assert_eq!(payload["resolution_error"]["kind"], "invalid-input");
}

#[test]
#[serial]
fn default_json_resolved_path_keeps_resolution_error_null() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
default_selector = "22.1.0"
tracked_selectors = ["22.1.0"]

[linked_runtimes]
"#,
    )
    .unwrap();

    let output = env
        .command()
        .args(["--output", "json", "default"])
        .output()
        .expect("default --output json with resolvable selector");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["default_selector"], "22.1.0");
    assert_eq!(payload["resolved_runtime"], "v22.1.0");
    assert!(payload["resolution_error"].is_null());
}

#[test]
#[serial]
fn default_json_reports_legacy_reserved_case_link_metadata() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("legacy-reserved-case-default-json");
    fs::create_dir_all(runtime_dir.join("bin")).unwrap();

    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            "schema_version = 1\ndefault_selector = \"LTS\"\ntracked_selectors = \
             [\"LTS\"]\n\n[linked_runtimes]\nLTS = \"{}\"\n",
            runtime_dir.display()
        ),
    )
    .unwrap();

    let output = env
        .command()
        .args(["--output", "json", "default"])
        .output()
        .expect("default --output json with legacy reserved-case linked selector");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["default_selector"], "LTS");
    assert_eq!(payload["selector_kind"], "linked-runtime");
    assert_eq!(payload["canonical_selector"], "LTS");
    assert_eq!(payload["resolved_runtime"], "LTS");
    assert!(payload["resolution_error"].is_null());
}

#[test]
#[serial]
fn default_human_unresolved_still_prints_selector() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
default_selector = "lts"
tracked_selectors = ["lts"]

[linked_runtimes]
"#,
    )
    .unwrap();

    env.command()
        .env("NODEUP_INDEX_URL", "http://127.0.0.1:9/index.json")
        .arg("default")
        .assert()
        .success()
        .stdout(predicates::str::contains("Default runtime: lts"))
        .stdout(predicates::str::contains("resolution unavailable"));
}

#[test]
#[serial]
fn show_active_runtime_and_which_resolve_legacy_reserved_case_default_link() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("legacy-reserved-case-default-resolve");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho legacy-default\n");

    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            "schema_version = 1\ndefault_selector = \"LTS\"\ntracked_selectors = \
             [\"LTS\"]\n\n[linked_runtimes]\nLTS = \"{}\"\n",
            runtime_dir.display()
        ),
    )
    .unwrap();

    let show_output = env
        .command()
        .args(["--output", "json", "show", "active-runtime"])
        .output()
        .expect("show active-runtime with legacy reserved-case default");
    assert!(show_output.status.success());
    let show_payload: Value = serde_json::from_slice(&show_output.stdout).unwrap();
    assert_eq!(show_payload["runtime"], "LTS");
    assert_eq!(show_payload["selector"], "LTS");
    assert_eq!(show_payload["selector_kind"], "linked-runtime");
    assert_eq!(show_payload["canonical_selector"], "LTS");

    env.command()
        .args(["which", "node"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            runtime_bin.join("node").to_str().unwrap(),
        ));
}

#[test]
#[serial]
fn show_active_runtime_resolves_legacy_reserved_case_override_link() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("legacy-reserved-case-override-resolve");
    let runtime_bin = runtime_dir.join("bin");
    let project_dir = env.root.join("legacy-reserved-case-override-project");
    fs::create_dir_all(&runtime_bin).unwrap();
    fs::create_dir_all(&project_dir).unwrap();
    write_runtime_executable(
        runtime_bin.join("node"),
        "#!/bin/sh\necho legacy-override\n",
    );

    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            "schema_version = 1\ntracked_selectors = [\"LATEST\"]\n\n[linked_runtimes]\nLATEST = \
             \"{}\"\n",
            runtime_dir.display()
        ),
    )
    .unwrap();
    fs::write(
        env.config_root.join("overrides.toml"),
        format!(
            "schema_version = 1\n\n[[entries]]\npath = \"{}\"\nselector = \"LATEST\"\n",
            project_dir.display()
        ),
    )
    .unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Active runtime: LATEST"));

    env.command()
        .current_dir(&project_dir)
        .args(["which", "node"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            runtime_bin.join("node").to_str().unwrap(),
        ));
}

#[test]
#[serial]
fn show_active_runtime_fails_when_linked_runtime_path_is_deleted() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-deleted");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(
        runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-node\n",
    );

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-runtime-deleted",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["default", "linked-runtime-deleted"])
        .assert()
        .success();

    fs::remove_dir_all(&runtime_dir).unwrap();

    env.command()
        .args(["show", "active-runtime"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' does not exist for runtime linked-runtime-deleted",
        ));
}

#[test]
#[serial]
fn show_active_runtime_logs_unavailable_reason_for_deleted_linked_runtime() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-deleted-logs");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(
        runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-node\n",
    );

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-runtime-deleted-logs",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["default", "linked-runtime-deleted-logs"])
        .assert()
        .success();

    fs::remove_dir_all(&runtime_dir).unwrap();

    env.command_with_info_logs()
        .args(["show", "active-runtime"])
        .assert()
        .failure()
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.show.active-runtime\"",
        ))
        .stdout(predicates::str::contains("availability: false"))
        .stdout(predicates::str::contains(
            "reason: \"node-executable-missing\"",
        ));
}

#[cfg(unix)]
#[test]
#[serial]
fn show_active_runtime_fails_when_linked_node_is_not_executable() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-active-not-executable");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho inactive\n").unwrap();

    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            r#"schema_version = 1
default_selector = "linked-active-not-executable"
tracked_selectors = ["linked-active-not-executable"]

[linked_runtimes]
"linked-active-not-executable" = "{}"
"#,
            runtime_dir.display()
        ),
    )
    .unwrap();

    env.command()
        .args(["show", "active-runtime"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' exists but is not runnable for runtime linked-active-not-executable",
        ));

    env.command()
        .args(["which", "node"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' exists but is not runnable for runtime linked-active-not-executable",
        ));

    env.command()
        .args(["run", "linked-active-not-executable", "node"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' exists but is not runnable for runtime linked-active-not-executable",
        ));
}

#[test]
#[serial]
fn windows_node_availability_rejects_extensionless_linked_runtime_node() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-windows-extensionless-node");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho wrong-shape\n");

    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            r#"schema_version = 1
default_selector = "linked-windows-extensionless-node"
tracked_selectors = ["linked-windows-extensionless-node"]

[linked_runtimes]
"linked-windows-extensionless-node" = "{}"
"#,
            runtime_dir.display()
        ),
    )
    .unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["show", "active-runtime"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' does not exist for runtime linked-windows-extensionless-node",
        ));

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["which", "node"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' does not exist for runtime linked-windows-extensionless-node",
        ));

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["run", "linked-windows-extensionless-node", "node"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'node' is not available in runtime linked-windows-extensionless-node",
        ));
}

#[test]
#[serial]
fn override_resolution_logs_hit_with_fallback_reason() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho node-22\n")],
        ),
        None,
    );
    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let project_dir = env.root.join("project-override-hit-logs");
    fs::create_dir_all(&project_dir).unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["override", "set", "22.1.0"])
        .assert()
        .success();

    env.command_with_info_logs()
        .current_dir(&project_dir)
        .args(["show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.resolve.override\"",
        ))
        .stdout(predicates::str::contains("matched: true"))
        .stdout(predicates::str::contains(
            "fallback_reason: \"override-matched\"",
        ));
}

#[test]
#[serial]
fn override_resolution_logs_miss_without_default_selector() {
    let env = TestEnv::new();

    env.command_with_info_logs()
        .args(["show", "active-runtime"])
        .assert()
        .failure()
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.resolve.override\"",
        ))
        .stdout(predicates::str::contains("matched: false"))
        .stdout(predicates::str::contains(
            "fallback_reason: \"no-default-selector\"",
        ));
}

#[test]
#[serial]
fn management_human_default_logging_suppresses_info_logs_without_rust_log_env() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("RUST_LOG")
        .args(["show", "home"])
        .output()
        .expect("show home without rust log env");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!stdout.contains("command_path: \"nodeup.show.home\""));
    assert!(!stderr.contains("command_path: \"nodeup.show.home\""));
}

#[test]
#[serial]
fn management_human_default_logging_keeps_warning_logs_without_rust_log_env() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
default_selector = "invalid selector"
tracked_selectors = ["invalid selector"]

[linked_runtimes]
"#,
    )
    .unwrap();

    let output = env
        .command()
        .env_remove("RUST_LOG")
        .args(["default"])
        .output()
        .expect("show default without rust log env");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("Default runtime: invalid selector (resolution unavailable)"));
    assert!(stdout.contains("command_path: \"nodeup.default\""));
    assert!(stdout.contains("outcome: \"unresolved\""));
}

#[test]
#[serial]
fn show_home_human_output_includes_all_roots() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["show", "home"])
        .output()
        .expect("show home");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("nodeup home:"));
    assert!(stdout.contains(&format!("data_root: {}", env.data_root.to_string_lossy())));
    assert!(stdout.contains(&format!("cache_root: {}", env.cache_root.to_string_lossy())));
    assert!(stdout.contains(&format!(
        "config_root: {}",
        env.config_root.to_string_lossy()
    )));
}

#[test]
#[serial]
fn json_show_active_runtime_failure_emits_stderr_error_envelope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "show", "active-runtime"])
        .output()
        .expect("show active-runtime --output json");

    assert_eq!(output.status.code(), Some(5));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "not-found");
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("No runtime selector resolved"));
    assert!(payload["message"].as_str().unwrap().contains("Hint:"));
    assert_eq!(payload["exit_code"], 5);
}

#[test]
#[serial]
fn json_show_active_runtime_failure_remains_parseable_without_rust_log_env() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("RUST_LOG")
        .args(["--output", "json", "show", "active-runtime"])
        .output()
        .expect("show active-runtime --output json without rust log env");

    assert_eq!(output.status.code(), Some(5));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "not-found");
    assert_eq!(payload["exit_code"], 5);
}

#[test]
#[serial]
fn json_show_home_remains_parseable_without_rust_log_env() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("RUST_LOG")
        .args(["--output", "json", "show", "home"])
        .output()
        .expect("show home --output json without rust log env");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!stdout.contains("command_path:"));
    assert!(!stderr.contains("command_path:"));

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["data_root"].as_str().is_some());
    assert!(payload["cache_root"].as_str().is_some());
    assert!(payload["config_root"].as_str().is_some());
}

#[test]
#[serial]
fn completions_generates_script_for_valid_shell() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "zsh"])
        .output()
        .expect("completions zsh");

    assert!(output.status.success());
    assert!(!output.stdout.is_empty());
    assert!(String::from_utf8_lossy(&output.stdout).contains("nodeup"));
}

#[test]
#[serial]
fn json_completions_success_outputs_raw_script() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "completions", "zsh"])
        .output()
        .expect("completions --output json");

    assert!(output.status.success());
    assert!(!output.stdout.is_empty());
    assert!(serde_json::from_slice::<Value>(&output.stdout).is_err());
    assert!(String::from_utf8_lossy(&output.stdout).contains("nodeup"));
}

#[test]
#[serial]
fn completions_accepts_global_output_after_shell() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "bash", "--output", "json"])
        .output()
        .expect("completions bash --output json");

    assert!(output.status.success());
    assert!(!output.stdout.is_empty());
    assert!(serde_json::from_slice::<Value>(&output.stdout).is_err());
    assert!(String::from_utf8_lossy(&output.stdout).contains("nodeup"));
}

#[test]
#[serial]
fn completions_accepts_help_after_shell() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "bash", "--help"])
        .output()
        .expect("completions bash --help");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    assert!(String::from_utf8_lossy(&output.stdout).contains("Generate shell completion scripts"));
}

#[test]
#[serial]
fn completions_accepts_valid_top_level_scope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "bash", "shim"])
        .output()
        .expect("completions bash shim");

    assert!(output.status.success());
    assert!(!output.stdout.is_empty());
    assert!(String::from_utf8_lossy(&output.stdout).contains("nodeup"));
}

#[test]
#[serial]
fn completions_accepts_global_output_after_scope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "bash", "shim", "--output", "json"])
        .output()
        .expect("completions bash shim --output json");

    assert!(output.status.success());
    assert!(!output.stdout.is_empty());
    assert!(serde_json::from_slice::<Value>(&output.stdout).is_err());
    assert!(String::from_utf8_lossy(&output.stdout).contains("nodeup"));
}

#[test]
#[serial]
fn completions_accepts_help_after_scope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "bash", "shim", "--help"])
        .output()
        .expect("completions bash shim --help");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    assert!(String::from_utf8_lossy(&output.stdout).contains("Generate shell completion scripts"));
}

#[test]
#[serial]
fn json_completions_invalid_shell_emits_invalid_input_error_envelope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "completions", "bad-shell"])
        .output()
        .expect("completions --output json invalid shell");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Unsupported shell"));
}

#[test]
#[serial]
fn json_completions_invalid_scope_emits_invalid_input_error_envelope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "completions", "bash", "invalid-scope"])
        .output()
        .expect("completions --output json invalid scope");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Unsupported command scope"));
    assert_eq!(
        payload["diagnostics"]["allowed_scope_category"],
        "top-level-command"
    );
    assert_eq!(payload["diagnostics"]["rejected_scope"], "invalid-scope");
}

#[test]
#[serial]
fn completions_subcommand_scope_suggests_top_level_scope() {
    let env = TestEnv::new();

    env.command()
        .args(["completions", "bash", "toolchain", "install"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "Unsupported command scope 'toolchain install'",
        ))
        .stderr(predicates::str::contains(
            "Only top-level command scopes are supported",
        ))
        .stderr(predicates::str::contains(
            "nodeup completions bash toolchain",
        ));
}

#[test]
#[serial]
fn json_completions_subcommand_scope_emits_scope_diagnostics() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "completions", "zsh", "override", "set"])
        .output()
        .expect("completions --output json invalid subcommand scope");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Unsupported command scope 'override set'"));
    assert_eq!(payload["diagnostics"]["rejected_scope"], "override set");
    assert_eq!(
        payload["diagnostics"]["allowed_scope_category"],
        "top-level-command"
    );
    assert_eq!(payload["diagnostics"]["suggested_scope"], "override");
    assert!(payload["diagnostics"]["allowed_scopes"]
        .as_array()
        .unwrap()
        .iter()
        .any(|scope| scope == "override"));
}

#[test]
#[serial]
fn json_completions_subcommand_scope_captures_option_like_tokens() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "completions",
            "bash",
            "override",
            "set",
            "--path",
        ])
        .output()
        .expect("completions --output json invalid option-like scope");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Unsupported command scope 'override set --path'"));
    assert_eq!(
        payload["diagnostics"]["rejected_scope"],
        "override set --path"
    );
    assert_eq!(payload["diagnostics"]["suggested_scope"], "override");
}

#[test]
#[serial]
fn json_completions_escaped_help_scope_is_rejected() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "completions", "bash", "--", "--help"])
        .output()
        .expect("completions --output json escaped help scope");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Unsupported command scope '--help'"));
    assert_eq!(payload["diagnostics"]["rejected_scope"], "--help");
}

#[test]
#[serial]
fn json_startup_failure_emits_stderr_error_envelope() {
    let env = TestEnv::new();
    let invalid_data_home = env.root.join("invalid-data-home");
    fs::write(&invalid_data_home, "not-a-directory").unwrap();

    let output = env
        .command()
        .env("NODEUP_DATA_HOME", &invalid_data_home)
        .args(["--output", "json", "show", "home"])
        .output()
        .expect("startup failure --output json");

    assert!(!output.status.success());
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "internal");
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("I/O operation failed:"));
    assert!(payload["message"].as_str().unwrap().contains("Hint:"));

    let process_exit_code = output.status.code().unwrap();
    assert_ne!(process_exit_code, 0);
    assert_eq!(payload["exit_code"], process_exit_code);
}

#[test]
#[serial]
fn self_update_reports_human_and_json_statuses() {
    let env = TestEnv::new();
    let target_binary = env.root.join("bin").join("nodeup");
    let source_binary = env.root.join("nodeup-next");
    fs::create_dir_all(target_binary.parent().unwrap()).unwrap();
    fs::write(&target_binary, "nodeup-old").unwrap();
    fs::write(&source_binary, "nodeup-new").unwrap();

    env.command()
        .env("NODEUP_SELF_BIN_PATH", target_binary.to_str().unwrap())
        .env("NODEUP_SELF_UPDATE_SOURCE", source_binary.to_str().unwrap())
        .args(["self", "update"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Self update status: updated"));

    env.command()
        .env("NODEUP_SELF_BIN_PATH", target_binary.to_str().unwrap())
        .env("NODEUP_SELF_UPDATE_SOURCE", source_binary.to_str().unwrap())
        .args(["--output", "json", "self", "update"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "\"status\": \"already-up-to-date\"",
        ));

    assert_eq!(
        fs::read(&target_binary).unwrap(),
        fs::read(&source_binary).unwrap()
    );
}

#[test]
#[serial]
fn self_update_logs_action_and_outcome_status() {
    let env = TestEnv::new();
    let target_binary = env.root.join("bin").join("nodeup");
    let source_binary = env.root.join("nodeup-next");
    fs::create_dir_all(target_binary.parent().unwrap()).unwrap();
    fs::write(&target_binary, "nodeup-old").unwrap();
    fs::write(&source_binary, "nodeup-new").unwrap();

    env.command_with_info_logs()
        .env("NODEUP_SELF_BIN_PATH", target_binary.to_str().unwrap())
        .env("NODEUP_SELF_UPDATE_SOURCE", source_binary.to_str().unwrap())
        .args(["--output", "json", "self", "update"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.self.update\"",
        ))
        .stdout(predicates::str::contains("action: \"self update\""))
        .stdout(predicates::str::contains("outcome: \"updated\""));
}

#[test]
#[serial]
fn self_uninstall_removes_artifacts_and_logs_outcome() {
    let env = TestEnv::new();
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();
    fs::write(env.cache_root.join("cache-marker.txt"), "cache").unwrap();
    fs::write(env.config_root.join("config-marker.txt"), "config").unwrap();

    env.command_with_info_logs()
        .args(["--output", "json", "self", "uninstall"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"removed\""))
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.self.uninstall\"",
        ))
        .stdout(predicates::str::contains("action: \"self uninstall\""))
        .stdout(predicates::str::contains("outcome: \"removed\""));

    assert!(!env.data_root.exists());
    assert!(!env.cache_root.exists());
    assert!(!env.config_root.exists());
}

#[test]
#[serial]
fn self_uninstall_reports_already_clean_on_repeated_runs() {
    let env = TestEnv::new();
    fs::write(
        env.config_root.join("settings.toml"),
        "schema_version = 1\n",
    )
    .unwrap();

    env.command()
        .args(["--output", "json", "self", "uninstall"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"removed\""));

    env.command()
        .args(["--output", "json", "self", "uninstall"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"already-clean\""));
}

#[test]
#[serial]
fn self_uninstall_reports_cleanup_boundaries_and_manual_steps() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-leftover");
    let binary_path = env.root.join("bin").join("nodeup");
    fs::create_dir_all(&shim_dir).unwrap();
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::write(&binary_path, "nodeup").unwrap();
    #[cfg(unix)]
    let shim_path = {
        let shim_path = shim_dir.join("node");
        std::os::unix::fs::symlink(&binary_path, &shim_path).unwrap();
        shim_path
    };
    #[cfg(not(unix))]
    let shim_path = {
        let shim_path = shim_dir.join("node.exe");
        fs::write(&shim_path, "shim").unwrap();
        fs::write(shim_dir.join(".node.exe.nodeup-shim"), "nodeup shim copy\n").unwrap();
        shim_path
    };
    fs::write(env.config_root.join("config-marker.txt"), "config").unwrap();

    env.command()
        .env("NODEUP_SHIM_DIR", &shim_dir)
        .env("NODEUP_SELF_BIN_PATH", &binary_path)
        .args(["--output", "json", "self", "uninstall"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"removed_paths\""))
        .stdout(predicates::str::contains("\"cleanup_boundaries\""))
        .stdout(predicates::str::contains("\"category\": \"binary\""))
        .stdout(predicates::str::contains("\"category\": \"shims\""))
        .stdout(predicates::str::contains(
            "\"category\": \"shell-profile-path\"",
        ))
        .stdout(predicates::str::contains("\"remaining_manual_steps\""))
        .stdout(predicates::str::contains(binary_path.to_str().unwrap()))
        .stdout(predicates::str::contains(shim_path.to_str().unwrap()));

    assert!(binary_path.exists());
    assert!(shim_path.exists() || fs::symlink_metadata(&shim_path).is_ok());
}

#[test]
#[serial]
fn self_uninstall_reports_default_setup_shim_leftovers() {
    let env = TestEnv::new();
    let shim_dir = env.root.join(".local").join("bin");
    let node_shim = if cfg!(windows) {
        shim_dir.join("node.exe")
    } else {
        shim_dir.join("node")
    };

    env.command()
        .env("HOME", &env.root)
        .args(["shim", "setup"])
        .assert()
        .success();

    env.command()
        .env("HOME", &env.root)
        .args(["--output", "json", "self", "uninstall"])
        .assert()
        .success()
        .stdout(predicates::str::contains(node_shim.to_str().unwrap()));

    assert!(node_shim.exists());
}

#[test]
#[serial]
fn self_uninstall_preserves_configured_shim_dir_inside_removed_root() {
    let env = TestEnv::new();
    let shim_dir = env.data_root.join("shims");
    let binary_path = env.root.join("bin").join("nodeup");
    fs::create_dir_all(&shim_dir).unwrap();
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::write(&binary_path, "nodeup").unwrap();
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();

    #[cfg(unix)]
    let shim_path = {
        let shim_path = shim_dir.join("node");
        std::os::unix::fs::symlink(&binary_path, &shim_path).unwrap();
        shim_path
    };
    #[cfg(not(unix))]
    let shim_path = {
        let shim_path = shim_dir.join("node.exe");
        fs::write(&shim_path, "shim").unwrap();
        fs::write(shim_dir.join(".node.exe.nodeup-shim"), "nodeup shim copy\n").unwrap();
        shim_path
    };

    let output = env
        .command()
        .env("NODEUP_SHIM_DIR", &shim_dir)
        .env("NODEUP_SELF_BIN_PATH", &binary_path)
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall preserves configured shim dir");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["likely_leftover_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == shim_path.to_str().unwrap()));
    assert!(payload["removed_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == env.data_root.to_str().unwrap()));
    assert!(shim_path.exists() || fs::symlink_metadata(&shim_path).is_ok());
    assert!(!env.data_root.join("data-marker.txt").exists());
}

#[cfg(unix)]
#[test]
#[serial]
fn self_uninstall_preserves_configured_symlinked_shim_dir_inside_removed_root() {
    let env = TestEnv::new();
    let real_shim_dir = env.root.join("real-shims");
    let shim_dir = env.data_root.join("linked-shims");
    let binary_path = env.root.join("bin").join("nodeup");
    fs::create_dir_all(&real_shim_dir).unwrap();
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::write(&binary_path, "nodeup").unwrap();
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();
    std::os::unix::fs::symlink(&real_shim_dir, &shim_dir).unwrap();

    let shim_path = shim_dir.join("node");
    std::os::unix::fs::symlink(&binary_path, real_shim_dir.join("node")).unwrap();

    let output = env
        .command()
        .env("NODEUP_SHIM_DIR", &shim_dir)
        .env("NODEUP_SELF_BIN_PATH", &binary_path)
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall preserves configured symlinked shim dir");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["likely_leftover_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == shim_path.to_str().unwrap()));
    assert!(fs::symlink_metadata(&shim_dir).is_ok());
    assert!(fs::symlink_metadata(&shim_path).is_ok());
    assert!(!env.data_root.join("data-marker.txt").exists());
}

#[test]
#[serial]
fn self_uninstall_preserves_custom_managed_shim_dir_inside_removed_root() {
    let env = TestEnv::new();
    let shim_dir = env.data_root.join("my-shims");
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();

    env.command()
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .success();

    let node_shim = if cfg!(windows) {
        shim_dir.join("node.exe")
    } else {
        shim_dir.join("node")
    };

    let output = env
        .command()
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall preserves custom managed shim dir");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["likely_leftover_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == node_shim.to_str().unwrap()));
    assert!(payload["removed_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == env.data_root.to_str().unwrap()));
    assert!(node_shim.exists() || fs::symlink_metadata(&node_shim).is_ok());
    assert!(!env.data_root.join("data-marker.txt").exists());
}

#[test]
#[serial]
fn self_uninstall_preserves_custom_managed_shim_dir_under_default_data_root() {
    let env = TestEnv::new();
    let data_root = env.root.join(".local").join("share").join("nodeup");
    let shim_dir = data_root.join("my-shims");
    fs::create_dir_all(&data_root).unwrap();
    fs::write(data_root.join("data-marker.txt"), "data").unwrap();

    Command::new(assert_cmd::cargo::cargo_bin!("nodeup"))
        .env("HOME", &env.root)
        .env("NODEUP_INDEX_URL", &env.index_url)
        .env("NODEUP_DOWNLOAD_BASE_URL", &env.download_base_url)
        .env("NODEUP_FORCE_PLATFORM", "linux-x64")
        .env("NODEUP_LOG_COLOR", "never")
        .env("RUST_LOG", "off")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .success();

    let node_shim = if cfg!(windows) {
        shim_dir.join("node.exe")
    } else {
        shim_dir.join("node")
    };

    let output = Command::new(assert_cmd::cargo::cargo_bin!("nodeup"))
        .env("HOME", &env.root)
        .env("NODEUP_INDEX_URL", &env.index_url)
        .env("NODEUP_DOWNLOAD_BASE_URL", &env.download_base_url)
        .env("NODEUP_FORCE_PLATFORM", "linux-x64")
        .env("NODEUP_LOG_COLOR", "never")
        .env("RUST_LOG", "off")
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall preserves default-root custom managed shim dir");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["likely_leftover_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == node_shim.to_str().unwrap()));
    assert!(payload["removed_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == data_root.to_str().unwrap()));
    assert!(node_shim.exists() || fs::symlink_metadata(&node_shim).is_ok());
    assert!(!data_root.join("data-marker.txt").exists());
}

#[test]
#[serial]
fn self_uninstall_reports_renamed_binary_cleanup_boundary() {
    let env = TestEnv::new();
    let binary_path = env.root.join("bin").join("nodeup-linux-amd64");
    let shim_dir = env.root.join("nodeup-shims-leftover");
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::create_dir_all(&shim_dir).unwrap();
    fs::write(&binary_path, "nodeup").unwrap();
    fs::write(env.config_root.join("config-marker.txt"), "config").unwrap();
    #[cfg(unix)]
    let shim_path = {
        let shim_path = shim_dir.join("node");
        std::os::unix::fs::symlink(&binary_path, &shim_path).unwrap();
        Some(shim_path)
    };
    #[cfg(not(unix))]
    let shim_path: Option<PathBuf> = None;

    let output = env
        .command()
        .env("NODEUP_SHIM_DIR", &shim_dir)
        .env("NODEUP_SELF_BIN_PATH", &binary_path)
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall reports renamed binary boundary");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let binary_boundary = payload["cleanup_boundaries"]
        .as_array()
        .unwrap()
        .iter()
        .find(|boundary| boundary["category"] == "binary")
        .unwrap();
    assert!(binary_boundary["paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == binary_path.to_str().unwrap()));
    if let Some(shim_path) = shim_path {
        assert!(payload["likely_leftover_paths"]
            .as_array()
            .unwrap()
            .iter()
            .any(|path| path == shim_path.to_str().unwrap()));
    }
    assert!(binary_path.exists());
}

#[test]
#[serial]
fn self_uninstall_preserves_configured_binary_inside_removed_root() {
    let env = TestEnv::new();
    let binary_path = env.data_root.join("bin").join("nodeup-linux-amd64");
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::write(&binary_path, "nodeup").unwrap();
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();

    let output = env
        .command()
        .env("NODEUP_SELF_BIN_PATH", &binary_path)
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall preserves configured binary");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["status"], "removed");
    assert!(payload["removed_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == env.data_root.to_str().unwrap()));
    assert!(payload["likely_leftover_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == binary_path.to_str().unwrap()));
    assert!(binary_path.exists());
    assert!(!env.data_root.join("data-marker.txt").exists());
}

#[cfg(unix)]
#[test]
#[serial]
fn self_uninstall_preserves_configured_binary_symlink_inside_removed_root() {
    let env = TestEnv::new();
    let real_binary_path = env.root.join("bin").join("nodeup-linux-amd64");
    let binary_path = env.data_root.join("bin").join("nodeup");
    fs::create_dir_all(real_binary_path.parent().unwrap()).unwrap();
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::write(&real_binary_path, "nodeup").unwrap();
    std::os::unix::fs::symlink(&real_binary_path, &binary_path).unwrap();
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();

    let output = env
        .command()
        .env("NODEUP_SELF_BIN_PATH", &binary_path)
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall preserves configured binary symlink");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["likely_leftover_paths"]
        .as_array()
        .unwrap()
        .iter()
        .any(|path| path == binary_path.to_str().unwrap()));
    assert!(fs::symlink_metadata(&binary_path).is_ok());
    assert!(!env.data_root.join("data-marker.txt").exists());
}

#[test]
#[serial]
fn self_uninstall_ignores_unowned_default_shim_leftovers() {
    let env = TestEnv::new();
    let shim_dir = env.root.join(".local").join("bin");
    fs::create_dir_all(&shim_dir).unwrap();
    fs::write(shim_dir.join("node"), "unrelated-node").unwrap();

    let output = env
        .command()
        .env("HOME", &env.root)
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall ignores unowned shims");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(!stdout.contains(shim_dir.join("node").to_str().unwrap()));
    assert!(shim_dir.join("node").exists());
}

#[test]
#[serial]
fn self_uninstall_reports_normalized_cleanup_boundary_paths() {
    let env = TestEnv::new();
    let data_root = env.root.join("nodeup-data-relative");
    let cache_root = env.root.join("nodeup-cache-relative");
    let config_root = env.root.join("nodeup-config-relative");
    fs::create_dir_all(&data_root).unwrap();
    fs::create_dir_all(&cache_root).unwrap();
    fs::create_dir_all(&config_root).unwrap();
    fs::write(config_root.join("settings.toml"), "schema_version = 1\n").unwrap();

    let output = Command::new(assert_cmd::cargo::cargo_bin!("nodeup"))
        .current_dir(&env.root)
        .env("NODEUP_DATA_HOME", "nodeup-data-relative")
        .env("NODEUP_CACHE_HOME", "nodeup-cache-relative")
        .env("NODEUP_CONFIG_HOME", "nodeup-config-relative")
        .env("NODEUP_INDEX_URL", &env.index_url)
        .env("NODEUP_DOWNLOAD_BASE_URL", &env.download_base_url)
        .env("NODEUP_FORCE_PLATFORM", "linux-x64")
        .env("NODEUP_LOG_COLOR", "never")
        .env("RUST_LOG", "off")
        .args(["--output", "json", "self", "uninstall"])
        .output()
        .expect("self uninstall relative roots");

    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let config_boundary = payload["cleanup_boundaries"]
        .as_array()
        .unwrap()
        .iter()
        .find(|boundary| boundary["category"] == "config")
        .unwrap();
    assert_eq!(
        config_boundary["paths"][0].as_str().unwrap(),
        config_root.to_str().unwrap()
    );
}

#[test]
#[serial]
fn self_uninstall_refuses_root_containing_running_binary() {
    let env = TestEnv::new();
    let binary_path = env.data_root.join("bin").join("nodeup");
    fs::create_dir_all(binary_path.parent().unwrap()).unwrap();
    fs::copy(assert_cmd::cargo::cargo_bin!("nodeup"), &binary_path).unwrap();
    fs::write(env.data_root.join("data-marker.txt"), "data").unwrap();

    env.command_with_program(&binary_path)
        .args(["self", "uninstall"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to uninstall path containing the running nodeup binary",
        ));

    assert!(binary_path.exists());
    assert!(env.data_root.join("data-marker.txt").exists());
}

#[test]
#[serial]
fn self_uninstall_rejects_non_nodeup_owned_paths() {
    let env = TestEnv::new();
    let unsafe_root = env.root.join("unsafe-home");
    let unsafe_cache = env.root.join("nodeup-cache");
    let unsafe_config = env.root.join("nodeup-config");
    fs::create_dir_all(&unsafe_root).unwrap();
    fs::create_dir_all(&unsafe_cache).unwrap();
    fs::create_dir_all(&unsafe_config).unwrap();
    fs::write(unsafe_root.join("keep.txt"), "do-not-delete").unwrap();

    let mut command = Command::new(assert_cmd::cargo::cargo_bin!("nodeup"));
    command
        .env("NODEUP_DATA_HOME", &unsafe_root)
        .env("NODEUP_CACHE_HOME", &unsafe_cache)
        .env("NODEUP_CONFIG_HOME", &unsafe_config)
        .env("NODEUP_INDEX_URL", &env.index_url)
        .env("NODEUP_DOWNLOAD_BASE_URL", &env.download_base_url)
        .env("NODEUP_FORCE_PLATFORM", "linux-x64");

    command
        .args(["self", "uninstall"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to uninstall non-nodeup-owned path",
        ));

    assert!(unsafe_root.exists());
    assert!(unsafe_root.join("keep.txt").exists());
}

#[test]
#[serial]
fn self_uninstall_validates_all_roots_before_deleting() {
    let env = TestEnv::new();
    let safe_data_root = env.root.join("nodeup-data-safe");
    let safe_cache_root = env.root.join("nodeup-cache-safe");
    let unsafe_config_root = env.root.join("unsafe-config-home");

    fs::create_dir_all(&safe_data_root).unwrap();
    fs::create_dir_all(&safe_cache_root).unwrap();
    fs::create_dir_all(&unsafe_config_root).unwrap();
    fs::write(safe_data_root.join("keep-data.txt"), "keep-data").unwrap();
    fs::write(safe_cache_root.join("keep-cache.txt"), "keep-cache").unwrap();

    let mut command = Command::new(assert_cmd::cargo::cargo_bin!("nodeup"));
    command
        .env("NODEUP_DATA_HOME", &safe_data_root)
        .env("NODEUP_CACHE_HOME", &safe_cache_root)
        .env("NODEUP_CONFIG_HOME", &unsafe_config_root)
        .env("NODEUP_INDEX_URL", &env.index_url)
        .env("NODEUP_DOWNLOAD_BASE_URL", &env.download_base_url)
        .env("NODEUP_FORCE_PLATFORM", "linux-x64");

    command
        .args(["self", "uninstall"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to uninstall non-nodeup-owned path",
        ));

    assert!(safe_data_root.exists());
    assert!(safe_data_root.join("keep-data.txt").exists());
    assert!(safe_cache_root.exists());
    assert!(safe_cache_root.join("keep-cache.txt").exists());
}

#[test]
#[serial]
fn shim_setup_creates_all_aliases_and_reports_path_guidance() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims");

    env.command()
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"created\""))
        .stdout(predicates::str::contains("\"path_active\": false"));

    if cfg!(windows) {
        env.command()
            .args([
                "--output",
                "json",
                "shim",
                "setup",
                "--dir",
                shim_dir.to_str().unwrap(),
            ])
            .assert()
            .success()
            .stdout(predicates::str::contains("$env:Path ="));

        for alias in ["node", "npm", "npx", "yarn", "pnpm"] {
            assert!(shim_dir.join(format!("{alias}.exe")).is_file());
        }
    } else {
        env.command()
            .args([
                "--output",
                "json",
                "shim",
                "setup",
                "--dir",
                shim_dir.to_str().unwrap(),
            ])
            .assert()
            .success()
            .stdout(predicates::str::contains("export PATH="));

        for alias in ["node", "npm", "npx", "yarn", "pnpm"] {
            assert!(fs::symlink_metadata(shim_dir.join(alias)).is_ok());
        }
    }
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_escapes_posix_path_guidance() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-$(touch pwn)'quoted");
    let shim_dir_text = shim_dir.to_str().unwrap();
    let expected = format!(
        "export PATH='{}':\"$PATH\"",
        shim_dir_text.replace('\'', "'\"'\"'")
    );

    env.command()
        .env("PATH", env.root.join("empty-path"))
        .args(["shim", "setup", "--dir", shim_dir_text])
        .assert()
        .success()
        .stdout(predicates::str::contains(expected));
}

#[test]
#[serial]
fn shim_setup_is_idempotent_for_existing_valid_aliases() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-idempotent");

    env.command()
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .success();

    env.command()
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "\"status\": \"already-configured\"",
        ))
        .stdout(predicates::str::contains("\"status\": \"existing\""));
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_repairs_copied_unix_alias_to_symlink() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-copied-alias");
    fs::create_dir_all(&shim_dir).unwrap();
    let copied_alias = shim_dir.join("node");
    fs::copy(assert_cmd::cargo::cargo_bin!("nodeup"), &copied_alias).unwrap();

    env.command()
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"repaired\""))
        .stdout(predicates::str::contains("\"alias\": \"node\""));

    assert!(fs::symlink_metadata(&copied_alias)
        .unwrap()
        .file_type()
        .is_symlink());
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_repairs_stale_symlink_alias() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-repair");
    fs::create_dir_all(&shim_dir).unwrap();
    let stale_target_dir = env.root.join("old-nodeup-bin");
    fs::create_dir_all(&stale_target_dir).unwrap();
    let stale_target = stale_target_dir.join("nodeup");
    fs::write(&stale_target, "old").unwrap();
    std::os::unix::fs::symlink(&stale_target, shim_dir.join("node")).unwrap();

    env.command()
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"repaired\""))
        .stdout(predicates::str::contains("\"alias\": \"node\""));

    let repaired_target = fs::read_link(shim_dir.join("node")).unwrap();
    assert_ne!(repaired_target, stale_target);
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_refuses_unrelated_symlink_alias() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-conflict");
    fs::create_dir_all(&shim_dir).unwrap();
    let existing_target = env.root.join("node");
    fs::write(&existing_target, "existing-node").unwrap();
    std::os::unix::fs::symlink(&existing_target, shim_dir.join("node")).unwrap();

    env.command()
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to replace non-nodeup shim target",
        ));

    assert_eq!(
        fs::read_link(shim_dir.join("node")).unwrap(),
        existing_target
    );
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_preflights_conflicts_before_creating_aliases() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-preflight");
    fs::create_dir_all(&shim_dir).unwrap();
    let existing_target = env.root.join("npm");
    fs::write(&existing_target, "existing-npm").unwrap();
    std::os::unix::fs::symlink(&existing_target, shim_dir.join("npm")).unwrap();

    env.command()
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to replace non-nodeup shim target",
        ));

    assert!(!shim_dir.join("node").exists());
    assert_eq!(
        fs::read_link(shim_dir.join("npm")).unwrap(),
        existing_target
    );
}

#[test]
#[serial]
fn shim_setup_uses_copy_mode_for_windows_hosts() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows");

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"method\": \"copy\""))
        .stdout(predicates::str::contains("$env:Path = "))
        .stdout(predicates::str::contains(" + $env:Path"))
        .stdout(predicates::str::contains("; add ").not());

    for alias in ["node", "npm", "npx", "yarn", "pnpm"] {
        assert!(shim_dir.join(format!("{alias}.exe")).is_file());
    }
}

#[test]
#[serial]
fn shim_setup_refuses_existing_windows_executable() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-conflict");
    fs::create_dir_all(&shim_dir).unwrap();
    let existing_node = shim_dir.join("node.exe");
    fs::write(&existing_node, "existing-node").unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to replace existing shim target with different content",
        ));

    assert_eq!(fs::read_to_string(existing_node).unwrap(), "existing-node");
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_refuses_windows_copy_mode_symlink_alias() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-symlink-conflict");
    fs::create_dir_all(&shim_dir).unwrap();
    let external_target = env.root.join("external-node.exe");
    let node = shim_dir.join("node.exe");
    fs::write(&external_target, "external-node").unwrap();
    std::os::unix::fs::symlink(&external_target, &node).unwrap();
    fs::write(shim_dir.join(".node.exe.nodeup-shim"), "nodeup shim copy\n").unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to replace symlink shim target in copy mode",
        ));

    assert_eq!(
        fs::read_to_string(&external_target).unwrap(),
        "external-node"
    );
    assert_eq!(fs::read_link(node).unwrap(), external_target);
}

#[test]
#[serial]
fn shim_setup_repairs_marked_windows_copy_alias() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-repair");
    let node = shim_dir.join("node.exe");
    let marker = shim_dir.join(".node.exe.nodeup-shim");

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .success();

    assert!(marker.is_file());
    let original = fs::read(&node).unwrap();
    fs::write(&node, "old-nodeup-copy").unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"repaired\""))
        .stdout(predicates::str::contains("\"alias\": \"node\""));

    assert_eq!(fs::read(node).unwrap(), original);
    assert!(marker.is_file());
}

#[test]
#[serial]
#[cfg(unix)]
fn shim_setup_refuses_symlinked_windows_copy_marker() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-marker-symlink");
    let node = shim_dir.join("node.exe");
    let marker = shim_dir.join(".node.exe.nodeup-shim");
    let external_marker_target = env.root.join("external-marker");
    fs::create_dir_all(&shim_dir).unwrap();
    fs::write(&node, "old-nodeup-copy").unwrap();
    fs::write(&external_marker_target, "external-marker").unwrap();
    std::os::unix::fs::symlink(&external_marker_target, &marker).unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to use symlink Windows shim ownership marker",
        ));

    assert_eq!(fs::read_to_string(&node).unwrap(), "old-nodeup-copy");
    assert_eq!(
        fs::read_to_string(&external_marker_target).unwrap(),
        "external-marker"
    );
    assert_eq!(fs::read_link(marker).unwrap(), external_marker_target);
}

#[test]
#[serial]
fn shim_setup_preflights_invalid_windows_copy_marker_before_create() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-marker-dir");
    let node = shim_dir.join("node.exe");
    let marker = shim_dir.join(".node.exe.nodeup-shim");
    fs::create_dir_all(&marker).unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to use non-file Windows shim ownership marker",
        ));

    assert!(!node.exists());
    assert!(marker.is_dir());
}

#[test]
#[serial]
fn shim_setup_backfills_marker_for_existing_windows_copy_alias() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-backfill");
    let node = shim_dir.join("node.exe");
    let marker = shim_dir.join(".node.exe.nodeup-shim");
    fs::create_dir_all(&shim_dir).unwrap();
    fs::copy(assert_cmd::cargo::cargo_bin!("nodeup"), &node).unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args([
            "--output",
            "json",
            "shim",
            "setup",
            "--dir",
            shim_dir.to_str().unwrap(),
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"alias\": \"node\""))
        .stdout(predicates::str::contains("\"status\": \"existing\""));

    assert!(marker.is_file());
}

#[test]
#[serial]
fn shim_setup_preflights_windows_conflicts_before_creating_aliases() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-preflight");
    fs::create_dir_all(&shim_dir).unwrap();
    let existing_npm = shim_dir.join("npm.exe");
    fs::write(&existing_npm, "existing-npm").unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to replace existing shim target with different content",
        ));

    assert!(!shim_dir.join("node.exe").exists());
    assert_eq!(fs::read_to_string(existing_npm).unwrap(), "existing-npm");
}

#[test]
#[serial]
fn shim_setup_refuses_windows_pathext_alias_conflicts() {
    let env = TestEnv::new();
    let shim_dir = env.root.join("nodeup-shims-windows-pathext");
    fs::create_dir_all(&shim_dir).unwrap();
    let existing_npm = shim_dir.join("npm.cmd");
    fs::write(&existing_npm, "existing-npm").unwrap();

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["shim", "setup", "--dir", shim_dir.to_str().unwrap()])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Refusing to create Windows .exe shim because another command name already exists",
        ));

    assert!(!shim_dir.join("node.exe").exists());
    assert!(!shim_dir.join("npm.exe").exists());
    assert_eq!(fs::read_to_string(existing_npm).unwrap(), "existing-npm");
}

#[test]
#[serial]
fn self_upgrade_data_migrates_legacy_schema_files() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    let overrides_file = env.config_root.join("overrides.toml");

    fs::write(
        &settings_file,
        r#"default_selector = "22.1.0"
tracked_selectors = ["22.1.0"]

[linked_runtimes]
local = "/tmp/local-runtime"
"#,
    )
    .unwrap();

    fs::write(
        &overrides_file,
        r#"[[entries]]
path = "/tmp/project"
selector = "22.1.0"
"#,
    )
    .unwrap();

    env.command_with_info_logs()
        .args(["--output", "json", "self", "upgrade-data"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"status\": \"upgraded\""))
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.self.upgrade-data\"",
        ))
        .stdout(predicates::str::contains("action: \"self upgrade-data\""))
        .stdout(predicates::str::contains("outcome: \"upgraded\""))
        .stdout(predicates::str::contains("\"from_schema\": 0"))
        .stdout(predicates::str::contains("\"to_schema\": 1"));

    let settings_content = fs::read_to_string(&settings_file).unwrap();
    let overrides_content = fs::read_to_string(&overrides_file).unwrap();
    assert!(settings_content.contains("schema_version = 1"));
    assert!(overrides_content.contains("schema_version = 1"));

    env.command().args(["override", "list"]).assert().success();
}

#[test]
#[serial]
fn self_upgrade_data_reports_field_type_context_for_invalid_settings() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"default_selector = 123
tracked_selectors = []
"#,
    )
    .unwrap();

    let output = env
        .command()
        .args(["self", "upgrade-data"])
        .output()
        .expect("self upgrade-data invalid settings type");

    assert_eq!(output.status.code(), Some(2));
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Expected 'default_selector' to be a string"));
    assert!(stderr.contains("actual_type=integer"));
    assert!(stderr.contains(&format!("file={}", settings_file.display())));
}

#[test]
#[serial]
fn update_without_candidates_includes_selector_source_context() {
    let env = TestEnv::new();

    env.command()
        .args(["update"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "No runtimes are eligible for update",
        ))
        .stderr(predicates::str::contains(
            "selector_source=installed-runtimes",
        ))
        .stderr(predicates::str::contains("installed_runtimes=0"))
        .stderr(predicates::str::contains("resolved_selectors=0"));
}

#[test]
#[serial]
fn completions_logs_action_and_outcome() {
    let env = TestEnv::new();

    env.command_with_info_logs()
        .args(["completions", "zsh"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.completions\"",
        ))
        .stdout(predicates::str::contains("action: \"generate\""))
        .stdout(predicates::str::contains("outcome: \"generated\""));
}

#[cfg(unix)]
#[test]
#[serial]
fn run_logs_exit_code_and_signal_details() {
    use std::os::unix::fs::PermissionsExt;

    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-logs");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();

    let delegated = runtime_bin.join("node");
    fs::write(&delegated, "#!/bin/sh\necho delegated-log\nexit 7\n").unwrap();
    let mut permissions = fs::metadata(&delegated).unwrap().permissions();
    permissions.set_mode(0o755);
    fs::set_permissions(&delegated, permissions).unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-logs",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command_with_info_logs()
        .args(["run", "linked-logs", "node"])
        .assert()
        .code(7)
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.run.process\"",
        ))
        .stdout(predicates::str::contains("exit_code: 7"))
        .stdout(predicates::str::contains("signal: None"));
}

#[cfg(unix)]
#[test]
#[serial]
fn run_maps_signal_termination_to_standard_exit_code() {
    use std::os::unix::fs::PermissionsExt;

    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-signal-exit");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();

    let delegated = runtime_bin.join("node");
    fs::write(&delegated, "#!/bin/sh\nkill -TERM $$\n").unwrap();
    let mut permissions = fs::metadata(&delegated).unwrap().permissions();
    permissions.set_mode(0o755);
    fs::set_permissions(&delegated, permissions).unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-signal-exit",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command_with_info_logs()
        .args(["run", "linked-signal-exit", "node"])
        .assert()
        .code(143)
        .stdout(predicates::str::contains(
            "Delegated command 'node' exited with status 143",
        ))
        .stdout(predicates::str::contains(
            "command_path: \"nodeup.run.process\"",
        ))
        .stdout(predicates::str::contains("exit_code: 143"))
        .stdout(predicates::str::contains("signal: Some(15)"));
}

#[test]
#[serial]
fn run_with_install_executes_command() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho node-run\n"),
                ("npm", "#!/bin/sh\necho npm-run\n"),
            ],
        ),
        None,
    );

    env.command()
        .args(["run", "--install", "22.1.0", "node"])
        .assert()
        .success()
        .stdout(predicates::str::contains("node-run"));
}

#[cfg(unix)]
#[test]
#[serial]
fn run_json_output_is_machine_parseable_with_delegated_output() {
    use std::os::unix::fs::PermissionsExt;

    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-json-streams");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();

    let delegated = runtime_bin.join("node");
    fs::write(
        &delegated,
        "#!/bin/sh\necho delegated-out\necho delegated-err >&2\nexit 9\n",
    )
    .unwrap();
    let mut permissions = fs::metadata(&delegated).unwrap().permissions();
    permissions.set_mode(0o755);
    fs::set_permissions(&delegated, permissions).unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-json-streams",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .args(["--output", "json", "run", "linked-json-streams", "node"])
        .output()
        .expect("run --output json with delegated output");

    assert_eq!(output.status.code(), Some(9));

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["runtime"], "linked-json-streams");
    assert_eq!(payload["command"], "node");
    assert_eq!(payload["exit_code"], 9);

    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!stdout.contains("delegated-out"));
    assert!(stderr.contains("delegated-out"));
    assert!(stderr.contains("delegated-err"));
}

#[test]
#[serial]
fn shim_dispatch_uses_argv0_alias() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho shim-ok\n")],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("node");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .output()
        .expect("run shim binary");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("shim-ok"));
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_rejects_linked_runtime_node_without_executable_bit() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-shim-not-executable");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();

    let delegated = runtime_bin.join("node");
    write_runtime_executable(&delegated, "#!/bin/sh\necho should-not-run\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-shim-not-executable",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    let mut permissions = fs::metadata(&delegated).unwrap().permissions();
    permissions.set_mode(0o644);
    fs::set_permissions(&delegated, permissions).unwrap();

    env.command()
        .args(["default", "linked-shim-not-executable"])
        .assert()
        .success();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("node");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .output()
        .expect("run shim binary with non-executable linked node");

    assert_eq!(output.status.code(), Some(5));
    assert!(output.stdout.is_empty());

    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Managed alias 'node' exists but is not runnable"));
    assert!(stderr.contains("executable bit is set"));
    assert!(!stderr.contains("should-not-run"));
}

#[test]
#[serial]
fn shim_dispatch_default_logging_suppresses_info_logs_without_rust_log_env() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho shim-ok\n")],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("node");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .env_remove("RUST_LOG")
        .output()
        .expect("run shim binary without rust log env");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stdout.contains("shim-ok"));
    assert!(!stdout.contains("command_path:"));
    assert!(!stderr.contains("command_path:"));
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_maps_signal_termination_to_standard_exit_code() {
    use std::os::unix::fs::PermissionsExt;

    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-shim-signal");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();

    let delegated = runtime_bin.join("node");
    fs::write(&delegated, "#!/bin/sh\nkill -TERM $$\n").unwrap();
    let mut permissions = fs::metadata(&delegated).unwrap().permissions();
    permissions.set_mode(0o755);
    fs::set_permissions(&delegated, permissions).unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-shim-signal",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["default", "linked-shim-signal"])
        .assert()
        .success();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("node");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .output()
        .expect("run shim binary with signal termination");

    assert_eq!(output.status.code(), Some(143));
}

#[test]
#[serial]
fn check_and_update_detect_newer_version() {
    let env = TestEnv::new();
    env.register_index(&[("22.2.0", Some("Jod")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );
    env.register_release(
        "22.2.0",
        make_archive("22.2.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.2\n")]),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command()
        .args(["check", "--output", "json"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"has_update\": true"));

    env.command()
        .args(["--output", "json", "update", "22.1.0"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "\"status\": \"skipped-exact-version\"",
        ));

    env.command()
        .args(["toolchain", "list", "--quiet"])
        .assert()
        .success()
        .stdout(predicates::str::contains("v22.1.0"))
        .stdout(predicates::str::contains("v22.2.0").not());
}

#[test]
#[serial]
fn update_reports_skipped_exact_version_when_latest_is_already_installed() {
    let env = TestEnv::new();
    env.register_index(&[("22.2.0", Some("Jod")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );
    env.register_release(
        "22.2.0",
        make_archive("22.2.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.2\n")]),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    env.command().args(["update", "22.1.0"]).assert().success();

    env.command()
        .args(["--output", "json", "update", "22.1.0"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "\"status\": \"skipped-exact-version\"",
        ));
}

#[test]
#[serial]
fn override_set_rejects_invalid_selector() {
    let env = TestEnv::new();
    let project_dir = env.root.join("project-invalid-override");
    fs::create_dir_all(&project_dir).unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["override", "set", "22.x"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("Invalid runtime selector"));
}

#[test]
#[serial]
fn missing_runtime_without_install_flag_fails() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);

    env.command()
        .args(["run", "22.1.0", "node"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("--install"));
}

#[test]
#[serial]
fn checksum_mismatch_fails_install() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);

    let archive_name = "node-v22.1.0-linux-x64.tar.xz".to_string();
    let mut shasums_override = HashMap::new();
    shasums_override.insert(archive_name, "deadbeef".to_string());

    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho bad\n")]),
        Some(shasums_override),
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("Checksum mismatch"));
}

#[test]
#[serial]
fn unsupported_platform_is_reported() {
    let env = TestEnv::new();

    let mut cmd = env.command();
    cmd.env("NODEUP_FORCE_PLATFORM", "windows-x86")
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .code(3)
        .failure()
        .stderr(predicates::str::contains("Unsupported host platform"))
        .stderr(predicates::str::contains("Windows x64"))
        .stderr(predicates::str::contains("x86 hosts are unsupported"))
        .stderr(predicates::str::contains(
            "Use an x64/arm64 host or a supported CI image",
        ));
}

#[test]
#[serial]
fn unsupported_platform_json_includes_deterministic_diagnostics() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NODEUP_FORCE_PLATFORM", "linux-x86")
        .args(["--output", "json", "toolchain", "install", "22.1.0"])
        .output()
        .expect("unsupported platform json error");

    assert_eq!(output.status.code(), Some(3));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "unsupported-platform");
    assert_eq!(payload["exit_code"], 3);
    assert_eq!(payload["diagnostics"]["os"], "linux");
    assert_eq!(payload["diagnostics"]["architecture"], "x86");
    assert_eq!(
        payload["diagnostics"]["platform_source"],
        "NODEUP_FORCE_PLATFORM"
    );
    assert_eq!(payload["diagnostics"]["forced_platform"], "linux-x86");
    assert_eq!(
        payload["diagnostics"]["supported_platforms"],
        serde_json::json!([
            "macos/x64",
            "macos/arm64",
            "linux/x64",
            "linux/arm64",
            "windows/x64",
            "windows/arm64"
        ])
    );
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_rejects_unsupported_x86_before_resolution() {
    let env = TestEnv::new();
    fs::write(
        env.config_root.join("settings.toml"),
        r#"schema_version = 1
default_selector = "22.1.0"
tracked_selectors = []

[linked_runtimes]
"#,
    )
    .unwrap();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("node");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .env("NODEUP_FORCE_PLATFORM", "windows-x86")
        .output()
        .expect("run shim binary on unsupported x86 platform");

    assert_eq!(output.status.code(), Some(3));
    assert!(output.stdout.is_empty());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("Unsupported host platform for shim dispatch"));
    assert!(stderr.contains("forced_platform=windows-x86"));
    assert!(stderr.contains("x86 hosts are unsupported"));
}

#[test]
#[serial]
fn linux_arm64_platform_installs_from_tar_xz_archive() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release_for_target(
        "22.1.0",
        "linux-arm64",
        make_archive(
            "22.1.0",
            "linux-arm64",
            &[("node", "#!/bin/sh\necho arm64\n")],
        ),
        None,
    );

    let runtime_root = env.data_root.join("toolchains").join("v22.1.0");

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "linux-arm64")
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    assert!(runtime_root.join("bin").join("node").exists());
}

#[test]
#[serial]
fn windows_x64_platform_installs_from_zip_archive() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release_for_target(
        "22.1.0",
        "win-x64",
        make_windows_zip(&[("node.exe", "node"), ("npm.cmd", "@echo off\r\n")]),
        None,
    );

    let runtime_root = env.data_root.join("toolchains").join("v22.1.0");

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    assert!(runtime_root.join("bin").join("node.exe").exists());
    assert!(runtime_root.join("bin").join("npm.cmd").exists());
}

#[test]
#[serial]
fn windows_arm64_platform_installs_from_zip_archive() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release_for_target(
        "22.1.0",
        "win-arm64",
        make_windows_zip(&[("node.exe", "node"), ("npm.cmd", "@echo off\r\n")]),
        None,
    );

    let runtime_root = env.data_root.join("toolchains").join("v22.1.0");

    env.command()
        .env("NODEUP_FORCE_PLATFORM", "windows-arm64")
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    assert!(runtime_root.join("bin").join("node.exe").exists());
}

#[test]
#[serial]
fn install_lock_contention_is_reported() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho lock\n")]),
        None,
    );

    let lock_dir = env.data_root.join("toolchains");
    fs::create_dir_all(&lock_dir).unwrap();
    let lock_file = lock_dir.join(".v22.1.0.install.lock");
    fs::write(&lock_file, "busy").unwrap();

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "Another install is already running",
        ));
}

#[test]
#[serial]
fn which_resolves_command_path_from_default_selector() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-which-default");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho which-default\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-default",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["default", "linked-which-default"])
        .assert()
        .success();

    let output = env
        .command()
        .args(["which", "node"])
        .output()
        .expect("which node using default linked runtime");

    assert!(output.status.success());
    let expected = fs::canonicalize(&runtime_dir)
        .unwrap()
        .join("bin")
        .join("node");
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert_eq!(stdout.trim(), expected.to_string_lossy());
}

#[test]
#[serial]
fn which_json_reports_stale_release_index_cache_fallback_for_channel_selector() {
    let env = TestEnv::new();
    let runtime_bin = env
        .data_root
        .join("toolchains")
        .join("v22.11.0")
        .join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho stale-cache\n");
    fs::write(
        env.cache_root.join("release-index.json"),
        serde_json::json!({
            "schema_version": 1,
            "index_url": env.index_url,
            "fetched_at_epoch_seconds": 1,
            "entries": [
                { "version": "v22.11.0", "lts": "Jod" }
            ]
        })
        .to_string(),
    )
    .unwrap();

    let index_mock = env.server.mock(|when, then| {
        when.method(GET).path("/download/release/index.json");
        then.status(500);
    });

    let output = env
        .command()
        .args(["--output", "json", "which", "--runtime", "lts", "node"])
        .output()
        .expect("which --runtime lts with stale release index fallback");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["runtime"], "v22.11.0");
    assert_eq!(payload["release_index"]["cache_state"], "stale-fallback");
    assert_eq!(
        payload["release_index"]["fallback_reason"],
        "refresh-failed"
    );
    assert_eq!(payload["release_index"]["selector"], "lts");
    assert_eq!(payload["release_index"]["selected_version"], "v22.11.0");
    assert!(payload["release_index"]["cache_age_seconds"]
        .as_u64()
        .is_some_and(|age| age > 600));
    assert_eq!(payload["release_index"]["ttl_seconds"], 600);
    assert!(payload["release_index"]["source_url"]
        .as_str()
        .is_some_and(|url| !url.contains('?') && !url.contains('#')));
    index_mock.assert_calls(3);
}

#[test]
#[serial]
fn which_human_output_stays_path_only_with_stale_release_index_cache_fallback() {
    let env = TestEnv::new();
    let runtime_bin = env
        .data_root
        .join("toolchains")
        .join("v22.11.0")
        .join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho stale-cache\n");
    fs::write(
        env.cache_root.join("release-index.json"),
        serde_json::json!({
            "schema_version": 1,
            "index_url": env.index_url,
            "fetched_at_epoch_seconds": 1,
            "entries": [
                { "version": "v22.11.0", "lts": "Jod" }
            ]
        })
        .to_string(),
    )
    .unwrap();

    let index_mock = env.server.mock(|when, then| {
        when.method(GET).path("/download/release/index.json");
        then.status(500);
    });

    let output = env
        .command()
        .args(["which", "--runtime", "lts", "node"])
        .output()
        .expect("which --runtime lts with stale release index fallback");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let expected = env
        .data_root
        .join("toolchains")
        .join("v22.11.0")
        .join("bin")
        .join("node");
    assert_eq!(
        String::from_utf8_lossy(&output.stdout),
        format!("{}\n", expected.display())
    );
    index_mock.assert_calls(3);
}

#[test]
#[serial]
fn which_explicit_runtime_takes_precedence_over_override_and_default() {
    let env = TestEnv::new();
    let default_runtime = env.root.join("linked-runtime-which-default-priority");
    let explicit_runtime = env.root.join("linked-runtime-which-explicit-priority");

    for (runtime_dir, marker) in [
        (&default_runtime, "default-priority"),
        (&explicit_runtime, "explicit-priority"),
    ] {
        let runtime_bin = runtime_dir.join("bin");
        fs::create_dir_all(&runtime_bin).unwrap();
        write_runtime_executable(
            runtime_bin.join("node"),
            &format!("#!/bin/sh\necho {marker}\n"),
        );
    }

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-default-priority",
            default_runtime.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-explicit-priority",
            explicit_runtime.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args(["default", "linked-which-default-priority"])
        .assert()
        .success();

    let project_dir = env.root.join("project-which-explicit-priority");
    fs::create_dir_all(&project_dir).unwrap();
    env.command()
        .args([
            "override",
            "set",
            "linked-which-default-priority",
            "--path",
            project_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args([
            "which",
            "--runtime",
            "linked-which-explicit-priority",
            "node",
        ])
        .output()
        .expect("which --runtime should prefer explicit selector");
    assert!(output.status.success());

    let expected = fs::canonicalize(&explicit_runtime)
        .unwrap()
        .join("bin")
        .join("node");
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert_eq!(stdout.trim(), expected.to_string_lossy());
}

#[test]
#[serial]
fn which_fails_when_runtime_is_not_installed() {
    let env = TestEnv::new();

    env.command()
        .args(["which", "--runtime", "22.1.0", "node"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Runtime v22.1.0 is not installed",
        ));
}

#[test]
#[serial]
fn which_fails_when_command_is_missing_for_runtime() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-which-missing-command");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho only-node\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-missing-command",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args(["default", "linked-which-missing-command"])
        .assert()
        .success();

    env.command()
        .args(["which", "npm"])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Command 'npm' does not exist for runtime linked-which-missing-command",
        ));
}

#[test]
#[serial]
fn json_which_failure_emits_stderr_error_envelope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "which", "node"])
        .output()
        .expect("which --output json failure");
    assert_eq!(output.status.code(), Some(5));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "not-found");
    assert_eq!(payload["exit_code"], 5);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("No runtime selector resolved"));
    assert!(payload["message"].as_str().unwrap().contains("Hint:"));
}

#[test]
#[serial]
fn override_list_json_includes_configured_entries() {
    let env = TestEnv::new();
    let project_a = env.root.join("project-override-list-a");
    let project_b = env.root.join("project-override-list-b");
    fs::create_dir_all(&project_a).unwrap();
    fs::create_dir_all(&project_b).unwrap();

    env.command()
        .args([
            "override",
            "set",
            "22.1.0",
            "--path",
            project_a.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args([
            "override",
            "set",
            "latest",
            "--path",
            project_b.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .args(["--output", "json", "override", "list"])
        .output()
        .expect("override list --output json");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("override list JSON array");
    assert_eq!(entries.len(), 2);

    let canonical_a = fs::canonicalize(&project_a)
        .unwrap()
        .to_string_lossy()
        .to_string();
    let canonical_b = fs::canonicalize(&project_b)
        .unwrap()
        .to_string_lossy()
        .to_string();
    assert!(entries.iter().any(|entry| {
        entry["path"] == canonical_a
            && entry["selector"] == "v22.1.0"
            && entry["selector_kind"] == "exact-version"
            && entry["canonical_selector"] == "v22.1.0"
            && entry.get("selector_alias_of").is_none()
    }));
    assert!(entries.iter().any(|entry| {
        entry["path"] == canonical_b
            && entry["selector"] == "latest"
            && entry["selector_kind"] == "channel"
            && entry["canonical_selector"] == "current"
            && entry["selector_alias_of"] == "current"
    }));
}

#[test]
#[serial]
fn override_unset_path_removes_only_target_entry() {
    let env = TestEnv::new();
    let project_a = env.root.join("project-override-unset-path-a");
    let project_b = env.root.join("project-override-unset-path-b");
    fs::create_dir_all(&project_a).unwrap();
    fs::create_dir_all(&project_b).unwrap();

    env.command()
        .args([
            "override",
            "set",
            "22.1.0",
            "--path",
            project_a.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args([
            "override",
            "set",
            "24.0.0",
            "--path",
            project_b.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["override", "unset", "--path", project_a.to_str().unwrap()])
        .assert()
        .success()
        .stdout(predicates::str::contains("Removed 1 override(s)"));

    let output = env
        .command()
        .args(["--output", "json", "override", "list"])
        .output()
        .expect("override list after path-scoped unset");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("override list JSON array");
    assert_eq!(entries.len(), 1);
    let canonical_b = fs::canonicalize(&project_b)
        .unwrap()
        .to_string_lossy()
        .to_string();
    assert_eq!(entries[0]["path"], canonical_b);
    assert_eq!(entries[0]["selector"], "v24.0.0");
}

#[test]
#[serial]
fn override_unset_without_path_uses_current_directory() {
    let env = TestEnv::new();
    let project_a = env.root.join("project-override-unset-cwd-a");
    let project_b = env.root.join("project-override-unset-cwd-b");
    fs::create_dir_all(&project_a).unwrap();
    fs::create_dir_all(&project_b).unwrap();

    env.command()
        .args([
            "override",
            "set",
            "22.1.0",
            "--path",
            project_a.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args([
            "override",
            "set",
            "24.0.0",
            "--path",
            project_b.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .current_dir(&project_a)
        .args(["override", "unset"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Removed 1 override(s)"));

    let output = env
        .command()
        .args(["--output", "json", "override", "list"])
        .output()
        .expect("override list after cwd-scoped unset");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("override list JSON array");
    assert_eq!(entries.len(), 1);
    let canonical_b = fs::canonicalize(&project_b)
        .unwrap()
        .to_string_lossy()
        .to_string();
    assert_eq!(entries[0]["path"], canonical_b);
    assert_eq!(entries[0]["selector"], "v24.0.0");
}

#[test]
#[serial]
fn override_unset_nonexistent_removes_only_stale_entries() {
    let env = TestEnv::new();
    let live_project = env.root.join("project-override-live");
    let stale_project = env.root.join("project-override-stale");
    fs::create_dir_all(&live_project).unwrap();
    fs::create_dir_all(&stale_project).unwrap();

    env.command()
        .args([
            "override",
            "set",
            "22.1.0",
            "--path",
            live_project.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args([
            "override",
            "set",
            "24.0.0",
            "--path",
            stale_project.to_str().unwrap(),
        ])
        .assert()
        .success();

    fs::remove_dir_all(&stale_project).unwrap();

    env.command()
        .args(["override", "unset", "--nonexistent"])
        .assert()
        .success()
        .stdout(predicates::str::contains("Removed 1 override(s)"));

    let output = env
        .command()
        .args(["--output", "json", "override", "list"])
        .output()
        .expect("override list after nonexistent cleanup");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("override list JSON array");
    assert_eq!(entries.len(), 1);
    let canonical_live = fs::canonicalize(&live_project)
        .unwrap()
        .to_string_lossy()
        .to_string();
    assert_eq!(entries[0]["path"], canonical_live);
    assert_eq!(entries[0]["selector"], "v22.1.0");
}

#[test]
#[serial]
fn json_override_unset_output_is_machine_parseable() {
    let env = TestEnv::new();
    let project = env.root.join("project-override-unset-json");
    fs::create_dir_all(&project).unwrap();

    env.command()
        .args([
            "override",
            "set",
            "latest",
            "--path",
            project.to_str().unwrap(),
        ])
        .assert()
        .success();

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "override",
            "unset",
            "--path",
            project.to_str().unwrap(),
        ])
        .output()
        .expect("override unset --output json");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("override unset JSON array");
    assert_eq!(entries.len(), 1);
    let canonical = fs::canonicalize(&project)
        .unwrap()
        .to_string_lossy()
        .to_string();
    assert_eq!(entries[0]["path"], canonical);
    assert_eq!(entries[0]["selector"], "latest");
    assert_eq!(entries[0]["selector_kind"], "channel");
    assert_eq!(entries[0]["canonical_selector"], "current");
    assert_eq!(entries[0]["selector_alias_of"], "current");
}

#[test]
#[serial]
fn json_override_unset_output_handles_legacy_reserved_case_linked_name() {
    let env = TestEnv::new();
    let project = env.root.join("legacy-reserved-case-unset-json");
    fs::create_dir_all(&project).unwrap();
    fs::write(
        env.config_root.join("overrides.toml"),
        format!(
            "schema_version = 1\n\n[[entries]]\npath = \"{}\"\nselector = \"LATEST\"\n",
            project.display()
        ),
    )
    .unwrap();

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "override",
            "unset",
            "--path",
            project.to_str().unwrap(),
        ])
        .output()
        .expect("override unset legacy reserved-case linked name");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("override unset JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "LATEST");
    assert_eq!(entries[0]["selector_kind"], "linked-runtime");
    assert_eq!(entries[0]["canonical_selector"], "LATEST");
    assert!(entries[0].get("selector_alias_of").is_none());

    let output = env
        .command()
        .args(["--output", "json", "override", "list"])
        .output()
        .expect("override list after legacy unset");
    assert!(output.status.success());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload.as_array().unwrap().is_empty());
}

#[test]
#[serial]
fn toolchain_install_requires_at_least_one_runtime_selector() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "install"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "required arguments were not provided",
        ))
        .stderr(predicates::str::contains(
            "Usage: nodeup toolchain install <RUNTIMES>...",
        ));
}

#[test]
#[serial]
fn toolchain_install_rejects_linked_runtime_selector() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-install-reject");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-install-reject",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    env.command()
        .args(["toolchain", "install", "linked-install-reject"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "`toolchain install` only supports semantic version or channel selectors",
        ));
}

#[test]
#[serial]
fn toolchain_install_rejects_missing_linked_runtime_selector_before_lookup() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "install", "ghost-linked"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "`toolchain install` only supports semantic version or channel selectors",
        ))
        .stderr(predicates::str::contains("Linked runtime 'ghost-linked'").not());
}

#[test]
#[serial]
fn toolchain_uninstall_requires_at_least_one_runtime_selector() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "uninstall"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "required arguments were not provided",
        ))
        .stderr(predicates::str::contains(
            "Usage: nodeup toolchain uninstall <RUNTIMES>...",
        ));
}

#[test]
#[serial]
fn toolchain_uninstall_linked_runtime_selector_points_to_unlink() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "uninstall", "linked-runtime"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "`toolchain uninstall` only supports exact version selectors",
        ))
        .stderr(predicates::str::contains("nodeup toolchain unlink <name>"));
}

#[test]
#[serial]
fn toolchain_uninstall_channel_selector_rejection_stays_distinct_from_reference_blockers() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "toolchain", "uninstall", "lts"])
        .output()
        .expect("uninstall channel selector");

    assert_eq!(output.status.code(), Some(2));
    assert!(output.stdout.is_empty());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert!(payload["diagnostics"].is_null());
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("only supports exact version selectors"));
}

#[test]
#[serial]
fn toolchain_link_missing_path_returns_not_found() {
    let env = TestEnv::new();
    let missing = env.root.join("missing-linked-runtime");

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-missing-path",
            missing.to_str().unwrap(),
        ])
        .assert()
        .failure()
        .code(5)
        .stderr(predicates::str::contains(
            "Linked runtime path does not exist",
        ));
}

#[test]
#[serial]
fn json_toolchain_link_missing_path_failure_emits_not_found_error_envelope() {
    let env = TestEnv::new();
    let missing = env.root.join("missing-linked-runtime-json");

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "toolchain",
            "link",
            "linked-missing-path-json",
            missing.to_str().unwrap(),
        ])
        .output()
        .expect("toolchain link missing path --output json");

    assert_eq!(output.status.code(), Some(5));
    assert!(output.stdout.is_empty());
    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "not-found");
    assert_eq!(payload["exit_code"], 5);
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Linked runtime path does not exist"));
}

#[test]
#[serial]
fn update_without_selectors_prefers_tracked_selectors_over_installed_versions() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );
    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
tracked_selectors = ["linked-update-priority"]

[linked_runtimes]
"linked-update-priority" = "/tmp/linked-update-priority"
"#,
    )
    .unwrap();

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update without selectors should use tracked selectors");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "linked-update-priority");
    assert_eq!(entries[0]["status"], "skipped-linked-runtime");
}

#[test]
#[serial]
fn update_without_selectors_skips_legacy_reserved_case_tracked_link() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("legacy-reserved-case-update");
    fs::create_dir_all(&runtime_dir).unwrap();
    fs::write(
        env.config_root.join("settings.toml"),
        format!(
            "schema_version = 1\ntracked_selectors = [\"LATEST\"]\n\n[linked_runtimes]\nLATEST = \
             \"{}\"\n",
            runtime_dir.display()
        ),
    )
    .unwrap();

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update with legacy reserved-case tracked link");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "LATEST");
    assert_eq!(entries[0]["selector_kind"], "linked-runtime");
    assert_eq!(entries[0]["canonical_selector"], "LATEST");
    assert_eq!(entries[0]["status"], "skipped-linked-runtime");
}

#[test]
#[serial]
fn update_linked_selector_reports_skipped_status() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "update", "linked-update-explicit"])
        .output()
        .expect("update linked selector");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "linked-update-explicit");
    assert_eq!(entries[0]["selector_kind"], "linked-runtime");
    assert_eq!(entries[0]["canonical_selector"], "linked-update-explicit");
    assert_eq!(entries[0]["status"], "skipped-linked-runtime");
    assert!(entries[0]["previous_runtime"].is_null());
    assert!(entries[0]["updated_runtime"].is_null());
}

#[test]
#[serial]
fn update_channel_selector_reports_updated_status() {
    let env = TestEnv::new();
    env.register_index(&[("22.2.0", Some("Jod")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );
    env.register_release(
        "22.2.0",
        make_archive("22.2.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.2\n")]),
        None,
    );

    let output = env
        .command()
        .args(["--output", "json", "update", "lts"])
        .output()
        .expect("update lts selector");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "lts");
    assert_eq!(entries[0]["selector_kind"], "channel");
    assert_eq!(entries[0]["canonical_selector"], "lts");
    assert_eq!(entries[0]["status"], "updated");
    assert_eq!(entries[0]["updated_runtime"], "v22.2.0");
}

#[test]
#[serial]
fn current_and_latest_resolve_as_aliases_and_report_canonical_selector() {
    let env = TestEnv::new();
    env.register_index(&[("24.0.0", None), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "24.0.0",
        make_archive("24.0.0", "linux-x64", &[("node", "#!/bin/sh\necho 24\n")]),
        None,
    );

    for selector in ["current", "latest"] {
        let output = env
            .command()
            .args(["--output", "json", "update", selector])
            .output()
            .expect("update current/latest selector");
        assert!(output.status.success());

        let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
        let entries = payload.as_array().expect("update JSON array");
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0]["selector"], selector);
        assert_eq!(entries[0]["selector_kind"], "channel");
        assert_eq!(entries[0]["canonical_selector"], "current");
        if selector == "latest" {
            assert_eq!(entries[0]["selector_alias_of"], "current");
        } else {
            assert!(entries[0].get("selector_alias_of").is_none());
        }
        assert_eq!(entries[0]["updated_runtime"], "v24.0.0");
    }

    env.command()
        .args(["--output", "json", "default", "latest"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "\"default_selector\": \"latest\"",
        ))
        .stdout(predicates::str::contains("\"selector_kind\": \"channel\""))
        .stdout(predicates::str::contains(
            "\"canonical_selector\": \"current\"",
        ))
        .stdout(predicates::str::contains(
            "\"selector_alias_of\": \"current\"",
        ));

    env.command()
        .args(["--output", "json", "show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"runtime\": \"v24.0.0\""))
        .stdout(predicates::str::contains("\"selector\": \"latest\""))
        .stdout(predicates::str::contains("\"selector_kind\": \"channel\""))
        .stdout(predicates::str::contains(
            "\"canonical_selector\": \"current\"",
        ))
        .stdout(predicates::str::contains(
            "\"selector_alias_of\": \"current\"",
        ));
}

#[test]
#[serial]
fn tracked_current_and_latest_are_canonicalized_to_one_channel_selector() {
    let env = TestEnv::new();
    env.register_index(&[("24.0.0", None)]);
    env.register_release(
        "24.0.0",
        make_archive("24.0.0", "linux-x64", &[("node", "#!/bin/sh\necho 24\n")]),
        None,
    );

    env.command().args(["default", "latest"]).assert().success();
    env.command()
        .args(["default", "current"])
        .assert()
        .success();

    assert_eq!(
        tracked_selectors_from_settings(&env.config_root.join("settings.toml")),
        vec!["current"]
    );

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update canonicalized current/latest tracked selector");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "current");
    assert_eq!(entries[0]["selector_kind"], "channel");
    assert_eq!(entries[0]["canonical_selector"], "current");
}

#[test]
#[serial]
fn tracked_exact_selectors_are_canonicalized_across_install_and_override() {
    let env = TestEnv::new();
    env.register_index(&[("24.0.0", Some("Krypton")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let project_dir = env.root.join("dedupe-install-override");
    fs::create_dir_all(&project_dir).unwrap();
    env.command()
        .args([
            "override",
            "set",
            "22.1.0",
            "--path",
            project_dir.to_str().unwrap(),
        ])
        .assert()
        .success();

    assert_eq!(
        tracked_selectors_from_settings(&env.config_root.join("settings.toml")),
        vec!["v22.1.0"]
    );

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update deduplicated exact selectors");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "v22.1.0");
    assert_eq!(entries[0]["selector_kind"], "exact-version");
    assert_eq!(entries[0]["canonical_selector"], "v22.1.0");
    assert_eq!(entries[0]["status"], "skipped-exact-version");
    assert_eq!(entries[0]["previous_runtime"], "v22.1.0");
    assert_eq!(entries[0]["updated_runtime"], "v22.1.0");
}

#[test]
#[serial]
fn tracked_exact_selectors_are_canonicalized_across_install_and_default() {
    let env = TestEnv::new();
    env.register_index(&[("24.0.0", Some("Krypton")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );

    env.command()
        .args(["toolchain", "install", "v22.1.0"])
        .assert()
        .success();
    env.command().args(["default", "22.1.0"]).assert().success();

    assert_eq!(
        tracked_selectors_from_settings(&env.config_root.join("settings.toml")),
        vec!["v22.1.0"]
    );

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update deduplicated default exact selector");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "v22.1.0");
    assert_eq!(entries[0]["status"], "skipped-exact-version");

    env.command()
        .args(["--output", "json", "show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"runtime\": \"v22.1.0\""));
}

#[test]
#[serial]
fn update_deduplicates_existing_semantic_exact_selectors() {
    let env = TestEnv::new();
    let settings_file = env.config_root.join("settings.toml");
    fs::write(
        &settings_file,
        r#"schema_version = 1
tracked_selectors = ["22.1.0", "v22.1.0"]

[linked_runtimes]
"#,
    )
    .unwrap();

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update existing duplicate exact selectors");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "v22.1.0");
    assert_eq!(entries[0]["status"], "skipped-exact-version");
    assert_eq!(entries[0]["previous_runtime"], "v22.1.0");
    assert_eq!(entries[0]["updated_runtime"], "v22.1.0");
}

#[test]
#[serial]
fn update_exact_default_reports_noop_and_keeps_resolution_pinned() {
    let env = TestEnv::new();
    env.register_index(&[("24.0.0", Some("Krypton")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );
    env.register_release(
        "24.0.0",
        make_archive("24.0.0", "linux-x64", &[("node", "#!/bin/sh\necho 24\n")]),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let output = env
        .command()
        .args(["--output", "json", "update"])
        .output()
        .expect("update exact default");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "v22.1.0");
    assert_eq!(entries[0]["status"], "skipped-exact-version");
    assert_eq!(entries[0]["previous_runtime"], "v22.1.0");
    assert_eq!(entries[0]["updated_runtime"], "v22.1.0");

    env.command()
        .args(["--output", "json", "show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"runtime\": \"v22.1.0\""));

    env.command()
        .args(["toolchain", "list", "--quiet"])
        .assert()
        .success()
        .stdout(predicates::str::contains("v22.1.0"))
        .stdout(predicates::str::contains("v24.0.0").not());
}

#[test]
#[serial]
fn update_exact_override_reports_noop_and_keeps_resolution_pinned() {
    let env = TestEnv::new();
    env.register_index(&[("24.0.0", Some("Krypton")), ("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );
    env.register_release(
        "24.0.0",
        make_archive("24.0.0", "linux-x64", &[("node", "#!/bin/sh\necho 24\n")]),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();
    let project_dir = env.root.join("exact-override-noop");
    fs::create_dir_all(&project_dir).unwrap();
    env.command()
        .current_dir(&project_dir)
        .args(["override", "set", "22.1.0"])
        .assert()
        .success();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args(["--output", "json", "update"])
        .output()
        .expect("update exact override");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("update JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["selector"], "v22.1.0");
    assert_eq!(entries[0]["status"], "skipped-exact-version");

    env.command()
        .current_dir(&project_dir)
        .args(["--output", "json", "show", "active-runtime"])
        .assert()
        .success()
        .stdout(predicates::str::contains("\"runtime\": \"v22.1.0\""));

    env.command()
        .args(["toolchain", "list", "--quiet"])
        .assert()
        .success()
        .stdout(predicates::str::contains("v22.1.0"))
        .stdout(predicates::str::contains("v24.0.0").not());
}

#[test]
#[serial]
fn check_with_no_installed_runtimes_returns_empty_payload() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "check"])
        .output()
        .expect("check --output json without installed runtimes");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("check JSON array");
    assert!(entries.is_empty());
}

#[test]
#[serial]
fn check_reports_latest_available_null_when_runtime_is_current() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive("22.1.0", "linux-x64", &[("node", "#!/bin/sh\necho 22.1\n")]),
        None,
    );

    env.command()
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .success();

    let output = env
        .command()
        .args(["--output", "json", "check"])
        .output()
        .expect("check --output json with up-to-date runtime");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    let entries = payload.as_array().expect("check JSON array");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0]["runtime"], "v22.1.0");
    assert_eq!(entries[0]["has_update"], false);
    assert!(entries[0]["latest_available"].is_null());
}

#[test]
#[serial]
fn color_flag_always_applies_ansi_to_human_stdout() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--color", "always", "show", "home"])
        .output()
        .expect("show home with --color always");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("\u{1b}["));
}

#[test]
#[serial]
fn nodeup_color_env_always_applies_ansi_to_human_stdout() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NODEUP_COLOR", "always")
        .args(["show", "home"])
        .output()
        .expect("show home with NODEUP_COLOR=always");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("\u{1b}["));
}

#[test]
#[serial]
fn color_flag_never_overrides_nodeup_color_env() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NODEUP_COLOR", "always")
        .args(["--color", "never", "show", "home"])
        .output()
        .expect("show home with --color never and NODEUP_COLOR=always");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(!stdout.contains("\u{1b}["));
}

#[test]
#[serial]
fn json_output_does_not_include_ansi_when_color_is_forced() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--output", "json", "--color", "always", "show", "home"])
        .output()
        .expect("show home --output json with forced color");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(!stdout.contains("\u{1b}["));

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["data_root"].is_string());
}

#[test]
#[serial]
fn invalid_release_index_ttl_does_not_log_for_commands_without_release_index() {
    let env = TestEnv::new();
    let output = env
        .command()
        .env("RUST_LOG", "nodeup=warn")
        .env("NODEUP_RELEASE_INDEX_TTL_SECONDS", "abc")
        .args(["show", "home"])
        .output()
        .expect("show home with invalid release index ttl");

    assert!(output.status.success());
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("nodeup home:"));
    assert!(!stdout.contains("Invalid release index TTL value"));
    assert!(!stdout.contains("invalid_value_category"));
    assert!(!stdout.contains("fallback_seconds"));
    assert!(!stdout.contains("env_value"));
    assert!(!stdout.contains("abc"));
}

#[test]
#[serial]
fn json_output_stays_parseable_with_invalid_release_index_ttl() {
    let env = TestEnv::new();
    let output = env
        .command()
        .env("NODEUP_RELEASE_INDEX_TTL_SECONDS", "-1")
        .args(["--output", "json", "show", "home"])
        .output()
        .expect("show home json with invalid release index ttl");

    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(payload["data_root"].is_string());
}

#[test]
#[serial]
fn color_diagnostics_reports_no_color_for_human_output_and_logs() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("NODEUP_LOG_COLOR")
        .env("NO_COLOR", "1")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color with NO_COLOR");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(!stdout.contains("\u{1b}["));

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["human_stdout"]["enabled"], false);
    assert_eq!(payload["human_stdout"]["source"], "NO_COLOR");
    assert_eq!(payload["human_stderr"]["enabled"], false);
    assert_eq!(payload["human_stderr"]["source"], "NO_COLOR");
    assert_eq!(payload["logs"]["enabled"], false);
    assert_eq!(payload["logs"]["source"], "NO_COLOR");
}

#[test]
#[serial]
fn color_diagnostics_reports_nodeup_color_overrides_no_color() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("NODEUP_LOG_COLOR")
        .env("NO_COLOR", "1")
        .env("NODEUP_COLOR", "always")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color with NODEUP_COLOR and NO_COLOR");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["human_stdout"]["enabled"], true);
    assert_eq!(payload["human_stdout"]["source"], "NODEUP_COLOR");
    assert_eq!(payload["human_stderr"]["enabled"], true);
    assert_eq!(payload["human_stderr"]["source"], "NODEUP_COLOR");
    assert_eq!(payload["logs"]["enabled"], false);
    assert_eq!(payload["logs"]["source"], "NO_COLOR");
}

#[test]
#[serial]
fn color_diagnostics_reports_nodeup_log_color_overrides_no_color() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NO_COLOR", "1")
        .env("NODEUP_LOG_COLOR", "always")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color with NODEUP_LOG_COLOR and NO_COLOR");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["human_stdout"]["enabled"], false);
    assert_eq!(payload["human_stdout"]["source"], "NO_COLOR");
    assert_eq!(payload["human_stderr"]["enabled"], false);
    assert_eq!(payload["human_stderr"]["source"], "NO_COLOR");
    assert_eq!(payload["logs"]["enabled"], true);
    assert_eq!(payload["logs"]["source"], "NODEUP_LOG_COLOR");
}

#[test]
#[serial]
fn color_diagnostics_preserves_auto_log_color_mode() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("NO_COLOR")
        .env("NODEUP_LOG_COLOR", "auto")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color with NODEUP_LOG_COLOR=auto");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["logs"]["enabled"], true);
    assert_eq!(payload["logs"]["mode"], "auto");
    assert_eq!(payload["logs"]["source"], "NODEUP_LOG_COLOR");
}

#[test]
#[serial]
fn color_diagnostics_preserves_auto_log_color_mode_with_no_color() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NO_COLOR", "1")
        .env("NODEUP_LOG_COLOR", "auto")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color with NODEUP_LOG_COLOR=auto and NO_COLOR");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["logs"]["enabled"], false);
    assert_eq!(payload["logs"]["mode"], "auto");
    assert_eq!(payload["logs"]["source"], "NODEUP_LOG_COLOR");
}

#[test]
#[serial]
fn color_diagnostics_reports_invalid_color_env_values() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NO_COLOR", "1")
        .env("NODEUP_COLOR", "sometimes")
        .env("NODEUP_LOG_COLOR", "maybe")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color with invalid color env values");
    assert!(output.status.success());

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["human_stdout"]["enabled"], false);
    assert_eq!(payload["human_stdout"]["source"], "NO_COLOR");
    assert_eq!(payload["human_stdout"]["ignored_nodeup_color"], "sometimes");
    assert_eq!(payload["human_stderr"]["ignored_nodeup_color"], "sometimes");
    assert_eq!(payload["logs"]["enabled"], false);
    assert_eq!(payload["logs"]["source"], "NO_COLOR");
    assert_eq!(payload["logs"]["ignored_nodeup_log_color"], "maybe");
}

#[test]
#[serial]
fn color_diagnostics_json_output_stays_plain_when_log_color_is_forced() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("NODEUP_LOG_COLOR", "always")
        .args(["--output", "json", "show", "color"])
        .output()
        .expect("show color json with forced log color");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(!stdout.contains("\u{1b}["));

    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["logs"]["enabled"], true);
    assert_eq!(payload["logs"]["source"], "NODEUP_LOG_COLOR");
}

#[test]
#[serial]
fn completions_output_stays_raw_without_ansi_when_color_is_forced() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--color", "always", "completions", "bash"])
        .output()
        .expect("bash completions with forced color");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("nodeup"));
    assert!(!stdout.contains("\u{1b}["));
}

#[test]
#[serial]
fn completions_output_stays_raw_with_invalid_release_index_ttl() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env("RUST_LOG", "nodeup=warn")
        .env("NODEUP_RELEASE_INDEX_TTL_SECONDS", "abc")
        .args(["completions", "bash"])
        .output()
        .expect("bash completions with invalid release index ttl");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("nodeup"));
    assert!(!stdout.contains("Invalid release index TTL value"));
    assert!(!stdout.contains("invalid_value_category"));
    assert!(!stdout.contains("abc"));
}

#[test]
#[serial]
fn human_error_output_uses_styled_label_when_color_is_forced() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["--color", "always", "which", "--runtime", "22.1.0", "node"])
        .output()
        .expect("which with missing runtime should fail");
    assert!(!output.status.success());

    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("\u{1b}[1;31mnodeup error:\u{1b}[0m"));
}

#[test]
#[serial]
fn json_error_output_stays_machine_parseable_when_color_is_forced() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args([
            "--output",
            "json",
            "--color",
            "always",
            "which",
            "--runtime",
            "22.1.0",
            "node",
        ])
        .output()
        .expect("json which with missing runtime should fail");
    assert!(!output.status.success());

    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!stderr.contains("\u{1b}["));

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "not-found");
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_supports_npm_alias() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho shim-node\n"),
                ("npm", "#!/bin/sh\necho shim-npm\n"),
            ],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("npm");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .output()
        .expect("run npm shim binary");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("shim-npm"));
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_supports_npx_alias() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho shim-node\n"),
                ("npx", "#!/bin/sh\necho shim-npx\n"),
            ],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("npx");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .output()
        .expect("run npx shim binary");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("shim-npx"));
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_supports_yarn_alias_via_npm_exec_with_package_manager_field() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    let npm_script = make_npm_argv_script("npm-argv");
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho shim-node\n"),
                ("npm", &npm_script),
            ],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let project_dir = env.root.join("project-shim-yarn");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"shim-yarn","packageManager":"yarn@4.13.0"}"#,
    )
    .unwrap();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("yarn");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .current_dir(&project_dir)
        .arg("--version")
        .output()
        .expect("run yarn shim binary");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(
        stdout.contains("npm-argv:exec --yes --package @yarnpkg/cli-dist@4.13.0 -- yarn --version")
    );
}

#[cfg(unix)]
#[test]
#[serial]
fn shim_dispatch_supports_pnpm_alias_via_npm_exec_with_package_manager_field() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    let npm_script = make_npm_argv_script("npm-argv");
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho shim-node\n"),
                ("npm", &npm_script),
            ],
        ),
        None,
    );

    env.command().args(["default", "22.1.0"]).assert().success();

    let project_dir = env.root.join("project-shim-pnpm");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"shim-pnpm","packageManager":"pnpm@10.32.1"}"#,
    )
    .unwrap();

    let real_bin = assert_cmd::cargo::cargo_bin!("nodeup");
    let shim_path = env.root.join("pnpm");
    std::os::unix::fs::symlink(real_bin, &shim_path).unwrap();

    let output = env
        .command_with_program(&shim_path)
        .current_dir(&project_dir)
        .arg("--version")
        .output()
        .expect("run pnpm shim binary");
    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("npm-argv:exec --yes --package pnpm@10.32.1 -- pnpm --version"));
}

#[test]
#[serial]
fn run_yarn_uses_package_manager_policy_via_npm_exec() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    let npm_script = make_npm_argv_script("npm-argv");
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho run-node\n"), ("npm", &npm_script)],
        ),
        None,
    );

    let project_dir = env.root.join("project-run-yarn-policy");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"run-yarn","packageManager":"yarn@1.22.22"}"#,
    )
    .unwrap();

    env.command()
        .current_dir(&project_dir)
        .args([
            "run",
            "--install",
            "22.1.0",
            "yarn",
            "install",
            "--immutable",
        ])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "npm-argv:exec --yes --package yarn@1.22.22 -- yarn install --immutable",
        ));
}

#[test]
#[serial]
fn which_yarn_uses_runtime_npm_path_in_npm_exec_mode() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-which-yarn-package-manager");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho node\n");
    fs::write(runtime_bin.join("npm"), "#!/bin/sh\necho npm\n").unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-yarn-package-manager",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args(["default", "linked-which-yarn-package-manager"])
        .assert()
        .success();

    let project_dir = env.root.join("project-which-yarn-package-manager");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"which-yarn","packageManager":"yarn@4.13.0"}"#,
    )
    .unwrap();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args(["which", "yarn"])
        .output()
        .expect("which yarn with packageManager");
    assert!(output.status.success());

    let expected = fs::canonicalize(runtime_dir.join("bin").join("npm")).unwrap();
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains(expected.to_string_lossy().as_ref()));
    assert!(stdout.contains("yarn will run via npm exec"));
    assert!(stdout.contains("@yarnpkg/cli-dist@4.13.0"));
    assert!(stdout.contains("pinned"));
}

#[test]
#[serial]
fn run_package_manager_mismatch_returns_conflict() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    let npm_script = make_npm_argv_script("npm-argv");
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho run-node\n"), ("npm", &npm_script)],
        ),
        None,
    );

    let project_dir = env.root.join("project-run-mismatch");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"run-mismatch","packageManager":"pnpm@10.32.1"}"#,
    )
    .unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["run", "--install", "22.1.0", "yarn", "--version"])
        .assert()
        .failure()
        .code(6)
        .stderr(predicates::str::contains(
            "does not match packageManager 'pnpm@10.32.1'",
        ));
}

#[test]
#[serial]
fn package_manager_range_json_error_identifies_failed_version_part() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho run-node\n"),
                ("npm", "#!/bin/sh\n"),
            ],
        ),
        None,
    );

    let project_dir = env.root.join("project-invalid-package-manager-range");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"invalid-range","packageManager":"pnpm@10.x"}"#,
    )
    .unwrap();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args([
            "--output",
            "json",
            "run",
            "--install",
            "22.1.0",
            "pnpm",
            "--version",
        ])
        .output()
        .expect("invalid packageManager range");
    assert!(!output.status.success());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["exit_code"], 2);
    assert_eq!(
        payload["diagnostics"]["diagnostic"],
        "package-manager-invalid"
    );
    assert_eq!(payload["diagnostics"]["failed_part"], "version");
    assert_eq!(payload["diagnostics"]["problem"], "non-exact-semver");
    assert_eq!(payload["diagnostics"]["manager"], "pnpm");
    assert_eq!(payload["diagnostics"]["version"], "10.x");
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("pnpm@<major>.<minor>.<patch>"));
}

#[test]
#[serial]
fn unsupported_package_manager_json_error_identifies_manager_part() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho run-node\n"),
                ("npm", "#!/bin/sh\n"),
            ],
        ),
        None,
    );

    let project_dir = env.root.join("project-invalid-package-manager-npm");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"invalid-npm","packageManager":"npm@10.0.0"}"#,
    )
    .unwrap();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args([
            "--output",
            "json",
            "run",
            "--install",
            "22.1.0",
            "yarn",
            "--version",
        ])
        .output()
        .expect("unsupported packageManager manager");
    assert!(!output.status.success());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["diagnostics"]["failed_part"], "manager");
    assert_eq!(payload["diagnostics"]["problem"], "unsupported-manager");
    assert_eq!(payload["diagnostics"]["manager"], "npm");
    assert!(payload["message"]
        .as_str()
        .unwrap()
        .contains("Unsupported packageManager manager 'npm'"));
}

#[test]
#[serial]
fn non_string_package_manager_json_error_identifies_expected_shape() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho run-node\n"),
                ("npm", "#!/bin/sh\n"),
            ],
        ),
        None,
    );

    let project_dir = env.root.join("project-invalid-package-manager-type");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"invalid-type","packageManager":10}"#,
    )
    .unwrap();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args([
            "--output",
            "json",
            "run",
            "--install",
            "22.1.0",
            "pnpm",
            "--version",
        ])
        .output()
        .expect("non-string packageManager");
    assert!(!output.status.success());

    let payload: Value = serde_json::from_slice(&output.stderr).unwrap();
    assert_eq!(payload["kind"], "invalid-input");
    assert_eq!(payload["diagnostics"]["failed_part"], "value");
    assert_eq!(payload["diagnostics"]["problem"], "non-string");
    assert_eq!(payload["diagnostics"]["received_type"], "number");
    assert_eq!(
        payload["diagnostics"]["expected"],
        "<manager>@<exact-semver>"
    );
}

#[test]
#[serial]
fn run_yarn_prefers_direct_binary_when_package_manager_field_is_missing() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    let npm_script = make_npm_argv_script("npm-argv");
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[
                ("node", "#!/bin/sh\necho run-node\n"),
                ("npm", &npm_script),
                ("yarn", "#!/bin/sh\necho direct-yarn\n"),
            ],
        ),
        None,
    );

    let project_dir = env.root.join("project-run-direct-yarn");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"run-direct-yarn"}"#,
    )
    .unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["run", "--install", "22.1.0", "yarn", "--version"])
        .assert()
        .success()
        .stdout(predicates::str::contains("direct-yarn"));
}

#[test]
#[serial]
fn run_yarn_falls_back_to_npm_exec_when_package_manager_field_is_missing_and_binary_is_missing() {
    let env = TestEnv::new();
    env.register_index(&[("22.1.0", Some("Jod"))]);
    let npm_script = make_npm_argv_script("npm-argv");
    env.register_release(
        "22.1.0",
        make_archive(
            "22.1.0",
            "linux-x64",
            &[("node", "#!/bin/sh\necho run-node\n"), ("npm", &npm_script)],
        ),
        None,
    );

    let project_dir = env.root.join("project-run-fallback-yarn");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"run-fallback-yarn"}"#,
    )
    .unwrap();

    env.command()
        .current_dir(&project_dir)
        .args(["run", "--install", "22.1.0", "yarn", "--version"])
        .assert()
        .success()
        .stdout(predicates::str::contains(
            "npm-argv:exec --yes --package @yarnpkg/cli-dist -- yarn --version",
        ))
        .stderr(predicates::str::contains(
            "unpinned fallback; add exact packageManager",
        ));
}

#[test]
#[serial]
fn which_pnpm_uses_runtime_npm_path_in_npm_exec_mode() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-which-pnpm-package-manager");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho node\n");
    fs::write(runtime_bin.join("npm"), "#!/bin/sh\necho npm\n").unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-pnpm-package-manager",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args(["default", "linked-which-pnpm-package-manager"])
        .assert()
        .success();

    let project_dir = env.root.join("project-which-pnpm-package-manager");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"which-pnpm","packageManager":"pnpm@10.32.1"}"#,
    )
    .unwrap();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args(["which", "pnpm"])
        .output()
        .expect("which pnpm with packageManager");
    assert!(output.status.success());

    let expected = fs::canonicalize(runtime_dir.join("bin").join("npm")).unwrap();
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains(expected.to_string_lossy().as_ref()));
    assert!(stdout.contains("pnpm will run via npm exec"));
    assert!(stdout.contains("pnpm@10.32.1"));
    assert!(stdout.contains("pinned"));
}

#[test]
#[serial]
fn which_pnpm_json_exposes_npm_exec_planning_fields() {
    let env = TestEnv::new();
    let runtime_dir = env
        .root
        .join("linked-runtime-which-pnpm-json-package-manager");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    write_runtime_executable(runtime_bin.join("node"), "#!/bin/sh\necho node\n");
    fs::write(runtime_bin.join("npm"), "#!/bin/sh\necho npm\n").unwrap();

    env.command()
        .args([
            "toolchain",
            "link",
            "linked-which-pnpm-json-package-manager",
            runtime_dir.to_str().unwrap(),
        ])
        .assert()
        .success();
    env.command()
        .args(["default", "linked-which-pnpm-json-package-manager"])
        .assert()
        .success();

    let project_dir = env.root.join("project-which-pnpm-json-package-manager");
    fs::create_dir_all(&project_dir).unwrap();
    fs::write(
        project_dir.join("package.json"),
        r#"{"name":"which-pnpm-json","packageManager":"pnpm@10.32.1"}"#,
    )
    .unwrap();

    let output = env
        .command()
        .current_dir(&project_dir)
        .args(["--output", "json", "which", "pnpm"])
        .output()
        .expect("which pnpm json with packageManager");
    assert!(output.status.success());

    let expected = fs::canonicalize(runtime_dir.join("bin").join("npm")).unwrap();
    let payload: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(payload["requested_command"], "pnpm");
    assert_eq!(
        payload["executable_path"].as_str().unwrap(),
        expected.to_string_lossy()
    );
    assert_eq!(payload["mode"], "npm-exec");
    assert_eq!(payload["reason"], "package-manager-pinned");
    assert_eq!(payload["package_spec"], "pnpm@10.32.1");
    assert_eq!(payload["package_spec_pinned"], true);
    assert_eq!(payload["planning"]["mode"], "npm-exec");
    assert_eq!(payload["planning"]["package_spec"], "pnpm@10.32.1");
}
