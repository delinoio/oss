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

/// Removing the last user of an import keeps the initialization of a
/// module with effects (as a bare import) and drops a pure module.
#[test]
fn residual_preserves_module_initialization() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { live } from \"./dep.ts\";\nconsole.log(live());\n",
            ),
            (
                "dep.ts",
                r#"import { sideLog } from "./side.ts";
import { pureHelper } from "./pure.ts";
export function live() { return 1; }
export function deadUser() { sideLog(); return pureHelper(); }
"#,
            ),
            (
                "side.ts",
                "console.log(\"side init\");\nexport function sideLog() { console.log(\"side\"); \
                 }\n",
            ),
            ("pure.ts", "export function pureHelper() { return 2; }\n"),
        ],
        &[RESIDUAL],
    );
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(!dep.contains("deadUser"), "{dep}");
    assert!(dep.contains("import \"./side.js\""), "{dep}");
    assert!(!dep.contains("pure.js"), "{dep}");
}

/// Declarations whose removal could change types, effects, exceptions or
/// what outside code can reach by name stay.
#[test]
fn residual_boundaries_stay() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import { live } from "./dep.ts";
import * as all from "./namespace.ts";
export function publicApi() { return "api"; }
console.log(live(), Object.keys(all).join(","));
const lazy = await import("./lazy.ts");
console.log(Object.keys(lazy).join(","));
"#,
            ),
            (
                "dep.ts",
                r#"function typeSource() { return { a: 1 }; }
type Shape = ReturnType<typeof typeSource>;
const shape: Shape = { a: 2 };
function register(name: string) { console.log("registered", name); return name; }
const registered = register("x");
class StaticInit { static { console.log("static block"); } }
class StaticEffect { static value = register("static"); }
function makeBase() { console.log("heritage evaluated"); return class {}; }
class Derived extends makeBase() {}
export default function unusedDefault() { return 0; }
export function live() { return shape.a; }
"#,
            ),
            (
                "namespace.ts",
                "export function viaNamespace() { return 1; }\n",
            ),
            (
                "lazy.ts",
                "export function viaDynamicImport() { return 2; }\n",
            ),
        ],
        &[RESIDUAL],
    );
    let dep = emitted_or_source(temp.path(), "dep.ts");
    for live in [
        "typeSource",
        "register(\"x\")",
        "StaticInit",
        "StaticEffect",
        "makeBase()",
        "unusedDefault",
    ] {
        assert!(dep.contains(live), "{live} missing in {dep}");
    }
    assert!(emitted_or_source(temp.path(), "namespace.ts").contains("viaNamespace"));
    assert!(emitted_or_source(temp.path(), "lazy.ts").contains("viaDynamicImport"));
    assert!(emitted_or_source(temp.path(), "main.ts").contains("publicApi"));
    let reasons = skipped_reasons(&report, RESIDUAL);
    for reason in [
        "type_constraint",
        "side_effects",
        "reflection",
        "public_export",
        "unsupported_syntax",
    ] {
        assert!(reasons.iter().any(|r| r == reason), "{reason}: {reasons:?}");
    }
}

/// Earlier removals leave helpers unused; the cleanup removes them in a
/// later round only when residual-dependency-removal is enabled.
#[test]
fn residual_cleans_up_after_member_removal() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            "import { Widget } from \"./widget.ts\";\nconsole.log(new Widget().render2());\n",
        ),
        (
            "widget.ts",
            r#"import { format } from "./format.ts";
function onlyForUnused(): string { return format("unused"); }
function factory() { return 1; }
export class Widget {
  produced = factory;
  render2() { return "widget"; }
  unusedMethod() { return onlyForUnused(); }
}
"#,
        ),
        (
            "format.ts",
            "export function format(v: string) { return `[${v}]`; }\n",
        ),
    ];
    let (temp, report) = run(files, &[METHOD, FIELD, RESIDUAL]);
    assert!(applied(&report, METHOD) > 0, "{report:#}");
    assert!(applied(&report, FIELD) > 0, "{report:#}");
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let widget = emitted(temp.path(), "widget.ts");
    for dead in [
        "unusedMethod",
        "onlyForUnused",
        "produced",
        "factory",
        "format",
    ] {
        assert!(!widget.contains(dead), "{dead} in {widget}");
    }
    let (temp, report) = run(files, &[METHOD, FIELD]);
    assert_eq!(applied(&report, RESIDUAL), 0);
    let widget = emitted(temp.path(), "widget.ts");
    assert!(!widget.contains("unusedMethod"), "{widget}");
    assert!(widget.contains("function onlyForUnused"), "{widget}");
    assert!(widget.contains("function factory"), "{widget}");
}

