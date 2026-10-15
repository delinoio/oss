// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `type-directed-control-flow`, `primitive-operation-simplification` and
//! `intrinsic-specialization`: positive fixtures with size benefits and
//! preservation fixtures for every listed safety boundary. Every fixture
//! compares Node output of the original and optimized program.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use common::*;
use serde_json::Value;

const TDCF: &str = "type-directed-control-flow";
const POS: &str = "primitive-operation-simplification";
const IS: &str = "intrinsic-specialization";

/// Optimizes with only `keep` enabled and checks behavior equality.
fn run(files: &[(&str, &str)], keep: &[&str]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(keep)));
    same_behavior(temp.path());
    (temp, report)
}

/// Optimizes with every pass enabled and checks behavior equality.
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

fn mentions_pass(report: &Value, pass: &str) -> bool {
    report["transformations"]
        .as_array()
        .unwrap()
        .iter()
        .any(|t| t["pass"] == pass || t["uses"].as_array().unwrap().iter().any(|u| u == pass))
}

// ---------------------------------------------------------------------------
// type-directed-control-flow
// ---------------------------------------------------------------------------

const TDCF_DEP: &str = r#"
export function shout(value: string): string {
  if (typeof value !== "string") {
    throw new TypeError("expected a string");
  }
  return value.toUpperCase();
}
export function total(items: number[]): number {
  if (items == null) {
    return 0;
  }
  let sum = 0;
  for (const item of items) sum += item;
  return sum;
}
"#;

const TDCF_MAIN: &str = r#"
import { shout, total } from "./dep.ts";
const VERBOSE = false;
function log(message: string) {
  if (VERBOSE) {
    console.log("verbose:", message);
  }
  console.log(message);
}
function describe(kind: string): string {
  switch (kind) {
    case "circle":
    case "disc":
      return "round";
    case "square":
      return "boxy";
    default:
      return "unknown";
  }
}
function width(box?: { width: number }): number {
  if (!box) return 0;
  return box?.width;
}
function area(size?: { width: number; height: number }): number {
  return size?.width ?? 0;
}
function label(name: string, fallback: string): string {
  return name ?? fallback;
}
function sign(n: number): string {
  if (typeof n === "number") {
    return n < 0 ? "-" : "+";
  }
  return "?";
}
let seen = "";
function track(): string {
  if ((seen = "tracked")) {
    return "yes";
  }
  return "no";
}
log(shout("hello"));
log(String(total([1, 2, 3])));
log(describe("circle"));
log(String(width({ width: 4 })) + String(width()));
log(String(area({ width: 3, height: 1 })));
log(label("named", "anon"));
log(sign(-2) + sign(5));
log(track() + seen);
"#;

const TDCF_FILES: &[(&str, &str)] = &[("main.ts", TDCF_MAIN), ("dep.ts", TDCF_DEP)];

#[test]
fn control_flow_simplifies_proven_tests_and_branches() {
    let (temp, report) = run(TDCF_FILES, &[TDCF]);
    assert!(applied(&report, TDCF) >= 8, "{report:#}");
    assert!(saved(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    let dep = emitted(temp.path(), "dep.ts");
    // Constant `if` on a const binding.
    assert!(!main.contains("verbose:"), "{main}");
    // Switch over a proven literal keeps only the selected clauses.
    assert!(
        !main.contains("boxy") && !main.contains("unknown"),
        "{main}"
    );
    // Optional chain after a dominating guard.
    assert!(main.contains("return box.width"), "{main}");
    // Optional chain on a proven argument with a nullable parameter type.
    assert!(main.contains("size.width ?? 0"), "{main}");
    // `??` with a proven non-nullish left operand.
    assert!(main.contains("return name;"), "{main}");
    // Unreachable code after the eliminated `if`.
    assert!(!main.contains("\"?\""), "{main}");
    // The impure condition is kept as a statement.
    assert!(main.contains("seen = \"tracked\";"), "{main}");
    assert!(!main.contains("return \"no\""), "{main}");
    // Cross-module type test and null check on closed internal functions.
    assert!(!dep.contains("typeof value"), "{dep}");
    assert!(!dep.contains("items == null"), "{dep}");
}

#[test]
fn control_flow_with_all_passes_enabled() {
    let (temp, report) = run_all(TDCF_FILES);
    assert!(applied(&report, TDCF) > 0);
    assert!(saved(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("verbose:"));
    // Check mode makes the same decisions as publishing mode.
    let checked = check(temp.path(), &[]);
    assert_eq!(without_measurements(checked), without_measurements(report));
}

#[test]
fn control_flow_follows_runtime_values_not_assertions() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"
const claimed = 42 as unknown as string;
if (typeof claimed === "string") {
  console.log("claimed string");
} else {
  console.log("runtime number");
}
const missing = undefined as unknown as { x: number };
console.log(missing?.x);
const present = { x: 1 } as { x: number } | undefined;
console.log(present?.x);
"#,
    )];
    let (temp, report) = run(files, &[TDCF]);
    let main = emitted(temp.path(), "main.ts");
    // The runtime value is a number; the branch the type claims is dead.
    assert!(!main.contains("claimed string"), "{main}");
    assert!(main.contains("runtime number"));
    // The checker says non-nullable, the runtime value is undefined.
    assert!(main.contains("missing?.x"), "{main}");
    assert!(skipped_reasons(&report, TDCF).contains(&"unproven_runtime_fact".to_owned()));
    // The runtime value is an object although the type admits undefined.
    assert!(main.contains("present.x"), "{main}");
}

