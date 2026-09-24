import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import yaml from "js-yaml";
import { Bump, Project, requiresCargoPublish } from "../release/project.mjs";

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
  assert.equal(jobs.prepare.needs, undefined);
  assert.deepEqual(jobs.registry.needs, ["prepare"]);
  assert.deepEqual(jobs.tag.needs, ["prepare", "registry"]);
  const publish = jobs.registry.steps.find((step) => step.name === "Publish only the selected crate");
  assert.equal(publish.if, "needs.prepare.outputs.kind == 'rust' && inputs.project != 'clibox' && inputs.project != 'pnport'");
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

test("pnport native and complete-set gates belong to its tag workflow", () => {
  const coordinator = source(".github/workflows/release-project.yml");
  assert.doesNotMatch(coordinator, /pnport-candidate|pnport-native|pnport-complete|RELEASE_CANDIDATE|test:typescript|install-smoke\.mjs/u);
  const release = yaml.load(source(".github/workflows/release-pnport.yml"));
  assert.equal(release.jobs.build.strategy.matrix.include.length, 6);
  assert.deepEqual(release.jobs.build.needs, "prepare");
  const native = release.jobs.build.steps.map((step) => step.run ?? "").join("\n");
  for (const gate of ["cargo test", "test:package", "test:typescript", "install-smoke.mjs", "evidence.mjs record"]) assert.match(native, new RegExp(gate, "u"));
  assert.deepEqual(release.jobs.package.needs, ["prepare", "build"]);
  const complete = release.jobs.package.steps.map((step) => step.run ?? "").join("\n");
  for (const gate of ["evidence.mjs assemble", "publish.mjs", "github-release.mjs", "homebrew.mjs"]) assert.ok(complete.includes(gate), gate);
  assert.deepEqual(release.jobs["publish-npm"].needs, ["prepare", "package"]);
  assert.deepEqual(release.jobs["publish-release"].needs, ["prepare", "package", "publish-npm"]);
  assert.deepEqual(release.jobs.homebrew.needs, ["prepare", "package", "publish-release"]);
});

test("pnport publication defaults to a credential-free dry run and exact tag", () => {
  const release = yaml.load(source(".github/workflows/release-pnport.yml"));
  assert.equal(release.on.workflow_dispatch.inputs.dry_run.default, "true");
  assert.deepEqual(release.permissions, { contents: "read" });
  assert.equal(release.jobs.build.strategy.matrix.include.length, 6);
  for (const jobName of ["publish-npm", "publish-release", "homebrew"]) {
    const job = release.jobs[jobName];
    assert.match(job.if, /needs\.prepare\.outputs\.dry_run == 'false'/u);
    assert.match(job.if, /refs\/tags\/pnport@v/u);
    assert.ok(job.needs.includes("package"));
  }
  assert.deepEqual(release.jobs["publish-release"].needs, ["prepare", "package", "publish-npm"]);
  assert.deepEqual(release.jobs.homebrew.needs, ["prepare", "package", "publish-release"]);
  assert.equal(release.jobs["publish-npm"].permissions["id-token"], "write");
  assert.equal(release.jobs["publish-release"].permissions["id-token"], "write");
  assert.match(source("scripts/release/update-homebrew.sh"), /conflicting pnport formula bytes/u);
});

for (const project of [Project.Clibox, Project.Pnport, Project.AsyncCommitHook]) for (const [scenario, results] of [
  ["success", ["success", "success", "success"]],
  ["prepare failure", ["failure", "skipped", "skipped"]],
  ["registry failure", ["success", "failure", "skipped"]],
  ["tag failure", ["success", "success", "failure"]],
]) test(`${project} release summary handles ${scenario} without CI outputs`, (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "release-summary-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const summaryFile = path.join(directory, "summary.md");
  const outputs = results[0] === "success" ? {
    previous_version: "1.2.2", version: "1.2.3", tag: `${project}@v1.2.3`, revision: "1".repeat(40),
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
    env: { RELEASE_PROJECT: project, RESULTS: JSON.stringify(jobs), GITHUB_STEP_SUMMARY: summaryFile },
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
    assert.ok(summary.includes(`Tag: ${project}@v1.2.3`));
    assert.doesNotMatch(summary, /Incomplete phases/u);
    if (project === Project.AsyncCommitHook) {
      assert.match(summary, /separate manual run.*release-async-commit-hook\.yml/u);
      assert.match(summary, /Select main and version 1\.2\.3; dry_run defaults to true/u);
      assert.ok(summary.includes(`Publication requires the dispatch commit to match ${outputs.revision}`));
      assert.match(summary, /never move the tag/u);
    } else assert.doesNotMatch(summary, /separate manual run/u);
  } else {
    assert.match(summary, /Incomplete phases:.*rerun this release run to reuse any recorded version/u);
    if (scenario === "prepare failure") assert.match(summary, /unresolved; inspect prepare logs/u);
    assert.doesNotMatch(summary, /preparation is complete|separate manual run/u);
  }
});

