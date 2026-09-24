import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { EventEmitter, once } from "node:events";
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import platforms from "../src/platforms.cjs";
import launcher from "../src/launcher.cjs";
import { packageRoot } from "../scripts/common.mjs";

const { targets, Platform, Architecture, Libc, selectTarget } = platforms;
const { Failure, resolveBinary, launch } = launcher;
const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

function fixture(t) {
  const directory = realpathSync(mkdtempSync(path.join(tmpdir(), "clibox launcher ")));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const manifestPath = path.join(directory, "package.json");
  writeFileSync(manifestPath, JSON.stringify({ name: "@delino/clibox", version: "1.2.3" }));
  const target = selectTarget();
  const dependency = path.join(directory, "node_modules", target.name);
  mkdirSync(path.join(dependency, "bin"), { recursive: true });
  writeFileSync(path.join(dependency, "package.json"), JSON.stringify({ name: target.name, version: "1.2.3" }));
  const binary = path.join(dependency, "bin", target.binary);
  writeFileSync(binary, "fixture");
  return { directory, manifestPath, target, dependency, binary };
}

test("all eight OS/CPU/libc combinations select exactly one explicit target", () => {
  assert.equal(targets.length, 8);
  assert.equal(new Set(targets.map(({ rust }) => rust)).size, 8);
  for (const target of targets) assert.equal(selectTarget(target.os, target.cpu, target.libc), target);
  assert.equal(selectTarget(Platform.Linux, Architecture.X64, Libc.Musl).suffix, "linux-x64-musl");
  for (const tuple of [["freebsd", "x64"], ["darwin", "ia32"], ["linux", "arm", "glibc"], ["linux", "x64", "unknown"]]) assert.equal(selectTarget(...tuple), undefined);
});

test("resolution requires the exact dependency identity, version and binary", (t) => {
  const f = fixture(t);
  assert.equal(resolveBinary(f.manifestPath, f.target), f.binary);
  assert.throws(() => resolveBinary(f.manifestPath, null), { code: Failure.Unsupported });
  for (const manifest of [{ name: f.target.name, version: "1.2.4" }, { name: "unexpected", version: "1.2.3" }]) {
    writeFileSync(path.join(f.dependency, "package.json"), JSON.stringify(manifest));
    assert.throws(() => resolveBinary(f.manifestPath, f.target), { code: Failure.Version });
  }
  writeFileSync(path.join(f.dependency, "package.json"), JSON.stringify({ name: f.target.name, version: "1.2.3" }));
  rmSync(f.binary);
  assert.throws(() => resolveBinary(f.manifestPath, f.target), { code: Failure.Missing });
  rmSync(f.dependency, { recursive: true });
  assert.throws(() => resolveBinary(f.manifestPath, f.target), (error) => error.code === Failure.Missing && /optional dependencies/u.test(error.message));
});

test("launch preserves literal argv, stdio, status and forwards signals without retaining handlers", async () => {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  const received = [];
  child.kill = (signal) => received.push(signal);
  const args = ["space argument", "$(not-a-command)", "--", "한글"];
  const result = launch("/a path/clibox", args, { platform: Platform.Linux, parent, spawnChild: (file, actualArgs, options) => {
    assert.equal(file, "/a path/clibox");
    assert.equal(actualArgs, args);
    assert.deepEqual(options.stdio, ["inherit", "inherit", "inherit", "pipe"]);
    assert.equal(options.shell, false);
    assert.equal(options.env.CLIBOX_TERMINAL_INTERRUPT_ACK_FD, "3");
    return child;
  } });
  parent.emit("SIGTERM");
  assert.deepEqual(received, ["SIGTERM"]);
  child.emit("exit", 23, null);
  assert.deepEqual(await result, { code: 23, signal: null });
  assert.equal(parent.eventNames().length, 0);
});

test("spawn failures produce actionable diagnostics and remove signal handlers", async () => {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  const result = launch("missing", [], { parent, spawnChild: () => child });
  child.emit("error", new Error("private fixture path"));
  await assert.rejects(result, (error) => error.code === Failure.Spawn && !error.message.includes("private fixture"));
  assert.equal(parent.eventNames().length, 0);
});

test("Windows console events await native cleanup and preserve numeric cancellation", async () => {
  for (const signal of ["SIGINT", "SIGBREAK"]) {
    for (const code of [130, 1]) {
      const parent = new EventEmitter();
      const child = new EventEmitter();
      child.exitCode = null;
      child.signalCode = null;
      const received = [];
      child.kill = (value) => received.push(value);
      const result = launch("clibox.exe", [], { platform: Platform.Windows, parent, spawnChild: () => child });
      let settled = false;
      result.then(() => { settled = true; });
      assert.equal(parent.emit(signal), true);
      await Promise.resolve();
      assert.equal(settled, false);
      assert.deepEqual(received, []);
      child.emit("exit", code, null);
      assert.deepEqual(await result, { code, signal: null });
      assert.equal(parent.eventNames().length, 0);
    }
  }
});