#[test]
fn control_flow_preserves_unproven_and_effectful_sites() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"
export function publicCheck(value: string): string {
  if (typeof value !== "string") return "not a string";
  return value;
}
function lateRead(): string {
  if (typeof late === "string") return "string";
  return "other";
}
try {
  console.log(lateRead());
} catch (error) {
  console.log((error as Error).name);
}
const late = "value";
console.log(lateRead());
let mode = "a";
function switchMode() {
  mode = "b";
}
switchMode();
if (mode === "a") console.log("mode a");
else console.log("mode b");
function narrowed(input: string | number): number {
  if (typeof input === "string") return input.length;
  return input;
}
console.log(narrowed("four"));
function callback(text: string): string {
  if (typeof text !== "string") return "?";
  return text;
}
console.log(["a", "b"].map(callback).join(","));
const DEBUG = false;
if (DEBUG) {
  var hoisted: number | undefined = 1;
}
console.log(hoisted);
let calls = 0;
function counted(): boolean {
  calls++;
  return true;
}
const always = { ready: true };
if (counted() && always) console.log("counted", calls);
console.log(publicCheck("ok"));
"#,
    )];
    let (temp, report) = run(files, &[TDCF]);
    let main = emitted(temp.path(), "main.ts");
    // Public entry export: the parameter is not closed.
    assert!(main.contains("typeof value !== \"string\""), "{main}");
    // Temporal dead zone: the read must still happen (and throw).
    assert!(main.contains("typeof late"), "{main}");
    // Mutated binding.
    assert!(main.contains("mode === \"a\""), "{main}");
    // Narrowing that the kept code relies on.
    assert!(main.contains("typeof input === \"string\""), "{main}");
    assert!(skipped_reasons(&report, TDCF).contains(&"type_constraint".to_owned()));
    // Escaping callback: not every call site is known.
    assert!(main.contains("typeof text !== \"string\""), "{main}");
    // Removing the branch would remove a hoisted `var`.
    assert!(main.contains("var hoisted"), "{main}");
    // The effectful call stays.
    assert!(main.contains("counted()"), "{main}");
}

#[test]
fn control_flow_disabled_never_runs() {
    let temp = project(TDCF_FILES);
    let report = optimize(temp.path(), &["--disable-pass", TDCF]);
    assert_eq!(applied(&report, TDCF), 0);
    assert!(!mentions_pass(&report, TDCF));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("verbose:"));
    assert!(main.contains("box?.width"));
    same_behavior(temp.path());
}

// ---------------------------------------------------------------------------
// primitive-operation-simplification
// ---------------------------------------------------------------------------

const POS_DEP: &str = r#"
export function format(count: number, unit: string): string {
  return String(count) + " " + String(unit);
}
"#;

const POS_MAIN: &str = r#"
import { format } from "./dep.ts";
function flag(enabled: boolean, value: string | undefined): string {
  if (!!value && Boolean(enabled)) return "both";
  if (enabled === true) return "enabled";
  if (enabled === false && !!value) return "value";
  return "none";
}
function toNumber(n: number): number {
  return Number(n) + +n;
}
function asBoolean(input: string): boolean {
  return Boolean(input);
}
function wrap(s: string): string {
  return `${s}` + ("" + s) + (s + "");
}
let seconds = 60 * 60 * 1000;
let negativeZero = 0 * -1;
let bits = (1 << 31) | 0;
let unsigned = -1 >>> 28;
let text = "con" + "cat";
console.log(format(3, "items"), format(4, "boxes"));
console.log(flag(true, "v"), flag(false, undefined), flag(false, "v"), flag(true, undefined));
console.log(toNumber(5), asBoolean(""), asBoolean("x"), wrap("w"), wrap("v"));
console.log(seconds, Object.is(negativeZero, -0), 1 / negativeZero, bits, unsigned, text);
"#;

const POS_FILES: &[(&str, &str)] = &[("main.ts", POS_MAIN), ("dep.ts", POS_DEP)];

#[test]
fn primitive_operations_simplify_proven_conversions() {
    let (temp, report) = run(POS_FILES, &[POS]);
    assert!(applied(&report, POS) >= 10, "{report:#}");
    assert!(saved(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    let dep = emitted(temp.path(), "dep.ts");
    // Test positions only observe ToBoolean.
    assert!(main.contains("if (value && enabled)"), "{main}");
    // Proven booleans compared with literals.
    assert!(main.contains("if (enabled)"), "{main}");
    assert!(main.contains("!enabled && value"), "{main}");
    // Proven numbers and strings lose redundant conversions.
    assert!(main.contains("return n + n;"), "{main}");
    assert!(main.contains("return !!input;"), "{main}");
    assert!(main.contains("return s + s + s;"), "{main}");
    // Exact folding, including signed zero and 32-bit wrap-around.
    assert!(main.contains("seconds = 3600000"), "{main}");
    assert!(main.contains("negativeZero = -0"), "{main}");
    assert!(main.contains("bits = -2147483648"), "{main}");
    assert!(main.contains("unsigned = 15"), "{main}");
    assert!(main.contains("text = \"concat\""), "{main}");
    // Cross-module: a proven number converts with `"" +`, a proven string
    // needs no conversion.
    assert!(dep.contains("\"\" + count + \" \" + unit"), "{dep}");
}

#[test]
fn primitive_operations_with_all_passes_enabled() {
    let (_temp, report) = run_all(POS_FILES);
    assert!(applied(&report, POS) > 0);
    assert!(saved(&report) > 0);
}

#[test]
fn primitive_operations_preserve_coercion_bigint_and_nan() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"
const trace: string[] = [];
const tricky = {
  valueOf() {
    trace.push("valueOf");
    return 7;
  },
  toString() {
    trace.push("toString");
    return "seven";
  },
};
function show(value: typeof tricky): string {
  return String(value) + ("" + value) + Number(value) + `${value}`;
}
console.log(show(tricky), trace.join(","));
const symbol = Symbol("tag");
function describeSymbol(value: symbol): string {
  return String(value);
}
console.log(describeSymbol(symbol));
const big = 10n;
function bigText(value: bigint): string {
  return String(value) + typeof Number(value);
}
console.log(bigText(big));
try {
  console.log((big as unknown as number) + 1);
} catch (error) {
  console.log((error as Error).name);
}
let notANumber = 0 / 0;
let infinite = 1 / 0;
let fraction = 1 + 0.5;
let mixed = "n" + 0.5;
console.log(notANumber, infinite, fraction, mixed);
export function publicText(value: number): string {
  return String(value);
}
console.log(publicText(1));
"#,
    )];
    let (temp, report) = run(files, &[POS]);
    let main = emitted(temp.path(), "main.ts");
    // Objects: conversions call user valueOf/toString with different hints.
    assert!(
        main.contains("String(value) + (\"\" + value) + Number(value)"),
        "{main}"
    );
    // Symbols: String() describes, `"" +` would throw.
    assert!(
        main.contains("return String(value);\n}\nconsole.log(describeSymbol"),
        "{main}"
    );
    // BigInt: Number() converts, unary plus would throw.
    assert!(main.contains("typeof Number(value)"), "{main}");
    // NaN and infinities have no literal; non-integer number text is not
    // folded into strings.
    assert!(main.contains("0 / 0") && main.contains("1 / 0"), "{main}");
    assert!(main.contains("\"n\" + 0.5"), "{main}");
    assert!(main.contains("fraction = 1.5"), "{main}");
    // Public entry export parameter is unknown.
    assert!(
        main.contains("export function publicText(value) {\n    return String(value);"),
        "{main}"
    );
    let _ = report;
}