test("Source and tap tokens are separately scoped and project release triggers remain explicit", () => {
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
    if (project === Project.AsyncCommitHook) {
      assert.deepEqual(Object.keys(release.on), ["workflow_dispatch"]);
      assert.equal(release.on.workflow_dispatch.inputs.dry_run.default, true);
      assert.match(JSON.stringify(release.jobs.validate), /refs\/heads\/main/u);
      assert.equal(release.jobs.publish.environment, "async-commit-hook-release");
      continue;
    }
    assert.deepEqual(release.on.push.tags, [`${project}@v*`]);
    if ([Project.CargoMono, Project.Runmoor, Project.Clibox, Project.Pnport, Project.ReactForge].includes(project)) continue;
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

test("non-Cargo projects reach source validation and tagging without Cargo publication credentials", () => {
  const prepare = workflow.jobs.prepare.steps.find((step) => step.name === "Validate configuration before committing");
  const registryToken = "${{ inputs.project != 'clibox' && inputs.project != 'pnport' && inputs.project != 'async-commit-hook' && inputs.project != 'react-forge' && secrets.CARGO_REGISTRY_TOKEN || '' }}";
  assert.equal(prepare.env.REGISTRY_TOKEN, registryToken);
  assert.equal(workflow.jobs.registry.steps.find((step) => step.name === "Publish only the selected crate").env.CARGO_REGISTRY_TOKEN, registryToken);
  assert.match(prepare.run, /requiresCargoPublish\(plan.project\)/u);
  const preflight = prepare.run.split("<<'JS'\n")[1].split("\nJS")[0];
  const environment = { CLIENT_ID: "fixture-id", PRIVATE_KEY: "fixture-key", RELEASE_BUMP: Bump.Patch };
  const runPreflight = (project, bump = Bump.Patch) => execFileSync(process.execPath, ["--input-type=module"], {
    cwd: new URL("../../", import.meta.url),
    input: preflight,
    env: { ...environment, RELEASE_PROJECT: project, RELEASE_BUMP: bump },
    stdio: "pipe",
  });
  assert.doesNotThrow(() => runPreflight(Project.Clibox));
  assert.doesNotThrow(() => runPreflight(Project.Pnport, Bump.Minor));
  assert.doesNotThrow(() => runPreflight(Project.AsyncCommitHook));
  assert.doesNotThrow(() => runPreflight(Project.ReactForge, Bump.Minor));
  assert.throws(() => runPreflight(Project.Binpm), /CARGO_REGISTRY_TOKEN is required/u);
  const registry = workflow.jobs.registry;
  assert.equal(registry.if, undefined);
  const validation = registry.steps.find((step) => step.name === "Validate immutable release source");
  assert.equal(validation.if, undefined);
  assert.equal(validation.run, "node scripts/release/project.mjs validate");
  for (const step of registry.steps.filter((step) =>
    ["Install Rust toolchain", "Cache Rust dependencies", "Publish only the selected crate"].includes(step.name))) {
    assert.equal(step.if, "needs.prepare.outputs.kind == 'rust' && inputs.project != 'clibox' && inputs.project != 'pnport'");
  }
  for (const project of Object.values(Project)) {
    assert.equal(requiresCargoPublish(project), [Project.Binpm, Project.CargoMono, Project.Nodeup, Project.WithWatch].includes(project));
  }
});
