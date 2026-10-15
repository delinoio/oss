// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared fixture helpers for pass integration tests. Each fixture runs the
//! original TypeScript and the optimized output in Node and compares stdout,
//! so values, side effects, ordering and exceptions must all match.
#![allow(dead_code)]
use std::{
    fs,
    path::Path,
    process::{Command, Output},
};

use serde_json::Value;

/// Fixtures import siblings as `./dep.ts` so Node runs the sources directly;
/// emit rewrites those specifiers to `./dep.js`.
pub const TSCONFIG: &str = r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"rewriteRelativeImportExtensions":true}}"#;

/// A temporary project with `main.ts` plus optional extra modules.
pub fn project(files: &[(&str, &str)]) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    fs::write(temp.path().join("tsconfig.json"), TSCONFIG).unwrap();
    fs::write(temp.path().join("package.json"), r#"{"type":"module"}"#).unwrap();
    for (name, code) in files {
        let path = temp.path().join(name);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, code).unwrap();
    }
    temp
}

pub fn typeopt(root: &Path, args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(root)
        .args(["--project", "tsconfig.json", "--entry", "main.ts"])
        .args(args)
        .output()
        .unwrap()
}

/// Optimizes into `out` and asserts success.
pub fn optimize(root: &Path, extra: &[&str]) -> Value {
    let mut args = vec!["--out-dir", "out", "--verbose"];
    args.extend_from_slice(extra);
    let output = typeopt(root, &args);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    serde_json::from_slice(&fs::read(root.join("out/report.json")).unwrap()).unwrap()
}

/// Analyzes without publishing and returns the report.
pub fn check(root: &Path, extra: &[&str]) -> Value {
    let mut args = vec!["--check"];
    args.extend_from_slice(extra);
    let output = typeopt(root, &args);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    serde_json::from_slice(&output.stdout).unwrap()
}

pub fn modules(root: &Path) -> Value {
    serde_json::from_slice(&fs::read(root.join("out/modules.json")).unwrap()).unwrap()
}

/// Emitted output path for a source module name such as `main.ts`.
pub fn output_of(root: &Path, source: &str) -> std::path::PathBuf {
    let manifest = modules(root);
    let source = root.join(source).canonicalize().unwrap();
    let entry = manifest["modules"]
        .as_array()
        .unwrap()
        .iter()
        .find(|m| Path::new(m["source"].as_str().unwrap()) == source)
        .unwrap_or_else(|| panic!("no module for {}", source.display()));
    root.join("out").join(entry["output"].as_str().unwrap())
}

pub fn emitted(root: &Path, source: &str) -> String {
    fs::read_to_string(output_of(root, source)).unwrap()
}

/// Copies emitted modules next to each other under their source names with
/// `.js` extensions so relative imports keep working, then runs the entry.
pub fn run_optimized(root: &Path) -> Output {
    let manifest = modules(root);
    let run = root.join("run");
    let _ = fs::remove_dir_all(&run);
    fs::create_dir_all(&run).unwrap();
    fs::write(run.join("package.json"), r#"{"type":"module"}"#).unwrap();
    let base = root.canonicalize().unwrap();
    for module in manifest["modules"].as_array().unwrap() {
        let source = Path::new(module["source"].as_str().unwrap());
        let relative = source.strip_prefix(&base).unwrap();
        let target = if module["kind"] == "passthrough" {
            run.join(relative)
        } else {
            run.join(relative).with_extension("js")
        };
        fs::create_dir_all(target.parent().unwrap()).unwrap();
        let code = if module["kind"] == "passthrough" {
            fs::read_to_string(source).unwrap()
        } else {
            fs::read_to_string(root.join("out").join(module["output"].as_str().unwrap())).unwrap()
        };
        fs::write(&target, code).unwrap();
    }
    // External packages are not rewritten; the optimized program resolves
    // them from a verbatim copy of the project's `node_modules`.
    let packages = root.join("node_modules");
    if packages.is_dir() {
        copy_dir(&packages, &run.join("node_modules"));
    }
    copy_json_assets(root, &run);
    Command::new("node")
        .arg(run.join("main.js"))
        .output()
        .unwrap()
}

/// JSON imports stay in the original project, so the Node fixture needs the
/// same data files beside the emitted modules.
fn copy_json_assets(from: &Path, to: &Path) {
    for entry in fs::read_dir(from).unwrap() {
        let entry = entry.unwrap();
        let name = entry.file_name();
        if entry.file_type().unwrap().is_dir() {
            if !matches!(name.to_str(), Some("out" | "run" | "node_modules")) {
                copy_json_assets(&entry.path(), &to.join(name));
            }
        } else if entry.path().extension().is_some_and(|ext| ext == "json")
            && !matches!(name.to_str(), Some("package.json" | "tsconfig.json"))
        {
            fs::create_dir_all(to).unwrap();
            fs::copy(entry.path(), to.join(name)).unwrap();
        }
    }
}

fn copy_dir(from: &Path, to: &Path) {
    fs::create_dir_all(to).unwrap();
    for entry in fs::read_dir(from).unwrap() {
        let entry = entry.unwrap();
        let target = to.join(entry.file_name());
        if entry.file_type().unwrap().is_dir() {
            copy_dir(&entry.path(), &target);
        } else {
            fs::copy(entry.path(), target).unwrap();
        }
    }
}

/// Runs the original sources and the optimized output and asserts equal
/// stdout, stderr exit status and success.
pub fn same_behavior(root: &Path) {
    let before = Command::new("node")
        .arg(root.join("main.ts"))
        .output()
        .unwrap();
    let after = run_optimized(root);
    assert_eq!(
        before.status.success(),
        after.status.success(),
        "before: {}\nafter: {}",
        String::from_utf8_lossy(&before.stderr),
        String::from_utf8_lossy(&after.stderr)
    );
    assert_eq!(
        String::from_utf8_lossy(&before.stdout),
        String::from_utf8_lossy(&after.stdout)
    );
}

/// Number of accepted bundles whose primary pass is `pass`.
pub fn applied(report: &Value, pass: &str) -> u64 {
    report["pass_summary"][pass]["applied"].as_u64().unwrap()
}

pub fn skipped_reasons(report: &Value, pass: &str) -> Vec<String> {
    report["skipped"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|s| s["pass"] == pass)
        .map(|s| s["reason"].as_str().unwrap().to_owned())
        .collect()
}

/// Every pass ID except `keep`, as `--disable-pass` arguments.
pub fn only(keep: &[&str]) -> Vec<String> {
    const ALL: [&str; 18] = [
        "property-shortening",
        "property-disambiguation",
        "property-ambiguation",
        "finite-key-rewriting",
        "unused-method-removal",
        "unused-field-removal",
        "constant-property-inlining",
        "method-devirtualization",
        "call-inlining",
        "function-specialization",
        "signature-reduction",
        "type-directed-control-flow",
        "primitive-operation-simplification",
        "enum-runtime-reduction",
        "discriminant-compaction",
        "aggregate-elimination",
        "intrinsic-specialization",
        "residual-dependency-removal",
    ];
    ALL.iter()
        .filter(|p| !keep.contains(p))
        .flat_map(|p| ["--disable-pass".to_owned(), (*p).to_owned()])
        .collect()
}

pub fn args(owned: &[String]) -> Vec<&str> {
    owned.iter().map(String::as_str).collect()
}

/// Size saved by the whole run in emitted ESM bytes.
pub fn saved(report: &Value) -> i64 {
    report["size"]["saved"].as_i64().unwrap()
}
