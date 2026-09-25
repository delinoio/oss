import test from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import {
  CredentialReader,
  CredentialSource,
  FIGMA_ENDPOINT,
} from "../src/figma/credentials.js";
import {
  OfficialFigmaConnection,
  CallSafety,
  RemoteError,
  retryAfter,
  ratePolicy,
  type SchedulerClock,
  withFigmaFile,
} from "../src/figma/transport.js";
import { ErrorCode } from "../src/types.js";
const schemas = ["whoami", "create_new_file", "use_figma", "upload_assets"].map(
  (name) => ({
    name,
    inputSchema: {
      type: "object",
      properties:
        name === "use_figma"
          ? { code: { type: "string", maxLength: 49000 } }
          : {},
    },
  }),
);
function fixture(token = `SYNTHETIC-${randomUUID()}`) {
  let time = 0;
  const sleeps: number[] = [];
  const authorizations: string[] = [];
  const calls: string[] = [];
  let reads = 0;
  const clock: SchedulerClock = {
    now: () => time,
    random: () => 0.5,
    sleep: async (ms) => {
      sleeps.push(ms);
      time += ms;
    },
  };
  const reader = new CredentialReader(
    { source: CredentialSource.Codex },
    async () => {
      reads++;
      return JSON.stringify({
        server_name: "figma",
        url: FIGMA_ENDPOINT,
        token_response: { access_token: token },
        expires_at: Date.now() + 3600000,
      });
    },
  );
  const failures: Array<Response | Error | object> = [];
  const fetcher: typeof fetch = async (input, init) => {
    assert.equal(String(input), FIGMA_ENDPOINT);
    authorizations.push(new Headers(init?.headers).get("Authorization")!);
    assert.equal(init?.redirect, "error");
    if (init?.method !== "POST") return new Response(null, { status: 405 });
    const body = JSON.parse(String(init.body));
    if (body.id === undefined) return new Response(null, { status: 202 });
    const response = (result: unknown) =>
      new Response(JSON.stringify({ jsonrpc: "2.0", id: body.id, result }), {
        headers: { "Content-Type": "application/json" },
      });
    if (body.method === "initialize")
      return response({
        protocolVersion: "2025-03-26",
        capabilities: { tools: {} },
        serverInfo: { name: "fixture", version: "1" },
      });
    if (body.method === "tools/list") return response({ tools: schemas });
    assert.equal(body.method, "tools/call");
    calls.push(body.params.name);
    const next = failures.shift();
    if (next instanceof Response) return next;
    if (next instanceof Error) throw next;
    return response(
      next ?? {
        content: [
          {
            type: "text",
            text: JSON.stringify(
              body.params.name === "whoami"
                ? { plans: [{ key: "team::1", tier: "pro", seat: "Full" }] }
                : { ok: true },
            ),
          },
        ],
      },
    );
  };
  return {
    connection: new OfficialFigmaConnection(reader, fetcher, clock, "team::1"),
    failures,
    sleeps,
    calls,
    authorizations,
    get reads() {
      return reads;
    },
    clock,
  };
}
test("official SDK negotiates capabilities and bounds code before any write", async () => {
  const f = fixture();
  try {
    await f.connection.connect();
    assert.equal(f.connection.codeLimit, 49000);
    await assert.rejects(
      f.connection.call(
        "use_figma",
        { code: "x".repeat(49001) },
        CallSafety.Write,
      ),
      (e) => e instanceof RemoteError && e.code === ErrorCode.ResourceLimit,
    );
    assert.equal(f.calls.length, 0);
    await f.connection.call("whoami", {}, CallSafety.Read);
    assert.equal(f.connection.stats.calls, 1);
    assert.ok(f.authorizations.every((v) => v.startsWith("Bearer SYNTHETIC-")));
  } finally {
    await f.connection.close();
  }
});
test("Retry-After precedes jittered backoff and shared admission; long quotas stop immediately", async () => {
  const f = fixture();
  try {
    f.failures.push(
      new Response("fixture", { status: 429, headers: { "Retry-After": "8" } }),
    );
    await f.connection.call("use_figma", { code: "return 1" }, CallSafety.Read);
    assert.equal(f.connection.stats.retries, 1);
    assert.equal(f.sleeps[0], 8000);
    f.failures.push(
      new Response("fixture", {
        status: 429,
        headers: { "Retry-After": "86400" },
      }),
    );
    await assert.rejects(
      f.connection.call("use_figma", { code: "return 1" }, CallSafety.Read),
      (e) => e instanceof RemoteError && e.code === ErrorCode.QuotaExceeded,
    );
    const calls = f.calls.length;
    await assert.rejects(
      f.connection.call("use_figma", { code: "return 1" }, CallSafety.Read),
    );
    assert.equal(f.calls.length, calls);
  } finally {
    await f.connection.close();
  }
});
test("three transient read retries; ambiguous writes are never replayed", async () => {
  const f = fixture();
  try {
    f.failures.push(
      ...Array.from(
        { length: 4 },
        () => new Response("fixture", { status: 503 }),
      ),
    );
    await assert.rejects(
      f.connection.call("use_figma", { code: "return 1" }, CallSafety.Read),
    );
    assert.equal(f.calls.length, 4);
    assert.equal(f.connection.stats.retries, 3);
    f.failures.push(new Response("fixture", { status: 503 }));
    await assert.rejects(
      f.connection.call("create_new_file", {}, CallSafety.Write),
      (e) => e instanceof RemoteError && !e.safeToRetry,
    );
    assert.equal(f.calls.length, 5);
    f.failures.push({
      isError: true,
      content: [
        {
          type: "text",
          text: JSON.stringify({
            safeToRetryWithoutCanvasRead: true,
            message: "temporarily unavailable",
          }),
        },
      ],
    });
    await f.connection.call(
      "use_figma",
      { code: "return 1" },
      CallSafety.Write,
    );
    assert.equal(f.calls.length, 7);
  } finally {
    await f.connection.close();
  }
});
test("401 rereads selected credentials once and errors never expose server secrets", async () => {
  const f = fixture();
  try {
    f.failures.push(
      new Response("SECRET-FROM-SERVER", { status: 401 }),
      new Response("SECRET-FROM-SERVER", { status: 401 }),
    );
    await assert.rejects(
      f.connection.call("whoami", {}, CallSafety.Read),
      (e) =>
        e instanceof RemoteError &&
        e.code === ErrorCode.Authentication &&
        !JSON.stringify(e).includes("SECRET"),
    );
    assert.equal(f.reads, 2);
    assert.equal(f.connection.stats.retries, 0);
    const calls = f.calls.length;
    const requests = f.authorizations.length;
    for (let i = 0; i < 2; i++) {
      await assert.rejects(f.connection.connect(), (e) => e.code === ErrorCode.Authentication);
      await assert.rejects(f.connection.call("whoami", {}, CallSafety.Read), (e) => e.code === ErrorCode.Authentication);
    }
    assert.equal(f.reads, 2);
    assert.equal(f.calls.length, calls);
    assert.equal(f.authorizations.length, requests);
  } finally {
    await f.connection.close();
  }
});
test("successful credential recovery does not replenish the session allowance", async () => {
  const f = fixture();
  try {
    f.failures.push(new Response("fixture", { status: 401 }));
    await f.connection.call("whoami", {}, CallSafety.Read);
    assert.equal(f.reads, 2);
    f.failures.push(new Response("fixture", { status: 401 }));
    const calls = f.calls.length;
    await assert.rejects(f.connection.call("whoami", {}, CallSafety.Read), (e) => e.code === ErrorCode.Authentication);
    assert.equal(f.calls.length, calls + 1);
    assert.equal(f.reads, 2);
    assert.equal(f.connection.stats.retries, 0);
  } finally { await f.connection.close(); }
});
test("permanent HTTP failures do not spend retry budget even for reads", async () => {
  const f = fixture();
  try {
    for (const [status, code] of [
      [400, ErrorCode.MalformedInput],
      [403, ErrorCode.PermissionDenied],
      [404, ErrorCode.InvalidTarget],
      [409, ErrorCode.Conflict],
      [413, ErrorCode.ResourceLimit],
    ] as const) {
      f.failures.push(new Response("SECRET-FROM-SERVER", { status }));
      const before = f.calls.length;
      await assert.rejects(
        f.connection.call("use_figma", { code: "return 1" }, CallSafety.Read),
        (e) => e instanceof RemoteError && e.code === code && !JSON.stringify(e).includes("SECRET"),
      );
      assert.equal(f.calls.length, before + 1);
    }
    assert.equal(f.connection.stats.retries, 0);
    assert.equal(f.reads, 1);
  } finally {
    await f.connection.close();
  }
});
test("sessions using the same authentication share their request budget", async () => {
  const token = `SYNTHETIC-${randomUUID()}`;
  const a = fixture(token),
    b = fixture(token);
  try {
    await a.connection.call("use_figma", { code: "return 1" }, CallSafety.Read);
    await b.connection.call("use_figma", { code: "return 2" }, CallSafety.Read);
    assert.deepEqual(b.sleeps, [6000]);
  } finally {
    await a.connection.close();
    await b.connection.close();
  }
});
test("file write queue preserves ordering even when a queued caller cancels", async () => {
  const events: number[] = [];
  let release!: () => void;
  const blocker = new Promise<void>((r) => {
    release = r;
  });
  const c = new AbortController();
  const a = withFigmaFile("file", new AbortController().signal, async () => {
    events.push(1);
    await blocker;
    events.push(2);
  });
  const b = withFigmaFile("file", c.signal, async () => {
    events.push(99);
  });
  c.abort();
  await assert.rejects(b);
  const d = withFigmaFile("file", new AbortController().signal, async () => {
    events.push(3);
  });
  release();
  await Promise.all([a, d]);
  assert.deepEqual(events, [1, 2, 3]);
});
test("documented plan policies and Retry-After parsing stay explicit", () => {
  assert.deepEqual(ratePolicy("enterprise", "Full"), {
    interval: 3000,
    quota: 600,
    windowMs: 86400000,
  });
  assert.equal(ratePolicy("organization", "Dev").interval, 4000);
  assert.equal(ratePolicy("pro", "Full").quota, 200);
  assert.equal(ratePolicy("pro", "View").quota, 6);
  assert.equal(ratePolicy("starter", "Full").quota, 20);
  assert.equal(retryAfter("2"), 2000);
  assert.equal(retryAfter("Thu, 01 Jan 1970 00:00:04 GMT", 1000), 3000);
  assert.equal(retryAfter("bad"), undefined);
});
