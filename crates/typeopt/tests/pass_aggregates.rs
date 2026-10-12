// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `aggregate-elimination` and `discriminant-compaction` fixtures. Every
//! fixture compares original and optimized Node behavior.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use common::*;
use serde_json::Value;

const AGGREGATE: &str = "aggregate-elimination";

fn only_aggregate() -> Vec<String> {
    only(&[AGGREGATE])
}

/// Optimizes with only `aggregate-elimination`, checks behavior and returns
/// the report.
fn aggregate(files: &[(&str, &str)]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only_aggregate()));
    same_behavior(temp.path());
    (temp, report)
}

/// Asserts that the pass changed nothing, that behavior is equal and that a
/// skip explains it with `reason`.
fn preserved(files: &[(&str, &str)], reason: &str) -> String {
    let (temp, report) = aggregate(files);
    assert_eq!(applied(&report, AGGREGATE), 0, "{report:#}");
    let reasons = skipped_reasons(&report, AGGREGATE);
    assert!(
        reasons.iter().any(|r| r == reason),
        "expected {reason} in {reasons:?}"
    );
    emitted(temp.path(), "main.ts")
}

#[test]
fn local_objects_and_tuples_become_scalars() {
    let files = [(
        "main.ts",
        r#"
export function area(width: number, height: number) {
  const box = { width: width * 2, height: height + 1, depth: 4 };
  if (width > 2) box.height += 10;
  return box.width * box.height;
}
export function sum(a: number, b: number) {
  const pair = [a + 1, b * 2];
  pair[1] = pair[1] + pair.length;
  return pair[0] + pair[1];
}
console.log(area(2, 3), area(5, 1), sum(4, 5));
"#,
    )];
    let (temp, report) = aggregate(&files);
    assert_eq!(applied(&report, AGGREGATE), 2, "{report:#}");
    assert!(saved(&report) > 0);
    let code = emitted(temp.path(), "main.ts");
    assert!(
        code.contains("let box_width = width * 2, box_height = height + 1;"),
        "{code}"
    );
    assert!(code.contains("box_height += 10"), "{code}");
    assert!(
        code.contains("let pair_0 = a + 1, pair_1 = b * 2;"),
        "{code}"
    );
    assert!(code.contains("pair_1 = pair_1 + 2"), "{code}");
    assert!(!code.contains("box.") && !code.contains("pair["), "{code}");
    // The same transformation is chosen with every pass enabled.
    let all = project(&files);
    let report = optimize(all.path(), &[]);
    assert!(applied(&report, AGGREGATE) >= 2, "{report:#}");
    same_behavior(all.path());
}

#[test]
fn annotated_aggregates_keep_their_field_types() {
    // Indexed-access annotations keep contextual typing; assertions keep a
    // union field from narrowing to its initializer.
    let (temp, report) = aggregate(&[(
        "main.ts",
        r#"
interface Settings { scale: number; mode: "fit" | "fill"; visible: boolean; label: string }
type Bounds = [number, number, number, number];
export function describe(n: number) {
  const s: Settings = { scale: n, mode: "fit", visible: false, label: "unused label" };
  if (n > 3) s.mode = "fill";
  if (s.visible === true) console.log("hidden");
  const b: Bounds = [n, 0, 0, n * 2];
  return `${s.scale}:${s.mode}:${b[0] + b[3]}`;
}
console.log(describe(1), describe(5));
"#,
    )]);
    assert_eq!(applied(&report, AGGREGATE), 2, "{report:#}");
    assert!(!skipped_reasons(&report, AGGREGATE).contains(&"recheck_failed".into()));
    let code = emitted(temp.path(), "main.ts");
    assert!(
        code.contains("let s_scale = n, s_mode = \"fit\", s_visible = false;"),
        "{code}"
    );
    assert!(code.contains("s_mode = \"fill\""), "{code}");
    assert!(code.contains("let b_0 = n, b_3 = n * 2;"), "{code}");
}

