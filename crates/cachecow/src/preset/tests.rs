// SPDX-License-Identifier: Apache-2.0
//! Ported from `cmds/cachecow/internal/preset/preset_test.go`, plus #4578.

#![cfg(unix)]

use std::{fs, os::unix::fs::symlink, sync::Mutex};

use serde_json::json;

use super::*;
use crate::repository::verify_file_stamps;

#[derive(Clone, Debug)]
struct Call {
    cwd: String,
    env: Vec<String>,
    args: Vec<String>,
}

#[derive(Default)]
struct FakeRunner {
    calls: Mutex<Vec<Call>>,
    version: Mutex<String>,
    metadata: Mutex<String>,
    compiler: Mutex<String>,
    wrapper_version: Mutex<String>,
    frontend: Mutex<String>,
}

impl FakeRunner {
    fn set(field: &Mutex<String>, value: impl Into<String>) {
        *field.lock().unwrap() = value.into();
    }

    fn call(&self, i: usize) -> Call {
        self.calls.lock().unwrap()[i].clone()
    }
}

impl Runner for FakeRunner {
    fn output(&self, _: &Context, cwd: &str, env: &[String], args: &[String]) -> Result<Vec<u8>> {
        self.calls.lock().unwrap().push(Call {
            cwd: cwd.to_string(),
            env: env.to_vec(),
            args: args.to_vec(),
        });
        let last = args.last().map(String::as_str);
        if last == Some("--version") && paths::base(&args[0]) == "sccache" {
            return Ok(self.wrapper_version.lock().unwrap().clone().into_bytes());
        }
        if last == Some("--version") {
            return Ok(self.version.lock().unwrap().clone().into_bytes());
        }
        if last == Some("-vV") {
            let compiler = self.compiler.lock().unwrap();
            return Ok(if compiler.is_empty() {
                b"rustc 1.99.1 (fixture)\nhost: x86_64-unknown-linux-gnu\nrelease: 1.99.1\n"
                    .to_vec()
            } else {
                compiler.as_bytes().to_vec()
            });
        }
        if args[0] == "node" {
            return Ok(self.frontend.lock().unwrap().clone().into_bytes());
        }
        Ok(self.metadata.lock().unwrap().clone().into_bytes())
    }
}

fn ctx() -> Context {
    Context::background()
}

fn s(p: &std::path::Path) -> String {
    p.to_str().unwrap().to_string()
}

fn strings(v: &[&str]) -> Vec<String> {
    v.iter().map(|s| (*s).to_string()).collect()
}

struct Fixture {
    _dir: tempfile::TempDir,
    cwd: String,
    argv: Vec<String>,
    env: Vec<String>,
    preset: Option<Id>,
    shim_dir: String,
    runner: FakeRunner,
}

impl Fixture {
    fn request(&self) -> Request<'_> {
        Request {
            cwd: self.cwd.clone(),
            argv: self.argv.clone(),
            env: Some(self.env.clone()),
            preset: self.preset,
            protected: Vec::new(),
            shim_dir: self.shim_dir.clone(),
            runner: Some(&self.runner),
        }
    }

    fn discover(&self) -> Result<PresetResult> {
        discover(&ctx(), self.request())
    }
}

fn project() -> (tempfile::TempDir, String) {
    let t = tempfile::tempdir().unwrap();
    let p = t.path().join("project 한 space");
    fs::create_dir(&p).unwrap();
    let p = s(&p);
    crate::repository::tests::run_git(&p, &["init"]);
    (t, p)
}

fn metadata(workspace: &str, target: &str, build: &str) -> String {
    json!({"version": 1, "workspace_root": workspace, "target_directory": target, "build_directory": build})
        .to_string()
}

fn cargo_fixture() -> Fixture {
    let (dir, p) = project();
    let runner = FakeRunner::default();
    FakeRunner::set(&runner.version, "cargo 1.99.1 (fixture)");
    let target = paths::join2(&p, "target");
    FakeRunner::set(&runner.metadata, metadata(&p, &target, &target));
    Fixture {
        _dir: dir,
        cwd: p,
        argv: strings(&["cargo", "build"]),
        env: strings(&["PATH=/fixture", "SETTING=secret-value"]),
        preset: None,
        shim_dir: String::new(),
        runner,
    }
}