/// Runs original and optimized sources of a cyclic project whose
/// initialization throws, and asserts both raise the same `ReferenceError`
/// before any stdout. `same_behavior` alone cannot prove the exception stays.
fn assert_tdz_preserved(root: &std::path::Path) {
    let before = std::process::Command::new("node")
        .arg(root.join("main.ts"))
        .output()
        .unwrap();
    let after = run_optimized(root);
    for (label, output) in [("original", &before), ("optimized", &after)] {
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(!output.status.success(), "{label} must fail: {stderr}");
        assert!(stderr.contains("ReferenceError"), "{label}: {stderr}");
        assert!(output.stdout.is_empty(), "{label} printed output");
    }
}

/// Entry `main.ts` and `b.ts` import each other. `b.ts` reads the entry
/// binding under runtime `typeof` while the entry has not initialized it.
fn tdz_cycle<'a>(entry_export: &'a str, b_unused: &'a str) -> Vec<(&'a str, String)> {
    vec![
        ("main.ts", format!("import \"./b.ts\";\n{entry_export}\n")),
        (
            "b.ts",
            format!(
                "import {{ x }} from \"./main.ts\";\n{b_unused}\nconsole.log(\"b initialized\");\n"
            ),
        ),
    ]
}

fn run_tdz(entry_export: &str, b_unused: &str, keep: &[&str]) {
    let owned = tdz_cycle(entry_export, b_unused);
    let files: Vec<(&str, &str)> = owned.iter().map(|(n, c)| (*n, c.as_str())).collect();
    let temp = project(&files);
    optimize(temp.path(), &args(&only(keep)));
    assert_tdz_preserved(temp.path());
}

/// Runtime `typeof` of an imported lexical binding still throws in its
/// temporal dead zone, so its unused declaration must stay.
#[test]
fn residual_keeps_typeof_of_imported_lexical_binding() {
    for (entry_export, b_unused) in [
        ("export const x = 1;", "const unused = typeof x;"),
        ("export const x = 1;", "const unused = typeof (x);"),
        ("export let x = 1;", "const unused = typeof x;"),
        ("export class x {}", "const unused = typeof x;"),
        ("export class x {}", "const unused = typeof (x);"),
    ] {
        run_tdz(entry_export, b_unused, &[RESIDUAL]);
        run_tdz(entry_export, b_unused, &[]);
        let owned = tdz_cycle(entry_export, b_unused);
        let files: Vec<(&str, &str)> = owned.iter().map(|(n, c)| (*n, c.as_str())).collect();
        let temp = project(&files);
        optimize(temp.path(), &[]);
        assert_tdz_preserved(temp.path());
    }
}

/// With every pass disabled, nothing may rewrite the module: the original
/// exception is trivially preserved. Guards the test helper itself.
#[test]
fn residual_typeof_tdz_all_passes_disabled() {
    let owned = tdz_cycle("export const x = 1;", "const unused = typeof x;");
    let files: Vec<(&str, &str)> = owned.iter().map(|(n, c)| (*n, c.as_str())).collect();
    let temp = project(&files);
    let disabled = only(&[]);
    optimize(temp.path(), &args(&disabled));
    assert_tdz_preserved(temp.path());
    assert!(emitted_or_source(temp.path(), "b.ts").contains("typeof x"));
}

