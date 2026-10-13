// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared pipeline, CLI and report behavior independent of any one pass.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use common::*;
use serde_json::Value;

const CROSS_MODULE: &[(&str, &str)] = &[
    (
        "main.ts",
        "import {make, read} from './dep.ts'; const value = make(); console.log(read(value));",
    ),
    (
        "dep.ts",
        "export function make() { return {longProperty: 41}; } export function read(v: \
         {longProperty: number}) { return v.longProperty + 1; }",
    ),
];

fn without_measurements(mut report: Value) -> Value {
    report.as_object_mut().unwrap().remove("measurements");
    report
}

#[test]
fn check_and_output_make_identical_decisions() {
    let temp = project(CROSS_MODULE);
    let checked = check(temp.path(), &[]);
    let written = optimize(temp.path(), &[]);
    assert_eq!(
        without_measurements(checked),
        without_measurements(written.clone())
    );
    assert_eq!(written["schema_version"], 1);
    assert_eq!(written["passes"]["enabled"].as_array().unwrap().len(), 18);
    same_behavior(temp.path());
    let manifest = modules(temp.path());
    assert_eq!(manifest["schema_version"], 0);
    let inputs = manifest["inputs"].as_array().unwrap();
    assert!(
        inputs.iter().any(|i| i["kind"] == "configuration"
            && i["path"].as_str().unwrap().ends_with("tsconfig.json"))
    );
}

#[test]
fn repeated_and_unknown_pass_exclusions() {
    let temp = project(CROSS_MODULE);
    let output = typeopt(temp.path(), &["--check", "--disable-pass", "no-such-pass"]);
    assert_eq!(output.status.code(), Some(2));
    let report = check(
        temp.path(),
        &[
            "--disable-pass",
            "call-inlining",
            "--disable-pass",
            "call-inlining",
            "--disable-pass",
            "enum-runtime-reduction",
        ],
    );
    assert_eq!(
        report["passes"]["disabled"],
        serde_json::json!(["call-inlining", "enum-runtime-reduction"])
    );
}

#[test]
fn all_passes_disabled_is_a_valid_zero_change_result() {
    let temp = project(CROSS_MODULE);
    let report = optimize(temp.path(), &args(&only(&[])));
    assert_eq!(report["transformations"].as_array().unwrap().len(), 0);
    assert_eq!(saved(&report), 0);
    assert!(report["no_change_reason"].is_string());
    for module in modules(temp.path())["modules"].as_array().unwrap() {
        assert_ne!(module["kind"], "optimized");
    }
    same_behavior(temp.path());
}

#[test]
fn reports_are_deterministic() {
    let temp = project(CROSS_MODULE);
    let first = without_measurements(check(temp.path(), &[]));
    let second = without_measurements(check(temp.path(), &[]));
    assert_eq!(first, second);
}

#[test]
fn snapshot_records_module_resolution_metadata() {
    let temp = project(&[
        (
            "main.ts",
            "import type { Value } from 'dep-pkg';\nconst value: Value = 1;\nconsole.log(value);\n",
        ),
        (
            "node_modules/dep-pkg/package.json",
            r#"{"name":"dep-pkg","types":"./types.d.ts"}"#,
        ),
        (
            "node_modules/dep-pkg/types.d.ts",
            "export type Value = number;\n",
        ),
    ]);
    optimize(temp.path(), &[]);
    let manifest = modules(temp.path());
    let inputs = manifest["inputs"].as_array().unwrap();
    for suffix in ["dep-pkg/package.json", "dep-pkg/types.d.ts"] {
        assert!(
            inputs
                .iter()
                .any(|i| i["path"].as_str().unwrap().ends_with(suffix)
                    && i["sha256"].as_str().unwrap().len() == 64),
            "{suffix} missing from {inputs:#?}"
        );
    }
}

const JSX_TSCONFIG: &str = r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"jsx":"react-jsx"}}"#;

/// A project whose `./dep` import resolves to `dep.tsx` after probing the
/// higher-priority `dep.ts` candidate, which is absent.
fn shadowed_dependency() -> tempfile::TempDir {
    let temp = project(&[
        (
            "main.ts",
            "import { answer } from './dep'; console.log(answer());",
        ),
        ("dep.tsx", "export function answer() { return 1; }"),
    ]);
    std::fs::write(temp.path().join("tsconfig.json"), JSX_TSCONFIG).unwrap();
    temp
}

fn absent_paths(manifest: &Value) -> Vec<String> {
    manifest["inputs"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|i| i["kind"] == "absent")
        .map(|i| {
            assert!(i.get("sha256").is_none(), "absent input has a digest: {i}");
            i["path"].as_str().unwrap().to_owned()
        })
        .collect()
}

