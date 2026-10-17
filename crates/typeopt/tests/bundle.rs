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

fn strip_ansi(input: &str) -> String {
    let mut output = String::with_capacity(input.len());
    let mut escape = false;
    let mut csi = false;
    for character in input.chars() {
        if !escape {
            if character == '\u{1b}' {
                escape = true;
            } else {
                output.push(character);
            }
            continue;
        }
        if !csi {
            csi = character == '[';
            if !csi {
                escape = false;
            }
            continue;
        }
        if ('@'..='~').contains(&character) {
            escape = false;
            csi = false;
        }
    }
    output
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
    assert_eq!(report["size"]["metric"], "chunk_text_bytes");
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

/// A computed key that is a symbol, or a string with a literal start that no
/// built-in name has, cannot replace a built-in global.
#[test]
fn keeps_intrinsics_beside_globals_written_under_symbol_or_prefixed_keys() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const n=42;const id="x";const tag=Symbol.for("k");Object.defineProperty(globalThis,`app${id}`,{value:1,configurable:true});globalThis[tag]=true;globalThis["app"+id+"2"]=2;console.log(String(n)+"!",globalThis[tag]);export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    assert!(!index.contains("String("), "{index}");
}

/// A reflection helper bound once and only called is analysed like the
/// direct calls, so it does not make every built-in replaceable.
#[test]
fn keeps_intrinsics_beside_an_aliased_object_assign_that_is_only_called() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const n=42;var assign=Object.assign,box={};function make(a){return assign({},a,{extra:1})}console.log(String(n)+"!",make({v:1}).extra,assign(box,{w:2}).w);export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    assert!(!index.contains("String("), "{index}");
}

/// A helper that also escapes as a value may reach unknown code.
#[test]
fn keeps_intrinsics_beside_an_aliased_object_assign_that_escapes() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const n=42;var assign=Object.assign;const list=[assign];console.log(String(n)+"!",list.length,assign({},{a:1}).a);export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    assert!(index.contains("String("), "{index}");
}

/// An alias call with a spread argument or a `Reflect.set` receiver hides its
/// real target.
#[test]
fn keeps_intrinsics_beside_aliased_writers_with_hidden_targets() {
    for code in [
        r#"const n=42;const custom=()=>"patched";const args=[globalThis,{String:custom}];var assign=Object.assign;assign(...args);console.log(String(n)+"!");export{};"#,
        r#"const n=42;var set=Reflect.set;set({},"String",String,globalThis);console.log(String(n)+"!");export{};"#,
    ] {
        let (temp, _) = run(&[("index.js", code)]);
        let index = out(temp.path(), "index.js");
        assert!(index.contains("String("), "{index}");
    }
}

/// A direct `Reflect.set` with a receiver, an exported alias and an alias call
/// on an unproven target all keep the conservative record.
#[test]
fn keeps_intrinsics_beside_reflect_set_receivers_exported_and_unproven_aliases() {
    for code in [
        r#"const n=42;Reflect.set({},"String",String,globalThis);console.log(String(n)+"!");export{};"#,
        r#"const n=42;const root=globalThis;var assign=Object.assign;assign(root,{String:String});console.log(String(n)+"!");export{};"#,
        r#"const n=42;export const assign=Object.assign;console.log(String(n)+"!");"#,
    ] {
        let (temp, _) = run(&[("index.js", code)]);
        let index = out(temp.path(), "index.js");
        assert!(index.contains("String("), "{code}\n{index}");
    }
}

/// A patched or unproven `Symbol.for` may return a string key.
#[test]
fn keeps_intrinsics_when_symbol_for_may_be_replaced() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const n=42;const box={v:Symbol};box.v.for=()=>"String";globalThis[Symbol.for("k")]=String;console.log(String(n)+"!");export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    assert!(index.contains("String("), "{index}");
}

/// A computed key whose literal start is a prefix of a built-in name may
/// replace that built-in.
#[test]
fn keeps_intrinsics_beside_globals_written_under_a_key_that_may_name_them() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const n=42;const tail="ing";globalThis["Str"+tail]=globalThis.String;console.log(String(n)+"!");export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    assert!(index.contains("String("), "{index}");
}

/// `// @ts-check` opts a chunk into semantic checking, but bundle types may
/// be wrong: existing diagnostics do not block optimization.
#[test]
fn ignores_preexisting_ts_check_diagnostics() {
    let (temp, report) = run(&[(
        "index.js",
        "// @ts-check\n/** @type {string} */\nconst n = 42;\nfunction \
         own(x){return{verticalSpacing:x,horizontalSpacing:x}}const \
         s=own(1);console.log(n,s.verticalSpacing+s.horizontalSpacing);export{};",
    )]);
    assert!(saved(&report) > 0, "{report:#}");
    assert!(!out(temp.path(), "index.js").contains("verticalSpacing"));
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

/// Classic scripts share top-level bindings through the global scope, so
/// a chunk that is not known to be an ES module stays unchanged.
#[test]
fn keeps_classic_scripts_unchanged() {
    let legacy = r#"function legacyGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}function unusedLookingHelper(n){return n*2}var legacyState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="./legacy.js"></script><script type="module" src="./index.js"></script>"#,
        ),
        ("legacy.js", legacy),
        (
            "index.js",
            r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "legacy.js"), legacy);
    assert!(
        saved(&report) > 0,
        "the module entry is still optimized: {report:#}"
    );
}

/// Lazy chunks import shared code back from the HTML entry chunk; the entry
/// stays an entry and every chunk belongs to the program.
#[test]
fn keeps_html_entries_that_lazy_chunks_import() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module" src="/index.js"></script>"#,
        ),
        (
            "index.js",
            r#"function shared(n){return{verticalSpacing:n,horizontalSpacing:n}}function unusedSharedHelper(){return 7}const s=shared(1);console.log(s.verticalSpacing);import("./lazy.js").then(l=>console.log(l.page()));export{shared as s};"#,
        ),
        (
            "lazy.js",
            r#"import{s as e}from"./index.js";function page(){const o=e(4);return o.verticalSpacing+o.horizontalSpacing}export{page};"#,
        ),
    ]);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(report["files"].as_u64(), Some(2), "{report:#}");
    assert!(!out(temp.path(), "index.js").contains("unusedSharedHelper"));
}

/// Direct eval can read the bindings of its chunk; that chunk stays
/// unchanged while the rest of the bundle is optimized.
#[test]
fn keeps_chunks_with_eval_unchanged() {
    let evaluating = r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n}}function unusedButEvaluated(){return"via eval"}export function run(code){return eval(code)}export{make};"#;
    let temp = bundle(&[
        (
            "index.js",
            r#"import{run as r,make as m}from"./evaluating.js";console.log(r("unusedButEvaluated()"),m(2).verticalSpacing);function own(n){return{firstOwnValue:n,secondOwnValue:n}}const s=own(1);console.log(s.firstOwnValue+s.secondOwnValue);"#,
        ),
        ("evaluating.js", evaluating),
    ]);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "evaluating.js"), evaluating);
    assert!(saved(&report) > 0, "{report:#}");
}

/// A module chunk `sub.js` made of `snippet` and an optimizable function,
/// imported by an optimizable `index.js`. Returns the temp dir, the chunk
/// text and the report of an optimization whose Node behavior is unchanged.
fn syntax_boundary_case(snippet: &str) -> (tempfile::TempDir, String, Value) {
    let sub = format!(
        "{snippet};function own(n){{return{{verticalSpacing:n,horizontalSpacing:n}}}}const \
         s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);export{{}};"
    );
    let (temp, report) = run(&[
        (
            "index.js",
            r#"import"./sub.js";function mine(n){return{firstOwnValue:n,secondOwnValue:n}}const m=mine(1);console.log(m.firstOwnValue+m.secondOwnValue);"#,
        ),
        ("sub.js", &sub),
    ]);
    (temp, sub, report)
}

/// Mentions of `eval` and `importScripts` that cannot run code or load a
/// script (feature checks, property names, property keys) do not make a
/// chunk a boundary: it is optimized and Node output is unchanged.
#[test]
fn optimizes_chunks_that_only_mention_eval_or_import_scripts() {
    for snippet in [
        r#"const g=typeof self!=="undefined"&&typeof self.importScripts"#,
        r#"console.log(typeof importScripts,typeof eval)"#,
        r#"console.log(typeof (importScripts),typeof (eval),typeof ((eval)))"#,
        r#"const g=typeof self!=="undefined"&&typeof (self.importScripts)"#,
        r#"console.log(typeof (globalThis.eval),typeof (globalThis["importScripts"]))"#,
        r#"const o={eval:1,importScripts:2};console.log(o.eval,o.importScripts)"#,
        r#"const o={"eval":1,eval(){return 2},get importScripts(){return 3}};console.log(o.eval(),o.importScripts)"#,
        r#"class K{eval(){return 1}importScripts=2}console.log(new K().eval(),new K().importScripts)"#,
        r#"const w={importScripts:()=>1};console.log(typeof w.importScripts,w["eval"])"#,
        r#"const{eval:ev}={eval:5};console.log(ev)"#,
        r#"const{importScripts:load}={importScripts:5};console.log(load)"#,
        r#"let ev;({eval:ev}={eval:5});console.log(ev)"#,
    ] {
        let (temp, sub, report) = syntax_boundary_case(snippet);
        assert_ne!(out(temp.path(), "sub.js"), sub, "{snippet}: {report:#}");
        assert!(saved(&report) > 0, "{snippet}: {report:#}");
    }
}

/// Module import and export names such as `import { eval as helper }` and
/// `export { helper as eval }` name an export, not the global `eval`, so
/// their chunks are optimized.
#[test]
fn optimizes_chunks_that_name_module_exports_eval_or_import_scripts() {
    let lib = r#"function pick(){return{verticalSpacing:1,horizontalSpacing:2}}function run(){const p=pick();return p.verticalSpacing+p.horizontalSpacing}export{run as eval,run as importScripts};"#;
    let sub = r#"import{eval as helper,importScripts as other}from"./lib.js";export{eval as again,importScripts as more}from"./lib.js";function own(n){return{leftSpacing:n,rightSpacing:n}}function wrapped(){const o=own(2);return o.leftSpacing+o.rightSpacing}export{wrapped as eval,wrapped as importScripts};console.log(helper(),other(),wrapped());"#;
    let namespace = r#"function own(n){return{upperSpacing:n,lowerSpacing:n}}function total(){const o=own(3);return o.upperSpacing+o.lowerSpacing}export{total};export*as eval from"./lib.js";"#;
    let (temp, report) = run(&[
        (
            "index.js",
            r#"import"./sub.js";import"./namespace.js";console.log("index");"#,
        ),
        ("lib.js", lib),
        ("sub.js", sub),
        ("namespace.js", namespace),
    ]);
    for (chunk, original) in [
        ("sub.js", sub),
        ("lib.js", lib),
        ("namespace.js", namespace),
    ] {
        assert_ne!(out(temp.path(), chunk), original, "{chunk}: {report:#}");
    }
}

/// A call or an alias of the global `eval` or `importScripts` keeps the
/// chunk unchanged while the rest of the bundle is optimized.
#[test]
fn keeps_chunks_that_use_eval_or_import_scripts_as_values() {
    for snippet in [
        r#"const e=eval;console.log(e("1+1"))"#,
        r#"console.log(eval("1+1"))"#,
        r#"console.log(globalThis.eval("1+1"))"#,
        r#"console.log(globalThis["eval"]("1+1"))"#,
        r#"const o={eval};console.log(typeof o.eval)"#,
        r#"globalThis.__never&&importScripts("a.js")"#,
        r#"globalThis.__never&&self.importScripts("a.js")"#,
        r#"globalThis.__never&&globalThis.importScripts("a.js")"#,
        r#"const f=typeof importScripts=="function"?importScripts:null;console.log(f)"#,
        r#"const f=globalThis.importScripts;console.log(f)"#,
        r#"typeof importScripts=="function"&&importScripts("vendor.js")"#,
        r#"function f(){const{importScripts:load}=self;return load("a.js")}globalThis.__never&&f()"#,
        r#"const{importScripts:load}=(globalThis);console.log(load)"#,
        r#"globalThis.__never&&(importScripts)("a.js")"#,
        r#"console.log((0,eval)("1+1"))"#,
        r#"const{eval:ev}=globalThis;console.log(ev("1+1"))"#,
        r#"let load;({importScripts:load}=globalThis);globalThis.__never&&load("a.js")"#,
    ] {
        let (temp, sub, report) = syntax_boundary_case(snippet);
        assert_eq!(out(temp.path(), "sub.js"), sub, "{snippet}: {report:#}");
        assert!(saved(&report) > 0, "{snippet}: {report:#}");
    }
}

/// Debug logging names each chunk's own rule before boundary reach, while
/// frozen-only chunks explain why they remain unchanged.
#[test]
fn logs_the_rule_that_makes_a_chunk_a_boundary() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import"./vendor.js";import"./classic.js";import"./loading.js";console.log(1);"#,
        ),
        (
            "vendor.js",
            r#"import"./evaluating.js";import"./reached.js";console.log(2);"#,
        ),
        (
            "evaluating.js",
            r#"const e=eval;console.log(e("1"));export{};"#,
        ),
        ("reached.js", r#"console.log(3);export{};"#),
        ("classic.js", r#"console.log(4);"#),
        (
            "loading.js",
            r#"globalThis.__never&&self.importScripts("a.js");export{};"#,
        ),
    ]);
    fs::write(
        temp.path().join("dist/index.html"),
        r#"<script src="classic.js"></script><script type="module" src="index.js"></script>"#,
    )
    .unwrap();
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "vendor.js"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let log = String::from_utf8_lossy(&output.stderr);
    let line = |chunk: &str| {
        log.lines()
            .find(|l| l.contains("bundle chunk is a boundary") && l.contains(chunk))
            .unwrap_or_else(|| panic!("no boundary log for {chunk}: {log}"))
            .to_string()
    };
    assert!(line("evaluating.js").contains("eval-reference"), "{log}");
    assert!(line("reached.js").contains("boundary-reachable"), "{log}");
    assert!(
        line("vendor.js").contains("excluded-or-classic-script"),
        "{log}"
    );
    assert!(
        line("classic.js").contains("excluded-or-classic-script"),
        "{log}"
    );
    assert!(line("loading.js").contains("import-scripts-call"), "{log}");
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    let frozen = report["frozen_chunks"].as_array().unwrap();
    assert_eq!(frozen.len(), 2, "{report:#}");
    let reached = frozen
        .iter()
        .find(|chunk| chunk["file"].as_str().unwrap().ends_with("reached.js"))
        .unwrap();
    assert!(reached["boundary"].as_str().unwrap().ends_with("vendor.js"));
    assert_eq!(reached["reason"], "boundary_reachable");
    for name in ["vendor.js", "classic.js", "evaluating.js", "reached.js"] {
        assert_eq!(
            fs::read(temp.path().join("dist").join(name)).unwrap(),
            fs::read(temp.path().join("out").join(name)).unwrap(),
            "{name}"
        );
    }
    same_behavior(temp.path());
}

/// A `with` statement keeps its chunk unchanged. Modules are strict code, so
/// the chunk is not run by Node here.
#[test]
fn keeps_chunks_with_with_statements_unchanged() {
    let with = r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}with({a:1}){own(a)}export{};"#;
    let temp = bundle(&[
        (
            "index.js",
            r#"function mine(n){return{firstOwnValue:n,secondOwnValue:n}}const m=mine(1);console.log(m.firstOwnValue+m.secondOwnValue);export{};"#,
        ),
        ("with.js", with),
    ]);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "with.js"), with);
    assert!(saved(&report) > 0, "{report:#}");
}

/// A chunk that uses `import.meta` is an ES module even without imports or
/// exports.
#[test]
fn optimizes_import_meta_chunks() {
    let temp = bundle(&[
        (
            "index.js",
            r#"const u=new URL("./worker.js",import.meta.url);console.log(u.pathname.endsWith("worker.js"));"#,
        ),
        (
            "worker.js",
            r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing,typeof import.meta.url);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    let worker = out(temp.path(), "worker.js");
    assert!(!worker.contains("verticalSpacing"), "{worker}");
    assert_eq!(
        node(&temp.path().join("dist/worker.js")).stdout,
        node(&temp.path().join("out/worker.js")).stdout
    );
}

/// A standalone `.mjs` chunk is an ES module even without imports or exports.
#[test]
fn optimizes_standalone_mjs_chunks() {
    let temp = bundle(&[
        (
            "index.js",
            r#"const u=new URL("./worker.mjs",import.meta.url);console.log(u.pathname.endsWith("worker.mjs"));"#,
        ),
        (
            "worker.mjs",
            r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    let worker = out(temp.path(), "worker.mjs");
    assert!(!worker.contains("verticalSpacing"), "{worker}");
    assert_eq!(
        node(&temp.path().join("dist/worker.mjs")).stdout,
        node(&temp.path().join("out/worker.mjs")).stdout
    );
}

/// Minified chunks put several imports on one line; module initialization
/// of every imported chunk must survive optimization.
#[test]
fn keeps_initialization_of_every_import_on_a_minified_line() {
    run(&[
        (
            "index.js",
            r#"import{a as x}from"./a.js";import{b as y}from"./b.js";import"./c.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing,x(),typeof y);"#,
        ),
        (
            "a.js",
            r#"console.log("init a");function a(){return 1}export{a};"#,
        ),
        (
            "b.js",
            r#"console.log("init b");function b(){return 2}export{b};"#,
        ),
        ("c.js", r#"console.log("init c");"#),
    ]);
}

/// A classic `importScripts` worker stays in the program as a boundary: an
/// export it reaches through `import()` survives.
#[test]
fn keeps_exports_that_classic_workers_reach() {
    let worker = r#"typeof importScripts=="function"&&importScripts("vendor.js");import("./shared.js").then(m=>console.log(m.workerOnly()));"#;
    let temp = bundle(&[
        (
            "index.js",
            r#"import{used as u}from"./shared.js";console.log(u());"#,
        ),
        (
            "shared.js",
            r#"function used(){return"main"}function workerOnly(){return"worker"}export{used,workerOnly};"#,
        ),
        ("sw.js", worker),
    ]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "sw.js"), worker);
    assert_eq!(
        node(&temp.path().join("dist/sw.js")).stdout,
        node(&temp.path().join("out/sw.js")).stdout
    );
    // A bundle with only a classic worker is a valid, unchanged result.
    let only = bundle(&[("sw.js", worker)]);
    optimize(only.path(), &[]);
    assert_eq!(out(only.path(), "sw.js"), worker);
}

/// A script that a worker loads through any callee form of `importScripts`
/// is a classic script, even when another chunk also imports it statically.
#[test]
fn keeps_scripts_loaded_by_every_import_scripts_callee_form() {
    let vendor = r#"function legacyHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var legacyState=legacyHelper();"#;
    for call in [
        r#"importScripts("./vendor.js")"#,
        r#"self.importScripts("./vendor.js")"#,
        r#"self["importScripts"]("./vendor.js")"#,
        r#"globalThis[`importScripts`]("./vendor.js")"#,
        r#"(self.importScripts)("./vendor.js")"#,
        r#"(self["importScripts"])("./vendor.js")"#,
    ] {
        let worker = format!("globalThis.__never&&{call};");
        let temp = bundle(&[
            (
                "index.js",
                r#"import"./vendor.js";function mine(n){return{firstOwnValue:n,secondOwnValue:n}}const m=mine(1);console.log(m.firstOwnValue+m.secondOwnValue);"#,
            ),
            ("vendor.js", vendor),
            ("sw.js", &worker),
        ]);
        let report = optimize(temp.path(), &[]);
        same_behavior(temp.path());
        assert_eq!(out(temp.path(), "vendor.js"), vendor, "{call}: {report:#}");
        assert_eq!(out(temp.path(), "sw.js"), worker, "{call}");
    }
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

/// Follows `sourceMappingURL`: inline `data:` maps are composed in place and
/// a map in another directory is composed where it is.
#[test]
fn composes_inline_and_referenced_maps() {
    let code = r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=make(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#;
    let map = |file: &str| {
        let mut builder = sourcemap::SourceMapBuilder::new(Some(file));
        let id = builder.add_source("src/main.ts");
        builder.set_source_contents(id, Some(code));
        let at = code.find("console").unwrap() as u32;
        builder.add(0, at, 0, at, Some("src/main.ts"), None, false);
        builder.into_sourcemap()
    };
    let inline = format!(
        "{code}\n//# sourceMappingURL={}",
        map("inline.js").to_data_url().unwrap()
    );
    let mut external = Vec::new();
    map("external.js").to_writer(&mut external).unwrap();
    let mut rooted = Vec::new();
    map("rooted.js").to_writer(&mut rooted).unwrap();
    let temp = bundle(&[
        (
            "index.js",
            r#"import"./inline.js";import"./external.js";import"./rooted.js";"#,
        ),
        ("inline.js", &inline),
        (
            "external.js",
            &format!("{code}\n//# sourceMappingURL=maps/ext%65rnal.map?v=1"),
        ),
        ("maps/external.map", std::str::from_utf8(&external).unwrap()),
        // Root-relative, below a deployment base path.
        (
            "rooted.js",
            &format!("{code}\n//# sourceMappingURL=/app/maps/rooted.map"),
        ),
        ("maps/rooted.map", std::str::from_utf8(&rooted).unwrap()),
    ]);
    let report = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    // size.after counts the published chunk code; inline source maps are
    // metadata, like separate map files.
    let published: u64 = ["index.js", "inline.js", "external.js", "rooted.js"]
        .iter()
        .map(|c| {
            let text = fs::read_to_string(temp.path().join("out").join(c)).unwrap();
            let code = match text.find("//# sourceMappingURL=") {
                Some(at) => &text[..at],
                None => &text[..],
            };
            code.len() as u64
        })
        .sum();
    assert_eq!(report["size"]["after"].as_u64(), Some(published));
    let console_maps_to_console = |js: &str, map: &sourcemap::SourceMap| {
        let token = map.tokens().next().expect("one token");
        let at = token.get_dst_col() as usize;
        assert!(js[at..].starts_with("console"), "{js}");
        assert_eq!(token.get_src_col() as usize, code.find("console").unwrap());
    };
    let edited = out(temp.path(), "inline.js");
    assert!(
        !edited.contains("verticalSpacing"),
        "the fixture must be optimized"
    );
    let url = edited.rsplit("sourceMappingURL=").next().unwrap().trim();
    let sourcemap::DecodedMap::Regular(decoded) = sourcemap::decode_data_url(url).unwrap() else {
        panic!("regular map")
    };
    console_maps_to_console(&edited, &decoded);
    let edited = out(temp.path(), "external.js");
    let composed = sourcemap::SourceMap::from_slice(
        &fs::read(temp.path().join("out/maps/external.map")).unwrap(),
    )
    .unwrap();
    console_maps_to_console(&edited, &composed);
    let edited = out(temp.path(), "rooted.js");
    let composed = sourcemap::SourceMap::from_slice(
        &fs::read(temp.path().join("out/maps/rooted.map")).unwrap(),
    )
    .unwrap();
    console_maps_to_console(&edited, &composed);
}

/// A remote or missing map cannot be composed, so an edited chunk drops its
/// reference; an unedited chunk keeps it.
#[test]
fn drops_remote_map_references_of_edited_chunks() {
    let code = r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=make(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#;
    let untouched = "console.log(1);\n//# sourceMappingURL=https://maps.example/untouched.js.map";
    let temp = bundle(&[
        (
            "index.js",
            r#"import"./edited.js";import"./untouched.js";import"./literal.js";import"./missing.js";"#,
        ),
        (
            "edited.js",
            &format!("{code}\n/*# sourceMappingURL=//maps.example/edited.js.map */"),
        ),
        // A local reference to a missing map cannot be composed either.
        (
            "missing.js",
            &format!("{code}\n//# sourceMappingURL=maps/missing.js.map"),
        ),
        // Directive-like text in a string is code, not a map reference.
        (
            "literal.js",
            &format!(
                "{code}const marker=\"//# sourceMappingURL=https://maps.example/x.map\";console.log(marker.length);"
            ),
        ),
        ("untouched.js", untouched),
    ]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    let edited = out(temp.path(), "edited.js");
    assert!(
        !edited.contains("verticalSpacing"),
        "the fixture must be optimized"
    );
    assert!(!edited.contains("sourceMappingURL"), "{edited}");
    assert_eq!(out(temp.path(), "untouched.js"), untouched);
    assert!(out(temp.path(), "literal.js").contains("maps.example/x.map\";"));
    let missing = out(temp.path(), "missing.js");
    assert!(
        !missing.contains("verticalSpacing"),
        "the fixture must be optimized"
    );
    assert!(!missing.contains("sourceMappingURL"), "{missing}");
}

#[test]
fn drops_directives_of_malformed_local_maps_of_edited_chunks() {
    let code = r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=make(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#;
    let unsupported = r#"{"version":3,"sections":[]}"#;
    let chunks = [
        (
            "index.js",
            r#"import"./explicit.js";import"./hidden.js";import"./unsupported.js";"#,
        ),
        (
            "explicit.js",
            &format!("{code}\n//# sourceMappingURL=explicit.js.map"),
        ),
        ("explicit.js.map", "{bad"),
        (
            "unsupported.js",
            &format!("{code}\n/*# sourceMappingURL=maps/unsupported.map */"),
        ),
        ("maps/unsupported.map", unsupported),
        // A hidden adjacent map has no directive to remove or add.
        ("hidden.js", &format!("{code}\n")),
        ("hidden.js.map", "{bad"),
    ];
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    for chunk in ["explicit.js", "unsupported.js", "hidden.js"] {
        let text = out(temp.path(), chunk);
        assert!(
            !text.contains("verticalSpacing"),
            "{chunk} must be optimized"
        );
        assert!(!text.contains("sourceMappingURL"), "{chunk}: {text}");
    }
    let published = temp.path().join("out");
    assert!(!published.join("explicit.js.map").exists());
    assert!(!published.join("maps/unsupported.map").exists());
    assert!(!published.join("hidden.js.map").exists());
    // The input directory is never modified.
    let dist = temp.path().join("dist");
    for (name, content) in &chunks[1..] {
        if name.ends_with(".map") {
            assert_eq!(fs::read_to_string(dist.join(name)).unwrap(), *content);
        }
    }
    assert!(
        fs::read_to_string(dist.join("explicit.js"))
            .unwrap()
            .contains("sourceMappingURL=explicit.js.map")
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
    // New hashes use the full base64url alphabet like the bundler's own.
    let hash = &entry["index-".len()..entry.len() - ".js".len()];
    assert_eq!(hash.len(), 8);
    assert!(
        hash.bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    );
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

/// Rollup's `[name].[hash].js` pattern (used by Outline) is renamed too.
#[test]
fn rehashes_dot_separated_hash_names() {
    let chunks: Vec<(&str, String)> = HASHED
        .iter()
        .map(|(name, code)| {
            let rename = |s: &str| {
                s.replace("-AAAAAAAA", ".AAAAAAAA")
                    .replace("-BBBBBBBB", ".BBBBBBBB")
                    .replace("-CCCCCCCC", ".CCCCCCCC")
            };
            (*name, rename(code))
        })
        .collect();
    let named: Vec<(String, String)> = chunks
        .iter()
        .map(|(name, code)| {
            (
                name.replace("-AAAAAAAA", ".AAAAAAAA")
                    .replace("-BBBBBBBB", ".BBBBBBBB")
                    .replace("-CCCCCCCC", ".CCCCCCCC"),
                code.clone(),
            )
        })
        .collect();
    let borrowed: Vec<(&str, &str)> = named
        .iter()
        .map(|(n, c)| (n.as_str(), c.as_str()))
        .collect();
    let temp = bundle(&borrowed);
    optimize(temp.path(), &[]);
    let assets = listing(&temp.path().join("out/assets"));
    assert!(
        assets.contains(&"plain.CCCCCCCC.js".to_owned()),
        "{assets:?}"
    );
    assert!(
        !assets
            .iter()
            .any(|a| a.contains("BBBBBBBB") || a.contains("AAAAAAAA")),
        "{assets:?}"
    );
    let entry = assets.iter().find(|a| a.starts_with("index.")).unwrap();
    let html = fs::read_to_string(temp.path().join("out/index.html")).unwrap();
    assert!(html.contains(&format!("/assets/{entry}")), "{html}");
    let after = node(&temp.path().join("out/assets").join(entry));
    assert!(
        after.status.success(),
        "{}",
        String::from_utf8_lossy(&after.stderr)
    );
}

/// An importer's new name covers the final content of what it imports: a
/// different leaf edit renames the unchanged importer differently.
#[test]
fn importer_names_follow_dependency_content() {
    let chunks = [
        (
            "index.html",
            r#"<script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/index-AAAAAAAA.js",
            r#"import{m as e}from"./leaf-BBBBBBBB.js";console.log(e(2));"#,
        ),
        (
            "assets/leaf-BBBBBBBB.js",
            r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n*2}}function m(n){const o=make(n);return o.verticalSpacing+o.horizontalSpacing}function unusedHelperThatIsNeverCalled(n){return n.toString(36).repeat(3)}export{m,unusedHelperThatIsNeverCalled as u};"#,
        ),
    ];
    let names = |extra: &[&str]| {
        let temp = bundle(&chunks);
        optimize(temp.path(), extra);
        let assets = listing(&temp.path().join("out/assets"));
        let index = assets
            .iter()
            .find(|a| a.starts_with("index-"))
            .unwrap()
            .clone();
        let leaf = assets
            .iter()
            .find(|a| a.starts_with("leaf-"))
            .unwrap()
            .clone();
        let entry = node(&temp.path().join("out/assets").join(&index));
        assert!(
            entry.status.success(),
            "{}",
            String::from_utf8_lossy(&entry.stderr)
        );
        (index, leaf)
    };
    let all = names(&[]);
    let without_removal = names(&["--disable-pass", "residual-dependency-removal"]);
    assert_ne!(all.1, without_removal.1, "the leaf differs");
    assert_ne!(
        all.0, without_removal.0,
        "so its importer is renamed differently"
    );
}

/// Hashed files that share a name in different directories are renamed
/// together from the content of all of them, so an edit to either one
/// changes the shared name.
#[test]
fn same_named_hashed_files_follow_every_content() {
    let chunks = [
        (
            "index.js",
            r#"import{m as e}from"./a/x-AAAAAAAA.js";import{p as t}from"./b/x-AAAAAAAA.js";console.log(e(2),t());"#,
        ),
        (
            "a/x-AAAAAAAA.js",
            r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n*2}}function m(n){const o=make(n);return o.verticalSpacing+o.horizontalSpacing}function unusedHelperThatIsNeverCalled(n){return n.toString(36).repeat(3)}export{m,unusedHelperThatIsNeverCalled as u};"#,
        ),
        (
            "b/x-AAAAAAAA.js",
            r#"function p(){return"plain"}export{p};"#,
        ),
    ];
    let names = |extra: &[&str]| {
        let temp = bundle(&chunks);
        optimize(temp.path(), extra);
        same_behavior(temp.path());
        let a = listing(&temp.path().join("out/a"));
        let b = listing(&temp.path().join("out/b"));
        assert_eq!(a, b, "references cannot tell the two files apart");
        a
    };
    let all = names(&[]);
    let without_removal = names(&["--disable-pass", "residual-dependency-removal"]);
    assert!(!all[0].contains("AAAAAAAA"));
    assert_ne!(all, without_removal);
}

/// A content-hashed JSON asset that references a renamed chunk is rewritten,
/// so it gets a new hash too, and its own referrers follow.
#[test]
fn rehashes_rewritten_hashed_text_assets() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<link rel="manifest" href="/assets/manifest-DDDD1111.json"><script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    );
    chunks.push((
        "assets/manifest-DDDD1111.json",
        r#"{"entry":"/assets/index-AAAAAAAA.js","settings":"/assets/settings-BBBBBBBB.js"}"#,
    ));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let assets = listing(&temp.path().join("out/assets"));
    let manifest = assets.iter().find(|a| a.starts_with("manifest-")).unwrap();
    assert_ne!(manifest, "manifest-DDDD1111.json", "{assets:?}");
    let html = fs::read_to_string(temp.path().join("out/index.html")).unwrap();
    assert!(html.contains(&format!("/assets/{manifest}")), "{html}");
    let json = fs::read_to_string(temp.path().join("out/assets").join(manifest)).unwrap();
    for name in json.split('"').filter(|p| p.starts_with("/assets/")) {
        assert!(
            assets.contains(&name["/assets/".len()..].to_owned()),
            "{json}"
        );
    }
}

/// A CSS asset that spells `url( name )` with whitespace still follows the
/// renamed chunk it references, and its own hash changes.
#[test]
fn rewrites_spaced_css_url_references() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<link rel="stylesheet" href="/assets/style-FFFF1111.css"><script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    );
    chunks.push((
        "assets/style-FFFF1111.css",
        "@import url( /assets/index-AAAAAAAA.js );a{b:url(/assets/settings-BBBBBBBB.js)}c{d:url( https://cdn.example/assets/index-AAAAAAAA.js )}",
    ));
    let run = || {
        let temp = bundle(&chunks);
        optimize(temp.path(), &[]);
        let assets = listing(&temp.path().join("out/assets"));
        let style = assets
            .iter()
            .find(|a| a.starts_with("style-"))
            .unwrap()
            .clone();
        let css = fs::read_to_string(temp.path().join("out/assets").join(&style)).unwrap();
        (assets, style, css)
    };
    let (assets, style, css) = run();
    assert_ne!(style, "style-FFFF1111.css", "{assets:?}");
    assert!(!css.contains("url( /assets/index-AAAAAAAA.js )"), "{css}");
    // The external URL keeps its name.
    assert!(
        css.contains("url( https://cdn.example/assets/index-AAAAAAAA.js )"),
        "{css}"
    );
    for part in css.split("url(").skip(1).take(2) {
        let name = part
            .trim_start()
            .split([' ', ')'])
            .next()
            .unwrap()
            .trim_start_matches("/assets/");
        assert!(assets.contains(&name.to_owned()), "{name} {assets:?}");
    }
    // Repeated runs publish the same names and bytes.
    assert_eq!(run(), (assets, style, css));
}

/// A reference spelled with escapes cannot be rewritten: the original chunk
/// (and what it imports) stays under its old name next to the renamed one.
#[test]
fn retains_chunks_that_encoded_references_name() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<p>&copy; 2026</p><script type="module" src="/assets/index&#45;AAAAAAAA.js"></script>"#,
    );
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let assets = listing(&temp.path().join("out/assets"));
    // The encoded page still loads the complete original graph.
    for original in ["index-AAAAAAAA.js", "settings-BBBBBBBB.js"] {
        assert!(assets.contains(&original.to_owned()), "{assets:?}");
        assert_eq!(
            out(temp.path(), &format!("assets/{original}")),
            fs::read_to_string(temp.path().join("dist/assets").join(original)).unwrap()
        );
    }
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = node(&temp.path().join("out/assets/index-AAAAAAAA.js"));
    assert_eq!(before.stdout, after.stdout);
    // The renamed, optimized graph exists as well.
    assert!(
        assets
            .iter()
            .any(|a| a.starts_with("index-") && !a.contains("AAAAAAAA"))
    );
}

/// A classic chunk is never edited by a pass, but its references follow
/// renamed chunks like those of any text asset, so no original is kept.
#[test]
fn rewrites_references_of_classic_boundary_chunks() {
    let legacy = r#"var settingsUrl="./settings-BBBBBBBB.js";var legacyState={counter:0};"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script src="/assets/legacy-EEEE1111.js"></script><script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    );
    chunks.push(("assets/legacy-EEEE1111.js", legacy));
    let temp = bundle(&chunks);
    let report = optimize(temp.path(), &[]);
    assert_eq!(report["size"]["retained"], 0, "{report:#}");
    let assets = listing(&temp.path().join("out/assets"));
    assert!(!assets.iter().any(|a| a.contains("BBBBBBBB")), "{assets:?}");
    let settings = assets
        .iter()
        .find(|a| a.starts_with("settings-"))
        .unwrap()
        .clone();
    // The classic chunk keeps its code with the new name in its string, and
    // gets a new hash itself; the page follows.
    let renamed_legacy = assets
        .iter()
        .find(|a| a.starts_with("legacy-"))
        .unwrap()
        .clone();
    assert_ne!(renamed_legacy, "legacy-EEEE1111.js");
    assert_eq!(
        out(temp.path(), &format!("assets/{renamed_legacy}")),
        legacy.replace("settings-BBBBBBBB.js", &settings)
    );
    assert!(out(temp.path(), "index.html").contains(&renamed_legacy));
}

/// An excluded chunk stays byte-for-byte unchanged: a string reference to an
/// edited chunk keeps the old name, whose original is kept.
#[test]
fn keeps_excluded_chunks_and_their_original_dependencies() {
    let legacy = r#"var settingsUrl="./settings-BBBBBBBB.js";var legacyState={counter:0};"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script src="/assets/legacy-EEEE1111.js"></script><script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    );
    chunks.push(("assets/legacy-EEEE1111.js", legacy));
    let temp = bundle(&chunks);
    let report = optimize(temp.path(), &["--bundle-exclude", "legacy-"]);
    assert!(
        report["size"]["retained"].as_u64().unwrap() > 0,
        "{report:#}"
    );
    let assets = listing(&temp.path().join("out/assets"));
    assert_eq!(out(temp.path(), "assets/legacy-EEEE1111.js"), legacy);
    assert_eq!(
        out(temp.path(), "assets/settings-BBBBBBBB.js"),
        fs::read_to_string(temp.path().join("dist/assets/settings-BBBBBBBB.js")).unwrap()
    );
    assert!(
        assets
            .iter()
            .any(|a| a.starts_with("settings-") && !a.contains("BBBBBBBB"))
    );
}

/// A name that an excluded chunk carries is never renamed, even for an
/// edited chunk elsewhere that shares it.
#[test]
fn keeps_names_shared_with_excluded_chunks() {
    let legacy = r#"var legacyState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="/legacy/x-AAAAAAAA.js"></script><script type="module" src="/index.js"></script>"#,
        ),
        ("legacy/x-AAAAAAAA.js", legacy),
        (
            "modern/x-AAAAAAAA.js",
            r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n*2}}function m(n){const o=make(n);return o.verticalSpacing+o.horizontalSpacing}export{m};"#,
        ),
        (
            "index.js",
            r#"import{m}from"./modern/x-AAAAAAAA.js";console.log(m(2));"#,
        ),
    ]);
    optimize(temp.path(), &["--bundle-exclude", "legacy/"]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "legacy/x-AAAAAAAA.js"), legacy);
    assert_eq!(listing(&temp.path().join("out/modern")), ["x-AAAAAAAA.js"]);
}