/// A `typeof` read deferred into an exported function runs after module
/// initialization, so behavior is preserved and unrelated dead code still
/// goes away. Type-only `typeof` queries do not become runtime reads.
#[test]
fn residual_typeof_after_initialization_still_optimizes() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            "import { kind } from \"./b.ts\";\nexport const x = 1;\nconsole.log(kind());\n",
        ),
        (
            "b.ts",
            r#"import { x } from "./main.ts";
export type XType = typeof x;
const deadLocal = { a: 1 };
export function kind(): string { return typeof x; }
"#,
        ),
    ];
    let (temp, report) = run(files, &[RESIDUAL]);
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let b = emitted(temp.path(), "b.ts");
    assert!(!b.contains("deadLocal"), "{b}");
    assert!(!b.contains("XType"), "{b}");
    assert!(b.contains("typeof x"), "{b}");
    run_all(files);
}

/// Initialized same-function locals and undeclared names keep the
/// non-throwing proof under runtime `typeof`.
#[test]
fn residual_typeof_of_initialized_or_undeclared_name_is_removed() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { value } from \"./dep.ts\";\nconsole.log(value());\n",
            ),
            (
                "dep.ts",
                r#"const local = 1;
const unusedProbe = typeof local;
// @ts-ignore Undeclared names never throw under typeof.
const unusedUndeclared = typeof notDeclaredAnywhere;
export function value() { return local; }
"#,
            ),
        ],
        &[RESIDUAL],
    );
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(!dep.contains("unusedProbe"), "{dep}");
    assert!(!dep.contains("unusedUndeclared"), "{dep}");
}

/// Ambient declarations (project `.d.ts` files and `declare` statements)
/// create no runtime lexical binding, so `typeof` of such a name never
/// throws and its unused probe is removable.
#[test]
fn residual_typeof_of_ambient_name_is_removed() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { value } from \"./dep.ts\";\nconsole.log(value());\n",
            ),
            (
                "globals.d.ts",
                "declare const __DEV__: boolean;\ndeclare class HostClass {}\n",
            ),
            (
                "dep.ts",
                r#"declare const LOCAL_FLAG: string;
const unusedDev = typeof __DEV__;
const unusedHostClass = typeof HostClass;
const unusedLocalFlag = typeof LOCAL_FLAG;
export function value() { return 1; }
"#,
            ),
        ],
        &[RESIDUAL],
    );
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    for probe in ["unusedDev", "unusedHostClass", "unusedLocalFlag"] {
        assert!(!dep.contains(probe), "{probe}: {dep}");
    }
}

/// Class and catch bindings that are initialized before a same-scope
/// `typeof` read keep the non-throwing proof.
#[test]
fn residual_typeof_of_initialized_class_and_catch_binding_is_removed() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { value } from \"./dep.ts\";\nconsole.log(value());\n",
            ),
            (
                "dep.ts",
                r#"class Local {}
const unusedClass = typeof Local;
try { throw 1; } catch (caught) { const unusedCatch = typeof caught; }
export function value() { return 1; }
"#,
            ),
        ],
        &[RESIDUAL],
    );
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    for probe in ["unusedClass", "unusedCatch"] {
        assert!(!dep.contains(probe), "{probe}: {dep}");
    }
}

