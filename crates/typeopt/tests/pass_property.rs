// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! property-shortening, property-disambiguation, property-ambiguation and
//! finite-key-rewriting.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use std::{fs, path::Path};

use common::*;
use serde_json::Value;

const SHORTENING: &str = "property-shortening";
const DISAMBIGUATION: &str = "property-disambiguation";
const AMBIGUATION: &str = "property-ambiguation";
const FINITE_KEYS: &str = "finite-key-rewriting";
const RESIDUAL: &str = "residual-dependency-removal";

/// Optimizes a fresh copy of `files` with only `keep` enabled (all passes
/// when `None`), checks Node behavior and returns the report.
fn run(files: &[(&str, &str)], keep: Option<&[&str]>) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = match keep {
        Some(keep) => optimize(temp.path(), &args(&only(keep))),
        None => optimize(temp.path(), &[]),
    };
    same_behavior(temp.path());
    (temp, report)
}

fn renamed(report: &Value) -> u64 {
    report["renamed_properties"].as_u64().unwrap()
}

fn mappings(root: &Path) -> Vec<Value> {
    let value: Value =
        serde_json::from_slice(&fs::read(root.join("out/properties.json")).unwrap()).unwrap();
    assert_eq!(value["schema_version"], 0);
    value["properties"].as_array().unwrap().clone()
}

fn mapping<'a>(all: &'a [Value], original: &str) -> Vec<&'a Value> {
    all.iter().filter(|m| m["original"] == original).collect()
}

fn transformations<'a>(report: &'a Value, pass: &str) -> Vec<&'a Value> {
    report["transformations"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|t| t["pass"] == pass)
        .collect()
}

fn uses(transformation: &Value) -> Vec<String> {
    transformation["uses"]
        .as_array()
        .unwrap()
        .iter()
        .map(|u| u.as_str().unwrap().to_owned())
        .collect()
}

// ---------------------------------------------------------------------------
// property-shortening
// ---------------------------------------------------------------------------

const MODULES: &[(&str, &str)] = &[
    (
        "main.ts",
        "import { makeSettings, describeSettings, SETTINGS } from './reexport.ts';\nimport { \
         counter } from './cycle-a.ts';\nconst settings = \
         makeSettings(3);\nconsole.log(describeSettings(settings), settings.verboseLevel, \
         SETTINGS.defaultLongName, counter(), counter());\nconst nested = { innerObject: { \
         deepPropertyName: 5 }, outerPropertyName: 1 \
         };\nconsole.log(nested.innerObject.deepPropertyName + nested.outerPropertyName);\n",
    ),
    (
        "reexport.ts",
        "export { makeSettings, describeSettings } from './dep.ts';\nexport * from \
         './consts.ts';\n",
    ),
    (
        "dep.ts",
        "export function makeSettings(level: number) {\n  return { verboseLevel: level, \
         outputFormat: 'json' };\n}\nexport const describeSettings = ({ verboseLevel, \
         outputFormat }: { verboseLevel: number; outputFormat: string }) =>\n  outputFormat + ':' \
         + verboseLevel;\n",
    ),
    (
        "consts.ts",
        "export const SETTINGS = { defaultLongName: 'x', otherLongName: 2 } as const;\n",
    ),
    (
        "cycle-a.ts",
        "import { bump } from './cycle-b.ts';\nconst state = { counterValue: 0 };\nexport \
         function counter() { return bump(state); }\n",
    ),
    (
        "cycle-b.ts",
        "import { counter } from './cycle-a.ts';\nexport function bump(s: { counterValue: number \
         }) { s.counterValue += 1; return s.counterValue; }\nexport const again = () => counter;\n",
    ),
];

