import { spawn } from "node:child_process";
import { constants, readFileSync } from "node:fs";
import { access, lstat, mkdir, readFile, realpath, rename, rm, writeFile } from "node:fs/promises";
import { createHash, randomUUID } from "node:crypto";
import { dirname, join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { unpackArchive } from "./archive.mjs";

const hash = bytes => createHash("sha256").update(bytes).digest("hex");
const identifier = /^[a-z][a-z0-9-]*$/;
const digest = /^[a-f0-9]{64}$/;
const revision = /^[a-f0-9]{40}$/;

export function readDependency(sourceRoot, id, lock = JSON.parse(readFileSync(join(sourceRoot, "prebuilt-dependencies.lock.json"), "utf8"))) {
  if (!identifier.test(id ?? "") || lock.schemaVersion !== 1 || !/^[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_.-]+$/.test(lock.repository)) throw new Error("Invalid prebuilt dependency lock identity.");
  const dependency = lock.dependencies?.[id];
  if (!dependency || !revision.test(dependency.source?.revision) || !/^https:\/\/github\.com\/[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_.-]+$/.test(dependency.source?.repository) || !Number.isSafeInteger(dependency.recipeVersion) || dependency.recipeVersion < 1 || dependency.release !== `${id}-${dependency.source.revision}-r${dependency.recipeVersion}` || !revision.test(dependency.producerRevision)) throw new Error("Prebuilt dependency must pin its source and release.");
  if (!/^nightly-\d{4}-\d{2}-\d{2}$/.test(dependency.toolchain) || !Array.isArray(dependency.verificationArgs) || !dependency.verificationArgs.length || dependency.verificationArgs.some(value => typeof value !== "string" || value.includes("\0")) || typeof dependency.verificationOutput !== "string" || !dependency.verificationOutput || /[\r\n\0]/.test(dependency.verificationOutput)) throw new Error("Invalid dependency verification contract.");
  const hosts = new Set();
  for (const [target, asset] of Object.entries(dependency.assets ?? {})) {
    const host = `${asset.platform}-${asset.arch}`;
    if (!/^[a-z0-9_-]+$/.test(target) || !["darwin", "win32", "linux"].includes(asset.platform) || !["x64", "arm64"].includes(asset.arch) || hosts.has(host) || asset.name !== `${id}-${target}.tar.gz` || !digest.test(asset.sha256) || !digest.test(asset.binarySha256) || !/^bin\/[a-zA-Z0-9_-]+(?:\.exe)?$/.test(asset.executable) || !Number.isSafeInteger(asset.size) || asset.size < 1 || asset.size > 256 * 1024 * 1024) throw new Error("Invalid prebuilt dependency asset.");
    if ((asset.platform === "win32") !== asset.executable.endsWith(".exe")) throw new Error("Executable extension does not match the host.");
    hosts.add(host);
  }
  if (!hosts.size) throw new Error("Prebuilt dependency has no assets.");
  return { repository: lock.repository, ...dependency };
}

export function dependencyPaths(sourceRoot, id, { platform = process.platform, arch = process.arch, source = false, lock } = {}) {
  const dependency = readDependency(sourceRoot, id, lock);
  const selected = Object.entries(dependency.assets).find(([, asset]) => asset.platform === platform && asset.arch === arch);
  if (!selected) throw new Error(`Unsupported prebuilt host: ${platform}/${arch}.`);
  const [target, asset] = selected;
  const root = join(resolve(sourceRoot), ".cache/prebuilt", id, dependency.release, target + (source ? "-source" : ""));
  return { root, executable: join(root, asset.executable), marker: join(root, "installation.json"), target, asset, dependency };
}

export function runDependencyCommand(command, args, { capture = false, ...options } = {}) {
  return new Promise((resolveCommand, reject) => {
    const child = spawn(command, args, { stdio: capture ? ["ignore", "pipe", "inherit"] : "inherit", shell: false, ...options });
    let output = "";
    let exceeded = false;
    if (capture) child.stdout.on("data", bytes => {
      output += bytes;
      if (output.length > 16 * 1024) { exceeded = true; child.kill(); }
    });
    const forward = signal => child.kill(signal);
    const interrupt = () => forward("SIGINT");
    const terminate = () => forward("SIGTERM");
    process.on("SIGINT", interrupt);
    process.on("SIGTERM", terminate);
    const cleanup = () => { process.off("SIGINT", interrupt); process.off("SIGTERM", terminate); };
    child.once("error", error => { cleanup(); reject(error); });
    child.once("exit", (code, signal) => {
      cleanup();
      if (code === 0 && !exceeded) resolveCommand(output.trim());
      else reject(new Error(`Dependency command failed (${signal ?? code}).`));
    });
  });
}

async function verifyExecutable(executable, paths, run, environment, expectedDigest) {
  if (!(await lstat(executable)).isFile()) throw new Error("Dependency executable is not a regular file.");
  await access(executable, constants.X_OK);
  if (hash(await readFile(executable)) !== expectedDigest) throw new Error("Dependency executable SHA-256 mismatch.");
  const output = await run(executable, paths.dependency.verificationArgs, { cwd: dirname(executable), env: environment, capture: true });
  if (output !== paths.dependency.verificationOutput) throw new Error("Dependency executable version mismatch.");
}

async function download(paths, fetchAsset) {
  const url = `https://github.com/${paths.dependency.repository}/releases/download/${encodeURIComponent(paths.dependency.release)}/${encodeURIComponent(paths.asset.name)}`;
  const response = await fetchAsset(url, { signal: AbortSignal.timeout(120_000) });
  if (!response.ok) throw new Error(`Prebuilt dependency download failed (HTTP ${response.status}).`);
  const declared = response.headers.get("content-length");
  if (declared !== null && Number(declared) !== paths.asset.size) throw new Error("Prebuilt dependency download size mismatch.");
  const chunks = [];
  let length = 0;
  if (!response.body) throw new Error("Prebuilt dependency download has no body.");
  for await (const chunk of response.body) {
    length += chunk.length;
    if (length > paths.asset.size) throw new Error("Prebuilt dependency download exceeds its pinned size.");
    chunks.push(Buffer.from(chunk));
  }
  const bytes = Buffer.concat(chunks);
  if (length !== paths.asset.size || hash(bytes) !== paths.asset.sha256) throw new Error("Prebuilt dependency archive SHA-256 or size mismatch.");
  return bytes;
}

export async function prepareDependency(sourceRoot, id, {
  platform = process.platform,
  arch = process.arch,
  source = false,
  lock,
  environment = process.env,
  run = runDependencyCommand,
  fetchAsset = globalThis.fetch,
  log = message => process.stderr.write(`${message}\n`),
  lockTimeoutMs = 120_000,
} = {}) {
  const isCi = value => Boolean(value && value !== "false");
  if (source && (isCi(environment.CI) || isCi(process.env.CI) || isCi(environment.GITHUB_ACTIONS) || isCi(process.env.GITHUB_ACTIONS))) throw new Error("Source builds are forbidden in CI. Publish the prebuilt release first.");
  const paths = dependencyPaths(sourceRoot, id, { platform, arch, source, lock });
  const report = status => log(JSON.stringify({ operation: "prebuilt_dependency", dependency: id, target: paths.target, release: paths.dependency.release, status }));
  await mkdir(dirname(paths.root), { recursive: true });
  const lockPath = paths.root + ".lock";
  const deadline = Date.now() + lockTimeoutMs;
  while (true) {
    try { await mkdir(lockPath); break; }
    catch (error) {
      if (error.code !== "EEXIST") throw error;
      if (Date.now() >= deadline) throw new Error("Dependency installation lock is busy. Inspect interrupted preparation before removing its lock.");
      await delay(50);
    }
  }
  let staging;
  let backup;
  try {
    try {
      const marker = JSON.parse(await readFile(paths.marker, "utf8"));
      const expectedDigest = source ? marker.binarySha256 : paths.asset.binarySha256;
      if (marker.schemaVersion !== 1 || marker.source !== source || marker.release !== paths.dependency.release || marker.target !== paths.target || !digest.test(expectedDigest) || marker.binarySha256 !== expectedDigest) throw new Error("Dependency cache marker mismatch.");
      await verifyExecutable(paths.executable, paths, run, environment, expectedDigest);
      report("reused");
      return { ...paths, installed: false };
    } catch { /* Missing, corrupt or unusable caches are verified download misses. */ }
    staging = paths.root + "." + randomUUID() + ".tmp";
    await mkdir(staging);
    const executable = join(staging, paths.asset.executable);
    if (source) {
      report("building_source");
      const build = paths.dependency.sourceBuild;
      if (!/^[a-zA-Z0-9_-]+$/.test(build?.command) || !Array.isArray(build.arguments)) throw new Error("Dependency has no explicit local build recipe.");
      const values = { ...paths.dependency.source, toolchain: paths.dependency.toolchain, target: paths.target, root: staging };
      const args = build.arguments.map(argument => {
        if (typeof argument !== "string" || argument.includes("\0")) throw new Error("Invalid local build argument.");
        return argument.replace(/\{([a-zA-Z]+)\}/g, (_, field) => {
          if (!Object.hasOwn(values, field)) throw new Error("Unknown local build argument placeholder.");
          return values[field];
        });
      });
      await run(build.command, args, { cwd: resolve(sourceRoot), env: environment });
    } else {
      report("downloading");
      const entries = unpackArchive(await download(paths, fetchAsset));
      if (!entries.some(entry => entry.name === paths.asset.executable)) throw new Error("Dependency archive is missing its executable.");
      if (entries.some(entry => entry.name === "installation.json")) throw new Error("Dependency archive contains a reserved installation marker.");
      for (const entry of entries) {
        const destination = join(staging, entry.name);
        await mkdir(dirname(destination), { recursive: true });
        await writeFile(destination, entry.data, { mode: entry.mode, flag: "wx" });
      }
    }
    const binarySha256 = source ? hash(await readFile(executable)) : paths.asset.binarySha256;
    await verifyExecutable(executable, paths, run, environment, binarySha256);
    await writeFile(join(staging, "installation.json"), JSON.stringify({ schemaVersion: 1, source, release: paths.dependency.release, target: paths.target, binarySha256 }) + "\n", { flag: "wx" });
    try {
      const info = await lstat(paths.root);
      if (!info.isDirectory()) throw new Error("Dependency cache is not a regular directory.");
      backup = paths.root + "." + randomUUID() + ".old";
      await rename(paths.root, backup);
    } catch (error) { if (error.code !== "ENOENT") throw error; }
    try { await rename(staging, paths.root); staging = undefined; }
    catch (error) {
      if (backup) { await rename(backup, paths.root); backup = undefined; }
      throw error;
    }
    report(source ? "source_installed" : "installed");
    return { ...paths, installed: true };
  } finally {
    if (staging) await rm(staging, { recursive: true, force: true });
    if (backup) await rm(backup, { recursive: true, force: true });
    await rm(lockPath, { recursive: true });
  }
}

async function main(args) {
  const [id, ...flags] = args;
  if (flags.some(flag => !["--source", "--print-path"].includes(flag)) || new Set(flags).size !== flags.length) throw new Error("Expected a dependency identifier, optional --source and --print-path.");
  const root = fileURLToPath(new URL("..", import.meta.url));
  const result = await prepareDependency(root, id, { source: flags.includes("--source") });
  if (flags.includes("--print-path")) process.stdout.write(result.executable + "\n");
}

// Node canonicalizes module URLs. Workspace symlinks must still run preparation.
if (process.argv[1] && (await realpath(process.argv[1]).catch(() => undefined)) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch(error => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
}
