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
    // `total` changes inside the loop, but only module-level code writes it,
    // so user code run by `+` cannot change it before the body reads it.
    assert!(main.contains("\"items\" + \":\" + total"), "{main}");
    same_behavior(temp.path());
}

#[test]
fn inlines_with_imports_of_exported_helpers() {
    let temp = project(&[
        (
            "main.ts",
            r#"import { toUnits } from "./units.ts";
const input = 3;
console.log(toUnits(input), toUnits(input + 1));
"#,
        ),
        (
            "units.ts",
            r#"export const UNIT_FACTOR = 12;
export function convertWithFactor(value: number) {
  return value * UNIT_FACTOR + 1;
}
export const toUnits = (value: number) => convertWithFactor(value) * UNIT_FACTOR;
"#,
        ),
    ]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("UNIT_FACTOR"), "{main}");
    assert!(!main.contains("toUnits("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn inlines_function_local_constants_after_their_declaration() {
    let temp = project(&[(
        "main.ts",
        r#"function summarize(values: number[]) {
  const report = () => {
    try {
      return describeValue(values.length);
    } catch (error) {
      return (error as Error).name;
    }
  };
  const early = report();
  const describeValue = (count: number) => "count=" + count;
  const halfLength = (count: number) => count / 2;
  return [early, describeValue(values.length), halfLength(values.length)].join(" ");
}
console.log(summarize([1, 2, 3]));
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("halfLength"), "{main}");
    // `report` may run before `describeValue` is initialized.
    assert!(main.contains("describeValue(values.length)"), "{main}");
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
fn keeps_reads_of_bindings_that_other_functions_write() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"let shared = 0;
function bump(): number {
  shared += 1;
  console.log("bump", shared);
  return shared;
}
function after(value: number) {
  return bump() + value;
}
console.log(after(shared), after(shared));
"#,
        )],
        "after(",
    );
}

#[test]
fn inlines_reads_of_function_private_mutable_bindings_after_body_effects() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"function log(label: string): number {
  console.log(label);
  return label.length;
}
function tagged(value: number) {
  return log("tag") + value;
}
function run(start: number) {
  let local = start;
  local += 1;
  return tagged(local);
}
console.log(run(1), run(5));
"#,
    )];
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("log(\"tag\") + local"), "{main}\n{report:#}");
    assert!(!main.contains("tagged("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn keeps_sites_that_run_before_a_top_level_declaration() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"function early(n: number) {
  return double(n) + 1;
}
try {
  console.log(early(1));
} catch (error) {
  console.log("before", (error as Error).name);
}
const double = (value: number) => value * 2;
console.log(early(2));
"#,
        )],
        "double(",
    );
}

#[test]
fn keeps_sites_in_namespace_members_that_run_before_a_top_level_declaration() {
    // Node cannot strip namespaces, so compare baseline and optimized emit.
    let files = &[(
        "main.ts",
        r#"function attempt(f: () => number): number {
  try {
    return f();
  } catch {
    return -1;
  }
}
namespace N {
  export function run() {
    return late(1);
  }
}
console.log(attempt(N.run));
const late = (value: number) => value * 2;
console.log(attempt(N.run));
"#,
    )];
    let baseline = project(files);
    optimize(baseline.path(), &args(&only(&[])));
    let original = run_optimized(baseline.path());
    assert!(
        original.status.success(),
        "{}",
        String::from_utf8_lossy(&original.stderr)
    );
    assert_eq!(original.stdout, b"-1\n2\n");

    let temp = project(files);
    optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("late("), "{main}");
    let optimized = run_optimized(temp.path());
    assert!(
        optimized.status.success(),
        "{}",
        String::from_utf8_lossy(&optimized.stderr)
    );
    assert_eq!(optimized.stdout, original.stdout, "{main}");
}

