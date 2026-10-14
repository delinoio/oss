// Local test-only benchmark. Fresh children isolate resource observations.
import { spawn, execFile as execFileCallback } from "node:child_process";
import { createHash } from "node:crypto";
import { renameSync } from "node:fs";
import {
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { cpus, release } from "node:os";
import {
  basename,
  dirname,
  isAbsolute,
  join,
  relative,
  resolve,
  sep,
} from "node:path";
import { monitorEventLoopDelay, performance } from "node:perf_hooks";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import React from "react";
import manifest from "../package.json" with { type: "json" };
import platforms from "../src/native-platforms.json" with { type: "json" };
import { readNativeBuildMetadata } from "./native-build-provenance.mjs";
import { readJavaScriptBuildMetadata } from "./js-build-provenance.mjs";
import {
  SCENE_CASES,
  assertTopology,
  expectedTopology,
  fixtureHash,
  grid,
  inspectGlb,
} from "./benchmark-scenes-core.mjs";

const execFile = promisify(execFileCallback);
const file = fileURLToPath(import.meta.url);
const root = fileURLToPath(new URL("../../../", import.meta.url));
const round = (value) => Number(value.toFixed(3));
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
if (process.versions.node.split(".")[0] !== "24")
  throw new Error("Scene benchmarks require Node.js 24.");

export async function nativeBuildProvenance(sourceRevision, options = {}) {
  const host =
    options.host ??
    platforms.find(
      (item) =>
        item.platform === process.platform &&
        item.architecture === process.arch,
    );
  if (!host) throw new Error("Unsupported scene benchmark host.");
  const output = options.output ?? join(root, "packages/react-forge/dist");
  const metadata = await readNativeBuildMetadata(output, host);
  if (metadata.sourceRevision !== sourceRevision)
    throw new Error("React Forge native build does not match source revision.");
  const binding = await readFile(join(output, `react-forge.${host.id}.node`));
  const nativeBindingSha256 = sha256(binding);
  return { nativeBuildProfile: metadata.profile, nativeBindingSha256 };
}

async function javascriptBuildProvenance(sourceRevision) {
  return readJavaScriptBuildMetadata(
    join(root, "packages/react-forge/dist"),
    sourceRevision,
  );
}

function maxRssBytes() {
  // libuv normalizes ru_maxrss to KiB, including on Windows.
  return process.resourceUsage().maxRSS * 1024;
}

async function caseMain(format, workload, output) {
  if (
    !["glb", "fbx"].includes(format) ||
    !SCENE_CASES.some((item) => item.workload === workload)
  )
    throw new Error("Invalid scene benchmark case.");
  const { createSession } = await import("../dist/index.js");
  const { Scene, Mesh } = await import(`../dist/${format}.js`);
  const expected = expectedTopology(workload);
  const loop = monitorEventLoopDelay({ resolution: 5 });
  loop.enable();
  await new Promise((done) => setTimeout(done, 10));
  const utilization = performance.eventLoopUtilization();
  const started = performance.now();
  const session = createSession(format);
  try {
    const geometries = [grid(expected.cells)];
    if (workload === "overlay") geometries.push(grid(expected.cells, 0.001));
    const sourceSha256 = fixtureHash(geometries);
    const handles = [];
    for (const geometry of geometries)
      handles.push(await session.registerGeometry(geometry));
    const material = {};
    const nodes = Array.from({ length: expected.meshes }, (_, index) =>
      React.createElement(Mesh, {
        key: index,
        geometry: handles[workload === "overlay" ? index : 0],
        material,
        translation: workload === "shared" ? [2 * index, 0, 0] : [0, 0, 0],
      }),
    );
    await session.render(React.createElement(Scene, null, ...nodes));
    const prepared = performance.now();
    const publication = await session.exportFile(output);
    const exported = performance.now();
    const eventLoopUtilization = await sampleEventLoopUtilization(
      utilization,
      () => new Promise((done) => setTimeout(done, 10)),
    );
    const bytes = await readFile(output);
    return {
      format,
      workload,
      parameters: {
        cells: expected.cells,
        geometries: expected.geometries,
        meshes: expected.meshes,
        overlayZ: workload === "overlay" ? 0.001 : null,
        sharedTranslationX: workload === "shared" ? 2 : null,
      },
      uniqueVertexCount: expected.uniqueVertexCount,
      uniqueTriangleCount: expected.uniqueTriangleCount,
      instanceVertexCount: expected.instanceVertexCount,
      instanceTriangleCount: expected.instanceTriangleCount,
      registeredGeometryBytes: expected.registeredGeometryBytes,
      fixtureSha256: sourceSha256,
      outputBytes: bytes.length,
      outputSha256: sha256(bytes),
      revision: publication.revision,
      prepareMs: round(prepared - started),
      exportMs: round(exported - prepared),
      totalMs: round(exported - started),
      peakRssBytes: maxRssBytes(),
      eventLoopP99Ms: round(loop.percentile(99) / 1e6),
      eventLoopMaxMs: round(loop.max / 1e6),
      eventLoopUtilization,
    };
  } finally {
    loop.disable();
    await session.dispose();
  }
}

export async function sampleEventLoopUtilization(previous, afterSample) {
  const sample = round(performance.eventLoopUtilization(previous).utilization);
  await afterSample?.();
  return sample;
}

export function assertSample(sample, format, workload, bytes) {
  const expected = expectedTopology(workload);
  if (
    sample?.format !== format ||
    sample?.workload !== workload ||
    sample?.parameters?.cells !== expected.cells ||
    sample?.parameters?.geometries !== expected.geometries ||
    sample?.parameters?.meshes !== expected.meshes ||
    sample?.fixtureSha256 !==
      fixtureHash(
        workload === "overlay"
          ? [grid(expected.cells), grid(expected.cells, 0.001)]
          : [grid(expected.cells)],
      ) ||
    sample?.outputBytes !== bytes.length ||
    sample?.outputSha256 !== sha256(bytes)
  )
    throw new Error("Invalid scene benchmark sample.");
  for (const key of [
    "uniqueVertexCount",
    "uniqueTriangleCount",
    "instanceVertexCount",
    "instanceTriangleCount",
    "registeredGeometryBytes",
  ])
    if (sample[key] !== expected[key])
      throw new Error(`Invalid scene benchmark ${key}.`);
  for (const key of [
    "prepareMs",
    "exportMs",
    "totalMs",
    "peakRssBytes",
    "eventLoopP99Ms",
    "eventLoopMaxMs",
    "eventLoopUtilization",
  ])
    if (!Number.isFinite(sample[key]) || sample[key] < 0)
      throw new Error(`Invalid scene benchmark ${key}.`);
  if (sample.eventLoopUtilization > 1 || !Number.isSafeInteger(sample.revision))
    throw new Error(
      "Invalid scene benchmark resource or revision observation.",
    );
}

function runChild(script, args, signal) {
  return new Promise((resolveChild, reject) => {
    if (signal?.aborted) return reject(new Error("Scene benchmark cancelled."));
    // Node's CLI treats the script entry point as a filesystem path, including
    // on Windows where ESM loader arguments still require file URLs.
    const child = spawn(process.execPath, [script, ...args], {
      cwd: fileURLToPath(new URL("../", import.meta.url)),
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    let failed;
    const cancel = () => child.kill("SIGTERM");
    signal?.addEventListener("abort", cancel, { once: true });
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      if (stdout.length > 1024 * 1024) {
        failed = new Error("Scene benchmark child output exceeded its limit.");
        child.kill("SIGTERM");
      }
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
      if (stderr.length > 256 * 1024) stderr = stderr.slice(-256 * 1024);
    });
    child.once("error", (error) => {
      failed = error;
    });
    child.once("close", (code, termination) => {
      signal?.removeEventListener("abort", cancel);
      if (signal?.aborted)
        return reject(new Error("Scene benchmark cancelled."));
      if (failed) return reject(failed);
      if (code !== 0 || termination)
        return reject(
          new Error(
            `Scene benchmark child failed (${code ?? termination}): ${stderr.trim()}`,
          ),
        );
      resolveChild(stdout);
    });
  });
}

async function inspectFbx(paths, signal) {
  if (!paths.length) return new Map();
  let stdout;
  try {
    ({ stdout } = await execFile(
      "cargo",
      [
        "run",
        "--locked",
        "--quiet",
        "-p",
        "forge-fbx",
        "--example",
        "inspect_benchmark",
        "--",
        ...paths,
      ],
      { cwd: root, maxBuffer: 1024 * 1024, signal },
    ));
  } catch (error) {
    if (signal?.aborted) throw new Error("Scene benchmark cancelled.");
    throw error;
  }
  const rows = JSON.parse(stdout);
  if (!Array.isArray(rows) || rows.length !== paths.length)
    throw new Error("Incomplete independent FBX inspection.");
  return new Map(rows.map((row) => [row.file, row]));
}

export function parseLockedUfbxVersion(lockfile) {
  const match = lockfile.match(
    /(?:^|\r?\n)name = "ufbx"\r?\nversion = "([^"]+)"/u,
  );
  if (!match) throw new Error("Unable to determine the locked ufbx version.");
  return match[1];
}

