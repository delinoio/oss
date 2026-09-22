import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { load } from "js-yaml";
import { Event, planJobs } from "./plan.mjs";

const read = (file) => readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const workflow = (name) => load(read(`.github/workflows/${name}.yml`));
const setupIndex = (steps) => steps.findIndex(({ uses }) => uses === "./.github/actions/setup-clibox");

test("clibox setup uses the lockfile and reuses existing installs", () => {
  const action = load(read(".github/actions/setup-clibox/action.yml"));
  const install = action.runs.steps.find(({ run }) => run?.startsWith("pnpm install"));
  assert.equal(install.run, "pnpm install --frozen-lockfile --ignore-scripts --filter delinoio-oss-workspace");
  assert.equal(install.if, "${{ inputs.install == 'true' }}");
  assert.equal(install["working-directory"], "${{ inputs.working-directory }}");
  assert.equal(action.runs.steps.at(-1).run, "node scripts/setup/verify-clibox.cjs");
  const ci = workflow("CI");
  for (const id of ["node-clibox-test", "devhud-android-emulator", "devhud-supply-chain", "devhud-release-contracts"]) {
    const steps = ci.jobs[id].steps;
    const setup = setupIndex(steps);
    const installs = steps.filter(({ run }) => run?.includes("pnpm install"));
    assert.equal(installs.length, 1, id);
    assert.ok(steps.indexOf(installs[0]) < setup, id);
    assert.equal(steps[setup].with.install, "false", id);
  }
});

test("release utilities are installed before checksumming, rendering, or secret decoding", () => {
  for (const name of ["binpm", "cargo-mono", "nodeup", "with-watch", "derun", "async-commit-hook"]) {
    const steps = workflow(`release-${name}`).jobs.publish.steps;
    const setup = setupIndex(steps);
    assert.ok(setup >= 0, name);
    assert.ok(setup < steps.findIndex(({ run }) => run?.includes("generate-checksums.sh")), name);
  }
  const privateJobs = workflow("package-devhud-private").jobs;
  for (const id of ["desktop", "mobile", "assemble"]) {
    const steps = privateJobs[id].steps;
    const setup = setupIndex(steps);
    assert.ok(setup >= 0, id);
    for (const [index, { run }] of steps.entries()) {
      if (/\bclibox (base64|hash|time)\b/u.test(run ?? "")) assert.ok(setup < index, id);
    }
  }
  const source = read(".github/workflows/package-devhud-private.yml");
  assert.doesNotMatch(source, /clibox base64 decode[^\n]*--text/u);
  assert.ok(source.includes('if ($LASTEXITCODE -ne 0) { throw "PFX Base64 decoding failed" }'));
});

test("historical store recovery gets clibox from the workflow revision without replacing release source", () => {
  const steps = workflow("release-devhud").jobs.submit_stores.steps;
  assert.equal(steps[0].with.ref, "${{ needs.identity.outputs.revision }}");
  const tooling = steps.find(({ name }) => name === "Checkout immutable workflow tooling");
  assert.equal(tooling.with.ref, "${{ github.workflow_sha }}");
  assert.equal(tooling.with.path, ".clibox-tooling");
  assert.equal(tooling.with["persist-credentials"], false);
  const setup = steps.find(({ uses }) => uses === "./.clibox-tooling/.github/actions/setup-clibox");
  assert.equal(setup.with["working-directory"], tooling.with.path);
  assert.equal(setup.if, tooling.if);
  assert.ok(steps.indexOf(tooling) < steps.indexOf(setup));
  assert.ok(steps.indexOf(setup) < steps.findIndex(({ run }) => run?.includes("clibox base64 decode")));
});

test("shared clibox launcher changes select its consumers and release fixtures", () => {
  const jobs = planJobs(Event.Push, ["scripts/clibox.cjs"]).jobs;
  for (const id of ["node-clibox-test", "devhud-supply-chain", "devhud-release-contracts", "devhud-desktop"]) assert.equal(jobs[id], true, id);
});