#[test]
fn initializer_effects_keep_their_order() {
    let (temp, report) = aggregate(&[(
        "main.ts",
        r#"
const log: string[] = [];
function note(label: string, value: number) { log.push(label); return value; }
export function run(n: number) {
  const r = { a: note("a", n), b: note("b", n + 1), c: n * 2, d: note("d", 4) };
  return r.b + r.d;
}
console.log(run(1), log.join(","));
"#,
    )]);
    assert_eq!(applied(&report, AGGREGATE), 1, "{report:#}");
    let code = emitted(temp.path(), "main.ts");
    // Unused effectful fields run in a comma expression before the next
    // used field; `n * 2`, which the checker treats as effect-free on the
    // left of a comma, keeps a binding of its own.
    assert!(
        code.contains(
            "let r_b = (note(\"a\", n), note(\"b\", n + 1)), r_c = n * 2, r_d = note(\"d\", 4);"
        ),
        "{code}"
    );
}

#[test]
fn module_level_tables_used_inside_functions() {
    let (temp, report) = aggregate(&[
        (
            "main.ts",
            "import { classify } from './dep.ts';\nconsole.log(classify('a b'), classify('ab'));\n",
        ),
        (
            "dep.ts",
            r#"
const PATTERNS = {
  /** Whitespace separates words. */
  SPACE: /\s/u,
  /** Hyphens join words. */
  HYPHEN: /-/u,
};
export function classify(text: string) {
  return PATTERNS.SPACE.test(text) ? "spaced" : PATTERNS.HYPHEN.test(text) ? "joined" : "word";
}
"#,
        ),
    ]);
    assert_eq!(applied(&report, AGGREGATE), 1, "{report:#}");
    let code = emitted(temp.path(), "dep.ts");
    assert!(code.contains("PATTERNS_SPACE.test(text)"), "{code}");
    assert!(!code.contains("PATTERNS."), "{code}");
}

#[test]
fn internal_namespaces_collapse_into_direct_bindings() {
    let files = [
        (
            "main.ts",
            r#"
import { format, parse } from "./dep.ts";
const LIMIT = 3;
function twice(n: number) { return n * 2; }
class Counter { count = 0; }
const helpers = { format, parse, limit: LIMIT, twice, Counter };
export function run(text: string) {
  const value = helpers.parse(text);
  const counter = new helpers.Counter();
  counter.count = helpers.twice(value);
  return helpers.format(counter.count + helpers.limit);
}
console.log(run("20"));
"#,
        ),
        (
            "dep.ts",
            "export function format(n: number) { return `<${n}>`; }\nexport const parse = (text: \
             string) => Number(text);\n",
        ),
    ];
    let (temp, report) = aggregate(&files);
    assert_eq!(applied(&report, AGGREGATE), 1, "{report:#}");
    let code = emitted(temp.path(), "main.ts");
    assert!(!code.contains("helpers"), "{code}");
    assert!(code.contains("format(counter.count + LIMIT)"), "{code}");
    assert!(code.contains("new Counter()"), "{code}");
    let all = project(&files);
    let report = optimize(all.path(), &[]);
    assert!(applied(&report, AGGREGATE) >= 1, "{report:#}");
    same_behavior(all.path());
}

#[test]
fn disabled_pass_never_runs() {
    let temp = project(&[(
        "main.ts",
        "export function f(n: number) { const p = { x: n, y: n + 1 }; return p.x * p.y; }\nconst \
         ns = { f };\nconsole.log(ns.f(3));\n",
    )]);
    let report = optimize(temp.path(), &["--disable-pass", AGGREGATE]);
    assert_eq!(applied(&report, AGGREGATE), 0);
    assert!(skipped_reasons(&report, AGGREGATE).is_empty());
    let code = emitted(temp.path(), "main.ts");
    assert!(
        code.contains("p.x * p.y") && code.contains("ns.f(3)"),
        "{code}"
    );
    same_behavior(temp.path());
}

