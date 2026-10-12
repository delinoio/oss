// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Stage 2 (`--bundle`): optimization of bundler output chunks as one closed
//! JavaScript program. Every fixture runs the original and the optimized
//! entry chunk in Node and compares stdout, stderr and exit status. Bundled
//! JavaScript is not type-checked, so many fixtures make inferred types lie
//! and check that no pass trusts them.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use std::{fs, path::Path, process::Command};

use common::{applied, saved};
use serde_json::Value;

/// A temporary bundle directory `dist/` with the given chunks.
fn bundle(chunks: &[(&str, &str)]) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    let dist = temp.path().join("dist");
    fs::create_dir_all(&dist).unwrap();
    fs::write(dist.join("package.json"), r#"{"type":"module"}"#).unwrap();
    for (name, code) in chunks {
        let path = dist.join(name);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, code).unwrap();
    }
    temp
}

fn run_typeopt(root: &Path, extra: &[&str]) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(root)
        .args(["--bundle", "dist", "--out-dir", "out", "--verbose"])
        .args(extra)
        .output()
        .unwrap()
}

/// Optimizes `dist/` into `out/`, asserts success and returns the report.
fn optimize(root: &Path, extra: &[&str]) -> Value {
    let output = run_typeopt(root, extra);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    serde_json::from_slice(&output.stdout).unwrap()
}

fn node(entry: &Path) -> std::process::Output {
    Command::new("node").arg(entry).output().unwrap()
}

/// Runs `index.js` of the original and the optimized bundle.
fn same_behavior(root: &Path) {
    let before = node(&root.join("dist/index.js"));
    let after = node(&root.join("out/index.js"));
    assert!(
        before.status.success(),
        "fixture must run: {}",
        String::from_utf8_lossy(&before.stderr)
    );
    assert_eq!(
        before.status.success(),
        after.status.success(),
        "after: {}",
        String::from_utf8_lossy(&after.stderr)
    );
    assert_eq!(
        String::from_utf8_lossy(&before.stdout),
        String::from_utf8_lossy(&after.stdout)
    );
}

fn out(root: &Path, chunk: &str) -> String {
    fs::read_to_string(root.join("out").join(chunk)).unwrap()
}

fn run(chunks: &[(&str, &str)]) -> (tempfile::TempDir, Value) {
    let temp = bundle(chunks);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    (temp, report)
}

// ---------------------------------------------------------------------------
// Positive fixtures
// ---------------------------------------------------------------------------

#[test]
fn renames_properties_across_chunks_and_shrinks_the_bundle() {
    let (temp, report) = run(&[
        (
            "index.js",
            r#"import{makeSettings as m,describeSettings as d}from"./settings.js";const s=m(3);console.log(d(s));console.log(d(m(7)));"#,
        ),
        (
            "settings.js",
            r#"function makeSettings(n){return{verticalSpacing:n,horizontalSpacing:n*2,highlightColour:n>4?"red":"blue"}}function describeSettings(s){return s.verticalSpacing+":"+s.horizontalSpacing+":"+s.highlightColour}export{makeSettings,describeSettings};"#,
        ),
    ]);
    assert!(saved(&report) > 0, "{report:#}");
    assert!(report["renamed_properties"].as_u64().unwrap() >= 3);
    assert!(!out(temp.path(), "settings.js").contains("verticalSpacing"));
    // Bundle mode never offers passes whose proofs depend on static types.
    let disabled: Vec<&str> = report["passes"]["disabled"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| p.as_str().unwrap())
        .collect();
    for pass in [
        "unused-method-removal",
        "unused-field-removal",
        "method-devirtualization",
        "discriminant-compaction",
        "constant-property-inlining",
    ] {
        assert!(disabled.contains(&pass), "{pass} must be unavailable");
    }
}

