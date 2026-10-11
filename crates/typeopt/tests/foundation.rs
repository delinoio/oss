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
