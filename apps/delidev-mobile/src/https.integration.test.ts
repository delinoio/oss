// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { beforeAll, afterAll, it, expect } from "vitest";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { promisify } from "node:util";
import { webcrypto, createHash } from "node:crypto";
import {
  createServer as httpsServer,
  request as httpsRequest,
  type Server,
} from "node:https";
import { request as httpRequest } from "node:http";
import { Readable } from "node:stream";
import { Code, createClient } from "@connectrpc/connect";
import { create, fromJson, fromJsonString } from "@bufbuild/protobuf";
import {
  createDeliDevTransport,
  DeviceService,
  DeviceType,
  SystemService,
  ResourceService,
  ConfigurationService,
  SessionService,
  EntityKind,
  CreateSessionRequestSchema,
  EnqueueInputRequestSchema,
  ControlSessionRequestSchema,
  SessionAction,
  SteerQueuedInputRequestSchema,
  RespondQuestionRequestSchema,
  SetInboxReadStateRequestSchema,
  SetNotificationPreferencesRequestSchema,
  InboxReadState,
  InboxService,
  ResourceSchema,
  NotificationState,
} from "@delinoio/delidev-api-client";
import { presentForeground } from "./notifications";
import { Connection, Status } from "./connection";
import {
  ProtectedState,
  Operation,
  uuid,
  documentBytes,
  documentOf,
} from "./state";
const run = promisify(execFile),
  delay = () => new Promise((r) => setTimeout(r, 20));
let directory: string, binary: string, ca: Buffer;
const children: ChildProcess[] = [],
  listeners: Server[] = [];
interface Fixture {
  origin: string;
  httpOrigin: string;
  token: string;
  serverId: string;
}
let first: Fixture,
  second: Fixture,
  nativeQuestion: Fixture,
  nativeSteer: Fixture,
  nativeInfo: any;
async function fixture(index: number): Promise<Fixture> {
  const scope = join(directory, `server-${index}`),
    child = spawn(
      binary,
      ["--data-dir", scope, "server", "run", "--listen", "127.0.0.1:0"],
      { stdio: "ignore" },
    );
  children.push(child);
  let httpOrigin = "",
    token = "";
  for (let i = 0; i < 750; i++) {
    if (child.exitCode !== null) throw new Error("Owned HTTPS fixture exited");
    try {
      httpOrigin = JSON.parse(
        await readFile(join(scope, "server.json"), "utf8"),
      ).url;
      token = JSON.parse(
        await readFile(join(scope, "owner.json"), "utf8"),
      ).token;
      if (httpOrigin && token) break;
    } catch {}
    await delay();
  }
  const origin = await proxyOrigin(httpOrigin);
  const serverId = (
    await createClient(
      SystemService,
      createDeliDevTransport({
        origin,
        getToken: () => token,
        fetch: trustedFetch,
      }),
    ).getStatus({})
  ).serverId;
  return { origin, httpOrigin, token, serverId };
}
async function proxyOrigin(httpOrigin: string): Promise<string> {
  const proxy = httpsServer(
    { key: await readFile(join(directory, "key.pem")), cert: ca },
    (request, response) => {
      const target = httpRequest(
        httpOrigin + request.url,
        { method: request.method, headers: request.headers },
        (upstream) => {
          response.writeHead(upstream.statusCode ?? 502, upstream.headers);
          upstream.pipe(response);
        },
      );
      target.on("error", () => {
        response.writeHead(502);
        response.end();
      });
      request.pipe(target);
      request.on("close", () => {
        if (request.aborted) target.destroy();
      });
    },
  );
  listeners.push(proxy);
  await new Promise<void>((r) => proxy.listen(0, "127.0.0.1", r));
  const address = proxy.address();
  if (!address || typeof address === "string")
    throw new Error("No HTTPS fixture address");
  return `https://127.0.0.1:${address.port}`;
}
const trustedFetch: typeof fetch = async (input, init) => {
  const request = new Request(input, init);
  const body = await request.arrayBuffer();
  return new Promise<Response>((resolve, reject) => {
    const outgoing = httpsRequest(
      request.url,
      {
        method: request.method,
        headers: Object.fromEntries(request.headers),
        ca,
        rejectUnauthorized: true,
      },
      (response) => {
        const headers = new Headers();
        for (const [key, value] of Object.entries(response.headers))
          if (value !== undefined)
            headers.set(key, Array.isArray(value) ? value.join(",") : value);
        resolve(
          new Response(Readable.toWeb(response) as ReadableStream<Uint8Array>, {
            status: response.statusCode,
            headers,
          }),
        );
      },
    );
    outgoing.on("error", reject);
    request.signal.addEventListener(
      "abort",
      () => outgoing.destroy(new Error("aborted")),
      { once: true },
    );
    outgoing.end(Buffer.from(body));
  });
};
const ownerTransport = (f: Fixture) =>
  createDeliDevTransport({
    origin: f.origin,
    getToken: () => f.token,
    fetch: trustedFetch,
  });
