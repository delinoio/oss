import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { realpathSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";
import { companion, launcherManifest, nativeManifest } from "../scripts/manifests.mjs";
const require = createRequire(import.meta.url);
const { targets, selectTarget } = require("../src/platforms.cjs");
const { resolveBinary, launch, Failure } = require("../src/launcher.cjs");
const revision = "a".repeat(40);

test("manifests pin six exact native packages and request Yarn unplugging", () => {
  const manifest = launcherManifest("0.1.0", revision);
  assert.equal(Object.keys(manifest.optionalDependencies).length, 6);
  assert.equal(manifest.scripts, undefined);
  for (const target of targets) {
    const native = nativeManifest(target.suffix, "0.1.0", revision);
    assert.equal(native.preferUnplugged, true);
    assert.equal(native.scripts, undefined);
    assert.equal(manifest.optionalDependencies[native.name], native.version);
    assert.deepEqual(native.os, [target.os]);
    assert.deepEqual(native.cpu, [target.cpu]);
    assert.equal(selectTarget(target.os, target.cpu, target.libc), target);
  }
  assert.equal(selectTarget("linux", "x64", "musl"), undefined);
  assert.equal(selectTarget("darwin", "ia32"), undefined);
  assert.throws(() => nativeManifest("linux-x64-musl", "0.1.0", revision));
});

test("resolve only the exact installed native package and require its companion", () => {
  const root = mkdtempSync(path.join(tmpdir(), "pnport-launcher-"));
  try {
    const target = targets[0];
    const manifest = path.join(root, "package.json");
    writeFileSync(manifest, JSON.stringify(launcherManifest("0.1.0", revision)));
    assert.throws(() => resolveBinary(manifest, target), error => error.code === Failure.Missing);
    const nativeRoot = path.join(root, "node_modules", target.name);
    mkdirSync(path.join(nativeRoot, "bin"), { recursive: true });
    writeFileSync(path.join(nativeRoot, "package.json"), JSON.stringify(nativeManifest(target.suffix, "0.1.0", revision)));
    writeFileSync(path.join(nativeRoot, "bin", target.binary), "fixture only");
    assert.throws(() => resolveBinary(manifest, target), error => error.code === Failure.Missing);
    writeFileSync(path.join(nativeRoot, "bin", companion(target)), "fixture only");
    assert.equal(resolveBinary(manifest, target), realpathSync(path.join(nativeRoot, "bin", target.binary)));
    writeFileSync(path.join(nativeRoot, "package.json"), JSON.stringify(nativeManifest(target.suffix, "0.2.0", revision)));
    assert.throws(() => resolveBinary(manifest, target), error => error.code === Failure.Version);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test("literal arguments, inherited streams and Unix signals reach the native child", async () => {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  child.exitCode = null; child.signalCode = null;
  const signals = []; child.kill = signal => signals.push(signal);
  const args = ["run", "--", "program", "", "literal;$()"];
  const promise = launch("/private/pnport", args, { parent, platform: "darwin", spawnChild(binary, received, options) {
    assert.equal(binary, "/private/pnport"); assert.deepEqual(received, args);
    assert.deepEqual(options, { stdio: "inherit", shell: false }); return child;
  } });
  parent.emit("SIGTERM"); assert.deepEqual(signals, ["SIGTERM"]);
  child.emit("exit", 42, null); assert.deepEqual(await promise, { code: 42, signal: null });
  assert.equal(parent.listenerCount("SIGTERM"), 0);
});

test("Windows console cancellation waits for native cleanup", async () => {
  const parent = new EventEmitter(); const child = new EventEmitter();
  child.exitCode = null; child.signalCode = null;
  child.kill = () => assert.fail("console events must not forcibly kill the child");
  const promise = launch("pnport.exe", [], { parent, platform: "win32", spawnChild: () => child });
  parent.emit("SIGINT"); parent.emit("SIGBREAK");
  child.emit("exit", 130, null); assert.equal((await promise).code, 130);
});
