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
        let data_root = root.join("data");
        let cache_root = root.join("cache");
        let config_root = root.join("config");
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
            header.set_size(script_body.as_bytes().len() as u64);
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
