// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import yaml from "js-yaml";
import { verifyProvenance } from "./provenance.mjs";
const text = readFileSync(
    new URL(
      "../../../.github/workflows/delidev-mobile-beta.yml",
      import.meta.url,
    ),
    "utf8",
  ),
  workflow = yaml.load(text);
test("mobile dispatch defaults to dry run; credentials are protected and ordinary checks never publish", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.mode.default, "dry-run");
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  for (const [name, job] of Object.entries(workflow.jobs)) {
    if (["package", "submit"].includes(name))
      assert.equal(job.environment, "delidev-mobile-beta");
    else assert.doesNotMatch(JSON.stringify(job), /secrets\./);
    for (const step of job.steps ?? [])
      if (step.uses?.startsWith("actions/checkout"))
        assert.equal(step.with.ref, "${{ inputs.source_sha }}");
  }
  assert.match(JSON.stringify(workflow.jobs.checks), /build:ios:sim/);
  assert.match(JSON.stringify(workflow.jobs.checks), /build:android:emulator/);
  assert.doesNotMatch(text, /actions\/cache|turbo run|--clobber|auto-merge/);
  assert.match(JSON.stringify(workflow.jobs.submit), /candidate_artifact_id/);
  assert.match(JSON.stringify(workflow.jobs.submit), /receipt_artifact_id/);
  assert.equal(workflow.jobs.submit.steps.at(-1).if, "always()");
  assert.doesNotMatch(
    JSON.stringify(workflow.jobs.submit),
    /pipeline.mjs build|signing.mjs/,
  );
});
test("candidate provenance is exact source, trusted workflow and complete successful original package", () => {
  const input = {
      sourceSha: "a".repeat(40),
      version: "1.0.0",
      iosBuild: "7",
      androidCode: "8",
    },
    artifact = {
      id: 9,
      name: `delidev-mobile-candidate-${input.sourceSha}-1.0.0-7-8`,
      expired: false,
      workflow_run: { head_sha: input.sourceSha },
    },
    run = {
      head_sha: input.sourceSha,
      event: "workflow_dispatch",
      path: ".github/workflows/delidev-mobile-beta.yml",
      conclusion: "success",
    };
  assert.equal(verifyProvenance(artifact, run, input).artifactId, 9);
  for (const delta of [
    { expired: true },
    { name: "foreign" },
    { workflow_run: { head_sha: "b".repeat(40) } },
  ])
    assert.throws(() =>
      verifyProvenance({ ...artifact, ...delta }, run, input),
    );
  for (const delta of [
    { event: "pull_request" },
    { path: ".github/workflows/release-devhud.yml" },
    { conclusion: "failure" },
  ])
    assert.throws(() =>
      verifyProvenance(artifact, { ...run, ...delta }, input),
    );
});
