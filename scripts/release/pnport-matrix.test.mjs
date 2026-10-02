import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import { load } from "js-yaml";
import { nativeMatrix } from "../../packages/pnport/scripts/native-matrix.mjs";
import { Event, matricesForEvent } from "../ci/plan.mjs";

const require = createRequire(import.meta.url);
const { targets } = require("../../packages/pnport/src/platforms.cjs");
const read = (file) => readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const ci = load(read(".github/workflows/CI.yml"));
const release = load(read(".github/workflows/release-pnport.yml"));

test("CI and exact-tag release use the package-owned four native hosts", () => {
  assert.deepEqual(targets.map(({ suffix }) => suffix), ["darwin-x64", "darwin-arm64", "linux-x64-gnu", "linux-arm64-gnu"]);
  assert.deepEqual(nativeMatrix.include.map(({ target, suffix }) => ({ target, suffix })), targets.map(({ rust, suffix }) => ({ target: rust, suffix })));
  assert.deepEqual(nativeMatrix.include.map(({ runner }) => runner), ["macos-15-intel", "macos-14", "ubuntu-22.04", "ubuntu-22.04-arm"]);
  for (const event of Object.values(Event)) assert.deepEqual(matricesForEvent(event).pnportMatrix, nativeMatrix);
  assert.equal(ci.jobs.changes.outputs.pnport_matrix, "${{ steps.plan.outputs.pnport_matrix }}");
  assert.equal(ci.jobs["pnport-native"].strategy.matrix, "${{ fromJSON(needs.changes.outputs.pnport_matrix) }}");
  assert.equal(release.jobs.prepare.outputs.matrix, "${{ steps.source.outputs.matrix }}");
  assert.equal(release.jobs.build.strategy.matrix, "${{ fromJSON(needs.prepare.outputs.matrix) }}");
  const source = release.jobs.prepare.steps.find(({ id }) => id === "source").run;
  assert.match(source, /matrix: JSON\.stringify\(nativeMatrix\)/u);
  assert.ok(source.includes("if (source.dry_run !== 'true') requireReleaseReady();"));
  for (const workflow of [ci, release]) {
    const job = workflow.jobs["pnport-native"] ?? workflow.jobs.build;
    assert.equal(job.env.MACOSX_DEPLOYMENT_TARGET, "13.0");
    const commands = job.steps.map(({ run }) => run ?? "").join("\n");
    for (const gate of ["cargo test --locked -p pnport", "test:package", "test:typescript", "install-smoke.mjs"]) assert.ok(commands.includes(gate), gate);
    assert.ok(commands.includes("-p pnport-core -p pnport-preload"));
    assert.ok(commands.includes('cargo build --locked -p pnport-preload --target "$PNPORT_TARGET"'));
    assert.ok(commands.includes("cargo test --locked -p fspy_preload_unix --features fspy_preload_unix/pnport"));
    assert.ok(!commands.includes("--test-threads=1"));
  }
});

test("Windows installer rejects requests before network, file, or process side effects", () => {
  const source = read("scripts/install/pnport.ps1");
  const gate = source.indexOf('throw "[install.pnport] Windows is not supported by this release.');
  assert.ok(gate > 0);
  for (const effect of ["Invoke-RestMethod", "Invoke-WebRequest", "New-Item", "Copy-Item", "tar.exe", "& $executable"]) assert.ok(gate < source.indexOf(effect), effect);
});

test("native CI and candidates require repeated benchmarks and publish only numeric results", () => {
  for (const workflow of [ci, release]) {
    const job = workflow.jobs["pnport-native"] ?? workflow.jobs.build;
    assert.equal(job.env.PNPORT_SUFFIX, "${{ matrix.suffix }}");
    const benchmarkIndex = job.steps.findIndex(({ run }) => run?.includes("scripts/benchmark.mjs"));
    const installIndex = job.steps.findIndex(({ run }) => run?.includes("scripts/install-smoke.mjs"));
    assert(benchmarkIndex > installIndex && installIndex >= 0);
    const benchmark = job.steps[benchmarkIndex];
    assert(benchmark.run.includes('"$RUNNER_TEMP/pnport-typescript" "$RUNNER_TEMP/pnport-benchmark"'));
    assert(benchmark.run.includes('"packages/pnport/dist/$PNPORT_SUFFIX/bin/pnport"'));
    assert(!benchmark.continueOnError && !benchmark["continue-on-error"] && !benchmark.if);
    const upload = job.steps[benchmarkIndex + 1];
    assert.equal(upload.with.path, "${{ runner.temp }}/pnport-benchmark/benchmark.json");
    assert.equal(upload.with["if-no-files-found"], "error");
    assert(upload.with.name.startsWith("pnport-benchmark-") && upload.with.name.includes("github.run_attempt"));
    assert(!upload.with.name.startsWith("pnport-native-"));
  }
});
