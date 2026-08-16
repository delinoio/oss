import assert from "node:assert/strict";
import test from "node:test";

import yaml from "js-yaml";

import {
  validateCiTargetMatrix,
  validateResolvedDependencySources,
} from "./verify-pins-policy.mjs";

const CRATES_IO_SOURCE = "registry+https://github.com/rust-lang/crates.io-index";
const TAURI_SOURCE =
  "git+https://github.com/tauri-apps/tauri?rev=4af26a3f7f8b692d62cca549bbacd93f5ce90b41#4af26a3f7f8b692d62cca549bbacd93f5ce90b41";
const DEVHUD_ID = "path+file:///repo/apps/devhud/src-tauri#devhud@0.1.0";
const CEF_RUNTIME_ID = `${TAURI_SOURCE}#tauri-runtime-cef@0.1.0`;
const DEBUG_CELL_ID = `${CRATES_IO_SOURCE}#dioxus-debug-cell@0.1.1`;
const UNRELATED_ID = "path+file:///repo/crates/unrelated#unrelated@0.1.0";
const UNRELATED_PATCH_ID = "git+https://example.com/unrelated#unrelated-helper@1.0.0";

function cargoMetadata(debugCellSource = CRATES_IO_SOURCE) {
  return {
    packages: [
      { id: DEVHUD_ID, name: "devhud", version: "0.1.0", source: null },
      { id: CEF_RUNTIME_ID, name: "tauri-runtime-cef", version: "0.1.0", source: TAURI_SOURCE },
      { id: DEBUG_CELL_ID, name: "dioxus-debug-cell", version: "0.1.1", source: debugCellSource },
      { id: UNRELATED_ID, name: "unrelated", version: "0.1.0", source: null },
      {
        id: UNRELATED_PATCH_ID,
        name: "unrelated-helper",
        version: "1.0.0",
        source: "git+https://example.com/unrelated#abcdef",
      },
    ],
    resolve: {
      nodes: [
        { id: DEVHUD_ID, dependencies: [CEF_RUNTIME_ID], features: [] },
        { id: CEF_RUNTIME_ID, dependencies: [DEBUG_CELL_ID], features: ["sandbox"] },
        { id: DEBUG_CELL_ID, dependencies: [], features: [] },
        { id: UNRELATED_ID, dependencies: [UNRELATED_PATCH_ID], features: [] },
        { id: UNRELATED_PATCH_ID, dependencies: [], features: [] },
      ],
    },
  };
}

const allowedSources = new Set([CRATES_IO_SOURCE, TAURI_SOURCE]);

test("accepts canonical sources while ignoring unrelated workspace patches", () => {
  assert.doesNotThrow(() =>
    validateResolvedDependencySources(cargoMetadata(), DEVHUD_ID, allowedSources),
  );
});

for (const source of [null, "git+https://example.com/dioxus-debug-cell#abcdef"]) {
  test(`rejects a transitive DevHUD patch from ${source ?? "a local path"}`, () => {
    assert.throws(
      () => validateResolvedDependencySources(cargoMetadata(source), DEVHUD_ID, allowedSources),
      /forbidden source in the DevHUD dependency graph: dioxus-debug-cell 0\.1\.1/u,
    );
  });
}

const targets = [
  { id: "macos-x64", runner: "macos-15-intel" },
  { id: "macos-arm64", runner: "macos-15" },
];

function workflowWithMatrix(matrix) {
  return yaml.load(`
jobs:
  devhud-desktop:
    strategy:
      matrix:
        include:
${matrix}
`);
}

test("accepts runner labels paired with their target IDs", () => {
  const workflow = workflowWithMatrix(`
          - id: macos-x64
            runner: macos-15-intel
          - id: macos-arm64
            runner: macos-15
`);

  assert.doesNotThrow(() => validateCiTargetMatrix(workflow, targets));
});

test("rejects a runner label that appears only in another matrix object", () => {
  const workflow = workflowWithMatrix(`
          - id: macos-x64
            runner: macos-15
          - id: macos-arm64
            runner: macos-15-intel
`);

  assert.throws(
    () => validateCiTargetMatrix(workflow, targets),
    /native CI matrix runner changed for macos-x64/u,
  );
});

test("requires exactly one matrix object per target ID", () => {
  const workflow = workflowWithMatrix(`
          - id: macos-x64
            runner: macos-15-intel
          - id: macos-x64
            runner: macos-15
`);

  assert.throws(
    () => validateCiTargetMatrix(workflow, targets),
    /native CI matrix contains a duplicate or invalid target ID/u,
  );
});
