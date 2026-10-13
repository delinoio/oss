// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `enum-runtime-reduction` and `constant-property-inlining`: positive
//! transformations, preservation boundaries and pass controls.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use std::path::Path;

use common::*;
use serde_json::Value;

const ENUM: &str = "enum-runtime-reduction";
const CONST: &str = "constant-property-inlining";

const ALLOW_JS: &str = r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"allowJs":true,"rewriteRelativeImportExtensions":true}}"#;

/// Node strips types but cannot run enums or namespaces, so fixtures with
/// those compare against the unoptimized emit (every pass disabled) instead
/// of the original sources.
fn needs_emit_baseline(root: &Path) -> bool {
    std::fs::read_dir(root).unwrap().any(|entry| {
        let path = entry.unwrap().path();
        path.extension().is_some_and(|e| e == "ts")
            && std::fs::read_to_string(&path).unwrap().contains("enum ")
    })
}

fn observed(output: std::process::Output) -> (bool, String) {
    (
        output.status.success(),
        String::from_utf8_lossy(&output.stdout).into_owned(),
    )
}

/// Optimizes with `extra` arguments and asserts unchanged behavior: stdout,
/// success and therefore exceptions and effect order.
fn optimize_same(root: &Path, extra: &[&str]) -> Value {
    if !needs_emit_baseline(root) {
        let report = optimize(root, extra);
        same_behavior(root);
        return report;
    }
    let baseline = optimize(root, &args(&only(&[])));
    assert_eq!(baseline["transformations"].as_array().unwrap().len(), 0);
    let before = observed(run_optimized(root));
    std::fs::remove_dir_all(root.join("out")).unwrap();
    let report = optimize(root, extra);
    let after = observed(run_optimized(root));
    assert_eq!(before, after);
    report
}

/// Optimizes with only `passes` enabled and checks behavior.
fn run_only(root: &Path, passes: &[&str]) -> Value {
    optimize_same(root, &args(&only(passes)))
}

/// Optimizes with every pass enabled and checks behavior.
fn run_all(root: &Path) -> Value {
    optimize_same(root, &[])
}

fn no_recheck_failures(report: &Value) {
    for pass in [ENUM, CONST] {
        assert!(
            !skipped_reasons(report, pass).contains(&"recheck_failed".to_owned()),
            "{pass} produced type-invalid code"
        );
    }
}

// ---------------------------------------------------------------------------
// enum-runtime-reduction
// ---------------------------------------------------------------------------

const ENUMS: &[(&str, &str)] = &[
    (
        "dep.ts",
        r#"export enum Direction {
  Up,
  Down = 5,
  Left = -1,
  Right,
}
export enum Color {
  Red = "red",
  Quote = "q\"uo\\te",
}
export function describe(d: Direction): string {
  switch (d) {
    case Direction.Up:
      return "up";
    case Direction.Left:
      return "left";
    default:
      return "other";
  }
}
"#,
    ),
    (
        "main.ts",
        r#"import { Color, Direction, describe } from "./dep.ts";
function pair<T>(value: T): [T, (next: T) => T] {
  return [value, (next) => next];
}
let d = Direction.Up;
d = Direction.Down;
const o = { k: Direction.Left };
o.k = Direction.Right;
const [first, update] = pair(Direction.Up);
const updated = update(Direction.Down);
const c: Color = Color.Quote;
const total = -Direction.Left + Direction.Left ** 2 + Direction.Down;
console.log(d, o.k, first, updated, c, total, describe(Direction.Left));
console.log(Direction.Down.toFixed(1), Color.Red.length, `${Color.Red}!`);
console.log(d === Direction.Down ? "down" : "not down");
"#,
    ),
];

