// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `signature-reduction` and `function-specialization` fixtures. Every
//! fixture compares Node output of the original and optimized program, so
//! values, effects, their order and exceptions must all agree.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use std::fs;

use common::*;
use serde_json::Value;

const SR: &str = "signature-reduction";
const FS: &str = "function-specialization";
const TDCF: &str = "type-directed-control-flow";

/// Optimizes with only `keep` enabled and checks behavior.
fn run(files: &[(&str, &str)], keep: &[&str]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(keep)));
    same_behavior(temp.path());
    (temp, report)
}

/// Optimizes with every pass enabled and checks behavior.
fn run_all(files: &[(&str, &str)]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    (temp, report)
}

fn without_measurements(mut report: Value) -> Value {
    report.as_object_mut().unwrap().remove("measurements");
    report
}

// ---------------------------------------------------------------------------
// signature-reduction
// ---------------------------------------------------------------------------

const UNUSED_PARAMETERS: &[(&str, &str)] = &[(
    "main.ts",
    r#"export {};
let counter = 0;
function tick(label: string): number {
  counter++;
  console.log("tick", label, counter);
  return counter;
}
function combine(unusedLeft: number, value: number, unusedRight: string, scale: number): number {
  return value * scale;
}
function trailing(kept: number, unusedTail: string): number {
  return kept + 1;
}
function nothingKept(unusedOnly: number): void {
  console.log("nothing kept runs after its argument");
}
function literalBefore(flag: number, unusedTail: number): number {
  return flag * 2;
}
console.log(combine(tick("a"), 2, "a fairly long string that nobody reads", 10));
console.log(combine(1, tick("b"), String(tick("c")), 7));
console.log(trailing(counter, String(tick("d"))));
nothingKept(tick("e"));
console.log(literalBefore(4, tick("f")));
console.log(literalBefore(5, 0));
function sandwich(left: number, kept: number, middle: string, right: number): number {
  return kept;
}
console.log(sandwich(tick("g"), counter, "pure", tick("h")));
console.log(sandwich(1, 2, String(tick("i")), 3));
"#,
)];