async function grant(f: Fixture, type = DeviceType.CLIENT) {
  const code = Buffer.from(
    webcrypto.getRandomValues(new Uint8Array(32)),
  ).toString("base64url");
  const result = await createClient(
    DeviceService,
    ownerTransport(f),
  ).createPairing({
    requestId: uuid(),
    name: "Owned mobile fixture",
    type,
    codeDigest: createHash("sha256").update(code).digest(),
  });
  return JSON.stringify({
    version: 1,
    server_id: f.serverId,
    pairing_id: result.pairing!.id,
    endpoint: f.origin,
    code,
  });
}
beforeAll(async () => {
  Object.defineProperty(globalThis, "crypto", {
    configurable: true,
    value: webcrypto,
  });
  directory = await mkdtemp(join(tmpdir(), "delidev-mobile-https-"));
  binary = process.env.DELIDEV_TEST_BINARY ?? join(directory, "delidev");
  if (!process.env.DELIDEV_TEST_BINARY)
    await run("go", ["build", "-o", binary, "./cmds/delidev-cli"], {
      cwd: resolve(process.cwd(), "../.."),
      timeout: 120000,
    });
  await run(
    "openssl",
    [
      "req",
      "-x509",
      "-newkey",
      "rsa:2048",
      "-nodes",
      "-keyout",
      join(directory, "key.pem"),
      "-out",
      join(directory, "cert.pem"),
      "-days",
      "1",
      "-subj",
      "/CN=localhost",
      "-addext",
      "subjectAltName=DNS:localhost,IP:127.0.0.1",
    ],
    { timeout: 30000 },
  );
  ca = await readFile(join(directory, "cert.pem"));
  first = await fixture(1);
  second = await fixture(2);
  const fixtureBinary =
    process.env.DELIDEV_MOBILE_FIXTURE_BINARY ??
    join(directory, "server-fixture");
  if (!process.env.DELIDEV_MOBILE_FIXTURE_BINARY)
    await run(
      "go",
      [
        "test",
        "-race",
        "-c",
        "-o",
        fixtureBinary,
        "./cmds/delidev-cli/internal/server",
      ],
      { cwd: resolve(process.cwd(), "../.."), timeout: 120000 },
    );
  const ready = join(directory, "fixture-private.json"),
    native = spawn(
      fixtureBinary,
      ["-test.run=^TestMobileHTTPSFixtureProcess$"],
      {
        env: { ...process.env, DELIDEV_MOBILE_FIXTURE_READY: ready },
        stdio: ["pipe", "ignore", "ignore"],
      },
    );
  children.push(native);
  for (let i = 0; i < 750; i++) {
    if (native.exitCode !== null)
      throw new Error("Owned interaction fixture exited");
    try {
      nativeInfo = JSON.parse(await readFile(ready, "utf8"));
      break;
    } catch {}
    await delay();
  }
  if (!nativeInfo)
    throw new Error("Owned interaction fixture did not become ready");
  nativeQuestion = {
    httpOrigin: nativeInfo.question.http_origin,
    origin: await proxyOrigin(nativeInfo.question.http_origin),
    serverId: nativeInfo.question.server_id,
    token: nativeInfo.question.token,
  };
  nativeSteer = {
    httpOrigin: nativeInfo.steer.http_origin,
    origin: await proxyOrigin(nativeInfo.steer.http_origin),
    serverId: nativeInfo.steer.server_id,
    token: nativeInfo.steer.token,
  };
}, 180000);
afterAll(async () => {
  for (const child of children) {
    if (child.exitCode === null) {
      const done = new Promise<void>((r) => child.once("exit", () => r()));
      if (child.stdin) {
        child.stdin.end();
        await Promise.race([done, new Promise((r) => setTimeout(r, 2000))]);
      }
      if (child.exitCode === null) child.kill("SIGTERM");
      await done;
    }
  }
  for (const server of listeners) {
    server.closeAllConnections();
    await new Promise<void>((r) => server.close(() => r()));
  }
  if (directory) await rm(directory, { recursive: true, force: true });
}, 15000);
it("pairs independent HTTPS profiles; lost acknowledgment replays original client without a second grant or device", async () => {
  let raw: string | null = null,
    lost = true;
  const seen: string[] = [];
  const state = new ProtectedState(
    {
      read: async () => raw,
      write: async (v) => {
        raw = v;
      },
    },
    async (input, init) => {
      const req = new Request(input, init);
      if (req.url.endsWith("/PairDevice")) {
        seen.push(Buffer.from(await req.clone().arrayBuffer()).toString("hex"));
        const response = await trustedFetch(req);
        if (lost) {
          lost = false;
          await response.arrayBuffer();
          throw new Error("lost acknowledgment");
        }
        return response;
      }
      return trustedFetch(req);
    },
  );
  const id = await state.preparePair("One", first.origin, await grant(first));
  const original = state.profile(id);
  await expect(state.pair(id)).rejects.toThrow();
  expect(state.profile(id)).toEqual(original);
  const reloaded = new ProtectedState(
    {
      read: async () => raw,
      write: async (v) => {
        raw = v;
      },
    },
    trustedFetch,
  );
  await reloaded.load();
  await reloaded.pair(id);
  expect(reloaded.profile(id).pairing).toBeUndefined();
  expect(reloaded.profile(id).token).toBe(original.token);
  const resources = createClient(ResourceService, ownerTransport(first));
  expect(
    (await resources.listResources({ filter: { kind: EntityKind.DEVICE } }))
      .resources,
  ).toHaveLength(1);
  expect(
    (await resources.listResources({ filter: { kind: EntityKind.PAIRING } }))
      .resources,
  ).toHaveLength(1);
  expect(
    (await resources.listResources({ filter: { kind: EntityKind.MACHINE } }))
      .resources,
  ).toHaveLength(0);
  const other = await reloaded.preparePair(
    "Two",
    second.origin,
    await grant(second),
  );
  await reloaded.pair(other);
  await reloaded.select(other);
  expect(
    (await createClient(SystemService, reloaded.transport(other)).getStatus({}))
      .serverId,
  ).toBe(second.serverId);
  await reloaded.select(id);
  expect(
    (await createClient(SystemService, reloaded.transport(id)).getStatus({}))
      .serverId,
  ).toBe(first.serverId);
  const device = (
    await resources.getResource({
      kind: EntityKind.DEVICE,
      id: original.deviceId,
    })
  ).resource!;
  await createClient(DeviceService, ownerTransport(first)).revokeDevice({
    mutation: {
      id: device.id,
      expectedRevision: device.revision,
      requestId: uuid(),
    },
  });
  await expect(
    createClient(SystemService, reloaded.transport(id)).getStatus({}),
  ).rejects.toThrow();
  expect(
    (await createClient(SystemService, reloaded.transport(other)).getStatus({}))
      .serverId,
  ).toBe(second.serverId);
}, 30000);
it("rejects untrusted certificates and mismatched HTTPS grant origin without acquiring credentials", async () => {
  let raw: string | null = null;
  const state = new ProtectedState({
    read: async () => raw,
    write: async (v) => {
      raw = v;
    },
  });
  const issued = await grant(second);
  await expect(
    state.preparePair("Wrong", first.origin, issued),
  ).rejects.toThrow();
  expect(raw).toBeNull();
  const id = await state.preparePair("Untrusted", second.origin, issued);
  await expect(state.pair(id)).rejects.toThrow();
  expect(state.profile(id).pairing).toBeTruthy();
}, 10000);
it("creates Worktree and General Chat and exactly replays Create/Send after acknowledgment loss", async () => {
  const transport = ownerTransport(second),
    devices = createClient(DeviceService, transport),
    config = createClient(ConfigurationService, transport);
  const workerGrant = JSON.parse(await grant(second, DeviceType.WORKER)),
    machineId = uuid(),
    workerToken = Buffer.from(
      webcrypto.getRandomValues(new Uint8Array(32)),
    ).toString("base64url");
  const worker = await devices.pairDevice({
    requestId: uuid(),
    pairingId: workerGrant.pairing_id,
    code: workerGrant.code,
    deviceId: uuid(),
    credentialDigest: createHash("sha256").update(workerToken).digest(),
    machineId,
    machineJson: documentBytes({
      name: "Remote fixture",
      os: "darwin",
      architecture: "arm64",
      version: "0.1.0",
      installations: [],
      worker_capabilities: ["remote-workspace-clone-v1"],
      last_seen: new Date().toISOString(),
      disabled: false,
    }),
  });
  expect(worker.machine?.id).toBe(machineId);
  const save = async (kind: EntityKind, data: unknown, schemaVersion = 1) =>
    (
      await config.saveConfiguration({
        kind,
        schemaVersion,
        mutation: { requestId: uuid() },
        documentJson: documentBytes(data),
      })
    ).resource!;
  const provider = await save(EntityKind.PROVIDER, {
    name: "Owned keyless",
    endpoint: "http://127.0.0.1:9/v1",
    protocol: "openai-chat",
    authentication: "keyless",
    discovery: false,
  });
  const model = await save(EntityKind.MODEL, {
    name: "Fixture model",
    provider_id: provider.id,
    native_id: "fixture",
    harnesses: ["codex"],
    manual: true,
    metadata_source: "unknown",
  });
  const agent = await save(EntityKind.AGENT, {
    name: "Owned accountless Agent",
    harness: "codex",
    model_id: model.id,
    accounts: [],
    templates: [],
    options: { permission: "default" },
  });
  const repository = await save(EntityKind.REPOSITORY, {
    name: "Remote fixture",
    remote_url: "git@github.com:fixture/mobile.git",
    checkouts: [],
    base: {},
    starting: {},
    auto_fetch: false,
  });
  const project = await save(EntityKind.PROJECT, {
    name: "Owned project",
    repositories: [repository.id],
    primary_repository: repository.id,
    agents: { configured: false, ids: [] },
    accounts: { configured: false, ids: [] },
  });
  let raw: string | null = null,
    lostMethod = "CreateSession";
  const bodies: Record<string, string[]> = {};
  const state = new ProtectedState(
    {
      read: async () => raw,
      write: async (v) => {
        raw = v;
      },
    },
    async (input, init) => {
      const request = new Request(input, init),
        method = request.url.split("/").at(-1)!;
      if (
        ["CreateSession", "EnqueueInput", "ControlSession"].includes(method)
      ) {
        (bodies[method] ??= []).push(
          Buffer.from(await request.clone().arrayBuffer()).toString("hex"),
        );
        const response = await trustedFetch(request);
        if (lostMethod === method && response.status === 200) {
          lostMethod = "";
          await response.arrayBuffer();
          throw new Error("lost acknowledgment");
        }
        return response;
      }
      return trustedFetch(request);
    },
  );
  const id = await state.preparePair(
    "Mutations",
    second.origin,
    await grant(second),
  );
  await state.pair(id);
  const request = create(CreateSessionRequestSchema, {
    requestId: uuid(),
    documentJson: documentBytes({
      name: "Remote Worktree",
      agent_id: agent.id,
      machine_id: machineId,
      project_id: project.id,
      workspace: "worktree",
      prompt: "No native inference",
      mode: "execute",
      source: "MANUAL",
    }),
  });
  await expect(
    state.perform(id, Operation.Create, request, ""),
  ).rejects.toThrow();
  const pending = state.profile(id).pending!;
  expect(pending.request).toContain(request.requestId);
  await state.retry(id);
  expect(bodies.CreateSession).toHaveLength(2);
  expect(bodies.CreateSession![0]).toBe(bodies.CreateSession![1]);
  const sessions = createClient(SessionService, state.transport(id));
  const first = (await sessions.listSessions({ pageSize: 50 })).sessions.find(
    (r) => documentOf(r.documentJson).name === "Remote Worktree",
  )!;
  expect(first).toBeTruthy();
  await state.perform(
    id,
    Operation.Create,
    create(CreateSessionRequestSchema, {
      requestId: uuid(),
      documentJson: documentBytes({
        name: "General",
        agent_id: agent.id,
        machine_id: machineId,
        workspace: "general-chat",
        prompt: "No native inference",
        mode: "execute",
        source: "MANUAL",
      }),
    }),
    "",
  );
  expect(
    (await sessions.listSessions({ pageSize: 50 })).sessions.some(
      (r) => documentOf(r.documentJson).workspace === "general-chat",
    ),
  ).toBe(true);
  lostMethod = "EnqueueInput";
  await expect(
    state.perform(
      id,
      Operation.Send,
      create(EnqueueInputRequestSchema, {
        requestId: uuid(),
        sessionId: first.id,
        documentJson: documentBytes({ prompt: "Queued once", mode: "execute" }),
      }),
      first.id,
    ),
  ).rejects.toThrow();
  await state.retry(id);
  expect(bodies.EnqueueInput![0]).toBe(bodies.EnqueueInput![1]);
  expect(
    (
      await sessions.listQueue({ sessionId: first.id, pageSize: 50 })
    ).inputs.filter((r) => documentOf(r.documentJson).prompt === "Queued once"),
  ).toHaveLength(1);
  await expect(
    sessions.controlSession({
      mutation: {
        id: first.id,
        expectedRevision: first.revision,
        requestId: uuid(),
      },
      action: SessionAction.RESUME,
    }),
  ).rejects.toThrow();
  const resources = createClient(ResourceService, state.transport(id));
  const current = (
    await resources.getResource({ kind: EntityKind.SESSION, id: first.id })
  ).resource!;
  lostMethod = "ControlSession";
  await expect(
    state.perform(
      id,
      Operation.Control,
      create(ControlSessionRequestSchema, {
        mutation: {
          id: first.id,
          expectedRevision: current.revision,
          requestId: uuid(),
        },
        action: SessionAction.STOP,
      }),
      first.id,
    ),
  ).rejects.toThrow();
  await state.retry(id);
  expect(bodies.ControlSession!.at(-2)).toBe(bodies.ControlSession!.at(-1));
  const stopped = (
    await resources.getResource({ kind: EntityKind.SESSION, id: first.id })
  ).resource!;
  expect(documentOf(stopped.documentJson).dispatch).toBe("paused");
  // A missing prepared workspace is an authoritative Resume blocker. Retain
  // the original uncertain request instead of starting replacement work.
  await expect(
    state.perform(
      id,
      Operation.Control,
      create(ControlSessionRequestSchema, {
        mutation: {
          id: first.id,
          expectedRevision: stopped.revision,
          requestId: uuid(),
        },
        action: SessionAction.RESUME,
      }),
      first.id,
    ),
  ).rejects.toThrow();
  expect(state.profile(id).pending?.operation).toBe(Operation.Control);
}, 30000);