/// Without exclusion, files that share a name with an edited chunk are
/// renamed together with it, classic scripts included.
#[test]
fn renames_classic_chunks_that_share_an_edited_name() {
    let legacy = r#"var legacyState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="/legacy/x-AAAAAAAA.js"></script><script type="module" src="/index.js"></script>"#,
        ),
        ("legacy/x-AAAAAAAA.js", legacy),
        (
            "modern/x-AAAAAAAA.js",
            r#"function make(n){return{verticalSpacing:n,horizontalSpacing:n*2}}function m(n){const o=make(n);return o.verticalSpacing+o.horizontalSpacing}export{m};"#,
        ),
        (
            "index.js",
            r#"import{m}from"./modern/x-AAAAAAAA.js";console.log(m(2));"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    let legacy_files = listing(&temp.path().join("out/legacy"));
    let modern_files = listing(&temp.path().join("out/modern"));
    assert_eq!(legacy_files, modern_files);
    assert_ne!(legacy_files, ["x-AAAAAAAA.js"]);
    assert_eq!(
        out(temp.path(), &format!("legacy/{}", legacy_files[0])),
        legacy
    );
    assert!(out(temp.path(), "index.html").contains(&format!("/legacy/{}", legacy_files[0])));
}

/// JavaScript escapes in a reference also keep the original, with its map.
#[test]
fn retains_escaped_references_with_their_maps() {
    let mut map = Vec::new();
    let mut builder = sourcemap::SourceMapBuilder::new(Some("settings-BBBBBBBB.js"));
    builder.add(0, 0, 0, 0, Some("src/settings.ts"), None, false);
    builder.into_sourcemap().to_writer(&mut map).unwrap();
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push((
        "assets/loader.js",
        r#"export const settingsUrl="./settings\u002dBBBBBBBB.js";"#,
    ));
    chunks.push((
        "assets/settings-BBBBBBBB.js.map",
        std::str::from_utf8(&map).unwrap(),
    ));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let original = |p: &str| fs::read(temp.path().join("dist/assets").join(p)).unwrap();
    let published = |p: &str| fs::read(temp.path().join("out/assets").join(p)).unwrap();
    assert_eq!(
        published("settings-BBBBBBBB.js"),
        original("settings-BBBBBBBB.js")
    );
    assert_eq!(
        published("settings-BBBBBBBB.js.map"),
        original("settings-BBBBBBBB.js.map")
    );
}

/// Check mode stages as well, so it reports retained code like output mode.
#[test]
fn check_mode_reports_the_published_size() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script type="module" src="/assets/index&#45;AAAAAAAA.js"></script>"#,
    );
    let output = bundle(&chunks);
    let published = optimize(output.path(), &[]);
    let check = bundle(&chunks);
    let checked = Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(check.path())
        .args(["--bundle", "dist", "--check"])
        .output()
        .unwrap();
    assert!(
        checked.status.success(),
        "{}",
        String::from_utf8_lossy(&checked.stderr)
    );
    let checked: Value = serde_json::from_slice(&checked.stdout).unwrap();
    assert_eq!(checked["size"], published["size"]);
    assert!(
        published["size"]["retained"].as_u64().unwrap() > 0,
        "{published:#}"
    );
}

/// A renamed chunk's adjacent map follows it and names the new file.
#[test]
fn renamed_maps_name_their_new_chunk() {
    let mut builder = sourcemap::SourceMapBuilder::new(Some("settings-BBBBBBBB.js"));
    builder.add(0, 0, 0, 0, Some("src/settings.ts"), None, false);
    let mut map = Vec::new();
    builder.into_sourcemap().to_writer(&mut map).unwrap();
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push((
        "assets/settings-BBBBBBBB.js.map",
        std::str::from_utf8(&map).unwrap(),
    ));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let assets = listing(&temp.path().join("out/assets"));
    let chunk = assets
        .iter()
        .find(|a| a.starts_with("settings-") && a.ends_with(".js"))
        .unwrap();
    let map = sourcemap::SourceMap::from_slice(
        &fs::read(temp.path().join("out/assets").join(format!("{chunk}.map"))).unwrap(),
    )
    .unwrap();
    assert_eq!(map.get_file(), Some(chunk.as_str()));
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
    let inside = bundle(&[("index.js", "console.log(1);")]);
    let output = Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(inside.path())
        .args(["--bundle", "dist", "--out-dir", "dist/optimized"])
        .output()
        .unwrap();
    assert!(!output.status.success());
    assert!(!inside.path().join("dist/optimized").exists());
    // Chunks with TypeScript-only syntax are not JavaScript.
    let typed = bundle(&[("index.js", "const a={b:1};console.log(a!.b);")]);
    assert!(!run_typeopt(typed.path(), &[]).status.success());
    let empty = bundle(&[("index.html", "<p>")]);
    assert!(!run_typeopt(empty.path(), &[]).status.success());
}

#[test]
fn rejects_pages_with_subresource_integrity() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module" src="./index.js" integrity="sha384-x"></script>"#,
        ),
        (
            "index.js",
            r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    let output = run_typeopt(temp.path(), &[]);
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("Subresource Integrity"));
    assert!(!temp.path().join("out").exists());
}

#[test]
fn rejects_any_spelling_of_subresource_integrity() {
    for html in [
        r#"<SCRIPT TYPE="module" SRC="./index.js" INTEGRITY = "sha384-x"></SCRIPT>"#,
        r#"<link rel=modulepreload href=./index.js integrity=sha384-x>"#,
        r#"<meta http-equiv="Content-Security-Policy" content="script-src 'self' 'SHA256-abc='"><script type="module" src="./index.js"></script>"#,
    ] {
        let temp = bundle(&[
            ("index.html", html),
            (
                "index.js",
                r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
            ),
        ]);
        let output = run_typeopt(temp.path(), &[]);
        assert!(!output.status.success(), "{html}");
        // Check mode is a preflight for the same blocker.
        let check = Command::new(env!("CARGO_BIN_EXE_typeopt"))
            .current_dir(temp.path())
            .args(["--bundle", "dist", "--check"])
            .output()
            .unwrap();
        assert!(!check.status.success(), "{html}");
    }
}

/// HTML module scripts are matched by resolved path, not by file name: a
/// classic script that shares a module's file name stays unchanged.
#[test]
fn matches_html_module_scripts_by_path() {
    let legacy = r#"function legacyGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var legacyState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="/legacy/index.js"></script><script type="module" src="/app/modern/index.js"></script>"#,
        ),
        ("legacy/index.js", legacy),
        (
            "modern/index.js",
            r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "legacy/index.js"), legacy);
    assert!(!out(temp.path(), "modern/index.js").contains("verticalSpacing"));
}

/// The report is part of the result: when it cannot be delivered, nothing
/// is published and no staging directory remains.
#[test]
fn publishes_nothing_when_the_report_cannot_be_written() {
    let temp = bundle(&[(
        "index.js",
        r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
    )]);
    let mut child = Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(temp.path())
        .args(["--bundle", "dist", "--out-dir", "out"])
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::null())
        .spawn()
        .unwrap();
    // Close the read end before the report is written.
    drop(child.stdout.take());
    let status = child.wait().unwrap();
    assert!(!status.success());
    let leftovers: Vec<_> = fs::read_dir(temp.path())
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .collect();
    assert_eq!(leftovers, ["dist"], "{leftovers:?}");
}

/// Relative module script URLs resolve against the page's `<base href>`.
#[test]
fn resolves_module_scripts_against_the_document_base() {
    let classic = r#"function legacyGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var legacyState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<base href="/modern/"><script src="/index.js"></script><script type="module" src="index.js"></script>"#,
        ),
        ("index.js", classic),
        (
            "modern/index.js",
            r#"function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "index.js"), classic);
    assert!(!out(temp.path(), "modern/index.js").contains("verticalSpacing"));
}

/// Symbolic links would make analysis and the staged copy disagree.
#[test]
fn rejects_bundles_with_symbolic_links() {
    let temp = bundle(&[("real.js", "console.log(1);")]);
    std::os::unix::fs::symlink("real.js", temp.path().join("dist/alias.js")).unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("symbolic links"));
    assert!(!temp.path().join("out").exists());
}

/// A chunk that a page loads as a classic script stays a boundary even when
/// a module also imports it.
#[test]
fn keeps_chunks_that_pages_load_as_classic_scripts() {
    let shared = r#"function sharedGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var sharedState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="./shared.js"></script><script type="module" src="./index.js"></script>"#,
        ),
        ("shared.js", shared),
        (
            "index.js",
            r#"import"./shared.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "shared.js"), shared);
}

/// Character references in HTML attributes are decoded before scripts are
/// classified.
#[test]
fn decodes_character_references_in_script_urls() {
    let shared = r#"function sharedGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var sharedState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="shared&#46;js"></script><script type="module" src="./index.js"></script>"#,
        ),
        ("shared.js", shared),
        (
            "index.js",
            r#"import"./shared.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "shared.js"), shared);
    let unknown = bundle(&[
        (
            "index.html",
            r#"<script src="shared&unknownref;js"></script>"#,
        ),
        ("shared.js", shared),
    ]);
    assert!(!run_typeopt(unknown.path(), &[]).status.success());
}