#[test]
fn removes_unused_internal_code_but_keeps_entry_exports() {
    let (temp, report) = run(&[
        (
            "index.js",
            r#"import{used as u}from"./lib.js";export function api(o){return o.publicField}console.log(u(2),api({publicField:5}));"#,
        ),
        (
            "lib.js",
            r#"function used(n){return n*3}function neverCalledAnywhereInTheBundle(n){return n.toString(16).padStart(8,"0")+"-"+n}export{used,neverCalledAnywhereInTheBundle};"#,
        ),
    ]);
    assert!(
        applied(&report, "residual-dependency-removal") > 0,
        "{report:#}"
    );
    assert!(!out(temp.path(), "lib.js").contains("neverCalledAnywhereInTheBundle"));
    // The entry chunk's export is a public contract.
    let index = out(temp.path(), "index.js");
    assert!(index.contains("export function api"));
    assert!(index.contains("publicField"));
}

// ---------------------------------------------------------------------------
// Boundaries specific to bundled JavaScript
// ---------------------------------------------------------------------------

/// faker-style: modules hang off a container instance and are looked up by
/// strings parsed at runtime. Every method must survive.
#[test]
fn keeps_methods_reached_through_runtime_parsed_paths() {
    let (temp, _) = run(&[(
        "index.js",
        r#"class Airline{seat(){return"1A"}recordLocator(){return"QX7"}}class Faker{constructor(){this.airline=new Airline}}const f=new Faker;const templates=["{{airline.recordLocator}}"];function fake(t){return t.replace(/\{\{(.+?)\}\}/g,(_,p)=>{let c=f;const parts=p.split(".");const m=parts.pop();for(const k of parts)c=c?.[k];return c[m]()})}console.log(f.airline.seat(),fake(templates[0]));"#,
    )]);
    assert!(out(temp.path(), "index.js").contains("recordLocator"));
}

#[test]
fn keeps_properties_read_through_parsed_paths() {
    run(&[(
        "index.js",
        r#"const config={alphaSetting:1,betaSetting:{gammaSetting:2}};function get(r,p){return p.split(".").reduce((c,k)=>c?.[k],r)}console.log(get(config,"betaSetting.gammaSetting"),get({alphaSetting:3},"alphaSetting"));"#,
    )]);
}

#[test]
fn keeps_properties_read_by_generated_functions() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const cfg={alphaSetting:41};const read=new Function("c","return c.alphaSetting+1");console.log(read(cfg));"#,
    )]);
    assert!(out(temp.path(), "index.js").contains("alphaSetting:"));
}

/// JSDoc and initializer types are unchecked in bundles and may lie.
#[test]
fn does_not_trust_unchecked_javascript_types() {
    let (temp, _) = run(&[(
        "index.js",
        r#"/** @type {string} */const n=globalThis.process?42:42;let a="";if(Math.random()<2)a=5;/** @type {{v:number}} */let o=JSON.parse("null");function f(x){return x?.v}console.log(typeof String(n),String(n)+1,typeof String(a),String(a)+1,o?.v,f(null),f({v:2}),!!n);"#,
    )]);
    // `n` is proven to be the number 42 at runtime regardless of its JSDoc
    // type, so its conversions may fold; `a` may hold a number or a string.
    let index = out(temp.path(), "index.js");
    assert!(index.contains("String(a)"));
    assert!(index.contains("o?.v"));
}

#[test]
fn keeps_dynamically_selected_namespace_exports() {
    let (temp, _) = run(&[
        (
            "index.js",
            r#"const m=await import("./lazy.js");console.log(m[["hel","lo"].join("")]());"#,
        ),
        (
            "lazy.js",
            r#"function hello(){return"hi"}function other(){return"o"}export{hello,other};"#,
        ),
    ]);
    assert!(out(temp.path(), "lazy.js").contains("function hello"));
}

#[test]
fn keeps_observable_function_length_and_arguments() {
    run(&[(
        "index.js",
        r#"function f(a,b){return a}function g(a,b){return arguments.length}const h=(a,b)=>a;console.log(f(1,2),f.length,g(1,2),h(3,4),h.length);"#,
    )]);
}