/// Enums compile to hoisted `var` bindings, so `typeof` of one never
/// throws. Node cannot run enums in strip-only mode, so this fixture checks
/// the emitted output instead of comparing behavior.
#[test]
fn residual_typeof_of_enum_is_removed() {
    let temp = project(&[
        (
            "main.ts",
            "import { value } from \"./dep.ts\";\nconsole.log(value());\n",
        ),
        (
            "dep.ts",
            r#"enum Kind { A, B }
const unusedEnum = typeof Kind;
export function value() { return 1; }
"#,
        ),
    ]);
    let report = optimize(temp.path(), &args(&only(&[RESIDUAL])));
    assert!(applied(&report, RESIDUAL) > 0, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(!dep.contains("unusedEnum"), "{dep}");
}

/// A `typeof` read of a class before its declaration throws, so the unused
/// probe stays in every pass configuration.
#[test]
fn residual_keeps_typeof_of_class_before_declaration() {
    let files: &[(&str, &str)] = &[
        ("main.ts", "import \"./dep.ts\";\nconsole.log(\"main\");\n"),
        (
            "dep.ts",
            r#"// @ts-ignore Used before its declaration.
const unusedProbe = typeof Late;
class Late {}
"#,
        ),
    ];
    for keep in [&[RESIDUAL][..], &[][..]] {
        let temp = project(files);
        optimize(temp.path(), &args(&only(keep)));
        assert_tdz_preserved(temp.path());
    }
    let temp = project(files);
    optimize(temp.path(), &[]);
    assert_tdz_preserved(temp.path());
}

/// A parameter default that reads a later parameter under `typeof` throws
/// `ReferenceError` (temporal dead zone), so signature reduction must not
/// drop that unused parameter. A default that reads an earlier parameter
/// stays removable.
#[test]
fn signature_reduction_keeps_typeof_of_later_parameter_default() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"export {};
// @ts-ignore Reading a later parameter is a TDZ error the checker rejects.
function probe(a: string = typeof b, b: number = 1): number { return b; }
function earlier(first: number, unused: string = typeof first): number { return first; }
try { console.log(probe()); } catch (e) { console.log("threw", (e as Error).name); }
console.log(earlier(2));
"#,
    )];
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(&["signature-reduction"])));
    same_behavior(temp.path());
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("typeof b"), "{main}");
    // The earlier-parameter default is initialized, so that parameter goes.
    assert!(!main.contains("unused"), "{main}\n{report:#}");
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

// ---------------------------------------------------------------------------
// unused-method-removal

const METHODS: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { Counter, Other } from "./counter.ts";
const counter = new Counter();
counter.inc();
counter.inc();
const other = new Other();
console.log(counter.value, other.shared(), counter.shared());
"#,
    ),
    (
        "counter.ts",
        r#"export class Counter {
  value = 0;
  inc() { this.#bump(); }
  #bump() { this.value += 1; }
  /** Nobody calls this. */
  unusedHelper(): number { return this.value * 2; }
  static unusedStatic(): Counter { return new Counter(); }
  get unusedAccessor(): number { return this.value; }
  set unusedAccessor(v: number) { this.value = v; }
  #unusedPrivate(): number { return this.#unusedPrivate(); }
  unusedRecursive(n: number): number { return n <= 0 ? 0 : this.unusedRecursive(n - 1); }
  shared() { return "counter"; }
}
export class Other {
  shared() { return "other"; }
}
"#,
    ),
];

#[test]
fn methods_unused_members_are_removed_across_modules() {
    let (temp, report) = run(METHODS, &[METHOD]);
    assert!(applied(&report, METHOD) >= 5, "{report:#}");
    let counter = emitted(temp.path(), "counter.ts");
    for dead in [
        "unusedHelper",
        "unusedStatic",
        "unusedAccessor",
        "#unusedPrivate",
        "unusedRecursive",
        "Nobody calls",
    ] {
        assert!(!counter.contains(dead), "{dead} in {counter}");
    }
    for live in ["inc()", "#bump()", "shared()"] {
        assert!(counter.contains(live), "{live} missing in {counter}");
    }
    assert!(saved(&report) > 0);
}

#[test]
fn methods_with_all_passes_enabled() {
    let (temp, report) = run_all(METHODS);
    assert!(applied(&report, METHOD) >= 5, "{report:#}");
    assert!(!emitted(temp.path(), "counter.ts").contains("unusedHelper"));
}

#[test]
fn methods_disabled_never_runs() {
    let (temp, report) = run(METHODS, &[FIELD, RESIDUAL]);
    assert_eq!(applied(&report, METHOD), 0);
    assert!(emitted_or_source(temp.path(), "counter.ts").contains("unusedHelper"));
}

