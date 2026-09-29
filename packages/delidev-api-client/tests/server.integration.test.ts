import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { createClient, type Transport } from "@connectrpc/connect";
import { afterAll, beforeAll, expect, it } from "vitest";
import { ConfigurationService, EntityKind, ResourceService, SystemService, SystemCapability, UserServiceKind, UserServiceState } from "../src/gen/delidev/v1/delidev_pb.js";
import { createDeliDevTransport } from "../src/transport.js";
import { clientFailure, FailureCode } from "../src/errors.js";
import { ConnectionState, SyncKind, synchronizeResources } from "../src/synchronization.js";
import { newRequestId } from "../src/validation.js";

const execute = promisify(execFile);
let directory: string;
let server: ChildProcess | undefined;
let transport: Transport;
let origin: string;
let token: string;
const stopped = new AbortController();
const pause = () => new Promise((resolve) => setTimeout(resolve, 25));

beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "delidev-ts-"));
  const binary = join(directory, process.platform === "win32" ? "delidev.exe" : "delidev");
  await execute("go", ["build", "-o", binary, "./cmds/delidev-cli"], { cwd: fileURLToPath(new URL("../../../", import.meta.url)), timeout: 120000 });
  const data = join(directory, "server");
  server = spawn(binary, ["--data-dir", data, "server", "run", "--listen", "127.0.0.1:0"], { stdio: "ignore" });
  const until = Date.now() + 15000;
  while (Date.now() < until) {
    if (server.exitCode !== null) throw new Error("The isolated Go server failed to start.");
    try {
      origin = JSON.parse(await readFile(join(data, "server.json"), "utf8")).url;
      // Test-owned temporary credentials only; never read the user's scope.
      token = JSON.parse(await readFile(join(data, "owner.json"), "utf8")).token;
      transport = createDeliDevTransport({ origin, getToken: () => token });
      const status = await createClient(SystemService, transport).getStatus({}, { timeoutMs: 1000 });
      expect(status.protocolVersion).toBe(1);
      return;
    } catch { await pause(); }
  }
  throw new Error("The isolated Go server did not become ready.");
}, 150000);

afterAll(async () => {
  stopped.abort();
  if (server && server.exitCode === null) {
    try { await createClient(SystemService, transport).stopServer({ requestId: newRequestId() }, { timeoutMs: 2000 }); } catch { /* Fixture teardown still owns its child. */ }
    const until = Date.now() + 3000;
    while (server.exitCode === null && Date.now() < until) await pause();
    if (server.exitCode === null) {
      const exited = new Promise<void>((resolve) => server!.once("exit", () => resolve()));
      server.kill("SIGKILL");
      await exited;
    }
  }
  if (directory) await rm(directory, { recursive: true, force: true });
});

it("uses real Go Connect binary messages, atomic events, replay receipts and indexed deletion", async () => {
  const configuration = createClient(ConfigurationService, transport);
  const resources = createClient(ResourceService, transport);
  const stream = synchronizeResources(resources, { kind: EntityKind.TEMPLATE }, { signal: stopped.signal });
  expect((await stream.next()).value).toMatchObject({ state: ConnectionState.Connecting });
  expect((await stream.next()).value).toMatchObject({ kind: SyncKind.Snapshot, resources: [] });
  expect((await stream.next()).value).toMatchObject({ state: ConnectionState.Live });
  const next = stream.next();
  const mutation = { requestId: newRequestId(), id: newRequestId() };
  const request = { mutation, kind: EntityKind.TEMPLATE, schemaVersion: 1, documentJson: new TextEncoder().encode(JSON.stringify({ name: "Private fixture", contents: "Unicode 한글 café retained." })) };
  const accepted = await configuration.saveConfiguration(request);
  expect(accepted.resource?.revision).toBe(1n);
  const updated = (await next).value;
  expect(updated).toMatchObject({ kind: SyncKind.Upsert, resource: { id: mutation.id, revision: 1n } });
  const replay = await configuration.saveConfiguration(request);
  expect(replay.replayed).toBe(true);
  const removed = stream.next();
  await configuration.deleteConfiguration({ kind: EntityKind.TEMPLATE, mutation: { id: mutation.id, expectedRevision: 1n, requestId: newRequestId() } });
  // A replay must not publish another Created event ahead of deletion.
  expect((await removed).value).toMatchObject({ kind: SyncKind.Remove, id: mutation.id });
  stopped.abort();
  expect((await stream.next()).done).toBe(true);
}, 15000);

it("decodes the Go server's typed authentication error without returning credentials", async () => {
  const invalid = createClient(SystemService, createDeliDevTransport({ origin, getToken: () => "z".repeat(43) }));
  try { await invalid.getStatus({}); throw new Error("Expected authentication rejection."); }
  catch (reason) {
    const problem = clientFailure(reason);
    expect(problem.code).toBe(FailureCode.Unauthenticated);
    expect(problem.correlationId).toMatch(/^[0-9a-f-]{36}$/);
    expect(JSON.stringify(problem)).not.toContain(token);
  }
});

it("exposes typed user-service capability and metadata without native side effects", async () => {
  const system = createClient(SystemService, transport);
  expect((await system.getStatus({})).capabilities).toContain(SystemCapability.USER_SERVICES_V1);
  const result = await system.getUserService({ kind: UserServiceKind.SERVER });
  expect(result.service).toMatchObject({ kind: UserServiceKind.SERVER, revision: 0n, state: UserServiceState.ABSENT, loginEnabled: false });
  expect(JSON.stringify(result, (_, value) => typeof value === "bigint" ? value.toString() : value)).not.toContain(token);
  await expect(system.getUserService({ kind: UserServiceKind.UNSPECIFIED })).rejects.toMatchObject({ code: 3 });
});
