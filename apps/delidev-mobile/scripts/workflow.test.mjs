// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
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
  assert.equal(workflow.concurrency.group, "delidev-mobile-beta");
  assert.equal(workflow.on.workflow_dispatch.inputs.target.default, "both");
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
test("dispatch validates exact iOS-only selection without Android input", () => {
  const script = workflow.jobs.validate.steps[0].run;
  const env = { ...process.env, MODE: "dry-run", GITHUB_SHA: "a".repeat(40),
    DELIDEV_MOBILE_SOURCE_SHA: "a".repeat(40), DELIDEV_MOBILE_VERSION: "0.1.0",
    DELIDEV_MOBILE_IOS_BUILD: "1", DELIDEV_MOBILE_ANDROID_CODE: "", DELIDEV_MOBILE_TARGET: "ios",
    CANDIDATE: "", RECEIPTS: "" };
  const run = (extra) => spawnSync(process.execPath, ["-e", script.split("node <<'JS'\n")[1].split("\nJS")[0]], {
    env: { ...env, ...extra }, encoding: "utf8",
  }).status;
  assert.equal(run({}), 0);
  assert.notEqual(run({ DELIDEV_MOBILE_TARGET: "both" }), 0);
  assert.notEqual(run({ DELIDEV_MOBILE_ANDROID_CODE: "2" }), 0);
  assert.notEqual(run({ DELIDEV_MOBILE_IOS_BUILD: "0" }), 0);
  assert.equal(run({ DELIDEV_MOBILE_TARGET: "both", DELIDEV_MOBILE_ANDROID_CODE: "2" }), 0);
  const input = { target: "ios", sourceSha: env.GITHUB_SHA, version: "0.1.0", iosBuild: "1" };
  const artifact = { id: 1, name: `delidev-mobile-candidate-${env.GITHUB_SHA}-0.1.0-1-ios`,
    workflow_run: { id: 101, head_sha: env.GITHUB_SHA } };
  const record = { id: 101, head_sha: env.GITHUB_SHA, event: "workflow_dispatch", path: ".github/workflows/delidev-mobile-beta.yml", conclusion: "success" };
  assert.equal(verifyProvenance(artifact, record, input).artifactId, 1);
  assert.throws(() => verifyProvenance(artifact, record, { ...input, target: "both", androidCode: "1" }));
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
      workflow_run: { id: 101, head_sha: input.sourceSha },
    },
    run = {
      id: 101,
      head_sha: input.sourceSha,
      event: "workflow_dispatch",
      path: ".github/workflows/delidev-mobile-beta.yml",
      conclusion: "success",
    };
  assert.equal(verifyProvenance(artifact, run, input).artifactId, 9);
  for (const delta of [
    { expired: true },
    { workflow_run: { id: 102, head_sha: input.sourceSha } },
    { workflow_run: { id: "101", head_sha: input.sourceSha } },
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

test("Android setup excludes the retired tools package", () => {
  for (const job of [workflow.jobs.checks, workflow.jobs.package]) {
    const setup = job.steps.find(step => step.uses?.startsWith("android-actions/setup-android@"));
    assert.equal(setup.with.packages, "platform-tools");
    assert.equal(setup.with["cmdline-tools-version"], "16111833");
  }
});

test("cross-run candidate and receipt downloads use their independently verified owners", () => {
  const steps = workflow.jobs.submit.steps;
  assert.equal(steps.find(step => step.id === "provenance").run,
    "node apps/delidev-mobile/scripts/provenance.mjs");
  const downloads = steps.filter(step => step.uses?.startsWith("actions/download-artifact@"));
  assert.equal(downloads[0].with["run-id"], "${{ steps.provenance.outputs.candidate_run_id }}");
  assert.equal(downloads[1].with["run-id"], "${{ steps.provenance.outputs.receipt_run_id }}");
  const input = { target: "ios", sourceSha: "a".repeat(40), version: "0.1.0", iosBuild: "1" };
  const run = { id: 101, head_sha: input.sourceSha, event: "workflow_dispatch",
    path: ".github/workflows/delidev-mobile-beta.yml", conclusion: "success" };
  const artifact = { id: 9, name: `delidev-mobile-candidate-${input.sourceSha}-0.1.0-1-ios`,
    workflow_run: { id: 101, head_sha: input.sourceSha } };
  assert.equal(verifyProvenance(artifact, run, input).runId, 101);
  assert.equal(verifyProvenance({ ...artifact, id: 10, name: "delidev-mobile-receipts-202-1",
    workflow_run: { id: 202, head_sha: input.sourceSha } },
    { ...run, id: 202, conclusion: "failure" }, input, true).runId, 202);
});
