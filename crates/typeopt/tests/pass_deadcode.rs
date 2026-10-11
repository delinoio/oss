// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Dead-code family: unused-method-removal, unused-field-removal and
//! residual-dependency-removal. Every fixture compares Node behavior of the
//! original sources and the optimized output.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use common::*;
use serde_json::Value;

const METHOD: &str = "unused-method-removal";
const FIELD: &str = "unused-field-removal";
const RESIDUAL: &str = "residual-dependency-removal";

fn run(files: &[(&str, &str)], keep: &[&str]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(keep)));
    same_behavior(temp.path());
    (temp, report)
}

fn run_all(files: &[(&str, &str)]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    (temp, report)
}

// ---------------------------------------------------------------------------
// residual-dependency-removal

/// Fixtures never import a binding without using it at runtime: Node's
/// type stripping keeps such imports while the TypeScript emit elides them.
const RESIDUAL_CROSS: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { used } from "./dep.ts";
import { liveReexport } from "./reexport.ts";
/** Never called. */
function deadLocal(): number {
  return deadLocal() + chainedHelper();
}
function chainedHelper(): number { return 2; }
const deadConst = { a: 1, b: [2, 3] }, liveConst = 5;
console.log(used(), liveConst, liveReexport());
"#,
    ),
    (
        "dep.ts",
        r#"import { log } from "./log.ts";
export function used() { return 1; }
export function unusedHelper() { log("x"); return 2; }
export class UnusedClass { static count = 1; read() { return UnusedClass.count; } }
export const impure = log("init");
"#,
    ),
    (
        "log.ts",
        r#"export function log(v: string) { console.log("log", v); return v; }
console.log("log module init");
"#,
    ),
    (
        "reexport.ts",
        r#"export { liveReexport, deadReexport } from "./other.ts";
export { onlyDead } from "./side.ts";
"#,
    ),
    (
        "other.ts",
        r#"export function liveReexport() { return 3; }
export function deadReexport() { return 4; }
console.log("other init");
"#,
    ),
    (
        "side.ts",
        "export function onlyDead() { return 5; }\nconsole.log(\"side init\");\n",
    ),
];

#[test]
fn residual_removes_unreferenced_declarations_across_modules() {
    let (temp, report) = run(RESIDUAL_CROSS, &[RESIDUAL]);
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("deadLocal"), "{main}");
    assert!(!main.contains("chainedHelper"), "{main}");
    assert!(!main.contains("deadConst"), "{main}");
    assert!(main.contains("liveConst"), "{main}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(!dep.contains("unusedHelper"), "{dep}");
    assert!(!dep.contains("UnusedClass"), "{dep}");
    // The impure initializer and the module initialization stay.
    assert!(dep.contains("log(\"init\")"), "{dep}");
    let reexport = emitted(temp.path(), "reexport.ts");
    assert!(!reexport.contains("deadReexport"), "{reexport}");
    assert!(!reexport.contains("onlyDead"), "{reexport}");
    // The side-effecting module stays a dependency.
    assert!(reexport.contains("import \"./side.js\""), "{reexport}");
    let other = emitted(temp.path(), "other.ts");
    assert!(!other.contains("deadReexport"), "{other}");
    assert!(!emitted(temp.path(), "side.ts").contains("onlyDead"));
    assert!(saved(&report) > 0);
}

#[test]
fn residual_with_all_passes_enabled() {
    let (_temp, report) = run_all(RESIDUAL_CROSS);
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
}

#[test]
fn residual_disabled_never_runs() {
    let (temp, report) = run(RESIDUAL_CROSS, &[METHOD, FIELD]);
    assert_eq!(applied(&report, RESIDUAL), 0);
    assert!(emitted_or_source(temp.path(), "main.ts").contains("deadLocal"));
}

/// Emitted output when the module was optimized, otherwise its source.
fn emitted_or_source(root: &std::path::Path, source: &str) -> String {
    let manifest = modules(root);
    let path = root.join(source).canonicalize().unwrap();
    let module = manifest["modules"]
        .as_array()
        .unwrap()
        .iter()
        .find(|m| std::path::Path::new(m["source"].as_str().unwrap()) == path)
        .unwrap();
    if module["kind"] == "optimized" {
        emitted(root, source)
    } else {
        std::fs::read_to_string(path).unwrap()
    }
}