#[test]
fn snapshot_records_absent_higher_priority_resolution_candidates() {
    let temp = shadowed_dependency();
    optimize(temp.path(), &[]);
    let manifest = modules(temp.path());
    let absent = absent_paths(&manifest);
    assert!(
        absent.iter().any(|p| p.ends_with("/dep.ts")),
        "dep.ts missing from {absent:#?}"
    );
    // Existing files and compiler-owned declarations are never absent inputs.
    assert!(absent.iter().all(|p| !p.ends_with("/dep.tsx")));
    assert!(absent.iter().all(|p| !p.starts_with("bundled:")));
    for path in &absent {
        assert!(
            !std::path::Path::new(path).exists(),
            "{path} exists on disk"
        );
    }
    // Absent entries follow every positive input; each group is path-sorted.
    let inputs = manifest["inputs"].as_array().unwrap();
    let first_absent = inputs.iter().position(|i| i["kind"] == "absent").unwrap();
    assert!(inputs[first_absent..].iter().all(|i| i["kind"] == "absent"));
    for group in [&inputs[..first_absent], &inputs[first_absent..]] {
        let paths: Vec<_> = group.iter().map(|i| i["path"].as_str().unwrap()).collect();
        let mut sorted = paths.clone();
        sorted.sort();
        sorted.dedup();
        assert_eq!(paths, sorted);
    }
    // The resolved candidate is an analyzed module with a positive digest.
    assert!(
        manifest["modules"]
            .as_array()
            .unwrap()
            .iter()
            .any(|m| m["source"].as_str().unwrap().ends_with("/dep.tsx"))
    );
}

#[test]
fn dangling_symlink_resolution_candidates_are_recorded_as_absent() {
    let temp = shadowed_dependency();
    // `dep.ts` outranks `dep.tsx` but its target does not exist yet, so the
    // compiler resolves past it. Creating the target would change resolution.
    let candidate = temp.path().join("dep.ts");
    std::os::unix::fs::symlink(temp.path().join("later-target.ts"), &candidate).unwrap();
    assert!(candidate.symlink_metadata().is_ok() && !candidate.exists());
    optimize(temp.path(), &[]);
    let absent = absent_paths(&modules(temp.path()));
    assert!(
        absent.iter().any(|p| p.ends_with("/dep.ts")),
        "dangling dep.ts missing from {absent:#?}"
    );
}

#[test]
fn wrong_kind_resolution_candidates_are_recorded_with_the_kind_that_satisfies_the_probe() {
    let temp = shadowed_dependency();
    // A directory named `dep.ts` fails the file probe, so `./dep` resolves to
    // `dep.tsx`. Replacing the directory with a file would change resolution.
    let candidate = temp.path().join("dep.ts");
    std::fs::create_dir(&candidate).unwrap();
    optimize(temp.path(), &[]);
    let manifest = modules(temp.path());
    let entry = manifest["inputs"]
        .as_array()
        .unwrap()
        .iter()
        .find(|i| i["path"].as_str().unwrap().ends_with("/dep.ts"))
        .unwrap_or_else(|| panic!("dep.ts missing from {:#?}", manifest["inputs"]))
        .clone();
    assert_eq!(entry["kind"], "mismatch", "{entry}");
    assert_eq!(entry["becomes"], "file", "{entry}");
    assert!(entry.get("sha256").is_none(), "{entry}");
    // It is not a false absence: the path exists, so an absent entry would
    // reject output that is still valid.
    assert!(
        !absent_paths(&manifest)
            .iter()
            .any(|p| p.ends_with("/dep.ts"))
    );
}

#[test]
fn absent_candidate_snapshot_is_deterministic() {
    let temp = shadowed_dependency();
    optimize(temp.path(), &[]);
    let first = std::fs::read(temp.path().join("out/modules.json")).unwrap();
    std::fs::remove_dir_all(temp.path().join("out")).unwrap();
    optimize(temp.path(), &[]);
    let second = std::fs::read(temp.path().join("out/modules.json")).unwrap();
    assert_eq!(first, second);
}

#[test]
fn package_resolution_misses_are_recorded_as_absent() {
    let temp = project(&[(
        "main.ts",
        "import type { Value } from 'dep-pkg'; export const v: Value | undefined = undefined;",
    )]);
    std::fs::create_dir_all(temp.path().join("node_modules/dep-pkg")).unwrap();
    std::fs::write(
        temp.path().join("node_modules/dep-pkg/index.d.ts"),
        "export type Value = number;",
    )
    .unwrap();
    optimize(temp.path(), &[]);
    let absent = absent_paths(&modules(temp.path()));
    // Higher-priority package candidates that do not exist, such as a
    // sibling declaration file or a manifest, are retained too.
    for suffix in [
        "node_modules/dep-pkg.d.ts",
        "node_modules/dep-pkg/index.ts",
        "node_modules/dep-pkg/package.json",
    ] {
        assert!(
            absent.iter().any(|p| p.ends_with(suffix)),
            "{suffix} missing from {absent:#?}"
        );
    }
}
