// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `signature-reduction` and `function-specialization` fixtures.
#![cfg(any(target_os = "linux", target_os = "macos"))]
mod common;
use common::*;
use serde_json::Value;

const SR: &str = "signature-reduction";

fn run(files: &[(&str, &str)], keep: &[&str]) -> (tempfile::TempDir, Value) {
    let temp = project(files);
    let report = optimize(temp.path(), &args(&only(keep)));
    same_behavior(temp.path());
    (temp, report)
}

#[test]
fn debug_signature_reduction() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"import { describe, log } from "./dep.ts";
let counter = 0;
function tick(label: string): number { counter++; console.log("tick", label); return counter; }
function combine(unusedLeft: number, value: number, unusedRight: string, scale: number): number {
  return value * scale;
}
console.log(combine(tick("a"), 2, "zzzzzzzzzzzzzzzzzzzzzzzzz", 10));
console.log(combine(1, tick("b"), String(tick("c")), 10));
console.log(describe("hello", 3));
log("x", 1);
log("y", 2);
"#,
        ),
        (
            "dep.ts",
            r#"export function describe(text: string, times: number): string {
  return text.repeat(times);
}
export function log(message: string, level: number): number {
  console.log("log", message);
  const doubled = level * 2;
  return doubled + 1000000;
}
"#,
        ),
    ];
    let (temp, report) = run(files, &[SR]);
    eprintln!(
        "{}",
        serde_json::to_string_pretty(&report["transformations"]).unwrap()
    );
    eprintln!(
        "{}",
        serde_json::to_string_pretty(&report["skipped"]).unwrap()
    );
    eprintln!("{}", emitted(temp.path(), "main.ts"));
    eprintln!("{}", emitted(temp.path(), "dep.ts"));
}

#[test]
fn debug_specialization() {
    let files: &[(&str, &str)] = &[
        (
            "main.ts",
            r#"import { draw } from "./dep.ts";
type Shape = "circle" | "square";
function area(shape: Shape, size: number): number {
  if (shape === "circle") {
    const radius = size / 2;
    console.log("computing circle area for", size);
    return Math.round(Math.PI * radius * radius * 1000) / 1000;
  }
  console.log("computing square area for", size);
  return size * size;
}
console.log(area("circle", 2), area("square", 3), area("circle", 4));
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
    let (temp, report) = run(
        files,
        &["function-specialization", "type-directed-control-flow"],
    );
    eprintln!(
        "{}",
        serde_json::to_string_pretty(&report["transformations"]).unwrap()
    );
    eprintln!(
        "{}",
        serde_json::to_string_pretty(&report["skipped"]).unwrap()
    );
    eprintln!("{}", emitted(temp.path(), "main.ts"));
    eprintln!("{}", emitted(temp.path(), "dep.ts"));
}