/// Each method name below is spelled only in its declaration; something
/// other than a name mention still reaches it.
#[test]
fn methods_reachable_without_static_mentions_stay() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import { Dynamic, Reflected, Protocol } from "./dep.ts";
const key = ["dyna", "micCall"].join("");
const d = new Dynamic();
console.log((d as any)[key]());
const r = new Reflected();
console.log(Object.getOwnPropertyNames(Object.getPrototypeOf(r)).join(","));
const p = new Protocol();
console.log(JSON.stringify({ p }), String(p));
"#,
            ),
            (
                "dep.ts",
                r#"export class Dynamic { dynamicCall() { return "dynamic"; } }
export class Reflected { reflectedOnly() { return 1; } }
export class Protocol {
  toJSON() { return "json"; }
  toString() { return "text"; }
}
"#,
            ),
        ],
        &[METHOD],
    );
    assert_eq!(applied(&report, METHOD), 0, "{report:#}");
    let dep = emitted_or_source(temp.path(), "dep.ts");
    for live in ["dynamicCall", "reflectedOnly", "toJSON", "toString"] {
        assert!(dep.contains(live), "{live} missing in {dep}");
    }
    let reasons = skipped_reasons(&report, METHOD);
    for reason in ["dynamic_access", "reflection", "special_property"] {
        assert!(reasons.iter().any(|r| r == reason), "{reasons:?}");
    }
}

#[test]
fn methods_overrides_super_calls_and_extraction_stay() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import { Derived, Holder } from "./dep.ts";
const d = new Derived();
console.log(d.describe());
const h = new Holder();
const extracted = h.callback;
console.log(extracted.call(h), h.identity === h.identity);
"#,
            ),
            (
                "dep.ts",
                r#"class Base {
  greet() { return "base"; }
  describe() { return this.greet(); }
}
export class Derived extends Base {
  override greet() { return "derived+" + super.greet(); }
}
export class Holder {
  tag = "holder";
  callback() { return this.tag; }
  identity() { return 1; }
}
"#,
            ),
        ],
        &[METHOD],
    );
    assert_eq!(applied(&report, METHOD), 0, "{report:#}");
    let dep = emitted_or_source(temp.path(), "dep.ts");
    for live in ["greet()", "super.greet", "callback()", "identity()"] {
        assert!(dep.contains(live), "{live} missing in {dep}");
    }
}

#[test]
fn methods_public_entry_exports_stay() {
    let (temp, report) = run(
        &[(
            "main.ts",
            r#"export class Api {
  unusedButPublic() { return 1; }
  used() { return 2; }
}
console.log(new Api().used());
"#,
        )],
        &[METHOD],
    );
    assert_eq!(applied(&report, METHOD), 0);
    assert!(skipped_reasons(&report, METHOD).contains(&"public_export".to_owned()));
    assert!(emitted_or_source(temp.path(), "main.ts").contains("unusedButPublic"));
}

#[test]
fn methods_interface_requirements_and_namespace_exposure_stay() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import * as ns from "./dep.ts";
import { make } from "./impl.ts";
const all: Record<string, unknown> = { ...ns };
console.log(Object.keys(all).length, make().run());
"#,
            ),
            (
                "dep.ts",
                "export class Exposed { exposedOnly() { return 1; } }\n",
            ),
            (
                "impl.ts",
                r#"interface Runner { run(): number; required(): number }
class Impl implements Runner {
  run() { return 3; }
  required() { return 4; }
}
export function make(): Runner { return new Impl(); }
"#,
            ),
        ],
        &[METHOD],
    );
    assert_eq!(applied(&report, METHOD), 0, "{report:#}");
    assert!(emitted_or_source(temp.path(), "dep.ts").contains("exposedOnly"));
    assert!(emitted_or_source(temp.path(), "impl.ts").contains("required()"));
}

