import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import yaml from "js-yaml";
import platforms from "../../packages/clibox/src/platforms.cjs";
import { Event, jobPaths, planJobs } from "./plan.mjs";

const source = (file) => readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const release = yaml.load(source(".github/workflows/release-clibox.yml"));
const ci = yaml.load(source(".github/workflows/CI.yml"));

test("clibox release covers all eight native targets and Alpine consumer execution", () => {
  assert.deepEqual(release.on.push.tags, ["clibox@v*"]);
  assert.equal(release.on.workflow_dispatch.inputs.dry_run.default, "true");
  assert.equal(release.concurrency.group, "release-clibox");
  assert.equal(release.concurrency["cancel-in-progress"], false);
  const matrix = release.jobs.build.strategy.matrix.include;
  assert.deepEqual(matrix.map(({ target }) => target).sort(), platforms.targets.map(({ rust }) => rust).sort());
  for (const target of platforms.targets) assert.equal(matrix.find((entry) => entry.target === target.rust).suffix, target.suffix);
  const steps = release.jobs.build.steps;
  const alpine = steps.find(({ name }) => name === "Smoke-test musl consumers in Alpine");
  assert.equal(alpine.if, "endsWith(matrix.target, '-musl')");
  assert.match(alpine.run, /node:24-alpine/u);
  assert.match(alpine.run, /test:package/u);
  assert.ok(steps.find(({ run }) => run?.includes("cargo build --locked --release -p clibox")));
  assert.ok(steps.find(({ run }) => run?.includes("package.mjs binary")));
});

test("OIDC is restricted to exact-tag enabled publication after the complete verified artifact", () => {
  assert.deepEqual(release.permissions, { contents: "read" });
  for (const [id, job] of Object.entries(release.jobs)) {
    if (id !== "publish") assert.equal(job.permissions, undefined, id);
    for (const step of job.steps) if (step.uses?.startsWith("actions/checkout@")) assert.equal(step.with["persist-credentials"], false);
  }
  assert.deepEqual(release.jobs.package.needs, ["prepare", "build"]);
  assert.deepEqual(release.jobs.publish.needs, ["prepare", "package"]);
  assert.deepEqual(release.jobs.publish.permissions, { contents: "read", "id-token": "write" });
  for (const condition of ["dry_run == 'false'", "vars.CLIBOX_NPM_PUBLISH_ENABLED == 'true'", "github.repository == 'delinoio/oss'", "refs/tags/clibox@v"]) assert.ok(release.jobs.publish.if.includes(condition));
  const publish = release.jobs.publish.steps.find(({ run }) => run?.includes("publish.mjs --publish"));
  assert.equal(publish.env.CLIBOX_NPM_PUBLISH_ENABLED, "${{ vars.CLIBOX_NPM_PUBLISH_ENABLED }}");
  assert.ok(release.jobs.package.steps.find(({ run }) => run?.includes("publish.mjs") && !run.includes("--publish")));
  assert.doesNotMatch(JSON.stringify(release), /secrets\.|NODE_AUTH_TOKEN|NPM_TOKEN|contents":"write|action-gh-release|homebrew/u);
  assert.match(source(".github/workflows/release-clibox.yml"), /npm bootstrap pending/u);
  const uploaded = release.jobs.package.steps.find(({ uses }) => uses?.startsWith("actions/upload-artifact@"));
  const downloaded = release.jobs.publish.steps.find(({ uses }) => uses?.startsWith("actions/download-artifact@"));
  assert.equal(uploaded.with.name, downloaded.with.name);
});

test("publication installs an exact OIDC-capable npm before checking and using it", () => {
  const steps = release.jobs.publish.steps;
  const setup = steps.findIndex(({ uses }) => uses?.startsWith("actions/setup-node@"));
  const install = steps.findIndex(({ name }) => name === "Install pinned npm for trusted publishing");
  const check = steps.findIndex(({ name }) => name === "Check npm trusted publishing support");
  const publish = steps.findIndex(({ run }) => run?.includes("publish.mjs --publish"));
  assert.ok(setup >= 0 && setup < install && install < check && check < publish);
  assert.equal(steps[install].run, "npm install --global npm@11.6.2 --ignore-scripts --no-audit --no-fund");
  assert.match(steps[check].run, /npm\(\['--version'\]\)/u);
  assert.match(steps[check].run, /npm 11\.5\.1\+ is required for OIDC/u);
});

test("clibox input changes select its aggregated consumer checks and force external Cargo inputs", () => {
  const id = "node-clibox-test";
  assert.equal(jobPaths[id].workspace, "@delino/clibox");
  assert.ok(ci.jobs["ci-result"].needs.includes(id));
  assert.deepEqual(ci.jobs[id].strategy.matrix.os, ["ubuntu-22.04", "macos-14", "windows-latest"]);
  for (const event of [Event.Push, Event.PullRequest]) {
    for (const file of ["packages/clibox/src/launcher.cjs", "crates/clibox/src/main.rs", ".github/workflows/release-clibox.yml", "scripts/release/project.mjs"]) {
      const plan = planJobs(event, [file]);
      assert.equal(plan.jobs[id], true, file);
      assert.equal(plan.forced[id], !file.startsWith("packages/clibox/"), file);
    }
    assert.equal(planJobs(event, ["apps/mpapp/src/unrelated.ts"]).jobs[id], false);
  }
  const turbo = JSON.parse(source("packages/clibox/turbo.json"));
  assert.equal(turbo.tasks["test:package"].cache, false);
  assert.equal(turbo.tasks["publish:npm"].cache, false);
  assert.ok(turbo.tasks.test.inputs.includes("$TURBO_ROOT$/crates/clibox/**"));
});