#[test]
fn identity_and_escape_preserve_objects() {
    // Comparing, passing, returning and serializing observe the object.
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n }; const q = p; return p === q ? p.x \
             : 0; }\nconsole.log(f(1));\n",
        )],
        "escapes",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n }; return [p.x, p === \
             globalThis.structuredClone(p)]; }\nconsole.log(f(1));\n",
        )],
        "observable_identity",
    );
    let code = preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, y: 2 }; console.log(p.x); return \
             JSON.stringify(p); }\nconsole.log(f(1));\n",
        )],
        "escapes",
    );
    assert!(code.contains("JSON.stringify(p)"), "{code}");
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, y: 2 }; const keys: string[] = []; \
             for (const k in p) keys.push(k); return [p.x, keys]; }\nconsole.log(f(1));\n",
        )],
        "reflection",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, y: 2 }; return { ...p, z: p.x }; \
             }\nconsole.log(f(1));\n",
        )],
        "reflection",
    );
}

#[test]
fn prototype_accessors_and_names_preserve_objects() {
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n }; return p.x + p.toString(); \
             }\nconsole.log(f(1));\n",
        )],
        "observable_operation",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, get y() { return this.x + 1; } }; \
             return p.x + p.y; }\nconsole.log(f(1));\n",
        )],
        "accessor",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, run() { return this.x; } }; return \
             p.x + p.run(); }\nconsole.log(f(1));\n",
        )],
        "observable_operation",
    );
    preserved(
        &[(
            "main.ts",
            "export function f() { const p = { cb: () => 1 }; return p.cb.name; \
             }\nconsole.log(f());\n",
        )],
        "observable_name",
    );
    preserved(
        &[(
            "main.ts",
            "const base = { greet: 'hi' };\nexport function f(n: number) { const p = { __proto__: \
             base, x: n }; return [p.x, (p as any).greet]; }\nconsole.log(f(1));\n",
        )],
        "prototype_mutation",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number, k: string) { const p = { [k]: n, x: 1 }; return p.x; \
             }\nconsole.log(f(1, 'a'));\n",
        )],
        "dynamic_access",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, y: 1 }; delete (p as { x?: number \
             }).x; return p.y; }\nconsole.log(f(1));\n",
        )],
        "escapes",
    );
}

#[test]
fn tuple_holes_iteration_and_length_writes_preserve_tuples() {
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const t = [n, , n + 1]; return t[0]; \
             }\nconsole.log(f(1));\n",
        )],
        "sparse_array",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(xs: number[]) { const t = [...xs, 1]; return t[0]; \
             }\nconsole.log(f([2]));\n",
        )],
        "custom_iterator",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const t = [n, n + 1]; const [a, b] = t; return a + b \
             + t[0]; }\nconsole.log(f(1));\n",
        )],
        "escapes",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number, i: number) { const t = [n, n + 1]; return t[i] + t[0]; \
             }\nconsole.log(f(1, 1));\n",
        )],
        "dynamic_access",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const t = [n, n + 1]; t.length = 1; return t[0]; \
             }\nconsole.log(f(1));\n",
        )],
        "mutation",
    );
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const t = [n, function (this: unknown) { return this; \
             }]; return [t[0], typeof (t[1] as () => unknown)()]; }\nconsole.log(f(1));\n",
        )],
        "observable_operation",
    );
}

#[test]
fn early_observers_preserve_effectful_initializers() {
    // `peek` is hoisted and can run while the initializer evaluates; the
    // original binding is still in its temporal dead zone then.
    preserved(
        &[(
            "main.ts",
            r#"
let hook: () => unknown = () => 0;
export function f(n: number) {
  function peek() { return box.width; }
  hook = peek;
  const box = { width: n, height: (() => { try { return hook(); } catch (e) { return String(e instanceof ReferenceError); } })() };
  return [box.width, box.height];
}
console.log(f(1));
"#,
        )],
        "side_effects",
    );
    // A self-reference inside the initializer needs the original binding.
    preserved(
        &[(
            "main.ts",
            r#"
export function f(n: number) {
  const p: { x: number; y: (() => number)[] } = { x: n, y: [() => p.x * 10] };
  return p.y[0]() + p.x;
}
console.log(f(1));
"#,
        )],
        "side_effects",
    );
}