#[test]
fn primitive_operations_respect_shadowed_and_replaced_intrinsics() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"
import "./patch.ts";
import { show } from "./shadow.ts";
function twice(n: number): number {
  return Number(n) * 2;
}
function yes(b: boolean): boolean {
  return Boolean(b);
}
console.log(twice(4), show(3), yes(true));
"#,
        ),
        (
            "patch.ts",
            r#"
const original = globalThis.Number;
(globalThis as any).Number = (value: unknown) => {
  console.log("patched Number");
  return original(value);
};
"#,
        ),
        (
            "shadow.ts",
            r#"
function String(value: unknown): string {
  return "shadow:" + value;
}
export function show(n: number): string {
  return String(n);
}
"#,
        ),
    ];
    let (temp, report) = run(files, &[POS]);
    let main = emitted(temp.path(), "main.ts");
    let shadow = emitted(temp.path(), "shadow.ts");
    assert!(main.contains("Number(n) * 2"), "{main}");
    assert!(skipped_reasons(&report, POS).contains(&"intrinsic_may_be_replaced".to_owned()));
    // Boolean is not replaced.
    assert!(main.contains("return b;"), "{main}");
    // A local `String` is not the built-in.
    assert!(shadow.contains("return String(n);"), "{shadow}");
}

#[test]
fn primitive_operations_disabled_never_run() {
    let temp = project(POS_FILES);
    let report = optimize(temp.path(), &["--disable-pass", POS]);
    assert_eq!(applied(&report, POS), 0);
    assert!(!mentions_pass(&report, POS));
    let main = emitted(temp.path(), "main.ts");
    // call-inlining may substitute the argument; the conversion stays.
    assert!(main.contains("Boolean("), "{main}");
    assert!(main.contains("60 * 60 * 1000"), "{main}");
    same_behavior(temp.path());
}

// ---------------------------------------------------------------------------
// intrinsic-specialization
// ---------------------------------------------------------------------------

const IS_DEP: &str = r#"
export function point(x: number, y: number): number[] {
  return [...[x, y], 0];
}
export function greet(s: string): string {
  return s.concat("!", "?");
}
"#;

const IS_MAIN: &str = r#"
import { point, greet } from "./dep.ts";
function sum(...values: number[]): number {
  return values.reduce((a, b) => a + b, 0);
}
function center(x: number, w: number): number {
  return x + w / 2;
}
const [cx, cy] = [center(1, 4), center(2, 6)];
console.log(cx, cy);
console.log(point(1, 2), sum(...[1, 2, 3]));
const merged = { ...{ a: 1, b: "two" }, c: true };
console.log(merged);
console.log(Array.from(["x", "y"]), [1].concat([2, 3], 4), [5, 6].slice());
function isAxis(k: string): boolean {
  return ["x"].includes(k);
}
console.log(isAxis("x"), isAxis("z"), greet("hi"));
const options: { a: number; b: number } = Object.assign({}, { a: 1 }, { b: 2 });
console.log(options);
function boom(): number {
  throw new Error("boom");
}
try {
  const [ok, bad] = [1, boom()];
  console.log(ok, bad);
} catch (error) {
  console.log((error as Error).message);
}
"#;

const IS_FILES: &[(&str, &str)] = &[("main.ts", IS_MAIN), ("dep.ts", IS_DEP)];