#[test]
fn keeps_function_valued_namespace_exports_before_a_late_binding() {
    for kind in ["const", "let"] {
        for value in [
            "(value: number) => late(value)",
            "function(value: number) { return late(value); }",
        ] {
            let source = format!(
                "namespace N {{ export {kind} run = {value}; }}\ntry {{ console.log(\"early\", \
                 N.run(1)); }}\ncatch (error) {{ console.log(\"early\", (error as Error).name); \
                 }}\nconst late = (value: number) => value * 2;\nconsole.log(\"late\", \
                 N.run(1));\n"
            );
            for passes in [Some(only(&[INLINE, RESIDUAL])), None] {
                let baseline = project(&[("main.ts", &source)]);
                optimize(baseline.path(), &args(&only(&[])));
                let original = run_optimized(baseline.path());
                assert!(
                    original.status.success(),
                    "{}",
                    String::from_utf8_lossy(&original.stderr)
                );
                assert_eq!(original.stdout, b"early ReferenceError\nlate 2\n");

                let temp = project(&[("main.ts", &source)]);
                let extra = passes.as_ref().map_or_else(Vec::new, |passes| args(passes));
                optimize(temp.path(), &extra);
                let main = emitted(temp.path(), "main.ts");
                assert!(main.contains("late("), "{main}");
                let optimized = run_optimized(temp.path());
                assert!(
                    optimized.status.success(),
                    "{}",
                    String::from_utf8_lossy(&optimized.stderr)
                );
                assert_eq!(optimized.stdout, original.stdout, "{main}");
                assert_eq!(
                    std::fs::read_to_string(temp.path().join("main.ts")).unwrap(),
                    source
                );
            }
        }
    }
}

#[test]
fn keeps_namespace_self_import_calls_before_a_late_binding() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"import * as self from "./main.ts";
export const run = () => late(2);
try {
  console.log("early", self["run"]());
} catch (error) {
  console.log("early", (error as Error).name);
}
const late = (value: number) => value * 2;
console.log("late", self["run"]());
"#,
    )];
    for passes in [Some(only(&[INLINE])), None] {
        let temp = project(files);
        let extra = passes.as_ref().map_or_else(Vec::new, |passes| args(passes));
        optimize(temp.path(), &extra);
        let main = emitted(temp.path(), "main.ts");
        assert!(main.contains("late("), "{main}");
        same_behavior(temp.path());
        assert_eq!(
            run_optimized(temp.path()).stdout,
            b"early ReferenceError\nlate 4\n"
        );
        assert_eq!(
            std::fs::read_to_string(temp.path().join("main.ts")).unwrap(),
            files[0].1
        );
    }
}

#[test]
fn ignores_type_only_self_imports_when_proving_late_calls() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"import type { Run as ImportedRun } from "./main.ts";
export type Run = () => number;
export const run: ImportedRun = () => late(2);
const late = (value: number) => value * 2;
console.log(run());
"#,
    )];
    let config = r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"verbatimModuleSyntax":true,"rewriteRelativeImportExtensions":true}}"#;

    let baseline = project(files);
    std::fs::write(baseline.path().join("tsconfig.json"), config).unwrap();
    optimize(baseline.path(), &args(&only(&[])));
    let original = run_optimized(baseline.path());
    assert!(
        original.status.success(),
        "{}",
        String::from_utf8_lossy(&original.stderr)
    );
    assert_eq!(original.stdout, b"4\n");

    let temp = project(files);
    std::fs::write(temp.path().join("tsconfig.json"), config).unwrap();
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    let optimized = run_optimized(temp.path());
    assert!(
        optimized.status.success(),
        "{}",
        String::from_utf8_lossy(&optimized.stderr)
    );
    assert_eq!(optimized.stdout, original.stdout);
}

#[test]
fn inlines_namespace_functions_created_after_late_binding() {
    for kind in ["const", "let"] {
        for value in [
            "(value: number) => late(value)",
            "function(value: number) { return late(value); }",
        ] {
            let source = format!(
                "console.log(\"effect\");\nconst late = (value: number) => value * 2;\nnamespace \
                 N {{ export {kind} run = {value}; }}\nconsole.log(N.run(1));\n"
            );
            let files = &[("main.ts", source.as_str())];

            let baseline = project(files);
            optimize(baseline.path(), &args(&only(&[])));
            let original = run_optimized(baseline.path());
            assert!(
                original.status.success(),
                "{}",
                String::from_utf8_lossy(&original.stderr)
            );
            assert_eq!(original.stdout, b"effect\n2\n");

            let temp = project(files);
            let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
            assert!(applied_count(&report, INLINE) > 0, "{report:#}");
            let optimized = run_optimized(temp.path());
            assert!(
                optimized.status.success(),
                "{}",
                String::from_utf8_lossy(&optimized.stderr)
            );
            assert_eq!(optimized.stdout, original.stdout);
        }
    }
}

