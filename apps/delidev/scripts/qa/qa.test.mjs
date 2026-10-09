// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createClient } from "@connectrpc/connect";
import { request as httpRequest } from "node:http";
import { QaRun, workersArgument, app } from "./run.mjs";
import { Processes, until } from "./processes.mjs";
import { auditVault, cleanup, list } from "./cleanup.mjs";
import { document, Environment } from "./environment.mjs";
import { createHost } from "./host.mjs";

test("worker arguments have a closed 1..8 range", () => {
  assert.equal(workersArgument([]), 4); assert.equal(workersArgument(["--", "--workers", "8"]), 8);
  for (const args of [["--workers", "0"], ["--workers", "9"], ["--workers", "4.0"], ["--data-dir", "/tmp"]]) assert.throws(() => workersArgument(args));
});

test("host rejects foreign origins, hosts, paths and overlapping controls", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "delidev-qa-host-test-")));
  await writeFile(join(root, "index.html"), "test");
  let release, calls = 0;
  const pending = new Promise(resolve => { release = resolve; });
  const host = await createHost(root, { endpoint: "http://127.0.0.1:12345", bootstrap: () => ({ token: "synthetic-in-memory" }), status: () => ({}), startServer: async () => { calls++; await pending; return {}; } });
  const post = (origin, body = {}) => fetch(`${host.origin}/__qa/start`, { method: "POST", headers: { Origin: origin, "Content-Type": "application/json" }, body: JSON.stringify(body) });
  try {
    assert.equal((await post("http://127.0.0.1:1")).status, 403);
    const foreignHost = await new Promise(resolve => { const request = httpRequest(`${host.origin}/__qa/bootstrap`, { headers: { Host: "evil.example" } }, response => { response.resume(); resolve(response.statusCode); }); request.end(); });
    assert.equal(foreignHost, 403);
    assert.equal((await fetch(`${host.origin}/__qa/bootstrap?token=x`)).status, 503);
    assert.equal((await post(host.origin, { binary: "/arbitrary" })).status, 503);
    const first = post(host.origin); await until(() => calls === 1);
    assert.equal((await post(host.origin)).status, 409); release(); assert.equal((await first).status, 200);
    const bootstrap = await fetch(`${host.origin}/__qa/bootstrap`);
    assert.equal(bootstrap.headers.get("cache-control"), "no-store"); assert.equal(bootstrap.headers.get("access-control-allow-origin"), null);
  } finally { release(); await host.close(); await rm(root, { recursive: true }); }
});

test("tracked processes stop owned children on failure and signals", async () => {
  const processes = new Processes();
  const unrelated = new Processes(), other = unrelated.spawn(process.execPath, ["-e", "setInterval(()=>{},1000)"]);
  const child = processes.spawn(process.execPath, ["-e", "setInterval(()=>{},1000)"]);
  try {
    await assert.rejects(processes.run(process.execPath, ["-e", "process.exit(3)"]), /command-failed/);
    await assert.rejects(processes.run(process.execPath, ["-e", "setInterval(()=>{},1000)"], { timeout: 50 }), /command-timeout/);
    await processes.close(); assert.notEqual((await child.done).signal, null); assert.equal(other.child.exitCode, null);
  } finally { await processes.close(); await unrelated.close(); }
});

test("protected-reference audit preserves staged, malformed and foreign records", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "delidev-qa-vault-test-")));
  const owner = "01900000-0000-7000-8000-000000000001", reference = "01900000-0000-7000-8000-000000000002";
  await mkdir(join(root, owner)); const file = join(root, owner, `${reference}.json`);
  const save = value => writeFile(file, JSON.stringify(value), { mode: 0o600 });
  try {
    await save({ version: 1, scope: owner, state: "staged" }); await assert.rejects(auditVault(root, owner));
    await save({ version: 1, scope: reference, state: "deleted" }); await assert.rejects(auditVault(root, owner));
    await save({ version: 1, scope: owner, reference: { owner, id: reference, purpose: "account-api" }, state: "deleted" }); await auditVault(root, owner);
    await writeFile(file, "{"); await assert.rejects(auditVault(root, owner));
  } finally { await rm(root, { recursive: true }); }
});

