// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import yaml from "js-yaml";
import { Event, planJobs } from "./plan.mjs";
const workflow = yaml.load(
  readFileSync(".github/workflows/release-ccow.yml", "utf8"),
);
test("CacheCow native gate is manual, main-only and owns four real environments", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(
    workflow.jobs.candidate.if,
    "github.repository == 'delinoio/oss' && github.ref == 'refs/heads/main'",
  );
  assert.equal(workflow.jobs.native.environment, "ccow-native-acceptance");
  const matrix = workflow.jobs.native.strategy.matrix.include;
  assert.deepEqual(
    matrix.map((m) => m.lane),
    ["macos15-apfs", "ubuntu22-btrfs", "ubuntu22-xfs", "windows24h2-refs"],
  );
  for (const entry of matrix) assert(entry.labels.includes("self-hosted"));
  assert.deepEqual(workflow.jobs.gate.needs, ["candidate", "native"]);
  assert.equal(workflow.jobs.gate.if, undefined);
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.deepEqual(workflow.jobs.publish.needs, [
    "candidate",
    "native",
    "gate",
  ]);
  assert.equal(workflow.jobs.publish.environment, "ccow-release");
  assert.match(workflow.jobs.publish.if, /!inputs.dry_run/u);
  assert.match(
    workflow.jobs.publish.if,
    /github.repository == 'delinoio\/oss'/u,
  );
  assert.match(workflow.jobs.publish.if, /github.ref == 'refs\/heads\/main'/u);
  const steps = workflow.jobs.publish.steps;
  const revalidate = steps.findIndex(
    (s) => s.name === "Revalidate native acceptance before credentials",
  );
  const credentials = steps.findIndex((s) => s.id === "release-bot");
  assert(revalidate >= 0 && credentials > revalidate);
  assert.equal(steps[credentials].with.repositories, "oss");
  assert.equal(steps[credentials].with.owner, "delinoio");
  assert.equal(steps[credentials].with["permission-contents"], "write");
  const publish = steps.at(-1);
  assert.match(publish.run, /ccow-publish.mjs/u);
  assert.equal(publish.env.NODE_AUTH_TOKEN, "${{ secrets.NPM_TOKEN }}");
  for (const [name, job] of Object.entries(workflow.jobs)) {
    for (const step of job.steps) {
      if (step.uses?.startsWith("actions/checkout@")) {
        assert.equal(step.with.ref, "${{ github.sha }}");
        assert.equal(step.with["persist-credentials"], false);
      }
    }
    if (name !== "publish") assert(!JSON.stringify(job).includes("secrets."));
  }
});
test("normal affected CI runs CacheCow distribution tests without native allocation", () => {
  for (const event of Object.values(Event))
    for (const file of [
      "packages/ccow/src/launcher.cjs",
      "cmds/cachecow/main.go",
      "scripts/release/ccow-acceptance.mjs",
      "scripts/release/ccow-publish.mjs",
      "scripts/release/ccow-publish.test.mjs",
      ".github/workflows/release-ccow.yml",
    ]) {
      assert.equal(
        planJobs(event, [file]).jobs["release-contracts"],
        true,
        `${event}: ${file}`,
      );
    }
  const scripts = JSON.parse(readFileSync("scripts/ci/package.json")).scripts;
  assert(
    scripts["ci:release-fixtures"].includes("packages/ccow/test/*.test.mjs"),
  );
  const inputs = JSON.parse(readFileSync("scripts/ci/turbo.json")).tasks[
    "ci:release-fixtures"
  ].inputs;
  assert(inputs.includes("$TURBO_ROOT$/packages/ccow/**"));
});