#[test]
fn boundaries_preserve_aggregates() {
    // Public entry exports are external contracts.
    preserved(
        &[(
            "main.ts",
            "export const config = { x: 1, y: 2 };\nconsole.log(config.x + config.y);\n",
        )],
        "public_export",
    );
    // Internal exports are referenced from another module.
    preserved(
        &[
            (
                "main.ts",
                "import { config } from './dep.ts';\nconsole.log(config.x + config.y);\n",
            ),
            ("dep.ts", "export const config = { x: 1, y: 2 };\n"),
        ],
        "escapes",
    );
    // A type query observes the layout.
    preserved(
        &[(
            "main.ts",
            "export function f(n: number) { const p = { x: n, y: 1 }; const q: typeof p = { x: 2, \
             y: 3 }; return [p.x, q]; }\nconsole.log(f(1));\n",
        )],
        "type_constraint",
    );
}

#[test]
fn namespace_this_shadowing_and_initialization_boundaries() {
    // A function that reads `this` sees the namespace object as receiver.
    preserved(
        &[(
            "main.ts",
            r#"
export {};
function describe(this: unknown) { return typeof this; }
const api = { describe };
console.log(api.describe());
"#,
        )],
        "observable_operation",
    );
    // A parameter with the same name would capture the direct binding.
    preserved(
        &[(
            "main.ts",
            r#"
function twice(n: number) { return n * 2; }
const api = { twice };
export function run(twice: number) { return api.twice(twice); }
console.log(run(4));
"#,
        )],
        "unsupported_syntax",
    );
    // A reassigned binding no longer holds the captured value.
    preserved(
        &[(
            "main.ts",
            r#"
export {};
let twice = (n: number) => n * 2;
const api = { twice };
twice = (n: number) => n * 3;
console.log(api.twice(4), twice(4));
"#,
        )],
        "mutation",
    );
    // Code before the namespace may read it during initialization.
    preserved(
        &[(
            "main.ts",
            r#"
export {};
function twice(n: number) { return n * 2; }
function early() { try { return api.twice(1); } catch (e) { return String(e instanceof ReferenceError); } }
console.log(early());
const api = { twice };
console.log(api.twice(2));
"#,
        )],
        "module_initialization",
    );
    // An import cycle can run another module first.
    preserved(
        &[
            (
                "main.ts",
                r#"
import { other } from "./dep.ts";
function twice(n: number) { return n * 2; }
const api = { twice };
export function viaApi(n: number) { return api.twice(n); }
console.log(other(), viaApi(2));
"#,
            ),
            (
                "dep.ts",
                r#"
import { viaApi } from "./main.ts";
export function other() { return typeof viaApi; }
"#,
            ),
        ],
        "module_initialization",
    );
    // The namespace itself escapes.
    preserved(
        &[(
            "main.ts",
            r#"
export {};
function twice(n: number) { return n * 2; }
const api = { twice };
console.log(api.twice(2), Object.keys(api));
"#,
        )],
        "escapes",
    );
}

// ---------------------------------------------------------------------------
// discriminant-compaction
// ---------------------------------------------------------------------------

const DISCRIMINANT: &str = "discriminant-compaction";

const SHAPES: &str = r#"
export interface Circle { kind: "circle"; radius: number }
export interface Rectangle { kind: "rectangle"; width: number; height: number }
export type Shape = Circle | { kind: "triangle"; base: number; height: number } | Rectangle;

export function circle(radius: number): Shape {
  return { kind: "circle", radius };
}

export function rectangle(width: number, height: number): Rectangle {
  return { kind: "rectangle", width, height };
}

export function area(shape: Shape): number {
  switch (shape.kind) {
    case "circle":
      return Math.PI * shape.radius * shape.radius;
    case "rectangle":
      return shape.width * shape.height;
    default:
      return (shape.base * shape.height) / 2;
  }
}
"#;

/// Optimizes with only `discriminant-compaction`, checks behavior and
/// returns the report.
fn discriminant(files: &[(&str, &str)]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(&[DISCRIMINANT])));
    same_behavior(temp.path());
    (temp, report)
}

