// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import yaml from "js-yaml";
import { planJobs, Event } from "./plan.mjs";
const root = new URL("../../", import.meta.url);
const workflow = yaml.load(
  readFileSync(
    new URL(".github/workflows/release-delitools-mobile.yml", root),
    "utf8",
  ),
);

test("DeliTools release is main-only, serialized and uses protected test-channel uploads", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.publish.default, false);
  assert.equal(
    workflow.jobs.preflight.if,
    "${{ github.ref == 'refs/heads/main' }}",
  );
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  for (const platform of ["ios", "android"]) {
    const job = workflow.jobs[platform];
    assert.equal(job.needs, "preflight");
    assert.equal(job.environment, "delitools-mobile");
    const publish = job.steps.find((step) =>
      step.name?.startsWith("Upload to"),
    );
    assert.equal(publish.if, "${{ inputs.publish }}");
    const cleanup = job.steps.find(
      (step) => step.name === "Remove signing material",
    );
    assert.equal(cleanup.if, "${{ always() }}");
    const upload = job.steps.find((step) =>
      step.uses?.startsWith("actions/upload-artifact@"),
    );
    assert.equal(upload.with.path, "mobile-artifacts/");
    assert.equal(upload.with["if-no-files-found"], "error");
  }
  assert.equal(workflow.jobs.ios["runs-on"], "macos-15");
  assert.equal(workflow.jobs.android["runs-on"], "ubuntu-22.04");
});

test("DeliTools app and release changes select affected validation without unrelated DevHud native jobs", () => {
  for (const event of Object.values(Event)) {
    for (const path of [
      "apps/delitools/src/main.tsx",
      ".github/workflows/release-delitools-mobile.yml",
      "apps/delitools/scripts/publish-android.mjs",
    ]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs["delitools-frontend"], true, `${event}: ${path}`);
      assert.equal(plan.jobs["delidev-frontend"], false);
      assert.equal(plan.jobs["devhud-desktop"], false);
    }
  }
});