#[test]
fn keeps_exported_functions_reachable_early_through_an_import_cycle() {
    let temp = project(&[
        (
            "main.ts",
            r#"import { start } from "./a.ts";
start();
"#,
        ),
        (
            "a.ts",
            r#"import { invoke } from "./b.ts";
export const run = () => target(2);
invoke();
const target = (value: number) => value * 2;
export function start() {
  console.log(run());
}
"#,
        ),
        (
            "b.ts",
            r#"import * as ns from "./a.ts";
export function invoke() {
  try {
    console.log((ns as any)["run"]());
  } catch {
    console.log("early");
  }
}
"#,
        ),
    ]);
    optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let module = emitted(temp.path(), "a.ts");
    assert!(module.contains("target("), "{module}");
    same_behavior(temp.path());
}

#[test]
fn keeps_exported_functions_in_an_import_cycle_that_run_before_the_declaration() {
    let temp = project(&[
        (
            "main.ts",
            r#"import * as ns from "./cycle.ts";
export const run = () => late(1);
try {
  console.log(ns.call());
} catch (error) {
  console.log("early", (error as Error).name);
}
const late = (value: number) => value * 2;
console.log(ns.call());
"#,
        ),
        (
            "cycle.ts",
            r#"import * as main from "./main.ts";
export const call = () => (main as any)["run"]();
"#,
        ),
    ]);
    optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("late("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn inlines_top_level_declarations_at_sites_that_run_after_them() {
    let temp = project(&[
        (
            "main.ts",
            r#"import { report } from "./report.ts";
console.log("start");
const double = (value: number) => value * 2;
function run(n: number) {
  return double(n) + 1;
}
console.log(run(3));
report(5);
"#,
        ),
        (
            "report.ts",
            r#"import { triple } from "./triple.ts";
export function report(n: number) {
  console.log("report", triple(n));
}
"#,
        ),
        (
            "triple.ts",
            r#"console.log("triple module");
export const triple = (value: number) => value * 3;
"#,
        ),
    ]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    let main = emitted(temp.path(), "main.ts");
    assert!(
        main.contains("* 2 + 1") && !main.contains("double"),
        "{main}\n{report:#}"
    );
    assert!(saved(&report) > 0, "{report:#}");
    let module = emitted(temp.path(), "report.ts");
    assert!(module.contains("n * 3"), "{module}\n{report:#}");
    same_behavior(temp.path());
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

// ---------------------------------------------------------------------------
// method-devirtualization: positive fixtures
// ---------------------------------------------------------------------------

const ACCUMULATOR: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { Accumulator } from "./accumulator.ts";
function log(label: string, value: number): number {
  console.log("eval", label);
  return value;
}
const accumulator = new Accumulator(log("scale", 2));
accumulator.addMeasurement(log("first", 3));
accumulator.addMeasurement(log("second", 4));
console.log(
  accumulator.describeTotalValue("total"),
  new Accumulator(log("fresh", 5)).describeTotalValue(String(log("label", 6))),
);
"#,
    ),
    (
        "accumulator.ts",
        r#"export class Accumulator {
  total = 0;
  values: number[] = [];
  scale: number;
  constructor(scale: number) {
    this.scale = scale;
  }
  addMeasurement(value: number) {
    this.values.push(value);
    this.total += this.scaledMeasurement(value);
  }
  scaledMeasurement(value: number) {
    return value * this.scale;
  }
  describeTotalValue(label: string) {
    return `${label}: ${this.total} from ${this.values.length} values`;
  }
}
"#,
    ),
];

#[test]
fn devirtualizes_methods_across_modules() {
    let temp = project(ACCUMULATOR);
    let report = optimize(temp.path(), &args(&only(&[DEVIRT])));
    assert!(applied_count(&report, DEVIRT) >= 3, "{report:#}");
    assert!(saved(&report) > 0);
    let module = emitted(temp.path(), "accumulator.ts");
    assert!(!module.contains("describeTotalValue"), "{module}");
    assert!(module.contains("export function"), "{module}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains(".addMeasurement("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn devirtualized_methods_feed_call_inlining() {
    let temp = project(ACCUMULATOR);
    let report = optimize(temp.path(), &args(&only(&[DEVIRT, INLINE, RESIDUAL])));
    assert!(applied_count(&report, DEVIRT) >= 3, "{report:#}");
    assert!(applied_count(&report, INLINE) >= 1, "{report:#}");
    let module = emitted(temp.path(), "accumulator.ts");
    assert!(module.contains("value * "), "{module}");
    same_behavior(temp.path());
    let temp = project(ACCUMULATOR);
    let report = optimize(temp.path(), &[]);
    assert!(applied_count(&report, DEVIRT) >= 3, "{report:#}");
    same_behavior(temp.path());
}

#[test]
fn devirtualizes_generic_classes_with_this_and_const_receivers() {
    let temp = project(&[(
        "main.ts",
        r#"class Stack<T> {
  items: T[] = [];
  pushAll(values: T[]) {
    for (const value of values) {
      this.pushOne(value);
    }
    return this.items.length;
  }
  pushOne(value: T) {
    this.items.push(value);
  }
  peekTop(): T | undefined {
    return this.items[this.items.length - 1];
  }
}
const numbers = new Stack<number>();
console.log(numbers.pushAll([1, 2, 3]), numbers.peekTop());
const words = new Stack<string>();
words.pushOne("a");
console.log(words.peekTop()?.toUpperCase());
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[DEVIRT])));
    assert!(applied_count(&report, DEVIRT) >= 3, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains(".peekTop("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn method_devirtualization_never_runs_when_disabled() {
    let temp = project(ACCUMULATOR);
    // Property shortening would rename the closed class's method; keep the
    // name so the call site shows that devirtualization did not run.
    let report = optimize(
        temp.path(),
        &[
            "--disable-pass",
            DEVIRT,
            "--disable-pass",
            "property-shortening",
        ],
    );
    assert_eq!(applied_count(&report, DEVIRT), 0);
    let module = emitted(temp.path(), "accumulator.ts");
    assert!(module.contains("describeTotalValue("), "{module}");
    same_behavior(temp.path());
}

// ---------------------------------------------------------------------------
// method-devirtualization: safety boundaries
// ---------------------------------------------------------------------------

/// Runs a fixture with devirtualization only and asserts that the method
/// stays a class member.
fn assert_not_devirtualized(files: &[(&str, &str)], member: &str) -> serde_json::Value {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(&[DEVIRT])));
    let main = emitted(temp.path(), "main.ts");
    assert!(
        main.contains(&format!("    {member}(")),
        "{member} was devirtualized:\n{main}\n{report:#}"
    );
    same_behavior(temp.path());
    report
}

#[test]
fn preserves_extracted_and_bound_methods() {
    assert_not_devirtualized(
        &[(
            "main.ts",
            r#"class Greeter {
  name = "greeter";
  greetLoudly() {
    return this === undefined ? "detached" : this.name.toUpperCase();
  }
}
const greeter = new Greeter();
const bound = greeter.greetLoudly.bind(greeter);
console.log(greeter.greetLoudly(), bound(), greeter.greetLoudly.name);
"#,
        )],
        "greetLoudly",
    );
}

#[test]
fn preserves_overrides_and_super_calls() {
    assert_not_devirtualized(
        &[(
            "main.ts",
            r#"class Shape {
  describeShape() {
    return "shape";
  }
}
class Circle extends Shape {
  describeShape() {
    return "circle of " + super.describeShape();
  }
}
const items = [new Shape(), new Circle()];
console.log(items.map((s) => s.describeShape()).join(","), new Circle().describeShape());
"#,
        )],
        "describeShape",
    );
}

#[test]
fn preserves_dynamic_keys_and_string_names() {
    assert_not_devirtualized(
        &[(
            "main.ts",
            r#"class Lookup {
  value = 3;
  computeTwice() {
    return this.value * 2;
  }
}
const lookup = new Lookup();
const key: string = ["compute", "Twice"].join("");
const method = (lookup as unknown as Record<string, () => number>)[key];
console.log(lookup.computeTwice(), typeof method, "computeTwice" in lookup);
"#,
        )],
        "computeTwice",
    );
}