#[test]
fn cargo_discovery_and_compatibility() {
    let mut f = cargo_fixture();
    f.argv = strings(&[
        "cargo",
        "+1.99.1",
        "build",
        "--target-dir",
        "cache 한",
        "--config",
        "build.jobs=2",
        "--features=one",
        "--",
        "--target-dir",
        "ignored",
    ]);
    let got = f.discover().unwrap();
    assert_eq!(got.roots.len(), 1);
    assert_eq!(got.roots[0].kinds.len(), 2);
    assert!(got.compatibility.compatible(&got.compatibility));
    let meta = f.runner.call(1);
    let joined = meta.args.join("\n");
    for arg in [
        "+1.99.1",
        "metadata",
        "build.target-dir=\"cache 한\"",
        "--features",
        "one",
        "build.jobs=2",
    ] {
        assert!(joined.contains(arg), "missing {arg}: {joined}");
    }
    let canonical_cwd = paths::eval_symlinks(&f.cwd).unwrap();
    assert!(!joined.contains("ignored"));
    assert_eq!(meta.cwd, canonical_cwd);
    assert_eq!(env_value(&meta.env, "CARGO_TARGET_DIR"), "cache 한");
    let encoded = serde_json::to_string(&got).unwrap();
    assert!(!encoded.contains("secret-value") && !encoded.contains("build.jobs"));
    let mut changed = got.compatibility.clone();
    changed.platform = "different".into();
    assert!(!got.compatibility.compatible(&changed));
    assert!(!got.compatibility.compatible(&Compatibility::default()));
    f.env.push("NEW_SETTING=1".into());
    let next = f.discover().unwrap();
    assert!(
        !got.compatibility.compatible(&next.compatibility),
        "ignored settings change"
    );
}

#[test]
fn shim_directory_in_path_does_not_change_compatibility() {
    let mut f = cargo_fixture();
    let shim = paths::join2(&f.cwd, "state-shims");
    f.shim_dir = shim.clone();
    let plain = f.discover().unwrap();
    // Activating the shims prepends or appends the directory to PATH.
    for path in [format!("{shim}:/fixture"), format!("/fixture:{shim}")] {
        f.env = strings(&[&format!("PATH={path}"), "SETTING=secret-value"]);
        let activated = f.discover().unwrap();
        assert_eq!(
            activated.compatibility.settings_digest, plain.compatibility.settings_digest,
            "PATH={path}"
        );
        assert!(plain.compatibility.compatible(&activated.compatibility));
        assert!(activated.compatibility.compatible(&plain.compatibility));
    }
    // Every other PATH change stays visible: tool lookup may differ.
    f.env = strings(&[
        &format!("PATH={shim}:/fixture:/other"),
        "SETTING=secret-value",
    ]);
    let other = f.discover().unwrap();
    assert!(!plain.compatibility.compatible(&other.compatibility));
    // Without a shim directory in the request the whole PATH is hashed.
    f.shim_dir = String::new();
    f.env = strings(&[&format!("PATH={shim}:/fixture"), "SETTING=secret-value"]);
    let unaware = f.discover().unwrap();
    assert!(!plain.compatibility.compatible(&unaware.compatibility));
}