/// Asserts that the shapes domain kept its tags for `reason`.
fn tags_preserved(main: &str, reason: &str) {
    let (temp, report) = discriminant(&[("main.ts", main), ("shapes.ts", SHAPES)]);
    assert_eq!(applied(&report, DISCRIMINANT), 0, "{report:#}");
    let reasons = skipped_reasons(&report, DISCRIMINANT);
    assert!(
        reasons.iter().any(|r| r == reason),
        "expected {reason} in {reasons:?}"
    );
    assert!(emitted(temp.path(), "shapes.ts").contains("kind: \"circle\""));
}

#[test]
fn closed_union_tags_become_small_numbers_across_modules() {
    let files = [
        (
            "main.ts",
            r#"
import { area, circle, rectangle, type Shape } from "./shapes.ts";
const shapes: Shape[] = [circle(1), rectangle(2, 3), { kind: "triangle", base: 4, height: 5 }];
let total = 0;
for (const shape of shapes) {
  if (shape.kind !== "triangle") total += area(shape);
}
const round = shapes.filter((s) => s.kind === "rectangle" || s.kind === "circle").length;
console.log(total.toFixed(3), round, shapes.map(area).map((a) => a.toFixed(2)).join(","));
"#,
        ),
        ("shapes.ts", SHAPES),
    ];
    let (temp, report) = discriminant(&files);
    assert_eq!(applied(&report, DISCRIMINANT), 1, "{report:#}");
    assert!(saved(&report) > 0);
    let shapes = emitted(temp.path(), "shapes.ts");
    assert!(shapes.contains("return { kind: 0, radius };"), "{shapes}");
    assert!(
        shapes.contains("return { kind: 1, width, height };"),
        "{shapes}"
    );
    assert!(
        shapes.contains("case 0:") && shapes.contains("case 1:"),
        "{shapes}"
    );
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("{ kind: 2, base: 4, height: 5 }"), "{main}");
    assert!(main.contains("shape.kind !== 2"), "{main}");
    assert!(main.contains("s.kind === 1 || s.kind === 0"), "{main}");
    // The transformation is also chosen with every pass enabled.
    let all = project(&files);
    let report = optimize(all.path(), &[]);
    assert_eq!(applied(&report, DISCRIMINANT), 1, "{report:#}");
    same_behavior(all.path());
}

#[test]
fn collections_copies_and_conditionals_keep_tags_consistent() {
    let (temp, report) = discriminant(&[(
        "main.ts",
        r#"
type Event =
  | { type: "pointer-down"; x: number }
  | { type: "pointer-up"; x: number }
  | { type: "key-press"; key: string };
const queue = new Map<string, Event[]>();
function record(channel: string, event: Event) {
  const list = queue.get(channel) ?? [];
  list.push(event);
  queue.set(channel, list);
}
function press(down: boolean, x: number): Event {
  return { type: down ? "pointer-down" : "pointer-up", x };
}
function mirror(event: Event): Event {
  return event.type === "key-press" ? event : { type: event.type, x: -event.x };
}
function sameType(a: Event, b: Event) {
  return a.type === b.type;
}
record("main", press(true, 1));
record("main", mirror(press(false, 2)));
record("keys", { type: "key-press", key: "a" });
const all = [...queue.values()].flat();
const seen = new Set<Event>(all);
let summary = "";
for (const event of seen) {
  switch (event.type) {
    case "pointer-down":
      summary += `d${event.x};`;
      break;
    case "pointer-up":
      summary += `u${event.x};`;
      break;
    case "key-press":
      summary += `k${event.key};`;
  }
}
console.log(summary, sameType(all[0], all[1]), all.find((e) => e.type === "key-press") !== undefined);
export {};
"#,
    )]);
    assert_eq!(applied(&report, DISCRIMINANT), 1, "{report:#}");
    let code = emitted(temp.path(), "main.ts");
    assert!(code.contains("type: down ? 0 : 1"), "{code}");
    assert!(code.contains("{ type: event.type, x: -event.x }"), "{code}");
    assert!(code.contains("case 2:"), "{code}");
    assert!(!code.contains("\"pointer-down\""), "{code}");
}

