// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const manifest = JSON.parse(readFileSync(join(root, "scripts/ci/package.json"), "utf8"));
const generatedPaths = ["protos/gen", "packages/devhud-api-client/src/gen", "packages/async-commit-hook-api-client/src/gen", "packages/delidev-api-client/src/gen"];

function fixture(t) {
  const cwd = realpathSync(mkdtempSync(join(tmpdir(), "protocol launcher with spaces ")));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const write = (path, body) => {
    mkdirSync(dirname(join(cwd, path)), { recursive: true });
    writeFileSync(join(cwd, path), body);
  };
  for (const name of ["from-root.mjs", "protocol-fresh.mjs"]) {
    write(`scripts/ci/${name}`, readFileSync(join(root, "scripts/ci", name), "utf8"));
  }
  const run = (args, options = {}) => spawnSync(process.execPath, args, {
    cwd: join(cwd, "scripts/ci"), encoding: "utf8", shell: false, timeout: 30_000, ...options,
  });
  const leaf = (name, options) => {
    const [node, ...args] = manifest.scripts[`ci:proto:${name}`].split(" ");
    assert.equal(node, "node");
    return run(args, options);
  };
  return { cwd, write, run, leaf };
}

function trace(f, outcome = { status: 0 }) {
  f.write("capture-spawn.mjs", `
    import childProcess from "node:child_process";
    import { EventEmitter } from "node:events";
    import { syncBuiltinESMExports } from "node:module";
    const outcome = ${JSON.stringify(outcome)};
    const capture = (command, args, options) => {
      console.log(JSON.stringify({ kind: "dispatch", command, args, options }));
      return { status: outcome.status, stdout: "", error: outcome.error ? new Error(outcome.error) : undefined };
    };
    childProcess.spawnSync = capture;
    childProcess.spawn = (command, args, options) => {
      capture(command, args, options);
      const child = new EventEmitter();
      child.kill = () => true;
      process.nextTick(() => outcome.error ? child.emit("error", new Error(outcome.error)) : child.emit("exit", outcome.status, null));
      return child;
    };
    syncBuiltinESMExports();
  `);
  return (name) => {
    const args = manifest.scripts[`ci:proto:${name}`].split(" ").slice(1);
    const result = f.run(["--import", pathToFileURL(join(f.cwd, "capture-spawn.mjs")).href, ...args]);
    const calls = result.stdout.split(/\r?\n/u).filter((line) => line.startsWith('{"kind":"dispatch"')).map((line) => JSON.parse(line));
    return { result, calls };
  };
}

test("lint, format and freshness dispatch the installed Buf entry through the current Node", (t) => {
  const f = fixture(t);
  const run = trace(f);
  for (const [name, args] of [["lint", ["lint"]], ["format", ["format", "--diff", "--exit-code"]]]) {
    const { result, calls } = run(name);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    assert.deepEqual(calls.map(({ command, args, options }) => ({ command, args, options: { ...options, cwd: resolve(options.cwd) } })), [{
      command: process.execPath, args: ["node_modules/@bufbuild/buf/bin/buf", ...args],
      options: { cwd: f.cwd, stdio: "inherit", shell: false },
    }]);
  }
  const { result, calls } = run("fresh");
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.deepEqual(calls.map(({ command, args }) => ({ command, args })), [
    { command: process.execPath, args: ["scripts/ci/protocol-fresh.mjs"] },
  ]);
  // Trace the actual synchronous freshness child as well as its outer wrapper.
  const fresh = f.run(["--import", pathToFileURL(join(f.cwd, "capture-spawn.mjs")).href, "protocol-fresh.mjs"]);
  assert.equal(fresh.status, 0, fresh.stdout + fresh.stderr);
  const syncCalls = fresh.stdout.trim().split(/\r?\n/u).map((line) => JSON.parse(line));
  assert.deepEqual(syncCalls.map(({ command, args }) => ({ command, args })), [
    { command: process.execPath, args: ["node_modules/@bufbuild/buf/bin/buf", "generate"] },
    { command: "git", args: ["diff", "--exit-code", "--", ...generatedPaths] },
    { command: "git", args: ["ls-files", "--others", "--exclude-standard", "--", ...generatedPaths] },
  ]);
  for (const call of syncCalls.slice(0, 2)) {
    assert.equal(resolve(call.options.cwd), f.cwd);
    assert.equal(call.options.shell, false);
  }
});

