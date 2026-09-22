"use strict";

const assert = require("node:assert/strict");
const { execFileSync, spawnSync } = require("node:child_process");
const { readFileSync, realpathSync } = require("node:fs");
const path = require("node:path");
const { createServer } = require("node:net");
const test = require("node:test");

// Invoke the active pnpm entry point without a shell, including on Windows.
const pnpm = process.env.npm_execpath ?? "pnpm";
const isScript = /\.(?:c?js|mjs)$/u.test(pnpm);
const command = isScript ? process.execPath : pnpm;
const prefix = [...(isScript ? [pnpm] : []), "exec", "clibox"];
const invoke = (args, options = {}) => execFileSync(command, [...prefix, ...args], options);

test("pnpm executes the published prebuilt separately from the source workspace without downloading", () => {
  const installed = realpathSync(path.resolve(__dirname, "../../node_modules/clibox-prebuilt/package.json"));
  const source = realpathSync(path.resolve(__dirname, "../../packages/clibox/package.json"));
  assert.notEqual(installed, source);
  const manifest = JSON.parse(readFileSync(installed, "utf8"));
  assert.equal(manifest.name, "@delino/clibox");
  assert.equal(manifest.version, "0.1.6");
  assert.notEqual(manifest.private, true);
  const options = { encoding: "utf8", env: { ...process.env, npm_config_offline: "true", npm_config_registry: "http://127.0.0.1:1" } };
  assert.equal(invoke(["--version"], options), "clibox 0.1.6\n");
  assert.equal(invoke(["--version"], options), "clibox 0.1.6\n");
});

test("binary Base64 decoding accepts whitespace and fails without leaking malformed input", () => {
  const bytes = Buffer.from([0, 255, 10, 128, 13, 0]);
  const encoded = bytes.toString("base64");
  assert.deepEqual(invoke(["base64", "decode"], { input: ` \n${encoded.slice(0, 4)}\t${encoded.slice(4)}\r\n` }), bytes);
  const invalid = "private-invalid-base64-fixture!";
  const result = spawnSync(command, [...prefix, "base64", "decode"], { input: invalid, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.ok(!result.stderr.includes(invalid));
});

test("pnpm preserves literal template values and command failures", () => {
  const value = "https://example.invalid/archive?one=1&two=$2|\\tail";
  assert.equal(invoke(["text", "replace", "__URL__", value], { input: "__URL__\n", encoding: "utf8" }), `${value}\n`);
  const failed = spawnSync(command, [...prefix, "hash", "verify", "0".repeat(64), "--text", "fixture", "--quiet"], { encoding: "utf8" });
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