#[test]
fn unrelated_domains_sharing_values_are_independent() {
    let (temp, report) = discriminant(&[(
        "main.ts",
        r#"
type Fill = { mode: "solid"; color: string } | { mode: "none" };
type Stroke = { mode: "none" } | { mode: "dashed"; gap: number };
function fill(on: boolean): Fill {
  return on ? { mode: "solid", color: "red" } : { mode: "none" };
}
function stroke(on: boolean): Stroke {
  return on ? { mode: "dashed", gap: 2 } : { mode: "none" };
}
function describe(f: Fill, s: Stroke) {
  return [f.mode === "none" ? "no fill" : "fill", s.mode === "none" ? "no stroke" : "stroke"].join("/");
}
console.log(describe(fill(true), stroke(false)), describe(fill(false), stroke(true)), "none");
export {};
"#,
    )]);
    assert_eq!(applied(&report, DISCRIMINANT), 2, "{report:#}");
    let code = emitted(temp.path(), "main.ts");
    // Unrelated string values keep their spelling.
    assert!(
        code.contains("\"no fill\"") && code.contains(", \"none\")"),
        "{code}"
    );
    assert!(
        code.contains("f.mode === 1") && code.contains("s.mode === 0"),
        "{code}"
    );
}

#[test]
fn disabled_discriminant_pass_never_runs() {
    let temp = project(&[
        (
            "main.ts",
            "import { area, circle } from \
             './shapes.ts';\nconsole.log(area(circle(2)).toFixed(2));\n",
        ),
        ("shapes.ts", SHAPES),
    ]);
    let report = optimize(temp.path(), &["--disable-pass", DISCRIMINANT]);
    assert_eq!(applied(&report, DISCRIMINANT), 0);
    assert!(skipped_reasons(&report, DISCRIMINANT).is_empty());
    assert!(emitted(temp.path(), "shapes.ts").contains("kind: \"circle\""));
    same_behavior(temp.path());
}

#[test]
fn serialization_logging_and_reflection_preserve_tags() {
    // Scene-style JSON serialization observes the tag.
    tags_preserved(
        "import { circle } from './shapes.ts';\nconsole.log(JSON.stringify([circle(1)]));\n",
        "external_boundary",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconsole.log(circle(1));\n",
        "external_boundary",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconsole.log(Object.values(circle(1)).join());\n",
        "external_boundary",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst keys: string[] = [];\nfor (const key in \
         circle(1)) keys.push(key);\nconsole.log(keys);\n",
        "reflection",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst wide: unknown = \
         circle(1);\nconsole.log(typeof wide);\n",
        "escapes",
    );
}

#[test]
fn tag_values_used_as_strings_preserve_tags() {
    // A computed key may read the tag.
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst s = circle(1);\nconst key = ['kind', \
         'radius'][Number(Math.random() > 2)] as keyof typeof s;\nconsole.log(s[key]);\n",
        "dynamic_access",
    );
    // A tag assigned from an arbitrary string.
    tags_preserved(
        "import { circle, rectangle } from './shapes.ts';\nconst s = rectangle(1, 2);\nconst t = \
         circle(1);\nconst name: string = 'circle';\nt.kind = name as \
         'circle';\nconsole.log(t.kind === 'circle', s.width);\n",
        "unproven_runtime_fact",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst s = circle(1);\nconsole.log(`shape: \
         ${s.kind}`);\n",
        "observable_operation",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst s = circle(1);\nconst kind = \
         s.kind;\nconsole.log(kind.length);\n",
        "observable_operation",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst labels = { circle: 'round', rectangle: \
         'boxy', triangle: 'pointy' };\nconst s = circle(1);\nconsole.log(labels[s.kind]);\n",
        "observable_operation",
    );
    tags_preserved(
        "import { circle } from './shapes.ts';\nconst s = circle(1);\nconst { kind } = \
         s;\nconsole.log(kind);\n",
        "observable_operation",
    );
}