test("Unix launchers continue forwarding SIGINT, SIGTERM and SIGHUP", async () => {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  const received = [];
  child.kill = (signal) => received.push(signal);
  const result = launch("clibox", [], { platform: Platform.Linux, parent, spawnChild: () => child });
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) parent.emit(signal);
  await delay(20);
  assert.deepEqual(received.sort(), ["SIGINT", "SIGTERM", "SIGHUP"].sort());
  child.emit("exit", null, "SIGTERM");
  assert.deepEqual(await result, { code: null, signal: "SIGTERM" });
  assert.equal(parent.eventNames().length, 0);
});

test("Unix launchers do not forward a terminal SIGINT acknowledged by the native child", async () => {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  const acknowledgement = new EventEmitter();
  child.stdio = [null, null, null, acknowledgement];
  child.exitCode = null;
  child.signalCode = null;
  const received = [];
  child.kill = (signal) => received.push(signal);
  const result = launch("clibox", [], { platform: Platform.Linux, parent, spawnChild: () => child });

  parent.emit("SIGINT");
  acknowledgement.emit("data", Buffer.from([1]));
  await delay(20);
  assert.deepEqual(received, []);

  parent.emit("SIGINT");
  await delay(20);
  assert.deepEqual(received, ["SIGINT"]);
  child.emit("exit", null, "SIGTERM");
  assert.deepEqual(await result, { code: null, signal: "SIGTERM" });
  assert.equal(parent.eventNames().length, 0);
});

test("Unix launchers forward SIGINT despite TTY stdin", async () => {
  const parent = new EventEmitter();
  parent.stdin = { isTTY: true };
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  const received = [];
  child.kill = (signal) => received.push(signal);
  const result = launch("clibox", [], { platform: Platform.Linux, parent, spawnChild: () => child });
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) parent.emit(signal);
  await delay(20);
  assert.deepEqual(received.sort(), ["SIGINT", "SIGTERM", "SIGHUP"].sort());
  child.emit("exit", null, "SIGTERM");
  assert.deepEqual(await result, { code: null, signal: "SIGTERM" });
  assert.equal(parent.eventNames().length, 0);
});

test("the installed launcher propagates real stdout/stderr, argv, cwd and exit status", { skip: process.platform === "win32" }, async (t) => {
  const f = fixture(t);
  for (const file of ["bin/clibox.cjs", "src/launcher.cjs", "src/platforms.cjs"]) {
    mkdirSync(path.dirname(path.join(f.directory, file)), { recursive: true });
    copyFileSync(path.join(packageRoot, file), path.join(f.directory, file));
  }
  writeFileSync(f.binary, `#!/usr/bin/env node\nconsole.log(JSON.stringify({args:process.argv.slice(2),cwd:process.cwd(),value:process.env.CLIBOX_TEST_VALUE}));console.error("fixture stderr");process.exitCode=23;\n`);
  chmodSync(f.binary, 0o755);
  const args = ["space argument", "$(literal)", "한글"];
  const child = spawn(process.execPath, [path.join(f.directory, "bin/clibox.cjs"), ...args], { cwd: f.directory, env: { ...process.env, CLIBOX_TEST_VALUE: "fixture" } });
  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (value) => { stdout += value; });
  child.stderr.on("data", (value) => { stderr += value; });
  const [code] = await once(child, "close");
  assert.equal(code, 23);
  assert.deepEqual(JSON.parse(stdout), { args, cwd: f.directory, value: "fixture" });
  assert.equal(stderr, "fixture stderr\n");
});

test("the installed launcher forwards and reproduces real termination", { skip: process.platform === "win32", timeout: 10000 }, async (t) => {
  const f = fixture(t);
  for (const file of ["bin/clibox.cjs", "src/launcher.cjs", "src/platforms.cjs"]) {
    mkdirSync(path.dirname(path.join(f.directory, file)), { recursive: true });
    copyFileSync(path.join(packageRoot, file), path.join(f.directory, file));
  }
  writeFileSync(f.binary, '#!/usr/bin/env node\nconsole.log("ready");setInterval(()=>{},1000);\n');
  chmodSync(f.binary, 0o755);
  const child = spawn(process.execPath, [path.join(f.directory, "bin/clibox.cjs")]);
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM"); });
  await once(child.stdout, "data");
  const closed = once(child, "close");
  child.kill("SIGTERM");
  assert.deepEqual(await closed, [null, "SIGTERM"]);
});
