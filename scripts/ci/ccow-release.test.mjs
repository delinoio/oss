// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import yaml from "js-yaml";
import { Event, planJobs } from "./plan.mjs";
const workflow = yaml.load(
  readFileSync(".github/workflows/release-ccow.yml", "utf8"),
);
test("CacheCow release is manual, main-only and runs no native acceptance", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(
    workflow.jobs.candidate.if,
    "github.repository == 'delinoio/oss' && github.ref == 'refs/heads/main'",
  );
  assert.deepEqual(Object.keys(workflow.jobs), [
    "build",
    "candidate",
    "publish",
    "homebrew",
    "linux-packages",
  ]);
  assert.doesNotMatch(
    JSON.stringify(workflow),
    /ccow-acceptance|ccow-evidence|native-acceptance/u,
  );
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.equal(workflow.jobs.publish.needs, "candidate");
  assert.equal(workflow.jobs.publish.environment, "ccow-release");
  assert.match(workflow.jobs.publish.if, /!inputs.dry_run/u);
  assert.match(
    workflow.jobs.publish.if,
    /github.repository == 'delinoio\/oss'/u,
  );
  assert.match(workflow.jobs.publish.if, /github.ref == 'refs\/heads\/main'/u);
  const steps = workflow.jobs.publish.steps;
  const verify = steps.findIndex(
    (s) => s.name === "Verify the exact candidate before credentials",
  );
  const credentials = steps.findIndex((s) => s.id === "release-bot");
  assert(verify >= 0 && credentials > verify);
  assert.equal(steps[credentials].with.repositories, "oss");
  assert.equal(steps[credentials].with.owner, "delinoio");
  assert.equal(steps[credentials].with["permission-contents"], "write");
  const publish = steps.at(-1);
  assert.match(publish.run, /ccow-publish.mjs/u);
  assert.deepEqual(publish.env, {
    GH_TOKEN: "${{ steps.release-bot.outputs.token }}",
    REQUESTED_VERSION: "${{ inputs.version }}",
  });
  // CacheCow ships native archives only: no npm credentials, registry or tools.
  assert.doesNotMatch(
    JSON.stringify(workflow),
    /NPM_TOKEN|NODE_AUTH_TOKEN|registry-url|registry\.npmjs|npm (?:publish|dist-tag|install)/u,
  );
  assert.deepEqual(Object.keys(workflow.jobs.publish.permissions ?? {}), [
    "contents",
  ]);
  for (const [name, job] of Object.entries(workflow.jobs)) {
    for (const step of job.steps ?? []) {
      if (step.uses?.startsWith("actions/checkout@")) {
        assert.equal(step.with.ref, "${{ github.sha }}");
        assert.equal(step.with["persist-credentials"], false);
      }
    }
    if (!["publish", "homebrew", "linux-packages"].includes(name))
      assert(!JSON.stringify(job).includes("secrets."));
  }
});
test("CacheCow candidates package Rust binaries built on matching hosts", () => {
  const build = workflow.jobs.build;
  assert.equal(build.if, workflow.jobs.candidate.if);
  assert.equal(workflow.jobs.candidate.needs, "build");
  const platforms = createRequire(import.meta.url)(
    "../../packages/ccow/src/platforms.cjs",
  );
  assert.deepEqual(
    build.strategy.matrix.include.map(({ target, rust }) => ({
      target,
      rust,
    })),
    platforms.targets.map(({ suffix, rust }) => ({ target: suffix, rust })),
  );
  assert.deepEqual(
    build.strategy.matrix.include.map(({ runner }) => runner),
    ["macos-15", "ubuntu-22.04", "ubuntu-24.04-arm", "windows-latest"],
  );
  const toolchain = build.steps.find(({ uses }) =>
    uses?.startsWith("dtolnay/rust-toolchain@"),
  );
  assert.equal(
    toolchain.with.toolchain,
    readFileSync("rust-toolchain", "utf8").trim(),
  );
  assert.match(
    build.steps.find(({ run }) => run?.includes("build-native.mjs")).run,
    /--target "\$CCOW_TARGET"/u,
  );
  const candidate = JSON.stringify(workflow.jobs.candidate);
  assert.doesNotMatch(candidate, /setup-go|build-native/u);
  assert.match(candidate, /package\.mjs/u);
  assert.match(candidate, /verify\.mjs/u);
});
test("normal affected CI runs CacheCow distribution tests without native allocation", () => {
  for (const event of Object.values(Event))
    for (const file of [
      "packages/ccow/src/platforms.cjs",
      "crates/cachecow/src/main.rs",
      "scripts/release/ccow-release-identity.mjs",
      "scripts/release/ccow-publish.mjs",
      "scripts/release/ccow-publish.test.mjs",
      "scripts/release/ccow-recover.mjs",
      "scripts/release/ccow-transport.test.mjs",
      ".github/workflows/release-ccow.yml",
      ".github/workflows/recover-ccow-release.yml",
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

test("CacheCow signs retained candidates and gates native package channels", () => {
  assert.deepEqual(workflow.jobs.candidate.permissions, {
    contents: "read",
    "id-token": "write",
  });
  assert.match(JSON.stringify(workflow.jobs.candidate), /cosign sign-blob/u);
  assert.match(JSON.stringify(workflow.jobs.publish), /ccow-signatures/u);
  const homebrew = workflow.jobs.homebrew;
  assert.deepEqual(homebrew.needs, ["candidate", "publish"]);
  const validation = homebrew.steps.findIndex((step) =>
    step.run?.includes("brew install"),
  );
  const credentials = homebrew.steps.findIndex((step) => step.id === "tap-bot");
  assert(validation >= 0 && validation < credentials);
  assert.match(
    homebrew.steps[validation].run,
    /brew test delinoio\/ccow-validation\/ccow/u,
  );
  assert.equal(homebrew.steps[credentials].with.repositories, "homebrew-tap");
  assert.equal(workflow.jobs["linux-packages"].with.project, "ccow");
  for (const name of ["homebrew", "linux-packages"])
    assert.match(workflow.jobs[name].if, /!inputs.dry_run/u);
});

test("CacheCow recovery verifies retained original candidates before release credentials without rebuilding", () => {
  const recovery = yaml.load(
    readFileSync(".github/workflows/recover-ccow-release.yml", "utf8"),
  );
  assert.deepEqual(Object.keys(recovery.on), ["workflow_dispatch"]);
  assert.deepEqual(recovery.permissions, { contents: "read", actions: "read" });
  assert.equal(recovery.concurrency.group, workflow.concurrency.group);
  const job = recovery.jobs.recover;
  assert.equal(
    job.if,
    `${workflow.jobs.candidate.if} && inputs.target != 'homebrew'`,
  );
  assert.equal(job.environment, "ccow-release");
  assert.equal(job["runs-on"], "ubuntu-22.04");
  const credentials = job.steps.findIndex((s) => s.id === "release-bot");
  for (const name of [
    "Resolve the original failed release before credentials",
    "Verify original candidate bytes before credentials",
    "Verify original signatures before credentials",
  ]) {
    const verification = job.steps.findIndex((s) => s.name === name);
    assert(verification >= 0 && verification < credentials);
  }
  const downloads = job.steps.filter((s) =>
    s.uses?.startsWith("actions/download-artifact@"),
  );
  assert.equal(downloads.length, 2);
  for (const step of downloads) {
    assert.equal(step.with["run-id"], "${{ inputs.source_run_id }}");
    assert.equal(step.with["github-token"], "${{ github.token }}");
    assert.match(step.with.name, /steps.source.outputs.revision/u);
  }
  assert.match(
    JSON.stringify(job),
    /certificate-github-workflow-sha.*SOURCE_REVISION/u,
  );
  assert.doesNotMatch(
    JSON.stringify(recovery),
    /cargo build|build-native|sign-blob|id-token|gh release|gh api|npm publish/u,
  );
});

test("Homebrew recovery verifies public bytes and native installation before tap-only credentials", () => {
  const recovery = yaml.load(
    readFileSync(".github/workflows/recover-ccow-release.yml", "utf8"),
  );
  assert.equal(
    recovery.on.workflow_dispatch.inputs.target.default,
    "github-release",
  );
  assert.deepEqual(recovery.on.workflow_dispatch.inputs.target.options, [
    "github-release",
    "homebrew",
  ]);
  const job = recovery.jobs.homebrew;
  assert.equal(
    job.if,
    `${workflow.jobs.candidate.if} && inputs.target == 'homebrew'`,
  );
  assert.equal(job["runs-on"], "macos-15");
  assert.equal(job.environment, "ccow-release");
  const credentials = job.steps.findIndex((s) => s.id === "tap-bot");
  for (const name of [
    "Resolve the original failed release before credentials",
    "Verify original candidate bytes before credentials",
    "Verify original signatures before credentials",
    "Verify complete public release before tap credentials",
    "Validate Homebrew installation before tap credentials",
  ]) {
    const verification = job.steps.findIndex((s) => s.name === name);
    assert(verification >= 0 && verification < credentials);
  }
  assert.equal(job.steps[credentials].with.repositories, "homebrew-tap");
  assert.match(JSON.stringify(job), /ccow-recover.mjs verify-published/u);
  assert.match(JSON.stringify(job), /brew audit --strict/u);
  assert.match(job.steps.at(-1).run, /brew test delinoio\/tap\/ccow/u);
  assert.doesNotMatch(
    JSON.stringify(job),
    /ccow-publish.mjs|release-bot.outputs|cargo build|sign-blob/u,
  );
});