/// Script URLs resolve with URL semantics: a base without a trailing slash
/// drops its last segment.
#[test]
fn resolves_script_urls_with_url_semantics() {
    let shared = r#"function sharedGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var sharedState={counter:0};"#;
    let temp = bundle(&[
        (
            "nested/page.html",
            r#"<base href="/modern"><script src="shared.js"></script>"#,
        ),
        ("shared.js", shared),
        ("nested/shared.js", "console.log(0);"),
        (
            "index.js",
            r#"import"./shared.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "shared.js"), shared);
}

/// Every JavaScript MIME type essence loads a classic script, and `.htm`
/// pages count as HTML.
#[test]
fn recognizes_legacy_script_types_and_htm_pages() {
    let shared = r#"function sharedGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var sharedState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.htm",
            r#"<script type="text/jscript; charset=utf-8" src="./shared.js"></script><script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
        ),
        ("shared.js", shared),
        (
            "assets/index-AAAAAAAA.js",
            r#"import"../shared.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "shared.js"), shared);
    let assets = listing(&temp.path().join("out/assets"));
    let page = fs::read_to_string(temp.path().join("out/index.htm")).unwrap();
    assert!(page.contains(&format!("/assets/{}", assets[0])), "{page}");
    assert!(!assets[0].contains("AAAAAAAA"), "{assets:?}");
}

/// Inline HTML scripts can import chunk exports, which stay public.
#[test]
fn keeps_exports_that_inline_scripts_import() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module">import{htmlOnly}from"./lib.js";console.log(htmlOnly());</script><script>import("./lazy.js").then(m=>m.lazyOnly())</script><script type="module" src="./index.js"></script>"#,
        ),
        (
            "lib.js",
            r#"function used(){return 1}function htmlOnly(){return 2}export{used,htmlOnly};"#,
        ),
        (
            "lazy.js",
            r#"function lazyOnly(){return 3}function unusedLazy(){return 4}export{lazyOnly,unusedLazy};"#,
        ),
        (
            "index.js",
            r#"import{used as u}from"./lib.js";console.log(u());"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert!(out(temp.path(), "lib.js").contains("htmlOnly"));
    assert!(out(temp.path(), "lazy.js").contains("lazyOnly"));
}

/// Fixture for the handler attribute tests: `index.js` uses `used` while
/// `handlerOnly` is reachable only from the HTML page; `other.js` is imported
/// by `index.js`, so its unused export is removed unless every chunk is public.
fn handler_bundle(html: &str) -> tempfile::TempDir {
    bundle(&[
        ("index.html", html),
        (
            "lib.js",
            r#"function used(){return 1}function handlerOnly(){return 2}export{used,handlerOnly};"#,
        ),
        (
            "index.js",
            r#"import{used as u}from"./lib.js";import{unrelatedExport as r}from"./other.js";console.log(u(),r());"#,
        ),
        (
            "other.js",
            r#"function unrelatedExport(){return 3}function unusedOther(){return 4}export{unrelatedExport,unusedOther};"#,
        ),
    ])
}

/// An event handler with a character reference the parser cannot decode may
/// hide an import: every chunk becomes a public entry.
#[test]
fn keeps_exports_when_a_handler_has_an_undecodable_reference() {
    for html in [
        r#"<button onclick="import&Tab;('./lib.js').then(m=>m.handlerOnly())"></button>"#,
        r#"<a href="javascript:import&Tab;('./lib.js').then(m=>m.handlerOnly())"></a>"#,
        r#"<a href="java&Tab;script:import('./lib.js').then(m=>m.handlerOnly())"></a>"#,
        r#"<a href="&Tab;JavaScript:void 0"></a>"#,
        r#"<form action="javascript:import&Tab;('./lib.js').then(m=>m.handlerOnly())"></form>"#,
        r#"<iframe src="javascript:import&Tab;('./lib.js').then(m=>m.handlerOnly())"></iframe>"#,
        r#"<a href="java&#115;cript:import&Tab;('./lib.js').then(m=>m.handlerOnly())"></a>"#,
        r#"<a href="javascript&colon;import&Tab;('./lib.js').then(m=>m.handlerOnly())"></a>"#,
    ] {
        let temp = handler_bundle(html);
        optimize(temp.path(), &[]);
        assert!(out(temp.path(), "lib.js").contains("handlerOnly"), "{html}");
        assert!(
            out(temp.path(), "other.js").contains("unusedOther"),
            "{html}"
        );
    }
}

/// An internal space or control character other than TAB, LF and CR keeps a
/// value from being a `javascript:` URL, so it does not broaden the public
/// entries; leading spaces and controls are still trimmed by the browser.
#[test]
fn preserves_internal_spaces_when_detecting_javascript_urls() {
    for html in [
        r#"<a href="java script:&Tab;"></a>"#,
        r#"<a href="java&Tab; script:import('./lib.js')"></a>"#,
        r#"<a href="java&#1;script:&Tab;"></a>"#,
    ] {
        let temp = handler_bundle(html);
        optimize(temp.path(), &[]);
        assert!(
            !out(temp.path(), "other.js").contains("unusedOther"),
            "{html}"
        );
    }
    for html in [
        r#"<a href="  javascript:import&Tab;('./lib.js').then(m=>m.handlerOnly())"></a>"#,
        r#"<a href="&#1; javascript&Tab;:void 0"></a>"#,
    ] {
        let temp = handler_bundle(html);
        optimize(temp.path(), &[]);
        assert!(
            out(temp.path(), "other.js").contains("unusedOther"),
            "{html}"
        );
    }
}

/// A fully decoded handler keeps precise import discovery.
#[test]
fn decodes_handlers_with_known_references_precisely() {
    let temp = handler_bundle(
        r#"<button onclick="import&#9;('./lib.js').then(m=>m.handlerOnly())"></button>"#,
    );
    optimize(temp.path(), &[]);
    assert!(out(temp.path(), "lib.js").contains("handlerOnly"));
    assert!(!out(temp.path(), "other.js").contains("unusedOther"));
}

/// An undecodable reference in an attribute that cannot run code does not
/// broaden the public entries.
#[test]
fn ignores_undecodable_references_in_non_executable_attributes() {
    for html in [
        r#"<p title="&unknownref; text" data-x="&Tab;"></p>"#,
        r#"<a href="/docs&Tab;/page">docs</a>"#,
        r#"<div title="javascript:&Tab;" data-url="javascript:import&Tab;('./lib.js')"></div>"#,
        r#"<img src="javascript:&Tab;"><link href="javascript:&Tab;">"#,
    ] {
        let temp = handler_bundle(html);
        optimize(temp.path(), &[]);
        assert!(
            !out(temp.path(), "other.js").contains("unusedOther"),
            "{html}"
        );
    }
}

/// An undecodable script `type` or base `href` is still rejected.
#[test]
fn rejects_undecodable_script_type_and_base_href() {
    for html in [
        r#"<script type="mod&unknownref;ule" src="index.js"></script>"#,
        r#"<base href="/a&unknownref;/"><script src="index.js"></script>"#,
    ] {
        let temp = handler_bundle(html);
        let output = run_typeopt(temp.path(), &[]);
        assert!(!output.status.success(), "{html}");
        assert!(
            String::from_utf8_lossy(&output.stderr)
                .contains("an HTML script or base URL uses an unsupported character reference"),
            "{html}"
        );
    }
}

/// A file that calls `importScripts` can run as a classic worker even when a
/// module imports it: it stays unchanged.
#[test]
fn keeps_dual_used_classic_workers_unchanged() {
    let worker = r#"typeof importScripts=="function"&&importScripts("vendor.js");function workerGlobal(){return{verticalSpacing:1,horizontalSpacing:2}}export{};"#;
    let temp = bundle(&[
        ("index.js", r#"import"./worker.js";console.log(1);"#),
        ("worker.js", worker),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "worker.js"), worker);
}

/// A computed `import(…)` in a chunk can load any chunk: exports stay public.
#[test]
fn keeps_exports_reachable_by_computed_chunk_imports() {
    let (temp, _) = run(&[
        (
            "index.js",
            r#"import{used as u}from"./lib.js";const p="./lib.js";import(p).then(m=>console.log(u(),m.htmlOnly()));"#,
        ),
        (
            "lib.js",
            r#"function used(){return 1}function htmlOnly(){return 2}export{used,htmlOnly};"#,
        ),
    ]);
    assert!(out(temp.path(), "lib.js").contains("htmlOnly"));
}

/// A root-relative import loads the chunk too; the compiler cannot link it,
/// so the chunk's exports stay public.
#[test]
fn keeps_exports_imported_through_root_relative_specifiers() {
    let temp = bundle(&[
        (
            "assets/index.js",
            r#"import{used as u}from"./lib.js";import{rootOnly as r}from"/app/assets/lib.js";console.log(u(),r());"#,
        ),
        (
            "assets/lib.js",
            r#"function used(){return 1}function rootOnly(){return 2}export{used,rootOnly};"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert!(out(temp.path(), "assets/lib.js").contains("rootOnly"));
}

/// Chunks in an import cycle that no entry reaches are loaded from outside
/// the program: their exports stay public.
#[test]
fn keeps_exports_of_root_import_cycles() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import{helper as h}from"./lib.js";export function api(){return h()}export const VERSION=1;"#,
        ),
        (
            "lib.js",
            r#"import{VERSION as v}from"./index.js";function helper(){return v}function externalOnly(){return 2}export{helper,externalOnly};"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert!(out(temp.path(), "index.js").contains("export function api"));
    assert!(out(temp.path(), "lib.js").contains("externalOnly"));
}

/// A chunk that a classic worker loads with `importScripts` stays a classic
/// boundary even when a module also imports it.
#[test]
fn keeps_chunks_that_workers_import_scripts_from() {
    let vendor = r#"function vendorHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var vendorState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.js",
            r#"import"./vendor.js";const w=new URL("./sw.js",import.meta.url);console.log(w.pathname.length>0);"#,
        ),
        ("vendor.js", vendor),
        (
            "sw.js",
            r#"typeof importScripts=="function"&&importScripts("./vendor.js");console.log(typeof vendorHelper);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "vendor.js"), vendor);
}

/// A root-relative `importScripts` URL resolves under the bundle root like a
/// root-relative module import, so the classic chunk stays unchanged.
#[test]
fn keeps_chunks_that_workers_import_scripts_from_root_relative_urls() {
    let vendor = r#"function vendorHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var vendorState={counter:0};"#;
    let temp = bundle(&[
        (
            "assets/index.js",
            r#"import"./vendor.js";const w=new URL("./sw.js",import.meta.url);console.log(w.pathname.length>0);"#,
        ),
        ("assets/vendor.js", vendor),
        (
            "assets/sw.js",
            r#"typeof importScripts=="function"&&importScripts("/assets/vendor.js");console.log(typeof vendorHelper);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "assets/vendor.js"), vendor);
}

/// A root-relative `importScripts` URL with a deployment base prefix drops the
/// prefix segments to find the bundled chunk.
#[test]
fn keeps_chunks_that_workers_import_scripts_from_with_a_deployment_base() {
    let vendor = r#"function vendorHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var vendorState={counter:0};"#;
    let temp = bundle(&[
        (
            "assets/index.js",
            r#"import"./vendor.js";const w=new URL("./sw.js",import.meta.url);console.log(w.pathname.length>0);"#,
        ),
        ("assets/vendor.js", vendor),
        (
            "assets/sw.js",
            r#"typeof importScripts=="function"&&importScripts("/app/assets/vendor.js");console.log(typeof vendorHelper);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "assets/vendor.js"), vendor);
}

/// A query in an import specifier still loads the chunk, but the compiler
/// cannot link it: that chunk's exports stay public.
#[test]
fn keeps_exports_imported_through_query_specifiers() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import{used as u}from"./lib.js";import{viaQuery as q}from"./lib.js?v=1";console.log(u(),q());"#,
        ),
        (
            "lib.js",
            r#"function used(){return 1}function viaQuery(){return 2}export{used,viaQuery};"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert!(out(temp.path(), "lib.js").contains("viaQuery"));
}

/// Inline import specifiers are JavaScript strings: escapes are decoded.
/// An import map can send any specifier anywhere: every chunk stays public.
#[test]
fn inline_imports_follow_escapes_and_import_maps() {
    let lib = r#"function used(){return 1}function htmlOnly(){return 2}export{used,htmlOnly};"#;
    for page in [
        r#"<script type="module">import{htmlOnly}from"./lib.js";htmlOnly();</script><script type="module" src="./index.js"></script>"#,
        r#"<script type="importmap">{"imports":{"lib":"/lib.js"}}</script><script type="module">import{htmlOnly}from"lib";htmlOnly();</script><script type="module" src="./index.js"></script>"#,
        r#"<script type="module">const p="./lib.js";import(p).then(m=>m.htmlOnly());</script><script type="module" src="./index.js"></script>"#,
        r#"<button onclick="import('./lib.js').then(m=>m.htmlOnly())">go</button><script type="module" src="./index.js"></script>"#,
        r#"<a href="javascript:import('./lib.js').then(m=>m.htmlOnly())">go</a><script type="module" src="./index.js"></script>"#,
    ] {
        let temp = bundle(&[
            ("index.html", page),
            ("lib.js", lib),
            (
                "index.js",
                r#"import{used as u}from"./lib.js";console.log(u());"#,
            ),
        ]);
        optimize(temp.path(), &[]);
        assert!(out(temp.path(), "lib.js").contains("htmlOnly"), "{page}");
    }
}

/// A base inside inert content (template, noscript) does not apply.
#[test]
fn ignores_base_elements_in_inert_content() {
    let shared = r#"function sharedGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var sharedState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<template><base href="/decoy/"></template><script src="shared.js"></script><script type="module" src="/index.js"></script>"#,
        ),
        ("shared.js", shared),
        ("decoy/shared.js", "console.log(0);"),
        (
            "index.js",
            r#"import"./shared.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "shared.js"), shared);
}

/// A script resolves against the base parsed before it, not a later one.
#[test]
fn applies_base_urls_only_to_later_scripts() {
    let shared = r#"function sharedGlobalHelper(){return{verticalSpacing:1,horizontalSpacing:2}}var sharedState={counter:0};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="shared.js"></script><base href="/other/"><script type="module" src="/index.js"></script>"#,
        ),
        ("shared.js", shared),
        // Where a later base would point: it must not be chosen.
        ("other/shared.js", "console.log(0);"),
        (
            "index.js",
            r#"import"./shared.js";function own(n){return{verticalSpacing:n,horizontalSpacing:n}}const s=own(1);console.log(s.verticalSpacing+s.horizontalSpacing);"#,
        ),
    ]);
    optimize(temp.path(), &[]);
    assert_eq!(out(temp.path(), "shared.js"), shared);
}

/// A chunk that imports a module outside the bundle cannot be published,
/// whether a sibling file or a package resolved from node_modules.
#[test]
fn rejects_imports_outside_the_bundle() {
    let temp = bundle(&[("index.js", r#"import"../outside.js";console.log(1);"#)]);
    fs::write(temp.path().join("outside.js"), "console.log(0);export{};").unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("outside the bundle directory"));
    let protocol_relative = bundle(&[(
        "index.js",
        r#"if(false)import("//cdn.example.com/app.js");console.log(1);"#,
    )]);
    let output = run_typeopt(protocol_relative.path(), &[]);
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("outside the bundle directory"));
    let package = bundle(&[("index.js", r#"import{x}from"pkg";console.log(x);"#)]);
    let module = package.path().join("node_modules/pkg");
    fs::create_dir_all(&module).unwrap();
    fs::write(
        module.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(module.join("index.js"), "export const x=1;").unwrap();
    let output = run_typeopt(package.path(), &[]);
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("outside the bundle directory"));
}

#[test]
fn rejects_static_commonjs_package_requires() {
    let temp = bundle(&[("index.js", r#"require("pkg");console.log(1);"#)]);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "module.exports=1;").unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(!output.status.success());
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("outside the bundle directory"),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

#[test]
fn ignores_shadowed_commonjs_package_requires() {
    for source in [
        r#"{const require=globalThis.require;require("pkg");}"#,
        r#"function run(require){require("pkg")}run(globalThis.require);"#,
    ] {
        let temp = bundle(&[("index.js", source)]);
        let output = run_typeopt(temp.path(), &[]);
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
    }
}

#[test]
fn rejects_relative_imports_hidden_by_dynamic_package_boundary() {
    let temp = bundle(&[(
        "sub/index.js",
        r#"import{x}from"../../node_modules/pkg/index";if(false)import("pkg");console.log(x);"#,
    )]);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const x=1;").unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("outside the bundle directory"));
}

/// An explicit runtime extension must keep its runtime file linked even when
/// TypeScript can resolve a sibling declaration file first.
#[test]
fn resolves_explicit_runtime_import_before_declaration_substitution() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import{make}from"./dep.js";console.log(make(3).verticalSpacing);"#,
        ),
        (
            "dep.js",
            "export function make(n){return{verticalSpacing:n,horizontalSpacing:n*2}}",
        ),
        (
            "dep.d.ts",
            "export declare function make(n:number): {verticalSpacing:number};",
        ),
    ]);
    let original = fs::read_to_string(temp.path().join("dist/dep.js")).unwrap();
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "index.js"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(out(temp.path(), "dep.js"), original);
}

/// An extensionless runtime import must prefer its chunk over a sibling
/// declaration file when TypeScript resolves the declaration first.
#[test]
fn resolves_extensionless_runtime_import_before_declaration_substitution() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import{make}from"./dep";console.log(make(3).verticalSpacing);"#,
        ),
        (
            "dep.js",
            "export function make(n){return{verticalSpacing:n,horizontalSpacing:n*2}}",
        ),
        (
            "dep.d.ts",
            "export declare function make(n:number): {verticalSpacing:number};",
        ),
    ]);
    let original = fs::read_to_string(temp.path().join("dist/dep.js")).unwrap();
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "index.js"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(out(temp.path(), "dep.js"), original);
}

/// A syntax error in a file reached only through a dynamic package boundary
/// must not block analysis of the closed application bundle.
#[test]
fn ignores_syntax_diagnostics_in_dynamic_only_package_files() {
    let temp = bundle(&[
        (
            "index.js",
            r#"import{value}from"./settings.js";import"./guarded.js";console.log(value(3).verticalSpacing);"#,
        ),
        (
            "guarded.js",
            r#"if(false)import("pkg");export const answer=1;"#,
        ),
        (
            "settings.js",
            r#"function value(n){return{verticalSpacing:n,horizontalSpacing:n*2}}export{value};"#,
        ),
    ]);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const = 1;").unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!out(temp.path(), "settings.js").contains("verticalSpacing"));
}

/// A dynamic package declaration cannot suppress diagnostics for an explicit
/// bundle root that it references.
#[test]
fn checks_dynamic_package_references_to_bundle_roots() {
    let temp = bundle(&[
        ("index.js", r#"if(false)import("pkg");console.log(1);"#),
        ("root.js", "export const = 1;"),
    ]);
    let package = temp.path().join("dist/node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"../../root.js"}"#,
    )
    .unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        !output.status.success(),
        "malformed bundle root was published"
    );
    assert!(!temp.path().join("out").exists());
}

#[test]
fn allows_transitive_dynamic_package_declarations() {
    let temp = bundle(&[("index.js", r#"if(false)import("pkg");console.log(1);"#)]);
    let package = temp.path().join("node_modules/pkg");
    let dependency = temp.path().join("node_modules/dep");
    fs::create_dir_all(&package).unwrap();
    fs::create_dir_all(&dependency).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js","types":"index.d.ts"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const x=1;").unwrap();
    fs::write(
        package.join("index.d.ts"),
        "/// <reference path=\"./shared.d.ts\" />\n/// <reference types=\"dep\" />\ntype Y = \
         import(\"dep\").Y;\nimport Dep = require(\"dep\");\nexport type {Y};",
    )
    .unwrap();
    fs::write(package.join("shared.d.ts"), "export type Shared = string;").unwrap();
    fs::write(
        dependency.join("package.json"),
        r#"{"name":"dep","types":"index.d.ts"}"#,
    )
    .unwrap();
    fs::write(
        dependency.join("index.d.ts"),
        "export declare const y:number;",
    )
    .unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

#[test]
fn allows_transitive_dynamic_package_commonjs_requires() {
    let temp = bundle(&[("index.js", r#"if(false)import("pkg");console.log(1);"#)]);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(
        package.join("index.js"),
        r#"module.exports=require("./helper.js");"#,
    )
    .unwrap();
    fs::write(package.join("helper.js"), "module.exports={value:1};").unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

/// A malformed import type in a declaration reached only through a dynamic
/// package boundary must be skipped without panicking during dependency walk.
#[test]
fn ignores_malformed_dynamic_package_import_types() {
    let temp = bundle(&[("index.js", r#"if(false)import("pkg");console.log(1);"#)]);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js","types":"index.d.ts"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const value=1;").unwrap();
    fs::write(package.join("index.d.ts"), "export type T = import(Foo).T;").unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

/// A package loaded only by a guarded dynamic import is an unchanged
/// boundary; an independent chunk can still be optimized.
#[test]
fn guarded_dynamic_package_import_is_a_boundary() {
    let chunks = [
        (
            "index.js",
            r#"import{value}from"./settings.js";globalThis.window={};const guarded=await import("./guarded.js");console.log(value(3).verticalSpacing,guarded.answer);"#,
        ),
        (
            "guarded.js",
            r#"import{answer}from"./shared.js";if(typeof window>"u"){await import("fs/promises");await import("pkg")}export{answer};"#,
        ),
        ("shared.js", r#"const answer=42;export{answer};"#),
        (
            "settings.js",
            r#"function value(n){return{verticalSpacing:n,horizontalSpacing:n*2}}export{value};"#,
        ),
    ];
    let temp = bundle(&chunks);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js","types":"index.d.ts"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const x=1;").unwrap();
    fs::write(
        package.join("index.d.ts"),
        "export declare const x: number;",
    )
    .unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stderr = strip_ansi(&String::from_utf8_lossy(&output.stderr));
    assert!(
        stderr.contains("a dynamic package import keeps the bundle chunk unchanged")
            && stderr.contains("guarded.js")
            && stderr.contains("specifier=\"fs/promises\"")
            && stderr.contains("specifier=\"pkg\""),
        "{stderr}"
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(
        report["frozen_chunks"]
            .as_array()
            .unwrap()
            .iter()
            .any(|chunk| chunk["file"].as_str().unwrap().ends_with("shared.js")),
        "{report:#}"
    );
    for name in ["guarded.js", "shared.js"] {
        assert_eq!(
            fs::read(temp.path().join("dist").join(name)).unwrap(),
            fs::read(temp.path().join("out").join(name)).unwrap(),
            "{name}"
        );
    }
    assert!(saved(&report) > 0, "{report:#}");
    assert_ne!(out(temp.path(), "settings.js"), chunks[3].1);
    same_behavior(temp.path());

    let check = Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(temp.path())
        .args(["--bundle", "dist", "--check", "--verbose"])
        .output()
        .unwrap();
    assert!(
        check.status.success(),
        "{}",
        String::from_utf8_lossy(&check.stderr)
    );
    let check_report: Value = serde_json::from_slice(&check.stdout).unwrap();
    assert_eq!(check_report["size"], report["size"]);
    assert_eq!(check_report["frozen_chunks"], report["frozen_chunks"]);
}

/// A dynamic package specifier that resembles a hashed chunk name keeps its
/// original text when publication rehashes another chunk.
#[test]
fn preserves_hashed_dynamic_package_specifiers() {
    let chunks = [
        (
            "index-AAAAAAAA.js",
            r#"import{value}from"./settings-BBBBBBBB.js";import("./guarded-CCCCCCCC.js");console.log(value(3).verticalSpacing);"#,
        ),
        (
            "guarded-CCCCCCCC.js",
            r#"if(false)import("settings-BBBBBBBB.js");export const answer=1;"#,
        ),
        (
            "settings-BBBBBBBB.js",
            r#"function value(n){return{verticalSpacing:n,horizontalSpacing:n*2}}export{value};"#,
        ),
    ];
    let temp = bundle(&chunks);
    let package = temp.path().join("node_modules/settings-BBBBBBBB.js");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"settings-BBBBBBBB.js","type":"module","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const value=1;").unwrap();

    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(
        out(temp.path(), "guarded-CCCCCCCC.js"),
        chunks[1].1,
        "dynamic package specifier was rewritten"
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(saved(&report) > 0, "{report:#}");
}

/// An excluded chunk keeps a dynamically importing dependency and its imports
/// under their original names, so publication cannot split the module graph.
#[test]
fn excluded_chunks_freeze_dynamic_package_boundaries() {
    let temp = bundle(&[
        (
            "assets/index-AAAAAAAA.js",
            r#"import"./vendor-DDDDDDDD.js";import{value}from"./settings-BBBBBBBB.js";console.log(value(3).verticalSpacing);"#,
        ),
        (
            "assets/vendor-DDDDDDDD.js",
            r#"import"./dynamic-CCCCCCCC.js";export const vendor=1;"#,
        ),
        (
            "assets/dynamic-CCCCCCCC.js",
            r#"import{value}from"./settings-BBBBBBBB.js";if(false)import("pkg");export const dynamic=value(4);"#,
        ),
        (
            "assets/settings-BBBBBBBB.js",
            r#"function value(n){return{verticalSpacing:n,horizontalSpacing:n*2}}export{value};"#,
        ),
    ]);
    let package = temp.path().join("node_modules/pkg");
    fs::create_dir_all(&package).unwrap();
    fs::write(
        package.join("package.json"),
        r#"{"name":"pkg","type":"module","main":"index.js"}"#,
    )
    .unwrap();
    fs::write(package.join("index.js"), "export const value=1;").unwrap();

    let report = optimize(temp.path(), &["--bundle-exclude", "vendor-"]);
    for chunk in [
        "vendor-DDDDDDDD.js",
        "dynamic-CCCCCCCC.js",
        "settings-BBBBBBBB.js",
    ] {
        assert_eq!(
            fs::read(temp.path().join("out/assets").join(chunk)).unwrap(),
            fs::read(temp.path().join("dist/assets").join(chunk)).unwrap(),
            "{chunk}"
        );
    }
    let frozen = report["frozen_chunks"].as_array().unwrap();
    assert!(
        frozen.iter().any(|chunk| chunk["file"]
            .as_str()
            .unwrap()
            .ends_with("dynamic-CCCCCCCC.js")),
        "{report:#}"
    );
    assert!(
        frozen.iter().any(|chunk| chunk["file"]
            .as_str()
            .unwrap()
            .ends_with("settings-BBBBBBBB.js")),
        "{report:#}"
    );
}

#[test]
fn static_package_import_and_reexport_still_reject() {
    for source in [
        r#"import{x}from"fs/promises";console.log(x);"#,
        r#"export{x}from"fs/promises";"#,
    ] {
        let temp = bundle(&[("index.js", source)]);
        for output in [
            run_typeopt(temp.path(), &[]),
            Command::new(env!("CARGO_BIN_EXE_typeopt"))
                .current_dir(temp.path())
                .args(["--bundle", "dist", "--check"])
                .output()
                .unwrap(),
        ] {
            assert!(!output.status.success());
            assert!(
                String::from_utf8_lossy(&output.stderr).contains("outside the bundle directory"),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
    }
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

// ---------------------------------------------------------------------------
// Boundary closure: chunks that a boundary chunk can import
// ---------------------------------------------------------------------------

/// The entry reaches the stateful `shared` chunk statically and through the
/// lazily imported `vendor` chunk, which the operator excludes.
const SHARED_STATE: [(&str, &str); 4] = [
    (
        "index.html",
        r#"<script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    ),
    (
        "assets/index-AAAAAAAA.js",
        r#"import{bump as b,count as c}from"./shared-BBBBBBBB.js";const o=b();console.log(o.verticalSpacing+o.horizontalSpacing,c());import("./vendor-DDDDDDDD.js").then(v=>console.log(v.read(),c()));"#,
    ),
    (
        "assets/vendor-DDDDDDDD.js",
        r#"import{bump as b,count as c}from"./shared-BBBBBBBB.js";function read(){b();return c()}export{read};"#,
    ),
    (
        "assets/shared-BBBBBBBB.js",
        r#"let n=0;function bump(){n++;return{verticalSpacing:n,horizontalSpacing:n*2}}function count(){return n}function unusedHelperThatIsNeverCalled(x){return x.toString(36).repeat(3)}export{bump,count,unusedHelperThatIsNeverCalled as u};"#,
    ),
];

fn asset_file(dir: &Path, prefix: &str) -> String {
    listing(&dir.join("assets"))
        .into_iter()
        .find(|a| a.starts_with(prefix) && a.ends_with(".js"))
        .unwrap_or_else(|| panic!("no {prefix} chunk in {dir:?}"))
}

fn run_asset(dir: &Path, prefix: &str) -> std::process::Output {
    let output = node(&dir.join("assets").join(asset_file(dir, prefix)));
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    output
}

/// A chunk that a boundary chunk imports keeps its name and content, so the
/// module has one instance: the excluded chunk and the entry share its state.
#[test]
fn keeps_chunks_that_excluded_chunks_import() {
    let temp = bundle(&SHARED_STATE);
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "vendor-"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("chunks that a boundary chunk can import stay unchanged")
            && stderr.contains("vendor-DDDDDDDD.js"),
        "the causing boundary is logged: {stderr}"
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    let (dist, out_dir) = (temp.path().join("dist"), temp.path().join("out"));
    // The shared chunk and the excluded chunk are byte-for-byte unchanged and
    // no second copy of the shared module exists.
    for chunk in ["shared-BBBBBBBB.js", "vendor-DDDDDDDD.js"] {
        assert_eq!(
            fs::read(out_dir.join("assets").join(chunk)).unwrap(),
            fs::read(dist.join("assets").join(chunk)).unwrap(),
            "{chunk}"
        );
    }
    let assets = listing(&out_dir.join("assets"));
    assert_eq!(
        assets.iter().filter(|a| a.starts_with("shared-")).count(),
        1,
        "{assets:?}"
    );
    assert_eq!(report["size"]["retained"], 0, "{report:#}");
    let frozen = report["frozen_chunks"].as_array().unwrap();
    assert_eq!(frozen.len(), 1, "{report:#}");
    assert_eq!(frozen[0]["reason"], "boundary_reachable");
    assert!(
        frozen[0]["file"]
            .as_str()
            .unwrap()
            .ends_with("shared-BBBBBBBB.js")
    );
    assert!(
        frozen[0]["boundary"]
            .as_str()
            .unwrap()
            .ends_with("vendor-DDDDDDDD.js")
    );
    // One state instance: the entry and the lazy chunk count the same state.
    let before = node(&dist.join("assets/index-AAAAAAAA.js"));
    let after = run_asset(&out_dir, "index-");
    assert_eq!(before.stdout, after.stdout);
    assert_eq!(String::from_utf8_lossy(&after.stdout), "3 1\n2 2\n");
    // Deterministic: a second run publishes the same files.
    let again = bundle(&SHARED_STATE);
    optimize(again.path(), &["--bundle-exclude", "vendor-"]);
    let names = listing(&out_dir.join("assets"));
    assert_eq!(names, listing(&again.path().join("out/assets")));
    for name in names {
        assert_eq!(
            fs::read(out_dir.join("assets").join(&name)).unwrap(),
            fs::read(again.path().join("out/assets").join(&name)).unwrap()
        );
    }
}

/// Without the exclusion the same graph is optimized and renamed.
#[test]
fn renames_the_same_graph_without_exclusion() {
    let temp = bundle(&SHARED_STATE);
    let report = optimize(temp.path(), &[]);
    let assets = listing(&temp.path().join("out/assets"));
    assert!(!assets.iter().any(|a| a.contains("BBBBBBBB")), "{assets:?}");
    assert_eq!(
        assets.iter().filter(|a| a.starts_with("shared-")).count(),
        1,
        "{assets:?}"
    );
    assert!(saved(&report) > 0, "{report:#}");
    assert_eq!(report["size"]["retained"], 0, "{report:#}");
    assert!(report.get("frozen_chunks").is_none(), "{report:#}");
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = run_asset(&temp.path().join("out"), "index-");
    assert_eq!(before.stdout, after.stdout);
}

/// Everything a boundary chunk reaches transitively stays unchanged, while a
/// chunk it does not reach is still optimized.
#[test]
fn freezes_chunks_transitively_and_optimizes_unreached_chunks() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/index-AAAAAAAA.js",
            r#"import"./vendor-DDDDDDDD.js";import{m}from"./settings-BBBBBBBB.js";console.log(m(2).verticalSpacing);"#,
        ),
        (
            "assets/vendor-DDDDDDDD.js",
            r#"import"./middle-EEEEEEEE.js";"#,
        ),
        (
            "assets/middle-EEEEEEEE.js",
            r#"import{p}from"./plain-CCCCCCCC.js";console.log(p());"#,
        ),
        (
            "assets/plain-CCCCCCCC.js",
            r#"function p(){return"plain"}function unusedPlainHelper(n){return n.toString(36).repeat(4)}export{p,unusedPlainHelper};"#,
        ),
        (
            "assets/settings-BBBBBBBB.js",
            r#"function m(n){return{verticalSpacing:n,horizontalSpacing:n*2}}export{m};"#,
        ),
    ]);
    let report = optimize(temp.path(), &["--bundle-exclude", "vendor-"]);
    let assets = listing(&temp.path().join("out/assets"));
    for kept in [
        "vendor-DDDDDDDD.js",
        "middle-EEEEEEEE.js",
        "plain-CCCCCCCC.js",
    ] {
        assert_eq!(
            fs::read(temp.path().join("out/assets").join(kept)).unwrap(),
            fs::read(temp.path().join("dist/assets").join(kept)).unwrap(),
            "{kept}"
        );
    }
    assert!(!assets.iter().any(|a| a.contains("BBBBBBBB")), "{assets:?}");
    assert_eq!(report["frozen_chunks"].as_array().unwrap().len(), 2);
    assert_eq!(report["size"]["retained"], 0);
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = run_asset(&temp.path().join("out"), "index-");
    assert_eq!(before.stdout, after.stdout);
}

/// A page whose entry chunk is a boundary and that preloads the chunks it
/// imports.
const FROZEN_ENTRY: [(&str, &str); 4] = [
    (
        "index.html",
        r#"<link rel="modulepreload" href="/assets/settings-BBBBBBBB.js"><link rel="modulepreload" href="/assets/plain-CCCCCCCC.js"><script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
    ),
    HASHED[1],
    HASHED[2],
    HASHED[3],
];

/// Chunk file names that `entry` imports directly or through other chunks.
fn executed_graph(assets: &Path, entry: &str) -> Vec<String> {
    let mut seen = vec![entry.to_owned()];
    let mut next = 0;
    while next < seen.len() {
        let text = fs::read_to_string(assets.join(&seen[next])).unwrap();
        next += 1;
        for part in text.split("\"./").skip(1) {
            let name = part.split('"').next().unwrap().to_owned();
            if !seen.contains(&name) {
                seen.push(name);
            }
        }
    }
    seen
}

fn modulepreloads(dir: &Path) -> Vec<String> {
    let html = fs::read_to_string(dir.join("index.html")).unwrap();
    html.split("rel=\"modulepreload\" href=\"/assets/")
        .skip(1)
        .map(|part| part.split('"').next().unwrap().to_owned())
        .collect()
}

#[test]
fn keeps_preloads_consistent_with_a_frozen_entry() {
    let temp = bundle(&FROZEN_ENTRY);
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "index-"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("HTML entry chunk imports chunks that therefore cannot be optimized"),
        "{stderr}"
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    let out_dir = temp.path().join("out");
    // Every file is published as it was: no renamed chunk that no page loads.
    assert_eq!(
        listing(&out_dir.join("assets")),
        listing(&temp.path().join("dist/assets"))
    );
    let graph = executed_graph(&out_dir.join("assets"), "index-AAAAAAAA.js");
    let preloads = modulepreloads(&out_dir);
    assert_eq!(preloads.len(), 2);
    for preload in preloads {
        assert!(
            graph.contains(&preload),
            "{preload} is not loaded: {graph:?}"
        );
    }
    // The report counts no saving and discloses no retained originals.
    assert_eq!(saved(&report), 0, "{report:#}");
    assert_eq!(report["size"]["retained"], 0, "{report:#}");
    let frozen: Vec<&str> = report["frozen_chunks"]
        .as_array()
        .unwrap()
        .iter()
        .map(|c| c["file"].as_str().unwrap())
        .collect();
    assert_eq!(frozen.len(), 2, "{report:#}");
    assert!(frozen.iter().any(|f| f.ends_with("settings-BBBBBBBB.js")));
    assert!(frozen.iter().any(|f| f.ends_with("plain-CCCCCCCC.js")));
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = run_asset(&out_dir, "index-");
    assert_eq!(before.stdout, after.stdout);
}

/// The same page with an entry that is not a boundary: the imported chunks
/// are optimized and the preloads follow the new names.
#[test]
fn renames_preloads_with_a_non_frozen_entry() {
    let temp = bundle(&FROZEN_ENTRY);
    let report = optimize(temp.path(), &[]);
    let out_dir = temp.path().join("out");
    assert!(saved(&report) > 0, "{report:#}");
    assert_eq!(report["size"]["retained"], 0, "{report:#}");
    assert!(report.get("frozen_chunks").is_none(), "{report:#}");
    let entry = asset_file(&out_dir, "index-");
    let graph = executed_graph(&out_dir.join("assets"), &entry);
    let preloads = modulepreloads(&out_dir);
    assert_eq!(preloads.len(), 2);
    for preload in &preloads {
        assert!(
            graph.contains(preload),
            "{preload} is not loaded: {graph:?}"
        );
    }
    assert!(
        !preloads.iter().any(|p| p.contains("BBBBBBBB")),
        "{preloads:?}"
    );
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = run_asset(&out_dir, "index-");
    assert_eq!(before.stdout, after.stdout);
}

/// An HTML entry that is a boundary imports another boundary, which imports
/// an ordinary chunk. The entry is the first boundary in path order that
/// reaches the chunk, so it is the one that gets the credit and the warning.
#[test]
fn credits_the_first_boundary_through_nested_boundaries() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module" src="/assets/a-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/a-AAAAAAAA.js",
            r#"import"./b-BBBBBBBB.js";console.log("a");"#,
        ),
        (
            "assets/b-BBBBBBBB.js",
            r#"import{p}from"./plain-CCCCCCCC.js";console.log(p());"#,
        ),
        (
            "assets/plain-CCCCCCCC.js",
            r#"function p(){return"plain"}function unusedPlainHelper(n){return n.toString(36).repeat(4)}export{p,unusedPlainHelper};"#,
        ),
    ]);
    let output = run_typeopt(
        temp.path(),
        &[
            "--bundle-exclude",
            "a-AAAAAAAA",
            "--bundle-exclude",
            "b-BBBBBBBB",
        ],
    );
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("HTML entry chunk imports chunks that therefore cannot be optimized"),
        "{stderr}"
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    let frozen = report["frozen_chunks"].as_array().unwrap();
    assert_eq!(frozen.len(), 1, "{report:#}");
    assert!(
        frozen[0]["file"]
            .as_str()
            .unwrap()
            .ends_with("plain-CCCCCCCC.js")
    );
    assert!(
        frozen[0]["boundary"]
            .as_str()
            .unwrap()
            .ends_with("a-AAAAAAAA.js")
    );
    assert_eq!(
        fs::read(temp.path().join("out/assets/plain-CCCCCCCC.js")).unwrap(),
        fs::read(temp.path().join("dist/assets/plain-CCCCCCCC.js")).unwrap()
    );
}

/// An excluded `<script src>` with a literal dynamic import is a pinned
/// chunk that a page loads: the chunks it imports stay unchanged and the log
/// warns.
#[test]
fn warns_when_an_excluded_html_script_freezes_chunks() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="/assets/app-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/app-AAAAAAAA.js",
            r#"import("./lazy-BBBBBBBB.js").then(m=>console.log(m.v()));"#,
        ),
        (
            "assets/lazy-BBBBBBBB.js",
            r#"function v(){return"lazy"}function unusedLazyHelper(n){return n.toString(36).repeat(4)}export{v,unusedLazyHelper};"#,
        ),
    ]);
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "app-"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("HTML entry chunk imports chunks that therefore cannot be optimized")
            && stderr.contains("app-AAAAAAAA.js"),
        "{stderr}"
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    let frozen = report["frozen_chunks"].as_array().unwrap();
    assert_eq!(frozen.len(), 1, "{report:#}");
    assert!(
        frozen[0]["file"]
            .as_str()
            .unwrap()
            .ends_with("lazy-BBBBBBBB.js")
    );
    assert!(
        frozen[0]["boundary"]
            .as_str()
            .unwrap()
            .ends_with("app-AAAAAAAA.js")
    );
}

/// A classic `<script src>` with a literal dynamic import freezes nothing:
/// the lazy chunk is optimized and renamed, and the classic chunk imports
/// the new name under a new hash of its own.
#[test]
fn optimizes_chunks_that_a_classic_html_script_imports() {
    let temp = bundle(&[
        (
            "index.html",
            r#"<script src="/assets/app-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/app-AAAAAAAA.js",
            r#"import("./lazy-BBBBBBBB.js").then(m=>console.log(m.v()));"#,
        ),
        (
            "assets/lazy-BBBBBBBB.js",
            r#"function shape(n){return{verticalSpacing:n,horizontalSpacing:n*2}}function v(){const o=shape(2);return"lazy"+(o.verticalSpacing+o.horizontalSpacing)}export{v};"#,
        ),
    ]);
    let report = optimize(temp.path(), &[]);
    assert!(report.get("frozen_chunks").is_none(), "{report:#}");
    assert!(saved(&report) > 0, "{report:#}");
    let out_dir = temp.path().join("out");
    let assets = listing(&out_dir.join("assets"));
    let app = assets.iter().find(|a| a.starts_with("app-")).unwrap();
    let lazy = assets.iter().find(|a| a.starts_with("lazy-")).unwrap();
    assert_ne!(app, "app-AAAAAAAA.js", "{assets:?}");
    assert_ne!(lazy, "lazy-BBBBBBBB.js", "{assets:?}");
    assert!(out(temp.path(), &format!("assets/{app}")).contains(lazy.as_str()));
    assert!(out(temp.path(), "index.html").contains(app.as_str()));
    let before = node(&temp.path().join("dist/assets/app-AAAAAAAA.js"));
    let after = node(&out_dir.join("assets").join(app));
    assert_eq!(before.stdout, after.stdout);
}

/// A lazy route chunk that uses `eval` and imports the shared chunk back no
/// longer freezes it: the shared chunk is optimized and both get new names,
/// with one module instance.
#[test]
fn optimizes_chunks_that_an_eval_chunk_imports() {
    let route = r#"import{ready as r}from"./index-AAAAAAAA.js";function run(){var f=eval("(function(){return 1})");return r()+f()}export{run};"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/index-AAAAAAAA.js",
            r#"import{bump as b,count as c}from"./shared-BBBBBBBB.js";export const ready=()=>c();b();import("./route-CCCCCCCC.js").then(m=>console.log("route",m.run()));"#,
        ),
        (
            "assets/shared-BBBBBBBB.js",
            r#"let n=0;function shape(k){return{verticalSpacing:k,horizontalSpacing:k*2}}function bump(){n++;const o=shape(n);return o.verticalSpacing+o.horizontalSpacing}function count(){return n}export{bump,count};"#,
        ),
        ("assets/route-CCCCCCCC.js", route),
    ]);
    let report = optimize(temp.path(), &[]);
    assert!(report.get("frozen_chunks").is_none(), "{report:#}");
    assert!(saved(&report) > 0, "{report:#}");
    let out_dir = temp.path().join("out");
    let assets = listing(&out_dir.join("assets"));
    assert_eq!(assets.len(), 3, "{assets:?}");
    let index = assets.iter().find(|a| a.starts_with("index-")).unwrap();
    let route_file = assets.iter().find(|a| a.starts_with("route-")).unwrap();
    assert_ne!(route_file, "route-CCCCCCCC.js");
    // The eval chunk keeps its code; only the import specifier follows.
    assert_eq!(
        out(temp.path(), &format!("assets/{route_file}")),
        route.replace("index-AAAAAAAA.js", index)
    );
    let before = node(&temp.path().join("dist/assets/index-AAAAAAAA.js"));
    let after = node(&out_dir.join("assets").join(index));
    assert!(before.status.success() && after.status.success());
    assert_eq!(before.stdout, after.stdout);
}

/// A Workbox precache manifest lists renamed chunks under their new names
/// and gives changed files a new revision of the same length.
#[test]
fn refreshes_service_worker_precache_entries() {
    let sw = r#"importScripts("./workbox-1a2b3c4d.js");self.workbox.precacheAndRoute([{url:"assets/index-AAAAAAAA.js",revision:null},{url:"assets/settings-BBBBBBBB.js",revision:null},{url:"assets/plain-CCCCCCCC.js",revision:null},{url:"index.html",revision:"dca4ee13e6e90a3b14607a0c53990951"},{revision:"a9b855bc4fa588808a72c126fa9ade00",url:"robots.txt"}],{});"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    chunks.push((
        "workbox-1a2b3c4d.js",
        "self.workbox={precacheAndRoute(){}};",
    ));
    chunks.push(("robots.txt", "User-agent: *"));
    let temp = bundle(&chunks);
    let report = optimize(temp.path(), &[]);
    assert_eq!(report["size"]["retained"], 0, "{report:#}");
    let assets = listing(&temp.path().join("out/assets"));
    let published = out(temp.path(), "sw.js");
    for asset in &assets {
        assert!(
            published.contains(&format!("assets/{asset}")),
            "{published}"
        );
    }
    assert!(!published.contains("BBBBBBBB"), "{published}");
    assert_eq!(published.len(), sw.len());
    // index.html names the renamed chunks: its revision changes. robots.txt
    // did not change: its revision stays.
    assert!(
        !published.contains("dca4ee13e6e90a3b14607a0c53990951"),
        "{published}"
    );
    assert!(
        published.contains(r#"{revision:"a9b855bc4fa588808a72c126fa9ade00",url:"robots.txt"}"#)
    );
}

#[test]
fn refreshes_only_executable_precache_calls() {
    let sw = concat!(
        r#"importScripts("./workbox.js");
const example='precacheAndRoute([{url:"index.html",revision:"v1"}])';
// precacheAndRoute([{url:"index.html",revision:"v1"}])
/* precacheAndRoute([{url:"index.html",revision:"v1"}]) */
const pattern=/precacheAndRoute\(\[\{url:"index.html",revision:"v1"\}\]\)/;
const template="#,
        "\u{60}",
        r#"precacheAndRoute([{url:"index.html",revision:"v1"}])"#,
        "\u{60}",
        r#";
self.workbox.precacheAndRoute([{url:"index.html",revision:"v1"}]);
precache([{url:"index.html",revision:"p1"}]);"#,
    );
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    chunks.push(("workbox.js", "self.workbox={precacheAndRoute(){}};"));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    let real_call = sw.rfind("self.workbox.precacheAndRoute").unwrap();
    assert_eq!(&published[..real_call], &sw[..real_call]);
    assert_eq!(
        published.matches(r#"revision:"v1""#).count(),
        5,
        "{published}"
    );
    assert!(published.contains(r#"self.workbox.precacheAndRoute([{url:"index.html",revision:""#));
    assert!(!published.contains(r#"precache([{url:"index.html",revision:"p1""#));
    assert_eq!(
        fs::read_to_string(temp.path().join("dist/sw.js")).unwrap(),
        sw
    );
}

#[test]
fn refreshes_classic_script_precache_entries() {
    let sw = r#"with(self){self.workbox.precacheAndRoute([{url:"index.html",revision:"v1"}]);}"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    chunks.push(("workbox.js", "self.workbox={precacheAndRoute(){}};"));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(!published.contains(r#"revision:\"v1\""#), "{published}");
    assert_eq!(published.len(), sw.len());
}

#[test]
fn refreshes_namespaced_workbox_precache_entries() {
    let sw = r#"self.workbox.precaching.precacheAndRoute([{url:"assets/index-AAAAAAAA.js",revision:"old"}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(!published.contains("revision:\"old\""), "{published}");
    assert!(
        published.contains("self.workbox.precaching.precacheAndRoute"),
        "{published}"
    );
}

#[test]
fn refreshes_namespace_imported_workbox_precache_entries() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script type="importmap">{"imports":{"workbox-precaching":"/workbox.js"}}</script><script type="module" src="./app.js"></script><script type="module" src="./sw.js"></script>"#,
    );
    chunks.push(("app.js", r#"import"./assets/index-AAAAAAAA.js";function unused(){return"removed"}console.log("app");"#));
    chunks.push((
        "sw.js",
        r#"import * as wb from "workbox-precaching";wb.precacheAndRoute([{url:"app.js",revision:""}]);"#,
    ));
    chunks.push(("workbox.js", "export function precacheAndRoute() {}"));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "{stderr}");
    assert!(
        stderr.contains("cannot refresh empty precache revision"),
        "{stderr}"
    );
}

#[test]
fn ignores_ambiguous_precache_calls_with_empty_revisions() {
    let sw = r#"function precache(){};precache([{url:"index.html",revision:""}]);function precacheAndRoute(){};precacheAndRoute([{url:"index.html",revision:""}]);const api={precacheAndRoute(){}};api.precacheAndRoute([{url:"index.html",revision:""}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(out(temp.path(), "sw.js").contains(r#"revision:"""#));
}

#[test]
fn ignores_precache_calls_inside_with_scopes() {
    let sw = r#"with({precacheAndRoute(){}}){precacheAndRoute([{url:"index.html",revision:""}])}with({workbox:{precacheAndRoute(){}}}){workbox.precacheAndRoute([{url:"index.html",revision:"old"}])}"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(out(temp.path(), "sw.js"), sw);
}

#[test]
fn ignores_ambiguous_precache_calls_with_nonempty_revisions() {
    let sw = r#"function precache(){};precache([{url:"index.html",revision:"p1"}]);function precacheAndRoute(){};precacheAndRoute([{url:"index.html",revision:"a1"}]);const api={precacheAndRoute(){}};api.precacheAndRoute([{url:"index.html",revision:"m1"}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(out(temp.path(), "sw.js"), sw);
}

#[test]
fn fails_empty_revision_for_direct_workbox_precache_call() {
    let sw = r#"precacheAndRoute([{url:"index.html",revision:""}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "{stderr}");
    assert!(
        stderr.contains("cannot refresh empty precache revision"),
        "{stderr}"
    );
}

#[test]
fn fails_empty_revision_for_bare_workbox_receiver() {
    let sw = r#"workbox.precacheAndRoute([{url:"index.html",revision:""}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "{stderr}");
    assert!(
        stderr.contains("cannot refresh empty precache revision"),
        "{stderr}"
    );
}

#[test]
fn fails_empty_revision_for_imported_workbox_precache_call() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script type="importmap">{"imports":{"workbox-precaching":"/workbox.js"}}</script><script type="module" src="./app.js"></script><script type="module" src="./sw.js"></script>"#,
    );
    chunks.push((
        "app.js",
        r#"import"./assets/index-AAAAAAAA.js";function unused(){return"removed"}console.log("app");"#,
    ));
    chunks.push((
        "sw.js",
        r#"import{precacheAndRoute}from"workbox-precaching";precacheAndRoute([{url:"app.js",revision:""}]);"#,
    ));
    chunks.push(("workbox.js", "export function precacheAndRoute() {}"));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "{stderr}");
    assert!(
        stderr.contains("cannot refresh empty precache revision"),
        "{stderr}"
    );
}

#[test]
fn refreshes_precache_entries_with_escaped_urls() {
    let sw = r#"self.workbox.precacheAndRoute([{url:"index\u002ehtml",revision:"v1"},{url:"robots\u002etxt",revision:"r1"},{url:"https:\u002f\u002fexample.com/index.html",revision:"x1"},{url:"..\u002f..\u002foutside.txt",revision:"o1"}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    chunks.push(("robots.txt", "User-agent: *"));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(published.contains(r#"url:"index\u002ehtml",revision:""#));
    assert!(!published.contains(r#"url:"index\u002ehtml",revision:"v1""#));
    assert!(published.contains(r#"url:"robots\u002etxt",revision:"r1""#));
    assert!(published.contains(r#"url:"https:\u002f\u002fexample.com/index.html",revision:"x1""#));
    assert!(published.contains(r#"url:"..\u002f..\u002foutside.txt",revision:"o1""#));
    assert_eq!(published.len(), sw.len());
}

#[test]
fn ignores_shadowed_workbox_aliases() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script type="importmap">{"imports":{"workbox-precaching":"/workbox.js"}}</script><script type="module" src="./sw.js"></script>"#,
    );
    chunks.push((
        "sw.js",
        r#"import{precacheAndRoute as cache}from"workbox-precaching";function run(cache){cache([{url:"assets/index-AAAAAAAA.js",revision:"old"}])}run(cache);"#,
    ));
    chunks.push(("workbox.js", "export function precacheAndRoute() {}"));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(published.contains("revision:\"old\""), "{published}");
}

#[test]
fn refreshes_imported_workbox_alias_outside_shadowed_scope() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script type="importmap">{"imports":{"workbox-precaching":"/workbox.js"}}</script><script type="module" src="./app.js"></script><script type="module" src="./sw.js"></script>"#,
    );
    chunks.push((
        "app.js",
        r#"import"./assets/index-AAAAAAAA.js";function unused(){return"removed"}console.log("app");"#,
    ));
    chunks.push((
        "sw.js",
        r#"import{precacheAndRoute as cache}from"workbox-precaching";cache([{url:"app.js",revision:""}]);function run(cache){cache([{url:"app.js",revision:"old"}])}run(cache);"#,
    ));
    chunks.push(("workbox.js", "export function precacheAndRoute() {}"));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "{stderr}");
    assert!(
        stderr.contains("cannot refresh empty precache revision"),
        "{stderr}"
    );
}

#[test]
fn ignores_local_workbox_receiver_with_empty_revision() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[0] = (
        "index.html",
        r#"<script type="importmap">{"imports":{"workbox-precaching":"/workbox.js"}}</script><script type="module" src="./app.js"></script><script type="module" src="./sw.js"></script>"#,
    );
    chunks.push((
        "app.js",
        r#"import"./assets/index-AAAAAAAA.js";function unused(){return"removed"}console.log("app");"#,
    ));
    chunks.push((
        "sw.js",
        r#"const workbox={precacheAndRoute(){}};workbox.precacheAndRoute([{url:"app.js",revision:""}]);"#,
    ));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(out(temp.path(), "sw.js").contains(r#"url:"app.js",revision:"""#));
}

#[test]
fn ignores_shadowed_global_workbox_receiver_roots() {
    for root in ["self", "globalThis", "window"] {
        let sw = format!(
            "function run({root}){{{root}.workbox.precacheAndRoute([{{url:\"app.js\",revision:\"\"\
             }}]);}}"
        );
        let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
        chunks.push(("sw.js", sw.as_str()));
        chunks.push(("app.js", "console.log(\"app\");"));
        let temp = bundle(&chunks);
        let output = run_typeopt(temp.path(), &[]);
        assert!(
            output.status.success(),
            "{root}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(
            out(temp.path(), "sw.js").contains("revision:\"\""),
            "{root}"
        );
    }
}

#[test]
fn keeps_pinned_and_hashed_precache_manifests() {
    let sw = r#"self.workbox.precacheAndRoute([{url:"index.html",revision:"old"}]);"#;
    for (name, options) in [
        ("sw.js", vec!["--bundle-exclude", "sw.js"]),
        ("sw-AB12cd34.js", vec![]),
    ] {
        let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
        chunks.push((name, sw));
        let temp = bundle(&chunks);
        optimize(temp.path(), &options);
        assert_eq!(out(temp.path(), name), sw, "{name}");
    }
}

#[test]
fn refreshes_deployment_prefixed_precache_url_after_exact_lookup() {
    let sw = r#"self.workbox.precacheAndRoute([{url:"/my-app/index.html",revision:"old"},{url:"/index.html",revision:"old"}]);"#;
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(!published.contains("revision:\"old\""), "{published}");
    assert!(
        published.contains("url:\"/my-app/index.html\""),
        "{published}"
    );
    assert_eq!(published.len(), sw.len());

    let mut exact_chunks = chunks;
    exact_chunks.push(("my-app/index.html", "unchanged exact target"));
    let exact = bundle(&exact_chunks);
    optimize(exact.path(), &[]);
    let published = out(exact.path(), "sw.js");
    assert!(
        published.contains(r#"url:"/my-app/index.html",revision:"old""#),
        "{published}"
    );
    assert!(
        !published.contains(r#"url:"/index.html",revision:"old""#),
        "{published}"
    );
}

#[test]
fn refreshes_colliding_short_precache_revision_and_rejects_empty_one() {
    use sha2::{Digest, Sha256};

    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push((
        "sw.js",
        r#"self.workbox.precacheAndRoute([{url:"/index.html",revision:"x"}]);"#,
    ));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let digest = Sha256::digest(fs::read(temp.path().join("out/index.html")).unwrap());
    let colliding = format!("{:x}", digest[0] >> 4);
    fs::remove_dir_all(temp.path().join("out")).unwrap();
    let sw = format!(
        "self.workbox.precacheAndRoute([{{url:\"/index.html\",revision:\"{colliding}\"}}]);"
    );
    fs::write(temp.path().join("dist/sw.js"), &sw).unwrap();
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(
        !published.contains(&format!("revision:\"{colliding}\"")),
        "{published}"
    );
    assert_eq!(published.len(), sw.len());

    fs::remove_dir_all(temp.path().join("out")).unwrap();
    optimize(temp.path(), &[]);
    assert_eq!(published, out(temp.path(), "sw.js"));

    fs::remove_dir_all(temp.path().join("out")).unwrap();
    let empty = r#"self.workbox.precacheAndRoute([{url:"/index.html",revision:""}]);"#;
    fs::write(temp.path().join("dist/sw.js"), empty).unwrap();
    let output = run_typeopt(temp.path(), &[]);
    assert!(!output.status.success());
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("cannot refresh empty precache revision"),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!temp.path().join("out").exists());
    assert_eq!(
        fs::read_to_string(temp.path().join("dist/sw.js")).unwrap(),
        empty
    );
}

/// A chunk in another directory with the same content-hashed file name as a
/// frozen chunk is frozen too: references name only the file name, so an
/// edited copy could not be renamed and would be published under the old hash.
#[test]
fn freezes_same_named_chunks_in_other_directories() {
    let shared = SHARED_STATE[3].1;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module" src="/assets/index-AAAAAAAA.js"></script>"#,
        ),
        (
            "assets/index-AAAAAAAA.js",
            r#"import{bump as b,count as c}from"./other/shared-BBBBBBBB.js";const o=b();console.log(o.verticalSpacing+o.horizontalSpacing,c());import("./vendor-DDDDDDDD.js").then(v=>console.log(v.read()));"#,
        ),
        SHARED_STATE[2],
        ("assets/shared-BBBBBBBB.js", shared),
        ("assets/other/shared-BBBBBBBB.js", shared),
    ]);
    let output = run_typeopt(temp.path(), &["--bundle-exclude", "vendor-"]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    let (dist, out_dir) = (temp.path().join("dist"), temp.path().join("out"));
    // Neither copy is edited or renamed, so no cache can hold the old hash
    // for new content.
    for chunk in ["shared-BBBBBBBB.js", "other/shared-BBBBBBBB.js"] {
        assert_eq!(
            fs::read(out_dir.join("assets").join(chunk)).unwrap(),
            fs::read(dist.join("assets").join(chunk)).unwrap(),
            "{chunk}"
        );
    }
    assert_eq!(
        report["frozen_chunks"].as_array().unwrap().len(),
        2,
        "{report:#}"
    );
    let before = node(&dist.join("assets/index-AAAAAAAA.js"));
    let after = run_asset(&out_dir, "index-");
    assert_eq!(before.stdout, after.stdout);
}

// ---------------------------------------------------------------------------
// call-inlining in bundle chunks
// ---------------------------------------------------------------------------

/// Unwritten `let` and `var` functions are inlined like `const` ones.
#[test]
fn inlines_unwritten_let_and_var_functions() {
    let (temp, report) = run(&[
        (
            "index.js",
            r#"import{run as r}from"./lib.js";console.log(r(2),r(5));"#,
        ),
        (
            "lib.js",
            r#"var twice=function(n){return n*2};let inc=n=>n+1;function run(x){return inc(twice(x))+inc(x)}export{run};"#,
        ),
    ]);
    assert!(applied(&report, "call-inlining") >= 2, "{report:#}");
    let lib = out(temp.path(), "lib.js");
    assert!(!lib.contains("twice(") && !lib.contains("inc("), "{lib}");
}

/// A `var` in a nested block may be uninitialized at a site after the
/// block, where the call throws.
#[test]
fn keeps_calls_of_var_functions_in_nested_blocks() {
    let (temp, _) = run(&[
        (
            "index.js",
            r#"import{run as r}from"./lib.js";try{console.log(r(true,1),r(false,1))}catch(e){console.log(e.constructor.name)}"#,
        ),
        (
            "lib.js",
            r#"function run(c,x){if(c){var f=function(n){return n+1}}return f(x)}export{run};"#,
        ),
    ]);
    assert!(out(temp.path(), "lib.js").contains("f(x)"));
}

/// Unchecked chunks do not need matching inferred types, and missing
/// arguments become `void 0`.
#[test]
fn inlines_calls_regardless_of_inferred_types_and_missing_arguments() {
    let (temp, report) = run(&[
        ("index.js", r#"import{show as s}from"./lib.js";s();"#),
        (
            "lib.js",
            r#"function len(s){return s.length}function pick(a,b){return b===void 0?a:b}function show(){console.log(len([1,2]),len("abc"),pick(3),pick(3,4))}export{show};"#,
        ),
    ]);
    // One bundle per target function covers all of its sites.
    assert!(applied(&report, "call-inlining") >= 2, "{report:#}");
    let lib = out(temp.path(), "lib.js");
    assert!(!lib.contains("len(") && !lib.contains("pick("), "{lib}");
}

/// Assignments to properties and free bindings are inlined with their
/// evaluation order; writes to parameters are not.
#[test]
fn inlines_assignments_but_not_parameter_writes() {
    let (temp, report) = run(&[
        ("index.js", r#"import{show as s}from"./lib.js";s();"#),
        (
            "lib.js",
            r#"let count=0;function bump(o,v){return o.total+=v}function last(o,v){return o.last=v}function init(o){return o.v??=7}function tick(){return count++}function next(n){return n+=1}function show(){const s={total:1,last:0,v:null};console.log(bump(s,3),last(s,4),init(s),init(s),tick(),tick(),count);let a=1;console.log(next(a),a,s.total,s.last,s.v)}export{show};"#,
        ),
    ]);
    assert!(applied(&report, "call-inlining") >= 4, "{report:#}");
    let lib = out(temp.path(), "lib.js");
    assert!(!lib.contains("bump(") && !lib.contains("tick("), "{lib}");
    assert!(lib.contains("next(a)"), "{lib}");
}

/// An argument that reads a binding the body writes is read before the
/// write; inlining would read it after.
#[test]
fn keeps_calls_whose_body_writes_an_argument_binding() {
    let (temp, _) = run(&[
        ("index.js", r#"import{show as s}from"./lib.js";s();"#),
        (
            "lib.js",
            r#"let n=0;function reset(x){return n=1,x}function show(){console.log(reset(n),n)}export{show};"#,
        ),
    ]);
    assert!(out(temp.path(), "lib.js").contains("reset(n)"));
}

/// A written binding imported at the site is read-only there, so a function
/// that writes its own module binding is not inlined into an importer.
#[test]
fn keeps_calls_that_write_a_binding_imported_at_the_site() {
    let (temp, _) = run(&[
        (
            "index.js",
            r#"import{n,tick}from"./lib.js";console.log(tick(),tick(),n);"#,
        ),
        (
            "lib.js",
            r#"let n=0;function tick(){return n++}export{n,tick};"#,
        ),
    ]);
    assert!(out(temp.path(), "index.js").contains("tick()"));
}

/// `{url, revision}` objects outside a Workbox precache call are application
/// data and keep their values.
#[test]
fn keeps_revision_data_outside_precache_calls() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks[1] = (
        "assets/index-AAAAAAAA.js",
        // The manifest escapes to unknown code, so its keys keep their names
        // (a tracked array of objects would have them renamed).
        r#"import{m as e}from"./settings-BBBBBBBB.js";import{p as t}from"./plain-CCCCCCCC.js";import{d}from"../config.js";globalThis.manifest=d;console.log(e(2).verticalSpacing,t(),d[0].revision);"#,
    );
    chunks.push((
        "config.js",
        r#"export const d=[{url:"index.html",revision:"v1"}];"#,
    ));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    assert_eq!(
        out(temp.path(), "config.js"),
        r#"export const d=[{url:"index.html",revision:"v1"}];"#
    );
}

/// Formatted Workbox manifests put whitespace before keys; their entries are
/// refreshed as well.
#[test]
fn refreshes_formatted_precache_entries() {
    let sw = "self.workbox.precacheAndRoute([\n  {\n    url: \"index.html\",\n    revision: \
              \"dca4ee13e6e90a3b14607a0c53990951\"\n  },\n  { revision: null, url: \
              \"assets/settings-BBBBBBBB.js\" }\n]);\n";
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push(("sw.js", sw));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(
        !published.contains("dca4ee13e6e90a3b14607a0c53990951"),
        "{published}"
    );
    assert!(!published.contains("BBBBBBBB"), "{published}");
    assert_eq!(published.len(), sw.len());
}

#[test]
fn refreshes_nested_precache_manifests_in_dependency_order() {
    use sha2::{Digest, Sha256};

    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push((
        "a-sw.js",
        r#"self.workbox.precacheAndRoute([{url:"z-sw.js",revision:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]);"#,
    ));
    chunks.push((
        "z-sw.js",
        r#"self.workbox.precacheAndRoute([{url:"index.html",revision:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]);"#,
    ));
    let temp = bundle(&chunks);
    optimize(temp.path(), &[]);

    let z_sw = out(temp.path(), "z-sw.js");
    let a_sw = out(temp.path(), "a-sw.js");
    let digest = Sha256::digest(z_sw.as_bytes())
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    assert!(
        a_sw.contains(&format!("revision:\"{}\"", &digest[..32])),
        "{a_sw}"
    );
    assert!(!a_sw.contains("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "{a_sw}");
    assert!(!z_sw.contains("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), "{z_sw}");
}

#[test]
fn rejects_cyclic_precache_manifest_dependencies() {
    let mut chunks: Vec<(&str, &str)> = HASHED.to_vec();
    chunks.push((
        "a-sw.js",
        r#"self.workbox.precaching.precacheAndRoute([{url:"b-sw.js",revision:"a"}]);"#,
    ));
    chunks.push((
        "b-sw.js",
        r#"self.workbox.precaching.precacheAndRoute([{url:"a-sw.js",revision:"b"}]);"#,
    ));
    let temp = bundle(&chunks);
    let output = run_typeopt(temp.path(), &[]);
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(!output.status.success(), "{stderr}");
    assert!(
        stderr.contains("cyclic precache manifest dependency"),
        "{stderr}"
    );
}

/// A precache URL that leaves the bundle directory is ignored: typeopt never
/// reads outside the staged output for it.
#[test]
fn ignores_precache_entries_outside_the_bundle() {
    let temp = bundle(&HASHED);
    fs::write(temp.path().join("outside.txt"), "outside").unwrap();
    let sw = r#"self.workbox.precacheAndRoute([{url:"../../outside.txt",revision:"00000000000000000000000000000000"},{url:"assets/../index.html",revision:"dca4ee13e6e90a3b14607a0c53990951"}]);"#;
    fs::write(temp.path().join("dist/sw.js"), sw).unwrap();
    optimize(temp.path(), &[]);
    let published = out(temp.path(), "sw.js");
    assert!(
        published
            .contains(r#"{url:"../../outside.txt",revision:"00000000000000000000000000000000"}"#),
        "{published}"
    );
    // A `..` that stays inside the bundle still names index.html.
    assert!(
        !published.contains("dca4ee13e6e90a3b14607a0c53990951"),
        "{published}"
    );
}

// ---------------------------------------------------------------------------
// --minify
// ---------------------------------------------------------------------------

/// A reflected callable can live in a different chunk from the read of its
/// source. Both runs must publish the same optimized callable text.
fn check_observable_function_source(index: &str, callable: &str) {
    let settings = format!(
        "{}\n{callable}\n//# sourceMappingURL=settings.js.map\n",
        VERBOSE[1].1
    );
    let map = r#"{"version":3,"sources":["src/settings.ts"],"names":[],"mappings":"AAAA"}"#;
    let temp = bundle(&[
        ("index.js", index),
        ("settings.js", &settings),
        ("settings.js.map", map),
    ]);
    let plain = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    assert!(
        plain["renamed_properties"].as_u64().unwrap() > 0,
        "{plain:#}"
    );
    let plain_settings = out(temp.path(), "settings.js");
    assert_ne!(plain_settings, settings);
    fs::remove_dir_all(temp.path().join("out")).unwrap();
    let report = optimize(temp.path(), &["--minify"]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "settings.js"), plain_settings);
    assert!(report["size"]["minified"].is_null(), "{report:#}");
    let map = sourcemap::SourceMap::from_slice(out(temp.path(), "settings.js.map").as_bytes())
        .expect("composed source map");
    assert_eq!(map.get_source(0), Some("src/settings.ts"));
}

#[test]
fn observable_function_source_direct_call() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(Function.prototype.toString.call(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_direct_method() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(reflected.toString().includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_helper_alias() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
const readSource=Function.prototype.toString;
function helper(value){return readSource.call(value)}
console.log(describe(make(3)),describe(make(7)));
console.log(helper(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_class_and_method() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,Reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(Function.prototype.toString.call(Reflected).includes("reflection-marker"));"#,
        r#"class Reflected { /* reflection-marker */ value() { return 1; } }
export { Reflected };"#,
    );
}

#[test]
fn observable_function_source_computed_alias() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
const key="toString";
const readSource=Function.prototype[key];
console.log(describe(make(3)),describe(make(7)));
console.log(readSource.call(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_dynamic_member() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
const key=["to","String"].join("");
console.log(describe(make(3)),describe(make(7)));
console.log(reflected[key]().includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_escaped_member() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(reflected["to\u0053tring"]().includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_destructured_alias() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
const FunctionAlias=Function;
const {toString:readSource}=FunctionAlias.prototype;
console.log(describe(make(3)),describe(make(7)));
console.log(readSource.call(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_computed_pattern_key() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
const {["toString"]:readSource}=reflected;
console.log(describe(make(3)),describe(make(7)));
console.log(readSource.call(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_dynamic_pattern_key() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
const key=["to","String"].join("");
const {[key]:readSource}=reflected;
console.log(describe(make(3)),describe(make(7)));
console.log(readSource.call(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_string_coercion() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(String(reflected).includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_template_coercion() {
    check_observable_function_source(
        r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(`${reflected}`.includes("reflection-marker"));"#,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
}

#[test]
fn observable_function_source_direct_eval() {
    let index = r#"import{describeSettings as describe,makeSettings as make,reflected}from"./settings.js";
console.log(describe(make(3)),describe(make(7)));
console.log(eval("reflected.toString()").includes("reflection-marker"));"#;
    let settings = format!(
        "{}\n{}\n//# sourceMappingURL=settings.js.map\n",
        VERBOSE[1].1,
        r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#,
    );
    let temp = bundle(&[
        ("index.js", index),
        ("settings.js", &settings),
        (
            "settings.js.map",
            r#"{"version":3,"sources":["src/settings.ts"],"names":[],"mappings":"AAAA"}"#,
        ),
    ]);
    let plain = optimize(temp.path(), &[]);
    same_behavior(temp.path());
    let plain_settings = out(temp.path(), "settings.js");
    fs::remove_dir_all(temp.path().join("out")).unwrap();
    let report = optimize(temp.path(), &["--minify"]);
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "settings.js"), plain_settings);
    assert!(
        report["size"]["minified"].is_null(),
        "{report:#}; plain={plain:#}"
    );
}

#[test]
fn observable_function_source_inline_html_script() {
    let settings = r#"function reflected(value) { /* reflection-marker */ return value + 1; }
export { reflected };"#;
    let temp = bundle(&[
        (
            "index.html",
            r#"<script type="module">import{reflected}from"./settings.js";console.log(reflected.toString().includes("reflection-marker"));</script><script type="module" src="./index.js"></script>"#,
        ),
        ("settings.js", settings),
        (
            "index.js",
            r#"import{makeSettings}from"./other.js";console.log(makeSettings(3).verticalSpacing);"#,
        ),
        (
            "other.js",
            r#"function makeSettings(count){return{verticalSpacing:count,horizontalSpacing:count*2}}export{makeSettings};"#,
        ),
    ]);
    let plain = optimize(temp.path(), &[]);
    let plain_other = out(temp.path(), "other.js");
    assert_ne!(
        plain_other,
        r#"function makeSettings(count){return{verticalSpacing:count,horizontalSpacing:count*2}}export{makeSettings};"#
    );
    fs::remove_dir_all(temp.path().join("out")).unwrap();
    let report = optimize(temp.path(), &["--minify"]);
    assert_eq!(out(temp.path(), "other.js"), plain_other);
    assert!(
        report["size"]["minified"].is_null(),
        "{report:#}; plain={plain:#}"
    );
}

/// `settings.js` keeps its object local, so renaming its properties stays a
/// real edit even when every chunk is a public entry.
const LOCAL_PROPERTIES: [(&str, &str); 2] = [
    (
        "index.js",
        r#"import{describeSettings as describe}from"./settings.js";
console.log(describe(3), describe(7));
"#,
    ),
    (
        "settings.js",
        r#"function describeSettings(count) {
  const local = { verticalSpacing: count, horizontalSpacing: count * 2, highlightColour: count > 4 ? "red" : "blue" };
  const description = local.verticalSpacing + ":" + local.horizontalSpacing + ":" + local.highlightColour;
  return description;
}
export { describeSettings };
"#,
    ),
];

/// Builds a bundle whose `settings.js` has a real optimization edit and whose
/// `index.html` is `html`.
fn html_attribute_bundle(html: &str) -> tempfile::TempDir {
    bundle(&[
        ("index.html", html),
        LOCAL_PROPERTIES[0],
        LOCAL_PROPERTIES[1],
    ])
}

/// An executable attribute with a character reference the parser cannot decode
/// may read callable source, so `--minify` must publish the unminified edit.
#[test]
fn observable_function_source_undecodable_executable_attribute() {
    for attributes in [
        r#"onclick="import&Tab;('./settings.js')""#,
        r#"onclick="Function.prototype&lbrack;'toString'&rbrack;.call(Object)""#,
        r#"onclick="import&Tab;('./settings.js')" title="plain""#,
        r#"href="javascript:import&Tab;('./settings.js')""#,
    ] {
        let html = if attributes.starts_with("href") {
            format!("<a {attributes}></a>")
        } else {
            format!("<button {attributes}></button>")
        };
        let temp = html_attribute_bundle(&html);
        let plain = optimize(temp.path(), &[]);
        same_behavior(temp.path());
        assert!(
            plain["renamed_properties"].as_u64().unwrap() > 0,
            "{html}: {plain:#}"
        );
        let plain_settings = out(temp.path(), "settings.js");
        assert_ne!(plain_settings, LOCAL_PROPERTIES[1].1, "{html}");
        fs::remove_dir_all(temp.path().join("out")).unwrap();
        let report = optimize(temp.path(), &["--minify"]);
        same_behavior(temp.path());
        assert_eq!(out(temp.path(), "settings.js"), plain_settings, "{html}");
        assert!(report["size"]["minified"].is_null(), "{html}: {report:#}");
    }
}

/// The same unknown reference in an attribute that cannot run code leaves the
/// edited chunk eligible for minification.
#[test]
fn observable_function_source_undecodable_non_executable_attribute_still_minifies() {
    for attributes in [
        r#"title="&Tab;&lbrack;'toString'&rbrack;""#,
        r#"data-x="import&Tab;('./settings.js')""#,
    ] {
        let html = format!("<p {attributes}></p>");
        let temp = html_attribute_bundle(&html);
        let report = optimize(temp.path(), &["--minify"]);
        same_behavior(temp.path());
        assert!(
            report["size"]["minified"].as_u64().unwrap() > 0,
            "{attributes}: {report:#}"
        );
    }
}

#[test]
fn observable_function_source_safe_chunk_still_minifies() {
    let index = format!(
        "{}\nconsole.log('toString is only text', (13).toString(16));\n",
        VERBOSE[0].1
    );
    let temp = bundle(&[("index.js", &index), VERBOSE[1]]);
    let report = optimize(temp.path(), &["--minify"]);
    same_behavior(temp.path());
    assert!(
        report["size"]["minified"].as_u64().unwrap() > 0,
        "{report:#}"
    );
}

const VERBOSE: [(&str, &str); 2] = [
    (
        "index.js",
        r#"import{describeSettings as describe,makeSettings as make}from"./settings.js";
const firstSettings = make(3);
const secondSettings = make(7);
console.log(describe(firstSettings), describe(secondSettings));
"#,
    ),
    (
        "settings.js",
        r#"function makeSettings(count) {
  const result = { verticalSpacing: count, horizontalSpacing: count * 2, highlightColour: count > 4 ? "red" : "blue" };
  return result;
}
function describeSettings(settings) {
  const description = settings.verticalSpacing + ":" + settings.horizontalSpacing + ":" + settings.highlightColour;
  return description;
}
export { makeSettings, describeSettings };
"#,
    ),
];

/// `--minify` shrinks edited chunks further with SWC and reports it.
#[test]
fn minifies_edited_chunks() {
    let temp = bundle(&VERBOSE);
    let plain = optimize(temp.path(), &[]);
    let plain_settings = out(temp.path(), "settings.js");
    fs::remove_dir_all(temp.path().join("out")).unwrap();
    let report = optimize(temp.path(), &["--minify"]);
    same_behavior(temp.path());
    let minified = report["size"]["minified"].as_u64().unwrap();
    assert!(minified > 0, "{report:#}");
    assert_eq!(
        report["size"]["saved"].as_i64().unwrap(),
        plain["size"]["saved"].as_i64().unwrap() + minified as i64,
        "{report:#}"
    );
    let settings = out(temp.path(), "settings.js");
    assert!(settings.len() < plain_settings.len(), "{settings}");
    // Exports keep their names for importers.
    assert!(settings.contains("makeSettings") && settings.contains("describeSettings"));
}

#[test]
fn minifies_legal_notices_with_overlapping_comment_text() {
    let settings = format!(
        "function unused() {{ /** @license MIT */ return 42; }}\n/** @license MIT additional \
         terms */\n{}",
        VERBOSE[1].1
    );
    let temp = bundle(&[("index.js", VERBOSE[0].1), ("settings.js", &settings)]);
    let report = optimize(
        temp.path(),
        &["--minify", "--disable-pass", "residual-dependency-removal"],
    );
    same_behavior(temp.path());
    assert!(
        report["size"]["minified"].as_u64().unwrap() > 0,
        "{report:#}"
    );
    let published = out(temp.path(), "settings.js");
    assert!(!published.contains("unused"), "{published}");
    assert_eq!(
        published.matches("/** @license MIT */").count(),
        1,
        "{published}"
    );
    assert_eq!(
        published
            .matches("/** @license MIT additional terms */")
            .count(),
        1,
        "{published}"
    );
}

#[test]
fn minifies_legal_notice_even_when_its_text_occurs_in_a_string() {
    let index = format!(
        "{}\nimport {{ noticeText }} from './settings.js'; console.log(noticeText);",
        VERBOSE[0].1
    );
    let settings = format!(
        "function unused() {{ /** @license MIT */ return 42; }}\nexport const noticeText = '* \
         @license MIT ';\n{}",
        VERBOSE[1].1
    );
    let temp = bundle(&[("index.js", &index), ("settings.js", &settings)]);
    let report = optimize(
        temp.path(),
        &["--minify", "--disable-pass", "residual-dependency-removal"],
    );
    same_behavior(temp.path());
    assert!(
        report["size"]["minified"].as_u64().unwrap() > 0,
        "{report:#}"
    );
    let published = out(temp.path(), "settings.js");
    assert!(!published.contains("unused"), "{published}");
    assert_eq!(
        published.matches("/** @license MIT */").count(),
        1,
        "{published}"
    );
    assert!(published.contains("* @license MIT "), "{published}");
}

#[test]
fn minifies_legal_notice_control_with_composed_source_map() {
    let index = format!(
        "{}\nimport {{ noticeText }} from './settings.js'; console.log(noticeText);",
        VERBOSE[0].1
    );
    let settings = format!(
        "/** @license MIT */\nexport const noticeText = 'unrelated';\n{}//# \
         sourceMappingURL=settings.js.map\n",
        VERBOSE[1].1
    );
    let map = r#"{"version":3,"sources":["src/settings.ts"],"names":[],"mappings":"AAAA"}"#;
    let temp = bundle(&[
        ("index.js", &index),
        ("settings.js", &settings),
        ("settings.js.map", map),
    ]);
    let report = optimize(
        temp.path(),
        &["--minify", "--disable-pass", "residual-dependency-removal"],
    );
    same_behavior(temp.path());
    assert!(
        report["size"]["minified"].as_u64().unwrap() > 0,
        "{report:#}"
    );
    let published = out(temp.path(), "settings.js");
    assert_eq!(
        published.matches("/** @license MIT */").count(),
        1,
        "{published}"
    );
    assert!(published.contains("noticeText"), "{published}");
    assert!(
        published
            .trim_end()
            .ends_with("//# sourceMappingURL=settings.js.map"),
        "{published}"
    );
    let map =
        sourcemap::SourceMap::from_slice(out(temp.path(), "settings.js.map").as_bytes()).unwrap();
    assert_eq!(map.get_source(0), Some("src/settings.ts"));
    assert!(map.tokens().count() > 0);
}

/// The published map leads from the minified code to the original sources.
#[test]
fn composes_maps_of_minified_chunks() {
    let mut chunks: Vec<(&str, &str)> = VERBOSE.to_vec();
    let settings = format!("{}//# sourceMappingURL=settings.js.map\n", VERBOSE[1].1);
    chunks[1] = ("settings.js", &settings);
    // One mapping per generated line, each to the same line of src/settings.ts.
    let lines = VERBOSE[1].1.lines().count();
    let mappings = std::iter::once("AAAA")
        .chain(std::iter::repeat_n("AACA", lines - 1))
        .collect::<Vec<_>>()
        .join(";");
    let map = format!(
        r#"{{"version":3,"sources":["src/settings.ts"],"names":[],"mappings":"{mappings}"}}"#
    );
    chunks.push(("settings.js.map", &map));
    let temp = bundle(&chunks);
    let report = optimize(temp.path(), &["--minify"]);
    assert!(
        report["size"]["minified"].as_u64().unwrap() > 0,
        "{report:#}"
    );
    same_behavior(temp.path());
    let published = out(temp.path(), "settings.js");
    assert!(
        published
            .trim_end()
            .ends_with("//# sourceMappingURL=settings.js.map"),
        "{published}"
    );
    let map: Value = serde_json::from_str(&out(temp.path(), "settings.js.map")).unwrap();
    assert_eq!(
        map["sources"],
        serde_json::json!(["src/settings.ts"]),
        "{map:#}"
    );
    // Function names stay (they are observable), so the minified
    // declaration of `describeSettings` maps to its source line.
    let decoded =
        sourcemap::SourceMap::from_slice(out(temp.path(), "settings.js.map").as_bytes()).unwrap();
    let column = published
        .find("function describeSettings")
        .map(|at| at + "function ".len())
        .expect("function name kept") as u32;
    let token = decoded.lookup_token(0, column).expect("mapped");
    assert_eq!(token.get_source(), Some("src/settings.ts"));
    assert_eq!(token.get_src_line(), 4, "{published}\n{map:#}");
}

/// Chunks that typeopt does not edit are never minified.
#[test]
fn leaves_unedited_chunks_unminified() {
    let mut chunks: Vec<(&str, &str)> = VERBOSE.to_vec();
    let untouched = "export function untouchedHelper(value) {\n  return value + 1;\n}\n";
    chunks.push(("untouched.js", untouched));
    chunks[0] = (
        "index.js",
        r#"import{describeSettings as describe,makeSettings as make}from"./settings.js";
import{untouchedHelper as h}from"./untouched.js";
const firstSettings = make(h(2));
console.log(describe(firstSettings));
"#,
    );
    let temp = bundle(&chunks);
    optimize(
        temp.path(),
        &[
            "--minify",
            "--disable-pass",
            "call-inlining",
            "--disable-pass",
            "signature-reduction",
        ],
    );
    same_behavior(temp.path());
    assert_eq!(out(temp.path(), "untouched.js"), untouched);
}

// ---------------------------------------------------------------------------
// Closed classes
// ---------------------------------------------------------------------------

/// Fields, methods, accessors and `super` calls of closed classes are
/// renamed together across chunks and the class hierarchy.
#[test]
fn renames_closed_class_members_across_chunks() {
    let (temp, report) = run(&[
        (
            "index.js",
            r#"import{Shape as S,Square as Q}from"./shapes.js";const g=new S(1,"green");console.log(g.describeShape());const q=new Q(5,"pink");console.log(q.describeShape(),q.areaOfShape(),q instanceof S,q.shapeLabel);q.shapeLabel="relabelled";console.log(q.describeShape());"#,
        ),
        (
            "shapes.js",
            r#"class Shape{#hiddenCount=0;constructor(s,c){this.sizeOfShape=s;this.colourOfShape=c}areaOfShape(){this.#hiddenCount++;return this.sizeOfShape*this.sizeOfShape}describeShape(){return this.colourOfShape+":"+this.areaOfShape()+":"+this.#hiddenCount}}class Square extends Shape{storedLabel={labelText:"sq"};constructor(n,c){super(n,c)}get shapeLabel(){return this.storedLabel.labelText}set shapeLabel(v){this.storedLabel={labelText:v}}describeShape(){return this.shapeLabel+super.describeShape()}}export{Shape,Square};"#,
        ),
    ]);
    assert!(saved(&report) > 0, "{report:#}");
    let shapes = out(temp.path(), "shapes.js");
    for name in [
        "sizeOfShape",
        "colourOfShape",
        "areaOfShape",
        "describeShape",
        "storedLabel",
        "shapeLabel",
        "labelText",
    ] {
        assert!(!shapes.contains(name), "{name} kept:\n{shapes}");
    }
    assert!(shapes.contains("#hiddenCount"), "{shapes}");
}

/// Named class expressions construct through their inner name, and static
/// factories that are only called directly return tracked instances.
#[test]
fn renames_instances_of_static_factories() {
    let (temp, _) = run(&[(
        "index.js",
        r#"const Store=class e{static create(t){return new e(t,{parentValue:1})}storeOptions;rootValue;constructor(t,n){this.storeOptions=t,this.rootValue=n}describeStore(){return this.storeOptions.optionLabel+this.rootValue.parentValue}};const s=Store.create({optionLabel:"x"});console.log(s.describeStore(),s instanceof Store);export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    for name in ["storeOptions", "rootValue", "describeStore", "optionLabel"] {
        assert!(!index.contains(name), "{name} kept:\n{index}");
    }
}

/// A dynamic import exposes the module namespace, so the classes it exports
/// are reachable without an indexed reference.
#[test]
fn keeps_classes_exported_through_dynamic_namespaces() {
    let (temp, _) = run(&[
        (
            "index.js",
            r#"import{localValue as v}from"./models.js";console.log(v);const ns=await import("./models.js");const key=["Mo","del"].join("");const m=new ns[key];console.log(m.longProperty,m.readLongProperty());"#,
        ),
        (
            "models.js",
            r#"class Model{constructor(){this.longProperty=1}readLongProperty(){return this.longProperty}}const local=new Model;const localValue=local.readLongProperty();export{Model,localValue};"#,
        ),
    ]);
    let models = out(temp.path(), "models.js");
    assert!(models.contains("longProperty"), "{models}");
    assert!(models.contains("readLongProperty"), "{models}");
}

/// Every fixture gives code that the analysis does not own a way to observe
/// an instance member by name, so the names stay.
#[test]
fn keeps_class_members_that_unknown_code_can_observe() {
    let cases: [(&str, &[&str]); 14] = [
        // A method loaded without a call runs with another receiver.
        (
            r#"class W{constructor(){this.fieldName=1}methodName(){return this.fieldName}}const w=new W;const f=w.methodName;console.log(w.methodName(),f.call({fieldName:9}));"#,
            &["fieldName", "methodName"],
        ),
        // Serialization reads every own property name.
        (
            r#"class P{constructor(v){this.payloadValue=v}}console.log(JSON.stringify(new P(1)));"#,
            &["payloadValue"],
        ),
        // A function stored in a field runs with the instance as `this`.
        (
            r#"class Q{constructor(cb){this.callbackFn=cb;this.valueField=3}run(){return this.callbackFn()}}console.log(new Q(function(){return this.valueField}).run());"#,
            &["valueField"],
        ),
        // An escaped instance's methods receive objects from unknown code.
        (
            r#"class R{readValue(o){return o.innerValue}}const r=new R;console.log(r.readValue({innerValue:1}));globalThis.hook=function(x){return x.readValue({innerValue:2})};console.log(globalThis.hook(r));"#,
            &["innerValue", "readValue"],
        ),
        // A base class outside the program reads its own property names.
        (
            r#"class E extends Error{constructor(m){super(m);this.codeValue=2}}try{throw new E("x")}catch(e){console.log(e.codeValue,e.message)}"#,
            &["codeValue"],
        ),
        // `arguments` reaches the arguments without parameters.
        (
            r#"class A{sum(){let t=0;for(const o of arguments)t+=o.partValue;return t}}console.log(new A().sum({partValue:1},{partValue:2}));"#,
            &["partValue"],
        ),
        // A private method loaded without a call runs with another receiver.
        (
            r#"class Pm{#secretHelper(){return this.visibleValue}constructor(){this.visibleValue=4}leak(){return this.#secretHelper}}const p=new Pm;console.log(p.leak().call({visibleValue:5}),p.visibleValue);"#,
            &["visibleValue"],
        ),
        // The prototype exposes method names.
        (
            r#"class Pr{greetPerson(){return 1}}console.log(new Pr().greetPerson(),Object.getOwnPropertyNames(Pr.prototype));"#,
            &["greetPerson"],
        ),
        // A static factory loaded without a call returns instances to
        // unknown callers.
        (
            r#"class L{static make(){return new L}longFieldName=1}const mk=L.make;console.log(mk().longFieldName);"#,
            &["longFieldName"],
        ),
        // An escaped instance reaches the static methods of its class.
        (
            r#"class K{static create(o){return o.optionLabel}keyField=1}console.log(K.create({optionLabel:"a"}));const k=new K;globalThis.hook=function(x){return x.constructor.create({optionLabel:"b"})};console.log(globalThis.hook(k));"#,
            &["optionLabel"],
        ),
        // `Symbol.hasInstance` receives the left operand of `instanceof`.
        (
            r#"class M{static[Symbol.hasInstance](v){return v.matchedValue===1}}const value={matchedValue:1};console.log(value instanceof M);"#,
            &["matchedValue"],
        ),
        (
            r#"globalThis.Matcher={[Symbol.hasInstance]:v=>v.matchedValue===1};const value={matchedValue:1};console.log(value instanceof globalThis.Matcher);"#,
            &["matchedValue"],
        ),
        // `Symbol.name` is the string "Symbol", not a well-known symbol.
        (
            r#"class C{[Symbol.name]=1;otherField=2}const c=new C;console.log(c.Symbol,c.otherField);"#,
            &["Symbol", "otherField"],
        ),
        // A function-valued field names its function.
        (
            r#"class H{handlerFn=()=>this.countValue;countValue=2}const h=new H;console.log(h.handlerFn(),h.handlerFn.name);"#,
            &["handlerFn"],
        ),
    ];
    for (code, names) in cases {
        // An export makes the chunk a module rather than a classic script.
        let code = format!("{code}export{{}};");
        let (temp, _) = run(&[("index.js", &code)]);
        let index = out(temp.path(), "index.js");
        for name in names {
            assert!(index.contains(name), "{name} renamed in {code}:\n{index}");
        }
    }
}

/// Edge cases of class semantics that renaming must not change; each runs
/// in Node before and after optimization.
#[test]
fn keeps_behavior_of_class_member_edge_cases() {
    let cases = [
        // A method called through a string-literal element access.
        r#"class C{make(){return{longProperty:1}}}const r=new C()["make"]();console.log(r.longProperty);"#,
        // The iteration protocol looks up `next` on what
        // `Symbol.iterator` returns.
        r#"class It{constructor(){this.position=0}next(){return this.position<2?{value:this.position++,done:false}:{value:void 0,done:true}}}class Iterable{[Symbol.iterator](){return new It}}for(const v of new Iterable)console.log(v);"#,
        // A later method replaces an accessor of the same name.
        r#"class C{get make(){return()=>({longProperty:0})}make(){return{longProperty:1}}}const r=new C().make();console.log(r.longProperty);"#,
        // A later static field replaces a static method of the same name.
        r#"globalThis.ext=o=>o.longProperty;class C{static call(o){return 0}static call=globalThis.ext}console.log(C.call({longProperty:1}));"#,
        // `Function.prototype` methods hand out the constructor.
        r#"class C{constructor(){this.longProperty=1}}const B=C.bind(null);const v=new B;console.log(v.longProperty);"#,
        // An extracted method returns to callers the analysis does not see.
        r#"class C{make(){return{longProperty:1}}}const make=new C().make;const result=make();console.log(result.longProperty);"#,
        // A base class field shadows a subclass method of the same name.
        r#"globalThis.external=o=>o.longProperty;class Base{action=globalThis.external}class Sub extends Base{action(o){return 0}}console.log(new Sub().action({longProperty:1}));"#,
        // Code can add names to `Object.prototype`.
        r#"Object.prototype.longProperty=1;class C{}console.log(new C().longProperty);"#,
    ];
    for code in cases {
        let code = format!("{code}export{{}};");
        run(&[("index.js", &code)]);
    }
}

// ---------------------------------------------------------------------------
// Arrays
// ---------------------------------------------------------------------------

/// A closed class whose instances the fixtures store in arrays.
const ITEM: &str = "class Item{constructor(v){this.itemValue=v}readItem(){return this.itemValue}}";

/// Runs `body` after the `Item` class as the only chunk and returns the
/// optimized chunk.
fn array_chunk(body: &str) -> (tempfile::TempDir, String) {
    // An export makes the chunk a module rather than a classic script.
    let code = format!("{ITEM}{body}export{{}};");
    let (temp, _) = run(&[("index.js", &code)]);
    let index = out(temp.path(), "index.js");
    (temp, index)
}

/// Arrays that analyzed code creates, fills and reads let the objects stored
/// in them be renamed; every fixture prints the same output in Node.
#[test]
fn renames_objects_stored_in_tracked_arrays() {
    let cases: [(&str, &[&str]); 5] = [
        // P1: push, unshift, for-of, pop, shift, at and length.
        (
            "const list=[];list.push(new Item(1));list.unshift(new Item(2));for(const it of \
             list)console.log(it.readItem());console.log(list.length,list.at(0).itemValue,list.\
             pop().readItem(),list.shift().itemValue);",
            &["itemValue", "readItem"],
        ),
        // P2: proven numeric indexes. Reading a possibly missing element
        // into unknown code would expose it, so only its type is printed.
        (
            "const list=[new Item(1),new Item(2)];for(let \
             i=0;i<list.length;i++)console.log(list[i].readItem());list[list.length]=new \
             Item(3);console.log(list[list.length-1].itemValue,typeof list[9]);",
            &["itemValue", "readItem"],
        ),
        // P3: inline callbacks and the methods that return arrays.
        (
            "const list=[new Item(1),new \
             Item(2)];list.forEach(x=>console.log(x.readItem()));const \
             w=list.map(x=>({wrappedValue:x.itemValue}));console.log(w[0].wrappedValue,list.\
             filter(x=>x.itemValue>1).length,list.find(function(x){return \
             x.itemValue===2}).readItem(),list.some(x=>x.itemValue>1),list.slice(1)[0].itemValue,\
             list.indexOf(list[0]));",
            &["itemValue", "readItem", "wrappedValue"],
        ),
        // P4: an array in a closed class field.
        (
            "class Bag{constructor(){this.items=[]}add(i){this.items.push(i);return \
             this.items[0]}}const b=new Bag;console.log(b.add(new Item(1)).readItem());",
            &["itemValue", "readItem", "items", "add"],
        ),
        // P5: object literal elements and spread.
        (
            "const rows=[{rowLabel:\"a\",rowCount:1}];const \
             all=[...rows,{rowLabel:\"b\",rowCount:2}];for(const r of \
             all)console.log(r.rowLabel,r.rowCount);",
            &["rowLabel", "rowCount"],
        ),
    ];
    for (body, names) in cases {
        let (_, index) = array_chunk(body);
        for name in names {
            assert!(!index.contains(name), "{name} kept in {body}:\n{index}");
        }
    }
}

/// Every fixture lets unknown code, or code the model does not cover, reach
/// an array or its elements, so the names stay.
#[test]
fn keeps_names_of_arrays_that_unknown_code_can_observe() {
    let cases: [(&str, &[&str]); 15] = [
        // N1: an array passed to unknown code.
        (
            "const list=[new \
             Item(1)];globalThis.read=l=>l[0].itemValue;console.log(globalThis.read(list));",
            &["itemValue"],
        ),
        // N2: serialization.
        (
            "console.log(JSON.stringify([{rowLabel:1}]));",
            &["rowLabel"],
        ),
        // N3: a callback with a `this` argument.
        (
            "const list=[new \
             Item(1)];list.forEach(function(x){console.log(this.tag,x.itemValue)},{tag:1});",
            &["itemValue"],
        ),
        // N4: a callback that is not an inline function.
        (
            "globalThis.cb=x=>console.log(x.itemValue);[new Item(1)].forEach(globalThis.cb);",
            &["itemValue"],
        ),
        // N5: an inline callback on a receiver the analysis does not own.
        (
            "const ext={map(f){return f({})}};const \
             r=(globalThis.e??ext).map(x=>({wrappedValue:1}));console.log(r.wrappedValue);",
            &["wrappedValue"],
        ),
        // N6: an escaped array may have an own `push`.
        (
            "const list=[];globalThis.hook=l=>{l.push=x=>console.log(x.itemValue)};globalThis.\
             hook(list);list.push(new Item(1));",
            &["itemValue"],
        ),
        // N7: a hole read reaches a numeric `Object.prototype` member.
        (
            "Object.prototype[1]={itemValue:9};const list=[new \
             Item(1)];console.log(list[1].itemValue);",
            &["itemValue"],
        ),
        // N8: a patched built-in.
        (
            "Array.prototype.push=function(x){console.log(x.itemValue)};[].push(new Item(1));",
            &["itemValue"],
        ),
        // N9: an unproven key.
        (
            "const list=[new Item(1)];const k=globalThis.k??\"0\";console.log(list[k].itemValue);",
            &["itemValue"],
        ),
        // N11: an unmodeled method.
        (
            "const list=[new Item(2),new \
             Item(1)];list.sort((a,b)=>a.itemValue-b.itemValue);console.log(list[0].itemValue);",
            &["itemValue"],
        ),
        // N12: array destructuring.
        (
            "const [first]=[new Item(1)];console.log(Object.keys(first));",
            &["itemValue"],
        ),
        // An element read that code passes to unknown code (a hole read
        // returns undefined, but the analysis cannot tell).
        (
            "const list=[new Item(1),new Item(2)];console.log(list[9]);",
            &["itemValue"],
        ),
        // A callback result that unknown code observes.
        (
            "const list=[new \
             Item(1)];globalThis.out=list.map(x=>({wrappedValue:x.itemValue}));console.\
             log(globalThis.out[0].wrappedValue);",
            &["wrappedValue"],
        ),
        // Array methods are modeled only when the built-in is intact.
        (
            "Object.defineProperty(Array.prototype,\"at\",{value(i){console.log(this[i].\
             itemValue);return this[i]}});const list=[new Item(1)];list.at(0);",
            &["itemValue"],
        ),
        // `Array.prototype` used as a value hides patches from the scan.
        (
            "const P=Array.prototype;P.pop=function(){globalThis.seen=this[0].itemValue;return \
             7};const list=[new Item(1)];console.log(list.pop(),globalThis.seen);",
            &["itemValue"],
        ),
    ];
    for (body, names) in cases {
        let (_, index) = array_chunk(body);
        for name in names {
            assert!(index.contains(name), "{name} renamed in {body}:\n{index}");
        }
    }
}

/// The method that hostile prototype code installs: it observes the element
/// of the receiver by its original name.
const HOSTILE_POP: &str = "function(){globalThis.seen=this[0].itemValue;return 7}";

/// A tracked array whose `pop` is the hostile method. The popped element only
/// reaches `typeof`, so nothing else makes the class escape.
const POP_TAIL: &str =
    "const list=[new Item(1)];const got=list.pop();console.log(typeof got,globalThis.seen);";

/// A tracked array stored at index zero of an empty array, whose prototype
/// chain holds a setter for `0` that reads the stored object by its original
/// name.
const SETTER_TAIL: &str = "const list=[];list[0]=new Item(1);console.log(globalThis.seen);";

/// Every way application code can reach the prototype chain of an array and
/// change what a tracked array does. The prototype-chain exposure rule keeps
/// the names of the stored objects in every spelling.
#[test]
fn keeps_names_when_the_prototype_chain_is_exposed() {
    let pop = |prefix: &str| format!("{prefix}P.pop={HOSTILE_POP};{POP_TAIL}");
    let setter = |prefix: &str| {
        format!(
            "{prefix}Object.defineProperty(P,\"0\",{{set(v){{globalThis.seen=v.itemValue}},\
             configurable:true}});{SETTER_TAIL}"
        )
    };
    let cases: Vec<(&str, String)> = vec![
        // Aliases of `Array.prototype` in every spelling.
        ("dot", pop("const P=Array.prototype;")),
        ("bracket", pop("const P=Array[\"prototype\"];")),
        ("template", pop("const P=Array[`prototype`];")),
        ("computed", pop("const P=Array[\"proto\"+\"type\"];")),
        ("global object", pop("const P=globalThis.Array.prototype;")),
        (
            "global object bracket",
            pop("const P=globalThis[\"Array\"][\"prototype\"];"),
        ),
        ("optional chain", pop("const P=(Array?.prototype);")),
        (
            "constructor alias",
            pop("const A=Array;const P=A.prototype;"),
        ),
        ("destructured", pop("const {prototype:P}=Array;")),
        (
            "destructured computed key",
            pop("const k=\"proto\"+\"type\";const {[k]:P}=Array;"),
        ),
        (
            "destructuring assignment",
            pop("let P;({prototype:P}=Array);"),
        ),
        (
            "returned",
            format!("const get=()=>Array.prototype;get().pop={HOSTILE_POP};{POP_TAIL}"),
        ),
        // Prototypes reached through the object model.
        ("__proto__", pop("const P=[].__proto__;")),
        ("__proto__ bracket", pop("const P=[][\"__proto__\"];")),
        ("getPrototypeOf", pop("const P=Object.getPrototypeOf([]);")),
        (
            "Reflect.getPrototypeOf",
            pop("const P=Reflect.getPrototypeOf([]);"),
        ),
        (
            "constructor.prototype",
            pop("const P=[].constructor.prototype;"),
        ),
        // `Object.prototype` aliases hide indexed accessors.
        (
            "Object.prototype alias",
            setter("const P=Object.prototype;"),
        ),
        (
            "Object.prototype bracket alias",
            setter("const P=Object[\"prototype\"];"),
        ),
        (
            "Object.prototype destructured",
            setter("const {prototype:P}=Object;"),
        ),
        (
            "prototype of the prototype",
            setter("const P=Object.getPrototypeOf(Array.prototype);"),
        ),
        // Callees that are not the real reflection writer.
        (
            "unknown callee",
            format!(
                "const helper={{assign(t){{t.pop={HOSTILE_POP}}}}};helper.assign(Array.prototype);\
                 {POP_TAIL}"
            ),
        ),
        (
            "unknown callee named like a writer",
            format!(
                "const Obj={{defineProperty(t,k,d){{t.pop=d.value}}}};Obj.defineProperty(Array.\
                 prototype,\"pop\",{{value:{HOSTILE_POP}}});{POP_TAIL}"
            ),
        ),
        (
            "shadowed Object",
            format!(
                "function install(Object){{Object.assign(Array.prototype,{{pop:\
                 {HOSTILE_POP}}})}}install({{assign(t,s){{t.pop=s.pop}}}});{POP_TAIL}"
            ),
        ),
        (
            "destructured writer",
            format!(
                "const {{defineProperty:d}}=Object;d(Array.prototype,\"pop\",{{value:\
                 {HOSTILE_POP}}});{POP_TAIL}"
            ),
        ),
        (
            "computed handle as a writer target",
            format!(
                "Object.defineProperty(Object[\"proto\"+\"type\"],\"0\",{{set(v){{globalThis.\
                 seen=v.itemValue}},configurable:true}});{SETTER_TAIL}"
            ),
        ),
        // Writes the scan records, which disable numeric modeling.
        (
            "__proto__ write",
            format!(
                "Array.prototype.__proto__={{set 0(v){{globalThis.seen=v.itemValue}},get \
                 0(){{return void 0}}}};{SETTER_TAIL}"
            ),
        ),
        (
            "setPrototypeOf",
            format!(
                "Object.setPrototypeOf(Array.prototype,{{set \
                 0(v){{globalThis.seen=v.itemValue}},get 0(){{return void 0}}}});{SETTER_TAIL}"
            ),
        ),
        (
            "bracket write",
            format!("Array[\"prototype\"].pop={HOSTILE_POP};{POP_TAIL}"),
        ),
        (
            "computed write",
            format!("const k=\"p\"+\"op\";Array.prototype[k]={HOSTILE_POP};{POP_TAIL}"),
        ),
        (
            "Reflect.defineProperty on a bracket handle",
            format!(
                "Reflect.defineProperty(Array[\"prototype\"],\"pop\",{{value:{HOSTILE_POP}}});\
                 {POP_TAIL}"
            ),
        ),
        // Reflection members of the prototype objects.
        (
            "__defineSetter__",
            format!(
                "Array.prototype.__defineSetter__(\"0\",function(v){{globalThis.seen=v.\
                 itemValue}});{SETTER_TAIL}"
            ),
        ),
        (
            "__defineSetter__ on Object.prototype",
            format!(
                "Object[\"prototype\"].__defineSetter__(\"0\",function(v){{globalThis.seen=v.\
                 itemValue}});{SETTER_TAIL}"
            ),
        ),
    ];
    let renamed: Vec<&str> = cases
        .iter()
        .filter(|(_, body)| !array_chunk(body).1.contains("itemValue"))
        .map(|(label, _)| *label)
        .collect();
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");
}

/// Prototype uses that stay inside the patch scan do not stop the model.
#[test]
fn renames_through_arrays_beside_scanned_prototype_uses() {
    let cases = [
        "console.log(Array.prototype.slice.call(\"abc\").length);",
        "console.log(Object.prototype.hasOwnProperty.call({},\"x\"));",
        "const {hasOwnProperty}=Object.prototype;console.log(hasOwnProperty.call({},\"x\"));",
        "console.log(Object.getPrototypeOf({})===Object.prototype,typeof Array.prototype);",
        "Object.defineProperty(Array.prototype,\"extraMember\",{value(){return \
         1},configurable:true});console.log([].extraMember());",
        "const same=[].constructor===Array;console.log(same);",
        "console.log(Array.isArray([]),Array[\"isArray\"]([]));",
    ];
    for fixture in cases {
        let body = format!(
            "{fixture}const list=[new Item(1)];list.push(new \
             Item(2));console.log(list.length,list[0].readItem());"
        );
        let (_, index) = array_chunk(&body);
        for name in ["itemValue", "readItem"] {
            assert!(!index.contains(name), "{name} kept in {body}:\n{index}");
        }
    }
}

/// Forms that stay inside the intrinsic allowlist do not stop the model: the
/// guards, comparisons and library calls that bundles use every day.
#[test]
fn renames_through_arrays_beside_allowlisted_intrinsic_forms() {
    let cases = [
        // Feature detection and comparisons.
        "console.log(typeof window,typeof Reflect!==\"undefined\",typeof globalThis);",
        "console.log([] instanceof Array,({}) instanceof Object,typeof Function);",
        // Library functions that return neither a handle nor the global object.
        "console.log(Object.keys({a:1}).length,Object.values({a:1})[0],Object.entries({a:1}).\
         length);",
        "console.log(Array.from(\"ab\").length,Array.of(1,2).length,Object.hasOwn({a:1},\"a\"));",
        "const frozen=Object.freeze({fixedValue:1});console.log(frozen.fixedValue,Object.\
         isFrozen(frozen));",
        "const bag=Object.create(null);Object.defineProperty(bag,\"hidden\",{value:1});console.\
         log(Object.getOwnPropertyNames(bag).length);",
        "const app={};Object.assign(app,{extra:1});Object.setPrototypeOf(app,null);console.\
         log(Reflect.ownKeys(app).length,Reflect.has(app,\"extra\"));",
        // Constructors only compared or asked for their name.
        "class Named{label(){return this.constructor.name}}console.log(new Named().label());",
        "class Kind{}console.log(new Kind().constructor===Kind);",
        // Strict `this`, ordinary members of the global object and timers.
        "function strictThis(){\"use strict\";return this}console.log(strictThis());",
        "globalThis.flag=1;console.log(globalThis.flag);",
        "setTimeout(()=>console.log(\"tick\"),0);",
        // A literal member of a prototype that no write replaces.
        "console.log(typeof Array.prototype.map,Object.prototype.toString.call(1));",
    ];
    for fixture in cases {
        let body = format!(
            "{fixture}const list=[new Item(1)];list.push(new \
             Item(2));console.log(list.length,list[0].readItem());"
        );
        let (_, index) = array_chunk(&body);
        for name in ["itemValue", "readItem"] {
            assert!(!index.contains(name), "{name} kept in {body}:\n{index}");
        }
    }
}

/// Every route to a shared built-in that the allowlist does not name keeps the
/// names, in every spelling the review rounds reported (patched writers,
/// `Reflect` and `Object` reflection getters, `__proto__`, `getPrototypeOf`,
/// `setPrototypeOf`, `constructor`, dynamic and concatenated keys, the global
/// object under every alias) and the generalizations of each. The hostile
/// payload reads the stored object by its original name, so a wrong rename
/// also changes the output that `run` compares.
#[test]
fn keeps_names_for_every_intrinsic_spelling() {
    let pop = |prefix: &str| format!("{prefix}P.pop={HOSTILE_POP};{POP_TAIL}");
    let setter = |prefix: &str| {
        format!(
            "{prefix}Object.defineProperty(P,\"0\",{{set(v){{globalThis.seen=v.itemValue}},\
             configurable:true}});{SETTER_TAIL}"
        )
    };
    let writer_result = format!(
        "const P=Object.defineProperty(Array.prototype,\"extraMember\",{{value(){{return \
         1}},configurable:true}});P.pop={HOSTILE_POP};{POP_TAIL}"
    );
    let cases: Vec<(&str, String)> = vec![
        // Thread: the writer check accepted a replaced `Object.defineProperty`.
        (
            "defineProperty replaced while Object is intact",
            format!(
                "Object.defineProperty=function(t,k,d){{t[k]=d.value;return \
                 t}};Object.defineProperty(Array.prototype,\"pop\",{{value:{HOSTILE_POP}}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "Object replaced and defineProperty replaced",
            format!(
                "globalThis.Object={{defineProperty(t,k,d){{t[k]=d.value;return \
                 t}}}};Object.defineProperty(Array.prototype,\"pop\",{{value:{HOSTILE_POP}}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "Reflect.defineProperty replaced",
            format!(
                "Reflect.defineProperty=function(t,k,d){{t[k]=d.value;return \
                 true}};Reflect.defineProperty(Array.prototype,\"pop\",{{value:{HOSTILE_POP}}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "Reflect.set replaced",
            format!(
                "Reflect.set=function(t,k,v){{t[k]=v;return \
                 true}};Reflect.set(Array.prototype,\"pop\",{HOSTILE_POP});{POP_TAIL}"
            ),
        ),
        // Thread: reflection getters were not recognized as handles.
        (
            "Reflect.get __proto__",
            pop("const P=Reflect.get([],\"__proto__\");"),
        ),
        (
            "Reflect.get concatenated key",
            pop("const P=Reflect.get([],\"__pro\"+\"to__\");"),
        ),
        (
            "Reflect.getPrototypeOf alias",
            pop("const g=Reflect.getPrototypeOf;const P=g([]);"),
        ),
        (
            "Object.getPrototypeOf call",
            pop("const P=Object.getPrototypeOf.call(null,[]);"),
        ),
        (
            "Object.getOwnPropertyDescriptor value of Array",
            pop("const P=Object.getOwnPropertyDescriptor(Array,\"prototype\").value;"),
        ),
        (
            "Object.getOwnPropertyDescriptor value of the prototype",
            pop(
                "const P=Object.getOwnPropertyDescriptor(Array.prototype,\"constructor\").value.\
                 prototype;",
            ),
        ),
        (
            "Object.getOwnPropertyDescriptors __proto__ getter",
            pop(
                "const P=Object.getOwnPropertyDescriptors(Object.prototype)[\"__proto__\"].get.\
                 call([]);",
            ),
        ),
        (
            "__lookupGetter__",
            pop("const P=[].__lookupGetter__(\"__proto__\").call([]);"),
        ),
        (
            "__lookupGetter__ concatenated name",
            pop("const P=[][\"__lookup\"+\"Getter__\"](\"__proto__\").call([]);"),
        ),
        (
            "Proxy of an array",
            pop("const P=Object.getPrototypeOf(new Proxy([],{}));"),
        ),
        // Writer results are handles too.
        ("writer result kept", writer_result),
        (
            "writer in an arrow body",
            pop(
                "const install=()=>Object.defineProperty(Array.prototype,\"extraMember\",\
                 {value(){return 1},configurable:true});const P=install();",
            ),
        ),
        (
            "Object.assign result kept",
            pop("const P=Object.assign(Array.prototype,{});"),
        ),
        (
            "Object.assign with a static key",
            format!("Object.assign(Array.prototype,{{pop:{HOSTILE_POP}}});{POP_TAIL}"),
        ),
        (
            "Object.defineProperties with a static key",
            format!(
                "Object.defineProperties(Array.prototype,{{pop:{{value:{HOSTILE_POP}}}}});\
                 {POP_TAIL}"
            ),
        ),
        // Dynamic keys and string concatenation.
        (
            "dynamic constructor key",
            pop("const k=\"con\"+\"structor\";const P=[][k].prototype;"),
        ),
        (
            "dynamic key from a function",
            pop("const key=()=>\"constr\"+\"uctor\";const P=[][key()].prototype;"),
        ),
        (
            "dynamic key from a parameter",
            pop("const get=(o,k)=>o[k];const P=get([],\"constructor\").prototype;"),
        ),
        (
            "dynamic key from a template",
            pop("const t=\"structor\";const P=[][`con${t}`].prototype;"),
        ),
        (
            "dynamic key on an object literal",
            setter("const P={}[\"con\"+\"structor\"].prototype;"),
        ),
        (
            "concatenated __proto__",
            pop("const k=\"__pro\"+\"to__\";const P=[][k];"),
        ),
        (
            "concatenated prototype on Array",
            format!("Array[\"proto\"+\"type\"].pop={HOSTILE_POP};{POP_TAIL}"),
        ),
        // Constructors read from values.
        (
            "constructor of a function",
            format!(
                "[].map.constructor(\"return Array.prototype\")().pop={HOSTILE_POP};{POP_TAIL}"
            ),
        ),
        (
            "Function constructor",
            format!("Function(\"return Array.prototype\")().pop={HOSTILE_POP};{POP_TAIL}"),
        ),
        (
            "constructor of an object literal",
            setter("const P=({}).constructor.prototype;"),
        ),
        (
            "optional chain constructor",
            pop("const P=[]?.constructor?.prototype;"),
        ),
        (
            "constructor destructured",
            pop("const {constructor:C}=[];const P=C.prototype;"),
        ),
        ("__proto__ destructured", pop("const {__proto__:P}=[];")),
        (
            "__proto__ destructured with a concatenated key",
            pop("const {[\"__pro\"+\"to__\"]:P}=[];"),
        ),
        // The global object under every alias.
        ("global", pop("const P=global.Array.prototype;")),
        (
            "global object concatenated name",
            pop("const P=globalThis[\"Ar\"+\"ray\"].prototype;"),
        ),
        (
            "global object destructured",
            pop("const {Array:A}=globalThis;const P=A.prototype;"),
        ),
        (
            "global object kept in a variable",
            pop("const g=globalThis;const P=g.Array.prototype;"),
        ),
        (
            "global object passed through a function",
            pop("const id=x=>x;const P=id(globalThis).Array.prototype;"),
        ),
        (
            "Reflect.get of the global object",
            pop("const P=Reflect.get(globalThis,\"Array\").prototype;"),
        ),
        // Aliases of the constructors themselves.
        (
            "Object kept in a variable",
            setter("const O=Object;const P=O.prototype;"),
        ),
        (
            "Array in an array",
            pop("const [A]=[Array];const P=A.prototype;"),
        ),
        (
            "Array as a property value",
            pop("const holder={A:Array};const P=holder.A.prototype;"),
        ),
        (
            "Array as a call argument",
            pop("const use=A=>A.prototype;const P=use(Array);"),
        ),
        (
            "Array extended",
            pop("class Mine extends Array{}const P=Object.getPrototypeOf(Mine.prototype);"),
        ),
    ];
    let renamed: Vec<&str> = cases
        .iter()
        .filter(|(_, body)| !array_chunk(body).1.contains("itemValue"))
        .map(|(label, _)| *label)
        .collect();
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");
}

/// A tracked array that only `push` and an index read touch.
const LIST_TAIL: &str =
    "const list=[new Item(1)];list.push(new Item(2));console.log(list.length,list[0].readItem());";

/// A tracked array whose `slice` allocates through its `constructor`.
const SLICE_TAIL: &str = "const list=[new Item(1)];console.log(list.slice(0)[0].readItem());";

/// Whether the tracked `Item` names survive in the optimized `body`.
fn item_names_kept(body: &str) -> bool {
    array_chunk(body).1.contains("itemValue")
}

/// A call of a member of the global object passes the global object as
/// `this`. The inherited `Object.prototype` methods return it (`valueOf`), so
/// `globalThis.valueOf()` is a handle to the global object and, through
/// `.Array.prototype`, to every built-in. Only a short list of functions that
/// return a primitive may be called, and only while nothing replaces them.
#[test]
fn keeps_names_beside_calls_of_global_object_members() {
    let patch_through = |call: &str| {
        format!("const g={call};const P=g.Array.prototype;P.pop={HOSTILE_POP};{POP_TAIL}")
    };
    let rejected: Vec<(&str, String)> = vec![
        // The reviewed spelling and its aliases of the global object.
        ("valueOf", patch_through("globalThis.valueOf()")),
        (
            "valueOf bracket",
            patch_through("globalThis[\"valueOf\"]()"),
        ),
        ("valueOf global", patch_through("global.valueOf()")),
        (
            "valueOf parenthesized",
            patch_through("(globalThis.valueOf)()"),
        ),
        (
            "valueOf optional call",
            patch_through("globalThis.valueOf?.()"),
        ),
        (
            "valueOf window",
            patch_through("(typeof window===\"undefined\"?globalThis:window).valueOf()"),
        ),
        // Every other inherited method, without a hostile payload: the call
        // alone turns modeling off.
        ("toString", format!("globalThis.toString();{LIST_TAIL}")),
        (
            "toLocaleString",
            format!("globalThis.toLocaleString();{LIST_TAIL}"),
        ),
        (
            "constructor",
            format!("globalThis.constructor();{LIST_TAIL}"),
        ),
        (
            "__lookupGetter__",
            format!("globalThis.__lookupGetter__(\"x\");{LIST_TAIL}"),
        ),
        (
            "__lookupSetter__",
            format!("globalThis.__lookupSetter__(\"x\");{LIST_TAIL}"),
        ),
        (
            "valueOf in statement position",
            format!("globalThis.valueOf();{LIST_TAIL}"),
        ),
        (
            "valueOf tagged template",
            format!("globalThis.valueOf``;{LIST_TAIL}"),
        ),
        // A member that application code defines can return its `this`.
        (
            "application function",
            format!("globalThis.helper=function(){{return 1}};globalThis.helper();{LIST_TAIL}"),
        ),
        (
            "strict application function",
            format!(
                "globalThis.helper=function(){{\"use strict\";return \
                 this}};globalThis.helper();{LIST_TAIL}"
            ),
        ),
        // The allowlisted names stop being handle-free once replaced.
        (
            "hasOwnProperty replaced on the prototype",
            format!(
                "Object.prototype.hasOwnProperty=function(){{return \
                 true}};globalThis.hasOwnProperty(\"x\");{LIST_TAIL}"
            ),
        ),
        (
            "hasOwnProperty replaced on the global object",
            format!(
                "globalThis.hasOwnProperty=function(){{return \
                 true}};globalThis.hasOwnProperty(\"x\");{LIST_TAIL}"
            ),
        ),
        (
            "hasOwnProperty replaced through a parameter",
            format!(
                "function swap(o){{o.hasOwnProperty=function(){{return \
                 true}}}}swap({{}});globalThis.hasOwnProperty(\"x\");{LIST_TAIL}"
            ),
        ),
        (
            "parseInt replaced",
            format!(
                "globalThis.parseInt=function(){{return 1}};globalThis.parseInt(\"1\");{LIST_TAIL}"
            ),
        ),
    ];
    let renamed: Vec<&str> = rejected
        .iter()
        .filter(|(_, body)| !item_names_kept(body))
        .map(|(label, _)| *label)
        .collect();
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");

    // Handle-free calls and reads of the global object do not stop the model.
    let allowed = [
        "globalThis.hasOwnProperty(\"x\");",
        "globalThis.isPrototypeOf({});",
        "globalThis.propertyIsEnumerable(\"x\");",
        "globalThis[\"hasOwnProperty\"](\"x\");",
        "console.log(globalThis.parseInt(\"1\"),globalThis.isNaN(1),globalThis.\
         encodeURIComponent(\"a\"));",
        "globalThis.console.log(1);",
        "console.log(typeof globalThis.valueOf,globalThis.valueOf===undefined);",
        "const read=globalThis.toString;console.log(typeof read);",
        "console.log(new globalThis.Date(0).getTime());",
        "globalThis.flag=1;console.log(globalThis.flag);",
    ];
    let kept: Vec<&str> = allowed
        .iter()
        .filter(|fixture| item_names_kept(&format!("{fixture}{LIST_TAIL}")))
        .copied()
        .collect();
    assert!(kept.is_empty(), "names kept beside: {kept:?}");
}

/// A bracketed writer on a named intrinsic handle is an explicit escape that
/// the intrinsic allowlist rejects (only the dot form is a verified writer), so
/// the names survive without help from the receiver route. The external
/// receiver route is covered by `keeps_names_for_writes_through_unproven_
/// receivers`, whose fixtures never name an intrinsic.
#[test]
fn keeps_names_for_bracketed_writers_on_named_intrinsics() {
    let cases: Vec<(&str, String)> = vec![
        (
            "bracketed defineProperty on Array.prototype",
            format!(
                "Object[\"defineProperty\"](Array.prototype,\"pop\",{{value:{HOSTILE_POP}}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "bracketed Reflect.set on Array.prototype",
            format!("Reflect[\"set\"](Array.prototype,\"pop\",{HOSTILE_POP});{POP_TAIL}"),
        ),
    ];
    let renamed: Vec<&str> = cases
        .iter()
        .filter(|(_, body)| !item_names_kept(body))
        .map(|(label, _)| *label)
        .collect();
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");
}

/// A property write, deletion or define-like call through a receiver that is
/// not provably a local object may patch an intrinsic that a caller supplied
/// (`install(Array.prototype, hostile)` from an importer), whatever the
/// binding spelling. The write is recorded for its key, so `pop` stops being
/// modeled and the tracked array that pops keeps its element names.
#[test]
fn keeps_names_for_writes_through_unproven_receivers() {
    let hostile = "function(){return 7}";
    let cases: Vec<(&str, String)> = vec![
        // Parameters of exported and ordinary functions.
        (
            "exported function parameter",
            format!("export function install(p){{p.pop={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "exported arrow parameter",
            format!("export const install=p=>{{p.pop={hostile}}};install({{}});{POP_TAIL}"),
        ),
        (
            "internal function parameter",
            format!("function install(p){{p.pop={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "default parameter",
            format!("function install(p={{}}){{p.pop={hostile}}}install();{POP_TAIL}"),
        ),
        (
            "object-destructured parameter",
            format!(
                "function install({{target}}){{target.pop={hostile}}}install({{target:{{}}}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "array-destructured parameter",
            format!("function install([t]){{t.pop={hostile}}}install([{{}}]);{POP_TAIL}"),
        ),
        (
            "rest parameter",
            format!("function install(...ps){{ps[0].pop={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "arguments object",
            format!("function install(){{arguments[0].pop={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "closure over a parameter",
            format!(
                "function make(p){{return function(){{p.pop={hostile}}}}}make({{}})();{POP_TAIL}"
            ),
        ),
        // Callbacks that unknown callers fill.
        (
            "callback parameter",
            format!("setTimeout(p=>{{p.pop={hostile}}},0,{{}});{POP_TAIL}"),
        ),
        (
            "callback stored for unknown callers",
            format!("const cb=p=>{{p.pop={hostile}}};cb({{}});export{{cb}};{POP_TAIL}"),
        ),
        (
            "iteration variable",
            format!("for(const p of [{{}}]){{p.pop={hostile}}}{POP_TAIL}"),
        ),
        // Class methods and `this` chosen by the caller.
        (
            "class method parameter",
            format!(
                "class Installer{{install(p){{p.pop={hostile}}}}}new \
                 Installer().install({{}});{POP_TAIL}"
            ),
        ),
        (
            "class method this",
            format!("class Patcher{{run(){{this.pop={hostile}}}}}new Patcher().run();{POP_TAIL}"),
        ),
        (
            "static method this",
            format!("class Patcher{{static run(){{this.pop={hostile}}}}}Patcher.run();{POP_TAIL}"),
        ),
        (
            "accessor this",
            format!(
                "class Patcher{{set value(v){{this.pop={hostile}}}}}new \
                 Patcher().value=1;{POP_TAIL}"
            ),
        ),
        (
            "function this",
            format!("function run(){{\"use strict\";this.pop={hostile}}}run.call({{}});{POP_TAIL}"),
        ),
        (
            "object method this",
            format!("const o={{run(){{this.pop={hostile}}}}};o.run();{POP_TAIL}"),
        ),
        // Receivers read from other values or returned by calls.
        (
            "property read",
            format!("const holder={{p:{{}}}};holder.p.pop={hostile};{POP_TAIL}"),
        ),
        (
            "call result",
            format!("JSON.parse(\"{{}}\").pop={hostile};{POP_TAIL}"),
        ),
        (
            "reassigned variable",
            format!("let o={{}};o=Object.create(null);o.pop={hostile};{POP_TAIL}"),
        ),
        (
            "alias of a parameter",
            format!("function install(p){{const q=p;q.pop={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "conditional with a parameter",
            format!(
                "function install(p){{const q=p||{{}};q.pop={hostile}}}install({{}});{POP_TAIL}"
            ),
        ),
        // Every write shape.
        (
            "bracket write",
            format!("function install(p){{p[\"pop\"]={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "computed write",
            format!("function install(p,k){{p[k]={hostile}}}install({{}},\"x\");{POP_TAIL}"),
        ),
        (
            "compound write",
            format!("function install(p){{p.pop||={hostile}}}install({{}});{POP_TAIL}"),
        ),
        (
            "destructuring assignment target",
            format!("function install(p){{({{a:p.pop}}={{a:{hostile}}})}}install({{}});{POP_TAIL}"),
        ),
        (
            "delete",
            format!("function install(p){{delete p.pop}}install({{pop:1}});{POP_TAIL}"),
        ),
        (
            "defineProperty",
            format!(
                "function install(p){{Object.defineProperty(p,\"pop\",{{value:\
                 {hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "Object.assign",
            format!(
                "function install(p){{Object.assign(p,{{pop:{hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "Reflect.set",
            format!(
                "function install(p){{Reflect.set(p,\"pop\",{hostile})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "Reflect.deleteProperty",
            format!(
                "function install(p){{Reflect.deleteProperty(p,\"pop\")}}install({{}});{POP_TAIL}"
            ),
        ),
        // Bracketed, template and parenthesized spellings of a writer name
        // record the same evidence as the dot form.
        (
            "bracketed defineProperty",
            format!(
                "function install(p){{Object[\"defineProperty\"](p,\"pop\",{{value:\
                 {hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "bracketed Object.assign",
            format!(
                "function install(p){{Object[\"assign\"](p,{{pop:{hostile}}})}}install({{}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "bracketed Reflect.set",
            format!(
                "function install(p){{Reflect[\"set\"](p,\"pop\",{hostile})}}install({{}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "bracketed Reflect.defineProperty",
            format!(
                "function install(p){{Reflect[\"defineProperty\"](p,\"pop\",{{value:\
                 {hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "bracketed Reflect.deleteProperty",
            format!(
                "function install(p){{Reflect[\"deleteProperty\"](p,\"pop\")}}install({{}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "template writer name",
            format!(
                "function install(p){{Object[`defineProperty`](p,\"pop\",{{value:\
                 {hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "parenthesized receiver and writer name",
            format!(
                "function install(p){{(Object)[(\"defineProperty\")](p,\"pop\",{{value:\
                 {hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "template key of a bracketed defineProperty",
            format!(
                "function install(p){{Object[\"defineProperty\"](p,`pop`,{{value:\
                 {hostile}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "bracketed defineProperties",
            format!(
                "function install(p){{Object[\"defineProperties\"](p,{{pop:{{value:\
                 {hostile}}}}})}}install({{}});{POP_TAIL}"
            ),
        ),
        (
            "bracketed alias that is only called",
            format!(
                "function install(p){{const \
                 d=Object[\"defineProperty\"];d(p,\"pop\",{{value:{hostile}}})}}install({{}});\
                 {POP_TAIL}"
            ),
        ),
        (
            "bracketed alias with an unknown key",
            format!(
                "function install(p,k){{const \
                 d=Object[\"defineProperty\"];d(p,k,{{value:1}})}}install({{}},\"x\");{LIST_TAIL}"
            ),
        ),
        (
            "bracketed defineProperty with an unknown key",
            format!(
                "function install(p,k){{Object[\"defineProperty\"](p,k,{{value:1}})}}install({{}},\
                 \"x\");{LIST_TAIL}"
            ),
        ),
        (
            "bracketed defineProperty with a numeric key",
            format!(
                "function install(p){{Object[\"defineProperty\"](p,0,{{value:1}})}}install({{}});\
                 {LIST_TAIL}"
            ),
        ),
        (
            "bracketed setPrototypeOf",
            format!(
                "function install(p){{Object[\"setPrototypeOf\"](p,null)}}install({{}});\
                 {LIST_TAIL}"
            ),
        ),
        (
            "bracketed Reflect.setPrototypeOf",
            format!(
                "function install(p){{Reflect[\"setPrototypeOf\"](p,null)}}install({{}});\
                 {LIST_TAIL}"
            ),
        ),
        // Keys that change the meaning of element reads and writes turn the
        // whole model off, not one method.
        (
            "numeric key",
            format!("function install(p){{p[0]=1}}install({{}});{LIST_TAIL}"),
        ),
        (
            "unknown key",
            format!("function install(p,k){{p[k]=1}}install({{}},\"x\");{LIST_TAIL}"),
        ),
        (
            "__proto__ key",
            format!("function install(p){{p.__proto__={{}}}}install({{}});{LIST_TAIL}"),
        ),
        (
            "setPrototypeOf",
            format!(
                "function install(p){{Object.setPrototypeOf(p,null)}}install({{}});{LIST_TAIL}"
            ),
        ),
        (
            "Object.assign with spread keys",
            format!("function install(p,s){{Object.assign(p,s)}}install({{}},{{}});{LIST_TAIL}"),
        ),
        // Other members the array model relies on.
        (
            "push",
            format!("function install(p){{p.push={hostile}}}install({{}});{LIST_TAIL}"),
        ),
        (
            "constructor",
            format!("function install(p){{p.constructor={hostile}}}install({{}});{SLICE_TAIL}"),
        ),
        (
            "next",
            format!(
                "function install(p){{p.next={hostile}}}install({{}});for(const x of [new \
                 Item(1)])console.log(x.readItem());"
            ),
        ),
    ];
    let renamed: Vec<&str> = cases
        .iter()
        .filter(|(_, body)| !item_names_kept(body))
        .map(|(label, _)| *label)
        .collect();
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");
}

/// Writes whose receiver is provably a fresh local object, and writes through
/// any receiver whose key no array operation relies on, leave the model on.
#[test]
fn renames_beside_writes_through_provably_local_receivers() {
    let hostile = "function(){return 7}";
    let cases: Vec<(&str, String)> = vec![
        (
            "object literal variable",
            format!("const o={{}};o.pop={hostile};o[\"k\"+1]=2;delete o.pop;"),
        ),
        (
            "array literal variable",
            format!("const a=[];a.pop={hostile};a[0]=1;a[a.length]=2;"),
        ),
        (
            "let variable never reassigned",
            format!("let o={{}};o.pop={hostile};"),
        ),
        (
            "alias chain",
            format!("const a={{}};const b=a;const c=b;c.pop={hostile};c[\"x\"+1]=1;"),
        ),
        (
            "conditional of locals",
            format!("const o=Math.random()>2?{{}}:[];o.pop={hostile};"),
        ),
        (
            "logical of locals",
            format!("const o=null||{{}};o.pop={hostile};"),
        ),
        (
            "function statics",
            format!("function helper(){{}}helper.pop={hostile};helper[\"k\"+1]=1;"),
        ),
        (
            "class statics",
            format!("class Registry{{}}Registry.pop={hostile};Registry[\"k\"+1]=1;"),
        ),
        (
            "new of an application class",
            format!("const it=new Item(2);it.pop={hostile};"),
        ),
        (
            "new of a library class",
            format!("const m=new Map();m.pop={hostile};m[\"k\"+1]=1;"),
        ),
        (
            "constructor this",
            format!(
                "class Box{{constructor(){{this.pop={hostile};this[\"k\"+1]=1;Object.assign(this,\
                 {{boxValue:1}})}}}}new Box();"
            ),
        ),
        (
            "field initializer this",
            "class Box{field=(this[\"k\"+1]=1)}new Box();".to_owned(),
        ),
        (
            "static block this",
            "class Box{static{this[\"k\"+1]=1}}".to_owned(),
        ),
        (
            "derived constructor this",
            "class Base{}class Derived extends Base{constructor(){super();this[\"k\"+1]=1}}new \
             Derived();"
                .to_owned(),
        ),
        (
            "Object.assign onto a local",
            "const o={};Object.assign(o,{a:1},{b:2});".to_owned(),
        ),
        (
            "defineProperty onto a local",
            format!("const o={{}};Object.defineProperty(o,\"pop\",{{value:{hostile}}});"),
        ),
        (
            "bracketed defineProperty onto a local",
            format!("const o={{}};Object[\"defineProperty\"](o,\"pop\",{{value:{hostile}}});"),
        ),
        (
            "bracketed Reflect.set onto a local",
            format!("const o={{}};Reflect[`set`](o,\"pop\",{hostile});"),
        ),
        (
            "bracketed setPrototypeOf onto a local",
            "const o={};Object[\"setPrototypeOf\"](o,null);".to_owned(),
        ),
        (
            "shadowed Object with a bracketed writer",
            format!(
                "function install(p){{const \
                 Object={{defineProperty(a,b,c){{}}}};Object[\"defineProperty\"](p,\"pop\",\
                 {{value:{hostile}}})}}install({{}});"
            ),
        ),
        (
            "shadowed Reflect with a bracketed writer",
            format!(
                "function install(p,Reflect){{Reflect[\"set\"](p,\"pop\",{hostile})}}install({{}},\
                 {{set(){{}}}});"
            ),
        ),
        (
            "benign key through a bracketed defineProperty on a parameter",
            "function tag(o){Object[\"defineProperty\"](o,\"tag\",{value:1})}tag({});".to_owned(),
        ),
        // A parameter write with a key that no array operation relies on is a
        // possible patch of that key only.
        (
            "benign key on a parameter",
            "function label(o,n){o.label=n}label({},1);".to_owned(),
        ),
        (
            "benign key on this of a method",
            "class Counter{count=0;bump(){this.count=this.count+1}}new Counter().bump();"
                .to_owned(),
        ),
        (
            "benign key on a property read",
            "const holder={inner:{}};holder.inner.tag=1;".to_owned(),
        ),
        (
            "benign key through Object.defineProperty on a parameter",
            "function tag(o){Object.defineProperty(o,\"tag\",{value:1})}tag({});".to_owned(),
        ),
        (
            "delete of a benign key",
            "function drop(o){delete o.tag}drop({tag:1});".to_owned(),
        ),
    ];
    let kept: Vec<&str> = cases
        .iter()
        .filter(|(_, fixture)| item_names_kept(&format!("{fixture}{LIST_TAIL}")))
        .map(|(label, _)| *label)
        .collect();
    assert!(kept.is_empty(), "names kept beside: {kept:?}");
}

/// Whether the tracked `Item` names survive in `body`, optimized without
/// running it: fixtures with syntax that Node 24 cannot execute (decorators).
fn item_names_kept_unrun(body: &str) -> bool {
    let code = format!("{ITEM}{body}export{{}};");
    let temp = bundle(&[("index.js", &code)]);
    optimize(temp.path(), &[]);
    out(temp.path(), "index.js").contains("itemValue")
}

/// The labels of `cases` whose tracked names `kept` reports as renamed, and
/// the labels where they survived.
fn partition_by_kept(
    cases: &[(&str, String)],
    kept: impl Fn(&str) -> bool,
) -> (Vec<String>, Vec<String>) {
    let mut survived = Vec::new();
    let mut renamed = Vec::new();
    for (label, body) in cases {
        if kept(body) {
            survived.push((*label).to_owned());
        } else {
            renamed.push((*label).to_owned());
        }
    }
    (survived, renamed)
}

/// A timer runs a string as code unless its callback is a function at run
/// time. The checker's type is not that evidence: a string that is asserted
/// (`@type {() => void}`) or annotated as callable has a call signature. Only
/// a function expression or a reference to an unassigned function qualifies.
#[test]
fn keeps_names_for_timer_callbacks_without_runtime_evidence() {
    let cases: Vec<(&str, String)> = vec![
        (
            "asserted union of a string and a function",
            format!(
                "const code=Math.random()>2?\"0\":()=>0;setTimeout(/** @type {{() => void}} */ \
                 (code),0);{POP_TAIL}"
            ),
        ),
        (
            "callback parameter",
            format!("function later(cb){{setTimeout(cb,0)}}later(()=>0);{POP_TAIL}"),
        ),
        (
            "callback property",
            format!("const job={{run:()=>0}};setTimeout(job.run,0);{POP_TAIL}"),
        ),
        (
            "callback call result",
            format!("function pick(){{return()=>0}}setTimeout(pick(),0);{POP_TAIL}"),
        ),
        (
            "reassigned function variable",
            format!("let tick=()=>0;tick=()=>1;setTimeout(tick,0);{POP_TAIL}"),
        ),
        (
            "variable initialized with a conditional",
            format!("const tick=Math.random()>2?\"0\":()=>0;setTimeout(tick,0);{POP_TAIL}"),
        ),
        (
            "reassigned function declaration",
            format!("function tick(){{}}tick=()=>1;setTimeout(tick,0);{POP_TAIL}"),
        ),
    ];
    let (survived, renamed) = partition_by_kept(&cases, item_names_kept);
    assert_eq!(survived.len(), cases.len(), "renamed for: {renamed:?}");
    // Neighbors: a function expression or an unassigned function reference is
    // runtime evidence and leaves array modeling on.
    let accepted = [
        ("arrow", "setTimeout(()=>0,0);"),
        ("function expression", "setTimeout(function(){},0);"),
        ("parenthesized arrow", "setTimeout((()=>0),0);"),
        (
            "arrow with a type cast around the function",
            "setTimeout(/** @type {() => void} */ (()=>0),0);",
        ),
        (
            "function held in a variable behind a cast",
            "const code=/** @type {unknown} */ (()=>0);setTimeout(/** @type {() => void} */ \
             (code),0);",
        ),
        (
            "function declaration",
            "function tick(){}setTimeout(tick,0);",
        ),
        ("const arrow", "const tick=()=>0;setTimeout(tick,0);"),
        (
            "var function expression",
            "var tick=function(){};setTimeout(tick,0);",
        ),
        ("arrow with extra arguments", "setTimeout(a=>0,0,1);"),
    ];
    for (label, fixture) in accepted {
        let body = format!("{fixture}{LIST_TAIL}");
        assert!(!item_names_kept(&body), "names kept beside {label}");
    }
}

/// A computed key is harmless only when its runtime value is a number, a
/// symbol or a harmless string. The checker's type is a claim: an assertion
/// (`"constructor" as unknown as number`) or an `any` value flowing into a
/// number-typed variable makes the claim false, and `a[k]` then reads
/// `Array`, a handle to every built-in.
#[test]
fn keeps_names_for_computed_keys_that_only_a_type_assertion_proves() {
    let patch_through =
        |setup: &str| format!("{setup}const A=[][k];A.prototype.pop={HOSTILE_POP};{POP_TAIL}");
    let number_of_unknown = "/** @type {number} */ (/** @type {unknown} */ (\"constructor\"))";
    let cases: Vec<(&str, String)> = vec![
        (
            "const asserted through unknown",
            patch_through(&format!("const k={number_of_unknown};")),
        ),
        (
            "key asserted at the access",
            format!("const A=[][{number_of_unknown}];A.prototype.pop={HOSTILE_POP};{POP_TAIL}"),
        ),
        (
            "any value asserted to a number",
            patch_through("const k=/** @type {number} */ (JSON.parse('\"constructor\"'));"),
        ),
        (
            "alias of an asserted constant",
            patch_through(&format!("const raw={number_of_unknown};const k=raw;")),
        ),
        (
            "variable assigned an asserted value later",
            patch_through(&format!("let k=0;k={number_of_unknown};")),
        ),
        (
            "variable assigned an any value later",
            patch_through("let k=0;k=JSON.parse('\"constructor\"');"),
        ),
        (
            "conditional with an asserted branch",
            patch_through(&format!("const k=Math.random()>2?1:{number_of_unknown};")),
        ),
        (
            "logical with an asserted operand",
            patch_through(&format!("const k=Math.random()>2||{number_of_unknown};")),
        ),
        (
            "comma with an asserted result",
            patch_through(&format!("const k=(0,{number_of_unknown});")),
        ),
        (
            "destructuring key",
            format!(
                "const k={number_of_unknown};const \
                 {{[k]:A}}=[];A.prototype.pop={HOSTILE_POP};{POP_TAIL}"
            ),
        ),
    ];
    let (survived, renamed) = partition_by_kept(&cases, item_names_kept);
    assert_eq!(survived.len(), cases.len(), "renamed for: {renamed:?}");
    // Neighbors: keys whose runtime value is a number, with or without a
    // harmless assertion, and literal keys, leave array modeling on.
    let accepted = [
        ("numeric literal", "const k=1;const v=[1,2][k];"),
        ("loop counter", "let t=0;for(let k=0;k<3;k++)t+=[1,2,3][k];"),
        (
            "number cast of a number",
            "const k=/** @type {number} */ (Number(\"1\"));const v=[1,2][k];",
        ),
        (
            "cast of a numeric literal",
            "const k=/** @type {number} */ (1);const v=[1,2][k];",
        ),
        (
            "conditional of numbers",
            "const k=Math.random()>2?1:2;const v=[1,2,3][k];",
        ),
        (
            "variable assigned numbers",
            "let k=0;k=2;k+=1;const v=[1,2,3,4][k];",
        ),
        ("length arithmetic", "const a=[1,2];const v=a[a.length-1];"),
        (
            "symbol key",
            "const s=Symbol(\"s\");const o={};o[s]=1;const v=o[s];",
        ),
        (
            "harmless string literal key",
            "const o={size:1};const v=o[\"size\"];",
        ),
    ];
    for (label, fixture) in accepted {
        let body = format!("{fixture}{LIST_TAIL}");
        assert!(!item_names_kept(&body), "names kept beside {label}");
    }
}

/// `new C()` is a fresh object only while nothing replaces the library
/// constructor `C`. A replaced `Date` can return an object that a caller
/// supplied (an exported setter keeps `Array.prototype`), so a write through
/// its instance is a possible patch of the member.
#[test]
fn keeps_names_beside_writes_through_replaced_library_constructors() {
    let hostile = "function(){return 7}";
    let setter = "let held;export function supply(v){held=v}";
    let cases: Vec<(&str, String)> = vec![
        (
            "assigned global binding",
            format!(
                "{setter}Date=function(){{return held}};const p=new \
                 Date();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "assigned member of globalThis",
            format!(
                "{setter}globalThis.Date=function(){{return held}};const p=new \
                 Date();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "assigned member of global by string key",
            format!(
                "{setter}global[\"Date\"]=function(){{return held}};const p=new \
                 Date();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "replaced Map",
            format!(
                "{setter}Map=function(){{return held}};const p=new \
                 Map();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "replaced Set, receiver through a conditional",
            format!(
                "{setter}Set=function(){{return held}};const p=Math.random()>2?{{}}:new \
                 Set();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "class extending a replaced constructor",
            format!(
                "{setter}Date=function(){{return held}};class Stamp extends Date{{}}const p=new \
                 Stamp();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "constructor this of a class extending a replaced constructor",
            format!(
                "{setter}Date=function(){{return held}};class Stamp extends \
                 Date{{constructor(){{super();this.pop={hostile}}}}}new Stamp();{POP_TAIL}"
            ),
        ),
        (
            "constructor replaced through an unproven receiver",
            format!(
                "{setter}function install(g){{g.Date=function(){{return \
                 held}}}}install({{}});const p=new Date();p.pop={hostile};{POP_TAIL}"
            ),
        ),
    ];
    let (survived, renamed) = partition_by_kept(&cases, item_names_kept);
    assert_eq!(survived.len(), cases.len(), "renamed for: {renamed:?}");
    // Neighbors: an unreplaced constructor stays fresh, even beside the
    // replacement of another one, and so does an application class.
    let accepted: Vec<(&str, String)> = vec![
        (
            "unreplaced Date",
            format!("const p=new Date();p.pop={hostile};{LIST_TAIL}"),
        ),
        (
            "other constructor replaced",
            format!(
                "{setter}Date=function(){{return held}};const m=new \
                 Map();m.pop={hostile};{LIST_TAIL}"
            ),
        ),
        (
            "class extending an unreplaced constructor",
            format!(
                "{setter}Date=function(){{return held}};class Cache extends Map{{}}const c=new \
                 Cache();c.pop={hostile};{LIST_TAIL}"
            ),
        ),
        (
            "application class beside a replacement",
            format!(
                "{setter}Date=function(){{return held}};const it=new \
                 Item(2);it.pop={hostile};{LIST_TAIL}"
            ),
        ),
        (
            "local shadowing the constructor name",
            format!(
                "function make(){{const Date=class{{}};const p=new \
                 Date();p.pop={hostile}}}make();{LIST_TAIL}"
            ),
        ),
    ];
    let (survived, _) = partition_by_kept(&accepted, item_names_kept);
    assert!(survived.is_empty(), "names kept beside: {survived:?}");
}

/// A class decorator may replace the declaration with any constructor, so the
/// original constructor chain does not show what `new C()` returns. A
/// decorated class, and every class built on one, is not a fresh receiver.
#[test]
fn keeps_names_beside_writes_through_decorated_classes() {
    let hostile = "function(){return 7}";
    let setter = "let held;export function supply(v){held=v}";
    let decorator = "function wrap(C){return function(){return held}}";
    let cases: Vec<(&str, String)> = vec![
        (
            "instance of a decorated class",
            format!(
                "{setter}{decorator}@wrap class Box{{}}const p=new \
                 Box();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "decorated class with a decorator factory",
            format!(
                "{setter}{decorator}function tag(){{return wrap}}@tag() class Box{{}}const p=new \
                 Box();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "static write on a decorated class",
            format!("{setter}{decorator}@wrap class Box{{}}Box.pop={hostile};{POP_TAIL}"),
        ),
        (
            "class extending a decorated class",
            format!(
                "{setter}{decorator}@wrap class Base{{}}class Box extends Base{{}}const p=new \
                 Box();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "constructor this of a decorated class",
            format!(
                "{setter}{decorator}@wrap class Box{{constructor(){{this.pop={hostile}}}}}new \
                 Box();{POP_TAIL}"
            ),
        ),
        (
            "decorated class expression",
            format!(
                "{setter}{decorator}const Box=@wrap class{{}};const p=new \
                 Box();p.pop={hostile};{POP_TAIL}"
            ),
        ),
        (
            "write on a decorated class expression",
            format!("{setter}{decorator}const Box=@wrap class{{}};Box.pop={hostile};{POP_TAIL}"),
        ),
    ];
    let (survived, renamed) = partition_by_kept(&cases, item_names_kept_unrun);
    assert_eq!(survived.len(), cases.len(), "renamed for: {renamed:?}");
    // Neighbors: classes without a class decorator stay fresh receivers, and
    // so do classes whose members, not the class itself, are decorated.
    let accepted: Vec<(&str, String)> = vec![
        (
            "plain class",
            format!("class Box{{}}const p=new Box();p.pop={hostile};{LIST_TAIL}"),
        ),
        (
            "class with a decorated method",
            format!(
                "function mark(m){{return m}}class Box{{@mark go(){{}}}}const p=new \
                 Box();p.pop={hostile};{LIST_TAIL}"
            ),
        ),
        (
            "class with a decorated field",
            format!(
                "function mark(){{}}class Box{{@mark field=1}}const p=new \
                 Box();p.pop={hostile};{LIST_TAIL}"
            ),
        ),
        (
            "class extending a plain class",
            format!("class Base{{}}class Box extends Base{{}}new Box().pop={hostile};{LIST_TAIL}"),
        ),
        (
            "plain class expression",
            format!("const Box=class{{}};Box.pop={hostile};{LIST_TAIL}"),
        ),
    ];
    let (survived, _) = partition_by_kept(&accepted, item_names_kept_unrun);
    assert!(survived.is_empty(), "names kept beside: {survived:?}");
}

/// The property that the hostile payloads of the unchecked-chunk fixtures read,
/// spelled so that the chunk does not mention it (an excluded chunk that
/// mentions `itemValue` would keep the name for that reason alone) and does not
/// contain a text needle of the patch scan (`globalThis`, `prototype`).
const OPAQUE_POP: &str = "function(){global.seen=this[0][\"item\"+\"Value\"];return 7}";

/// The entry chunk of the unchecked-chunk fixtures: it imports `vendor.js` and
/// stores a tracked object in an array whose `pop` the vendor chunk replaces.
fn vendor_entry() -> String {
    format!("import\"./vendor.js\";{ITEM}{POP_TAIL}export{{}};")
}

/// Optimizes the entry with `vendor` as an unchecked chunk (operator-excluded
/// or a syntax boundary because it calls `eval`), checks that the program
/// behaves the same, and returns the optimized entry chunk.
fn vendor_run(vendor: &str, boundary: &[&str]) -> String {
    let temp = bundle(&[("index.js", &vendor_entry()), ("vendor.js", vendor)]);
    let output = run_typeopt(temp.path(), boundary);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    same_behavior(temp.path());
    out(temp.path(), "index.js")
}

/// An unchecked chunk (excluded by the operator, or a boundary because it
/// calls `eval`) that reaches the intrinsics keeps the names of every checked
/// chunk, in spellings that the text needles of the patch scan do not see.
#[test]
fn keeps_names_when_an_unchecked_chunk_reaches_intrinsics() {
    let hostile = [
        // Thread: a split spelling of `prototype` on the constructor.
        ("split prototype", "Array[\"proto\"+\"type\"].pop=H;"),
        (
            "split prototype in a variable",
            "const k=\"proto\"+\"type\";Array[k].pop=H;",
        ),
        (
            "constructor of an array",
            "[][\"con\"+\"structor\"][\"proto\"+\"type\"].pop=H;",
        ),
        ("Reflect.get", "Reflect.get([],\"__pro\"+\"to__\").pop=H;"),
        (
            "property descriptor of Array",
            "Object.getOwnPropertyDescriptor(Array,\"proto\"+\"type\").value.pop=H;",
        ),
        (
            "split getPrototypeOf",
            "Object[\"getPrototype\"+\"Of\"]([]).pop=H;",
        ),
        (
            "Function constructor",
            "Function(\"return Array\")()[\"proto\"+\"type\"].pop=H;",
        ),
        (
            "global object",
            "global[\"Ar\"+\"ray\"][\"proto\"+\"type\"].pop=H;",
        ),
        (
            "writer on a split prototype",
            "Object.assign(Array[\"proto\"+\"type\"],{pop:H});",
        ),
    ];
    let mut renamed: Vec<&str> = Vec::new();
    for (label, body) in hostile {
        let vendor = format!("const H={OPAQUE_POP};{body}export{{}};");
        let index = vendor_run(&vendor, &["--bundle-exclude", "vendor"]);
        if !index.contains("itemValue") {
            renamed.push(label);
        }
    }
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");
    // Dynamic code in any chunk is a route to every intrinsic: `eval` and
    // `Function` are guarded globals in every spelling.
    for dynamic in [
        "eval(\"0\");export{};",
        "(0,eval)(\"0\");export{};",
        "export const f=new Function(\"return 1\");",
        "export const g=globalThis.eval;",
        "export const h=globalThis[\"ev\"+\"al\"];",
    ] {
        let index = vendor_run(dynamic, &[]);
        assert!(index.contains("itemValue"), "{dynamic}:\n{index}");
    }
    // The same chunk without a route to the intrinsics lets the model run, so
    // the fixtures above keep the names because of the rule.
    let index = vendor_run(
        "export const flag=Array.isArray([]);",
        &["--bundle-exclude", "vendor"],
    );
    assert!(!index.contains("itemValue"), "{index}");
}

/// Entry chunk for the fixtures that only the analysis sees: the tracked array
/// is renamable unless some other chunk rejects the model.
fn static_entry() -> String {
    format!(
        "{ITEM}const list=[];list.push(new Item(1));console.log(list.pop().itemValue);export{{}};"
    )
}

/// Optimizes the entry beside a classic chunk and reports whether the name
/// survives. The classic chunk is not executed: its sloppy-mode code cannot
/// run as an ES module.
fn name_survives_beside(classic: &str) -> bool {
    let temp = bundle(&[("index.js", &static_entry()), ("classic.js", classic)]);
    let output = run_typeopt(temp.path(), &[]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    out(temp.path(), "index.js").contains("itemValue")
}

/// A classic script runs sloppy code: a function called without a receiver
/// sees the global object as `this`, and `with` rebinds names.
#[test]
fn keeps_names_beside_sloppy_classic_chunks_that_can_reach_the_global_object() {
    let rejected = [
        ("sloppy function this", "function leak(){return this}"),
        ("sloppy method this", "var o={m(){return this}};"),
        ("sloppy arrow in a function", "function f(){return()=>this}"),
        ("top-level this of a script", "var g=this;"),
        ("with statement", "with({}){}"),
        ("string timer", "setTimeout(\"0\");"),
        ("string interval", "setInterval(\"0\",1);"),
        ("data module import", "import(\"data:text/javascript,0\");"),
        ("computed import", "var u=\"./x.js\";import(u);"),
    ];
    let survived: Vec<&str> = rejected
        .iter()
        .filter(|(_, code)| !name_survives_beside(code))
        .map(|(label, _)| *label)
        .collect();
    assert!(survived.is_empty(), "names renamed for: {survived:?}");
    // Strict code and application functions do not reach the global object.
    for classic in [
        "\"use strict\";function ok(){return this}",
        "class K{m(){return this}}",
        "function f(){\"use strict\";return this}",
        "setTimeout(()=>0,1);",
        "import(\"./other.js\").catch(()=>0);",
        "var plain=function(a){return a+1};",
    ] {
        assert!(!name_survives_beside(classic), "name kept beside {classic}");
    }
}

/// Every write to `length` truncates or extends the array, so it is an escape
/// boundary, and so is every operation spelled as a write.
#[test]
fn keeps_names_of_arrays_whose_length_is_written() {
    let writes = [
        "list.length=0",
        "list[\"length\"]=0",
        "list.length=list.length",
        "list.length+=0",
        "list.length*=1",
        "list.length++",
        "--list.length",
        "try{delete list.length}catch{}",
        "[list.length]=[0]",
        "({length:list.length}={length:0})",
        "for(list.length of[0]);",
    ];
    let renamed: Vec<&str> = writes
        .into_iter()
        .filter(|write| {
            let body = format!(
                "const list=[new \
                 Item(1)];console.log(list[0].itemValue);{write};console.log(list.length);"
            );
            !array_chunk(&body).1.contains("itemValue")
        })
        .collect();
    assert!(renamed.is_empty(), "names renamed for: {renamed:?}");
}

/// A receiver that is an array or a closed class instance keeps both; the
/// closed class has its own `length`.
#[test]
fn keeps_mixed_array_and_instance_receivers() {
    let (_, index) = array_chunk(
        "class Sized{constructor(){this.length=3}}const v=globalThis.k?[1,2]:new \
         Sized;console.log(v.length);",
    );
    assert!(index.contains("length"), "{index}");
}

/// Arrays are never rename units: a program with only arrays has no object
/// region and no property, and arrays beside objects add no region.
#[test]
fn reports_no_regions_for_arrays() {
    let code = "const a=[1,2];const b=a.map(x=>x+1);b.push(a.length);console.log(a,b);export{};";
    let (_, report) = run(&[("index.js", code)]);
    assert_eq!(report["object_regions"], 0, "{report:#}");
    assert_eq!(report["properties"], 0, "{report:#}");
    let with_object = "const rows=[{rowLabel:1}];const copy=rows.slice(0);for(const r of \
                       copy)console.log(r.rowLabel);export{};";
    let (_, report) = run(&[("index.js", with_object)]);
    assert_eq!(report["object_regions"], 1, "{report:#}");
    assert_eq!(report["properties"], 1, "{report:#}");
}

/// Arrays that analyzed code keeps private let objects stored in them be
/// renamed even when other arrays escape.
#[test]
fn renames_through_one_array_while_another_escapes() {
    let (_, index) = array_chunk(
        "const kept=[{keptLabel:1}];const \
         leaked=[{leakedLabel:2}];globalThis.leak=leaked;console.log(kept[0].keptLabel,globalThis.\
         leak.length);",
    );
    assert!(!index.contains("keptLabel"), "{index}");
    assert!(index.contains("leakedLabel"), "{index}");
}

/// Debug logs report which array built-ins are modeled, how many array values
/// exist and why arrays are preserved, with the array cause labels.
#[test]
fn logs_array_decisions_with_their_causes() {
    let fixtures: [(&str, &str); 5] = [
        (
            "array:unmodeled-member",
            "const list=[new Item(2),new \
             Item(1)];list.sort((a,b)=>a.itemValue-b.itemValue);console.log(list[0].itemValue);",
        ),
        (
            "access:mixed-array-receiver",
            "class Sized{constructor(){this.length=3}}const v=globalThis.k?[1,2]:new \
             Sized;console.log(v.length);",
        ),
        (
            "store-into-array-length",
            "const list=[];list.length={lengthLabel:1};console.log(list.length);",
        ),
        (
            "sink:exposed-callback-result",
            "const ext={map(f){return f({})}};const \
             r=(globalThis.e??ext).map(x=>({wrappedValue:1}));console.log(r.wrappedValue);",
        ),
        (
            "call-argument:escaped-array",
            "const list=[];globalThis.hook=l=>{l.push=x=>console.log(x.itemValue)};globalThis.\
             hook(list);list.push(new Item(1));",
        ),
    ];
    for (label, body) in fixtures {
        let code = format!("{ITEM}{body}export{{}};");
        let temp = bundle(&[("index.js", &code)]);
        let output = run_typeopt(temp.path(), &[]);
        assert!(output.status.success());
        let log = strip_ansi(&String::from_utf8_lossy(&output.stderr));
        for event in [
            "array builtins intact",
            "array values allocated",
            "array preservation causes",
        ] {
            assert!(log.contains(event), "{event} missing for {label}:\n{log}");
        }
        assert!(log.contains(label), "{label} missing in {body}:\n{log}");
    }
}

/// Optimizing twice gives identical output: arrays do not make the analysis
/// order-dependent.
#[test]
fn array_analysis_is_deterministic() {
    let code = format!(
        "{ITEM}const list=[new Item(1),new Item(2)];const \
         w=list.map(x=>({{wrappedValue:x.itemValue}}));for(const it of \
         w)console.log(it.wrappedValue);list.push(new \
         Item(3));console.log(list.length);export{{}};"
    );
    let (first, _) = run(&[("index.js", &code)]);
    let (second, _) = run(&[("index.js", &code)]);
    assert_eq!(
        out(first.path(), "index.js"),
        out(second.path(), "index.js")
    );
}

// ---------------------------------------------------------------------------
// Closed classes: esbuild and Rolldown `keepNames` static blocks
// ---------------------------------------------------------------------------

/// The local `keepNames` helper of esbuild: `Object.defineProperty` is
/// aliased and `__name` is a two-parameter arrow function.
const KEEP_NAMES_HELPER: &str =
    "var e=Object.defineProperty,t=(t,n)=>e(t,`name`,{value:n,configurable:!0});";

/// `static{t(this,`IshikawaDB`)}` does not let the class escape, so the
/// members of the class are renamed and `C.name` keeps its value.
#[test]
fn renames_members_of_classes_with_a_keep_names_block() {
    let code = format!(
        "{KEEP_NAMES_HELPER}var \
         g=class{{constructor(){{this.stackOfNodes=[]}}static{{t(this,\
         `IshikawaDB`)}}pushNode(v){{this.stackOfNodes.push(v);return \
         this.stackOfNodes.length}}}};const d=new g;console.log(g.name,d.pushNode(1));export{{}};"
    );
    let (temp, _) = run(&[("index.js", &code)]);
    let index = out(temp.path(), "index.js");
    for name in ["stackOfNodes", "pushNode"] {
        assert!(!index.contains(name), "{name} kept:\n{index}");
    }
    assert!(index.contains("IshikawaDB"), "{index}");
}

/// The helper is imported from another chunk under another name.
#[test]
fn renames_members_of_classes_with_an_imported_keep_names_helper() {
    let helper = format!("{KEEP_NAMES_HELPER}export{{t as n}};");
    let (temp, _) = run(&[
        ("helper.js", &helper),
        (
            "index.js",
            r#"import{n as t}from"./helper.js";var g=class{constructor(){this.stackOfNodes=[]}static{t(this,`IshikawaDB`)}pushNode(v){this.stackOfNodes.push(v);return this.stackOfNodes.length}};const d=new g;console.log(g.name,d.pushNode(1));"#,
        ),
    ]);
    let index = out(temp.path(), "index.js");
    for name in ["stackOfNodes", "pushNode"] {
        assert!(!index.contains(name), "{name} kept:\n{index}");
    }
    assert!(index.contains("IshikawaDB"), "{index}");
}

/// Other spellings of the exact helper body are accepted too.
#[test]
fn accepts_equivalent_keep_names_helper_spellings() {
    let helpers = [
        // A function declaration with a string key and `true`.
        r#"function t(a,b){return Object.defineProperty(a,"name",{value:b,configurable:true})}"#,
        // A function expression with an expression statement and swapped
        // descriptor properties.
        r#"const t=function(a,b){Object.defineProperty(a,"name",{configurable:true,value:b})};"#,
        // An arrow function with a block body and a parenthesized call.
        r#"const t=(a,b)=>{return(Object.defineProperty)(a,"name",{value:b,configurable:!0})};"#,
        // An alias of `Object.defineProperty` that is declared once.
        r#"const d=Object.defineProperty;const t=(a,b)=>d(a,"name",{"value":b,"configurable":true});"#,
    ];
    for helper in helpers {
        let code = format!(
            r#"{helper}class G{{constructor(){{this.longField=1}}readField(){{return this.longField}}static{{t(this,"Named")}}}}console.log(G.name,new G().readField());export{{}};"#
        );
        let (temp, _) = run(&[("index.js", &code)]);
        let index = out(temp.path(), "index.js");
        for name in ["longField", "readField"] {
            assert!(!index.contains(name), "{name} kept for {helper}:\n{index}");
        }
    }
}

/// A base class and its subclass can both carry the block.
#[test]
fn renames_members_of_a_base_and_a_subclass_with_keep_names_blocks() {
    let code = format!(
        "{KEEP_NAMES_HELPER}var \
         b=class{{constructor(){{this.baseField=1}}static{{t(this,`Base`)}}baseMethod(){{return \
         this.baseField}}}};var s=class extends \
         b{{constructor(){{super();this.derivedField=2}}static{{t(this,\
         `Derived`)}}derivedMethod(){{return \
         this.baseMethod()+this.derivedField}}}};console.log(b.name,s.name,new \
         s().derivedMethod());export{{}};"
    );
    let (temp, _) = run(&[("index.js", &code)]);
    let index = out(temp.path(), "index.js");
    for name in ["baseField", "baseMethod", "derivedField", "derivedMethod"] {
        assert!(!index.contains(name), "{name} kept:\n{index}");
    }
}

/// Every fixture keeps the names of a class whose static block is not the
/// exact `keepNames` shape, and runs the same before and after.
#[test]
fn keeps_members_of_classes_with_other_static_blocks() {
    let good = r#"const t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});"#;
    let cases = [
        // The helper leaks its first argument.
        (
            "reason=HelperShape",
            r#"const t=(a,b)=>{globalThis.leaked=a;return Object.defineProperty(a,"name",{value:b,configurable:true})};class G{constructor(){this.secretField=1}static{t(this,"G")}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // The helper is reassigned.
        (
            "reason=HelperWritten",
            r#"let t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});t=a=>{globalThis.leaked=a};class G{constructor(){this.secretField=1}static{t(this,"G")}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // The helper is declared twice.
        (
            "reason=HelperUnresolved",
            r#"var t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});var t=a=>{globalThis.leaked=a};class G{constructor(){this.secretField=1}static{t(this,"G")}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // `Object` is a module-local binding.
        (
            "reason=ShadowedObject",
            r#"const Object={defineProperty(o){globalThis.leaked=o}};const t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"G")}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // The alias of `defineProperty` is reassigned.
        (
            "reason=DefinePropertyAlias",
            r#"let d=Object.defineProperty;d=o=>{globalThis.leaked=o};const t=(a,b)=>d(a,"name",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"G")}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // The alias is not `Object.defineProperty`.
        (
            "reason=DefinePropertyAlias",
            r#"const d=o=>{globalThis.leaked=o};const t=(a,b)=>d(a,"name",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"G")}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // The block runs a second statement with `this`.
        (
            "reason=NotSingleCall",
            r#"class G{constructor(){this.secretField=1}static{t(this,"G");globalThis.leaked=this}}new G;console.log(new globalThis.leaked().secretField);"#,
        ),
        // The name is not a literal.
        (
            "reason=NonLiteralName",
            r#"globalThis.nameOf="N";class G{constructor(){this.secretField=1}static{t(this,globalThis.nameOf)}}console.log(G.name,new G().secretField);"#,
        ),
        // The name is a template with a substitution.
        (
            "reason=NonLiteralName",
            r#"class G{constructor(){this.secretField=1}static{t(this,`N${1+1}`)}}console.log(G.name,new G().secretField);"#,
        ),
        // The first argument is derived from `this`.
        (
            "reason=ReceiverArgument",
            r#"class G{constructor(){this.secretField=1}static{t(this.constructor,"N")}}console.log(G.name,new G().secretField);"#,
        ),
        // The helper defines `length`.
        (
            "reason=HelperShape",
            r#"const t=(a,b)=>Object.defineProperty(a,"length",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(G.length,new G().secretField);"#,
        ),
        // The descriptor has a third property.
        (
            "reason=HelperShape",
            r#"const t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true,writable:true});class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(G.name,new G().secretField);"#,
        ),
        // The helper reads `arguments`.
        (
            "reason=HelperShape",
            r#"const t=function(a,b){return Object.defineProperty(a,"name",{value:arguments[1],configurable:true})};class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(G.name,new G().secretField);"#,
        ),
        // The helper is async.
        (
            "reason=HelperShape",
            r#"const t=async(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(new G().secretField);"#,
        ),
        // The helper has a default parameter.
        (
            "reason=HelperShape",
            r#"const t=(a,b="x")=>Object.defineProperty(a,"name",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(G.name,new G().secretField);"#,
        ),
        // The helper installs a getter instead of a value.
        (
            "reason=HelperShape",
            r#"const t=(a,b)=>Object.defineProperty(a,"name",{get:()=>b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(G.name,new G().secretField);"#,
        ),
        // The helper does not set `configurable`.
        (
            "reason=HelperShape",
            r#"const t=(a,b)=>Object.defineProperty(a,"name",{value:b,enumerable:true});class G{constructor(){this.secretField=1}static{t(this,"N")}}console.log(G.name,new G().secretField);"#,
        ),
        // A second static block uses `this`.
        (
            "reason=NotSingleCall",
            r#"class G{constructor(){this.secretField=1}static{t(this,"Uid")}static{this.count=0}}console.log(G.name,G.count,new G().secretField);"#,
        ),
        // A static method uses `this`.
        (
            "\"static-receiver\": 1",
            r#"class G{constructor(){this.secretField=1}static{t(this,"G")}static make(){return new this}}console.log(G.name,G.make().secretField);"#,
        ),
    ];
    for (needle, code) in cases {
        let code =
            if code.contains("const t=") || code.contains("let t=") || code.contains("var t=") {
                format!("{code}export{{}};")
            } else {
                format!("{good}{code}export{{}};")
            };
        let temp = bundle(&[("index.js", &code)]);
        let output = run_typeopt(temp.path(), &[]);
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        same_behavior(temp.path());
        let index = out(temp.path(), "index.js");
        assert!(
            index.contains("secretField"),
            "renamed for {code}:\n{index}"
        );
        let log = strip_ansi(&String::from_utf8_lossy(&output.stderr));
        assert!(log.contains(needle), "expected {needle} for {code}:\n{log}");
    }
}

/// A helper in a chunk that no pass may trust (it calls `eval`) is not
/// proven.
#[test]
fn keeps_members_when_the_keep_names_helper_is_in_a_boundary_chunk() {
    let (temp, _) = run(&[
        (
            "helper.js",
            r#"eval("0");const t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});export{t};"#,
        ),
        (
            "index.js",
            r#"import{t}from"./helper.js";class G{constructor(){this.secretField=1}static{t(this,"G")}}console.log(G.name,new G().secretField);"#,
        ),
    ]);
    let index = out(temp.path(), "index.js");
    assert!(index.contains("secretField"), "{index}");
}

/// The debug log counts the accepted blocks and names the reason of a
/// rejected one.
#[test]
fn logs_keep_names_static_block_decisions() {
    let code = format!(
        r#"{KEEP_NAMES_HELPER}class SecretAlpha{{constructor(){{this.firstField=1}}static{{t(this,`SecretNameAlpha`)}}}}class SecretBeta{{constructor(){{this.secondField=1}}static{{t(this,`SecretNameBeta`)}}static{{this.count=0}}}}console.log(SecretAlpha.name,SecretBeta.name,new SecretAlpha().firstField,new SecretBeta().secondField);export{{}};"#
    );
    let temp = bundle(&[("index.js", &code)]);
    let output = run_typeopt(temp.path(), &[]);
    assert!(output.status.success());
    let log = strip_ansi(&String::from_utf8_lossy(&output.stderr));
    assert!(log.contains("keepNames static block accepted"), "{log}");
    assert!(
        log.contains("keepNames-shaped static block rejected"),
        "{log}"
    );
    assert!(log.contains("reason=NotSingleCall"), "{log}");
    assert!(log.contains("name_blocks=2"), "{log}");
    // Source identifiers and string literals never reach the log.
    for secret in [
        "SecretAlpha",
        "SecretBeta",
        "SecretNameAlpha",
        "SecretNameBeta",
    ] {
        assert!(!log.contains(secret), "{secret} logged:\n{log}");
    }
}

/// Every way application code can replace `Object` or `Object.defineProperty`
/// keeps the names, for the aliased helper and for the inlined direct call,
/// and the program behaves the same before and after.
#[test]
fn keeps_members_when_define_property_may_be_replaced() {
    let helper = r#"const t=(a,b)=>Object.defineProperty(a,"name",{value:b,configurable:true});"#;
    let direct = r#"class G{constructor(){this.secretField=1}static{Object.defineProperty(this,"name",{value:"G",configurable:true})}}"#;
    let aliased = r#"const e=Object.defineProperty,t=(a,b)=>e(a,"name",{value:b,configurable:true});class G{constructor(){this.secretField=1}static{t(this,"G")}}"#;
    let wrapper = r#"{const o=Object.defineProperty;Object.defineProperty=function(a,...r){globalThis.leaked=a;return o.call(this,a,...r)}}"#;
    let reflect = r#"Reflect.set(Object,"defineProperty",function(a){globalThis.leaked=a})"#;
    let global = r#"globalThis.Object={defineProperty(a){globalThis.leaked=a}}"#;
    let define =
        r#"Object.defineProperty(Object,"defineProperty",{value(a){globalThis.leaked=a}})"#;
    let assign = r#"Object.assign(Object,{defineProperty(a){globalThis.leaked=a}})"#;
    let tail = "new G;console.log(new globalThis.leaked().secretField);export{};";
    for patch in [wrapper, reflect, global, define, assign] {
        for class in [direct, aliased] {
            let class = if class == aliased {
                class.to_owned()
            } else {
                format!("{helper}{class}")
            };
            let code = format!("{patch};{class}{tail}");
            let temp = bundle(&[("index.js", &code)]);
            let output = run_typeopt(temp.path(), &[]);
            assert!(
                output.status.success(),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
            same_behavior(temp.path());
            let index = out(temp.path(), "index.js");
            assert!(
                index.contains("secretField"),
                "renamed for {code}:\n{index}"
            );
            let log = strip_ansi(&String::from_utf8_lossy(&output.stderr));
            assert!(
                log.contains("reason=DefinePropertyPatched"),
                "expected a patch rejection for {code}:\n{log}"
            );
        }
    }
}

/// Call inlining can replace the helper call with its body before the
/// property passes run; that form is accepted with the same checks.
#[test]
fn handles_keep_names_blocks_after_helper_inlining() {
    let (temp, _) = run(&[(
        "index.js",
        r#"class G{constructor(){this.longField=1}readField(){return this.longField}static{Object.defineProperty(this,`name`,{value:"Direct",configurable:!0})}}console.log(G.name,new G().readField());export{};"#,
    )]);
    let index = out(temp.path(), "index.js");
    for name in ["longField", "readField"] {
        assert!(!index.contains(name), "{name} kept:\n{index}");
    }
    // A shadowed `Object` or a non-literal value keeps the names.
    for code in [
        r#"const Object={defineProperty(o){globalThis.leaked=o}};class G{constructor(){this.secretField=1}static{Object.defineProperty(this,"name",{value:"G",configurable:true})}}new G;console.log(new globalThis.leaked().secretField);"#,
        r#"globalThis.nameOf="N";class G{constructor(){this.secretField=1}static{Object.defineProperty(this,"name",{value:globalThis.nameOf,configurable:true})}}console.log(G.name,new G().secretField);"#,
        r#"class G{constructor(){this.secretField=1}static{Object.defineProperty(this,"length",{value:"N",configurable:true})}}console.log(G.length,new G().secretField);"#,
    ] {
        let code = format!("{code}export{{}};");
        let (temp, _) = run(&[("index.js", &code)]);
        let index = out(temp.path(), "index.js");
        assert!(
            index.contains("secretField"),
            "renamed for {code}:\n{index}"
        );
    }
}
