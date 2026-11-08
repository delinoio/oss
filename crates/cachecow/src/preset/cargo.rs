// SPDX-License-Identifier: Apache-2.0
//! Cargo cache discovery (Go `cargo.go`).
//!
//! Cargo metadata owns config precedence, workspace selection, relative paths
//! and build-dir templates. Never reconstruct those rules from `Cargo.toml`.
//! <https://doc.rust-lang.org/cargo/commands/cargo-metadata.html>

use std::collections::BTreeMap;

use anyhow::{Result, bail};
use serde::Deserialize;
use serde_json::Value;

use super::{
    CargoWrapper, PresetResult, Resolved, Root, RootKind, digest, env_value, null_default,
    resolve_executable, set_env, supported,
};
use crate::{Context, paths, repository};

#[derive(Deserialize, Default)]
#[serde(default)]
struct Metadata {
    #[serde(deserialize_with = "null_default")]
    version: i64,
    #[serde(rename = "target_directory", deserialize_with = "null_default")]
    target: String,
    #[serde(rename = "build_directory", deserialize_with = "null_default")]
    build: String,
    #[serde(rename = "workspace_root", deserialize_with = "null_default")]
    workspace: String,
    #[serde(deserialize_with = "null_default")]
    packages: Vec<Package>,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct Package {
    #[serde(rename = "manifest_path", deserialize_with = "null_default")]
    manifest: String,
}

/// Nightly uses the same metadata contract, but its complete compiler identity
/// remains part of compatibility so different nightly builds never share
/// caches. Keep the stable floor strict: a prerelease of the floor is older
/// than it.
pub(super) fn supported_toolchain(version: &str) -> bool {
    match version.strip_suffix("-nightly") {
        Some(base) => supported(base, "1.99.1", "2.0.0") && base != "1.99.1",
        None => supported(version, "1.99.1", "2.0.0"),
    }
}

fn toml_string(s: &str) -> String {
    serde_json::to_string(s).unwrap_or_default()
}

fn cargo_home(req: &Resolved<'_>) -> Result<String> {
    let home = env_value(&req.env, "CARGO_HOME");
    if !home.is_empty() {
        return Ok(if paths::is_abs(home) {
            home.to_string()
        } else {
            paths::join2(&req.cwd, home)
        });
    }
    let mut home = env_value(&req.env, "HOME").to_string();
    if home.is_empty() {
        home = env_value(&req.env, "USERPROFILE").to_string();
    }
    if home.is_empty() {
        home = paths::user_home_dir()
            .map_err(|_| anyhow::anyhow!("cannot resolve Cargo home; set CARGO_HOME"))?;
    }
    Ok(paths::join2(&home, ".cargo"))
}

pub(super) fn discover(ctx: &Context, req: &Resolved<'_>, out: &mut PresetResult) -> Result<Value> {
    for key in [
        "CARGO_BUILD_RUSTC",
        "RUSTC_WORKSPACE_WRAPPER",
        "CARGO_BUILD_RUSTC_WRAPPER",
        "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER",
    ] {
        if !env_value(&req.env, key).is_empty() {
            bail!(
                "custom compiler wrappers cannot yet be verified; use the selected toolchain \
                 without wrappers"
            );
        }
    }
    let wrapper = env_value(&req.env, "RUSTC_WRAPPER");
    let mut wrapper_identity = None;
    if !wrapper.is_empty() {
        // Other wrappers may alter compiler arguments or run outside the
        // native owner. Only the tested sccache protocol is supported here.
        if !cfg!(unix) || paths::base(wrapper) != "sccache" {
            bail!(
                "custom compiler wrappers cannot yet be verified; supported wrapper: \
                 RUSTC_WRAPPER=sccache 0.14.x on Unix"
            );
        }
        let executable = resolve_executable(&req.cwd, &req.env, wrapper)?;
        let stamps = repository::capture_file_stamps(std::slice::from_ref(&executable))?;
        let bytes = req.runner.output(
            ctx,
            &req.cwd,
            &req.env,
            &[executable.clone(), "--version".into()],
        )?;
        let text = String::from_utf8_lossy(&bytes);
        let fields: Vec<_> = text.split_whitespace().collect();
        if fields.len() != 2 || fields[0] != "sccache" || !supported(fields[1], "0.14.0", "0.15.0")
        {
            bail!("cannot verify sccache 0.14.x; select a supported RUSTC_WRAPPER");
        }
        wrapper_identity =
            Some(serde_json::json!({"Version": fields[1], "Digest": stamps[0].digest}));
        out.inputs.extend(stamps);
        out.cargo_wrapper = Some(CargoWrapper::Sccache { executable });
    }
    let mut prefix = vec![req.argv[0].clone()];
    let mut start = 1;
    if req.argv.len() > 1 && req.argv[1].starts_with('+') {
        prefix.push(req.argv[1].clone());
        start += 1;
    }
    let mut version_args = prefix.clone();
    version_args.push("--version".into());
    let b = req.runner.output(ctx, &req.cwd, &req.env, &version_args)?;
    let text = String::from_utf8_lossy(&b);
    let words: Vec<&str> = text.split_whitespace().collect();
    if words.len() < 2 || words[0] != "cargo" || !supported_toolchain(words[1]) {
        bail!("Cargo requires 1.99.1 or later within 1.x; select a supported toolchain");
    }
    out.compatibility.tool_version = words[1].to_string();
    let mut args = prefix.clone();
    args.extend(["metadata", "--format-version", "1", "--no-deps"].map(String::from));
    let mut env = req.env.clone();
    let mut extra_configs = Vec::new();
    // Forward only discovery-relevant options. Runtime arguments remain
    // untouched. Directory CLI options become highest-precedence --config
    // overrides, because metadata does not accept every build option.
    let mut i = start;
    while i < req.argv.len() {
        let arg = &req.argv[i];
        if arg.starts_with("-Z") || arg.starts_with("-C") {
            bail!("unstable Cargo options cannot be verified; use stable Cargo without -Z or -C");
        }
        if arg == "--" {
            break;
        }
        let (key, inline_value) = match arg.split_once('=') {
            Some((k, v)) => (k, Some(v.to_string())),
            None => (arg.as_str(), None),
        };
        match key {
            "--manifest-path" | "--config" | "--target-dir" | "--build-dir" | "--features"
            | "-F" | "--target" => {
                let value = match inline_value {
                    Some(v) => v,
                    None => {
                        i += 1;
                        let Some(v) = req.argv.get(i) else {
                            bail!("Cargo discovery option has no value; correct the build command");
                        };
                        v.clone()
                    }
                };
                if value.is_empty() {
                    bail!("Cargo discovery option has an empty value; correct the build command");
                }
                match key {
                    "--config" => {
                        if value.contains('=') {
                            validate_config(&value)?;
                        }
                        args.extend([key.to_string(), value]);
                    }
                    "--target-dir" => {
                        env = set_env(&env, "CARGO_TARGET_DIR", &value);
                        extra_configs.push(format!("build.target-dir={}", toml_string(&value)));
                    }
                    "--build-dir" => {
                        extra_configs.push(format!("build.build-dir={}", toml_string(&value)));
                    }
                    "--target" => args.extend(["--filter-platform".to_string(), value]),
                    _ => args.extend([key.to_string(), value]),
                }
            }
            "--all-features" | "--no-default-features" | "--offline" | "--locked" | "--frozen" => {
                args.push(key.to_string());
            }
            "-Z" | "--unstable-features" | "-C" => {
                bail!("unstable Cargo settings are unsupported; select stable Cargo");
            }
            _ => {}
        }
        i += 1;
    }
    for config in extra_configs {
        args.extend(["--config".to_string(), config]);
    }
    let input_paths = input_paths(req, &args)?;
    out.inputs
        .extend(repository::capture_file_stamps(&input_paths)?);
    let b = req.runner.output(ctx, &req.cwd, &env, &args)?;
    let meta: Option<Metadata> = serde_json::from_slice(&b).ok();
    let Some(mut meta) = meta.filter(|m| {
        m.version == 1
            && paths::is_abs(&m.target)
            && paths::is_abs(&m.build)
            && paths::is_abs(&m.workspace)
    }) else {
        bail!(
            "Cargo metadata did not report verified target_directory and build_directory; use a \
             Cargo that reports both fields (no target-dir fallback is safe)"
        );
    };
    // Metadata from a manifest outside this repository must not authorize its
    // cache.
    match repository::discover(ctx, &meta.workspace) {
        Ok(w) if w.common_dir == out.repository.common_dir => {}
        _ => {
            bail!("Cargo workspace is outside the selected Git repository; correct --manifest-path")
        }
    }
    meta.workspace = repository::canonical(&meta.workspace)?;
    out.roots = vec![
        Root {
            path: meta.target.clone(),
            kinds: vec![RootKind::Target],
        },
        Root {
            path: meta.build.clone(),
            kinds: vec![RootKind::Build],
        },
    ];
    let mut rustc = env_value(&req.env, "RUSTC").to_string();
    if rustc.is_empty() {
        rustc = "rustc".into();
    }
    let mut compiler_args = vec![rustc.clone()];
    if prefix.len() > 1 && rustc == "rustc" {
        compiler_args.push(prefix[1].clone());
    }
    compiler_args.push("-vV".into());
    let b = req.runner.output(ctx, &req.cwd, &req.env, &compiler_args)?;
    let text = String::from_utf8_lossy(&b).into_owned();
    let fields: Vec<&str> = text.split_whitespace().collect();
    if fields.len() < 2
        || !supported_toolchain(fields[1])
        || !text.starts_with("rustc ")
        || !text.contains("\nhost: ")
    {
        bail!("cannot verify the rustc compiler; check the selected Rust toolchain");
    }
    out.compatibility.compiler_version = digest(&text);
    // Hash configuration bytes without storing them. Cargo still decides
    // source freshness; source files and artifact timestamps are never
    // rewritten.
    let mut configs = BTreeMap::new();
    let mut p = req.cwd.clone();
    loop {
        for name in ["config", "config.toml"] {
            hash_file(&mut configs, &paths::join(&[p.as_str(), ".cargo", name]))?;
        }
        let parent = paths::dir(&p);
        if parent == p {
            break;
        }
        p = parent;
    }
    let home = cargo_home(req)?;
    for name in ["config", "config.toml"] {
        hash_file(&mut configs, &paths::join2(&home, name))?;
    }
    // File-form --config can live outside the usual hierarchy.
    for pair in args.windows(2) {
        if pair[0] == "--config" && !pair[1].contains('=') {
            let p = if paths::is_abs(&pair[1]) {
                pair[1].clone()
            } else {
                paths::join2(&req.cwd, &pair[1])
            };
            hash_file(&mut configs, &p)?;
        }
    }
    // Preserve configuration position and precedence in the digest, while
    // allowing identical configuration in corresponding linked worktrees.
    let root = out.repository.root.clone();
    let mut manifests = BTreeMap::new();
    let mut manifest_paths = vec![paths::join2(&meta.workspace, "Cargo.toml")];
    manifest_paths.extend(meta.packages.iter().map(|p| p.manifest.clone()));
    for path in manifest_paths {
        let b = match std::fs::read(&path) {
            Ok(b) => b,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(_) => bail!("cannot verify Cargo manifest settings; check permissions"),
        };
        let canonical = repository::canonical(&path)?;
        manifests.insert(
            canonical.replace(root.as_str(), "${WORKTREE}"),
            digest(&String::from_utf8_lossy(&b)),
        );
    }
    let normalized: BTreeMap<String, String> = configs
        .into_iter()
        .map(|(k, v)| (k.replace(root.as_str(), "${WORKTREE}"), v))
        .collect();
    let mut settings = serde_json::json!({
        "Configs": normalized,
        "Manifests": manifests,
        "Workspace": relative_project(&root, &meta.workspace),
    });
    if let Some(wrapper) = wrapper_identity {
        settings["Wrapper"] = wrapper;
    }
    Ok(settings)
}

fn hash_file(dst: &mut BTreeMap<String, String>, path: &str) -> Result<()> {
    let b = match std::fs::read(path) {
        Ok(b) => b,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => bail!("cannot read Cargo configuration; check permissions"),
    };
    let text = String::from_utf8_lossy(&b).into_owned();
    validate_config(&text)?;
    dst.insert(path.to_string(), digest(&text));
    Ok(())
}

pub(super) fn validate_config(text: &str) -> Result<()> {
    let Ok(config) = text.parse::<toml::Table>() else {
        bail!("malformed Cargo configuration; correct the TOML file");
    };
    if config.contains_key("include") {
        bail!("Cargo config includes cannot yet be verified; use self-contained configuration");
    }
    if let Some(toml::Value::Table(build)) = config.get("build") {
        for key in ["rustc", "rustc-wrapper", "rustc-workspace-wrapper"] {
            if build.contains_key(key) {
                bail!(
                    "configured compiler or wrapper cannot yet be verified; use the selected \
                     toolchain"
                );
            }
        }
    }
    if let Some(toml::Value::Table(env)) = config.get("env") {
        for key in [
            "RUSTC",
            "RUSTUP_TOOLCHAIN",
            "RUSTC_WRAPPER",
            "RUSTC_WORKSPACE_WRAPPER",
        ] {
            if env.contains_key(key) {
                bail!("compiler overrides in Cargo config env cannot yet be verified");
            }
        }
    }
    Ok(())
}

fn relative_project(root: &str, path: &str) -> String {
    paths::rel(root, path).unwrap_or_else(|| path.to_string())
}

/// Captures candidates before metadata runs, including missing files whose
/// later creation changes Cargo's configuration precedence. They are checked
/// again after discovery and under the leases before Cargo rereads them.
fn input_paths(req: &Resolved<'_>, args: &[String]) -> Result<Vec<String>> {
    let mut out = Vec::new();
    let mut dir = req.cwd.clone();
    loop {
        out.push(paths::join2(&dir, "Cargo.toml"));
        out.push(paths::join2(&dir, "rust-toolchain"));
        out.push(paths::join2(&dir, "rust-toolchain.toml"));
        out.push(paths::join(&[dir.as_str(), ".cargo", "config"]));
        out.push(paths::join(&[dir.as_str(), ".cargo", "config.toml"]));
        let parent = paths::dir(&dir);
        if parent == dir {
            break;
        }
        dir = parent;
    }
    let home = cargo_home(req)?;
    out.push(paths::join2(&home, "config"));
    out.push(paths::join2(&home, "config.toml"));
    for pair in args.windows(2) {
        if pair[0] == "--manifest-path" || (pair[0] == "--config" && !pair[1].contains('=')) {
            out.push(if paths::is_abs(&pair[1]) {
                pair[1].clone()
            } else {
                paths::join2(&req.cwd, &pair[1])
            });
        }
    }
    Ok(out)
}
