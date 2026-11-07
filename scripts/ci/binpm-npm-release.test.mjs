// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import yaml from "js-yaml";
import { targets } from "../../packages/binpm/scripts/common.mjs";

const text = readFileSync(".github/workflows/release-binpm-npm.yml", "utf8");
const workflow = yaml.load(text);

test("binpm npm releases automatically on version tags and manual runs default to dry-run", () => {
  assert.deepEqual(workflow.on.push.tags, ["binpm@v*"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  assert.match(
    workflow.jobs.prepare.if,
    /github.repository == 'delinoio\/oss'/u,
  );
  assert.match(
    workflow.jobs.prepare.steps.find((s) => s.id === "source").run,
    /releaseIdentity\(\)/u,
  );
});

test("all six native build hosts pass installed-consumer checks before assembly", () => {
  const { build, package: assemble } = workflow.jobs;
  assert.deepEqual(
    build.strategy.matrix.include.map((t) => t.target).sort(),
    targets.map((t) => t.rust).sort(),
  );
  const legacy = yaml.load(
    readFileSync(".github/workflows/release-binpm.yml", "utf8"),
  );
  assert.deepEqual(
    build.strategy.matrix.include.map((t) => t.target).sort(),
    legacy.jobs.build.strategy.matrix.include.map((t) => t.target).sort(),
  );
  assert.equal(build.needs, "prepare");
  const smoke = build.steps.findIndex((s) =>
    s.run?.includes("scripts/smoke.mjs"),
  );
  const upload = build.steps.findIndex((s) =>
    s.uses?.startsWith("actions/upload-artifact@"),
  );
  assert(smoke > 0 && smoke < upload);
  assert.deepEqual(assemble.needs, ["prepare", "build"]);
  assert.match(
    assemble.steps.find((s) => s.run?.includes("package.mjs main")).run,
    /package.mjs verify/u,
  );
  const linux = build.steps.find((s) => s.run?.includes("docker run"));
  assert.match(linux.run, /packaging\/linux\/pins.json/u);
  assert.match(linux.run, /build-rust.sh binpm/u);
});

test("only live exact-tag publication receives OIDC; no static npm credential is used", () => {
  for (const [name, job] of Object.entries(workflow.jobs)) {
    if (name !== "publish") assert.equal(job.permissions, undefined);
    for (const step of job.steps)
      if (step.uses?.startsWith("actions/checkout@")) {
        assert.equal(step.with["persist-credentials"], false);
        assert.equal(
          step.with.ref,
          name === "prepare"
            ? "${{ github.sha }}"
            : "${{ needs.prepare.outputs.revision }}",
        );
      }
  }
  const publish = workflow.jobs.publish;
  assert.deepEqual(publish.permissions, {
    contents: "read",
    "id-token": "write",
  });
  assert.match(publish.if, /needs.prepare.outputs.dry_run == 'false'/u);
  assert.match(publish.if, /github.repository == 'delinoio\/oss'/u);
  assert.match(publish.if, /refs\/tags\/binpm@v/u);
  assert.doesNotMatch(text, /NPM_TOKEN|NODE_AUTH_TOKEN|secrets\.|vars\./u);
  assert.deepEqual(publish.needs, ["prepare", "package"]);
});

test("publication pins npm and reuses the complete version-and-revision-bound artifact", () => {
  const { package: assemble, publish } = workflow.jobs;
  const pin = publish.steps.findIndex((s) => s.run?.includes("npm@11.6.2"));
  const download = publish.steps.findIndex((s) =>
    s.uses?.startsWith("actions/download-artifact@"),
  );
  assert(pin >= 0 && download > pin);
  assert.equal(
    publish.steps[download].with.name,
    assemble.steps.at(-1).with.name,
  );
  assert.match(
    publish.steps[download].with.name,
    /outputs.version.*outputs.revision/u,
  );
  assert.match(publish.steps.at(-1).run, /publish.mjs --publish$/u);
});