#[test]
fn cargo_rejects_unknown_unsafe_and_unsupported() {
    for version in [
        "cargo 1.99.0",
        "cargo 2.0.0",
        "cargo 1.99.1-nightly",
        "cargo 1.100.0-beta",
        "garbage",
    ] {
        let f = cargo_fixture();
        FakeRunner::set(&f.runner.version, version);
        assert!(f.discover().is_err(), "accepted version {version}");
    }
    for kind in [
        "missing-build",
        "malformed",
        "unsafe",
        "overlap",
        "bad-config",
    ] {
        let f = cargo_fixture();
        let cwd = f.cwd.clone();
        match kind {
            "missing-build" => FakeRunner::set(&f.runner.metadata, r#"{"version":1}"#),
            "malformed" => FakeRunner::set(&f.runner.metadata, "{"),
            "unsafe" => FakeRunner::set(&f.runner.metadata, metadata(&cwd, &cwd, &cwd)),
            "overlap" => FakeRunner::set(
                &f.runner.metadata,
                metadata(
                    &cwd,
                    &paths::join2(&cwd, "target"),
                    &paths::join(&[cwd.as_str(), "target", "build"]),
                ),
            ),
            _ => {
                fs::create_dir(paths::join2(&cwd, ".cargo")).unwrap();
                fs::write(
                    paths::join(&[cwd.as_str(), ".cargo", "config.toml"]),
                    "bad = [",
                )
                .unwrap();
            }
        }
        assert!(f.discover().is_err(), "{kind}: accepted invalid discovery");
    }
}

#[test]
fn cargo_config_changes_and_monorepo() {
    let mut f = cargo_fixture();
    let sub = paths::join2(&f.cwd, "member");
    fs::create_dir(&sub).unwrap();
    f.cwd.clone_from(&sub);
    let config = paths::join2(&sub, ".cargo");
    fs::create_dir(&config).unwrap();
    let p = paths::join2(&config, "config.toml");
    fs::write(&p, "[build]\njobs=2\n").unwrap();
    let a = f.discover().unwrap();
    fs::write(&p, "[build]\njobs=3\n").unwrap();
    let b = f.discover().unwrap();
    assert!(
        !a.compatibility.compatible(&b.compatibility),
        "ignored config"
    );
}

fn frontend_data(cwd: &str) -> serde_json::Value {
    json!({
        "nodeVersion": "24.0.0", "toolVersion": "2.3.0", "rspackVersion": "2.3.0",
        "result": {
            "schema": 1, "preset": "rspack",
            "inputs": [paths::join2(cwd, "rspack.config.js")],
            "caches": [{"type": "persistent", "path": paths::join(&[cwd, ".cache", "rspack"])}],
            "outputs": [paths::join2(cwd, "dist")],
            "settings": {"mode": "production"},
        }
    })
}

fn frontend_fixture() -> (Fixture, serde_json::Value) {
    let mut f = cargo_fixture();
    f.preset = Some(Id::Rspack);
    f.argv = strings(&["rspack", "build"]);
    fs::create_dir(paths::join2(&f.cwd, ".ccow")).unwrap();
    fs::write(
        paths::join(&[f.cwd.as_str(), ".ccow", "discover.cjs"]),
        "fixture",
    )
    .unwrap();
    let data = frontend_data(&f.cwd);
    (f, data)
}

#[test]
fn frontend_integration() {
    for kind in [
        "valid",
        "node-old",
        "tool-old",
        "output",
        "dependencies",
        "unknown-settings",
        "malformed",
    ] {
        let (f, mut data) = frontend_fixture();
        let cwd = f.cwd.clone();
        match kind {
            "node-old" => data["nodeVersion"] = json!("23.0.0"),
            "tool-old" => data["toolVersion"] = json!("2.2.9"),
            "output" => {
                data["result"]["caches"] =
                    json!([{"type": "persistent", "path": paths::join2(&cwd, "dist")}]);
            }
            "dependencies" => {
                data["result"]["caches"] = json!([{"type": "persistent", "path": paths::join(&[cwd.as_str(), "node_modules", ".cache"])}]);
            }
            "unknown-settings" => {
                data["result"].as_object_mut().unwrap().remove("settings");
            }
            _ => {}
        }
        FakeRunner::set(&f.runner.frontend, data.to_string());
        if kind == "malformed" {
            FakeRunner::set(&f.runner.frontend, "{");
        }
        let got = f.discover();
        if kind == "valid" {
            assert_eq!(got.unwrap().roots.len(), 1);
        } else {
            assert!(got.is_err(), "{kind}: accepted unsafe/unknown frontend");
        }
    }
}

#[test]
fn preset_selection() {
    let mut f = cargo_fixture();
    f.argv[0] = "wrapper".into();
    assert!(f.discover().is_err(), "guessed wrapper preset");
    f.preset = Some(Id::Cargo);
    f.discover().unwrap();
}

#[test]
fn equivalent_worktrees_and_different_cwd() {
    let mut f = cargo_fixture();
    let p = f.cwd.clone();
    crate::repository::tests::run_git(
        &p,
        &[
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "--allow-empty",
            "-m",
            "init",
        ],
    );
    let t = tempfile::tempdir().unwrap();
    let w = s(&t.path().join("linked"));
    crate::repository::tests::run_git(&p, &["worktree", "add", "-b", "linked", &w]);
    let a = f.discover().unwrap();
    f.cwd.clone_from(&w);
    let target = paths::join2(&w, "target");
    FakeRunner::set(&f.runner.metadata, metadata(&w, &target, &target));
    let next = f.discover().unwrap();
    assert!(
        a.compatibility.compatible(&next.compatibility),
        "equivalent worktrees differ"
    );
    fs::create_dir(paths::join2(&w, "member")).unwrap();
    f.cwd = paths::join2(&w, "member");
    let nested = f.discover().unwrap();
    assert!(
        !next.compatibility.compatible(&nested.compatibility),
        "different cwd treated as same"
    );
}

/// #4578: a derived worktree whose environment and arguments differ from the
/// source only by the worktree root prefix is compatible once the source
/// request is translated; any other difference stays incompatible.
#[test]
fn worktree_root_translation_makes_equivalent_requests_compatible() {
    let mut f = cargo_fixture();
    let primary = paths::eval_symlinks(&f.cwd).unwrap();
    crate::repository::tests::run_git(
        &primary,
        &[
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "--allow-empty",
            "-m",
            "init",
        ],
    );
    let t = tempfile::tempdir().unwrap();
    fs::create_dir(t.path().join("wt")).unwrap();
    let derived = paths::join2(&paths::eval_symlinks(&s(t.path())).unwrap(), "wt/linked");
    crate::repository::tests::run_git(&primary, &["worktree", "add", "-b", "linked", &derived]);
    let env_for = |root: &str| {
        vec![
            format!("PATH={root}/node_modules/.bin:/fixture"),
            format!("INIT_CWD={root}"),
            "SETTING=1".to_string(),
        ]
    };
    let argv_for = |root: &str| {
        vec![
            "cargo".to_string(),
            "build".to_string(),
            "--config".to_string(),
            format!("{root}/extra.toml"),
        ]
    };
    // Current (derived) worktree discovery.
    f.cwd.clone_from(&derived);
    f.env = env_for(&derived);
    f.argv = argv_for(&derived);
    let target = paths::join2(&derived, "target");
    FakeRunner::set(&f.runner.metadata, metadata(&derived, &target, &target));
    let current = f.discover().unwrap();
    // Source discovery with the current request, untranslated (the bug).
    let source_target = paths::join2(&primary, "target");
    FakeRunner::set(
        &f.runner.metadata,
        metadata(&primary, &source_target, &source_target),
    );
    f.cwd.clone_from(&primary);
    let untranslated = f.discover().unwrap();
    assert!(
        !current
            .compatibility
            .compatible(&untranslated.compatibility)
    );
    // Translated source request.
    f.env = translate_worktree_root(&env_for(&derived), &derived, &primary);
    f.argv = translate_worktree_root(&argv_for(&derived), &derived, &primary);
    assert_eq!(f.env, env_for(&primary));
    assert_eq!(f.argv, argv_for(&primary));
    let translated = f.discover().unwrap();
    assert!(current.compatibility.compatible(&translated.compatibility));
    // Any other difference remains incompatible.
    f.env.push("FOO=1".into());
    let different = f.discover().unwrap();
    assert!(!current.compatibility.compatible(&different.compatibility));
}

#[test]
fn translate_worktree_root_mirrors_digest_normalization() {
    let values = strings(&[
        "/w/a",
        "PATH=/w/bin:/w:/w-other/bin:/x/w/y",
        "--manifest-path=/w/Cargo.toml",
        "/w",
        "word/w/x",
        "/wx",
    ]);
    assert_eq!(
        translate_worktree_root(&values, "/w", "/src"),
        strings(&[
            "/src/a",
            "PATH=/src/bin:/src:/src-other/bin:/x/src/y",
            "--manifest-path=/src/Cargo.toml",
            "/src",
            "word/src/x",
            "/srcx",
        ])
    );
    assert_eq!(translate_worktree_root(&values, "", "/src"), values);
    assert_eq!(translate_worktree_root(&values, "/w", "/w"), values);
}

#[test]
fn native_node_protocol_with_fixture_packages() {
    let Ok(node) = which("node") else {
        eprintln!("Node is unavailable");
        return;
    };
    let (mut f, _) = frontend_fixture();
    let mut env: Vec<String> = std::env::vars().map(|(k, v)| format!("{k}={v}")).collect();
    env.push(format!("PATH={}", paths::dir(&node)));
    let cwd = f.cwd.clone();
    for name in ["@rspack/core", "@rsbuild/core"] {
        let p = paths::join(&[cwd.as_str(), "node_modules", name]);
        fs::create_dir_all(&p).unwrap();
        fs::write(
            paths::join2(&p, "package.json"),
            format!(r#"{{"name":"{name}","version":"2.3.0","bin":{{"rsbuild":"bin.js"}}}}"#),
        )
        .unwrap();
    }
    let bin = paths::join(&[cwd.as_str(), "node_modules", "@rsbuild/core", "bin.js"]);
    write_exec(&bin, "#!/usr/bin/env node\n");
    let script = "const path=require('node:path'); module.exports=async ({cwd,preset,argv}) => \
                  ({schema:1,preset,inputs:[path.join(cwd,'rsbuild.config.js')],caches:[{type:'\
                  persistent',path:path.join(cwd,'.cache','test')}],outputs:[path.join(cwd,'dist'\
                  )],settings:{mode:argv[3]}});";
    fs::write(
        paths::join(&[cwd.as_str(), ".ccow", "discover.cjs"]),
        script,
    )
    .unwrap();
    let run = |argv0: &str, preset: Id| {
        discover(
            &ctx(),
            Request {
                cwd: cwd.clone(),
                argv: vec![
                    argv0.to_string(),
                    "build".into(),
                    "--mode".into(),
                    "production".into(),
                ],
                env: Some(env.clone()),
                preset: Some(preset),
                protected: Vec::new(),
                shim_dir: String::new(),
                runner: None,
            },
        )
    };
    let result = run(&bin, Id::Rsbuild).unwrap();
    assert_eq!(result.preset, Id::Rsbuild);
    assert!(result.compatibility.verified());
    // A supported cwd installation must not certify a selected older install.
    let external_dir = tempfile::tempdir().unwrap();
    let external = s(&external_dir.path().join("tool"));
    fs::create_dir_all(paths::join(&[
        external.as_str(),
        "node_modules",
        "@rspack/core",
    ]))
    .unwrap();
    fs::write(
        paths::join2(&external, "package.json"),
        r#"{"name":"@rsbuild/core","version":"2.2.0","bin":{"rsbuild":"bin.js"}}"#,
    )
    .unwrap();
    write_exec(&paths::join2(&external, "bin.js"), "#!/usr/bin/env node\n");
    fs::write(
        paths::join(&[
            external.as_str(),
            "node_modules",
            "@rspack/core",
            "package.json",
        ]),
        r#"{"version":"2.3.0"}"#,
    )
    .unwrap();
    assert!(run(&paths::join2(&external, "bin.js"), Id::Rsbuild).is_err());
    // Probe failures must not expose project stdout/stderr or argv.
    fs::write(
        paths::join(&[cwd.as_str(), ".ccow", "discover.cjs"]),
        "throw new Error('secret-value')",
    )
    .unwrap();
    let e = run(&bin, Id::Rsbuild).unwrap_err();
    assert!(!format!("{e:#}").contains("secret-value"));
    f.argv.clear();
}

fn which(name: &str) -> Result<String> {
    for dir in paths::split_list(&std::env::var("PATH").unwrap_or_default()) {
        let p = paths::join2(&dir, name);
        if fs::metadata(&p).is_ok_and(|m| m.is_file()) {
            return Ok(p);
        }
    }
    bail!("{name} not found")
}

fn write_exec(path: &str, text: &str) {
    use std::os::unix::fs::PermissionsExt;
    fs::write(path, text).unwrap();
    fs::set_permissions(path, fs::Permissions::from_mode(0o700)).unwrap();
}

#[test]
fn cargo_separated_roots_and_environment() {
    let mut f = cargo_fixture();
    f.env.extend(strings(&[
        "CARGO_TARGET_DIR=custom-target",
        "CARGO_BUILD_BUILD_DIR=custom-build",
    ]));
    let cwd = f.cwd.clone();
    FakeRunner::set(
        &f.runner.metadata,
        metadata(
            &cwd,
            &paths::join2(&cwd, "custom-target"),
            &paths::join2(&cwd, "custom-build"),
        ),
    );
    let got = f.discover().unwrap();
    assert_eq!(got.roots.len(), 2);
    assert_eq!(
        env_value(&f.runner.call(1).env, "CARGO_BUILD_BUILD_DIR"),
        "custom-build"
    );
}

#[test]
fn cargo_rejects_unverified_config_indirection() {
    for setting in [
        r#"build.rustc="custom""#,
        r#"build={rustc="custom"}"#,
        r#"include=["other.toml"]"#,
        r#"env.RUSTC="custom""#,
        r#"build.rustc-wrapper="wrapper""#,
    ] {
        let mut f = cargo_fixture();
        f.argv.extend(strings(&["--config", setting]));
        assert!(f.discover().is_err(), "accepted {setting}");
    }
}

#[test]
fn compatibility_rejects_malformed_records() {
    let f = cargo_fixture();
    let got = f.discover().unwrap();
    let changes: [fn(&mut Compatibility); 4] = [
        |c| c.schema = 2,
        |c| c.tool_version = "unknown".into(),
        |c| c.settings_digest = "z".repeat(64),
        |c| c.architecture.clear(),
    ];
    for change in changes {
        let mut c = got.compatibility.clone();
        change(&mut c);
        assert!(!c.compatible(&c), "accepted malformed compatibility");
    }
}

#[test]
fn session_and_shell_environment_does_not_invalidate_compatibility() {
    let mut f = cargo_fixture();
    let a = f.discover().unwrap();
    f.env.extend(strings(&[
        "CCOW_SESSION=session-token",
        "PWD=/different",
        "OLDPWD=/previous",
        "SHLVL=42",
        "_=/bin/other",
    ]));
    let b = f.discover().unwrap();
    assert!(a.compatibility.compatible(&b.compatibility));
}

#[test]
fn semver_rules_match_go() {
    assert!(supported("1.99.1", "1.99.1", "2.0.0"));
    assert!(supported("1.100.0", "1.99.1", "2.0.0"));
    assert!(supported("1.99.10", "1.99.9", "2.0.0"));
    assert!(supported("2", "2.0.0", "3.0.0"), "shorthand major");
    assert!(supported("2.3", "2.3.0", "3.0.0"), "shorthand minor");
    for bad in [
        "1.99.0",
        "2.0.0",
        "1.100.0-nightly",
        "1.99.1+build",
        "01.99.1",
        "1.99.1.1",
        "",
        "v1.99.1",
        "garbage",
    ] {
        assert!(!supported(bad, "1.99.1", "2.0.0"), "accepted {bad}");
    }
}

/// Uses an installed toolchain, not synthetic output.
/// `CCOW_TEST_CARGO_TOOLCHAIN` selects an installed rustup toolchain.
#[test]
fn real_cargo_configured_directories() {
    let toolchain = std::env::var("CCOW_TEST_CARGO_TOOLCHAIN").unwrap_or_else(|_| "stable".into());
    let Ok(version) = std::process::Command::new("cargo")
        .arg(format!("+{toolchain}"))
        .arg("--version")
        .output()
    else {
        eprintln!("Cargo unavailable");
        return;
    };
    let text = String::from_utf8_lossy(&version.stdout).into_owned();
    let fields: Vec<&str> = text.split_whitespace().collect();
    if !version.status.success() || fields.len() < 2 || !cargo::supported_toolchain(fields[1]) {
        eprintln!("installed {toolchain} Cargo does not meet the supported floor: {text}");
        return;
    }
    let (_dir, p) = project();
    let rustup_home = std::process::Command::new("rustup")
        .args(["show", "home"])
        .output()
        .unwrap();
    let cargo_home = tempfile::tempdir().unwrap();
    let home = tempfile::tempdir().unwrap();
    let mut env: Vec<String> = std::env::vars()
        .filter(|(k, _)| !k.starts_with("CARGO_") && !k.starts_with("RUST") && k != "HOME")
        .map(|(k, v)| format!("{k}={v}"))
        .collect();
    env.push(format!("CARGO_HOME={}", s(cargo_home.path())));
    env.push(format!("HOME={}", s(home.path())));
    env.push(format!(
        "RUSTUP_HOME={}",
        String::from_utf8_lossy(&rustup_home.stdout).trim()
    ));
    for (path, content) in [
        (
            "Cargo.toml",
            "[package]\nname='real_ccow_probe'\nversion='0.1.0'\nedition='2024'\n",
        ),
        ("src/main.rs", "fn main() {}\n"),
        (
            ".gitignore",
            "configured-target/\nconfigured-build/\ncli-target/\n",
        ),
        (
            ".cargo/config.toml",
            "[build]\ntarget-dir='configured-target'\nbuild-dir='configured-build'\n",
        ),
    ] {
        let path = paths::join2(&p, path);
        fs::create_dir_all(paths::dir(&path)).unwrap();
        fs::write(&path, content).unwrap();
    }
    let root = paths::eval_symlinks(&p).unwrap();
    for over in [false, true] {
        let mut args = vec![
            "cargo".to_string(),
            format!("+{toolchain}"),
            "check".into(),
            "--offline".into(),
        ];
        let mut target = "configured-target";
        if over {
            args.extend(strings(&["--target-dir", "cli-target"]));
            target = "cli-target";
        }
        let got = discover(
            &ctx(),
            Request {
                cwd: p.clone(),
                argv: args.clone(),
                env: Some(env.clone()),
                preset: None,
                protected: Vec::new(),
                shim_dir: String::new(),
                runner: None,
            },
        )
        .unwrap();
        assert_eq!(got.roots.len(), 2);
        assert_eq!(got.roots[0].path, paths::join2(&root, target));
        assert_eq!(got.roots[1].path, paths::join2(&root, "configured-build"));
        let status = std::process::Command::new(&args[0])
            .args(&args[1..])
            .current_dir(&p)
            .env_clear()
            .envs(env.iter().filter_map(|e| e.split_once('=')))
            .status()
            .unwrap();
        assert!(status.success(), "real Cargo check failed");
        for r in &got.roots {
            fs::metadata(&r.path).unwrap();
        }
    }
}

#[test]
fn cargo_cache_selection_inputs() {
    for kind in [
        "local-change",
        "local-create",
        "ancestor",
        "home",
        "explicit",
        "manifest",
        "source-only",
    ] {
        let mut f = cargo_fixture();
        let mut path = paths::join(&[f.cwd.as_str(), ".cargo", "config.toml"]);
        let extra = tempfile::tempdir().unwrap();
        match kind {
            "ancestor" => {
                path = paths::join(&[paths::dir(&f.cwd).as_str(), ".cargo", "config.toml"])
            }
            "home" => {
                f.env.push(format!("CARGO_HOME={}", s(extra.path())));
                path = paths::join2(&s(extra.path()), "config.toml");
            }
            "explicit" => {
                path = paths::join2(&s(extra.path()), "extra.toml");
                f.argv.extend(["--config".to_string(), path.clone()]);
            }
            "manifest" => path = paths::join2(&f.cwd, "Cargo.toml"),
            _ => {}
        }
        fs::create_dir_all(paths::dir(&path)).unwrap();
        if kind != "local-create" {
            fs::write(&path, "[build]\ntarget-dir='target'\n").unwrap();
        }
        let result = f.discover().unwrap();
        if kind == "source-only" {
            path = paths::join2(&f.cwd, "main.rs");
        }
        fs::write(&path, "[build]\ntarget-dir='other'\n").unwrap();
        let e = verify_file_stamps(&result.inputs);
        if kind == "source-only" {
            e.unwrap();
        } else {
            assert!(e.is_err(), "{kind}: accepted changed cache selection");
        }
    }
}

#[test]
fn frontend_configuration_stamps() {
    for kind in ["changed", "created", "missing-inputs"] {
        let (f, mut data) = frontend_fixture();
        let config = paths::join2(&f.cwd, "rspack.config.js");
        if kind == "changed" {
            fs::write(&config, "cache-a").unwrap();
        }
        if kind == "missing-inputs" {
            data["result"].as_object_mut().unwrap().remove("inputs");
        }
        FakeRunner::set(&f.runner.frontend, data.to_string());
        let result = f.discover();
        if kind == "missing-inputs" {
            assert!(result.is_err(), "accepted undeclared configuration");
            continue;
        }
        let result = result.unwrap();
        verify_file_stamps(&result.inputs).unwrap();
        fs::write(&config, "cache-b").unwrap();
        assert!(
            verify_file_stamps(&result.inputs).is_err(),
            "{kind}: accepted stale selection"
        );
    }
}

#[test]
fn frontend_external_cache_alias_retarget() {
    let (f, mut data) = frontend_fixture();
    let a = paths::join(&[f.cwd.as_str(), ".cache", "a"]);
    let b = paths::join(&[f.cwd.as_str(), ".cache", "b"]);
    for p in [&a, &b] {
        fs::create_dir_all(p).unwrap();
    }
    let alias_dir = tempfile::tempdir().unwrap();
    let alias = s(&alias_dir.path().join("external-alias"));
    symlink(&a, &alias).unwrap();
    data["result"]["caches"] = json!([{"type": "persistent", "path": alias}]);
    FakeRunner::set(&f.runner.frontend, data.to_string());
    let result = f.discover().unwrap();
    verify_cache_paths(&result).unwrap();
    fs::remove_file(&alias).unwrap();
    symlink(&b, &alias).unwrap();
    assert!(
        verify_cache_paths(&result).is_err(),
        "retargeted alias retained old claims"
    );
}

#[test]
fn nightly_discovery_preserves_selection_and_compiler_identity() {
    let mut f = cargo_fixture();
    f.argv.insert(1, "+nightly-2026-09-28".into());
    FakeRunner::set(&f.runner.version, "cargo 1.101.0-nightly (fixture)");
    let compiler = |commit| {
        format!(
            "rustc 1.101.0-nightly ({commit})\nhost: aarch64-apple-darwin\nrelease: \
             1.101.0-nightly\n"
        )
    };
    FakeRunner::set(&f.runner.compiler, compiler("first"));
    let a = f.discover().unwrap();
    assert!(a.compatibility.verified());
    for call in f.runner.calls.lock().unwrap().iter() {
        assert_eq!(call.args[1], "+nightly-2026-09-28");
    }
    FakeRunner::set(&f.runner.compiler, compiler("second"));
    let b = f.discover().unwrap();
    assert!(!a.compatibility.compatible(&b.compatibility));
    FakeRunner::set(
        &f.runner.compiler,
        "rustc 1.99.1-nightly\nhost: aarch64-apple-darwin\n",
    );
    assert!(f.discover().is_err());
}

#[test]
fn sccache_identity_and_private_transport_compatibility() {
    use std::os::unix::fs::PermissionsExt;
    let mut f = cargo_fixture();
    let bin = paths::join2(&f.cwd, "bin");
    fs::create_dir(&bin).unwrap();
    let wrapper = paths::join2(&bin, "sccache");
    fs::write(&wrapper, "wrapper version one").unwrap();
    fs::set_permissions(&wrapper, fs::Permissions::from_mode(0o700)).unwrap();
    f.env
        .extend(strings(&[&format!("PATH={bin}"), "RUSTC_WRAPPER=sccache"]));
    FakeRunner::set(&f.runner.wrapper_version, "sccache 0.14.0");
    let first = f.discover().unwrap();
    assert_eq!(
        first.cargo_wrapper,
        Some(CargoWrapper::Sccache {
            executable: wrapper.clone()
        })
    );
    assert!(first.inputs.iter().any(|stamp| stamp.path == wrapper));
    // The pinned spelling and a different session transport must not break
    // nested same-cache admission or source compatibility.
    f.env = first.cargo_wrapper.as_ref().unwrap().environment(&f.env);
    f.env
        .push("SCCACHE_SERVER_UDS=/tmp/ccow-s-example/ipc.sccache".into());
    let nested = f.discover().unwrap();
    assert!(first.compatibility.compatible(&nested.compatibility));
    fs::write(&wrapper, "wrapper version two").unwrap();
    assert!(verify_file_stamps(&first.inputs).is_err());
    let changed = f.discover().unwrap();
    assert!(!first.compatibility.compatible(&changed.compatibility));
    for version in ["sccache 0.13.0", "sccache 0.15.0", "custom 0.14.0"] {
        FakeRunner::set(&f.runner.wrapper_version, version);
        assert!(f.discover().is_err(), "accepted {version}");
    }
    FakeRunner::set(&f.runner.wrapper_version, "sccache 0.14.0");
    let target = paths::join2(&f.cwd, "target");
    fs::create_dir(&target).unwrap();
    fs::write(paths::join2(&f.cwd, ".gitignore"), "target/\n").unwrap();
    let inside = paths::join2(&target, "sccache");
    fs::copy(&wrapper, &inside).unwrap();
    f.env = set_env(&f.env, "RUSTC_WRAPPER", &inside);
    assert!(
        f.discover()
            .unwrap_err()
            .to_string()
            .contains("protected storage")
    );
}
