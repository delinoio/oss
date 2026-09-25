import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import yaml from "js-yaml";

const root = new URL("../../", import.meta.url);
const source = (file) => readFileSync(new URL(file, root), "utf8");
const release = yaml.load(source(".github/workflows/release-react-forge.yml"));
const ci = yaml.load(source(".github/workflows/CI.yml"));
const platforms = JSON.parse(source("packages/react-forge/src/native-platforms.json"));

test("React Forge release is exact-tag or credential-free manual dry run", () => {
  assert.deepEqual(release.on.push.tags, ["react-forge@v*"]);
  assert.equal(release.on.workflow_dispatch.inputs.dry_run.default, "true");
  assert.deepEqual(release.permissions, { contents: "read" });
  assert.equal(release.concurrency["cancel-in-progress"], false);
  assert.deepEqual(release.jobs.build.strategy.matrix.include.map(({ id, target }) => ({ id, target })), platforms.map(({ id, target }) => ({ id, target })));
  assert.deepEqual(release.jobs.package.needs, ["prepare", "build"]);
  assert.deepEqual(release.jobs["publish-npm"].needs, ["prepare", "package"]);
  assert.match(release.jobs["publish-npm"].if, /refs\/tags\/react-forge@v/u);
  assert.equal(release.jobs["publish-npm"].permissions["id-token"], "write");
  assert.ok(release.jobs["publish-npm"].steps.some((step) => step.run?.includes("publish.mjs --publish")));
  assert.ok(!JSON.stringify(release).includes("NPM_TOKEN"));
});

test("release runs the same host validation before complete candidate assembly", () => {
  const sharedCommand = 'bash packages/react-forge/scripts/validate-host.sh "${{ matrix.target }}"';
  assert.ok(ci.jobs["react-forge"].steps.some((step) => step.run === sharedCommand));
  assert.ok(release.jobs.build.steps.some((step) => step.run === sharedCommand));
  const validation = source("packages/react-forge/scripts/validate-host.sh");
  for (const command of ["install-smoke.mjs", "test:render", "benchmark", "typecheck:examples", "examples/travel-ir.tsx"]) assert.match(validation, new RegExp(command.replaceAll(".", "\\."), "u"));
  assert.equal(ci.jobs["react-forge-package"], undefined);
  assert.ok(!ci.jobs["react-forge"].steps.some((step) => String(step.with?.name).startsWith("react-forge-native-")));
  assert.ok(release.jobs.package.steps.some((step) => step.run?.includes("package.mjs verify")));
});
