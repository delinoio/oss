import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import test from "node:test";
import yaml from "js-yaml";
import { Bump, Project } from "../release/project.mjs";

const source = (file) => readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const workflow = yaml.load(source(".github/workflows/release-project.yml"));

test("Selected release is manual, serialized, main-only and permission bounded", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  const inputs = workflow.on.workflow_dispatch.inputs;
  assert.deepEqual(Object.keys(inputs), ["project", "bump"]);
  assert.deepEqual(inputs.project.options, Object.values(Project));
  assert.deepEqual(inputs.bump.options, Object.values(Bump));
  assert.equal(inputs.bump.default, "patch");
  assert.equal(inputs.project.type, "choice");
  assert.equal(inputs.bump.type, "choice");
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  assert.equal(workflow.concurrency.group, "release-project");
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(workflow.jobs.prepare.if, "github.repository == 'delinoio/oss' && github.ref == 'refs/heads/main'");
  assert.equal(existsSync(new URL("../../.github/workflows/auto-publish.yml", import.meta.url)), false);
});

test("Publication follows exact-commit CI, pushes the selected tag, and does not await downstream release", () => {
  const jobs = workflow.jobs;
  assert.deepEqual(jobs.registry.needs, ["prepare", "ci"]);
  assert.deepEqual(jobs.tag.needs, ["prepare", "registry"]);
  assert.equal(jobs.release, undefined);
  const publish = jobs.registry.steps.find((step) => step.name === "Publish only the selected crate");
  assert.equal(publish.if, "needs.prepare.outputs.kind == 'rust'");
  assert.equal(publish.run, 'cargo run --locked -p cargo-mono -- publish --package "$RELEASE_PROJECT"');
  for (const name of ["ci", "registry", "tag"]) {
    const checkout = jobs[name].steps.find((step) => step.uses?.startsWith("actions/checkout@"));
    assert.equal(checkout.with.ref, "${{ needs.prepare.outputs.revision }}");
    assert.equal(checkout.with["persist-credentials"], false);
    assert.equal(jobs[name].env.RELEASE_REVISION, "${{ needs.prepare.outputs.revision }}");
  }
  const script = source("scripts/release/project.mjs");
  assert.doesNotMatch(script, /--force|push.+--tags|x-access-token:/u);
  assert.match(script, /:refs\/tags\/\$\{identity.tag\}/u);
  for (const name of ["ci"]) {
    assert.deepEqual(jobs[name].permissions, { contents: "read", actions: "read" });
    assert.ok(!JSON.stringify(jobs[name]).includes("PRIVATE_KEY"));
  }
  assert.doesNotMatch(source(".github/workflows/release-project.yml"), /wait-release/u);
  assert.equal(jobs.summary.if, "always()");
  assert.deepEqual(jobs.summary.needs, ["prepare", "ci", "registry", "tag"]);
});

test("Source and tap tokens are separately scoped and all six tag workflows remain available", () => {
  for (const name of ["prepare", "tag"]) {
    const token = workflow.jobs[name].steps.find((step) => step.uses === "actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1");
    assert.equal(token.with.repositories, "oss");
    assert.equal(token.with.owner, "delinoio");
    assert.equal(token.with["permission-contents"], "write");
    assert.equal(token.with["skip-token-revoke"], undefined);
    assert.equal(token.with["client-id"], "${{ vars.DELINO_RELEASE_BOT_CLIENT_ID }}");
    assert.equal(token.with["private-key"], "${{ secrets.DELINO_RELEASE_BOT_PRIVATE_KEY }}");
  }
  for (const project of Object.values(Project)) {
    const release = yaml.load(source(`.github/workflows/release-${project}.yml`));
    assert.deepEqual(release.on.push.tags, [`${project}@v*`]);
    if ([Project.CargoMono, Project.Runmoor].includes(project)) continue;
    const steps = release.jobs.publish.steps;
    const token = steps.find((step) => step.uses === "actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1");
    assert.equal(token.with.repositories, "homebrew-tap");
    assert.equal(token.with.owner, "delinoio");
    assert.equal(token.with["permission-contents"], "write");
    assert.equal(token.if, "env.DRY_RUN != 'true'");
    const submit = steps.find((step) => step.name === "Submit Homebrew update");
    assert.equal(submit.env.GH_TOKEN, "${{ steps.tap-bot.outputs.token }}");
    assert.equal(submit.env.RELEASE_BOT_NAME, "${{ steps.tap-identity.outputs.name }}");
    assert.ok(steps.indexOf(token) < steps.indexOf(submit));
    assert.doesNotMatch(JSON.stringify(release), /secrets\.GH_TOKEN/u);
  }
  const tap = source("scripts/release/update-homebrew.sh");
  assert.doesNotMatch(tap, /x-access-token:|remote set-url/u);
  assert.match(tap, /credential\.helper=!gh auth git-credential/u);
});
