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
test("CacheCow native gate is manual, main-only and runs four GitHub-hosted lanes", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(
    workflow.jobs.candidate.if,
    "github.repository == 'delinoio/oss' && github.ref == 'refs/heads/main'",
  );
  const native = workflow.jobs.native;
  assert.equal(native.environment, undefined);
  assert.equal(native["runs-on"], "${{ matrix.runner }}");
  const matrix = native.strategy.matrix.include;
  assert.deepEqual(
    matrix.map(({ lane, runner, filesystem }) => ({
      lane,
      runner,
      filesystem,
    })),
    [
      { lane: "macos15-apfs", runner: "macos-15", filesystem: undefined },
      { lane: "ubuntu22-btrfs", runner: "ubuntu-22.04", filesystem: "btrfs" },
      { lane: "ubuntu22-xfs", runner: "ubuntu-22.04", filesystem: "xfs" },
      {
        lane: "windows24h2-refs",
        runner: "windows-2025",
        filesystem: undefined,
      },
    ],
  );
  // Each lane provisions disposable image volumes and runs acceptance on them,
  // never on the runner's own checkout volume.
  for (const os of ["Linux", "macOS", "Windows"]) {
    const step = native.steps.find((s) => s.if === `runner.os == '${os}'`);
    assert(step, `Missing ${os} volume provisioning`);
    for (const name of [
      "CCOW_NATIVE_WORKSPACE",
      "CCOW_ACCEPTANCE_COPY_WORKSPACE",
      "CCOW_ACCEPTANCE_UNSUPPORTED_WORKSPACE",
    ])
      assert.match(step.run, new RegExp(name, "u"));
  }
  assert.match(
    native.steps.find((s) => s.name === "Install the minimum Cargo toolchain")
      .run,
    /rustup toolchain install 1\.99\.1 /u,
  );
  const accept = native.steps.find((s) =>
    s.run?.includes("ccow-acceptance.mjs native"),
  );
  assert.match(accept.run, /"\$CCOW_NATIVE_WORKSPACE"/u);
  assert.doesNotMatch(accept.run, /GITHUB_WORKSPACE/u);
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
    ["macos-15", "ubuntu-22.04", "windows-latest"],
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
      "packages/ccow/src/launcher.cjs",
      "crates/cachecow/src/main.rs",
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
