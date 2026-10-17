// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import test from "node:test";
import { load } from "js-yaml";
import { developmentMatrix } from "../../packages/pnport/scripts/native-matrix.mjs";

const workflow = load(
  readFileSync(
    new URL("../../.github/workflows/pnport-windows.yml", import.meta.url),
    "utf8",
  ),
);

test("Windows development planning selects native hosts without release authority", () => {
  const output = execFileSync(
    process.execPath,
    [new URL("./pnport-windows-matrix.mjs", import.meta.url).pathname],
    { encoding: "utf8" },
  );
  const matrix = JSON.parse(output.trim().slice("matrix=".length));
  assert.deepEqual(
    matrix.include,
    developmentMatrix.include.filter(({ target }) =>
      target.endsWith("-pc-windows-msvc"),
    ),
  );
  assert.deepEqual(
    matrix.include.map(({ runner }) => runner),
    ["windows-2022", "windows-11-arm"],
  );
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.ok(Object.hasOwn(workflow.on, "workflow_dispatch"));
  assert.ok(workflow.on.pull_request.paths.includes("crates/fspy*/**"));
  assert.ok(workflow.on.pull_request.paths.includes("Cargo.lock"));
  assert.equal(workflow.jobs.windows.strategy["fail-fast"], false);
  assert.equal(workflow.jobs.windows.needs, "plan");
  const steps = workflow.jobs.windows.steps;
  assert.ok(
    steps.some(({ run }) => run?.includes("./scripts/ci/pnport-windows.ps1")),
  );
  assert.ok(
    steps
      .filter(({ uses }) => uses?.startsWith("actions/checkout@"))
      .every(({ with: options }) => options["persist-credentials"] === false),
  );
  assert.doesNotMatch(
    JSON.stringify(workflow),
    /secrets\.|pull_request_target|release-project|publish/,
  );
});