#[test]
fn shortening_crosses_internal_imports_reexports_and_cycles() {
    let (temp, report) = run(MODULES, Some(&[SHORTENING]));
    let root = temp.path();
    assert!(applied(&report, SHORTENING) > 0);
    assert_eq!(applied(&report, DISAMBIGUATION), 0);
    assert_eq!(applied(&report, AMBIGUATION), 0);
    assert!(saved(&report) > 0);
    for t in transformations(&report, SHORTENING) {
        assert!(uses(t).is_empty(), "{t}");
    }
    // Objects created in one module and read in another share one group.
    let dep = emitted(root, "dep.ts");
    assert!(
        !dep.contains("verboseLevel: level") && !dep.contains("outputFormat: 'json'"),
        "{dep}"
    );
    let main = emitted(root, "main.ts");
    for name in [
        "verboseLevel",
        "defaultLongName",
        "deepPropertyName",
        "outerPropertyName",
    ] {
        assert!(!main.contains(name), "{name}: {main}");
    }
    assert!(!emitted(root, "consts.ts").contains("defaultLongName"));
    assert!(!emitted(root, "cycle-b.ts").contains("counterValue"));
    // Destructured parameter bindings keep their names.
    assert!(dep.contains(": outputFormat"), "{dep}");
    let all = mappings(root);
    assert_eq!(mapping(&all, "verboseLevel").len(), 1);
    assert_ne!(mapping(&all, "verboseLevel")[0]["renamed"], "verboseLevel");

    // Other passes may delete or inline code first, so only the net result
    // is comparable with every pass enabled.
    let (_, everything) = run(MODULES, None);
    assert!(saved(&everything) > 0);
}

#[test]
fn shortening_keeps_destructured_bindings_and_defaults() {
    let (temp, report) = run(
        &[(
            "main.ts",
            "let calls = 0;\nfunction describe({ firstLongName, secondLongName = ++calls }: { \
             firstLongName: number; secondLongName?: number }) {\n  return firstLongName + \
             secondLongName;\n}\nconsole.log(describe({ firstLongName: 1, secondLongName: \
             undefined }), describe({ firstLongName: 2, secondLongName: 5 }), calls);\nexport \
             {};\n",
        )],
        Some(&[SHORTENING]),
    );
    assert!(renamed(&report) > 0);
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains(": firstLongName"), "{main}");
    assert!(main.contains("secondLongName = ++calls"), "{main}");
}

#[test]
fn shortening_follows_containment_until_the_container_escapes() {
    let (_, report) = run(
        &[(
            "main.ts",
            "const outer = { innerObject: { deepPropertyName: 1 } };\nconst alias = \
             outer.innerObject;\nalias.deepPropertyName += \
             1;\nconsole.log(outer.innerObject.deepPropertyName);\nexport {};\n",
        )],
        Some(&[SHORTENING]),
    );
    assert_eq!(renamed(&report), 2);
    // A contained object stored into an unknown receiver escapes; its
    // container stays renamable.
    let (temp, _) = run(
        &[(
            "main.ts",
            "const outer = { innerObject: { deepPropertyName: 1 } };\nconst sink: any = \
             {};\nsink.held = outer.innerObject;\nconsole.log(JSON.stringify(sink), \
             outer.innerObject.deepPropertyName);\nexport {};\n",
        )],
        None,
    );
    assert!(emitted(temp.path(), "main.ts").contains("deepPropertyName: 1"));
    for code in [
        // The container escapes, so its contents do.
        "const outer = { innerObject: { deepPropertyName: 1 } \
         };\nconsole.log(JSON.stringify(outer), outer.innerObject.deepPropertyName);\nexport {};\n",
        // Computed names are not modeled as fields.
        "const name = 'dyn' + 'amic';\nconst outer = { [name]: { deepPropertyName: 1 } \
         };\nconsole.log(JSON.stringify(outer));\nexport {};\n",
        // Arrays are untracked containers.
        "const inner = { deepPropertyName: 1 };\nconst list = \
         [inner];\nconsole.log(JSON.stringify(list), inner.deepPropertyName);\nexport {};\n",
    ] {
        let (_, report) = run(&[("main.ts", code)], None);
        assert_eq!(renamed(&report), 0, "{code}");
    }
}