#[test]
fn signature_reduction_removes_unused_parameters_keeping_argument_effects() {
    let (temp, report) = run(UNUSED_PARAMETERS, &[SR]);
    assert!(applied(&report, SR) >= 4, "{report:#}");
    assert!(saved(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function combine(value, scale)"), "{main}");
    // Effectful removed arguments still evaluate in order.
    assert!(main.contains("combine((tick(\"a\"), 2), 10)"), "{main}");
    assert!(main.contains("function trailing(kept)"), "{main}");
    assert!(main.contains("[counter, String(tick(\"d\"))][0]"), "{main}");
    assert!(main.contains("(tick(\"e\"), nothingKept)()"), "{main}");
    assert!(main.contains("literalBefore((tick(\"f\"), 4))"), "{main}");
    assert!(!main.contains("a fairly long string"), "{main}");
    assert!(
        main.contains("sandwich((tick(\"g\"), [counter, tick(\"h\")][0]))"),
        "{main}"
    );
}

#[test]
fn signature_reduction_with_all_passes_enabled() {
    let (_temp, report) = run_all(UNUSED_PARAMETERS);
    assert!(applied(&report, SR) >= 4, "{report:#}");
}

const CONSTANT_ARGUMENT: &[(&str, &str)] = &[(
    "main.ts",
    r#"export {};
type Unit = "px" | "em" | "rem";
function format(value: number, unit: Unit, depth: number): string {
  if (depth > 0) {
    return format(value / 2, unit, depth - 1);
  }
  const described = { unit };
  return value + unit + JSON.stringify(described);
}
console.log(format(10, "px", 0));
console.log(format(20, "px", 2));
console.log(format(30, "px", 1));
"#,
)];

#[test]
fn signature_reduction_propagates_a_common_constant_argument() {
    let (temp, report) = run(CONSTANT_ARGUMENT, &[SR]);
    assert!(applied(&report, SR) > 0, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function format(value, depth)"), "{main}");
    assert!(main.contains("format(value / 2, depth - 1)"), "{main}");
    assert!(main.contains("{ unit: \"px\" }"), "{main}");
    assert!(main.contains("format(20, 2)"), "{main}");
    let (_temp, report) = run_all(CONSTANT_ARGUMENT);
    assert!(applied(&report, SR) > 0);
}

const UNUSED_RESULTS: &[(&str, &str)] = &[(
    "main.ts",
    r#"export {};
const log: string[] = [];
function record(entry: string): number {
  log.push(entry);
  const total = log.length * 1000 + entry.length;
  return total;
}
function walk(depth: number): { depth: number; done: boolean } {
  log.push("walk " + depth);
  if (depth > 0) {
    return walk(depth - 1);
  }
  return { depth, done: true };
}
function finish(entry: string): string[] {
  log.push(entry);
  return log.slice();
}
record("first");
record("second");
void record("third");
walk(3);
finish("last");
console.log(log.join(","));
"#,
)];

#[test]
fn signature_reduction_drops_results_no_caller_uses() {
    let (temp, report) = run(UNUSED_RESULTS, &[SR]);
    assert!(applied(&report, SR) >= 3, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    // A pure result disappears; recursion forwards nothing.
    assert!(!main.contains("done: true"), "{main}");
    assert!(main.contains("return walk(depth - 1);"), "{main}");
    // An effectful final result stays as a statement.
    assert!(main.contains("log.slice();"), "{main}");
    assert!(!main.contains("return log.slice()"), "{main}");
    let (_temp, report) = run_all(UNUSED_RESULTS);
    assert!(applied(&report, SR) >= 3);
}

const CROSS_MODULE: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { scale as resize, describe } from "./dep.ts";
import { relay } from "./relay.ts";
console.log(resize(3, "ignored label text"), resize(4, "another ignored label"));
console.log(describe("value", "!"), describe("other", "!"));
console.log(relay(5));
"#,
    ),
    (
        "dep.ts",
        r#"export function scale(value: number, label: string): number {
  return value * 3;
}
export function describe(name: string, suffix: string): string {
  return name + suffix;
}
"#,
    ),
    (
        "relay.ts",
        r#"export { scale as relayed } from "./dep.ts";
import { scale } from "./dep.ts";
export const relay = (n: number) => scale(n, "relay label");
"#,
    ),
];

#[test]
fn signature_reduction_crosses_internal_module_aliases() {
    let (temp, report) = run(CROSS_MODULE, &[SR]);
    assert!(applied(&report, SR) >= 2, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("function scale(value)"), "{dep}");
    assert!(dep.contains("function describe(name)"), "{dep}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("resize(3)"), "{main}");
    assert!(!main.contains("ignored label"), "{main}");
    let relay = emitted(temp.path(), "relay.ts");
    assert!(!relay.contains("relay label"), "{relay}");
    let (_temp, report) = run_all(CROSS_MODULE);
    assert!(applied(&report, SR) >= 2);
}

#[test]
fn signature_reduction_preserves_public_entry_exports() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"export function api(value: number, unused: string): number {
  return value + 1;
}
console.log(api(1, "a"), api(2, "b"));
"#,
    )];
    let (temp, report) = run(files, &[SR]);
    assert_eq!(applied(&report, SR), 0);
    assert!(skipped_reasons(&report, SR).contains(&"public_export".to_owned()));
    assert!(emitted(temp.path(), "main.ts").contains("function api(value, unused)"));
}

#[test]
fn signature_reduction_preserves_observable_boundaries() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"import * as ns from "./space.ts";
// Escapes as a value: `.length` and unknown callers can observe it.
function escaping(value: number, unused: string): number { return value; }
console.log(escaping.length, escaping(1, "a"), [1].map(escaping as (v: number) => number));
// `arguments` observes the actual argument list.
function counting(value: number, unused: string): number { return arguments.length + value; }
console.log(counting(1, "x"));
// A default with effects must keep running when the argument is missing.
function defaulted(value: number, unused: number = console.log("default ran") as unknown as number): number {
  return value;
}
console.log(defaulted(1), defaulted(2, 3));
// A type query observes the signature.
function typed(value: number, unused: string): number { return value; }
const alias: typeof typed = (v) => v;
console.log(typed(1, "y"), alias(2, "z"));
// A namespace object exposes every export.
console.log(ns.spaced(1, "w"), Object.keys(ns).length);
// Async results stay observable through the promise.
async function later(): Promise<number> { return 1; }
later();
// A mid-body effectful return value is already minimal.
function middle(flag: boolean): number {
  if (flag) return Number(console.log("middle"));
  return 2;
}
middle(true);
middle(false);
"#,
        ),
        (
            "space.ts",
            "export function spaced(value: number, unused: string): number { return value; }\n",
        ),
    ];
    let (temp, report) = run(files, &[SR]);
    assert_eq!(
        applied(&report, SR),
        0,
        "{}",
        emitted(temp.path(), "main.ts")
    );
    let reasons = skipped_reasons(&report, SR);
    for reason in ["escapes", "observable_arity", "type_constraint"] {
        assert!(reasons.contains(&reason.to_owned()), "{reasons:?}");
    }
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function escaping(value, unused)"), "{main}");
    assert!(
        main.contains("function defaulted(value, unused ="),
        "{main}"
    );
    assert!(main.contains("return 2;"), "{main}");
    assert!(emitted(temp.path(), "space.ts").contains("spaced(value, unused)"));
}