it("expired pairing retains its exact original credential and cannot start or replace remote work", async () => {
  let raw: string | null = null;
  const state = new ProtectedState(
    {
      read: async () => raw,
      write: async (v) => {
        raw = v;
      },
    },
    trustedFetch,
  );
  const id = await state.preparePair(
    "Expired",
    nativeQuestion.origin,
    JSON.stringify({
      version: 1,
      server_id: nativeQuestion.serverId,
      pairing_id: nativeInfo.question.expired_pairing_id,
      endpoint: nativeQuestion.origin,
      code: nativeInfo.question.expired_code,
    }),
  );
  const original = state.profile(id);
  await expect(state.pair(id)).rejects.toThrow();
  expect(state.profile(id)).toEqual(original);
});
it("denied foreground OS submission has an independent receipt and leaves the original Inbox unread", async () => {
  let raw: string | null = null,
    calls = 0;
  const state = new ProtectedState(
      {
        read: async () => raw,
        write: async (v) => {
          raw = v;
        },
      },
      trustedFetch,
    ),
    id = await state.preparePair(
      "Notifications",
      nativeQuestion.origin,
      await grant(nativeQuestion),
    );
  await state.pair(id);
  await state.update((s) => {
    s.notifications = true;
  });
  const inbox = createClient(InboxService, state.transport(id)),
    candidates = await inbox.listNotificationCandidates({ limit: 1 });
  expect(candidates.candidates).toHaveLength(1);
  const original = candidates.candidates[0]!;
  await presentForeground(state, id, () => true, {
    notify: async () => {
      calls++;
      return false;
    },
  });
  const delivery = await inbox.getNotificationDelivery({
    inboxId: original.inboxId,
  });
  expect(delivery.delivery?.state).toBe(NotificationState.DENIED);
  expect(
    documentOf(
      (await inbox.getInboxEntry({ id: original.inboxId })).view!.entry!
        .documentJson,
    ).read_state,
  ).toBe("unread");
  await presentForeground(state, id, () => true, {
    notify: async () => {
      calls++;
      return true;
    },
  });
  expect(calls).toBe(1);
});
it("original Steer and question responses replay one accepted operation after acknowledgment loss; stale responses fail", async () => {
  for (const [fixture, operation, method] of [
    [nativeQuestion, Operation.Question, "RespondQuestion"],
    [nativeSteer, Operation.Steer, "SteerQueuedInput"],
  ] as const) {
    let raw: string | null = null,
      lost = true;
    const bodies: string[] = [];
    const state = new ProtectedState(
      {
        read: async () => raw,
        write: async (v) => {
          raw = v;
        },
      },
      async (input, init) => {
        const request = new Request(input, init);
        if (request.url.endsWith("/" + method)) {
          bodies.push(
            Buffer.from(await request.clone().arrayBuffer()).toString("hex"),
          );
          const response = await trustedFetch(request);
          if (lost && response.status === 200) {
            lost = false;
            await response.arrayBuffer();
            throw new Error("lost acknowledged response");
          }
          return response;
        }
        return trustedFetch(request);
      },
    );
    const id = await state.preparePair(
      "Original native metadata",
      fixture.origin,
      await grant(fixture),
    );
    await state.pair(id);
    const target =
      operation === Operation.Question
        ? nativeInfo.question.interaction_id
        : nativeInfo.steer.session_id;
    const request =
      operation === Operation.Question
        ? create(RespondQuestionRequestSchema, {
            mutation: { id: target, expectedRevision: 1n, requestId: uuid() },
            responseJson: documentBytes({
              answers: { choice: ["First"], optional: [] },
            }),
          })
        : fromJson(SteerQueuedInputRequestSchema, nativeInfo.steer.request);
    if (operation === Operation.Question) {
      const bad = createClient(ResourceService, state.transport(id));
      const observed = await bad.getResource({
        kind: EntityKind.INTERACTION,
        id: target,
      });
      expect(observed.resource?.revision).toBe(1n);
    }
    await expect(
      state.perform(id, operation, request, target),
    ).rejects.toThrow();
    const pending = state.profile(id).pending;
    const reloaded = new ProtectedState(
      {
        read: async () => raw,
        write: async (v) => {
          raw = v;
        },
      },
      trustedFetch,
    );
    await reloaded.load();
    expect(reloaded.profile(id).pending).toEqual(pending);
    await reloaded.retry(id);
    expect(bodies).toHaveLength(1);
    // The same original request can be read-only replayed again by the server;
    // a new stale mutation identity cannot adopt the accepted result.
    const result = await state.retry(id);
    expect(result).toMatchObject({ replayed: true });
    expect(bodies).toHaveLength(2);
    expect(bodies[0]).toBe(bodies[1]);
    expect(state.profile(id).pending).toBeUndefined();
    if (operation === Operation.Question) {
      const inbox = createClient(InboxService, state.transport(id)),
        list = await inbox.listInbox({ pageSize: 50 });
      const view = list.entries.find((v) => v.interaction?.id === target)!;
      expect(view.interaction?.revision).toBe(2n);
      expect(documentOf(view.entry!.documentJson).read_state).toBe("unread");
      await state.perform(
        id,
        Operation.Read,
        create(SetInboxReadStateRequestSchema, {
          mutation: {
            id: view.entry!.id,
            expectedRevision: view.entry!.revision,
            requestId: uuid(),
          },
          readState: InboxReadState.READ,
        }),
        view.entry!.id,
      );
      expect(
        documentOf(
          (await inbox.getInboxEntry({ id: view.entry!.id })).view!.entry!
            .documentJson,
        ).read_state,
      ).toBe("read");
    }
  }
}, 30000);
it("foreground synchronization fences old streams, resnapshots and never retries protected mutations", async () => {
  let raw: string | null = null,
    sends = 0;
  const state = new ProtectedState(
    {
      read: async () => raw,
      write: async (v) => {
        raw = v;
      },
    },
    async (input, init) => {
      const request = new Request(input, init);
      if (request.url.endsWith("/EnqueueInput")) {
        sends++;
        throw new Error("unresolved send");
      }
      return trustedFetch(request);
    },
  );
  const id = await state.preparePair(
    "Sync",
    nativeSteer.origin,
    await grant(nativeSteer),
  );
  await state.pair(id);
  await expect(
    state.perform(
      id,
      Operation.Send,
      create(EnqueueInputRequestSchema, {
        requestId: uuid(),
        sessionId: nativeInfo.steer.session_id,
        documentJson: documentBytes({
          prompt: "Retained original",
          mode: "execute",
        }),
      }),
      nativeInfo.steer.session_id,
    ),
  ).rejects.toThrow();
  const observed: Status[] = [],
    connection = new Connection(state, id, (s) => observed.push(s));
  void connection.resume();
  for (let i = 0; i < 100 && !observed.includes(Status.Live); i++)
    await delay();
  expect(observed).toContain(Status.Live);
  connection.suspend();
  expect(connection.canMutate()).toBe(false);
  void connection.resume();
  for (let i = 0; i < 100 && connection.status !== Status.Live; i++)
    await delay();
  expect(connection.status).toBe(Status.Live);
  expect(sends).toBe(1);
  expect(connection.canMutate()).toBe(false);
  connection.suspend();
}, 15000);