#[test]
fn shortening_preserves_public_and_javascript_boundaries() {
    let dep = (
        "dep.ts",
        "export function make() { return { longPropertyName: 1 }; }\n",
    );
    // A function re-exported by the entry is a public contract.
    let (_, report) = run(
        &[
            (
                "main.ts",
                "import { make } from './dep.ts';\nexport { make } from \
                 './dep.ts';\nconsole.log(make().longPropertyName);\n",
            ),
            dep,
        ],
        None,
    );
    assert_eq!(renamed(&report), 0);
    // Entry exports.
    let (_, report) = run(
        &[(
            "main.ts",
            "export const settings = { longPropertyName: 1 \
             };\nconsole.log(settings.longPropertyName);\n",
        )],
        None,
    );
    assert_eq!(renamed(&report), 0);
    // JavaScript receives the object.
    let temp = project(&[
        (
            "main.ts",
            "import { make } from './dep.ts';\nimport { read } from './external.js';\nconst value \
             = make();\nconsole.log(read(value), value.longPropertyName);\n",
        ),
        dep,
        (
            "external.js",
            "export function read(value) { return value.longPropertyName; }\n",
        ),
    ]);
    fs::write(
        temp.path().join("tsconfig.json"),
        r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true,"allowJs":true,"rewriteRelativeImportExtensions":true}}"#,
    )
    .unwrap();
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(renamed(&report), 0);
}

#[test]
fn shortening_preserves_exports_reachable_through_namespace_objects() {
    let dep = (
        "dep.ts",
        "export const settings = { longPropertyName: 1 };\n",
    );
    for main in [
        // Dynamic import: the namespace object is a value.
        "import { settings } from './dep.ts';\nconst ns: any = await import('./dep.ts');\nconst \
         key = 'sett' + 'ings';\nconsole.log(settings.longPropertyName, \
         JSON.stringify(ns[key]));\n",
        // A namespace import used as a value.
        "import * as dep from './dep.ts';\nimport { settings } from './dep.ts';\nconst pick = (m: \
         any) => m['sett' + 'ings'];\nconsole.log(settings.longPropertyName, \
         JSON.stringify(pick(dep)));\n",
    ] {
        let (_, report) = run(&[("main.ts", main), dep], None);
        assert_eq!(renamed(&report), 0, "{main}");
    }
    // Member access through a namespace import stays analyzable.
    let (_, report) = run(
        &[
            (
                "main.ts",
                "import * as dep from './dep.ts';\nimport { settings } from \
                 './dep.ts';\nconsole.log(settings.longPropertyName, typeof dep);\n",
            ),
            dep,
        ],
        None,
    );
    assert_eq!(renamed(&report), 0, "typeof exposes the namespace");
}

#[test]
fn shortening_safety_boundaries_keep_names() {
    let cases = [
        // `typeof` connects the object's names to another type.
        "const obj = { longPropertyName: 1 };\ntype Shape = typeof obj;\nconst copy: Shape = { \
         longPropertyName: 2 };\nconsole.log(obj.longPropertyName, \
         copy.longPropertyName);\nexport {};\n",
        // A named type assertion.
        "interface Shape { longPropertyName: number }\nconst obj = { longPropertyName: 1 } as \
         Shape;\nconsole.log(obj.longPropertyName);\nexport {};\n",
        // A generic parameter type describes the value.
        "function identity<T>(value: T): T { return value; }\nconst obj = identity({ \
         longPropertyName: 1 });\nconsole.log(obj.longPropertyName);\nexport {};\n",
        // Destructuring assignment and loop targets replace the value.
        "let obj = { longPropertyName: 1 };\n[obj] = \
         [JSON.parse('{\"longPropertyName\":2}')];\nconsole.log(obj.longPropertyName);\nexport \
         {};\n",
        "let obj = { longPropertyName: 1 };\nfor (obj of \
         [JSON.parse('{\"longPropertyName\":2}')]) {}\nconsole.log(obj.longPropertyName);\nexport \
         {};\n",
        "let obj = { longPropertyName: 1 };\n({ obj } = { obj: \
         JSON.parse('{\"longPropertyName\":2}') });\nconsole.log(obj.longPropertyName);\nexport \
         {};\n",
        // Calls through a property pass the object as `this`.
        "function helper(this: { longPropertyName: number }) { return this.longPropertyName; \
         }\nconst obj = { longPropertyName: 1, run: helper };\nconsole.log(obj.run());\nexport \
         {};\n",
        // Deletion, string conversion, loose comparison, proxies.
        "const obj = { longPropertyName: 1 as number | undefined };\ndelete (obj as \
         any).longPropertyName;\nconsole.log(obj.longPropertyName);\nexport {};\n",
        "const obj = { longPropertyName: 1, toString() { return 'x'; } };\nconsole.log(`${obj}`, \
         obj.longPropertyName);\nexport {};\n",
        "const obj = { longPropertyName: 1 };\nconsole.log(obj == (null as any), \
         obj.longPropertyName);\nexport {};\n",
        "const obj = { longPropertyName: 1 };\nconst proxy = new Proxy(obj, \
         {});\nconsole.log(proxy.longPropertyName);\nexport {};\n",
        // `arguments` and `this` make a function an unknown boundary.
        "function read(value: { longPropertyName: number }) { return arguments.length + \
         value.longPropertyName; }\nconsole.log(read({ longPropertyName: 1 }));\nexport {};\n",
        // A value read before a closed function joins unknown values.
        "function read(value: { longPropertyName: number }) { return value.longPropertyName; \
         }\nconsole.log(read({ longPropertyName: 1 }), \
         read(JSON.parse('{\"longPropertyName\":2}')));\nexport {};\n",
        // Default export of a value.
        "const obj = { longPropertyName: 1 };\nconsole.log(obj.longPropertyName);\nexport default \
         obj;\n",
    ];
    for code in cases {
        // Property passes alone: call-inlining could legitimately separate
        // a fresh literal from the unknown values these fixtures join.
        let (_, report) = run(
            &[("main.ts", code)],
            Some(&[SHORTENING, DISAMBIGUATION, AMBIGUATION, FINITE_KEYS]),
        );
        assert_eq!(renamed(&report), 0, "{code}");
    }
}

