import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import yaml from "js-yaml";
import launcher from "../bin/bundlewhy.cjs";
import { metadata, npm, sourceText, targets } from "../scripts/common.mjs";
import {
  executableTarball,
  inspectTarball,
  packageManifest,
  tarballName,
  verifySet,
} from "../scripts/package.mjs";
import { stage } from "../scripts/github-release.mjs";
import { publishArtifacts } from "../../clibox/scripts/publish.mjs";

const revision = "1".repeat(40);
const { version } = metadata();
const source = (file) =>
  readFileSync(new URL(`../../../${file}`, import.meta.url), "utf8");

function fixture(t) {
  const directory = mkdtempSync(
    path.join(tmpdir(), "bundlewhy release fixture "),
  );
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const output = path.join(directory, "tarballs");
  mkdirSync(output);
  for (const target of [...targets, undefined]) {
    const manifest = packageManifest(target, version, revision);
    const folder = path.join(directory, target?.suffix ?? "main");
    mkdirSync(path.join(folder, "bin"), { recursive: true });
    writeFileSync(path.join(folder, "package.json"), JSON.stringify(manifest));
    writeFileSync(
      path.join(folder, "README.md"),
      sourceText("packages/bundlewhy/README.md"),
    );
    for (const file of ["LICENSE", "NOTICE"])
      writeFileSync(path.join(folder, file), sourceText(file));
    const executable = target ? `bin/${target.binary}` : "bin/bundlewhy.cjs";
    writeFileSync(
      path.join(folder, executable),
      target
        ? `fixture ${target.suffix}`
        : sourceText("packages/bundlewhy/bin/bundlewhy.cjs"),
    );
    const [packed] = JSON.parse(
      npm(
        ["pack", "--json", "--ignore-scripts", "--pack-destination", output],
        { cwd: folder },
      ),
    );
    const file = path.join(output, packed.filename);
    writeFileSync(file, executableTarball(readFileSync(file), executable));
  }
  return { directory, output };
}

test("release matrix matches the launcher and source versions reject drift", () => {
  assert.deepEqual(
    targets.map((target) => target.suffix).sort(),
    Object.values(launcher.PLATFORM_PACKAGES).sort(),
  );
  for (const target of targets) {
    const manifest = packageManifest(target, version, revision);
    assert.deepEqual(manifest.os, [target.os]);
    assert.deepEqual(manifest.cpu, [target.cpu]);
    if (target.libc) assert.deepEqual(manifest.libc, ["glibc"]);
    assert.equal(manifest.scripts, undefined);
  }
  const main = packageManifest(undefined, version, revision);
  assert.deepEqual(
    main.optionalDependencies,
    Object.fromEntries(targets.map((target) => [target.name, version])),
  );
  assert.equal(main.engines.node, ">=22.18");
  assert.equal(main.private, undefined);
  assert.throws(
    () =>
      metadata((file) =>
        sourceText(file).replace(
          `"version": "${version}"`,
          '"version": "99.0.0"',
        ),
      ),
    /version mismatch/u,
  );
  assert.throws(
    () => packageManifest(undefined, version, "main"),
    /exact source commit/u,
  );
});

test("complete set binds packages to source and preserves immutable input", (t) => {
  const { directory, output } = fixture(t);
  const artifacts = verifySet(output, revision);
  assert.equal(artifacts.length, 4);
  assert.equal(artifacts.at(-1).name, "@delino/bundlewhy");
  const before = artifacts.map((item) =>
    readFileSync(path.join(output, item.filename)),
  );
  const candidate = stage(output, path.join(directory, "github"), revision);
  assert.equal(candidate.plan.tag, `bundlewhy@v${version}`);
  assert.equal(candidate.files.size, 5);
  assert.equal(
    candidate.files.get("SHA256SUMS").toString().trim().split("\n").length,
    4,
  );
  for (let index = 0; index < artifacts.length; index++)
    assert.deepEqual(
      readFileSync(path.join(output, artifacts[index].filename)),
      before[index],
    );
  assert.throws(() => verifySet(output, "2".repeat(40)), /metadata mismatch/u);
  writeFileSync(path.join(output, "unexpected.tgz"), "unexpected");
  assert.throws(() => verifySet(output, revision), /exactly four/u);
});

test("tarball verification rejects swapped packages and missing native targets", (t) => {
  const { output } = fixture(t);
  const main = path.join(output, tarballName("@delino/bundlewhy", version));
  // Another validly named archive cannot masquerade as the launcher.
  writeFileSync(
    main,
    readFileSync(path.join(output, tarballName(targets[0].name, version))),
  );
  assert.throws(
    () => inspectTarball(main, { version, sourceRevision: revision }),
    /Tarball name mismatch/u,
  );
  rmSync(main);
  assert.throws(
    () => stage(output, path.join(output, "github"), revision),
    /exactly four/u,
  );
});