#[test]
fn enum_members_inline_and_enum_objects_disappear() {
    for all in [false, true] {
        let temp = project(ENUMS);
        let report = if all {
            run_all(temp.path())
        } else {
            run_only(temp.path(), &[ENUM])
        };
        assert_eq!(applied(&report, ENUM), 2, "{report:#}");
        assert!(saved(&report) > 0);
        no_recheck_failures(&report);
        let dep = emitted(temp.path(), "dep.ts");
        assert!(!dep.contains("Direction["), "{dep}");
        assert!(!dep.contains("Color["), "{dep}");
        let main = emitted(temp.path(), "main.ts");
        assert!(!main.contains("Direction."), "{main}");
        assert!(!main.contains("Color."), "{main}");
        assert!(main.contains(r#""q\"uo\\te""#), "{main}");
        assert!(main.contains("(-1) ** 2"), "{main}");
    }
}

#[test]
fn enum_reverse_lookup_and_dynamic_access_keep_the_object() {
    let temp = project(&[
        ("dep.ts", "export enum E { A, B }\n"),
        (
            "main.ts",
            "import { E } from './dep.ts';\nconst key: number = Number('1');\nconsole.log(E[E.A], \
             E[key], E.B);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert!(skipped_reasons(&report, ENUM).contains(&"reflection".to_owned()));
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("E[E[\"A\"] = 0] = \"A\""), "{dep}");
    no_recheck_failures(&report);
}

#[test]
fn enum_enumeration_keeps_the_object() {
    let temp = project(&[
        ("dep.ts", "export enum E { A = 'a', B = 'b' }\n"),
        (
            "main.ts",
            "import { E } from './dep.ts';\nconsole.log(Object.keys(E), E.A);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert!(skipped_reasons(&report, ENUM).contains(&"reflection".to_owned()));
    assert!(emitted(temp.path(), "dep.ts").contains("E[\"B\"]"));
}

#[test]
fn enum_passed_as_value_is_preserved() {
    let temp = project(&[
        ("dep.ts", "export enum E { A, B }\n"),
        (
            "main.ts",
            "import { E } from './dep.ts';\nfunction mutate(o: any) { o.A = 7; \
             }\nmutate(E);\nconsole.log(E.A, E.B);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 0);
    assert!(skipped_reasons(&report, ENUM).contains(&"escapes".to_owned()));
    assert!(emitted(temp.path(), "main.ts").contains("E.A"));
}

#[test]
fn enum_member_write_through_cast_is_preserved() {
    let temp = project(&[
        ("dep.ts", "export enum E { A, B }\n"),
        (
            "main.ts",
            "import { E } from './dep.ts';\n(E as any).A = 7;\nconsole.log(E.A, E.B);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("E.A"), "{main}");
    assert!(skipped_reasons(&report, ENUM).contains(&"mutation".to_owned()));
}

#[test]
fn enum_declaration_merging_is_preserved() {
    let temp = project(&[
        (
            "dep.ts",
            "export enum E { A }\nexport enum E { B = 1 }\nexport enum F { X }\nexport namespace \
             F { export const y = 2; }\n",
        ),
        (
            "main.ts",
            "import { E, F } from './dep.ts';\nconsole.log(E.A, E.B, F.X, F.y);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 0);
    assert_eq!(
        skipped_reasons(&report, ENUM)
            .iter()
            .filter(|r| *r == "declaration_merging")
            .count(),
        2
    );
}

#[test]
fn enum_computed_initializers_keep_effects() {
    let temp = project(&[
        (
            "dep.ts",
            "function compute(): number { console.log('computed'); return 3; }\nexport enum E { A \
             = 1, B = compute(), }\n",
        ),
        (
            "main.ts",
            "import { E } from './dep.ts';\nconsole.log(E.A, E.B);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert!(skipped_reasons(&report, ENUM).contains(&"not_constant".to_owned()));
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("compute()"), "{dep}");
}

#[test]
fn enum_public_export_and_javascript_references_are_preserved() {
    let temp = project(&[
        (
            "main.ts",
            "export enum Public { A, B }\nimport { Shared } from './dep.ts';\nimport { read } \
             from './reader.js';\nconsole.log(Public.A, Shared.X, read());\n",
        ),
        ("dep.ts", "export enum Shared { X = 'x' }\n"),
        (
            "reader.js",
            "import { Shared } from './dep.ts';\nexport function read() { return Shared.X; }\n",
        ),
        ("tsconfig.json", ALLOW_JS),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 0);
    let reasons = skipped_reasons(&report, ENUM);
    assert!(reasons.contains(&"public_export".to_owned()), "{reasons:?}");
    assert!(
        reasons.contains(&"external_boundary".to_owned()),
        "{reasons:?}"
    );
}

#[test]
fn enum_reads_before_initialization_keep_their_exception() {
    // `f` runs before the enum statement: the original throws a TypeError.
    let temp = project(&[(
        "main.ts",
        "function f() { return E.A; }\ntry { console.log(f()); } catch (e) { console.log('early', \
         (e as Error).name); }\nenum E { A = 4 }\nconsole.log(E.A);\n",
    )]);
    let report = run_only(temp.path(), &[ENUM]);
    no_recheck_failures(&report);
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("return E.A"), "{main}");
}

#[test]
fn enum_reads_inside_an_import_cycle_keep_their_exception() {
    // main -> a -> b -> a: b evaluates before a declared the enum.
    let temp = project(&[
        (
            "main.ts",
            "import { E } from './a.ts';\nconsole.log(E.A);\n",
        ),
        (
            "a.ts",
            "import { early } from './b.ts';\nexport enum E { A = 1 }\nconsole.log(early);\n",
        ),
        (
            "b.ts",
            "import { E } from './a.ts';\nlet value: string;\ntry { value = String(E.A); } catch \
             (e) { value = (e as Error).name; }\nexport const early = value;\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    no_recheck_failures(&report);
    assert!(emitted(temp.path(), "b.ts").contains("E.A"));
}

#[test]
fn enum_namespace_imports_and_type_uses_are_supported() {
    let temp = project(&[
        (
            "dep.ts",
            "export enum Kind { Circle = 'circle', Square = 'square' }\nexport interface Shape { \
             kind: Kind; size: number }\nexport type CircleOnly = { kind: Kind.Circle };\nexport \
             function unit(): number { return 1; }\n",
        ),
        (
            "main.ts",
            "import * as dep from './dep.ts';\nfunction area(s: dep.Shape): number { return \
             s.kind === dep.Kind.Circle ? 3 * s.size : s.size; }\nconst c: dep.CircleOnly = { \
             kind: dep.Kind.Circle };\nconsole.log(area({ kind: dep.Kind.Square, size: 2 }), \
             c.kind, typeof dep.Kind.Circle, dep.unit());\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 1);
    no_recheck_failures(&report);
    assert!(!emitted(temp.path(), "dep.ts").contains("Kind["));
}

#[test]
fn escaping_namespace_objects_preserve_enums() {
    let temp = project(&[
        ("dep.ts", "export enum E { A, B }\n"),
        (
            "main.ts",
            "import * as dep from './dep.ts';\nconst all: any = dep;\nall.E.A = \
             9;\nconsole.log(dep.E.A);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 0);
    assert!(skipped_reasons(&report, ENUM).contains(&"escapes".to_owned()));
}

#[test]
fn reexported_enums_keep_their_object_but_inline_reads() {
    let temp = project(&[
        ("dep.ts", "export enum E { A = 10, B = 20 }\n"),
        (
            "barrel.ts",
            "export { E } from './dep.ts';\nexport const scale = 2;\n",
        ),
        (
            "main.ts",
            "import { E, scale } from './barrel.ts';\nconsole.log(E.A + E.B, E.A * E.B * scale);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 1);
    assert!(emitted(temp.path(), "dep.ts").contains("E[E[\"A\"] = 10]"));
    assert!(!emitted(temp.path(), "main.ts").contains("E.A"));
}

#[test]
fn verbatim_imports_keep_the_enum_object() {
    // Verbatim module syntax keeps `import { E }` at runtime, so the
    // export must survive even though every read is inlined.
    let temp = project(&[
        ("dep.ts", "export enum E { A = 10, B = 20 }\n"),
        (
            "main.ts",
            "import { E } from './dep.ts';\nconsole.log(E.A + E.B);\n",
        ),
        (
            "tsconfig.json",
            r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"verbatimModuleSyntax":true,"rewriteRelativeImportExtensions":true}}"#,
        ),
    ]);
    let report = run_only(temp.path(), &[ENUM]);
    no_recheck_failures(&report);
    assert!(skipped_reasons(&report, ENUM).contains(&"observable_identity".to_owned()));
    assert!(emitted(temp.path(), "dep.ts").contains("E[E[\"A\"] = 10]"));
}

#[test]
fn const_enums_are_left_to_the_compiler() {
    let temp = project(&[("main.ts", "const enum C { A = 1 }\nconsole.log(C.A);\n")]);
    let report = run_only(temp.path(), &[ENUM]);
    assert_eq!(applied(&report, ENUM), 0);
    assert!(skipped_reasons(&report, ENUM).is_empty());
}

#[test]
fn disabled_enum_pass_never_runs() {
    let temp = project(ENUMS);
    let report = optimize_same(temp.path(), &["--disable-pass", ENUM]);
    assert_eq!(applied(&report, ENUM), 0);
    assert!(emitted(temp.path(), "dep.ts").contains("Direction["));
    assert!(emitted(temp.path(), "main.ts").contains("Direction."));
}

// ---------------------------------------------------------------------------
// constant-property-inlining
// ---------------------------------------------------------------------------

const OBJECTS: &[(&str, &str)] = &[
    (
        "dep.ts",
        r#"export const THEME = { LIGHT: "light", DARK: "dark" } as const;
export const ENV = {
  TEST: "test",
  PRODUCTION: "production",
  RETRIES: 3,
  OFFSET: -2,
  ENABLED: true,
  NOTHING: null,
  MISSING: undefined,
  BIG: 10n,
};
export const KEYS = { "with-dash": "dash", 0: "zero" };
export class Limits {
  static readonly MAX = 10;
  static min = 1;
  static clamp(v: number) {
    return Math.min(Limits.MAX, Math.max(Limits.min, v));
  }
}
"#,
    ),
    (
        "main.ts",
        r#"import { ENV, KEYS, Limits, THEME } from "./dep.ts";
let theme: string = THEME.LIGHT;
theme = THEME.DARK;
const settings = { mode: ENV.TEST };
settings.mode = "other";
function retry(times: number) { return times * 2; }
console.log(theme, settings.mode, ENV.PRODUCTION, retry(ENV.RETRIES), ENV.OFFSET ** 2);
console.log(ENV.ENABLED, ENV.NOTHING, ENV.MISSING, typeof ENV.BIG, KEYS["with-dash"], KEYS[0]);
console.log(Limits.clamp(50), Limits.MAX, Limits.min);
"#,
    ),
];

#[test]
fn constant_properties_inline_across_modules_and_definitions_go() {
    for all in [false, true] {
        let temp = project(OBJECTS);
        let report = if all {
            run_all(temp.path())
        } else {
            run_only(temp.path(), &[CONST])
        };
        assert!(applied(&report, CONST) >= 10, "{report:#}");
        assert!(saved(&report) > 0);
        no_recheck_failures(&report);
        let main = emitted(temp.path(), "main.ts");
        for gone in ["THEME.", "ENV.", "KEYS[", "Limits.MAX", "Limits.min"] {
            assert!(!main.contains(gone), "{gone} in {main}");
        }
        let dep = emitted(temp.path(), "dep.ts");
        // Alone, the pass leaves empty objects behind; with every pass,
        // residual-dependency-removal deletes them as well.
        for object in ["THEME = {}", "ENV = {}"] {
            assert_eq!(dep.contains(object), !all, "{object} in {dep}");
        }
        assert!(!dep.contains("static min"), "{dep}");
        // A fresh `readonly` literal keeps its member type, so the field stays.
        assert!(dep.contains("static MAX = 10"), "{dep}");
        assert!(dep.contains("Math.min(10, Math.max(1, v))"), "{dep}");
    }
}

#[test]
fn written_properties_are_preserved() {
    let temp = project(&[
        ("dep.ts", "export const CONFIG = { a: 1, b: 2 };\n"),
        (
            "main.ts",
            "import { CONFIG } from './dep.ts';\nconsole.log(CONFIG.a);\nCONFIG.a = \
             5;\nconsole.log(CONFIG.a, CONFIG.b);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[CONST]);
    assert!(skipped_reasons(&report, CONST).contains(&"mutation".to_owned()));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("CONFIG.a"), "{main}");
    assert!(!main.contains("CONFIG.b"), "{main}");
}

#[test]
fn aliases_calls_and_dynamic_writes_prevent_inlining() {
    let temp = project(&[
        (
            "dep.ts",
            "export const ALIASED = { a: 1 };\nexport const PASSED = { a: 1 };\nexport const \
             DYNAMIC = { a: 1 };\nexport const METHOD = { a: 1, bump() { this.a++; } };\n",
        ),
        (
            "main.ts",
            r#"import { ALIASED, DYNAMIC, METHOD, PASSED } from './dep.ts';
const alias = ALIASED;
alias.a = 2;
function mutate(o: { a: number }) { o.a = 3; }
mutate(PASSED);
const key = String.fromCharCode(97) as 'a';
DYNAMIC[key] = 4;
METHOD.bump();
console.log(ALIASED.a, PASSED.a, DYNAMIC.a, METHOD.a);
"#,
        ),
    ]);
    let report = run_only(temp.path(), &[CONST]);
    assert_eq!(applied(&report, CONST), 0);
    let reasons = skipped_reasons(&report, CONST);
    assert_eq!(
        reasons.iter().filter(|r| *r == "escapes").count(),
        3,
        "{reasons:?}"
    );
    assert!(reasons.contains(&"mutation".to_owned()), "{reasons:?}");
}

#[test]
fn reflection_allows_inlining_but_keeps_definitions() {
    let temp = project(&[
        (
            "dep.ts",
            "export const SIZES = { small: 1, large: 2 } as const;\n",
        ),
        (
            "main.ts",
            "import { SIZES } from './dep.ts';\nconsole.log(Object.values(SIZES), 'small' in \
             SIZES, SIZES.small, SIZES.large);\nfor (const k in SIZES) console.log(k);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[CONST]);
    assert_eq!(applied(&report, CONST), 2);
    let dep = emitted(temp.path(), "dep.ts");
    assert!(
        dep.contains("small: 1") && dep.contains("large: 2"),
        "{dep}"
    );
    assert!(!emitted(temp.path(), "main.ts").contains("SIZES.small"));
}

#[test]
fn accessors_and_spreads_are_preserved() {
    let temp = project(&[(
        "main.ts",
        r#"let reads = 0;
const GETTER = { get a() { reads++; return 1; }, b: 2 };
const base = { b: 5 };
const SPREAD = { ...base, c: 2 };
console.log(GETTER.a, GETTER.b, SPREAD.c, reads);
"#,
    )]);
    let report = run_only(temp.path(), &[CONST]);
    assert_eq!(applied(&report, CONST), 0);
    let reasons = skipped_reasons(&report, CONST);
    assert!(reasons.contains(&"accessor".to_owned()), "{reasons:?}");
    assert!(
        reasons.contains(&"unsupported_syntax".to_owned()),
        "{reasons:?}"
    );
}

#[test]
fn temporal_dead_zone_reads_keep_their_exception() {
    let temp = project(&[(
        "main.ts",
        "function f() { return LIMITS.max; }\ntry { console.log(f()); } catch (e) { \
         console.log('early', (e as Error).name); }\nconst LIMITS = { max: 5 };\nconsole.log(f(), \
         LIMITS.max);\n",
    )]);
    let report = run_only(temp.path(), &[CONST]);
    no_recheck_failures(&report);
    assert!(emitted(temp.path(), "main.ts").contains("return LIMITS.max"));
}

#[test]
fn type_queries_keep_property_definitions() {
    let temp = project(&[
        (
            "dep.ts",
            "export const MODES = { first: 'a', second: 'b' } as const;\nexport type Mode = \
             typeof MODES[keyof typeof MODES];\nexport const count = 2;\n",
        ),
        (
            "main.ts",
            "import { MODES, count, type Mode } from './dep.ts';\nconst m: Mode = \
             MODES.first;\nconsole.log(m, MODES.second, count);\n",
        ),
    ]);
    let report = run_only(temp.path(), &[CONST]);
    assert_eq!(applied(&report, CONST), 2);
    no_recheck_failures(&report);
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("first: 'a'"), "{dep}");
}

#[test]
fn public_constant_objects_are_preserved() {
    let temp = project(&[(
        "main.ts",
        "export const API = { version: 2 };\nconsole.log(API.version);\n",
    )]);
    let report = run_only(temp.path(), &[CONST]);
    assert_eq!(applied(&report, CONST), 0);
    assert!(skipped_reasons(&report, CONST).contains(&"public_export".to_owned()));
}

#[test]
fn static_field_boundaries_are_preserved() {
    let temp = project(&[(
        "main.ts",
        r#"class Instantiated { static LIMIT = 3; }
new Instantiated();
class Base { static LIMIT = 4; }
class Derived extends Base { static OWN = 8; }
class UsesThis { static LIMIT = 5; static read() { return this.LIMIT; } }
class Written { static LIMIT = 6; }
Written.LIMIT = 7;
console.log(Instantiated.LIMIT, Base.LIMIT, Derived.OWN, UsesThis.LIMIT, UsesThis.read(), Written.LIMIT);
"#,
    )]);
    let report = run_only(temp.path(), &[CONST]);
    assert_eq!(applied(&report, CONST), 0);
    let reasons = skipped_reasons(&report, CONST);
    for reason in ["escapes", "inheritance", "observable_operation", "mutation"] {
        assert!(
            reasons.contains(&reason.to_owned()),
            "{reason}: {reasons:?}"
        );
    }
}

#[test]
fn literal_conditions_and_statement_starts_stay_type_valid() {
    let temp = project(&[(
        "main.ts",
        r#"const FLAGS = { name: "x", count: 2, zero: 0, empty: "" };
function show() { return 1 }
if (FLAGS.name) console.log("named");
console.log(FLAGS.count && "counted", FLAGS.zero || "zero", FLAGS.empty ?? "empty");
show()
FLAGS.name.toUpperCase()
console.log((FLAGS.count, FLAGS.zero), !FLAGS.name);
"#,
    )]);
    let report = run_only(temp.path(), &[CONST]);
    no_recheck_failures(&report);
    assert!(applied(&report, CONST) > 0);
}

#[test]
fn check_and_output_agree_and_repeat_deterministically() {
    let temp = project(OBJECTS);
    let strip = |mut report: Value| {
        report.as_object_mut().unwrap().remove("measurements");
        report
    };
    let first = strip(check(temp.path(), &[]));
    let second = strip(check(temp.path(), &[]));
    let written = strip(optimize(temp.path(), &[]));
    assert_eq!(first, second);
    assert_eq!(first, written);
}

#[test]
fn disabled_constant_pass_never_runs() {
    let temp = project(OBJECTS);
    let report = optimize_same(temp.path(), &["--disable-pass", CONST]);
    assert_eq!(applied(&report, CONST), 0);
    let main = emitted(temp.path(), "main.ts");
    // Reads stay reads; property-shortening may still rename the keys.
    assert!(main.contains("THEME."), "{main}");
    assert!(main.contains("Limits.MAX"), "{main}");
}

#[test]
fn object_truthiness_in_if_conditions_preserves_the_binding() {
    let temp = project(&[(
        "main.ts",
        r#"const options = { descriptiveValue: 42 };
if (options) console.log(options.descriptiveValue);
"#,
    )]);
    optimize_same(temp.path(), &args(&only(&[CONST])));
}