#[test]
fn signature_reduction_keeps_rest_mixed_arity_and_recursion_safe() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"export {};
let order = "";
function mark(tag: string): string { order += tag; return tag; }
function rest(first: number, ...others: string[]): number { return first; }
console.log(rest(1), rest(2, mark("a"), mark("b")), rest(3, "pure"));
function optional(value: number, unused?: string): number { return value * 2; }
console.log(optional(1), optional(2, mark("c")));
function countdown(n: number, ignored: string): number {
  return n <= 0 ? 0 : countdown(n - 1, mark("r")) + 1;
}
console.log(countdown(3, mark("s")), order);
function thrower(value: number, unused: string): number { return value; }
try {
  thrower(1, (() => { throw new Error("argument throws"); })());
} catch (error) {
  console.log((error as Error).message, order);
}
"#,
    )];
    let (temp, report) = run(files, &[SR]);
    let main = emitted(temp.path(), "main.ts");
    assert!(applied(&report, SR) >= 3, "{main}");
    assert!(main.contains("function rest(first)"), "{main}");
    assert!(main.contains("function countdown(n)"), "{main}");
    run_all(files);
}

#[test]
fn signature_reduction_preserves_javascript_references() {
    let temp = project(&[
        (
            "main.ts",
            r#"import { fromJs } from "./bridge.js";
import { shared } from "./dep.ts";
console.log(shared(1, "x"), fromJs());
"#,
        ),
        (
            "dep.ts",
            "export function shared(value: number, unused: string): number { return value; }\n",
        ),
        (
            "bridge.js",
            "import { shared } from \"./dep.ts\";\nexport const fromJs = () => shared(2, \"y\");\n",
        ),
    ]);
    fs::write(
        temp.path().join("tsconfig.json"),
        r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"allowJs":true,"rewriteRelativeImportExtensions":true}}"#,
    )
    .unwrap();
    let report = check(temp.path(), &args(&only(&[SR])));
    assert_eq!(applied(&report, SR), 0, "{report:#}");
    assert!(skipped_reasons(&report, SR).contains(&"external_boundary".to_owned()));
}

#[test]
fn signature_reduction_keeps_finally_and_exceptions() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"export {};
const events: string[] = [];
function guarded(n: number, unused: string): number {
  try {
    events.push("try " + n);
    if (n > 1) {
      throw new Error("too large " + n);
    }
    return n;
  } finally {
    events.push("finally " + n);
  }
}
guarded(1, "a");
try {
  guarded(2, "b");
} catch (error) {
  events.push((error as Error).message);
}
console.log(events.join(" | "));
"#,
    )];
    let (temp, report) = run(files, &[SR]);
    assert!(applied(&report, SR) >= 2, "{report:#}");
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function guarded(n)"), "{main}");
    assert!(main.contains("return;"), "{main}");
    assert!(main.contains("finally"), "{main}");
}

#[test]
fn signature_reduction_preserves_namespace_observable_modules() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"import { lazyHelper } from "./lazy.ts";
import { listed } from "./listed.ts";
console.log(lazyHelper(1, "one"), lazyHelper(2, "two"), listed(3, "three"), listed(4, "four"));
const exported = Object.values(await import("./lazy.ts"));
console.log(exported.map((f) => (f as (...args: unknown[]) => unknown).length));
"#,
        ),
        (
            "lazy.ts",
            "export function lazyHelper(value: number, unused: string): number { return value; }\n",
        ),
        (
            "listed.ts",
            "function listed(value: number, unused: string): number { return value; }\nexport { \
             listed };\n",
        ),
    ];
    let (temp, report) = run(files, &[SR]);
    assert!(
        skipped_reasons(&report, SR).contains(&"escapes".to_owned()),
        "{report:#}"
    );
    assert!(emitted(temp.path(), "lazy.ts").contains("lazyHelper(value, unused)"));
    // An export list alone does not make a module observable.
    assert!(emitted(temp.path(), "listed.ts").contains("function listed(value)"));
}