/// Values that reach `any` or a generic parameter may meet computed-name
/// lookups there. Numeric keys and keys from enumeration never name a
/// method, so they do not block unrelated classes.
#[test]
fn methods_imprecise_flows_and_key_provenance() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import { Escaping, Generic, Kept } from "./dep.ts";
const escaping = new Escaping();
const holder: any = escaping;
console.log(holder[["esc", "aped"].join("")]());
function invoke<T>(target: T, key: string): unknown {
  return (target as any)[key]();
}
console.log(invoke(new Generic(), ["generic", "Call"].join("")));
const kept = new Kept();
const table: any = { a: 1, b: 2 };
let sum = 0;
for (const key in table) sum += table[key];
const list: any = [1, 2, 3];
for (let i = 0; i < list.length; i++) sum += list[i];
console.log(sum, kept.live());
"#,
            ),
            (
                "dep.ts",
                r#"export class Escaping { escaped() { return "escaped"; } }
export class Generic { genericCall() { return "generic"; } }
export class Kept {
  live() { return "live"; }
  removable() { return "gone"; }
}
"#,
            ),
        ],
        &[METHOD],
    );
    assert_eq!(applied(&report, METHOD), 1, "{report:#}");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(dep.contains("escaped()"), "{dep}");
    assert!(dep.contains("genericCall()"), "{dep}");
    assert!(!dep.contains("removable"), "{dep}");
}

#[test]
fn empty_mapped_types_preserve_members_across_imprecise_flows() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import { Kept } from "./dep.ts";
const value: Record<never, never> = new Kept();
const key = ["obser", "vable"].join("");
console.log((value as any)[key]());
"#,
            ),
            (
                "dep.ts",
                "export class Kept { observable() { return 42; } }",
            ),
        ],
        &[METHOD],
    );
    assert_eq!(applied(&report, METHOD), 0, "{report:#}");
    assert!(emitted_or_source(temp.path(), "dep.ts").contains("observable()"));
}

// ---------------------------------------------------------------------------
// unused-field-removal

const FIELDS: &[(&str, &str)] = &[
    (
        "main.ts",
        r#"import { Tracker } from "./tracker.ts";
const tracker = new Tracker("a");
tracker.bump();
console.log(tracker.bump(), tracker.log.join(","));
const config = { used: 1, unusedProperty: 2, alsoUnusedProperty: "x" };
config.unusedProperty = effect("write");
console.log(config.used, (config.unusedProperty = 7));
function effect(tag: string): number {
  console.log("effect", tag);
  return 5;
}
"#,
    ),
    (
        "tracker.ts",
        r#"export class Tracker {
  count = 0;
  unusedInitialized = 42;
  unusedDeclared?: string;
  static unusedStatic = "static";
  #unusedPrivate = 1;
  log: string[] = [];
  deadWrite = 0;
  constructor(label: string) {
    this.deadWrite = sideEffect("ctor");
    this.#unusedPrivate = 2;
    this.log.push(label);
  }
  bump() {
    this.count += 1;
    return this.count;
  }
}
function sideEffect(tag: string) {
  console.log("effect", tag);
  return 1;
}
"#,
    ),
];

#[test]
fn fields_unread_fields_and_dead_writes_are_removed() {
    let (temp, report) = run(FIELDS, &[FIELD]);
    assert!(applied(&report, FIELD) >= 6, "{report:#}");
    let tracker = emitted(temp.path(), "tracker.ts");
    for dead in [
        "unusedInitialized",
        "unusedDeclared",
        "unusedStatic",
        "#unusedPrivate",
        "deadWrite",
    ] {
        assert!(!tracker.contains(dead), "{dead} in {tracker}");
    }
    // The right-hand side of the dead write still runs.
    assert!(tracker.contains("sideEffect(\"ctor\")"), "{tracker}");
    assert!(tracker.contains("count"), "{tracker}");
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("unusedProperty:"), "{main}");
    assert!(!main.contains("alsoUnusedProperty"), "{main}");
    assert!(main.contains("effect(\"write\")"), "{main}");
    assert!(saved(&report) > 0);
}