#[test]
fn intrinsics_specialize_fresh_literals() {
    let (temp, report) = run(IS_FILES, &[IS]);
    assert!(applied(&report, IS) >= 9, "{report:#}");
    assert!(saved(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    let dep = emitted(temp.path(), "dep.ts");
    assert!(
        main.contains("const cx = center(1, 4), cy = center(2, 6);"),
        "{main}"
    );
    assert!(main.contains("sum(1, 2, 3)"), "{main}");
    assert!(main.contains("{ a: 1, b: \"two\", c: true }"), "{main}");
    assert!(
        main.contains("[\"x\", \"y\"]") && !main.contains("Array.from"),
        "{main}"
    );
    assert!(
        main.contains("[1, 2, 3, 4]") && main.contains("[5, 6]"),
        "{main}"
    );
    assert!(main.contains("return k === \"x\";"), "{main}");
    assert!(
        main.contains("{ a: 1, b: 2 }") && !main.contains("Object.assign"),
        "{main}"
    );
    assert!(main.contains("const ok = 1, bad = boom();"), "{main}");
    // Cross-module: spread flattening and string concatenation.
    assert!(dep.contains("return [x, y, 0];"), "{dep}");
    assert!(dep.contains("return s + \"!\" + \"?\";"), "{dep}");
}

#[test]
fn intrinsics_with_all_passes_enabled() {
    let (_temp, report) = run_all(IS_FILES);
    assert!(applied(&report, IS) > 0);
    assert!(saved(&report) > 0);
}

#[test]
fn intrinsics_preserve_holes_iterators_accessors_and_coercion() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"
const trace: string[] = [];
console.log([...[1, , 3]], Array.from([1, , 2]), [0, , 1].slice());
class Bag {
  *[Symbol.iterator]() {
    trace.push("bag");
    yield 1;
  }
}
const bag = new Bag();
console.log([...bag], Array.from(bag), trace.join(","));
const withGetter = {
  ...{
    get g() {
      trace.push("get");
      return 1;
    },
  },
};
console.log(withGetter, trace.join(","));
const tricky = {
  toString() {
    trace.push("toString");
    return "t";
  },
  valueOf() {
    trace.push("valueOf");
    return 1;
  },
};
function glue(s: string): string {
  return s.concat(tricky as unknown as string);
}
console.log(glue("a"), trace.join(","));
function isNaNValue(x: number): boolean {
  return [NaN, 1].includes(x);
}
console.log(isNaNValue(NaN), [0].includes(-0));
function scoped(): string {
  try {
    const [first, second] = [1, peek()];
    function peek(): number {
      return first;
    }
    return String(first + second);
  } catch (error) {
    return (error as Error).name;
  }
}
console.log(scoped());
export function shout(s: string): string {
  return s.concat("!");
}
console.log(shout("x"));
"#,
    )];
    let (temp, report) = run(files, &[IS]);
    let main = emitted(temp.path(), "main.ts");
    let reasons = skipped_reasons(&report, IS);
    // Holes become undefined when spread or copied by Array.from.
    assert!(
        main.contains("...[1, , 3]") && main.contains("Array.from([1, , 2])"),
        "{main}"
    );
    assert!(reasons.contains(&"sparse_array".to_owned()), "{reasons:?}");
    // User iterators run.
    assert!(main.contains("[...bag], Array.from(bag)"), "{main}");
    assert!(
        reasons.contains(&"custom_iterator".to_owned()),
        "{reasons:?}"
    );
    // Spreading copies getter values.
    assert!(main.contains("...{"), "{main}");
    assert!(reasons.contains(&"accessor".to_owned()), "{reasons:?}");
    // Objects convert with a different hint.
    assert!(main.contains("s.concat(tricky)"), "{main}");
    // SameValueZero: NaN is never a literal element.
    assert!(main.contains("[NaN, 1].includes(x)"), "{main}");
    // A binding read before the destructuring completes.
    assert!(
        main.contains("const [first, second] = [1, peek()]"),
        "{main}"
    );
    // Public entry export parameter.
    assert!(main.contains("return s.concat(\"!\");"), "{main}");
}

#[test]
fn intrinsics_respect_replaced_prototype_members() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"
import { trace } from "./patch.ts";
function has(k: string): boolean {
  return ["a"].includes(k);
}
const copy = [...[1, 2]];
const [p, q] = [3, 4];
console.log(has("b"), copy, p, q, trace.join(","));
"#,
        ),
        (
            "patch.ts",
            r#"
export const trace: string[] = [];
const iterate = Array.prototype[Symbol.iterator];
(Array.prototype as any)[Symbol.iterator] = function (this: unknown[]) {
  trace.push("iterate");
  return iterate.call(this);
};
Object.defineProperty(Array.prototype, "includes", {
  value: function () {
    trace.push("includes");
    return true;
  },
});
"#,
        ),
    ];
    let (temp, report) = run(files, &[IS]);
    let main = emitted(temp.path(), "main.ts");
    assert_eq!(applied(&report, IS), 0, "{report:#}");
    assert!(main.contains("[\"a\"].includes(k)"), "{main}");
    assert!(main.contains("[...[1, 2]]"), "{main}");
    assert!(main.contains("const [p, q] = [3, 4]"), "{main}");
    assert!(skipped_reasons(&report, IS).contains(&"intrinsic_may_be_replaced".to_owned()));
}

/// An ESM package whose runtime file is `index.js` and whose declaration is
/// `index.d.ts`, laid out under `node_modules/<name>`.
fn package(name: &str, runtime: &str, declaration: &str) -> Vec<(String, String)> {
    vec![
        (
            format!("node_modules/{name}/package.json"),
            format!(
                r#"{{"name":"{name}","type":"module","exports":"./index.js","types":"./index.d.ts"}}"#
            ),
        ),
        (
            format!("node_modules/{name}/index.d.ts"),
            declaration.to_owned(),
        ),
        (format!("node_modules/{name}/index.js"), runtime.to_owned()),
    ]
}