test("protocol wrappers retain child failures and spawn errors", (t) => {
  const f = fixture(t);
  for (const outcome of [{ status: 7 }, { status: null }, { error: "fixture spawn failure" }]) {
    const run = trace(f, outcome);
    for (const name of ["lint", "format"]) {
      const { result } = run(name);
      assert.equal(result.status, outcome.status ?? 1, result.stdout + result.stderr);
      if (outcome.error) assert.match(result.stderr, /fixture spawn failure/u);
    }
    const fresh = f.run(["--import", pathToFileURL(join(f.cwd, "capture-spawn.mjs")).href, "protocol-fresh.mjs"]);
    assert.equal(fresh.status, outcome.status ?? 1, fresh.stdout + fresh.stderr);
    assert.equal(fresh.stdout.trim().split(/\r?\n/u).length, 1, "Failed generation must stop before freshness checks");
    if (outcome.error) assert.match(fresh.stderr, /fixture spawn failure/u);
  }
});

test("from-root preserves spaces and shell metacharacters in literal Node argv", (t) => {
  const f = fixture(t);
  f.write("argv entry.cjs", "console.log(JSON.stringify({ cwd: process.cwd(), args: process.argv.slice(2) }));\n");
  const args = ["with spaces", "& echo injected > shell-ran", "; echo injected > shell-ran", "$(echo injected)", "`echo injected`", "quote\" ' ^ % ! | < >", "한글"];
  const result = f.run(["from-root.mjs", "node", "argv entry.cjs", ...args]);
  assert.equal(result.status, 0, result.stdout + result.stderr);
  const output = result.stdout.split(/\r?\n/u).find((line) => line.startsWith('{"cwd"'));
  assert.deepEqual(JSON.parse(output), { cwd: f.cwd, args });
  assert.equal(existsSync(join(f.cwd, "shell-ran")), false);
  assert.equal(existsSync(join(f.cwd, "scripts/ci/shell-ran")), false);
});

function installedFixture(t) {
  const f = fixture(t);
  symlinkSync(join(root, "node_modules"), join(f.cwd, "node_modules"), process.platform === "win32" ? "junction" : "dir");
  // Remove all Buf executable/shim directories, including pnpm's inherited bins.
  // Preserve native Git for freshness inspection and normal OS runtime lookup.
  const env = Object.fromEntries(Object.entries(process.env).filter(([name]) => name.toUpperCase() !== "PATH"));
  const path = Object.entries(process.env).find(([name]) => name.toUpperCase() === "PATH")?.[1] ?? "";
  env.PATH = path.split(delimiter).filter((entry) => entry && !["buf", "buf.exe", "buf.cmd", "buf.bat"].some((name) => existsSync(join(entry, name)))).join(delimiter);
  const unavailable = spawnSync("buf", ["--version"], { cwd: f.cwd, env, shell: false });
  assert.ok(unavailable.error, "The fixture must not have a standalone Buf on PATH");
  f.write("gitconfig", "");
  Object.assign(env, { GIT_CONFIG_GLOBAL: join(f.cwd, "gitconfig"), GIT_CONFIG_NOSYSTEM: "1", GIT_TERMINAL_PROMPT: "0" });
  f.write("buf.yaml", "version: v2\nmodules:\n  - path: protos\nlint:\n  use:\n    - STANDARD\n");
  const schema = 'syntax = "proto3";\n\npackage fixture.v1;\n\nmessage Fixture {\n  string value = 1;\n}\n';
  f.write("protos/fixture/v1/fixture.proto", schema);
  const buf = (...args) => spawnSync(process.execPath, ["node_modules/@bufbuild/buf/bin/buf", ...args], {
    cwd: f.cwd, env, encoding: "utf8", shell: false, timeout: 30_000,
  });
  const leaf = (name) => f.leaf(name, { env });
  return { ...f, env, schema, buf, leaf };
}

