import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import yaml from "js-yaml";
import { releasePlan } from "./tairouter.mjs";
import { Project, readVersion } from "./project.mjs";

const version = readVersion(Project.TaiRouter);
const tag = `tairouter@v${version}`;
const revision = "a".repeat(40);
const env = {
  GITHUB_REPOSITORY: "delinoio/oss",
  GITHUB_EVENT_NAME: "workflow_dispatch",
  GITHUB_REF: "refs/heads/main",
  GITHUB_SHA: revision,
  REQUESTED_VERSION: version,
  DRY_RUN: "false",
};
const target = (sha) => async () => ({
  status: 200,
  body: { object: { type: "commit", sha } },
});

test("TaiRouter publication requires an existing tag at the exact source commit", async () => {
  assert.deepEqual(await releasePlan(env, target(revision)), {
    version,
    tag,
    dry_run: "false",
  });
  assert.deepEqual(
    await releasePlan(
      { ...env, GITHUB_EVENT_NAME: "push", GITHUB_REF: `refs/tags/${tag}` },
      target(revision),
    ),
    { version, tag, dry_run: "false" },
  );
  await assert.rejects(
    releasePlan(env, target("b".repeat(40))),
    /existing tag/u,
  );
  await assert.rejects(
    releasePlan(env, async () => ({ status: 404 })),
    /existing tag/u,
  );
  await assert.rejects(
    releasePlan(env, async () => ({ status: 403 })),
    /ownership/u,
  );
  for (const change of [
    { GITHUB_REPOSITORY: "fork/oss" },
    { GITHUB_REF: "refs/heads/topic" },
    { REQUESTED_VERSION: "999.0.0" },
    { GITHUB_SHA: "invalid" },
    { GITHUB_EVENT_NAME: "pull_request" },
  ])
    await assert.rejects(releasePlan({ ...env, ...change }, target(revision)));
});

test("TaiRouter dry runs need no tag or publication credentials", async () => {
  const plan = await releasePlan(
    { ...env, DRY_RUN: "true", GITHUB_REF: "refs/heads/topic" },
    () => {
      throw new Error("No API access expected");
    },
  );
  assert.equal(plan.dry_run, "true");
});

test("TaiRouter gates all publication behind fixtures and dual-architecture image builds", () => {
  const workflow = yaml.load(
    readFileSync(
      new URL("../../.github/workflows/release-tairouter.yml", import.meta.url),
      "utf8",
    ),
  );
  assert.deepEqual(workflow.on.push.tags, ["tairouter@v*"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.deepEqual(workflow.jobs.publish.needs, ["plan", "validate", "image"]);
  assert.deepEqual(workflow.jobs["publish-npm"].needs, ["plan", "publish"]);
  for (const name of ["publish", "publish-npm"])
    assert.match(workflow.jobs[name].if, /dry_run == 'false'/u);
  assert.match(
    workflow.jobs["publish-npm"].if,
    /TAIROUTER_NPM_PUBLISH_ENABLED == 'true'/u,
  );
  assert.equal(workflow.jobs["publish-npm"].environment, "tairouter-npm");
  const validation = JSON.stringify(workflow.jobs.validate);
  for (const gate of [
    "go test -race",
    "generate:check",
    "compose-smoke.py",
    "router-load.py",
    "build-release.py",
    "promtool",
  ])
    assert.ok(validation.includes(gate), gate);
  const image = workflow.jobs.image.steps.find(
    (step) => step.name === "Build Linux images",
  );
  assert.match(image.run, /--platform linux\/amd64,linux\/arm64/u);
  assert.match(image.run, /type=oci/u);
  assert.match(image.run, /RELEASE_DRY_RUN.*false/u);
  assert.doesNotMatch(JSON.stringify(workflow), /macos-|secrets\.NPM_TOKEN/u);
});