/// Runs the project with only intrinsic specialization and again with every
/// pass, asserting that both keep behavior and the `.slice()` call, and that
/// intrinsic specialization reports the conservative skip.
fn assert_slice_preserved(files: &[(&str, &str)], expected: &str) {
    for keep in [&[IS][..], &[][..]] {
        let temp = project(files);
        if files.iter().any(|(name, _)| {
            !name.starts_with("node_modules/")
                && (name.ends_with(".js") || name.ends_with(".mjs") || name.ends_with(".cjs"))
        }) {
            std::fs::write(
                temp.path().join("tsconfig.json"),
                r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"allowJs":true,"rewriteRelativeImportExtensions":true}}"#,
            )
            .unwrap();
        }
        let report = if keep.is_empty() {
            optimize(temp.path(), &[])
        } else {
            optimize(temp.path(), &args(&only(keep)))
        };
        same_behavior(temp.path());
        let optimized = run_optimized(temp.path());
        assert_eq!(
            String::from_utf8_lossy(&optimized.stdout),
            expected,
            "{report:#}"
        );
        let main = emitted(temp.path(), "main.ts");
        assert!(main.contains(".slice()"), "{main}");
        assert_eq!(applied(&report, IS), 0, "{report:#}");
        assert!(
            skipped_reasons(&report, IS).contains(&"intrinsic_may_be_replaced".to_owned()),
            "{report:#}"
        );
    }
}

#[test]
fn bare_core_looking_packages_do_not_prove_host_identity() {
    for name in ["process", "buffer", "http"] {
        let packages = package(
            name,
            "Array.prototype.slice = function () { return [9]; };\n",
            "export {};\n",
        );
        let source = format!("import \"{name}\";\nconsole.log(JSON.stringify([1].slice()));\n");
        let app = [("main.ts", source.as_str())];
        let files = with_packages(&app, &packages);
        // Node itself resolves these names to the host. The assertion here
        // checks the optimizer's guard for browser or alternate resolvers.
        let temp = project(&files);
        let report = optimize(temp.path(), &args(&only(&[IS])));
        assert_eq!(applied(&report, IS), 0, "{name}: {report:#}");
        assert!(emitted(temp.path(), "main.ts").contains(".slice()"));
        assert!(skipped_reasons(&report, IS).contains(&"intrinsic_may_be_replaced".to_owned()));
    }
}

#[test]
fn bare_core_looking_runtime_forms_keep_intrinsic_guards() {
    let packages = package(
        "process",
        "Array.prototype.slice = function () { return [9]; };\nexport const ready = true;\n",
        "export declare const ready: boolean;\n",
    );
    let cases: [&[(&str, &str)]; 3] = [
        &[(
            "main.ts",
            "export {};\nawait import(\"process\");\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &[
            (
                "main.ts",
                "import \"./bridge.ts\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            ("bridge.ts", "export * from \"process\";\n"),
        ],
        &[
            (
                "main.ts",
                "import \"./bridge.cjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            ("bridge.cjs", "require(\"process\");\n"),
        ],
    ];
    for app in cases {
        let files = with_packages(app, &packages);
        // Node loads its host module; an alternate resolver can load the
        // package, so the intrinsic guard is still required.
        assert_slice_preserved(&files, "[1]\n");
    }
}

#[test]
fn ambient_bare_node_core_imports_preserve_intrinsic_proofs() {
    for name in [
        "http",
        "child_process",
        "fs/promises",
        "assert/strict",
        "stream/promises",
    ] {
        for specifier in [name.to_owned(), format!("node:{name}")] {
            let source =
                format!("import \"{specifier}\";\nconsole.log(JSON.stringify([5, 6].slice()));\n");
            let declaration = format!("declare module \"{specifier}\";\n");
            let files = [
                ("main.ts", source.as_str()),
                ("host.d.ts", declaration.as_str()),
            ];
            let (temp, report) = run(&files, &[IS]);
            assert!(applied(&report, IS) >= 1, "{specifier}: {report:#}");
            assert!(saved(&report) > 0, "{specifier}: {report:#}");
            assert!(!emitted(temp.path(), "main.ts").contains(".slice()"));
            assert_eq!(
                String::from_utf8_lossy(&run_optimized(temp.path()).stdout),
                "[5,6]\n"
            );
            if matches!(name, "http" | "child_process" | "fs/promises") {
                let (temp, report) = run_all(&files);
                assert!(applied(&report, IS) >= 1, "{specifier}: {report:#}");
                assert!(!emitted(temp.path(), "main.ts").contains(".slice()"));

                let temp = project(&files);
                let report = optimize(temp.path(), &["--disable-pass", IS]);
                same_behavior(temp.path());
                assert_eq!(applied(&report, IS), 0, "{specifier}: {report:#}");
                assert!(emitted(temp.path(), "main.ts").contains(".slice()"));
            }
        }
    }
}

#[test]
fn bare_node_core_runtime_forms_preserve_intrinsic_proofs() {
    let cases: &[(&str, &str)] = &[
        (
            "main.ts",
            "export {};\nawait import(\"http\");\nconsole.log(JSON.stringify([5, 6].slice()));\n",
        ),
        ("bridge.ts", "export * from \"http\";\n"),
        ("bridge.cjs", "require(\"http\");\n"),
    ];
    for (file, source) in cases {
        let entry = if *file == "main.ts" {
            *source
        } else {
            "import \"./bridge.ts\";\nconsole.log(JSON.stringify([5, 6].slice()));\n"
        };
        let entry = if *file == "bridge.cjs" {
            "import \"./bridge.cjs\";\nconsole.log(JSON.stringify([5, 6].slice()));\n"
        } else {
            entry
        };
        let mut files = vec![
            ("main.ts", entry),
            ("host.d.ts", "declare module \"http\";\n"),
        ];
        if *file != "main.ts" {
            files.push((*file, *source));
        }
        let temp = project(&files);
        if *file == "bridge.cjs" {
            std::fs::write(
                temp.path().join("tsconfig.json"),
                r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"allowJs":true,"rewriteRelativeImportExtensions":true}}"#,
            )
            .unwrap();
        }
        let report = optimize(temp.path(), &args(&only(&[IS])));
        same_behavior(temp.path());
        assert!(applied(&report, IS) >= 1, "{file}: {report:#}");
        assert!(!emitted(temp.path(), "main.ts").contains(".slice()"));
    }
}

