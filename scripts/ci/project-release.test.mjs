import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
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

test("Publication follows preparation without CI and tags only after registry success", () => {
  const jobs = workflow.jobs;
  assert.deepEqual(Object.keys(jobs), ["prepare", "registry", "tag", "summary"]);
  assert.deepEqual(jobs.registry.needs, ["prepare"]);
  assert.deepEqual(jobs.tag.needs, ["prepare", "registry"]);
  const publish = jobs.registry.steps.find((step) => step.name === "Publish only the selected crate");
  assert.equal(publish.if, "needs.prepare.outputs.kind == 'rust'");
  assert.equal(publish.run, 'cargo run --locked -p cargo-mono -- publish --package "$RELEASE_PROJECT"');
  assert.equal(publish["continue-on-error"], undefined);
  for (const name of ["registry", "tag"]) {
    assert.equal(jobs[name].if, undefined);
    assert.equal(jobs[name]["continue-on-error"], undefined);
    const checkout = jobs[name].steps.find((step) => step.uses?.startsWith("actions/checkout@"));
    assert.equal(checkout.with.ref, "${{ needs.prepare.outputs.revision }}");
    assert.equal(checkout.with["persist-credentials"], false);
    assert.equal(jobs[name].env.RELEASE_REVISION, "${{ needs.prepare.outputs.revision }}");
  }
  const script = source("scripts/release/project.mjs");
  assert.doesNotMatch(script, /--force|push.+--tags|x-access-token:/u);
  assert.match(script, /:refs\/tags\/\$\{identity.tag\}/u);
  assert.doesNotMatch(script, /waitForCiWorkflow|wait-ci|\/actions|CI\.yml/u);
  assert.doesNotMatch(source(".github/workflows/release-project.yml"), /wait-ci|wait-release|ci_url|jobs\.ci|needs\.ci|actions: read/u);
  assert.equal(jobs.summary.if, "always()");
  assert.deepEqual(jobs.summary.needs, ["prepare", "registry", "tag"]);
});

for (const [scenario, results] of [
  ["success", ["success", "success", "success"]],
  ["prepare failure", ["failure", "skipped", "skipped"]],
  ["registry failure", ["success", "failure", "skipped"]],
  ["tag failure", ["success", "success", "failure"]],
]) test(`Release summary handles ${scenario} without CI outputs`, (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "release-summary-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const summaryFile = path.join(directory, "summary.md");
  const outputs = results[0] === "success" ? {
    previous_version: "1.2.2", version: "1.2.3", tag: "clibox@v1.2.3", revision: "1".repeat(40),
  } : {};
  const jobs = {
    prepare: { result: results[0], outputs },
    registry: { result: results[1] },
    tag: { result: results[2] },
  };
  const step = workflow.jobs.summary.steps.find((step) => step.name === "Report release outcome");
  assert.equal(step.env.RESULTS, "${{ toJSON(needs) }}");
  const script = /^node <<'JS'\n([\s\S]+)\nJS\n?$/u.exec(step.run);
  assert.ok(script, "Summary must expose its Node script for fixture execution");
  const result = spawnSync(process.execPath, ["-e", script[1]], {
    encoding: "utf8",
    env: { RELEASE_PROJECT: Project.Clibox, RESULTS: JSON.stringify(jobs), GITHUB_STEP_SUMMARY: summaryFile },
  });
  assert.equal(result.error, undefined);
  assert.equal(result.status, scenario === "success" ? 0 : 1, result.stderr);
  assert.equal(result.stderr, "");
  const summary = readFileSync(summaryFile, "utf8");
  for (const [name, job] of Object.entries(jobs)) assert.ok(summary.includes(`| ${name} | ${job.result} |`));
  assert.doesNotMatch(summary, /\| ci \||CI Result|Workflow run|undefined/u);
  if (scenario === "success") {
    assert.ok(summary.includes("Version: 1.2.2 → 1.2.3"));
    assert.ok(summary.includes(`Commit: ${outputs.revision}`));
    assert.ok(summary.includes("Tag: clibox@v1.2.3"));
    assert.doesNotMatch(summary, /Incomplete phases/u);
  } else {
    assert.match(summary, /Incomplete phases:.*rerun this release run to reuse any recorded version/u);
    if (scenario === "prepare failure") assert.match(summary, /unresolved; inspect prepare logs/u);
  }
});

test("Source and tap tokens are separately scoped and all selected tag workflows remain available", () => {
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
    if ([Project.CargoMono, Project.Runmoor, Project.Clibox].includes(project)) continue;
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