#[test]
fn property_passes_never_run_when_shortening_is_disabled() {
    let (temp, report) = run(MODULES, Some(&[DISAMBIGUATION, AMBIGUATION, FINITE_KEYS]));
    for pass in [SHORTENING, DISAMBIGUATION, AMBIGUATION] {
        assert_eq!(applied(&report, pass), 0);
        assert_eq!(report["pass_summary"][pass]["cleanup_in"], 0);
    }
    assert_eq!(renamed(&report), 0);
    assert!(emitted(temp.path(), "dep.ts").contains("verboseLevel"));
    let (_, report) = run(MODULES, Some(&[]));
    assert_eq!(renamed(&report), 0);
    assert_eq!(saved(&report), 0);
}

// ---------------------------------------------------------------------------
// property-disambiguation
// ---------------------------------------------------------------------------

const UNRELATED: &[(&str, &str)] = &[
    (
        "main.ts",
        "import { local } from './local.ts';\nconst kept = { sharedPropertyName: 1, \
         keptOtherName: 2 };\nconsole.log(JSON.stringify(kept));\nconsole.log(local());\n",
    ),
    (
        "local.ts",
        "export function local() {\n  const value = { sharedPropertyName: 3, localOtherName: 4 \
         };\n  return value.sharedPropertyName + value.sharedPropertyName + \
         value.localOtherName;\n}\n",
    ),
];

#[test]
fn disambiguation_separates_unrelated_same_named_properties() {
    let (with, report) = run(UNRELATED, Some(&[SHORTENING, DISAMBIGUATION]));
    assert!(applied(&report, DISAMBIGUATION) > 0);
    for t in transformations(&report, DISAMBIGUATION) {
        assert_eq!(uses(t), [SHORTENING]);
    }
    let local = emitted(with.path(), "local.ts");
    assert!(!local.contains("sharedPropertyName"), "{local}");
    assert!(emitted(with.path(), "main.ts").contains("sharedPropertyName"));
    let all = mappings(with.path());
    let shared = mapping(&all, "sharedPropertyName");
    assert_eq!(shared.len(), 2);
    assert_ne!(shared[0]["group"], shared[1]["group"]);
    assert_ne!(shared[0]["renamed"], shared[1]["renamed"]);

    // Ablation: without disambiguation, the escaping object preserves the
    // name everywhere.
    let (without, ablated) = run(UNRELATED, Some(&[SHORTENING]));
    assert_eq!(applied(&ablated, DISAMBIGUATION), 0);
    assert!(emitted(without.path(), "local.ts").contains("sharedPropertyName"));
    assert!(saved(&report) > saved(&ablated));
}