#[test]
fn prefix_only_polyfill_computed_and_application_patches_keep_intrinsic_guards() {
    for name in ["test", "http-polyfill"] {
        let packages = package(
            name,
            "Array.prototype.slice = function () { return [9]; };\n",
            "export {};\n",
        );
        let source = format!("import \"{name}\";\nconsole.log(JSON.stringify([5, 6].slice()));\n");
        let app = [("main.ts", source.as_str())];
        let files = with_packages(&app, &packages);
        assert_slice_preserved(&files, "[9]\n");
    }

    let packages = package(
        "http",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let app = [
        (
            "main.ts",
            "import \"./bridge.cjs\";\nconsole.log(JSON.stringify([5, 6].slice()));\n",
        ),
        ("bridge.cjs", "require(\"http\");\n"),
        ("host.d.ts", "declare module \"http\";\n"),
    ];
    assert_slice_preserved(&with_packages(&app, &packages), "[5,6]\n");
    let app = [
        (
            "main.ts",
            "import \"http\";\nconsole.log(JSON.stringify([5, 6].slice()));\n",
        ),
        ("host.d.ts", "declare module \"http\";\n"),
    ];
    assert_slice_preserved(&with_packages(&app, &packages), "[5,6]\n");

    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let app = [(
        "main.ts",
        "export {};\nconst specifier = \"patch-slice\";\nawait \
         import(specifier);\nconsole.log(JSON.stringify([5, 6].slice()));\n",
    )];
    assert_slice_preserved(&with_packages(&app, &packages), "[9]\n");

    let files = [(
        "main.ts",
        "(Array.prototype as any).slice = function () { return [9]; \
         };\nconsole.log(JSON.stringify([5, 6].slice()));\n",
    )];
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn ambient_json_suffix_does_not_prove_data_identity() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            "import \"missing.json\";\nconsole.log(JSON.stringify([1].slice()));\n",
        ),
        ("ambient.d.ts", "declare module \"missing.json\";\n"),
    ];
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(&[IS])));
    assert_eq!(applied(&report, IS), 0, "{report:#}");
    assert!(emitted(temp.path(), "main.ts").contains(".slice()"));
    assert!(skipped_reasons(&report, IS).contains(&"intrinsic_may_be_replaced".to_owned()));
}

#[test]
fn executable_package_with_json_suffix_guards_intrinsics() {
    let packages = package(
        "patch.json",
        "Array.prototype.slice = function () { return [9]; };\nexport const ready = true;\n",
        "export declare const ready: boolean;\n",
    );
    let cases: [&[(&str, &str)]; 4] = [
        &[(
            "main.ts",
            "import \"patch.json\";\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &[(
            "main.ts",
            "export {};\nawait \
             import(\"patch.json\");\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &[
            (
                "main.ts",
                "import \"./bridge.ts\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            ("bridge.ts", "export { ready } from \"patch.json\";\n"),
        ],
        &[
            (
                "main.ts",
                "import \"./bridge.cjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            ("bridge.cjs", "module.require(\"patch.json\");\n"),
        ],
    ];
    for (case, app) in cases.into_iter().enumerate() {
        let files = with_packages(app, &packages);
        assert_slice_preserved(&files, "[9]\n");
        if case == 0 {
            let temp = project(&files);
            let options = only(&[IS]);
            let checked = check(temp.path(), &args(&options));
            let published = optimize(temp.path(), &args(&options));
            assert_eq!(
                without_measurements(checked),
                without_measurements(published)
            );
        }
    }
}

#[test]
fn resolved_json_data_keeps_intrinsics_specializable() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            "import data from \"./data.json\" with { type: \"json\" \
             };\nconsole.log(JSON.stringify([data.value].slice()));\n",
        ),
        ("data.json", "{\"value\":5}\n"),
    ];
    let (temp, report) = run(files, &[IS]);
    assert!(applied(&report, IS) >= 1, "{report:#}");
    assert!(!emitted(temp.path(), "main.ts").contains(".slice()"));
}

#[test]
fn module_require_and_created_loader_guard_intrinsics() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    for bridge in [
        ("bridge.cjs", "require(\"patch-slice\");\n"),
        ("bridge.cjs", "module.require(\"patch-slice\");\n"),
        ("bridge.cjs", "module[\"require\"](\"patch-slice\");\n"),
        (
            "bridge.cjs",
            "const target = \"patch-slice\";\nmodule.require(target);\n",
        ),
        (
            "bridge.cjs",
            "const { createRequire: makeRequire } = require(\"node:module\");\nconst load = \
             makeRequire(__filename);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.cjs",
            "const mod = require(\"node:module\");\nconst load = \
             mod.createRequire(__filename);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.cjs",
            "const load = \
             require(\"node:module\").createRequire(__filename);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.mjs",
            "import { createRequire as makeRequire } from \"node:module\";\nconst load = \
             makeRequire(import.meta.url);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.ts",
            "// @ts-ignore\nimport { createRequire as makeRequire } from \"node:module\";\nconst \
             load = makeRequire(import.meta.url);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.mjs",
            "import * as mod from \"node:module\";\nconst load = \
             mod.createRequire(import.meta.url);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.mjs",
            "import * as mod from \"node:module\";\nconst load = \
             mod[\"createRequire\"](import.meta.url);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.mjs",
            "import mod from \"node:module\";\nconst load = \
             mod.createRequire(import.meta.url);\nload(\"patch-slice\");\n",
        ),
        (
            "bridge.mjs",
            "const { createRequire: makeRequire } = await import(\"node:module\");\nconst load = \
             makeRequire(import.meta.url);\nload(\"patch-slice\");\n",
        ),
    ] {
        let entry = format!(
            "import \"./{}\";\nconsole.log(JSON.stringify([1].slice()));\n",
            bridge.0
        );
        let app = [("main.ts", entry.as_str()), (bridge.0, bridge.1)];
        let files = with_packages(&app, &packages);
        assert_slice_preserved(&files, "[9]\n");
    }
}