#[test]
fn preserves_dynamic_keys_on_generic_values() {
    assert_not_devirtualized(
        &[(
            "main.ts",
            r#"class Probe {
  probeMethod() {
    return "probe";
  }
}
function has<T extends object>(value: T, key: string) {
  return key in value;
}
const probe = new Probe();
console.log(probe.probeMethod(), has(probe, ["probe", "Method"].join("")));
"#,
        )],
        "probeMethod",
    );
}

#[test]
fn preserves_unproven_and_nullish_receivers() {
    let report = assert_not_devirtualized(
        &[(
            "main.ts",
            r#"class Counter {
  count = 1;
  incrementCount() {
    return ++this.count;
  }
}
function bump(counter: Counter) {
  return counter.incrementCount();
}
const maybe: Counter | undefined = Math.random() > 2 ? new Counter() : undefined;
console.log(bump(new Counter()), maybe?.incrementCount());
try {
  bump(null as unknown as Counter);
} catch (error) {
  console.log((error as Error).name);
}
"#,
        )],
        "incrementCount",
    );
    assert!(!skipped_reasons(&report, DEVIRT).is_empty());
}

#[test]
fn preserves_this_escape_arguments_and_private_members() {
    let temp = project(&[(
        "main.ts",
        r#"class Builder {
  private secret = 1;
  parts: string[] = [];
  appendPart(part: string) {
    this.parts.push(part);
    return this;
  }
  countArguments(first: number) {
    return arguments.length + first;
  }
  revealSecret() {
    return this.secret;
  }
}
const builder = new Builder();
console.log(builder.appendPart("a").parts.length, builder.countArguments(1), builder.revealSecret());
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[DEVIRT])));
    assert_eq!(applied_count(&report, DEVIRT), 0, "{report:#}");
    same_behavior(temp.path());
}

