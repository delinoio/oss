// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { create, toJsonString } from "@bufbuild/protobuf";
import {
  CreateSessionRequestSchema,
  EnqueueInputRequestSchema,
  SteerQueuedInputRequestSchema,
  RespondQuestionRequestSchema,
  EntityKind,
} from "@delinoio/delidev-api-client";
import {
  ProtectedState,
  Operation,
  uuid,
  httpsOrigin,
  documentBytes,
} from "./state";
let raw: string | null;
beforeEach(() => {
  raw = null;
  vi.stubGlobal("crypto", webcrypto);
});
const store = {
  read: async () => raw,
  write: async (value: string) => {
    raw = value;
  },
};
const profile = () => ({
  id: uuid(),
  serverId: uuid(),
  deviceId: uuid(),
  name: "Owned",
  origin: "https://example.test",
  token: "a".repeat(43),
});
it("requires explicit HTTPS and rejects origins with credentials or paths", () => {
  for (const origin of [
    "http://127.0.0.1:123",
    "https://token@example.test",
    "https://example.test/path",
    "https://example.test/?key=secret",
  ])
    expect(() => httpsOrigin(origin)).toThrow();
  expect(httpsOrigin("https://example.test")).toBe("https://example.test");
});
it("stores original pairing before network I/O and reload preserves credential and request identities", async () => {
  let calls = 0;
  const state = new ProtectedState(store, async () => {
    calls++;
    throw new Error("lost");
  });
  const server = uuid(),
    pairing = uuid();
  const id = await state.preparePair(
    "Owned",
    "https://example.test",
    JSON.stringify({
      version: 1,
      server_id: server,
      pairing_id: pairing,
      endpoint: "https://example.test",
      code: "x".repeat(43),
    }),
  );
  const original = state.profile(id);
  expect(calls).toBe(0);
  await expect(state.pair(id)).rejects.toThrow();
  const resumed = new ProtectedState(store, async () => {
    calls++;
    throw new Error("lost");
  });
  await resumed.load();
  expect(resumed.profile(id)).toEqual(original);
  await expect(resumed.pair(id)).rejects.toThrow();
  expect(resumed.profile(id).pairing).toEqual(original.pairing);
  expect(calls).toBe(2);
});
for (const operation of [
  Operation.Create,
  Operation.Send,
  Operation.Steer,
  Operation.Question,
])
  it(`durably retains exact ${operation} protobuf and blocks replacement after response loss`, async () => {
    let calls = 0;
    const state = new ProtectedState(store, async () => {
        calls++;
        throw new Error("lost");
      }),
      p = profile();
    await state.update((s) => {
      s.profiles.push(p);
      s.selectedProfile = p.id;
    });
    const requestId = uuid(),
      session = uuid();
    const request =
      operation === Operation.Create
        ? create(CreateSessionRequestSchema, {
            requestId,
            documentJson: documentBytes({ name: "Owned", prompt: "draft" }),
          })
        : operation === Operation.Send
          ? create(EnqueueInputRequestSchema, {
              requestId,
              sessionId: session,
              documentJson: documentBytes({ prompt: "draft", mode: "execute" }),
            })
          : operation === Operation.Steer
            ? create(SteerQueuedInputRequestSchema, {
                mutation: { id: uuid(), expectedRevision: 8n, requestId },
                sessionId: session,
                expectedExecutionId: uuid(),
                expectedTurnId: uuid(),
              })
            : create(RespondQuestionRequestSchema, {
                mutation: { id: uuid(), expectedRevision: 5n, requestId },
                responseJson: documentBytes({
                  answers: { original: ["answer"] },
                }),
              });
    await expect(
      state.perform(p.id, operation, request, session),
    ).rejects.toThrow();
    const first = state.profile(p.id).pending;
    expect(first?.request).toContain(requestId);
    const reloaded = new ProtectedState(store, async () => {
      calls++;
      throw new Error("lost");
    });
    await reloaded.load();
    expect(reloaded.profile(p.id).pending).toEqual(first);
    await expect(
      reloaded.perform(p.id, operation, request, session),
    ).rejects.toThrow("pending-operation");
    expect(calls).toBe(1);
    await expect(reloaded.retry(p.id)).rejects.toThrow();
    expect(calls).toBe(2);
    expect(reloaded.profile(p.id).pending).toEqual(first);
  });
it("durable write failure prevents sending and changing selected profile", async () => {
  let calls = 0;
  const state = new ProtectedState(
    {
      read: async () => null,
      write: async () => {
        throw new Error("vault");
      },
    },
    async () => {
      calls++;
      throw new Error("network");
    },
  );
  const p = profile();
  state.state.profiles = [p];
  await expect(state.select(p.id)).rejects.toThrow();
  expect(state.state.selectedProfile).toBe("");
  await expect(
    state.perform(
      p.id,
      Operation.Send,
      create(EnqueueInputRequestSchema, {
        requestId: uuid(),
        sessionId: uuid(),
        documentJson: documentBytes({ prompt: "draft" }),
      }),
      uuid(),
    ),
  ).rejects.toThrow();
  expect(calls).toBe(0);
});
it("forgets only the explicit profile; does not revoke or stop remote execution", async () => {
  let calls = 0;
  const state = new ProtectedState(store, async () => {
      calls++;
      throw new Error();
    }),
    a = profile(),
    b = profile();
  await state.update((s) => {
    s.profiles = [a, b];
    s.selectedProfile = a.id;
  });
  await state.forget(a.id);
  expect(state.state.profiles).toEqual([b]);
  expect(state.state.selectedProfile).toBe("");
  expect(calls).toBe(0);
});
