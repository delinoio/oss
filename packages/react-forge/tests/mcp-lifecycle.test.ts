import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import test from "node:test";

const cli = fileURLToPath(new URL("../bin/react-forge.mjs", import.meta.url));
const exec = promisify(execFile);

async function processFixture(work: (peer: Awaited<ReturnType<typeof rawPeer>>, cwd: string) => Promise<void>) {
  const cwd = await mkdtemp(join(tmpdir(), "react-forge-mcp-process-"));
  const peer = await rawPeer(cwd);
  try { await work(peer, cwd); }
  finally { peer.child.kill("SIGKILL"); await peer.exit; await rm(cwd, { recursive: true, force: true }); }
}
async function rawPeer(cwd: string) {
  const child = spawn(process.execPath, [cli, "mcp", "--cwd", cwd], { stdio: ["pipe", "pipe", "pipe"] });
  let stderr = "";
  child.stderr.on("data", chunk => { stderr += String(chunk); });
  const waiters = new Map<number, (value: any) => void>();
  let serial = 0;
  const lines = createInterface({ input: child.stdout });
  lines.on("line", line => {
    const message = JSON.parse(line);
    waiters.get(message.id)?.(message);
    waiters.delete(message.id);
  });
  const exit = new Promise<number | null>((resolve, reject) => { child.once("error", reject); child.once("exit", resolve); });
  const request = (method: string, params: unknown) => new Promise<any>(resolve => {
    const id = ++serial;
    waiters.set(id, resolve);
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
  });
  const initialized = await request("initialize", { protocolVersion: "2025-11-25", capabilities: {}, clientInfo: { name: "lifecycle-fixture", version: "1" } });
  assert.ok(initialized.result, JSON.stringify(initialized));
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" })}\n`);
  return { child, exit, stderr: () => stderr, request, async execute(code: string) {
    const result = await request("tools/call", { name: "react_forge_execute", arguments: { code } });
    assert.ok(!result.result?.isError, JSON.stringify(result));
    return result.result.structuredContent;
  } };
}

const cleanupTask = `import {writeFile} from 'node:fs/promises';import {createSession,Format} from '@delino/react-forge';
  export default async()=>{const session=createSession(Format.Pdf);await writeFile('worker.pid',String(process.pid));
    const dispose=session.dispose.bind(session);session.dispose=async()=>{await dispose();await writeFile('disposed','yes');};return session;};`;

test("EOF cleans the execution process and SIGINT/SIGTERM preserve CLI exit codes", { timeout: 20000 }, async () => {
  const signals: ("SIGINT" | "SIGTERM" | undefined)[] = process.platform === "win32" ? [undefined] : [undefined, "SIGINT", "SIGTERM"];
  for (const signal of signals) await processFixture(async (peer, cwd) => {
    await peer.execute(cleanupTask);
    if (signal) peer.child.kill(signal); else peer.child.stdin.end();
    assert.equal(await peer.exit, signal === "SIGINT" ? 130 : signal === "SIGTERM" ? 143 : 0);
    assert.equal(await readFile(join(cwd, "disposed"), "utf8"), "yes");
    const pid = Number(await readFile(join(cwd, "worker.pid"), "utf8"));
    assert.throws(() => process.kill(pid, 0));
  });
});

test("shutdown grace reaps a callback that blocks the execution event loop", { timeout: 20000 }, async () => processFixture(async (peer, cwd) => {
  await peer.execute(cleanupTask);
  void peer.request("tools/call", { name: "react_forge_execute", arguments: { code: "export default()=>{for(;;){}};" } });
  // The parent remains responsive even when trusted task code cannot cooperate.
  const listed = await peer.request("tools/list", {});
  assert.equal(listed.result.tools.length, 9);
  peer.child.stdin.end();
  const start = performance.now();
  assert.equal(await peer.exit, 0);
  assert.ok(performance.now() - start < 10000);
  const pid = Number(await readFile(join(cwd, "worker.pid"), "utf8"));
  assert.throws(() => process.kill(pid, 0));
}));

test("worker loss reports uncertain outcomes and does not restart or retry", { timeout: 10000 }, async () => processFixture(async peer => {
  const result = await peer.request("tools/call", { name: "react_forge_execute", arguments: { code: "export default()=>process.exit(19);" } });
  assert.equal(result.result.isError, true);
  assert.equal(result.result.structuredContent.error.code, "unknown_outcome");
  assert.equal(await peer.exit, 1);
}));

test("MCP CLI validation keeps stdout empty", async () => {
  for (const args of [["mcp", "--json"], ["mcp", "--cwd"], ["mcp", "--cwd", "missing-directory-that-does-not-exist"]]) {
    await assert.rejects(exec(process.execPath, [cli, ...args]), (error: any) => {
      assert.equal(error.stdout, "");
      assert.ok(["io", "malformed_input"].includes(JSON.parse(error.stderr).error.code));
      return true;
    });
  }
});