async function lockedUfbxVersion() {
  return parseLockedUfbxVersion(
    await readFile(join(root, "Cargo.lock"), "utf8"),
  );
}

export async function sourceStatus(destination) {
  const relativeDestination = relative(root, destination);
  const insideRepository =
    relativeDestination &&
    !isAbsolute(relativeDestination) &&
    relativeDestination !== ".." &&
    !relativeDestination.startsWith(`..${sep}`);
  const args = ["status", "--porcelain=v1", "--untracked-files=all", "--", "."];
  if (insideRepository)
    args.push(`:(exclude)${relativeDestination.split(sep).join("/")}`);
  const { stdout } = await execFile("git", args, { cwd: root });
  return stdout;
}

export async function runBenchmark(output, options = {}) {
  if (!output || typeof output !== "string")
    throw new Error("Expected --output <report.json>.");
  const destination = resolve(output);
  const { stdout: sourceRevision } = await execFile(
    "git",
    ["rev-parse", "HEAD"],
    { cwd: root },
  );
  const sourceStatusOutput = await sourceStatus(destination);
  const cases = options.cases ?? SCENE_CASES;
  const signal = options.signal;
  const ufbxVersion = await lockedUfbxVersion();
  await mkdir(dirname(destination), { recursive: true });
  const owned = await mkdtemp(
    join(dirname(destination), ".react-forge-scene-benchmark-"),
  );
  const staged = join(dirname(destination), `.${basename(owned)}-report.json`);
  let ownedRemoved = false;
  let published = false;
  try {
    const pending = [];
    for (const { format, workload } of cases) {
      const artifact = join(owned, `${format}-${workload}.${format}`);
      const stdout = await runChild(
        options.caseFile ?? file,
        ["--case", format, workload, artifact],
        signal,
      );
      let sample;
      try {
        sample = JSON.parse(stdout);
      } catch {
        throw new Error("Malformed scene benchmark child JSON.");
      }
      const bytes = await readFile(artifact);
      assertSample(sample, format, workload, bytes);
      pending.push({ sample, artifact, bytes, format, workload });
      process.stdout.write(
        `${format} ${workload}: ${sample.totalMs} ms, ${Math.round(sample.peakRssBytes / 1048576)} MiB peak RSS\n`,
      );
    }
    let independentFbx;
    if (options.verifyArtifact) independentFbx = new Map();
    else
      independentFbx = await inspectFbx(
        pending
          .filter((item) => item.format === "fbx")
          .map((item) => item.artifact),
        signal,
      );
    for (const item of pending) {
      const expected = expectedTopology(item.workload);
      let actual;
      if (options.verifyArtifact) actual = await options.verifyArtifact(item);
      else if (item.format === "glb") {
        const { default: validator } = await import("gltf-validator");
        const validation = await validator.validateBytes(item.bytes);
        if (validation.issues.numErrors)
          throw new Error("Khronos GLB validation failed.");
        actual = inspectGlb(item.bytes);
      } else {
        const inspected = independentFbx.get(item.artifact);
        if (inspected) {
          const { file: _temporaryFile, ...counts } = inspected;
          actual = counts;
        }
      }
      if (!actual) throw new Error("Missing independent scene inspection.");
      assertTopology(actual, expected);
      item.sample.independentTopology = actual;
      item.sample.validator =
        item.format === "glb"
          ? `Khronos glTF Validator ${manifest.devDependencies["gltf-validator"]} and GLB accessor parser`
          : `ufbx ${ufbxVersion}`;
    }
    if (signal?.aborted) throw new Error("Scene benchmark cancelled.");
    await (options.javascriptProvenance ??
      javascriptBuildProvenance(sourceRevision.trim()));
    const native =
      options.nativeProvenance ??
      (await nativeBuildProvenance(sourceRevision.trim()));
    const report = {
      schemaVersion: 1,
      command:
        "pnpm --filter @delino/react-forge benchmark:scenes --output <report.json>",
      measuredAt: new Date().toISOString(),
      sourceRevision: sourceRevision.trim(),
      sourceDirty: Boolean(sourceStatusOutput.trim()),
      node: process.version,
      react: React.version,
      packageVersion: manifest.version,
      ...native,
      host: {
        platform: process.platform,
        architecture: process.arch,
        osRelease: release(),
        cpu: cpus()[0]?.model,
        logicalCpus: cpus().length,
      },
      method:
        "One fresh Node 24 process per fixed case; libuv maxRSS KiB normalized to bytes on every host. Prepare/export/total use monotonic wall time. Event-loop delay uses a 5 ms monitor; observations include OS cache effects and are not GPU or numeric SLO evidence.",
      samples: pending.map(({ sample }) => sample),
    };
    await writeFile(
      join(owned, "report.json"),
      `${JSON.stringify(report, null, 2)}\n`,
    );
    if (signal?.aborted) throw new Error("Scene benchmark cancelled.");
    await rename(join(owned, "report.json"), staged);
    await rm(owned, { recursive: true, force: true });
    ownedRemoved = true;
    if (signal?.aborted) throw new Error("Scene benchmark cancelled.");
    renameSync(staged, destination);
    published = true;
    return report;
  } finally {
    if (!ownedRemoved)
      await rm(owned, { recursive: true, force: true }).catch(() => {});
    if (!published) await rm(staged, { force: true }).catch(() => {});
  }
}

if (process.argv[1] && resolve(process.argv[1]) === file) {
  if (process.argv[2] === "--case") {
    if (process.argv.length !== 6)
      throw new Error("Invalid scene benchmark case arguments.");
    const sample = await caseMain(
      process.argv[3],
      process.argv[4],
      process.argv[5],
    );
    process.stdout.write(`${JSON.stringify(sample)}\n`);
  } else {
    if (
      process.argv.length !== 4 ||
      process.argv[2] !== "--output" ||
      !process.argv[3] ||
      process.argv[3].startsWith("--")
    )
      throw new Error("Usage: benchmark-scenes.mjs --output <report.json>");
    const controller = new AbortController();
    const cancel = () => controller.abort();
    process.on("SIGINT", cancel);
    process.on("SIGTERM", cancel);
    try {
      await runBenchmark(process.argv[3], { signal: controller.signal });
    } finally {
      process.off("SIGINT", cancel);
      process.off("SIGTERM", cancel);
    }
  }
}