#[test]
fn fields_with_all_passes_enabled() {
    let (temp, report) = run_all(FIELDS);
    // With every pass enabled, aggregate-elimination may scalar-replace the
    // closed object literal first; the class fields are still this pass's.
    assert!(applied(&report, FIELD) >= 4, "{report:#}");
    assert!(!emitted(temp.path(), "tracker.ts").contains("unusedInitialized"));
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("unusedProperty:"), "{main}");
}

#[test]
fn fields_disabled_never_runs() {
    let (temp, report) = run(FIELDS, &[METHOD, RESIDUAL]);
    assert_eq!(applied(&report, FIELD), 0);
    assert!(emitted_or_source(temp.path(), "tracker.ts").contains("unusedInitialized"));
}

/// Own properties of objects that escape stay: serialization, enumeration,
/// spread, destructuring, `this` escapes, inheritance, extracted methods
/// and impure initializers.
#[test]
fn fields_of_observable_objects_stay() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                r#"import {
  Serialized, Enumerated, Spread, Destructured, SelfEscaping, Child, Extracted, Impure,
  StaticViaInstance,
} from "./dep.ts";
console.log(JSON.stringify(new Serialized()));
console.log(Object.keys(new Enumerated()).join(","));
const spread = new Spread();
console.log(JSON.stringify({ ...spread }));
const { hiddenRead } = new Destructured();
console.log(hiddenRead);
new SelfEscaping();
console.log(new Child().base);
const extracted = new Extracted();
const other = {};
const setter = extracted.set;
setter.call(other);
console.log(JSON.stringify(other));
console.log(new Impure().kept);
const options = { a: 1, b: 2 };
console.log(Object.keys(options).length);
const viaInstance = new StaticViaInstance();
console.log(Object.keys(viaInstance.constructor).join(","));
"#,
            ),
            (
                "dep.ts",
                r#"export class Serialized { serializedOnly = 1; }
export class Enumerated { enumeratedOnly = 2; }
export class Spread { spreadOnly = 3; }
export class Destructured { hiddenRead = 4; }
const registry: object[] = [];
export class SelfEscaping {
  escapingField = 5;
  constructor() { registry.push(this); console.log(Object.keys(registry[0]).length); }
}
class Base { base = "base"; }
export class Child extends Base { childOnly = 6; }
export class Extracted {
  written = 0;
  set() { this.written = 1; }
}
function effect() { console.log("initializer effect"); return 7; }
export class Impure {
  kept = 0;
  impureField = effect();
}
export class StaticViaInstance { static staticOnly = 8; }
"#,
            ),
        ],
        &[FIELD],
    );
    assert_eq!(applied(&report, FIELD), 0, "{report:#}");
    let dep = emitted_or_source(temp.path(), "dep.ts");
    for live in [
        "serializedOnly",
        "enumeratedOnly",
        "spreadOnly",
        "hiddenRead",
        "escapingField",
        "childOnly",
        "this.written = 1",
        "impureField",
        "staticOnly",
    ] {
        assert!(dep.contains(live), "{live} missing in {dep}");
    }
    assert!(emitted_or_source(temp.path(), "main.ts").contains("b: 2"));
    let reasons = skipped_reasons(&report, FIELD);
    for reason in ["escapes", "inheritance", "mutation", "side_effects"] {
        assert!(reasons.iter().any(|r| r == reason), "{reason}: {reasons:?}");
    }
}

#[test]
fn fields_public_entry_exports_stay() {
    let (temp, report) = run(
        &[(
            "main.ts",
            r#"export class Api {
  unreadPublicField = 1;
  used = 2;
}
console.log(new Api().used);
"#,
        )],
        &[FIELD],
    );
    assert_eq!(applied(&report, FIELD), 0);
    assert!(skipped_reasons(&report, FIELD).contains(&"public_export".to_owned()));
    assert!(emitted_or_source(temp.path(), "main.ts").contains("unreadPublicField"));
}