#[test]
fn signature_reduction_preserves_namespace_members() {
    // Node cannot strip namespaces, so this fixture is only analyzed.
    let temp = project(&[(
        "main.ts",
        r#"export namespace Tools {
  export function helper(value: number, unused: string): number { return value; }
  export const run = () => helper(1, "x") + helper(2, "y");
}
console.log(Tools.run(), Object.values(Tools).map((f) => (f as Function).length));
"#,
    )]);
    let report = check(temp.path(), &args(&only(&[SR])));
    assert_eq!(applied(&report, SR), 0, "{report:#}");
}

#[test]
fn signature_reduction_disabled_never_runs() {
    let temp = project(UNUSED_PARAMETERS);
    let report = optimize(temp.path(), &["--disable-pass", SR]);
    assert_eq!(applied(&report, SR), 0);
    let main = emitted(temp.path(), "main.ts");
    assert!(
        main.contains("function combine(unusedLeft, value, unusedRight, scale)"),
        "{main}"
    );
    same_behavior(temp.path());
}

#[test]
fn signature_reduction_check_and_output_agree_and_are_deterministic() {
    let temp = project(CROSS_MODULE);
    let first = check(temp.path(), &[]);
    let second = check(temp.path(), &[]);
    let written = optimize(temp.path(), &[]);
    assert_eq!(
        without_measurements(first.clone()),
        without_measurements(second)
    );
    assert_eq!(without_measurements(first), without_measurements(written));
}

// ---------------------------------------------------------------------------
// function-specialization
// ---------------------------------------------------------------------------

const SPECIALIZE: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { draw } from "./dep.ts";
type Shape = "circle" | "square";
let effects = "";
function size(n: number): number { effects += n; return n; }
function area(shape: Shape, side: number): number {
  if (shape === "circle") {
    const radius = side / 2;
    console.log("computing the circle area for a side of", side);
    return Math.round(Math.PI * radius * radius * 1000) / 1000;
  }
  console.log("computing the square area for a side of", side);
  return side * side;
}
console.log(area("circle", size(2)), area("square", size(3)), area("circle", 4), effects);
console.log(draw("svg", 1), draw("canvas", 2));
"#,
    ),
    (
        "dep.ts",
        r#"export type Target = "svg" | "canvas";
export function draw(target: Target, width: number): string {
  switch (target) {
    case "svg":
      return `<svg width="${width}" xmlns="http://www.w3.org/2000/svg"></svg>`;
    case "canvas":
      return `canvas(${width}) with a context that is created lazily`;
  }
}
"#,
    ),
];

#[test]
fn function_specialization_clones_per_literal_across_modules() {
    let (temp, report) = run(SPECIALIZE, &[FS, TDCF]);
    assert_eq!(applied(&report, FS), 2, "{report:#}");
    assert!(saved(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function area_square(side)"), "{main}");
    assert!(!main.contains("=== \"circle\""), "{main}");
    assert!(
        main.contains("area(size(2)), area_square(size(3))"),
        "{main}"
    );
    assert!(main.contains("import { draw, draw_canvas }"), "{main}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("export function draw_canvas(width)"), "{dep}");
    assert!(!dep.contains("switch"), "{dep}");
    let (_temp, report) = run_all(SPECIALIZE);
    assert_eq!(applied(&report, FS), 2, "{report:#}");
}

#[test]
fn function_specialization_shares_equivalent_clones_and_bounds_recursion() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"export {};
type Kind = "a" | "b" | "c";
function render(kind: Kind, depth: number): string {
  switch (kind) {
    case "a":
    case "b":
      if (depth > 0) {
        return "[shared a/b at " + depth + "]" + render(kind, depth - 1);
      }
      return "[shared a/b end]";
    case "c":
      return "<c>" + render("a", depth);
  }
}
console.log(render("a", 2), render("b", 1), render("c", 1), render("a", 0));
console.log(render("b", 3), render("c", 2), render("a", 4), render("b", 0));
console.log(render("c", 0), render("a", 1), render("b", 2), render("c", 3));
function toggle(flag: boolean, value: number): number {
  return flag ? value * 1000 + 123456 : value - 987654;
}
console.log(toggle(true, 1), toggle(false, 2), toggle(true, 3), toggle(false, 4));
console.log(toggle(true, 5), toggle(false, 6), toggle(true, 7), toggle(false, 8));
console.log(toggle(true, 9), toggle(false, 10), toggle(true, 11), toggle(false, 12));
"#,
    )];
    let (temp, report) = run(files, &[FS, TDCF]);
    let main = emitted(temp.path(), "main.ts");
    assert_eq!(
        applied(&report, FS),
        2,
        "{main}{:?}",
        skipped_reasons(&report, FS)
    );
    // "a" and "b" share one implementation; "c" has its own.
    assert_eq!(main.matches("function render").count(), 2, "{main}");
    assert!(!main.contains("render_b"), "{main}");
    assert!(main.contains("render(depth - 1)"), "{main}");
    assert!(!main.contains("flag"), "{main}");
    run_all(files);
}