#[test]
fn disambiguation_joins_objects_that_meet_through_flows() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { pick } from './pick.ts';\nconst first = { sharedPropertyName: 1, \
                 firstOnlyName: 2 };\nconst second = { sharedPropertyName: 3, secondOnlyName: 4 \
                 };\nconst chosen = pick<{ sharedPropertyName: number }>(Math.random() > 2, \
                 first, second);\nconsole.log(chosen.sharedPropertyName, first.firstOnlyName, \
                 second.secondOnlyName);\nconst unrelated = { sharedPropertyName: 5 \
                 };\nconsole.log(unrelated.sharedPropertyName);\n",
            ),
            (
                "pick.ts",
                "export function pick<T>(flag: boolean, a: T, b: T) { return flag ? a : b; }\n",
            ),
        ],
        Some(&[SHORTENING, DISAMBIGUATION]),
    );
    let all = mappings(temp.path());
    // `T` is a type constraint on the joined objects: preserved together.
    let shared = mapping(&all, "sharedPropertyName");
    assert_eq!(shared.len(), 2);
    let joined: Vec<_> = shared
        .iter()
        .filter(|m| m["renamed"] == "sharedPropertyName")
        .collect();
    assert_eq!(joined.len(), 1);
    assert!(renamed(&report) > 0);

    // Without a type parameter the joined region is renamed consistently.
    let (temp, _) = run(
        &[(
            "main.ts",
            "function pick(flag: boolean, a: { sharedPropertyName: number }, b: { \
             sharedPropertyName: number }) { return flag ? a : b; }\nconst first = { \
             sharedPropertyName: 1, firstOnlyName: 2 };\nconst second = { sharedPropertyName: 3, \
             secondOnlyName: 4 };\nconst alias = second;\nconsole.log(pick(Math.random() > 2, \
             first, alias).sharedPropertyName, first.firstOnlyName, \
             second.secondOnlyName);\nexport {};\n",
        )],
        Some(&[SHORTENING, DISAMBIGUATION]),
    );
    let all = mappings(temp.path());
    let shared = mapping(&all, "sharedPropertyName");
    assert_eq!(shared.len(), 1, "one joined group");
    assert_ne!(shared[0]["renamed"], "sharedPropertyName");
}

#[test]
fn disambiguation_attribution_matches_its_contribution() {
    // Unique names need no disambiguation.
    let (_, report) = run(
        &[(
            "main.ts",
            "const value = { uniquePropertyName: 1 \
             };\nconsole.log(value.uniquePropertyName);\nexport {};\n",
        )],
        Some(&[SHORTENING, DISAMBIGUATION]),
    );
    assert_eq!(applied(&report, SHORTENING), 1);
    assert_eq!(applied(&report, DISAMBIGUATION), 0);
    assert_eq!(report["pass_summary"][DISAMBIGUATION]["cleanup_in"], 0);
}

// ---------------------------------------------------------------------------
// property-ambiguation
// ---------------------------------------------------------------------------

/// Three independent regions with twenty long properties each.
fn many_regions() -> String {
    let mut code = String::new();
    for region in 0..3 {
        let names: Vec<String> = (0..20)
            .map(|i| format!("region{region}PropertyNumber{i}"))
            .collect();
        code.push_str(&format!(
            "const value{region} = {{ {} }};\n",
            names
                .iter()
                .enumerate()
                .map(|(i, n)| format!("{n}: {i}"))
                .collect::<Vec<_>>()
                .join(", ")
        ));
        code.push_str(&format!(
            "console.log({});\n",
            names
                .iter()
                .map(|n| format!("value{region}.{n}"))
                .collect::<Vec<_>>()
                .join(" + ")
        ));
    }
    code.push_str("export {};\n");
    code
}

