use std::{
    collections::HashMap,
    fs,
    io::Write,
    path::{Path, PathBuf},
};

use assert_cmd::Command;
use httpmock::{Method::GET, MockServer};
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
    }

    fn apply_env_std(&self, command: &mut std::process::Command) {
        command.env("NODEUP_DATA_HOME", &self.data_root);
        command.env("NODEUP_CACHE_HOME", &self.cache_root);
        command.env("NODEUP_CONFIG_HOME", &self.config_root);
        command.env("NODEUP_INDEX_URL", &self.index_url);
        command.env("NODEUP_DOWNLOAD_BASE_URL", &self.download_base_url);
        command.env("NODEUP_FORCE_PLATFORM", "linux-x64");
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
        .stderr(predicates::str::contains("used as default runtime"));
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
        .stderr(predicates::str::contains("referenced by an override"));
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
        .stderr(predicates::str::contains("No runtimes to update"));
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
fn override_resolution_logs_hit_with_fallback_reason() {
    let env = TestEnv::new();
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
            "command_path=\"nodeup.resolve.override\"",
        ))
        .stdout(predicates::str::contains("matched=true"))
        .stdout(predicates::str::contains(
            "fallback_reason=\"override-matched\"",
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
            "command_path=\"nodeup.resolve.override\"",
        ))
        .stdout(predicates::str::contains("matched=false"))
        .stdout(predicates::str::contains(
            "fallback_reason=\"no-default-selector\"",
        ));
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
            "command_path=\"nodeup.self.update\"",
        ))
        .stdout(predicates::str::contains("action=\"self update\""))
        .stdout(predicates::str::contains("outcome=\"updated\""));
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
            "command_path=\"nodeup.self.uninstall\"",
        ))
        .stdout(predicates::str::contains("action=\"self uninstall\""))
        .stdout(predicates::str::contains("outcome=\"removed\""));

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
            "command_path=\"nodeup.self.upgrade-data\"",
        ))
        .stdout(predicates::str::contains("action=\"self upgrade-data\""))
        .stdout(predicates::str::contains("outcome=\"upgraded\""))
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
fn completions_logs_action_and_outcome() {
    let env = TestEnv::new();

    env.command_with_info_logs()
        .args(["completions", "zsh"])
        .assert()
        .failure()
        .stdout(predicates::str::contains(
            "command_path=\"nodeup.completions\"",
        ))
        .stdout(predicates::str::contains("action=\"generate\""))
        .stdout(predicates::str::contains("outcome=\"not-implemented\""));
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
            "command_path=\"nodeup.run.process\"",
        ))
        .stdout(predicates::str::contains("exit_code=7"))
        .stdout(predicates::str::contains("signal=None"));
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
        .stderr(predicates::str::contains("Invalid selector"));
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