#[test]
fn function_specialization_rejects_unprofitable_clones_by_size() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"export {};
type Mode = "x" | "y";
function work(mode: Mode, n: number): number {
  let total = 0;
  for (let i = 0; i < n; i++) {
    total += i * 3 + 1;
    total = total % 1000003;
    total ^= i << 2;
  }
  console.log("a long shared message that every clone would have to repeat", total);
  return mode === "x" ? total : -total;
}
console.log(work("x", 10), work("y", 20));
"#,
    )];
    let (temp, report) = run(files, &[FS, TDCF]);
    assert_eq!(applied(&report, FS), 0, "{report:#}");
    assert!(skipped_reasons(&report, FS).contains(&"no_size_benefit".to_owned()));
    assert!(emitted(temp.path(), "main.ts").contains("function work(mode, n)"));
}

#[test]
fn function_specialization_requires_type_directed_control_flow() {
    let (temp, report) = run(SPECIALIZE, &[FS]);
    assert_eq!(applied(&report, FS), 0, "{report:#}");
    assert!(skipped_reasons(&report, FS).contains(&"requires_disabled_pass".to_owned()));
    assert!(emitted(temp.path(), "main.ts").contains("function area(shape, side)"));
}

#[test]
fn function_specialization_disabled_never_runs() {
    let temp = project(SPECIALIZE);
    let report = optimize(temp.path(), &["--disable-pass", FS]);
    assert_eq!(applied(&report, FS), 0);
    assert!(!emitted(temp.path(), "main.ts").contains("area_square"));
    same_behavior(temp.path());
}

#[test]
fn function_specialization_preserves_unknown_public_and_escaping_functions() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"type Mode = "on" | "off";
function mixed(mode: Mode, n: number): number {
  if (mode === "on") { return n + 1000; }
  return n - 1000;
}
const dynamic: Mode = Math.random() > 2 ? "on" : "off";
console.log(mixed("on", 1), mixed("off", 2), mixed(dynamic, 3));
export function api(mode: Mode, n: number): number {
  if (mode === "on") { return n * 1000; }
  return n / 1000;
}
console.log(api("on", 1), api("off", 2));
function escaping(mode: Mode, n: number): number {
  if (mode === "on") { return n + 2000; }
  return n - 2000;
}
const handlers = [escaping];
console.log(escaping("on", 1), escaping("off", 2), handlers.length);
"#,
    )];
    let (temp, report) = run(files, &[FS, TDCF]);
    assert_eq!(applied(&report, FS), 0, "{report:#}");
    let reasons = skipped_reasons(&report, FS);
    for reason in ["unproven_runtime_fact", "public_export", "escapes"] {
        assert!(reasons.contains(&reason.to_owned()), "{reasons:?}");
    }
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("function mixed(mode, n)"), "{main}");
}

#[test]
fn function_specialization_extends_export_lists() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"import { pick } from "./dep.ts";
console.log(pick("first", 1), pick("second", 2), pick("first", 3), pick("second", 4));
console.log(pick("first", 5), pick("second", 6), pick("first", 7), pick("second", 8));
"#,
        ),
        (
            "dep.ts",
            r#"type Which = "first" | "second";
function pick(which: Which, n: number): string {
  if (which === "first") {
    return "the first implementation handles " + n;
  } else {
    return "the second implementation handles " + n * 2;
  }
}
export { pick };
"#,
        ),
    ];
    let (temp, report) = run(files, &[FS, TDCF]);
    assert_eq!(applied(&report, FS), 1, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("export { pick, pick_second }"), "{dep}");
    assert!(
        emitted(temp.path(), "main.ts").contains("import { pick, pick_second }"),
        "{dep}"
    );
}

#[test]
fn function_specialization_check_and_output_agree() {
    let temp = project(SPECIALIZE);
    let checked = check(temp.path(), &[]);
    let written = optimize(temp.path(), &[]);
    assert_eq!(without_measurements(checked), without_measurements(written));
}