#[test]
fn ambiguation_reuses_short_names_across_noninterfering_regions() {
    let code = many_regions();
    let (temp, report) = run(
        &[("main.ts", &code)],
        Some(&[SHORTENING, DISAMBIGUATION, AMBIGUATION]),
    );
    assert!(applied(&report, AMBIGUATION) > 0);
    for t in transformations(&report, AMBIGUATION) {
        assert!(uses(t).contains(&SHORTENING.to_owned()));
    }
    let all = mappings(temp.path());
    let mut per_group = std::collections::BTreeMap::<String, Vec<String>>::new();
    for m in &all {
        let group = m["group"].as_str().unwrap();
        let object = group[..group.rfind(':').unwrap()].to_owned();
        per_group
            .entry(object)
            .or_default()
            .push(m["renamed"].as_str().unwrap().to_owned());
    }
    assert_eq!(per_group.len(), 3);
    for names in per_group.values() {
        let unique: std::collections::BTreeSet<_> = names.iter().collect();
        assert_eq!(unique.len(), names.len(), "names within one object differ");
        assert!(names.iter().all(|n| n.len() == 1));
    }

    // Ablation: globally unique names need two characters for some.
    let (without, ablated) = run(&[("main.ts", &code)], Some(&[SHORTENING, DISAMBIGUATION]));
    assert_eq!(applied(&ablated, AMBIGUATION), 0);
    let names: std::collections::BTreeSet<_> = mappings(without.path())
        .iter()
        .map(|m| m["renamed"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(names.len(), 60);
    assert!(saved(&report) > saved(&ablated));
}

#[test]
fn ambiguation_never_reuses_names_on_one_receiver_or_retained_names() {
    let (temp, _) = run(
        &[(
            "main.ts",
            "const first = { a: 1, longPropertyName: 2 };\nconst second = { otherPropertyName: 3 \
             };\nconst joined = Math.random() > 2 ? first : { a: 4, longPropertyName: 5 \
             };\nconsole.log(first.a, first.longPropertyName, second.otherPropertyName, \
             joined.longPropertyName, JSON.stringify(second.otherPropertyName));\nexport {};\n",
        )],
        // Constant inlining would remove the literal-only objects first.
        Some(&[SHORTENING, DISAMBIGUATION, AMBIGUATION]),
    );
    let all = mappings(temp.path());
    let long = mapping(&all, "longPropertyName");
    assert_eq!(long.len(), 1);
    assert_ne!(long[0]["renamed"], "a");
    assert_ne!(long[0]["renamed"], "longPropertyName");
    // The other region may reuse the first short name.
    assert_eq!(mapping(&all, "otherPropertyName")[0]["renamed"], "a");
}

// ---------------------------------------------------------------------------
// finite-key-rewriting
// ---------------------------------------------------------------------------

#[test]
fn finite_keys_resolve_literal_and_const_singletons() {
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { SELECTED_ENTRY_KEY } from './keys.ts';\nconst table = { first: 1, \
                 second: 2 };\nconst LOCAL_SELECTED_KEY = 'second';\nfunction read() { return \
                 table[SELECTED_ENTRY_KEY] + table[LOCAL_SELECTED_KEY]; \
                 }\nconsole.log(table['first'], read(), table?.['second']);\n",
            ),
            ("keys.ts", "export const SELECTED_ENTRY_KEY = 'first';\n"),
        ],
        Some(&[FINITE_KEYS]),
    );
    assert!(applied(&report, FINITE_KEYS) >= 3);
    let main = emitted(temp.path(), "main.ts");
    assert!(main.contains("table.first"), "{main}");
    assert!(main.contains("table.second"), "{main}");
    // Dropping the last use of an import would elide it; without the
    // cleanup pass the access stays or the gate keeps the dependency with a
    // bare import.
    assert!(
        main.contains("table[SELECTED_ENTRY_KEY]") || main.contains("import \"./keys.js\""),
        "{main}"
    );
    assert!(main.contains("table?.second"), "{main}");
    // Without residual-dependency-removal the declaration stays.
    assert!(main.contains("LOCAL_SELECTED_KEY"), "{main}");
    for t in transformations(&report, FINITE_KEYS) {
        assert!(uses(t).is_empty());
    }

    // With the cleanup pass the unused local declaration goes away in the
    // same bundle; the exported key stays.
    let (temp, report) = run(
        &[
            (
                "main.ts",
                "import { SELECTED_ENTRY_KEY } from './keys.ts';\nconst table = { first: 1, \
                 second: 2 };\nconst KEY = 'second';\nconsole.log(table[KEY], \
                 table[SELECTED_ENTRY_KEY], SELECTED_ENTRY_KEY);\n",
            ),
            ("keys.ts", "export const SELECTED_ENTRY_KEY = 'first';\n"),
        ],
        Some(&[FINITE_KEYS, RESIDUAL]),
    );
    let main = emitted(temp.path(), "main.ts");
    assert!(
        !main.contains("const KEY") && !main.contains("table["),
        "{main}"
    );
    let mut bundles: Vec<Vec<String>> = transformations(&report, FINITE_KEYS)
        .into_iter()
        .map(uses)
        .collect();
    bundles.sort();
    assert_eq!(bundles, [vec![], vec![RESIDUAL.to_owned()]]);
}

