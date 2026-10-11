// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `method-devirtualization` and `call-inlining` fixtures. Every fixture
//! compares Node execution of the original and optimized output.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use common::*;

const INLINE: &str = "call-inlining";
const DEVIRT: &str = "method-devirtualization";
const RESIDUAL: &str = "residual-dependency-removal";

fn applied_count(report: &serde_json::Value, pass: &str) -> u64 {
    applied(report, pass)
}

// ---------------------------------------------------------------------------
// call-inlining: positive fixtures
// ---------------------------------------------------------------------------

const SMALL_FUNCTIONS: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { average, isFreedraw, scaled } from "./geometry.ts";
const values = [1, 2, 3];
function log(label: string): number {
  console.log("eval", label);
  return values.length;
}
console.log(average(values[0], values[2]));
console.log(average(log("left"), log("right")));
console.log(isFreedraw("freedraw"), isFreedraw(String(log("type"))));
const factor = 3;
console.log(scaled(factor), scaled(log("scaled")));
"#,
    ),
    (
        "geometry.ts",
        r#"export const average = (a: number, b: number) => (a + b) / 2;
export function isFreedraw(type: string) {
  return type === "freedraw";
}
export const scaled = (value: number): number => value * 2 + 1;
"#,
    ),
];

#[test]
fn inlines_small_functions_across_modules_with_cleanup() {
    let temp = project(SMALL_FUNCTIONS);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) >= 3, "{report:#}");
    assert!(saved(&report) > 0);
    let geometry = emitted(temp.path(), "geometry.ts");
    assert!(!geometry.contains("average"), "{geometry}");
    assert!(!geometry.contains("isFreedraw"), "{geometry}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("(values[0] + values[2]) / 2"), "{main}");
    assert!(main.contains("=== \"freedraw\""), "{main}");
    // The bundle records the declaration removal as cleanup.
    let bundle = report["transformations"]
        .as_array()
        .unwrap()
        .iter()
        .find(|t| t["pass"] == INLINE)
        .unwrap();
    assert_eq!(bundle["uses"], serde_json::json!([RESIDUAL]), "{bundle:#}");
    same_behavior(temp.path());
}

#[test]
fn inlines_small_functions_with_all_passes() {
    let temp = project(SMALL_FUNCTIONS);
    let report = optimize(temp.path(), &[]);
    assert!(applied_count(&report, INLINE) >= 3, "{report:#}");
    same_behavior(temp.path());
}

#[test]
fn inlines_call_sites_without_disabled_cleanup() {
    let temp = project(SMALL_FUNCTIONS);
    let report = optimize(temp.path(), &args(&only(&[INLINE])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    for bundle in report["transformations"].as_array().unwrap() {
        assert!(
            bundle["uses"].as_array().unwrap().is_empty(),
            "a disabled cleanup ran: {bundle:#}"
        );
    }
    // The declarations stay because residual-dependency-removal is off.
    let geometry = emitted(temp.path(), "geometry.ts");
    assert!(geometry.contains("isFreedraw"), "{geometry}");
    same_behavior(temp.path());
}

#[test]
fn call_inlining_never_runs_when_disabled() {
    let temp = project(SMALL_FUNCTIONS);
    let all_but = optimize(temp.path(), &["--disable-pass", INLINE]);
    assert_eq!(applied_count(&all_but, INLINE), 0);
    assert!(
        all_but["transformations"]
            .as_array()
            .unwrap()
            .iter()
            .all(|t| t["pass"] != INLINE
                && !t["uses"].as_array().unwrap().iter().any(|u| u == INLINE))
    );
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("average("), "{main}");
    same_behavior(temp.path());
    let temp = project(SMALL_FUNCTIONS);
    let cleanup_only = optimize(temp.path(), &args(&only(&[RESIDUAL])));
    assert_eq!(applied_count(&cleanup_only, INLINE), 0);
    same_behavior(temp.path());
}

#[test]
fn inlines_local_function_declarations() {
    let temp = project(&[(
        "main.ts",
        r#"function square(value: number) {
  return value * value;
}
function describeLongName(name: string, count: number) {
  return name + ":" + count;
}
const side = 7;
let total = 0;
for (let i = 0; i < 3; i++) {
  total += square(side) + square(i);
}
console.log(total, describeLongName("items", total));
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("function square"), "{main}");
    assert!(main.contains("side * side + i * i"), "{main}");
    // `total` changes inside the loop and `+` may run user code first.
    assert!(
        main.contains("describeLongName(\"items\", total)"),
        "{main}"
    );
    same_behavior(temp.path());
}

// ---------------------------------------------------------------------------
// call-inlining: safety boundaries
// ---------------------------------------------------------------------------

/// Runs a fixture with call inlining (and cleanup) and asserts that the
/// named function's call sites stay calls.
fn assert_not_inlined(files: &[(&str, &str)], call: &str) -> serde_json::Value {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(
        main.contains(call),
        "{call} was inlined:\n{main}\n{report:#}"
    );
    same_behavior(temp.path());
    report
}

#[test]
fn preserves_effect_order_against_the_body() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"let counter = 0;
function bump(): number {
  counter += 1;
  console.log("bump", counter);
  return counter;
}
function afterBump(value: number) {
  return bump() + value;
}
console.log(afterBump(bump()), afterBump(bump()));
"#,
        )],
        "afterBump(",
    );
}

