import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { packArchive } from "./archive.mjs";
import { dependencyPaths, prepareDependency, readDependency } from "./prebuilt-dependencies.mjs";


const locked = JSON.parse(await readFile(new URL("../prebuilt-dependencies.lock.json", import.meta.url), "utf8")).dependencies["tauri-cli"];
const recipe = { id: "tauri-cli", source: locked.source, toolchain: locked.toolchain, verificationArgs: locked.verificationArgs, command: locked.sourceBuild.command, arguments: locked.sourceBuild.arguments, executable: "bin/cargo-tauri", targets: Object.entries(locked.assets).map(([target, asset]) => ({ target, platform: asset.platform, arch: asset.arch })) };
const hash = bytes => createHash("sha256").update(bytes).digest("hex");
const version = "tauri-cli 2.11.4";

async function fixture(t, options = {}) {
  const root = await mkdtemp(join(tmpdir(), "prebuilt-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const binary = Buffer.from("fixture binary");
  const archives = {};
  const assets = {};
  for (const target of recipe.targets) {
    const executable = recipe.executable + (target.platform === "win32" ? ".exe" : "");
    const bytes = packArchive([{ name: executable, data: binary, mode: 0o755 }, { name: "LICENSE", data: "original license" }]);
    archives[`${recipe.id}-${target.target}.tar.gz`] = bytes;
    assets[target.target] = { platform: target.platform, arch: target.arch, executable, name: `${recipe.id}-${target.target}.tar.gz`, sha256: hash(bytes), binarySha256: hash(binary), size: bytes.length };
  }
  const lock = { schemaVersion: 1, repository: "delinoio/prebuilt", dependencies: { "tauri-cli": {
    source: recipe.source, release: `tauri-cli-${recipe.source.revision}-r1`, recipeVersion: 1, toolchain: recipe.toolchain, producerRevision: "a".repeat(40), verificationArgs: recipe.verificationArgs, verificationOutput: version,
    sourceBuild: { command: recipe.command, arguments: recipe.arguments }, assets,
  } } };
  const calls = { fetched: 0, commands: [] };
  const settings = {
    platform: "darwin", arch: "arm64", lock, environment: {}, log: () => {},
    fetchAsset: async url => {
      calls.fetched += 1;
      const name = decodeURIComponent(new URL(url).pathname.split("/").at(-1));
      return new Response(archives[name], { status: 200 });
    },
    run: async (command, args) => {
      calls.commands.push({ command, args });
      if (command === "cargo") {
        const installRoot = args[args.indexOf("--root") + 1];
        const asset = Object.values(assets).find(item => item.platform === settings.platform && item.arch === settings.arch);
        const executable = join(installRoot, asset.executable);
        await mkdir(dirname(executable), { recursive: true });
        await writeFile(executable, binary, { mode: 0o755 });
        return "";
      }
      assert.deepEqual(args, ["tauri", "--version"]);
      return version;
    },
    ...options,
  };
  return { root, binary, lock, settings, calls, archives };
}

test("downloads once and verifies restored executable bytes before reuse", async t => {
  const f = await fixture(t);
  const first = await prepareDependency(f.root, "tauri-cli", f.settings);
  assert.equal(first.installed, true);
  assert.equal(f.calls.fetched, 1);
  assert.equal(await readFile(join(first.root, "LICENSE"), "utf8"), "original license");
  const second = await prepareDependency(f.root, "tauri-cli", f.settings);
  assert.equal(second.installed, false);
  assert.equal(f.calls.fetched, 1);
  await writeFile(first.executable, "corrupt restored binary");
  const third = await prepareDependency(f.root, "tauri-cli", f.settings);
  assert.equal(third.installed, true);
  assert.equal(f.calls.fetched, 2);
  assert.deepEqual(await readFile(third.executable), f.binary);
  assert.ok(f.calls.commands.every(call => call.command !== "cargo"));
});

test("selects every supported native host including Windows exe", async t => {
  const f = await fixture(t);
  for (const target of recipe.targets) {
    const installed = await prepareDependency(f.root, "tauri-cli", { ...f.settings, platform: target.platform, arch: target.arch });
    assert.equal(installed.target, target.target);
    assert.equal(installed.executable.endsWith(".exe"), target.platform === "win32");
  }
  assert.equal(f.calls.fetched, 6);
});

test("changed source and recipe version use separate cache identities", async t => {
  const f = await fixture(t);
  const first = await prepareDependency(f.root, "tauri-cli", f.settings);
  const lock = structuredClone(f.lock);
  lock.dependencies["tauri-cli"].source.revision = "b".repeat(40);
  lock.dependencies["tauri-cli"].release = `tauri-cli-${"b".repeat(40)}-r1`;
  const changed = await prepareDependency(f.root, "tauri-cli", { ...f.settings, lock });
  assert.notEqual(changed.root, first.root);
  assert.equal(f.calls.fetched, 2);
  assert.deepEqual(await readFile(first.executable), f.binary);
});

test("archive digest and executable digest mismatches fail before execution", async t => {
  const f = await fixture(t);
  for (const field of ["sha256", "binarySha256"]) {
    const lock = structuredClone(f.lock);
    lock.dependencies["tauri-cli"].assets["aarch64-apple-darwin"][field] = "f".repeat(64);
    await assert.rejects(prepareDependency(f.root, "tauri-cli", { ...f.settings, lock }), /SHA-256/);
  }
  assert.equal(f.calls.commands.length, 0);
});

test("download failures never invoke a source build", async t => {
  const f = await fixture(t, { fetchAsset: async () => new Response("missing", { status: 404 }) });
  await assert.rejects(prepareDependency(f.root, "tauri-cli", f.settings), /HTTP 404/);
  assert.equal(f.calls.commands.length, 0);
});

test("unsupported hosts and unpinned source fail before download", async t => {
  const f = await fixture(t);
  await assert.rejects(prepareDependency(f.root, "tauri-cli", { ...f.settings, platform: "linux", arch: "ia32" }), /Unsupported/);
  const lock = structuredClone(f.lock);
  lock.dependencies["tauri-cli"].source.revision = "main";
  assert.throws(() => readDependency(f.root, "tauri-cli", lock), /pin/);
  assert.equal(f.calls.fetched, 0);
});

test("failed verification cleans staging and retains the previous cache", async t => {
  const f = await fixture(t);
  const installed = await prepareDependency(f.root, "tauri-cli", f.settings);
  await writeFile(installed.marker, "invalid marker");
  await assert.rejects(prepareDependency(f.root, "tauri-cli", { ...f.settings, run: async () => "wrong version" }), /version/);
  assert.deepEqual(await readFile(installed.executable), f.binary);
  await assert.rejects(readFile(installed.root + ".lock"), { code: "ENOENT" });
});

test("concurrent preparation downloads only once", async t => {
  const f = await fixture(t);
  const results = await Promise.all([prepareDependency(f.root, "tauri-cli", f.settings), prepareDependency(f.root, "tauri-cli", f.settings)]);
  assert.equal(f.calls.fetched, 1);
  assert.deepEqual(results.map(result => result.installed).sort(), [false, true]);
});

test("an existing installation lock is retained on timeout", async t => {
  const f = await fixture(t);
  const paths = dependencyPaths(f.root, "tauri-cli", f.settings);
  await mkdir(paths.root + ".lock", { recursive: true });
  await assert.rejects(prepareDependency(f.root, "tauri-cli", { ...f.settings, lockTimeoutMs: 0 }), /lock is busy/);
  await writeFile(join(paths.root + ".lock", "owner"), "still present");
});

test("explicit source builds are rejected in CI", async t => {
  const f = await fixture(t);
  await assert.rejects(prepareDependency(f.root, "tauri-cli", { ...f.settings, source: true, environment: { CI: "true" } }), /forbidden in CI/);
  assert.equal(f.calls.commands.length, 0);
});

test("explicit local source builds never become downloaded cache entries", async t => {
  const f = await fixture(t);
  const original = { CI: process.env.CI, GITHUB_ACTIONS: process.env.GITHUB_ACTIONS };
  // Model a local process without relaxing the production CI guard. Node runs
  // these top-level cases sequentially and other test files have separate processes.
  delete process.env.CI;
  delete process.env.GITHUB_ACTIONS;
  try {
    const source = await prepareDependency(f.root, "tauri-cli", { ...f.settings, source: true });
    assert.equal(f.calls.fetched, 0);
    assert.ok(f.calls.commands[0].args.includes(recipe.source.revision));
    const downloaded = await prepareDependency(f.root, "tauri-cli", f.settings);
    assert.notEqual(source.root, downloaded.root);
    assert.equal(f.calls.fetched, 1);
  } finally {
    for (const [key, value] of Object.entries(original)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
});