#[test]
fn external_producers_and_lookalikes_preserve_tags() {
    tags_preserved(
        "import { area, type Shape } from './shapes.ts';\nconst s = \
         JSON.parse('{\"kind\":\"circle\",\"radius\":1}') as \
         Shape;\nconsole.log(area(s).toFixed(2));\n",
        "external_boundary",
    );
    tags_preserved(
        "import { area } from './shapes.ts';\nconst lookalike = { kind: 'circle' as const, \
         radius: 2 };\nconsole.log(area(lookalike).toFixed(2));\n",
        "external_boundary",
    );
    tags_preserved(
        "import { area, circle } from './shapes.ts';\nfunction same<T>(value: T): T { return \
         value; }\nconsole.log(area(same(circle(1))).toFixed(2));\n",
        "type_constraint",
    );
}

#[test]
fn type_level_uses_of_tags_preserve_tags() {
    tags_preserved(
        "import { circle, type Shape } from './shapes.ts';\ntype Round = Extract<Shape, { kind: \
         'circle' }>;\nconst r = circle(1) as Round;\nconsole.log(r.radius);\n",
        "type_constraint",
    );
    tags_preserved(
        "import { circle, type Shape } from './shapes.ts';\nconst kinds: Shape['kind'][] = \
         ['circle'];\nconsole.log(kinds.includes(circle(1).kind));\n",
        "type_constraint",
    );
    // A type predicate that forgets the domain.
    tags_preserved(
        r#"
import { circle, type Shape } from "./shapes.ts";
type Named = { kind: string; name?: string };
function named(value: Shape | Named): value is Named { return "name" in value; }
const s: Shape | Named = circle(1);
console.log(named(s) ? s.kind : "anonymous");
"#,
        "escapes",
    );
}

#[test]
fn public_and_javascript_boundaries_preserve_tags() {
    // The entry exports the union type to external consumers.
    tags_preserved(
        "import { area, circle } from './shapes.ts';\nexport type { Shape } from \
         './shapes.ts';\nconsole.log(area(circle(1)).toFixed(2));\n",
        "public_export",
    );
    // An entry export returns domain values.
    tags_preserved(
        "import { circle } from './shapes.ts';\nexport function make() { return circle(1); \
         }\nconsole.log(make().kind === 'circle');\n",
        "public_export",
    );
    // JavaScript calls a function that produces domain values.
    let temp = project(&[
        (
            "main.ts",
            "import { describe } from './reader.js';\nconsole.log(describe());\n",
        ),
        (
            "reader.js",
            "import { circle } from './shapes.ts';\nexport function describe() { return \
             JSON.stringify(circle(3)); }\n",
        ),
        ("shapes.ts", SHAPES),
    ]);
    std::fs::write(
        temp.path().join("tsconfig.json"),
        TSCONFIG.replace("\"strict\":true", "\"strict\":true,\"allowJs\":true"),
    )
    .unwrap();
    let report = check(temp.path(), &args(&only(&[DISCRIMINANT])));
    assert_eq!(applied(&report, DISCRIMINANT), 0, "{report:#}");
    assert!(
        skipped_reasons(&report, DISCRIMINANT).contains(&"external_boundary".to_owned()),
        "{report:#}"
    );
}

#[test]
fn check_and_output_decisions_are_identical_and_deterministic() {
    let temp = project(&[
        (
            "main.ts",
            "import { area, circle } from './shapes.ts';\nfunction f(n: number) { const p = { x: \
             n, y: n + 1, z: 0 }; return p.x * p.y; \
             }\nconsole.log(area(circle(f(2))).toFixed(1));\n",
        ),
        ("shapes.ts", SHAPES),
    ]);
    let strip = |mut report: Value| {
        report.as_object_mut().unwrap().remove("measurements");
        report
    };
    let first = strip(check(temp.path(), &[]));
    let second = strip(check(temp.path(), &[]));
    assert_eq!(first, second);
    let written = strip(optimize(temp.path(), &[]));
    assert_eq!(first, written);
    assert_eq!(applied(&written, DISCRIMINANT), 1, "{written:#}");
    // With every pass enabled, constant and field passes may simplify the
    // local object first; this test checks decision parity, not ownership.
    assert!(applied(&written, AGGREGATE) <= 1, "{written:#}");
    same_behavior(temp.path());
}