#[test]
fn finite_keys_coordinate_multi_key_sets_with_renaming() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        "const table = { firstEntryName: 1, secondEntryName: 2 };\nconst pick = Math.random() > 2 \
         ? 'firstEntryName' : 'secondEntryName';\nconsole.log(table[pick], \
         table.secondEntryName);\nexport {};\n",
    )];
    let (temp, report) = run(files, Some(&[SHORTENING, FINITE_KEYS]));
    let main = emitted(temp.path(), "main.ts");
    assert!(!main.contains("EntryName"), "{main}");
    let bundles = transformations(&report, SHORTENING);
    assert!(
        bundles
            .iter()
            .any(|t| uses(t).contains(&FINITE_KEYS.to_owned())),
        "{report}"
    );
    // Ablation: without the pass the key stays dynamic and the object keeps
    // its names.
    let (temp, report) = run(files, Some(&[SHORTENING]));
    assert_eq!(renamed(&report), 0);
    assert!(emitted(temp.path(), "main.ts").contains("firstEntryName"));
}

#[test]
fn finite_keys_preserve_unproven_and_observable_keys() {
    let cases = [
        // External string.
        "const table = { first: 1, second: 2 };\nconst key = ((globalThis as any).missing ?? \
         'first') as 'first';\nconsole.log(table[key]);\nexport {};\n",
        // The key text is observed.
        "const table = { firstEntryName: 1, secondEntryName: 2 };\nconst pick = Math.random() > 2 \
         ? 'firstEntryName' : 'secondEntryName';\nconsole.log(pick, table[pick]);\nexport {};\n",
        // The same key also selects a property of an unknown object.
        "const table = { firstEntryName: 1, secondEntryName: 2 };\nconst external: any = \
         JSON.parse('{\"firstEntryName\":5}');\nconst pick = Math.random() < 2 ? 'firstEntryName' \
         : 'secondEntryName';\nconsole.log(table[pick], external[pick]);\nexport {};\n",
        // The key is stored in an object that escapes.
        "const table = { firstEntryName: 1, secondEntryName: 2 };\nconst pick = Math.random() < 2 \
         ? 'firstEntryName' : 'secondEntryName';\nconst holder = { key: pick \
         };\nconsole.log(table[pick], JSON.stringify(holder));\nexport {};\n",
        // Comparison observes the key text.
        "const table = { firstEntryName: 1, secondEntryName: 2 };\nconst pick = Math.random() > 2 \
         ? 'firstEntryName' : 'secondEntryName';\nconsole.log(pick === 'firstEntryName', \
         table[pick]);\nexport {};\n",
        // A type-level key set without runtime provenance.
        "const table = { firstEntryName: 1, secondEntryName: 2 };\nfunction get(key: \
         'firstEntryName' | 'secondEntryName') { return table[key]; \
         }\nconsole.log(get('firstEntryName'));\nexport {};\n",
        // keyof assertion.
        "const table = { firstEntryName: 1 };\nconst key: string = \
         'firstEntryName';\nconsole.log(table[key as keyof typeof table]);\nexport {};\n",
        // Numeric coercion.
        "const table = { 1: 'one', firstEntryName: 2 };\nconst key = 1;\nconsole.log(table[key], \
         table.firstEntryName);\nexport {};\n",
    ];
    for code in cases {
        let (_, report) = run(&[("main.ts", code)], None);
        assert_eq!(renamed(&report), 0, "{code}");
    }
    let (_, report) = run(
        &[(
            "main.ts",
            "const table = { firstEntryName: 1, secondEntryName: 2 };\nfunction get(key: \
             'firstEntryName' | 'secondEntryName') { return table[key]; \
             }\nconsole.log(get('firstEntryName'));\nexport {};\n",
        )],
        Some(&[FINITE_KEYS]),
    );
    assert!(
        skipped_reasons(&report, FINITE_KEYS).contains(&"unproven_runtime_fact".to_owned()),
        "{report}"
    );
}

