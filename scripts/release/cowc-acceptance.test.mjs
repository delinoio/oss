// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  accept,
  checks,
  gate,
  lanes,
  phases,
  releaseIdentity,
  validateMeasurements,
} from "./cowc-acceptance.mjs";

const revision = "a".repeat(40);
const fixture = () => ({
  kind: "native",
  unperformed: [],
  tools: { cargo: "1.99.1", rspack: "2.3.0", rsbuild: "2.3.0" },
  checks: Object.fromEntries(checks.map((id) => [id, true])),
  measurements: ["cargo", "rspack", "rsbuild"].flatMap((preset) =>
    phases.map((phase) => ({
      preset,
      phase,
      physicalBytes: 42,
      preparationMs: 1,
      buildMs: 2,
      peakMemoryBytes: 1024,
    })),
  ),
});

test("release identity rejects arbitrary refs, versions and revisions", () => {
  assert.equal(
    releaseIdentity("0.1.0", revision, "refs/heads/main").tag,
    "cowc@v0.1.0",
  );
  for (const args of [
    ["0.1.1", revision, "refs/heads/main"],
    ["0.1.0", revision, "refs/heads/topic"],
    ["0.1.0", "0".repeat(40), "refs/heads/main"],
  ])
    assert.throws(() => releaseIdentity(...args));
});
test("native measurement schema requires every scenario and minimum tool", () => {
  assert.equal(validateMeasurements(fixture()).measurements.length, 12);
  for (const mutate of [
    (r) => {
      r.kind = "fixture";
    },
    (r) => {
      r.unperformed.push("rollback");
    },
    (r) => {
      r.checks["physical-sharing"] = false;
    },
    (r) => {
      delete r.checks["process-tree"];
    },
    (r) => {
      r.tools.cargo = "1.100.0";
    },
    (r) => {
      r.measurements.pop();
    },
    (r) => {
      r.measurements[0].physicalBytes = -1;
    },
    (r) => {
      r.measurements[0].peakMemoryBytes = null;
    },
    (r) => {
      r.measurements[0] = r.measurements[1];
    },
  ]) {
    const result = fixture();
    mutate(result);
    assert.throws(() => validateMeasurements(result));
  }
});
test("public evidence projects out raw content and arbitrary commands", () => {
  const result = fixture();
  result.secret = "must not persist";
  result.measurements[0].command = "private arguments";
  assert(!JSON.stringify(validateMeasurements(result)).includes("private"));
  assert(!JSON.stringify(validateMeasurements(result)).includes("secret"));
});
test("missing evidence fails closed and blocked entrypoint retains unperformed checks", async () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "cowc-gate-"));
  try {
    assert.throws(() => gate(directory, directory, revision));
    const output = path.join(directory, "blocked.json");
    await assert.rejects(
      accept({
        lane: Object.keys(lanes)[0],
        artifacts: directory,
        workspace: directory,
        output,
        revision,
      }),
    );
    const record = JSON.parse(readFileSync(output, "utf8"));
    assert.equal(record.status, "blocked");
    assert.deepEqual(record.unperformed, checks);
    assert(!("measurements" in record));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
