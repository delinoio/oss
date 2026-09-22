"use strict";

const assert = require("node:assert/strict");
const { execFileSync, spawnSync } = require("node:child_process");
const { mkdtempSync, mkdirSync, writeFileSync, rmSync } = require("node:fs");
const { tmpdir } = require("node:os");
const path = require("node:path");
const { createServer } = require("node:net");
const test = require("node:test");
const { resolveLauncher } = require("../clibox.cjs");

const runner = path.resolve(__dirname, "../clibox.cjs");
const invoke = (args, options = {}) => execFileSync(process.execPath, [runner, ...args], options);

test("published prebuilt is distinct from the source workspace and runs offline from any cwd", () => {
  const resolved = resolveLauncher();
  assert.equal(resolved.version, "0.1.6");
  assert.ok(resolved.launcher.includes(`${path.sep}node_modules${path.sep}`));
  const options = { cwd: tmpdir(), encoding: "utf8", env: { ...process.env, npm_config_offline: "true", npm_config_registry: "http://127.0.0.1:1" } };
  assert.equal(invoke(["--version"], options), "clibox 0.1.6\n");
  assert.equal(invoke(["--version"], options), "clibox 0.1.6\n");
});

test("missing, mismatched, and private source installations never fall back", (t) => {
  const root = mkdtempSync(path.join(tmpdir(), "clibox-resolution-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(path.join(root, "package.json"), JSON.stringify({ devDependencies: { "clibox-prebuilt": "npm:@delino/clibox@0.1.6" } }));
  assert.throws(() => resolveLauncher(root), /pnpm install/u);
  const installed = path.join(root, "node_modules/clibox-prebuilt");
  mkdirSync(installed, { recursive: true });
  for (const overrides of [{ version: "0.1.5" }, { private: true }]) {
    writeFileSync(path.join(installed, "package.json"), JSON.stringify({ name: "@delino/clibox", version: "0.1.6", bin: { clibox: "bin/clibox.cjs" }, ...overrides }));
    assert.throws(() => resolveLauncher(root), /pnpm install/u);
  }
});

test("binary Base64 decoding accepts whitespace and fails without leaking malformed input", () => {
  const bytes = Buffer.from([0, 255, 10, 128, 13, 0]);
  const encoded = bytes.toString("base64");
  assert.deepEqual(invoke(["base64", "decode"], { input: ` \n${encoded.slice(0, 4)}\t${encoded.slice(4)}\r\n` }), bytes);
  const invalid = "private-invalid-base64-fixture!";
  const result = spawnSync(process.execPath, [runner, "base64", "decode"], { input: invalid, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.ok(!result.stderr.includes(invalid));
});

test("literal template values and native exit codes survive the repository launcher", () => {
  const value = "https://example.invalid/archive?one=1&two=$2|\\tail";
  assert.equal(invoke(["text", "replace", "__URL__", value], { input: "__URL__\n", encoding: "utf8" }), `${value}\n`);
  const failed = spawnSync(process.execPath, [runner, "hash", "verify", "0".repeat(64), "--text", "fixture", "--quiet"], { encoding: "utf8" });
  assert.equal(failed.status, 1);
  assert.equal(failed.stdout, "");
  assert.equal(invoke(["time", "format", "2026-09-22T08:21:16Z", "--timezone", "UTC", "--format", "%Y-%m-%dT%H:%M:%SZ"], { encoding: "utf8" }), "2026-09-22T08:21:16Z\n");
});

test("development port conflicts retain binding checks and give portable clibox guidance", async (t) => {
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const port = String(server.address().port);
  const result = spawnSync(process.execPath, [path.resolve(__dirname, "../run-rspress-port.mjs"), "fixture", "dev", port, "CLIBOX_TEST_PORT_OVERRIDE"], { encoding: "utf8", env: { ...process.env, CLIBOX_TEST_PORT_OVERRIDE: port } });
  assert.equal(result.status, 1);
  assert.ok(result.stderr.includes(`pnpm exec clibox port list ${port}`));
  assert.ok(result.stderr.includes('clibox env run "CLIBOX_TEST_PORT_OVERRIDE=<free-port>"'));
  assert.equal(server.listening, true);
});