#[test]
fn preserves_argument_order_and_conditional_evaluation() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"function log(label: string, value: number): number {
  console.log(label);
  return value;
}
function swapped(first: number, second: number) {
  return second - first;
}
function guarded(flag: boolean, value: number) {
  return flag && value > 0;
}
console.log(swapped(log("a", 1), log("b", 2)), swapped(log("c", 3), log("d", 4)));
console.log(guarded(false, log("never", 1)), guarded(true, log("once", 2)));
"#,
        )],
        "swapped(",
    );
}

#[test]
fn preserves_evaluation_count() {
    let report = assert_not_inlined(
        &[(
            "main.ts",
            r#"function next(): number {
  console.log("next");
  return 2;
}
function twice(value: number) {
  return value + value;
}
function ignore(value: number) {
  return 1;
}
console.log(twice(next()), twice(next()), ignore(next()), ignore(next()));
"#,
        )],
        "twice(",
    );
    assert!(skipped_reasons(&report, INLINE).contains(&"side_effects".to_owned()));
}

#[test]
fn preserves_recursion_this_and_arguments() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"function factorial(n: number): number {
  return n <= 1 ? 1 : n * factorial(n - 1);
}
function receiver(this: unknown, value: number) {
  return value;
}
function count(value: number) {
  return arguments.length + value;
}
console.log(factorial(5), factorial(3), receiver.call(1, 2), count(1), count(2));
"#,
        )],
        "factorial(5)",
    );
}

#[test]
fn preserves_shadowed_free_names() {
    let report = assert_not_inlined(
        &[
            (
                "main.ts",
                r#"import { applyScale } from "./scale.ts";
const scale = 100;
console.log(applyScale(2), applyScale(3), scale);
"#,
            ),
            (
                "scale.ts",
                r#"const scale = 10;
export function applyScale(value: number) {
  return value * scale;
}
"#,
            ),
        ],
        "applyScale(",
    );
    assert!(skipped_reasons(&report, INLINE).contains(&"unsupported_syntax".to_owned()));
}

#[test]
fn preserves_temporal_dead_zone_errors() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"function early() {
  try {
    return late(1);
  } catch (error) {
    return (error as Error).name;
  }
}
console.log(early());
const late = (value: number) => value + 1;
console.log(late(2), late(3));
"#,
        )],
        "late(1)",
    );
}

#[test]
fn preserves_observed_function_identity() {
    let temp = project(&[(
        "main.ts",
        r#"function plusOne(value: number) {
  return value + 1;
}
const alias = plusOne;
console.log(plusOne(1), plusOne(2), alias.name, alias.length, alias === plusOne);
"#,
    )]);
    optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function plusOne"), "{main}");
    same_behavior(temp.path());
}

#[test]
fn preserves_public_entry_exports() {
    let temp = project(&[(
        "main.ts",
        r#"export function publicHelper(value: number) {
  return value * 3;
}
console.log(publicHelper(2), publicHelper(5));
"#,
    )]);
    optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("export function publicHelper"), "{main}");
    same_behavior(temp.path());
}

#[test]
fn preserves_async_generators_and_type_predicates() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"async function later(value: number) {
  return value + 1;
}
function* numbers(value: number) {
  return value;
}
function isText(value: string | number): value is string {
  return typeof value === "string";
}
const input: string | number = Math.random() > 2 ? 1 : "text";
later(1).then((v) => console.log("later", v));
console.log(numbers(1).next().value, isText(input) ? input.length : 0, isText(3));
"#,
        )],
        "isText(input)",
    );
}

#[test]
fn preserves_member_callee_this_binding() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"const target = {
  name: "target",
  read(this: { name: string } | void) {
    return this ? this.name : "detached";
  },
};
function pick(source: typeof target) {
  return source.read;
}
console.log(pick(target)(), pick(target)());
"#,
        )],
        "pick(target)",
    );
}

#[test]
fn keeps_module_initialization_when_removing_imports() {
    let temp = project(&[
        (
            "main.ts",
            r#"import { double } from "./effects.ts";
console.log("main", double(21));
"#,
        ),
        (
            "effects.ts",
            r#"console.log("effects initialized");
export function double(value: number) {
  return value * 2;
}
"#,
        ),
    ]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("import \"./effects.js\""), "{main}");
    same_behavior(temp.path());
}