#[test]
fn preserves_external_names_and_public_classes() {
    let temp = project(&[(
        "main.ts",
        r#"export class Exposed {
  describeExposed() {
    return "exposed";
  }
}
class Local {
  toString() {
    return "local";
  }
}
const local = new Local();
console.log(new Exposed().describeExposed(), local.toString(), `${local}`);
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[DEVIRT])));
    assert_eq!(applied_count(&report, DEVIRT), 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("    describeExposed("), "{main}");
    assert!(main.contains("    toString("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn preserves_prototype_reflection() {
    assert_not_devirtualized(
        &[(
            "main.ts",
            r#"class Reflected {
  reflectedMethod() {
    return 1;
  }
}
const instance = new Reflected();
console.log(
  instance.reflectedMethod(),
  Object.getOwnPropertyNames(Object.getPrototypeOf(instance)).join(","),
);
"#,
        )],
        "reflectedMethod",
    );
}

/// Regression: a shorthand property `{ heapSize }` reads the function value.
/// Inlining its direct call must not delete the declaration (an Emscripten
/// import table in Excalidraw has exactly this shape).
#[test]
fn shorthand_property_reads_keep_inlined_functions() {
    const CODE: &str =
        "const Module = (function () {\n  const heap = [1, 2, 3];\n  function heapSize() { return \
         heap.length; }\n  function grow(n: number) { const old = heapSize(); return old + n; }\n  \
         var imports = { heapSize, grow };\n  return imports;\n})();\nconsole.log(Module.grow(1), \
         Module.heapSize(), Object.keys(Module).join(','));\nexport default Module;\n";
    for keep in [vec![INLINE, RESIDUAL], vec![]] {
        let temp = project(&[("main.ts", CODE)]);
        let report = if keep.is_empty() {
            optimize(temp.path(), &[])
        } else {
            optimize(temp.path(), &args(&only(&keep)))
        };
        let main = emitted(temp.path(), "main.ts");
        assert!(main.contains("function heapSize"), "{main}");
        assert!(
            skipped_reasons(&report, INLINE)
                .iter()
                .chain(skipped_reasons(&report, RESIDUAL).iter())
                .all(|r| r != "recheck_failed"),
            "{report:#}"
        );
        same_behavior(temp.path());
    }
}

/// `@ts-nocheck` disables the semantic recheck behind every transformation,
/// so such a module is an unchecked boundary and stays untouched.
#[test]
fn ts_nocheck_modules_are_not_transformed() {
    let code = "// @ts-nocheck\nfunction size() { return 2; }\nconst table = { size };\nexport \
                default function read() { return size() + table.size(); }\n";
    let temp = project(&[
        (
            "main.ts",
            "import read from './dep.ts';\nconsole.log(read());\n",
        ),
        ("dep.ts", code),
    ]);
    let report = optimize(temp.path(), &[]);
    let dep = modules(temp.path())["modules"]
        .as_array()
        .unwrap()
        .iter()
        .find(|m| m["source"].as_str().unwrap().ends_with("dep.ts"))
        .unwrap()
        .clone();
    assert_ne!(dep["kind"], "optimized", "{report:#}");
    same_behavior(temp.path());
}

/// Regression: an `export { f }` specifier is an alias, not a use; a
/// function only exported that way and called once can still be inlined
/// without breaking the export.
#[test]
fn export_specifiers_are_aliases_not_declarations() {
    let temp = project(&[
        (
            "main.ts",
            "import { twice } from './dep.ts';\nconsole.log(twice(21));\n",
        ),
        (
            "dep.ts",
            "function twice(n: number) { return n * 2; }\nexport { twice };\n",
        ),
    ]);
    let report = optimize(temp.path(), &[]);
    assert_eq!(
        report["skipped"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|s| s["reason"] == "recheck_failed")
            .count(),
        0
    );
    same_behavior(temp.path());
}

/// A binding named `undefined` is an ordinary application value. Calling it
/// through a library callback runs application code, so the later constant
/// must keep its temporal-dead-zone `ReferenceError`.
#[test]
fn shadowed_undefined_is_not_treated_as_inert() {
    let temp = project(&[(
        "main.ts",
        "const undefined = () => late();\ntry {\n  [0].forEach(undefined);\n  console.log('no \
         error');\n} catch (error) {\n  console.log((error as Error).name);\n}\nconst late = () \
         => 1;\nexport {};\n",
    )]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
}

#[test]
fn inlines_unwritten_let_and_var_functions() {
    let temp = project(&[(
        "main.ts",
        r#"let increment = (n: number) => n + 1;
var double = function (n: number) {
  return n * 2;
};
console.log(increment(1), double(4), increment(double(2)));
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) >= 2, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(
        !main.contains("increment(") && !main.contains("double("),
        "{main}"
    );
    same_behavior(temp.path());
}

#[test]
fn inlines_unwritten_function_local_var() {
    let temp = project(&[(
        "main.ts",
        r#"function run() {
  var computeLongName = (value: number) => value + 1;
  console.log(computeLongName(0));
}
run();
"#,
    )]);
    let report = optimize(temp.path(), &args(&only(&[INLINE, RESIDUAL])));
    assert!(applied_count(&report, INLINE) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("computeLongName("), "{main}");
    same_behavior(temp.path());
}

#[test]
fn preserves_for_initializer_var_function_loop_effects() {
    let cases = [
        (
            r#"function run() {
  let n = 0;
  for (var f = () => 1; n++ < 1;) {
    console.log("body");
  }
  console.log(f(), n);
}
run();
"#,
            "body\n1 2\n",
        ),
        (
            r#"function run() {
  let n = 0;
  for (var f = (value: number) => value + 1; (console.log("condition", n), n++ < 1);) {
    console.log("body");
  }
  console.log(f(0), n);
}
run();
"#,
            "condition 0\nbody\ncondition 1\n1 2\n",
        ),
    ];
    for (source, expected) in cases {
        for extra in [Some(only(&[INLINE, RESIDUAL])), None] {
            let temp = project(&[("main.ts", source)]);
            let owned = extra.unwrap_or_default();
            let report = optimize(temp.path(), &args(&owned));
            let main = emitted(temp.path(), "main.ts");
            assert!(main.contains("for (var f"), "{main}\n{report:#}");
            assert!(main.contains("console.log(f("), "{main}\n{report:#}");
            same_behavior(temp.path());
            let output = run_optimized(temp.path());
            assert!(
                output.status.success(),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
            assert_eq!(String::from_utf8_lossy(&output.stdout), expected);
        }
    }
}

#[test]
fn preserves_written_let_functions() {
    assert_not_inlined(
        &[(
            "main.ts",
            r#"let pick = (n: number) => n + 1;
console.log(pick(1));
pick = (n: number) => n - 1;
console.log(pick(1));
"#,
        )],
        "pick(1)",
    );
}
