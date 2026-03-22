use std::{
    collections::HashMap,
    fs,
    io::Write,
    path::{Path, PathBuf},
};

use assert_cmd::Command;
use httpmock::{Method::GET, MockServer};
use serde_json::Value;
use serial_test::serial;
use sha2::{Digest, Sha256};
use xz2::write::XzEncoder;

struct TestEnv {
    root: PathBuf,
    data_root: PathBuf,
    cache_root: PathBuf,
    config_root: PathBuf,
    index_url: String,
    download_base_url: String,
    server: MockServer,
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
        let version = normalize(version);
        let segment = "linux-x64";
        let archive_name = format!("node-{version}-{segment}.tar.xz");

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
    fs::write(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-standard\n",
    )
    .unwrap();

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
    fs::write(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-quiet\n",
    )
    .unwrap();

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
    fs::write(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-verbose\n",
    )
    .unwrap();

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
    fs::write(
        linked_runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-json\n",
    )
    .unwrap();

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
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n").unwrap();

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
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n").unwrap();

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
    assert!(stderr.contains("Linked runtime path must contain `bin/node`"));
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
        .contains("Linked runtime path must contain `bin/node`"));
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

    env.command()
        .args(["toolchain", "uninstall", "v22.1.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("used as the default runtime"));
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

    env.command()
        .args(["toolchain", "uninstall", "v22.1.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains(
            "referenced by a directory override",
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
        .stderr(predicates::str::contains("used as the default runtime"));

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
fn show_active_runtime_fails_when_linked_runtime_path_is_deleted() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-deleted");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    fs::write(
        runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-node\n",
    )
    .unwrap();

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
    fs::write(
        runtime_bin.join("node"),
        "#!/bin/sh\necho linked-runtime-node\n",
    )
    .unwrap();

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
fn management_human_default_logging_emits_info_logs_without_rust_log_env() {
    let env = TestEnv::new();

    let output = env
        .command()
        .env_remove("RUST_LOG")
        .args(["show", "home"])
        .output()
        .expect("show home without rust log env");

    assert!(output.status.success());

    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("command_path: \"nodeup.show.home\""));
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
fn completions_accepts_valid_top_level_scope() {
    let env = TestEnv::new();

    let output = env
        .command()
        .args(["completions", "bash", "show"])
        .output()
        .expect("completions bash show");

    assert!(output.status.success());
    assert!(!output.stdout.is_empty());
    assert!(String::from_utf8_lossy(&output.stdout).contains("nodeup"));
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
        .stdout(predicates::str::contains("\"status\": \"updated\""));
}

#[test]
#[serial]
fn update_reports_already_up_to_date_when_latest_is_already_installed() {
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
            "\"status\": \"already-up-to-date\"",
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
    env.register_index(&[("22.1.0", Some("Jod"))]);

    let mut cmd = env.command();
    cmd.env("NODEUP_FORCE_PLATFORM", "windows-x64")
        .args(["toolchain", "install", "22.1.0"])
        .assert()
        .failure()
        .stderr(predicates::str::contains("supports macOS/Linux"));
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
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho which-default\n").unwrap();

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
        fs::write(
            runtime_bin.join("node"),
            format!("#!/bin/sh\necho {marker}\n").as_bytes(),
        )
        .unwrap();
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
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho only-node\n").unwrap();

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
            "lts",
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
    assert!(entries
        .iter()
        .any(|entry| entry["path"] == canonical_a && entry["selector"] == "v22.1.0"));
    assert!(entries
        .iter()
        .any(|entry| entry["path"] == canonical_b && entry["selector"] == "lts"));
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
            "22.1.0",
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
    assert_eq!(entries[0]["selector"], "v22.1.0");
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
            "Missing runtime selector for `nodeup toolchain install`",
        ));
}

#[test]
#[serial]
fn toolchain_install_rejects_linked_runtime_selector() {
    let env = TestEnv::new();
    let runtime_dir = env.root.join("linked-runtime-install-reject");
    let runtime_bin = runtime_dir.join("bin");
    fs::create_dir_all(&runtime_bin).unwrap();
    fs::write(runtime_bin.join("node"), "#!/bin/sh\necho linked-runtime\n").unwrap();

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
fn toolchain_uninstall_requires_at_least_one_runtime_selector() {
    let env = TestEnv::new();

    env.command()
        .args(["toolchain", "uninstall"])
        .assert()
        .failure()
        .code(2)
        .stderr(predicates::str::contains(
            "Missing runtime selector for `nodeup toolchain uninstall`",
        ));
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
    assert_eq!(entries[0]["status"], "updated");
    assert_eq!(entries[0]["updated_runtime"], "v22.2.0");
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