/// A chunk that no other chunk imports (a worker or HTML entry) is an entry:
/// its top-level code and exports stay.
#[test]
fn treats_unimported_chunks_as_entries() {
    let temp = bundle(&[
        (
            "index.js",
            r#"const u=new URL("./worker.js",import.meta.url);console.log(u.pathname.endsWith("worker.js"));"#,
        ),
        (
            "worker.js",
            r#"export function onTask(t){return t.taskPayload}function helper(){return 1}console.log(helper());"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    let worker = out(temp.path(), "worker.js");
    assert!(worker.contains("onTask"));
    assert!(worker.contains("taskPayload"));
    assert_eq!(
        node(&temp.path().join("dist/worker.js")).stdout,
        node(&temp.path().join("out/worker.js")).stdout
    );
}

#[test]
fn never_publishes_typescript_only_syntax() {
    let (temp, _) = run(&[(
        "index.js",
        r#"function make(){return{inner:{value:1}}}const o=make();const p=o?.inner;console.log(p?.value,o.inner?.value);"#,
    )]);
    let index = out(temp.path(), "index.js");
    assert!(!index.contains("!."), "{index}");
    assert!(!index.contains(" as "), "{index}");
}

// ---------------------------------------------------------------------------
// Output contract
// ---------------------------------------------------------------------------

#[test]
fn copies_other_files_and_composes_maps_of_edited_chunks() {
    let code = r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=make(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#;
    // An identity-like map: every token maps to the same column of src/main.ts.
    let mut builder = sourcemap::SourceMapBuilder::new(Some("index.js"));
    let id = builder.add_source("src/main.ts");
    builder.set_source_contents(id, Some(code));
    let mut boundary = true;
    for (offset, ch) in code.char_indices() {
        let word = ch.is_alphanumeric();
        if word && boundary {
            builder.add(
                0,
                offset as u32,
                0,
                offset as u32,
                Some("src/main.ts"),
                None,
                false,
            );
        }
        boundary = !word;
    }
    let mut map = Vec::new();
    builder.into_sourcemap().to_writer(&mut map).unwrap();
    let temp = bundle(&[
        ("index.js", code),
        ("index.js.map", std::str::from_utf8(&map).unwrap()),
        ("index.html", "<script type=module src=./index.js></script>"),
        ("style.css", "body{}"),
    ]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    let out_dir = temp.path().join("out");
    assert!(out_dir.join("index.html").exists());
    assert!(out_dir.join("style.css").exists());
    let edited = out(temp.path(), "index.js");
    assert_ne!(edited, code, "the fixture must be optimized");
    let composed =
        sourcemap::SourceMap::from_slice(&fs::read(out_dir.join("index.js.map")).unwrap()).unwrap();
    assert_eq!(composed.get_source(0), Some("src/main.ts"));
    // Every mapped token of unchanged code still starts the same word.
    let mut checked = 0;
    for token in composed.tokens() {
        let at = token.get_dst_col() as usize;
        let src = token.get_src_col() as usize;
        let word = |text: &str, at: usize| -> String {
            text[at..]
                .chars()
                .take_while(|c| c.is_alphanumeric())
                .collect()
        };
        if word(&edited, at) == "console" || word(&edited, at) == "log" {
            assert_eq!(word(&edited, at), word(code, src));
            checked += 1;
        }
    }
    assert_eq!(checked, 2, "{edited}");
    // The input directory is unchanged.
    assert_eq!(
        fs::read(temp.path().join("dist/index.js.map")).unwrap(),
        map
    );
}

fn listing(dir: &Path) -> Vec<String> {
    let mut names: Vec<String> = fs::read_dir(dir)
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .collect();
    names.sort();
    names
}

const HASHED: [(&str, &str); 4] = [
    (
        "index.html",
        r#"<script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    ),
    (
        "assets/index-AAAAAAAA.js",
        r#"import{m as e}from"./settings-BBBBBBBB.js";import{p as t}from"./plain-CCCCCCCC.js";console.log(e(2).verticalSpacing,t());"#,
    ),
    (
        "assets/settings-BBBBBBBB.js",
        r#"function m(n){return{verticalSpacing:n,horizontalSpacing:n*2}}function unusedHelperThatIsNeverCalled(n){return n.toString(36).repeat(3)}export{m,unusedHelperThatIsNeverCalled as u};"#,
    ),
    (
        "assets/plain-CCCCCCCC.js",
        r#"function p(){return"plain"}export{p};"#,
    ),
];

#[test]
fn rehashes_edited_chunks_and_their_importers() {
    let temp = bundle(&HASHED);
    optimize(temp.path(), &[]);
    let assets = listing(&temp.path().join("out/assets"));
    // The untouched leaf keeps its name; the edited chunk and its importer
    // get new hashes, and every reference follows.
    assert!(
        assets.contains(&"plain-CCCCCCCC.js".to_owned()),
        "{assets:?}"
    );
    assert!(!assets.iter().any(|a| a.contains("BBBBBBBB")), "{assets:?}");
    assert!(!assets.iter().any(|a| a.contains("AAAAAAAA")), "{assets:?}");
    let entry = assets.iter().find(|a| a.starts_with("index-")).unwrap();
    let html = fs::read_to_string(temp.path().join("out/index.html")).unwrap();
    assert!(html.contains(&format!("/assets/{entry}")), "{html}");
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = node(&temp.path().join("out/assets").join(entry));
    assert!(
        after.status.success(),
        "{}",
        String::from_utf8_lossy(&after.stderr)
    );
    assert_eq!(before.stdout, after.stdout);
    // Deterministic names.
    let again = bundle(&HASHED);
    optimize(again.path(), &[]);
    assert_eq!(assets, listing(&again.path().join("out/assets")));
}

#[test]
fn keeps_chunk_names_on_request() {
    let temp = bundle(&HASHED);
    optimize(temp.path(), &["--keep-chunk-names"]);
    let assets = listing(&temp.path().join("out/assets"));
    assert_eq!(
        assets,
        [
            "index-AAAAAAAA.js",
            "plain-CCCCCCCC.js",
            "settings-BBBBBBBB.js"
        ]
    );
}

#[test]
fn excluded_chunks_stay_unchanged_boundaries() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import{make as m,read as r}from"./vendor.js";const o=m(3);console.log(r(o));function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
        (
            "vendor.js",
            r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n}}function read(o){return o.verticalSpacing+o.horizontalSpacing}function unusedVendorHelper(){return 1}export{make,read,unusedVendorHelper};"#,
        ),
    ]);
    let report = optimize(temp.path(), &["--bundle-exclude", "vendor"]);
    same_behavior(temp.path());
    assert_eq!(
        out(temp.path(), "vendor.js"),
        fs::read_to_string(temp.path().join("dist/vendor.js")).unwrap()
    );
    assert!(saved(&report) > 0, "{report:#}");
}

#[test]
fn rejects_an_existing_destination_and_bundles_without_chunks() {
    let temp = bundle(&[("index.js", "console.log(1);")]);
    fs::create_dir(temp.path().join("out")).unwrap();
    assert!(!run_typeopt(temp.path(), &[]).status.success());
    let empty = bundle(&[("index.html", "<p>")]);
    assert!(!run_typeopt(empty.path(), &[]).status.success());
}

#[test]
fn bundle_output_is_deterministic() {
    let chunks = [
        (
            "index.js",
            r#"import{a as x}from"./a.js";console.log(x({firstValue:1,secondValue:2}).firstValue);"#,
        ),
        (
            "a.js",
            r#"function a(o){return{firstValue:o.firstValue+o.secondValue,secondValue:0}}export{a};"#,
        ),
    ];
    let one = bundle(&chunks);
    let two = bundle(&chunks);
    optimize(one.path(), &[]);
    optimize(two.path(), &[]);
    for chunk in ["index.js", "a.js"] {
        assert_eq!(out(one.path(), chunk), out(two.path(), chunk));
    }
}
