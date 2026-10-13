// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Regression tests for `benchmark/measure.mjs` chunk-reference validation.
//! Each test builds synthetic production directories and runs the script, so
//! loading references are checked against their own build independently of
//! any asset emitted by another build.
#![cfg(any(target_os = "linux", target_os = "macos"))]
use std::{
    fs,
    path::{Path, PathBuf},
    process::Command,
};

use serde_json::{Value, json};

const REVISION: &str = "c5b4a4ee3006d113633fcb59bd63965599b0c5c0";
const EXTENDED_REVISION: &str = "1111111111111111111111111111111111111111";

fn benchmark_dir() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("benchmark")
}

fn pin() -> Value {
    serde_json::from_slice(&fs::read(benchmark_dir().join("pin.json")).unwrap()).unwrap()
}

/// `measure.mjs` refuses to run on any Node other than the pinned one because
/// the compressors ship with Node. Other versions skip these tests with a
/// message instead of failing for an unrelated reason.
fn pinned_node() -> bool {
    let output = Command::new("node")
        .args(["-p", "process.versions.node"])
        .output()
        .unwrap();
    let actual = String::from_utf8(output.stdout).unwrap();
    let pinned = pin()["node"].as_str().unwrap().to_string();
    if actual.trim() == pinned {
        return true;
    }
    eprintln!(
        "skipping measure.mjs regression: Node {} is not the pinned {pinned}",
        actual.trim()
    );
    false
}

#[derive(Clone, Copy, PartialEq)]
enum Kind {
    Original,
    Existing,
    Extended,
}

/// Incompressible-looking filler that gives a build a predictable size.
fn padding(length: usize) -> String {
    let mut state = 12345u64;
    (0..length)
        .map(|_| {
            state = state
                .wrapping_mul(6364136223846793005)
                .wrapping_add(1442695040888963407);
            char::from(33 + ((state >> 33) % 90) as u8)
        })
        .collect()
}

/// Writes a synthetic build. `files` hold extra assets; the sizes make later
/// builds strictly smaller than earlier ones.
fn write_build(root: &Path, kind: Kind, index_html: &str, main_js: &str, files: &[(&str, &str)]) {
    fs::create_dir_all(root.join("assets")).unwrap();
    fs::write(root.join("index.html"), index_html).unwrap();
    let size = match kind {
        Kind::Original => 6000,
        Kind::Existing => 3000,
        Kind::Extended => 100,
    };
    fs::write(
        root.join("assets/main-12345678.js"),
        format!("{main_js}\n/*{}*/\n", padding(size)),
    )
    .unwrap();
    for (name, content) in files {
        let path = root.join(name);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, content).unwrap();
    }
    let mut meta = json!({
        "schema_version": 1,
        "revision": REVISION,
        "source_tree": "fixture",
        "yarn_lock_sha256": "fixture",
        "node": pin()["node"],
        "vite": "5.0.12",
        "mode": "production",
        "sourcemap": false,
        "environment": {},
        "optimized": kind != Kind::Original,
    });
    match kind {
        Kind::Original => {}
        Kind::Existing => {
            meta["optimizer"] = json!({
                "source_revision": pin()["existing_typeopt_revision"],
                "source_dirty": false,
                "report_schema_version": 0,
            });
        }
        Kind::Extended => {
            meta["optimizer"] = json!({
                "source_revision": EXTENDED_REVISION,
                "source_dirty": false,
                "report_schema_version": 1,
            });
        }
    }
    fs::write(root.join("typeopt-build.json"), meta.to_string()).unwrap();
}

const ENTRY_HTML: &str = r#"<script type="module" src="/assets/main-12345678.js"></script>"#;

struct Outcome {
    success: bool,
    report: Value,
}

fn measure(roots: &[&Path]) -> Outcome {
    let output = Command::new("node")
        .arg(benchmark_dir().join("measure.mjs"))
        .args(roots)
        .output()
        .unwrap();
    Outcome {
        success: output.status.success(),
        report: serde_json::from_slice(&output.stdout).unwrap_or_else(|error| {
            panic!(
                "unparseable measure.mjs output ({error}): {}",
                String::from_utf8_lossy(&output.stderr)
            )
        }),
    }
}

/// Two builds with the same entry HTML and the same JS in both.
fn two_builds(
    html: &str,
    main_js: &str,
    original_files: &[(&str, &str)],
    optimized_files: &[(&str, &str)],
) -> (tempfile::TempDir, Outcome) {
    let temp = tempfile::tempdir().unwrap();
    let original = temp.path().join("original");
    let optimized = temp.path().join("optimized");
    write_build(&original, Kind::Original, html, main_js, original_files);
    write_build(&optimized, Kind::Extended, html, main_js, optimized_files);
    let outcome = measure(&[&original, &optimized]);
    (temp, outcome)
}

fn broken(outcome: &Outcome, build: &str) -> Vec<String> {
    outcome.report["broken_chunk_references"][build]
        .as_array()
        .unwrap()
        .iter()
        .map(|entry| entry.as_str().unwrap().to_string())
        .collect()
}