test("real Go environments isolate RPC state, Workers, revocation and failures", { timeout: 300_000 }, async t => {
  const run = new QaRun({ workers: 2, log: () => {} });
  try {
    await run.start(); const [a, b] = run.environments, api = run.api;
    const transport = environment => api.createDeliDevTransport({ origin: environment.endpoint, getToken: () => environment.clientCredential.token });
    const configuration = environment => createClient(api.ConfigurationService, transport(environment));
    const repositories = new Map();
    for (const environment of [a, b]) {
      const path = join(environment.root, "git-fixture"); await mkdir(path);
      await run.processes.run("git", ["init", "--initial-branch=main", path]);
      await run.processes.run("git", ["-C", path, "-c", "user.name=QA", "-c", "user.email=qa@example.invalid", "commit", "--allow-empty", "-m", "QA fixture"]);
      await run.processes.run("git", ["-C", path, "remote", "add", "origin", "https://github.com/fixture/qa-repository.git"]);
      const response = await configuration(environment).saveConfiguration({ kind: api.EntityKind.REPOSITORY, schemaVersion: 1, mutation: { requestId: api.newRequestId() }, documentJson: new TextEncoder().encode(JSON.stringify({ name: "QA repository", remote_url: "https://github.com/fixture/qa-repository.git", checkouts: [{ machine_id: environment.workerCredential.machine_id, path }], preferred_remote: "", github_owner: "", github_name: "", integration_id: "" })) });
      assert(response.job);
      const repository = await until(async () => (await list(environment, api.EntityKind.REPOSITORY))[0]);
      repositories.set(environment, repository);
    }
    const definition = (environment, name) => ({ name, repositories: [repositories.get(environment).id], primary_repository: repositories.get(environment).id, agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } });
    const save = (environment, name, row) => {
      // Retain new server-owned project settings on revision-bound updates;
      // the first legacy-shaped creation still checks default compatibility.
      const value = { ...(row ? document(row) : {}), ...definition(environment, name) };
      return configuration(environment).saveConfiguration({ kind: api.EntityKind.PROJECT, schemaVersion: api.configurationSchemaVersion(api.EntityKind.PROJECT, value), mutation: { requestId: api.newRequestId(), id: row?.id ?? "", expectedRevision: row?.revision ?? 0n }, documentJson: new TextEncoder().encode(JSON.stringify(value)) });
    };
    await t.test("distinct origins, identities and same-name project writes", async () => {
      assert.notEqual(a.host.origin, b.host.origin); assert.notEqual(a.serverId, b.serverId); assert.notEqual(a.clientCredential.token, b.clientCredential.token); assert.notEqual(a.workerCredential.machine_id, b.workerCredential.machine_id);
      const [first, second] = await Promise.all([save(a, "Parallel project"), save(b, "Parallel project")]);
      await save(a, "Changed only A", first.resource);
      assert.equal(document((await list(b, api.EntityKind.PROJECT))[0]).name, "Parallel project");
      const updated = (await list(a, api.EntityKind.PROJECT))[0];
      await configuration(a).deleteConfiguration({ kind: api.EntityKind.PROJECT, mutation: { requestId: api.newRequestId(), id: updated.id, expectedRevision: updated.revision } });
      assert.equal((await list(a, api.EntityKind.PROJECT)).length, 0); assert.equal((await list(b, api.EntityKind.PROJECT))[0].id, second.resource.id);
      const cors = await fetch(`${a.endpoint}/delidev.v1.SystemService/GetStatus`, { method: "OPTIONS", headers: { Origin: b.host.origin, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type" } });
      assert.notEqual(cors.headers.get("access-control-allow-origin"), b.host.origin);
    });
    await t.test("actual Worker inspects a temporary Git repository and restarts explicitly", async () => {
      const repository = join(a.root, "git-fixture");
      const inspected = await a.cli(["repository", "inspect", "--machine-id", a.workerCredential.machine_id, "--path", repository, "--wait"]);
      assert(inspected);
      const original = await a.workerStatus();
      await a.controlWorker({ action: "stop", generation: original.generation });
      await until(async () => (await a.workerStatus()).state === "exited");
      assert.equal((await b.workerStatus()).state, "running");
      await a.controlWorker({ action: "start" }); const replacement = await a.workerStatus();
      assert.notEqual(replacement.generation, original.generation);
      await assert.rejects(a.controlWorker({ action: "stop", generation: original.generation }));
      assert.equal((await a.workerStatus()).generation, replacement.generation);
    });
    await t.test("client revocation and one server crash leave the other environment usable", async () => {
      const device = (await list(a, api.EntityKind.DEVICE)).find(row => row.id === a.clientCredential.device_id);
      await createClient(api.DeviceService, a.ownerTransport).revokeDevice({ mutation: { requestId: api.newRequestId(), id: device.id, expectedRevision: device.revision } });
      await assert.rejects(createClient(api.SystemService, transport(a)).getStatus({}));
      assert.equal((await createClient(api.SystemService, transport(b)).getStatus({})).serverId, b.serverId);
      await a.processes.stop(a.server); assert.equal((await b.status()).state, "ready");
    });
    await t.test("partial startup failure retains unknown authority and reaps its children", async () => {
      const failed = new Environment({ root: join(run.root, "failed-fixture"), assets: run.assets, binary: process.execPath, api, index: 3 });
      await assert.rejects(failed.prepare());
      const result = await cleanup(failed);
      assert.equal(result.state, "preserved"); assert(result.reasons.includes("server-cleanup-authority-unavailable"));
      assert.equal(failed.processes.children.size, 0);
      assert.equal((await b.status()).state, "ready");
      // This deliberately invalid executable never started a Go server or
      // created protected references. Remove only the test's synthetic fixture.
      await rm(failed.root, { recursive: true });
    });
    await t.test("failed protected cleanup preserves the original environment", async () => {
      const retained = new Environment({ root: join(run.root, "retained-fixture"), assets: run.assets, binary: run.binary, api, index: 3 });
      await retained.prepare();
      const owner = api.newRequestId(), reference = api.newRequestId(), directory = join(retained.serverRoot, "secrets", owner);
      await mkdir(directory, { mode: 0o700 });
      await writeFile(join(directory, `${reference}.json`), JSON.stringify({ version: 1, scope: retained.serverId, state: "staged" }), { mode: 0o600 });
      const result = await cleanup(retained);
      assert.equal(result.state, "preserved"); assert(result.reasons.includes("protected-reference-unconfirmed"));
      assert.equal(retained.processes.children.size, 0); await retained.ownsRoot();
      // Metadata-only fixture: no native key was staged by this test.
      await rm(retained.root, { recursive: true });
    });
  } finally {
    const result = await run.close();
    assert(result.environments.every(environment => environment.state === "deleted"), JSON.stringify(result));
    const record = await readFile(join(run.artifacts, "run.json"), "utf8");
    assert(!record.includes("token")); await assert.rejects(readFile(join(run.root, "worker-1", "qa-owner.json")));
    console.log(JSON.stringify({ operation: "qa-integration-validation", command: "pnpm test:qa", source: run.source, cleanup: result.environments, nativeAcceptance: "not-performed", accountAcceptance: "not-performed" }));
  }
});

test("runner SIGTERM after readiness and SIGINT during preparation settle cleanup", { timeout: 300_000 }, async () => {
  const processes = new Processes();
  try {
    for (const [signal, stage] of [["SIGTERM", "qa-environments"], ["SIGINT", "qa-build"]]) {
      const child = processes.spawn(process.execPath, [join(app, "scripts/qa/run.mjs"), "--workers", "1"]);
      await until(() => child.lines.find(value => value.operation === stage), 180_000);
      child.child.kill(signal); child.child.kill(signal);
      const result = await Promise.race([child.done, new Promise((_, reject) => { const timer = setTimeout(() => reject(new Error("signal cleanup timeout")), 60_000); timer.unref(); })]);
      assert.equal(result.code, 0);
      const record = child.lines.find(value => value.operation === "qa-cleanup");
      assert(record); assert(record.environments.every(environment => environment.state === "deleted"));
      const persisted = JSON.parse(await readFile(join(record.artifacts, "run.json"), "utf8"));
      assert.equal(persisted.operation, "qa-cleanup"); await processes.stop(child);
    }
  } finally { await processes.close(); }
});
