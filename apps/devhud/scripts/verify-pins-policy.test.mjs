import assert from "node:assert/strict";
import test from "node:test";

import yaml from "js-yaml";

import {
  validateCiTargetMatrix,
  validateDependencyPresence,
  validateResolvedDependencySources,
} from "./verify-pins-policy.mjs";

const CRATES_IO_SOURCE = "registry+https://github.com/rust-lang/crates.io-index";
const TAURI_SOURCE =
  "git+https://github.com/tauri-apps/tauri?rev=4af26a3f7f8b692d62cca549bbacd93f5ce90b41#4af26a3f7f8b692d62cca549bbacd93f5ce90b41";
const DEVHUD_ID = "path+file:///repo/apps/devhud/src-tauri#devhud@0.1.0";
const NATIVE_MESSAGING_HOST_ID =
  "path+file:///repo/crates/devhud-native-messaging-host#devhud-native-messaging-host@0.1.0";
const CEF_RUNTIME_ID = `${TAURI_SOURCE}#tauri-runtime-cef@0.1.0`;
const DEBUG_CELL_ID = `${CRATES_IO_SOURCE}#dioxus-debug-cell@0.1.1`;
const UNRELATED_ID = "path+file:///repo/crates/unrelated#unrelated@0.1.0";
const UNRELATED_PATCH_ID = "git+https://example.com/unrelated#unrelated-helper@1.0.0";

function cargoMetadata(debugCellSource = CRATES_IO_SOURCE) {
  return {
    packages: [
      { id: DEVHUD_ID, name: "devhud", version: "0.1.0", source: null },
      {
        id: NATIVE_MESSAGING_HOST_ID,
        name: "devhud-native-messaging-host",
        version: "0.1.0",
        source: null,
      },
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
        {
          id: DEVHUD_ID,
          dependencies: [CEF_RUNTIME_ID, NATIVE_MESSAGING_HOST_ID],
          features: [],
        },
        { id: NATIVE_MESSAGING_HOST_ID, dependencies: [], features: [] },
        { id: CEF_RUNTIME_ID, dependencies: [DEBUG_CELL_ID], features: ["sandbox"] },
        { id: DEBUG_CELL_ID, dependencies: [], features: [] },
        { id: UNRELATED_ID, dependencies: [UNRELATED_PATCH_ID], features: [] },
        { id: UNRELATED_PATCH_ID, dependencies: [], features: [] },
      ],
    },
  };
}

const allowedSources = new Set([CRATES_IO_SOURCE, TAURI_SOURCE]);
const allowedLocalPackageIds = new Set([NATIVE_MESSAGING_HOST_ID]);

test("accepts canonical sources and the approved local Native Messaging host", () => {
  assert.doesNotThrow(() =>
    validateResolvedDependencySources(
      cargoMetadata(),
      DEVHUD_ID,
      allowedSources,
      allowedLocalPackageIds,
    ),
  );
});

test("enforces target-specific rdev presence", () => {
  const withoutRdev = cargoMetadata();
  assert.doesNotThrow(() =>
    validateDependencyPresence(withoutRdev, DEVHUD_ID, "rdev", false, "x86_64-apple-darwin"),
  );

  const withRdev = cargoMetadata();
  const rdevId = `${CRATES_IO_SOURCE}#rdev@0.5.3`;
  withRdev.packages.push({ id: rdevId, name: "rdev", version: "0.5.3", source: CRATES_IO_SOURCE });
  withRdev.resolve.nodes[0].dependencies.push(rdevId);
  withRdev.resolve.nodes.push({ id: rdevId, dependencies: [], features: [] });
  assert.doesNotThrow(() =>
    validateDependencyPresence(withRdev, DEVHUD_ID, "rdev", true, "x86_64-unknown-linux-gnu"),
  );
  assert.throws(
    () => validateDependencyPresence(withRdev, DEVHUD_ID, "rdev", false, "aarch64-apple-darwin"),
    /rdev must be absent from aarch64-apple-darwin/u,
  );
});

test("rejects the Native Messaging host without an explicit local package approval", () => {
  assert.throws(
    () => validateResolvedDependencySources(cargoMetadata(), DEVHUD_ID, allowedSources),
    /forbidden source in the DevHUD dependency graph: devhud-native-messaging-host 0\.1\.0/u,
  );
});

for (const source of [null, "git+https://example.com/dioxus-debug-cell#abcdef"]) {
  test(`rejects a transitive DevHUD patch from ${source ?? "a local path"}`, () => {
    assert.throws(
      () =>
        validateResolvedDependencySources(
          cargoMetadata(source),
          DEVHUD_ID,
          allowedSources,
          allowedLocalPackageIds,
        ),
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
