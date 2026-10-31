// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import yaml from "js-yaml";
import { Event, planJobs } from "./plan.mjs";
const workflow = yaml.load(
  readFileSync(".github/workflows/release-cowc.yml", "utf8"),
);
test("CowCache native gate is manual, main-only, read-only and owns four real environments", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(workflow.jobs.candidate.if, "github.ref == 'refs/heads/main'");
  assert.equal(workflow.jobs.native.environment, "cowc-native-acceptance");
  const matrix = workflow.jobs.native.strategy.matrix.include;
  assert.deepEqual(
    matrix.map((m) => m.lane),
    ["macos15-apfs", "ubuntu22-btrfs", "ubuntu22-xfs", "windows24h2-refs"],
  );
  for (const entry of matrix) assert(entry.labels.includes("self-hosted"));
  assert.deepEqual(workflow.jobs.gate.needs, ["candidate", "native"]);
  assert.equal(workflow.jobs.gate.if, undefined);
  assert(!JSON.stringify(workflow).match(/npm publish|gh release|git push/u));
});
test("normal affected CI runs CowCache distribution tests without native allocation", () => {
  for (const event of Object.values(Event))
    for (const file of [
      "packages/cowc/src/launcher.cjs",
      "cmds/cowcache/main.go",
      "scripts/release/cowc-acceptance.mjs",
      ".github/workflows/release-cowc.yml",
    ]) {
      assert.equal(
        planJobs(event, [file]).jobs["release-contracts"],
        true,
        `${event}: ${file}`,
      );
    }
  const scripts = JSON.parse(readFileSync("scripts/ci/package.json")).scripts;
  assert(
    scripts["ci:release-fixtures"].includes("packages/cowc/test/*.test.mjs"),
  );
  const inputs = JSON.parse(readFileSync("scripts/ci/turbo.json")).tasks[
    "ci:release-fixtures"
  ].inputs;
  assert(inputs.includes("$TURBO_ROOT$/packages/cowc/**"));
});
