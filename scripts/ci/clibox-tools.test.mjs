import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { load } from "js-yaml";
import { Event, planJobs } from "./plan.mjs";

const read = (file) =>
  readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const workflow = (name) => load(read(`.github/workflows/${name}.yml`));
const setupIndex = (steps) =>
  steps.findIndex(({ uses }) => uses === "./.github/actions/setup-clibox");

test("clibox setup uses the lockfile and reuses existing installs", () => {
  const action = load(read(".github/actions/setup-clibox/action.yml"));
  const install = action.runs.steps.find(({ run }) =>
    run?.startsWith("pnpm install"),
  );
  assert.equal(
    install.run,
    "pnpm install --frozen-lockfile --ignore-scripts --filter delinoio-oss-workspace",
  );
  assert.equal(install.if, "${{ inputs.install == 'true' }}");
  assert.equal(install["working-directory"], "${{ inputs.working-directory }}");
  assert.ok(
    action.runs.steps.at(-1).run.includes("pnpm exec clibox --version"),
  );
  assert.ok(
    action.runs.steps
      .at(-1)
      .run.includes('test "$actual" = "clibox $expected"'),
  );
  const ci = workflow("CI");
  for (const id of ["node-clibox-test"]) {
    const steps = ci.jobs[id].steps;
    const setup = setupIndex(steps);
    const installs = steps.filter(({ run }) => run?.includes("pnpm install"));
    assert.equal(installs.length, 1, id);
    assert.ok(steps.indexOf(installs[0]) < setup, id);
    assert.equal(steps[setup].with.install, "false", id);
  }
});

test("release utilities are installed before checksumming, rendering, or secret decoding", () => {
  for (const name of [
    "binpm",
    "cargo-mono",
    "nodeup",
    "with-watch",
    "async-commit-hook",
  ]) {
    const steps = workflow(`release-${name}`).jobs.publish.steps;
    const setup = setupIndex(steps);
    assert.ok(setup >= 0, name);
    const consumer = steps.findIndex(({ run }) =>
      run?.includes(
        name === "async-commit-hook"
          ? "generate-checksums.sh"
          : "legacy-cli-release.mjs",
      ),
    );
    assert.ok(consumer >= 0, name);
    assert.ok(setup < consumer, name);
  }
});

test("clibox dependency changes select its consumers and release fixtures", () => {
  const jobs = planJobs(Event.Push, ["pnpm-lock.yaml"]).jobs;
  for (const id of ["node-clibox-test"]) assert.equal(jobs[id], true, id);
});