const LAZY: (&str, &str) = ("assets/lazy-12345678.js", "export const value = 1;");

#[test]
fn valid_local_loading_passes() {
    if !pinned_node() {
        return;
    }
    let (_temp, outcome) = two_builds(
        r#"<script type="module" src="/assets/main-12345678.js"></script>
<link rel="modulepreload" href="/assets/lazy-12345678.js">"#,
        "import './lazy-12345678.js'; import('./lazy-12345678.js');",
        &[LAZY],
        &[LAZY],
    );
    assert!(outcome.success);
    assert_eq!(outcome.report["passed"], true);
    assert!(broken(&outcome, "original").is_empty());
    assert!(broken(&outcome, "optimized").is_empty());
}

#[test]
fn missing_relative_dynamic_import_fails_when_absent_everywhere() {
    if !pinned_node() {
        return;
    }
    let (_temp, outcome) = two_builds(ENTRY_HTML, "import('./lazy-12345678.js');", &[], &[]);
    assert!(!outcome.success);
    assert_eq!(outcome.report["passed"], false);
    let expected = vec!["assets/main-12345678.js -> ./lazy-12345678.js".to_string()];
    assert_eq!(broken(&outcome, "original"), expected);
    assert_eq!(broken(&outcome, "optimized"), expected);
}

#[test]
fn missing_no_substitution_template_dynamic_import_fails() {
    if !pinned_node() {
        return;
    }
    // The baseline keeps the chunk the optimized build lost. Templates with
    // substitutions cannot be resolved statically and stay ignored.
    let (_temp, outcome) = two_builds(
        ENTRY_HTML,
        "import(`./lazy-12345678.js`); const n = 1; import(`./dynamic-${n}.js`);",
        &[LAZY],
        &[],
    );
    assert!(!outcome.success);
    assert_eq!(outcome.report["passed"], false);
    assert!(broken(&outcome, "original").is_empty());
    assert_eq!(
        broken(&outcome, "optimized"),
        ["assets/main-12345678.js -> ./lazy-12345678.js"]
    );
}

#[test]
fn missing_static_imports_fail_in_every_syntax() {
    if !pinned_node() {
        return;
    }
    let (_temp, outcome) = two_builds(
        ENTRY_HTML,
        "import{a as \
         b}from\"./named-12345678.js\";export*from'./star-12345678.js';import\"./effect-12345678.\
         js\";import x from './default-12345678.js';",
        &[],
        &[],
    );
    assert!(!outcome.success);
    let expected = [
        "assets/main-12345678.js -> ./default-12345678.js",
        "assets/main-12345678.js -> ./effect-12345678.js",
        "assets/main-12345678.js -> ./named-12345678.js",
        "assets/main-12345678.js -> ./star-12345678.js",
    ];
    assert_eq!(broken(&outcome, "optimized"), expected);
}

#[test]
fn missing_root_relative_html_references_fail() {
    if !pinned_node() {
        return;
    }
    let (_temp, outcome) = two_builds(
        r#"<script type="module" src="/assets/main-12345678.js"></script>
<script type="module" src="/assets/entry-12345678.js"></script>
<link rel="modulepreload" href="/assets/preload-12345678.js">"#,
        "export const ready = true;",
        &[],
        &[],
    );
    assert!(!outcome.success);
    assert_eq!(
        broken(&outcome, "original"),
        [
            "index.html -> /assets/entry-12345678.js",
            "index.html -> /assets/preload-12345678.js",
        ]
    );
}

#[test]
fn prefixless_relative_html_urls_are_document_relative_references() {
    if !pinned_node() {
        return;
    }
    // HTML URLs resolve against the document without a `./` prefix, while the
    // same text in JavaScript is a bare specifier and stays ignored.
    let html = r#"<script type="module" src="assets/main-12345678.js"></script>
<script type="module" src="assets/entry-12345678.js"></script>
<link rel="modulepreload" href="assets/lazy-12345678.js">
<script type="module">import "assets/bare-12345678.js";</script>"#;
    let (_temp, outcome) = two_builds(html, "export const ready = true;", &[LAZY], &[]);
    assert!(!outcome.success);
    assert_eq!(
        broken(&outcome, "original"),
        ["index.html -> assets/entry-12345678.js"]
    );
    assert_eq!(
        broken(&outcome, "optimized"),
        [
            "index.html -> assets/entry-12345678.js",
            "index.html -> assets/lazy-12345678.js",
        ]
    );
}

#[test]
fn javascript_escapes_are_decoded_before_resolving_specifiers() {
    if !pinned_node() {
        return;
    }
    let main_js = r#"
import "./lazy\x2d12345678.js";
import("./lazy\u002d12345678.js");
import("./lazy\u{2d}12345678.js");
import('./lazy\
-12345678.js');
import(`./lazy\5512345678.js`);
import("./gone\x2d12345678.js");
"#;
    // Every escape spells the existing `lazy-12345678.js` (`\55` is an octal
    // "-", limited to two digits here); only `gone-12345678.js` is
    // absent from both builds.
    let (_temp, outcome) = two_builds(ENTRY_HTML, main_js, &[LAZY], &[LAZY]);
    assert!(!outcome.success);
    let expected = vec!["assets/main-12345678.js -> ./gone-12345678.js".to_string()];
    assert_eq!(broken(&outcome, "original"), expected);
    assert_eq!(broken(&outcome, "optimized"), expected);
}

