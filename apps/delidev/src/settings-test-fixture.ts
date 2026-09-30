// SPDX-License-Identifier: Apache-2.0
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import processInfo from "node:process";
import { promisify } from "node:util";
import { createClient, type Transport } from "@connectrpc/connect";
import { afterAll, afterEach, beforeAll, vi } from "vitest";
import { SystemService, createDeliDevTransport, newRequestId } from "@delinoio/delidev-api-client";

export const pause = () => new Promise((resolve) => setTimeout(resolve, 25));
export async function stopChild(child?: ChildProcess) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  const until = Date.now() + 5000;
  while (child.exitCode === null && child.signalCode === null && Date.now() < until) await pause();
  if (child.exitCode === null && child.signalCode === null) {
    const exit = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.kill("SIGKILL"); await exit;
  }
}

// Call once per integration test file; no process or database is shared across files.
export function useSettingsFixture() {
let directory: string, transport: Transport, providerOrigin: string, binary: string, scope: string;
let process: ChildProcess | undefined, worker: ChildProcess | undefined, provider: Server | undefined;
afterEach(() => vi.unstubAllGlobals());
beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "delidev-settings-"));
  binary = join(directory, processInfo.platform === "win32" ? "delidev.exe" : "delidev");
  await promisify(execFile)("go", ["build", "-o", binary, "./cmds/delidev-cli"], { cwd: resolve(processInfo.cwd(), "../.."), timeout: 120000 });
  scope = join(directory, "server");
  process = spawn(binary, ["--data-dir", scope, "server", "run", "--listen", "127.0.0.1:0"], { stdio: "ignore" });
  const until = Date.now() + 15000;
  let ready = false;
  while (Date.now() < until) {
    if (process.exitCode !== null) throw new Error("Temporary server exited before readiness");
    try {
      const origin = JSON.parse(await readFile(join(scope, "server.json"), "utf8")).url;
      const token = JSON.parse(await readFile(join(scope, "owner.json"), "utf8")).token;
      transport = createDeliDevTransport({ origin, getToken: () => token });
      await createClient(SystemService, transport).getStatus({}, { timeoutMs: 1000 });
      ready = true;
      break;
    } catch { await pause(); }
  }
  if (!ready) throw new Error("Temporary server readiness timed out");
  // Only this owned non-inference HTTP endpoint is used. No installed model
  // server, user credential store, upstream account or harness is accessed.
  provider = createServer((request, response) => {
    if (request.method !== "GET" || request.url !== "/v1/models") { response.writeHead(404); response.end(); return; }
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ object: "list", data: [{ id: "fixture-model", object: "model" }] }));
  });
  await new Promise<void>((resolve) => provider!.listen(0, "127.0.0.1", resolve));
  const address = provider.address();
  if (!address || typeof address === "string") throw new Error("No fixture listener");
  providerOrigin = `http://127.0.0.1:${address.port}/v1`;
}, 150000);
function runCLI(args: string[], input?: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = execFile(binary, ["--data-dir", scope, ...args], { timeout: 30000, maxBuffer: 1 << 20 }, (error, stdout) => error ? reject(new Error("Owned fixture CLI failed")) : resolve(stdout));
    child.stdin!.end(input);
  });
}

afterAll(async () => {
  try {
    await stopChild(worker);
    if (process && process.exitCode === null && transport) {
      try { await createClient(SystemService, transport).stopServer({ requestId: newRequestId() }, { timeoutMs: 2000 }); } catch { /* The owned child is reaped below. */ }
    }
    await stopChild(process);
  } finally {
    if (provider?.listening) await new Promise<void>((resolve) => provider!.close(() => resolve()));
    if (directory) await rm(directory, { recursive: true, force: true });
  }
}, 15000);
return { get directory() { return directory; }, get transport() { return transport; }, get providerOrigin() { return providerOrigin; }, get binary() { return binary; }, get scope() { return scope; }, get worker() { return worker; }, set worker(value: ChildProcess | undefined) { worker = value; }, runCLI };
}