#[test]
fn finite_keys_respect_initialization_privacy_and_ownership() {
    let cases: &[(&str, &[(&str, &str)])] = &[
        // Temporal dead zone: the access may run before the declaration.
        (
            "tdz",
            &[(
                "main.ts",
                "const table = { first: 1 };\nfunction early() { return table[KEY]; }\nlet \
                 result: unknown;\ntry { result = early(); } catch (error) { result = (error as \
                 Error).name; }\nconsole.log(result);\nconst KEY = 'first';\nexport {};\n",
            )],
        ),
        // Import cycles may read the binding early.
        (
            "cycle",
            &[
                (
                    "main.ts",
                    "import { read } from './a.ts';\nconsole.log(read());\n",
                ),
                (
                    "a.ts",
                    "import { KEY_IN_CYCLE } from './b.ts';\nconst table = { first: 1 };\nexport \
                     function read() { return table[KEY_IN_CYCLE]; }\n",
                ),
                (
                    "b.ts",
                    "import { read } from './a.ts';\nexport const KEY_IN_CYCLE = 'first';\nexport \
                     const again = () => read;\n",
                ),
            ],
        ),
        // Private members are only reachable through computed access.
        (
            "private",
            &[(
                "main.ts",
                "class Box { private secret = 1; static peek(box: Box) { return box['secret']; } \
                 }\nconsole.log(Box.peek(new Box()));\nexport {};\n",
            )],
        ),
        // Index signatures and `any` receivers are not application-owned
        // declarations.
        (
            "index",
            &[(
                "main.ts",
                "const map: Record<string, number> = { first: 1 };\nconst loose: any = { first: 2 \
                 };\nconsole.log(map['first'], loose['first']);\nexport {};\n",
            )],
        ),
        // Protocol names stay computed.
        (
            "proto",
            &[(
                "main.ts",
                "const holder = { constructor: 1, value: 2 };\nconsole.log(holder['constructor'], \
                 holder['value']);\nexport {};\n",
            )],
        ),
    ];
    for (name, files) in cases {
        let (temp, report) = run(files, Some(&[FINITE_KEYS]));
        let main_like = if *name == "cycle" { "a.ts" } else { "main.ts" };
        let code = emitted(temp.path(), main_like);
        match *name {
            "proto" => {
                assert!(code.contains("holder['constructor']"), "{code}");
                assert!(code.contains("holder.value"), "{code}");
            }
            _ => {
                assert_eq!(applied(&report, FINITE_KEYS), 0, "{name}: {code}");
            }
        }
    }
}

#[test]
fn finite_keys_keep_getters_and_never_run_when_disabled() {
    let files: &[(&str, &str)] = &[(
        "main.ts",
        "let reads = 0;\nconst table = { get first() { reads++; return 1; } };\nconst KEY = \
         'first';\nconsole.log(table['first'], table[KEY], reads);\nexport {};\n",
    )];
    // The static form calls the same getter.
    let (temp, report) = run(files, Some(&[FINITE_KEYS]));
    assert!(applied(&report, FINITE_KEYS) > 0);
    assert!(emitted(temp.path(), "main.ts").contains("table.first"));
    let (_, report) = run(files, None);
    assert_eq!(renamed(&report), 0);
    // Disabled.
    let (temp, report) = run(files, Some(&[SHORTENING, RESIDUAL]));
    assert_eq!(applied(&report, FINITE_KEYS), 0);
    assert_eq!(report["pass_summary"][FINITE_KEYS]["cleanup_in"], 0);
    let main = emitted(temp.path(), "main.ts");
    assert!(
        main.contains("table['first']") || main.contains("table[\"first\"]"),
        "{main}"
    );
}

#[test]
fn property_results_are_deterministic_and_check_matches_output() {
    let temp = project(MODULES);
    let checked = check(temp.path(), &[]);
    let written = optimize(temp.path(), &[]);
    let strip = |mut r: Value| {
        r.as_object_mut().unwrap().remove("measurements");
        r
    };
    assert_eq!(strip(checked.clone()), strip(written));
    assert_eq!(strip(check(temp.path(), &[])), strip(checked));
    let first = fs::read(temp.path().join("out/properties.json")).unwrap();
    let again = project(MODULES);
    optimize(again.path(), &[]);
    let second = String::from_utf8(fs::read(again.path().join("out/properties.json")).unwrap())
        .unwrap()
        .replace(
            &again.path().canonicalize().unwrap().display().to_string(),
            &temp.path().canonicalize().unwrap().display().to_string(),
        );
    assert_eq!(String::from_utf8(first).unwrap(), second);
}
