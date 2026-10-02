// SPDX-License-Identifier: Apache-2.0
// Offline measurements use already prepared, synthetic conformance projects.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawn, spawnSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { cpus, release, totalmem } from "node:os";
import { delimiter, dirname, join, resolve } from "node:path";
import { performance } from "node:perf_hooks";
import { fileURLToPath, pathToFileURL } from "node:url";

const repository = fileURLToPath(new URL("../../../", import.meta.url));
const samplingIntervalMs = 100;
const compiler = "7.1.0-dev.20260812.1";
const yarn = "4.18.0";
// The tsc launcher uses /usr/bin/env node. Keep it on the same explicit Node
// runtime as the harness, matching conformance, regardless of ambient PATH.
const environment = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}` };
delete environment.NODE_OPTIONS;

export function summarize(values) {
  assert(values.length >= 5 && values.every((value) => Number.isFinite(value) && value >= 0));
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return { median: sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2, min: sorted[0], max: sorted.at(-1) };
}

export function processTreeRss(table, root) {
  const rows = table.trim().split("\n").map((line) => line.trim().split(/\s+/u).map(Number));
  assert(rows.every((row) => row.length === 3 && row.every(Number.isSafeInteger) && row.every((value) => value >= 0)), "Invalid ps measurement.");
  const owned = new Set([root]);
  for (let changed = true; changed;) {
    changed = false;
    for (const [pid, parent] of rows) if (!owned.has(pid) && owned.has(parent)) { owned.add(pid); changed = true; }
  }
  return rows.reduce((sum, [pid, , rss]) => sum + (owned.has(pid) ? rss * 1024 : 0), 0);
}

export function diskUsage(directory) {
  let logicalBytes = 0, allocatedBytes = 0, files = 0;
  const seen = new Set();
  function visit(path) {
    const metadata = lstatSync(path);
    const key = `${metadata.dev}:${metadata.ino}`;
    if (seen.has(key)) return;
    seen.add(key);
    allocatedBytes += metadata.blocks * 512;
    if (metadata.isDirectory()) for (const name of readdirSync(path)) visit(join(path, name));
    else { logicalBytes += metadata.size; files++; }
  }
  if (existsSync(directory)) visit(directory);
  return { logicalBytes, allocatedBytes, files };
}

function digest(path) { return createHash("sha256").update(readFileSync(path)).digest("hex"); }
function execute(program, args, cwd) {
  const result = spawnSync(program, args, { cwd, env: environment, encoding: "utf8", timeout: 120_000, maxBuffer: 4 * 1024 * 1024 });
  assert.ifError(result.error);
  assert.equal(result.status, 0, `${program} failed with status ${result.status}; benchmark is incomplete.`);
  return result.stdout.trim();
}

async function measure(program, args, cwd) {
  let peak = 0, observations = 0, measurementFailed = false, polling;
  const start = performance.now();
  const child = spawn(program, args, { cwd, env: environment, stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "", stderr = "";
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (bytes) => { if (stdout.length < 65536) stdout += bytes; });
  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (bytes) => { if (stderr.length < 65536) stderr += bytes; });
  let failure;
  child.on("error", (error) => { failure = error; });
  const completed = new Promise((accept) => child.once("close", (status, signal) => accept({ status, signal })));
  async function sample() {
    if (polling || !child.pid) return;
    polling = new Promise((accept) => {
      const ps = spawn("/bin/ps", ["-axo", "pid=,ppid=,rss="], { stdio: ["ignore", "pipe", "ignore"] });
      let table = "";
      ps.stdout.setEncoding("utf8");
      ps.stdout.on("data", (bytes) => { table += bytes; });
      ps.on("error", () => { measurementFailed = true; });
      ps.once("close", (status) => {
        try {
          if (status !== 0) throw new Error("ps failed");
          const rss = processTreeRss(table, child.pid);
          if (rss > 0) { observations++; peak = Math.max(peak, rss); }
        } catch { measurementFailed = true; }
        accept();
      });
    });
    await polling;
    polling = undefined;
  }
  const timer = setInterval(() => { void sample(); }, samplingIntervalMs);
  void sample();
  let escalation;
  let timedOut = false;
  let interrupted = false;
  const cancel = () => {
    interrupted = true;
    child.kill("SIGTERM");
    escalation ??= setTimeout(() => child.kill("SIGKILL"), 7000);
  };
  const deadline = setTimeout(() => { timedOut = true; cancel(); }, 120_000);
  process.once("SIGINT", cancel);
  process.once("SIGTERM", cancel);
  const outcome = await completed;
  const wallMs = performance.now() - start;
  clearInterval(timer);
  clearTimeout(deadline);
  clearTimeout(escalation);
  process.removeListener("SIGINT", cancel);
  process.removeListener("SIGTERM", cancel);
  await polling;
  assert.ifError(failure);
  const diagnosticCodes = [...new Set(`${stdout}\n${stderr}`.match(/\b(?:TS\d{4}|PNPORT_[A-Z_]+)\b/gu) ?? [])];
  assert(!interrupted && !timedOut && outcome.status === 0 && !outcome.signal,
    `Benchmark child failed (${JSON.stringify({ ...outcome, diagnosticCodes })}); no complete result is recorded.`);
  assert(!measurementFailed && observations > 0, "Process-tree RSS could not be sampled; no complete result is recorded.");
  return { wallMs, sampledPeakProcessTreeRssBytes: peak, memoryObservations: observations, stdout };
}

async function main(args) {
  const [suppliedFixture, suppliedOutput, suppliedBinary, nativeSourceRevision, suppliedSamples = "5", suppliedIterations = "1000"] = args;
  assert(suppliedFixture && suppliedOutput && suppliedBinary && args.length <= 6 && /^[a-f0-9]{40}$/u.test(nativeSourceRevision ?? ""),
    "Usage: benchmark.mjs <prepared-typescript-fixture> <new-output-directory> <native-pnport> <native-source-revision> [samples>=5] [filesystem-iterations]");
  assert(["darwin", "linux"].includes(process.platform), "Benchmarks require native macOS or glibc Linux.");
  const samples = Number(suppliedSamples), iterations = Number(suppliedIterations);
  assert(Number.isSafeInteger(samples) && samples >= 5 && samples <= 100);
  assert(Number.isSafeInteger(iterations) && iterations >= 1 && iterations <= 100000);
  const fixture = realpathSync(resolve(suppliedFixture)), binary = realpathSync(resolve(suppliedBinary));
  const output = resolve(suppliedOutput);
  assert.deepEqual(JSON.parse(readFileSync(join(fixture, "prepared.json"), "utf8")), { yarn, compiler, platform: process.platform, arch: process.arch });
  assert(!existsSync(output), "Output must be a new directory; existing data is never replaced.");
  const sourceRevision = execute("git", ["rev-parse", "HEAD"], repository);
  assert.equal(execute("git", ["cat-file", "-t", nativeSourceRevision], repository), "commit");
  assert(!execute("git", ["status", "--porcelain", "--untracked-files=normal"], repository), "Commit benchmark sources before collecting revision-bound evidence.");
  const image = readFileSync(binary).subarray(0, 4).toString("hex");
  assert(["7f454c46", "cffaedfe", "feedfacf"].includes(image), "Supply the native pnport executable rather than an installer/npm wrapper.");
  const nativeDigest = digest(binary);
  const companion = join(dirname(binary), process.platform === "darwin" ? "libpnport_preload.dylib" : "libpnport_preload.so");
  const companionDigest = digest(companion);
  mkdirSync(output, { mode: 0o700, recursive: true });
  const filesystemBinary = join(output, "filesystem-benchmark");
  execute("cc", ["-O2", "-Wall", "-Wextra", "-Werror", fileURLToPath(new URL("../test/fixtures/benchmark.c", import.meta.url)), "-o", filesystemBinary], output);
  if (process.platform === "darwin") execute("/usr/bin/codesign", ["--force", "--sign", "-", filesystemBinary], output);
  const pnportVersion = execute(binary, ["--version"], output);
  const measurements = [], fixtureDigests = [];
  for (const format of ["inline", "split"]) {
    const root = join(fixture, format), project = join(root, "packages/app");
    assert(!existsSync(join(root, "node_modules")));
    assert(existsSync(join(root, "packages/core/lib/index.d.ts")), "Run TypeScript conformance first to prepare reference declarations.");
    const platform = `${process.platform}-${process.arch}`;
    const nativePackage = readdirSync(join(root, ".yarn/unplugged")).find((name) => name.startsWith(`@typescript-typescript-${platform}-`));
    assert(nativePackage, "Missing prepared official native compiler.");
    const native = join(root, ".yarn/unplugged", nativePackage, "node_modules/@typescript", `typescript-${platform}`, "lib/tsc");
    fixtureDigests.push({ format, lockSha256: digest(join(root, "yarn.lock")), manifestSha256: digest(join(root, ".pnp.cjs")),
      splitDataSha256: format === "split" ? digest(join(root, ".pnp.data.json")) : null, nativeCompilerSha256: digest(native),
      sourceSha256: digest(join(project, "src/index.ts")), referenceDeclarationSha256: digest(join(root, "packages/core/lib/index.d.ts")) });
    for (const workload of ["filesystem", "typescript"]) {
      let expectedCalls;
      for (let repeat = 0; repeat < samples; repeat++) {
        const cache = join(output, `${format}-${workload}-${repeat}-cache`);
        const buildInfo = join(output, `${format}-${workload}-${repeat}.tsbuildinfo`);
        for (const condition of ["cold", "warm"]) {
          // Compiler incremental state is reset in BOTH conditions; only the
          // pnport cache differs. An incremental no-op is not a warm-cache run.
          rmSync(buildInfo, { force: true });
          const cacheBefore = diskUsage(cache);
          if (condition === "cold") assert.deepEqual(cacheBefore, { logicalBytes: 0, allocatedBytes: 0, files: 0 });
          else assert(cacheBefore.logicalBytes > 0, "Warm runs must reuse the completed cold cache.");
          const command = workload === "filesystem"
            ? [filesystemBinary, "node_modules/@types/node/package.json", String(iterations)]
            : ["tsc", "--noEmit", "-p", "packages/app", "--tsBuildInfoFile", buildInfo];
          const cwd = workload === "filesystem" ? project : root;
          const result = await measure(binary, ["--project", root, "--cache-dir", cache, "run", "--", ...command], cwd);
          const { stdout, ...metrics } = result;
          let fixtureCalls = null;
          if (workload === "filesystem") {
            fixtureCalls = JSON.parse(stdout);
            assert.equal(fixtureCalls.iterations, iterations);
            assert(fixtureCalls.readBytes > 0 && fixtureCalls.directoryEntries > 0);
            expectedCalls ??= fixtureCalls;
            assert.deepEqual(fixtureCalls, expectedCalls, "Filesystem workload changed between samples.");
          } else assert.equal(stdout, "", "TypeScript workload must complete without diagnostics.");
          const cacheAfter = diskUsage(cache);
          assert(cacheAfter.logicalBytes > 0, "The workload must materialize dependency backing.");
          const measurement = { format, workload, repeat, condition, ...metrics, cacheBefore, cacheAfter, fixtureCalls };
          measurements.push(measurement);
          console.log(JSON.stringify({ event: "pnport_benchmark_sample", ...measurement }));
        }
      }
    }
  }
  const summary = [];
  for (const format of ["inline", "split"]) for (const workload of ["filesystem", "typescript"]) for (const condition of ["cold", "warm"]) {
    const selected = measurements.filter((sample) => sample.format === format && sample.workload === workload && sample.condition === condition);
    summary.push({ format, workload, condition, wallMs: summarize(selected.map((sample) => sample.wallMs)),
      sampledPeakProcessTreeRssBytes: summarize(selected.map((sample) => sample.sampledPeakProcessTreeRssBytes)),
      cacheLogicalBytes: summarize(selected.map((sample) => sample.cacheAfter.logicalBytes)), cacheAllocatedBytes: summarize(selected.map((sample) => sample.cacheAfter.allocatedBytes)) });
  }
  const archiveDigests = readdirSync(join(fixture, "external-cache")).filter((name) => name.endsWith(".zip")).sort()
    .map((name) => ({ name, sha256: digest(join(fixture, "external-cache", name)) }));
  assert(archiveDigests.length > 0);
  assert.equal(digest(binary), nativeDigest, "The measured native executable changed.");
  assert.equal(digest(companion), companionDigest, "The measured companion changed.");
  for (const inputs of fixtureDigests) {
    const root = join(fixture, inputs.format), platform = `${process.platform}-${process.arch}`;
    const nativePackage = readdirSync(join(root, ".yarn/unplugged")).find((name) => name.startsWith(`@typescript-typescript-${platform}-`));
    assert.equal(digest(join(root, "yarn.lock")), inputs.lockSha256);
    assert.equal(digest(join(root, ".pnp.cjs")), inputs.manifestSha256);
    if (inputs.format === "split") assert.equal(digest(join(root, ".pnp.data.json")), inputs.splitDataSha256);
    assert.equal(digest(join(root, "packages/app/src/index.ts")), inputs.sourceSha256);
    assert.equal(digest(join(root, "packages/core/lib/index.d.ts")), inputs.referenceDeclarationSha256);
    assert.equal(digest(join(root, ".yarn/unplugged", nativePackage, "node_modules/@typescript", `typescript-${platform}`, "lib/tsc")), inputs.nativeCompilerSha256);
  }
  const evidence = { event: "pnport_benchmark", schemaVersion: 1, complete: true, sourceRevision, declaredNativeSourceRevision: nativeSourceRevision, pnportVersion, pnportNativeSha256: nativeDigest, preloadSha256: companionDigest,
    filesystemFixtureSourceSha256: digest(fileURLToPath(new URL("../test/fixtures/benchmark.c", import.meta.url))), filesystemFixtureBinarySha256: digest(filesystemBinary),
    host: { platform: process.platform, arch: process.arch, kernel: release(), cpu: cpus()[0]?.model, logicalCpuCount: cpus().length, totalMemoryBytes: totalmem(),
      osVersion: process.platform === "darwin" ? execute("/usr/bin/sw_vers", ["-productVersion"], output) : readFileSync("/etc/os-release", "utf8").match(/^PRETTY_NAME="?([^"\n]+)"?/mu)?.[1] },
    tools: { node: process.version, cCompiler: execute("cc", ["--version"], output).split("\n")[0], yarn, compiler, samplingIntervalMs, memory: "sum of observed descendant ps RSS; sampled peak, shared pages may be counted more than once", disk: "lstat bytes and 512-byte st_blocks; unique inodes, no followed symlinks", filesystem: "instrumented fixture calls and bytes, not kernel syscall/device I/O counts", cache: "fresh cache per cold/warm pair; both runs perform the same workload with fresh compiler build info" },
    samples, iterations, fixtureDigests, archiveDigests, measurements, summary };
  writeFileSync(join(output, "benchmark.json"), `${JSON.stringify(evidence, null, 2)}\n`, { flag: "wx", mode: 0o600 });
  console.log(JSON.stringify({ event: "pnport_benchmark_complete", sourceRevision, summary }));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => { console.error(error.message); process.exitCode = 1; });
}