#[test]
fn different_hash_asset_in_another_build_keeps_exact_target_unresolved() {
    if !pinned_node() {
        return;
    }
    let (_temp, outcome) = two_builds(
        ENTRY_HTML,
        "import('./lazy-12345678.js');",
        &[("assets/lazy-87654321.js", "export const value = 1;")],
        &[],
    );
    assert!(!outcome.success);
    let expected = vec!["assets/main-12345678.js -> ./lazy-12345678.js".to_string()];
    assert_eq!(broken(&outcome, "original"), expected);
    assert_eq!(broken(&outcome, "optimized"), expected);
}

#[test]
fn harmless_strings_external_urls_and_removed_chunks_pass() {
    if !pinned_node() {
        return;
    }
    let main_js = r#"
const label = "./missing-12345678.js";
const code = "import('./missing-12345678.js')";
const template = `from "./missing-12345678.js" ${label}`;
// import './missing-12345678.js';
/* import('./missing-12345678.js') */
const holder = { import: "./missing-12345678.js", from: "./missing-12345678.js" };
import "https://cdn.example.com/lib.js";
import("//cdn.example.com/lib.js");
import "bare-package";
import('./kept-12345678.js');
"#;
    let kept = ("assets/kept-12345678.js", "export const kept = 1;");
    let unused = ("assets/unused-12345678.js", "export const unused = 1;");
    let (_temp, outcome) = two_builds(
        r#"<script type="module" src="https://cdn.example.com/entry.js"></script>
<link rel="modulepreload" href="//cdn.example.com/preload.js">
<script type="module" src="/assets/main-12345678.js"></script>
<!-- <script type="module" src="/assets/commented-12345678.js"></script> -->"#,
        main_js,
        &[kept, unused],
        &[kept],
    );
    assert!(outcome.success, "{}", outcome.report);
    assert_eq!(outcome.report["passed"], true);
    assert!(broken(&outcome, "original").is_empty());
    assert!(broken(&outcome, "optimized").is_empty());
    assert_eq!(
        outcome.report["comparisons"][0]["removed"],
        json!(["assets/unused.js"])
    );
}

#[test]
fn size_gate_still_applies_with_valid_references() {
    if !pinned_node() {
        return;
    }
    let temp = tempfile::tempdir().unwrap();
    let original = temp.path().join("original");
    let optimized = temp.path().join("optimized");
    // The optimized build is larger, so it must fail with clean references.
    write_build(&original, Kind::Extended, ENTRY_HTML, "export {};", &[]);
    write_build(&optimized, Kind::Original, ENTRY_HTML, "export {};", &[]);
    let outcome = measure(&[&original, &optimized]);
    assert!(!outcome.success);
    assert!(broken(&outcome, "optimized").is_empty());
    assert_eq!(outcome.report["comparisons"][0]["strictly_smaller"], false);
}

fn three_builds(main_js: &str, files: [&[(&str, &str)]; 3]) -> (tempfile::TempDir, Outcome) {
    let temp = tempfile::tempdir().unwrap();
    let roots = ["original", "existing", "extended"].map(|name| temp.path().join(name));
    let kinds = [Kind::Original, Kind::Existing, Kind::Extended];
    for ((root, kind), files) in roots.iter().zip(kinds).zip(files) {
        write_build(root, kind, ENTRY_HTML, main_js, files);
    }
    let outcome = measure(&[&roots[0], &roots[1], &roots[2]]);
    (temp, outcome)
}

#[test]
fn three_build_form_validates_each_build_independently() {
    if !pinned_node() {
        return;
    }
    let (_valid_temp, valid) =
        three_builds("import('./lazy-12345678.js');", [&[LAZY], &[LAZY], &[LAZY]]);
    assert!(valid.success, "{}", valid.report);
    assert_eq!(valid.report["provenance_mismatches"], json!([]));
    assert_eq!(valid.report["comparisons"].as_array().unwrap().len(), 2);

    let (_missing_temp, missing) = three_builds("import('./lazy-12345678.js');", [&[], &[], &[]]);
    assert!(!missing.success);
    assert_eq!(missing.report["provenance_mismatches"], json!([]));
    for build in ["original", "existing", "extended"] {
        assert_eq!(
            broken(&missing, build),
            ["assets/main-12345678.js -> ./lazy-12345678.js"]
        );
    }

    // Only the extended build lost the chunk its entry still imports.
    let (_partial_temp, partial) =
        three_builds("import('./lazy-12345678.js');", [&[LAZY], &[LAZY], &[]]);
    assert!(!partial.success);
    assert!(broken(&partial, "original").is_empty());
    assert!(broken(&partial, "existing").is_empty());
    assert_eq!(broken(&partial, "extended").len(), 1);
}