test("installed Buf lint and format return their actual results with no standalone Buf", (t) => {
  const f = installedFixture(t);
  const pinned = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).devDependencies["@bufbuild/buf"];
  const version = f.buf("--version");
  assert.equal(version.status, 0, version.stdout + version.stderr);
  assert.equal(version.stdout.trim(), pinned);
  for (const name of ["lint", "format"]) {
    const result = f.leaf(name);
    assert.equal(result.status, 0, result.stdout + result.stderr);
  }
  f.write("protos/fixture/v1/fixture.proto", f.schema.replace("message Fixture", "message fixture"));
  const lint = f.buf("lint");
  assert.notEqual(lint.status, 0);
  assert.equal(f.leaf("lint").status, lint.status);
  f.write("protos/fixture/v1/fixture.proto", f.schema.replace("  string value = 1;", "string value=1;"));
  const format = f.buf("format", "--diff", "--exit-code");
  assert.notEqual(format.status, 0);
  assert.equal(f.leaf("format").status, format.status);
});

test("installed Buf generation retains failure status and tracked/untracked freshness", (t) => {
  const f = installedFixture(t);
  const git = (...args) => execFileSync("git", args, { cwd: f.cwd, env: f.env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(f.cwd, "empty-hooks"));
  f.write(".gitignore", "node_modules\n");
  f.write("output.txt", "fixture output\n");
  // A tiny offline protoc plugin emits one short CodeGeneratorResponse file.
  // It needs no Go/TypeScript plugin toolchain to exercise the Buf launcher.
  f.write("plugin.cjs", `
    const { readFileSync } = require("node:fs");
    const field = (tag, value) => Buffer.concat([Buffer.from([tag, value.length]), value]);
    process.stdin.resume();
    process.stdin.on("end", () => {
      const file = Buffer.concat([field(10, Buffer.from("fixture.txt")), field(122, readFileSync("output.txt"))]);
      process.stdout.write(field(122, file));
    });
  `);
  f.write("buf.gen.yaml", JSON.stringify({ version: "v2", plugins: [{ local: [process.execPath, join(f.cwd, "plugin.cjs")], out: "protos/gen" }] }));
  let result = f.leaf("fresh");
  assert.notEqual(result.status, 0, "New generated files must be rejected");
  assert.match(result.stderr, /Generated protocol files are untracked/u);
  assert.equal(readFileSync(join(f.cwd, "protos/gen/fixture.txt"), "utf8"), "fixture output\n");
  git("add", "--all");
  git("commit", "-m", "clean protocol fixture");
  result = f.leaf("fresh");
  assert.equal(result.status, 0, result.stdout + result.stderr);
  f.write("output.txt", "changed output\n");
  result = f.leaf("fresh");
  assert.notEqual(result.status, 0, "Changed generated output must be rejected");
  assert.match(result.stdout, /changed output/u);
  f.write("output.txt", "fixture output\n");
  assert.equal(f.leaf("fresh").status, 0);
  for (const path of generatedPaths) {
    f.write(`${path}/unexpected.txt`, "untracked\n");
    result = f.leaf("fresh");
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Generated protocol files are untracked/u);
    rmSync(join(f.cwd, path, "unexpected.txt"));
  }
  f.write("protos/fixture/v1/fixture.proto", "invalid schema\n");
  const generation = f.buf("generate");
  assert.notEqual(generation.status, 0);
  result = f.leaf("fresh");
  assert.equal(result.status, generation.status, result.stdout + result.stderr);
});
