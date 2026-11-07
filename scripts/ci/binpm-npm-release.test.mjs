// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import yaml from "js-yaml";

const workflow = yaml.load(
  readFileSync(".github/workflows/release-binpm-npm.yml", "utf8"),
);

test("binpm npm release is manual, main-only and dry-run by default", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.deepEqual(Object.keys(workflow.jobs), ["package", "publish"]);
  for (const job of Object.values(workflow.jobs)) {
    assert.match(job.if, /github\.repository == 'delinoio\/oss'/u);
    assert.match(job.if, /github\.ref == 'refs\/heads\/main'/u);
  }
});

test("only the publish job receives the OIDC token and no static npm secret", () => {
  const { package: packageJob, publish } = workflow.jobs;
  assert.equal(packageJob.permissions, undefined);
  assert.deepEqual(publish.permissions, {
    contents: "read",
    "id-token": "write",
  });
  assert.match(publish.if, /!inputs\.dry_run/u);
  assert.match(publish.if, /vars\.BINPM_NPM_PUBLISH_ENABLED == 'true'/u);
  assert.equal(publish.needs, "package");
  const text = readFileSync(".github/workflows/release-binpm-npm.yml", "utf8");
  assert.doesNotMatch(text, /NPM_TOKEN|NODE_AUTH_TOKEN|secrets\./u);
  for (const job of Object.values(workflow.jobs))
    for (const step of job.steps)
      if (step.uses?.startsWith("actions/checkout@")) {
        assert.equal(step.with.ref, "${{ github.sha }}");
        assert.equal(step.with["persist-credentials"], false);
      }
});

test("publish pins an OIDC-capable npm and publishes the verified artifact", () => {
  const steps = workflow.jobs.publish.steps;
  const pin = steps.find(({ run }) => run?.includes("npm@11.6.2"));
  assert(pin, "npm must be pinned to a trusted-publishing version");
  assert(
    steps.findIndex((s) => s.uses?.startsWith("actions/download-artifact@")) >
      steps.indexOf(pin),
  );
  assert.match(steps.at(-1).run, /publish\.mjs --publish$/u);
});