test("publication confirms all native packages before the launcher and reuses exact versions", async (t) => {
  const { output } = fixture(t);
  const artifacts = verifySet(output, revision);
  const remote = new Map([[artifacts[0].name, artifacts[0].integrity]]);
  const writes = [];
  await publishArtifacts(artifacts, {
    dryRun: false,
    report: () => {},
    lookup: async (artifact) => remote.get(artifact.name) ?? null,
    publish: async (artifact) => {
      if (artifact.name === "@delino/bundlewhy")
        for (const target of targets) assert.ok(remote.has(target.name));
      writes.push(artifact.name);
      remote.set(artifact.name, artifact.integrity);
    },
  });
  assert.deepEqual(
    writes,
    artifacts.slice(1).map((artifact) => artifact.name),
  );
  remote.set(artifacts.at(-1).name, "conflicting");
  await assert.rejects(
    publishArtifacts(artifacts, {
      dryRun: false,
      lookup: async (artifact) => remote.get(artifact.name),
      publish: () => assert.fail("must reject before writes"),
    }),
    /Conflicting published integrity/u,
  );
});

test("release workflow builds the three native targets with embedded UI before any publication", () => {
  const workflow = yaml.load(source(".github/workflows/release-bundlewhy.yml"));
  assert.deepEqual(workflow.on.push.tags, ["bundlewhy@v*"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, "true");
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  assert.deepEqual(
    workflow.jobs.build.strategy.matrix.include.map((item) => item.target),
    targets.map((item) => item.rust),
  );
  const steps = workflow.jobs.build.steps;
  const ui = steps.findIndex(
    (step) => step.run === "pnpm --filter bundlewhy-web test",
  );
  assert.ok(
    ui > 0 &&
      ui < steps.findIndex((step) => step.name?.startsWith("Build Linux")),
  );
  assert.match(
    steps.find((step) => step.name === "Test and package the native executable")
      .run,
    /test:package.*--binary/u,
  );
  assert.doesNotMatch(JSON.stringify(workflow), /BUNDLEWHY_ALLOW_MISSING_UI/u);
  for (const name of ["publish", "publish-release"]) {
    assert.deepEqual(workflow.jobs[name].needs, ["prepare", "package"]);
    assert.match(
      workflow.jobs[name].if,
      /dry_run == 'false'.*delinoio\/oss.*refs\/tags\/bundlewhy@v/u,
    );
    assert.equal(workflow.jobs[name].permissions["id-token"], "write");
  }
});

test("prepare permits development dry runs and rejects publication outside the exact source tag", (t) => {
  const workflow = yaml.load(source(".github/workflows/release-bundlewhy.yml"));
  const script = workflow.jobs.prepare.steps
    .find((step) => step.id === "source")
    .run.split("<<'JS'\n")[1]
    .split("\nJS")[0];
  const cwd = new URL("../../../", import.meta.url);
  const commit = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd,
    encoding: "utf8",
  }).trim();
  const directory = mkdtempSync(path.join(tmpdir(), "bundlewhy prepare "));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const invoke = (overrides) =>
    spawnSync(process.execPath, ["--input-type=module"], {
      cwd,
      input: script,
      encoding: "utf8",
      env: {
        ...process.env,
        GITHUB_EVENT_NAME: "workflow_dispatch",
        GITHUB_REF: "refs/heads/development",
        GITHUB_SHA: commit,
        GITHUB_REPOSITORY: "delinoio/oss",
        GITHUB_OUTPUT: path.join(directory, "outputs"),
        REQUESTED_VERSION: version,
        REQUESTED_DRY_RUN: "true",
        ...overrides,
      },
    });
  assert.equal(invoke({}).status, 0);
  assert.match(
    readFileSync(path.join(directory, "outputs"), "utf8"),
    /dry_run=true/u,
  );
  assert.notEqual(invoke({ REQUESTED_DRY_RUN: "false" }).status, 0);
  assert.notEqual(
    invoke({
      REQUESTED_DRY_RUN: "false",
      GITHUB_REF: `refs/tags/bundlewhy@v${version}`,
      GITHUB_SHA: "2".repeat(40),
    }).status,
    0,
  );
  assert.notEqual(
    invoke({
      REQUESTED_DRY_RUN: "false",
      GITHUB_REF: `refs/tags/bundlewhy@v${version}`,
      GITHUB_REPOSITORY: "other/fork",
    }).status,
    0,
  );
  assert.equal(
    invoke({
      GITHUB_EVENT_NAME: "push",
      GITHUB_REF: `refs/tags/bundlewhy@v${version}`,
    }).status,
    0,
  );
});