#[test]
fn exported_created_loader_guard_propagates_across_application_modules() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let files = with_packages(
        &[
            (
                "main.ts",
                "import \"./consumer.mjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            (
                "consumer.mjs",
                "import { load } from \"./loader.mjs\";\nload(\"patch-slice\");\n",
            ),
            (
                "loader.mjs",
                "import { createRequire } from \"node:module\";\nexport const load = \
                 createRequire(import.meta.url);\n",
            ),
        ],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn exported_created_loader_namespace_guard_propagates_across_application_modules() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let files = with_packages(
        &[
            (
                "main.ts",
                "import \"./consumer.mjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            (
                "consumer.mjs",
                "import * as loaders from \"./loader.mjs\";\nloaders.load(\"patch-slice\");\n",
            ),
            (
                "loader.mjs",
                "import { createRequire } from \"node:module\";\nexport const load = \
                 createRequire(import.meta.url);\n",
            ),
        ],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn default_exported_created_loader_guard_propagates_across_application_modules() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let files = with_packages(
        &[
            (
                "main.ts",
                "import \"./consumer.mjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            (
                "consumer.mjs",
                "import load from \"./loader.mjs\";\nload(\"patch-slice\");\n",
            ),
            (
                "loader.mjs",
                "import { createRequire } from \"node:module\";\nexport default \
                 createRequire(import.meta.url);\n",
            ),
        ],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn exported_created_loader_bracket_namespace_guard_propagates_across_application_modules() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let files = with_packages(
        &[
            (
                "main.ts",
                "import \"./consumer.mjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            (
                "consumer.mjs",
                "import * as loaders from \"./loader.mjs\";\nloaders[\"load\"](\"patch-slice\");\n",
            ),
            (
                "loader.mjs",
                "import { createRequire } from \"node:module\";\nexport const load = \
                 createRequire(import.meta.url);\n",
            ),
        ],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn created_loader_relative_specifier_uses_creation_module_base() {
    let files = [
        (
            "main.ts",
            "import \"./consumer.mjs\";\nconsole.log(JSON.stringify([1].slice()));\n",
        ),
        (
            "consumer.mjs",
            "import { load } from \"./subdir/loader.mjs\";\nload(\"./patcher.cjs\");\n",
        ),
        (
            "subdir/loader.mjs",
            "import { createRequire } from \"node:module\";\nexport const load = \
             createRequire(import.meta.url);\n",
        ),
        (
            "subdir/patcher.cjs",
            "Array.prototype.slice = function () { return [9]; };\n",
        ),
        ("patcher.cjs", "module.exports = {};\n"),
    ];
    let temp = project(&files);
    let report = optimize(temp.path(), &args(&only(&[IS])));
    assert_eq!(applied(&report, IS), 0, "{report:#}");
    assert!(emitted(temp.path(), "main.ts").contains(".slice()"));
}

fn with_packages<'a>(
    files: &'a [(&'a str, &'a str)],
    packages: &'a [(String, String)],
) -> Vec<(&'a str, &'a str)> {
    files
        .iter()
        .copied()
        .chain(packages.iter().map(|(p, c)| (p.as_str(), c.as_str())))
        .collect()
}

#[test]
fn intrinsics_respect_patches_from_external_packages() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    );
    let files = with_packages(
        &[(
            "main.ts",
            "import \"patch-slice\";\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn intrinsics_keep_patched_method_effects_from_external_packages() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { console.log(\"slice\"); return [9]; };\n",
        "export {};\n",
    );
    let files = with_packages(
        &[(
            "main.ts",
            "import \"patch-slice\";\nconsole.log(JSON.stringify([1].slice()));\nconsole.log(JSON.\
             stringify([2].slice()));\n",
        )],
        &packages,
    );
    assert_slice_preserved(&files, "slice\n[9]\nslice\n[9]\n");
}

#[test]
fn intrinsics_treat_declaration_only_packages_as_unknown_runtime() {
    let packages = package(
        "patch-lib",
        "export function setup() { Array.prototype.slice = function () { return [9]; }; }\n",
        "export declare function setup(): void;\n",
    );
    let files = with_packages(
        &[(
            "main.ts",
            "import { setup } from \
             \"patch-lib\";\nsetup();\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn intrinsics_respect_transitive_external_imports() {
    let mut packages = package("outer", "import \"inner\";\n", "export {};\n");
    packages.extend(package(
        "inner",
        "Array.prototype.slice = function () { return [9]; };\n",
        "export {};\n",
    ));
    let files = with_packages(
        &[(
            "main.ts",
            "import \"outer\";\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &packages,
    );
    assert_slice_preserved(&files, "[9]\n");
}

#[test]
fn intrinsics_respect_external_dynamic_imports_and_reexports() {
    let packages = package(
        "patch-slice",
        "Array.prototype.slice = function () { return [9]; };\nexport const ready = true;\n",
        "export declare const ready: boolean;\n",
    );
    let dynamic = with_packages(
        &[(
            "main.ts",
            "export {};\nawait \
             import(\"patch-slice\");\nconsole.log(JSON.stringify([1].slice()));\n",
        )],
        &packages,
    );
    assert_slice_preserved(&dynamic, "[9]\n");
    let reexport = with_packages(
        &[
            (
                "main.ts",
                "import \"./bridge.ts\";\nconsole.log(JSON.stringify([1].slice()));\n",
            ),
            ("bridge.ts", "export { ready } from \"patch-slice\";\n"),
        ],
        &packages,
    );
    assert_slice_preserved(&reexport, "[9]\n");
}

#[test]
fn type_only_package_imports_keep_intrinsics_specializable() {
    let packages = package(
        "types-only",
        "throw new Error(\"never loaded at runtime\");\n",
        "export type Value = number;\n",
    );
    let files = with_packages(
        &[(
            "main.ts",
            "import type { Value } from \"types-only\";\nexport type { Value } from \
             \"types-only\";\nconst v: Value = 5;\nconsole.log(JSON.stringify([v, 6].slice()));\n",
        )],
        &packages,
    );
    let (temp, report) = run(&files, &[IS]);
    let main = emitted(temp.path(), "main.ts");
    assert!(applied(&report, IS) >= 1, "{report:#}");
    assert!(!main.contains(".slice()"), "{main}");
}

#[test]
fn node_builtin_imports_keep_intrinsics_specializable() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            "import \"node:os\";\nconsole.log(JSON.stringify([5, 6].slice()));\n",
        ),
        ("host.d.ts", "declare module \"node:os\";\n"),
    ];
    let (temp, report) = run(files, &[IS]);
    let main = emitted(temp.path(), "main.ts");
    assert!(applied(&report, IS) >= 1, "{report:#}");
    assert!(!main.contains(".slice()"), "{main}");
}

#[test]
fn intrinsics_disabled_never_run() {
    let temp = project(IS_FILES);
    let report = optimize(temp.path(), &["--disable-pass", IS]);
    assert_eq!(applied(&report, IS), 0);
    assert!(!mentions_pass(&report, IS));
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("Array.from"));
    assert!(main.contains("const [cx, cy]"));
    same_behavior(temp.path());
}

// ---------------------------------------------------------------------------
// Shared syntax and scope hazards
// ---------------------------------------------------------------------------

#[test]
fn edits_respect_asi_token_boundaries_and_hoisting() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"
const FLAG = true
const OFF = false
let base = Math.random() > 2 ? 1 : 3
function paren(): string {
  let out = "a"
  if (FLAG) (out = "b")
  return out
}
function glued(s: string): string {
  return("" + s)
}
function negative(): number {
  return base-2*-1
}
function hoisting(): number {
  return helper()
  function helper(): number {
    return 7
  }
}
function labeled(mode: string, stop: boolean): string {
  let result = "start"
  switch (mode) {
    case "a": {
      if (stop) break
      result = "ran"
      break
    }
    default:
      result = "default"
  }
  return result
}
function call(callback?: () => number): number {
  return callback?.() ?? 0
}
function first(values?: number[]): number {
  return values?.[0] ?? -1
}
function loop(items: Array<{ x: number } | undefined>): number {
  let sum = 0
  for (const item of items) {
    if (!item) continue
    sum += item?.x
  }
  return sum
}
function pickWidth(): number {
  return OFF ? Math.max(1, 2) : Math.min(1, 2)
}
const nothing = null
console.log(paren(), glued("g"), glued("h"), negative(), hoisting())
console.log(labeled("a", true), labeled("a", false))
console.log(call(() => 3), call(() => 4), first([5]), first([6, 7]))
console.log(loop([{ x: 1 }, undefined, { x: 2 }]), pickWidth())
console.log(typeof nothing === "object", nothing === null)
"#,
    )];
    let (temp, report) = run(files, &[TDCF, POS, IS]);
    assert!(
        applied(&report, TDCF) > 0 && applied(&report, POS) > 0,
        "{report:#}"
    );
    let main = emitted(temp.path(), "main.ts");
    // The kept parenthesized statement does not continue the previous one.
    assert!(main.contains("(out = \"b\");"), "{main}");
    // Removed parentheses do not merge `return` with the operand.
    assert!(main.contains("return s;"), "{main}");
    // A folded negative literal does not form `--`.
    assert!(main.contains("base - -2"), "{main}");
    // Hoisted function declarations after `return` stay.
    assert!(main.contains("function helper()"), "{main}");
    // A `break` inside the selected clause leaves the switch.
    assert!(main.contains("switch (mode)"), "{main}");
    // Optional calls and element accesses on proven values.
    assert!(
        main.contains("callback()") && main.contains("values[0]"),
        "{main}"
    );
    // Narrowing by a `continue` guard inside a loop body.
    assert!(main.contains("item.x"), "{main}");
    // A constant conditional keeps the branch with the same type.
    assert!(!main.contains("Math.max"), "{main}");
}

#[test]
fn optional_chains_keep_narrowing_that_later_code_needs() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        r#"
type Options = { map?: Map<string, number>; flag?: boolean; name?: string };
function guard(opts?: Options): number {
  if (opts?.map && opts.map.size > 0) return opts.map.size;
  if (opts?.flag) {
    return opts?.map?.size ?? 1;
  }
  const label = opts?.name ? opts.name.length : 0;
  return label + (opts?.name?.length ?? 0);
}
console.log(guard({ map: new Map([["a", 1]]) }), guard({ flag: true }), guard({ name: "n" }));
"#,
    )];
    let (temp, report) = run(files, &[TDCF]);
    assert!(applied(&report, TDCF) > 0, "{report:#}");
    let reasons = skipped_reasons(&report, TDCF);
    assert!(
        !reasons.contains(&"recheck_failed".to_owned()),
        "{reasons:?}"
    );
    let main = emitted(temp.path(), "main.ts");
    // Guards whose narrowing is used later keep `?.`.
    assert!(main.contains("opts?.map && opts.map.size"), "{main}");
    assert!(main.contains("opts?.name ? opts.name.length"), "{main}");
}

#[test]
fn simplify_family_is_deterministic() {
    let temp = project(TDCF_FILES);
    let first = without_measurements(check(temp.path(), &[]));
    let second = without_measurements(check(temp.path(), &[]));
    assert_eq!(first, second);
}
