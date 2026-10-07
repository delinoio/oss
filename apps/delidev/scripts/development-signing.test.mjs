// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { once } from "node:events";
import { X509Certificate } from "node:crypto";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { developmentCertificateName, developmentServerIdentifier, publishDevelopmentBundle, readSigningIdentity, registerDevelopmentIdentity, serverRequirement, validateDevelopmentCertificate } from "./development-signing.mjs";

const identity = "A".repeat(40);
function temporary(t) {
  const root = mkdtempSync(join(tmpdir(), "delidev-development-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return root;
}
function certificate(t) {
  const root = temporary(t);
  const config = join(root, "openssl.conf");
  writeFileSync(config, `[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=ext\n[dn]\nCN=${developmentCertificateName}\n[ext]\nextendedKeyUsage=codeSigning\n`, { mode: 0o600 });
  execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-config", config, "-keyout", join(root, "key"), "-out", join(root, "cert")], { stdio: "ignore" });
  const pem = readFileSync(join(root, "cert"), "utf8");
  return { pem, fingerprint: new X509Certificate(pem).fingerprint.replaceAll(":", "") };
}

test("development identity requires the exact dedicated self-signed, unexpired code certificate", t => {
  const { pem, fingerprint } = certificate(t);
  assert.equal(validateDevelopmentCertificate(pem, fingerprint.toLowerCase()), fingerprint);
  assert.throws(() => validateDevelopmentCertificate(pem, identity), /development-certificate-missing/);
  assert.throws(() => validateDevelopmentCertificate(pem, "certificate name"), /signing-identity-invalid/);
  assert.throws(() => validateDevelopmentCertificate(pem, fingerprint, Date.now() + 2 * 86400_000), /development-certificate-expired/);
});

test("registration persists only a fingerprint and rejects unsafe or malformed local configuration", async t => {
  const root = temporary(t), directory = join(root, "config");
  const { pem, fingerprint } = certificate(t);
  const calls = [];
  await registerDevelopmentIdentity(fingerprint, { directory, environment: {}, command: async (name, args) => {
    calls.push([name, args]);
    return name === "/usr/bin/security" ? pem : "";
  } });
  assert.equal(readSigningIdentity(directory), fingerprint);
  assert.deepEqual(JSON.parse(readFileSync(join(directory, "signing.json"))), { schemaVersion: 1, identity: fingerprint });
  assert.deepEqual(calls[0][1], ["find-certificate", "-a", "-c", developmentCertificateName, "-p"]);
  assert.ok(calls[1][1].includes(developmentServerIdentifier));
  chmodSync(join(directory, "signing.json"), 0o644);
  assert.throws(() => readSigningIdentity(directory), /signing-config-invalid/);
  chmodSync(join(directory, "signing.json"), 0o600);
  writeFileSync(join(directory, "signing.json"), "{}");
  assert.throws(() => readSigningIdentity(directory), /signing-config-invalid/);
  rmSync(join(directory, "signing.json"));
  symlinkSync(join(root, "outside"), join(directory, "signing.json"));
  assert.throws(() => readSigningIdentity(directory), /signing-config-invalid/);
});

test("published bundles retain original server bytes across rebuild", async t => {
  const root = temporary(t), source = join(root, "source.app"), output = join(root, "runs");
  const binaries = join(source, "Contents/MacOS");
  mkdirSync(binaries, { recursive: true });
  writeFileSync(join(binaries, "delidev"), "first-server");
  writeFileSync(join(binaries, "delidev-desktop"), "desktop");
  const calls = [];
  const command = async (_name, args) => { calls.push(args); return ""; };
  const first = await publishDevelopmentBundle(source, output, identity, {}, command);
  writeFileSync(join(binaries, "delidev"), "second-server");
  const second = await publishDevelopmentBundle(source, output, identity, {}, command);
  assert.notEqual(first, second);
  assert.equal(readFileSync(join(first, "../delidev"), "utf8"), "first-server");
  assert.equal(readFileSync(join(second, "../delidev"), "utf8"), "second-server");
  assert.equal(readdirSync(output).length, 2);
  for (const args of calls.filter(args => args.includes("--force"))) assert.ok(!args.includes("--deep"));
  assert.ok(calls.some(args => args.includes(serverRequirement(identity))));
});

test("a forced desktop exit preserves simulated server/Worker processes and their published files", { skip: process.platform !== "darwin", timeout: 20_000 }, async t => {
  const root = mkdtempSync(join(tmpdir(), "delidev-process-test-")), source = join(root, "source.app"), output = join(root, "runs");
  const binaries = join(source, "Contents/MacOS");
  mkdirSync(binaries, { recursive: true });
  copyFileSync("/bin/sleep", join(binaries, "delidev"));
  const original = readFileSync(join(binaries, "delidev"));
  writeFileSync(join(binaries, "delidev-desktop"), "fixture-desktop", { mode: 0o700 });
  // Simulate two independent execution roles that hold the published file open.
  // Native Mach-O identity is exercised separately by temporary-keychain tests.
  const desktop = `
    const { spawn } = require('node:child_process');
    const { once } = require('node:events');
    const file = require('node:path').join(process.argv[1], '../delidev');
    const code = 'require("node:fs").openSync(process.argv[1], "r"); process.send("fixture-ready"); setInterval(() => {}, 1000);';
    const children = [0, 1].map(() => spawn(process.execPath, ['-e', code, file], { stdio: ['ignore', 'ignore', 'ignore', 'ipc'] }));
    Promise.all(children.map(child => once(child, 'message'))).then(() => process.stdout.write('fixture-ready ' + process.pid + ' ' + children.map(child => child.pid).join(' ') + '\\n'));
    setInterval(() => {}, 1000);
  `;
  const wrapper = spawn(process.execPath, ["--input-type=module", "-e", `
    import { runDesktop } from ${JSON.stringify(new URL("./run-desktop.mjs", import.meta.url).href)};
    import { publishDevelopmentBundle } from ${JSON.stringify(new URL("./development-signing.mjs", import.meta.url).href)};
    import { spawnDevServer, exitLikeChild } from ${JSON.stringify(new URL("../../../scripts/spawn-dev-server.mjs", import.meta.url).href)};
    let calls = 0;
    exitLikeChild(await runDesktop([], {
      platform: 'darwin', environment: { npm_execpath: '/fixture/pnpm.cjs' }, log() {},
      identityFor: async () => ${JSON.stringify(identity)}, lock: () => () => {}, creditsFor: () => '/fixture/CREDITS.html',
      publish: (_source, _output, identity, options) => publishDevelopmentBundle(${JSON.stringify(source)}, ${JSON.stringify(output)}, identity, options, async () => ''),
      run: async (command, args, options, lifecycle) => ++calls < 3 ? { code: 0, signal: null } : spawnDevServer(process.execPath, ['-e', ${JSON.stringify(desktop)}, command], options, lifecycle),
    }));
  `], { stdio: ["ignore", "pipe", "pipe"] });
  let diagnostics = "";
  wrapper.stderr.on("data", chunk => { diagnostics += chunk; });
  let pids = [];
  t.after(async () => {
    const owned = [...pids, ...(wrapper.exitCode === null && wrapper.signalCode === null ? [wrapper.pid] : [])];
    for (const pid of owned) {
      try { process.kill(pid, "SIGKILL"); } catch (error) { if (error.code !== "ESRCH") throw error; }
    }
    const deadline = Date.now() + 5000;
    for (const pid of owned) {
      while (true) {
        try { process.kill(pid, 0); } catch (error) { if (error.code === "ESRCH") break; throw error; }
        assert.ok(Date.now() < deadline, "owned fixture process did not exit before file cleanup");
        await new Promise(resolve => setTimeout(resolve, 10));
      }
    }
    rmSync(root, { recursive: true, force: true });
  });
  const exited = once(wrapper, "exit");
  let text = "";
  for await (const chunk of wrapper.stdout.iterator({ destroyOnReturn: false })) {
    text += chunk;
    const ready = text.match(/fixture-ready (\d+) (\d+) (\d+)\n/);
    if (ready) { pids = ready.slice(1).map(Number); break; }
  }
  assert.equal(pids.length, 3, diagnostics + text);
  const first = join(output, readdirSync(output)[0], "DeliDev.app/Contents/MacOS/delidev");
  process.kill(pids[0], "SIGKILL");
  assert.deepEqual(await exited, [null, "SIGKILL"]);
  pids = pids.slice(1);
  for (const pid of pids) process.kill(pid, 0);
  writeFileSync(join(binaries, "delidev"), "rebuilt-source");
  await publishDevelopmentBundle(source, output, identity, {}, async () => "");
  assert.deepEqual(readFileSync(first), original);
  for (const pid of pids) process.kill(pid, 0);
});

test("signing failure discards only unpublished output and keeps earlier live bundles", async t => {
  const root = temporary(t), source = join(root, "source.app"), output = join(root, "runs");
  mkdirSync(join(source, "Contents/MacOS"), { recursive: true });
  for (const name of ["delidev", "delidev-desktop"]) writeFileSync(join(source, "Contents/MacOS", name), "original");
  const first = await publishDevelopmentBundle(source, output, identity, {}, async () => "");
  await assert.rejects(publishDevelopmentBundle(source, output, identity, {}, async () => { throw new Error("fixture-signing-failed"); }), /fixture-signing-failed/);
  assert.ok(existsSync(first));
  assert.equal(readdirSync(output).length, 1);
});