it("retains granular false presence and the original revision through HTTPS acknowledgment loss, reload and exact retry", async () => {
  let raw: string | null = null, lost = true;
  const bodies: string[] = [];
  const storage = { read: async () => raw, write: async (value: string) => { raw = value; } };
  const fetcher: typeof fetch = async (input, init) => {
    const request = new Request(input, init);
    if (!request.url.endsWith("/SetNotificationPreferences")) return trustedFetch(request);
    bodies.push(Buffer.from(await request.clone().arrayBuffer()).toString("hex"));
    const response = await trustedFetch(request);
    if (lost && response.status === 200) {
      lost = false;
      await response.arrayBuffer();
      throw new Error("lost preference acknowledgment");
    }
    return response;
  };
  const state = new ProtectedState(storage, fetcher);
  // A fresh client grant avoids adopting any shared interaction/Inbox fixture.
  const id = await state.preparePair("Granular preferences", first.origin, await grant(first));
  await state.pair(id);
  const inbox = createClient(InboxService, state.transport(id));
  const original = (await inbox.getNotificationPreferences({ situations: true })).preferences!;
  expect(original.situations).toBeDefined();
  const request = create(SetNotificationPreferencesRequestSchema, {
    requestId: uuid(), expectedRevision: original.revision, changes: { questions: false },
  });
  await expect(state.perform(id, Operation.Preferences, request, "")).rejects.toThrow();
  const pending = state.profile(id).pending!;
  const retained = fromJsonString(SetNotificationPreferencesRequestSchema, pending.request);
  expect(retained).toMatchObject({ requestId: request.requestId, expectedRevision: original.revision, changes: { questions: false } });
  expect(retained.preferences).toBeUndefined();
  expect(retained.changes?.approvals).toBeUndefined();
  // Subsequent draft edits cannot change the durably captured false selection.
  request.changes!.questions = true;
  const reloaded = new ProtectedState(storage, fetcher);
  await reloaded.load();
  expect(reloaded.profile(id).pending).toEqual(pending);
  const current = (await inbox.getNotificationPreferences({ situations: true })).preferences!;
  expect(current.revision).toBe(original.revision + 1n);
  expect(current.situations).toEqual({ ...original.situations, questions: false });
  expect(reloaded.profile(id).pending).toEqual(pending);
  // Another explicit server-side change advances the snapshot. Receipt replay
  // must preserve it rather than replacing preferences with the old snapshot.
  const concurrent = await createClient(InboxService, createDeliDevTransport({ origin: first.origin, getToken: () => state.profile(id).token, fetch: trustedFetch })).setNotificationPreferences({
    requestId: uuid(), expectedRevision: current.revision,
    changes: { approvals: !current.situations!.approvals },
  });
  expect(concurrent.preferences?.revision).toBe(current.revision + 1n);
  const replay = await reloaded.retry(id);
  expect(replay).toMatchObject({ requestId: retained.requestId, replayed: true, preferences: concurrent.preferences });
  expect(bodies).toHaveLength(2);
  expect(bodies[0]).toBe(bodies[1]);
  expect(reloaded.profile(id).pending).toBeUndefined();
  const stale = create(SetNotificationPreferencesRequestSchema, {
    requestId: uuid(), expectedRevision: original.revision, changes: { questions: true },
  });
  await expect(reloaded.perform(id, Operation.Preferences, stale, "")).rejects.toMatchObject({ code: Code.Aborted });
  expect(reloaded.profile(id).pending?.request).toContain(stale.requestId);
  expect((await inbox.getNotificationPreferences({ situations: true })).preferences).toEqual(concurrent.preferences);
}, 30000);
