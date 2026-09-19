import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import net from "node:net";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const app = new URL("../", import.meta.url);
const wrapper = new URL("development.mjs", import.meta.url);

async function claimPort() {
  const server = net.createServer();
  server.listen(46308, "localhost");
  await once(server, "listening");
  return server;
}

test("development rejects address overrides and occupied ports", async () => {
  const override = spawnSync(process.execPath, [fileURLToPath(wrapper), "--port", "46310"], { cwd: app, encoding: "utf8" });
  assert.notEqual(override.status, 0);
  assert.match(override.stderr, /fixed localhost:46308/);
  const server = await claimPort();
  try {
    const occupied = spawnSync(process.execPath, [fileURLToPath(wrapper)], { cwd: app, encoding: "utf8" });
    assert.equal(occupied.status, 1);
    assert.match(occupied.stderr, /Port 46308 is occupied/);
  } finally { await new Promise((resolve) => server.close(resolve)); }
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  test(`development awaits descendant cleanup on ${signal}`, { timeout: 60_000 }, async () => {
    const dir = await mkdtemp(join(tmpdir(), "ach-development-"));
    let child;
    let descendants;
    try {
      const bin = join(dir, "bin"), fixture = join(dir, "pnpm.mjs");
      await mkdir(bin);
      // This stand-in for pnpm deliberately does not forward termination. Its
      // server grandchild must be stopped by the wrapper's process-tree owner.
      await writeFile(fixture, `
import { spawn } from 'node:child_process';
const server = spawn(process.execPath, ['--input-type=module', '-e', ${JSON.stringify(`
import net from 'node:net';
const server = net.createServer();
server.listen(46308, 'localhost', () => process.send({ ready: true }));
`)}], { stdio: ['ignore', 'ignore', 'inherit', 'ipc'] });
server.on('message', () => console.log(JSON.stringify({ parent: process.pid, server: server.pid })));
`);
      await writeFile(join(bin, "pnpm"), '#!/bin/sh\nexec "$ACH_TEST_NODE" "$ACH_DEV_FIXTURE"\n', { mode: 0o755 });
      await writeFile(join(bin, "pnpm.cmd"), '@"%ACH_TEST_NODE%" "%ACH_DEV_FIXTURE%"\r\n');
      // Windows lacks POSIX process signals. Inject the same Node signal event
      // over IPC there; production still runs its real taskkill /t cleanup.
      child = spawn(process.execPath, ["--input-type=module", "-e", `
process.argv = [process.execPath, ${JSON.stringify(fileURLToPath(wrapper))}];
process.on('message', signal => process.emit(signal));
await import(${JSON.stringify(wrapper.href)});
`], {
        cwd: app,
        env: { ...process.env, PATH: bin + delimiter + process.env.PATH, ACH_TEST_NODE: process.execPath, ACH_DEV_FIXTURE: fixture },
        stdio: ["ignore", "pipe", "pipe", "ipc"],
      });
      const exited = once(child, "exit");
      let output = "", errors = "";
      child.stderr.on("data", (chunk) => { errors += chunk; });
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(`server did not start: ${errors}`)), 30_000);
        child.once("exit", () => { clearTimeout(timer); reject(new Error(`wrapper exited before readiness: ${errors}`)); });
        child.stdout.on("data", (chunk) => {
          output += chunk;
          if (output.includes("\n")) {
            try { descendants = JSON.parse(output.trim()); clearTimeout(timer); resolve(); }
            catch (error) { clearTimeout(timer); reject(error); }
          }
        });
      });
      if (process.platform === "win32") child.send(signal);
      else child.kill(signal);
      const [code, exitSignal] = await exited;
      if (process.platform !== "win32") assert.equal(exitSignal, signal, errors);
      else assert.notEqual(code, 0, errors);
      // Rebinding immediately after wrapper exit proves it awaited cleanup,
      // rather than leaving a server that eventually exits on its own.
      const server = await claimPort();
      await new Promise((resolve) => server.close(resolve));
      descendants = undefined;
    } finally {
      if (descendants) for (const pid of [descendants.server, descendants.parent]) {
        try { process.kill(pid, "SIGKILL"); } catch (error) { if (error.code !== "ESRCH") throw error; }
      }
      if (child && child.exitCode === null && child.signalCode === null) {
        child.kill("SIGKILL");
        await once(child, "exit");
      }
      await rm(dir, { recursive: true, force: true });
    }
  });
}
