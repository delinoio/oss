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
