// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  ClaimNotificationResponseSchema,
  InboxService,
  ListNotificationCandidatesResponseSchema,
  NotificationCandidateSchema,
  NotificationDeliverySchema,
  NotificationState,
  ReportNotificationResponseSchema,
} from "@delinoio/delidev-api-client";
import { Operation, PendingPhase, ProtectedState, uuid } from "./state";
import { presentForeground, type DrainOwner } from "./notifications";
beforeEach(() => {
  vi.stubGlobal("crypto", webcrypto);
  vi.spyOn(console, "warn").mockImplementation(() => {});
  vi.spyOn(console, "info").mockImplementation(() => {});
});
interface Options {
  total: number;
  /** Inbox ids whose claim response is a replay that grants no OS submission. */
  replayed?: Set<string>;
  failReport?: boolean;
  failList?: boolean;
}
async function harness(options: Options) {
  let raw: string | null = null;
  const state = new ProtectedState({
    read: async () => raw,
    write: async (value: string) => {
      raw = value;
    },
  });
  const profile = {
    id: uuid(),
    serverId: uuid(),
    deviceId: uuid(),
    name: "Owned",
    origin: "https://example.test",
    token: "a".repeat(43),
  };
  await state.update((s) => {
    s.profiles.push(profile);
    s.selectedProfile = profile.id;
    s.notifications = true;
  });
  const claimed = new Set<string>(),
    reported: string[] = [],
    limits: number[] = [],
    events: string[] = [];
  const ids = Array.from({ length: options.total }, (_, i) => `inbox-${i}`);
  const transport = createRouterTransport((router) => {
    router.service(InboxService, {
      listNotificationCandidates: async (request) => {
        events.push("list");
        if (options.failList) throw new ConnectError("down", Code.Unavailable);
        limits.push(request.limit);
        const open = ids.filter((id) => !claimed.has(id));
        return create(ListNotificationCandidatesResponseSchema, {
          candidates: open
            .slice(0, request.limit)
            .map((inboxId) => create(NotificationCandidateSchema, { inboxId })),
          more: open.length > request.limit,
        });
      },
      claimNotification: async (request) => {
        const replayed = options.replayed?.has(request.inboxId) ?? false;
        claimed.add(request.inboxId);
        return create(ClaimNotificationResponseSchema, {
          delivery: create(NotificationDeliverySchema, {
            claimId: `claim-${request.inboxId}`,
          }),
          mayPresent: !replayed,
          replayed,
          requestId: request.requestId,
        });
      },
      reportNotification: async (request) => {
        if (options.failReport)
          throw new ConnectError("lost", Code.Unavailable);
        reported.push(request.inboxId + ":" + request.state);
        return create(ReportNotificationResponseSchema, {});
      },
    });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  return { state, profile, claimed, reported, limits, events };
}
const owner = (): DrainOwner & { epoch: number } => ({ epoch: 0 });
it("drains 101 candidates in three limit-50 batches with one OS submission each and macrotask yields", async () => {
  const { state, profile, limits, reported, events } = await harness({
    total: 101,
  });
  const notify = vi.fn(async () => {
    events.push("notify");
    return true;
  });
  const timeout = vi.spyOn(globalThis, "setTimeout");
  await presentForeground(state, profile.id, () => true, { notify }, owner());
  expect(limits).toEqual([50, 50, 50]);
  expect(notify).toHaveBeenCalledTimes(101);
  expect(reported).toHaveLength(101);
  expect(
    reported.every((r) => r.endsWith(":" + NotificationState.SUBMITTED)),
  ).toBe(true);
  expect(timeout.mock.calls.filter(([, ms]) => ms === 0)).toHaveLength(2);
  expect(state.profile(profile.id).pending).toBeUndefined();
  // Each batch lists only after the previous batch settled.
  const lists = events.reduce<number[]>(
    (acc, e, i) => (e === "list" ? [...acc, i] : acc),
    [],
  );
  expect(lists).toEqual([0, 51, 102]);
});
it("joins an in-flight drain for the same generation and starts a new one for a new generation", async () => {
  const { state, profile, limits } = await harness({ total: 3 });
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const notify = vi.fn(async () => {
    await gate;
    return true;
  });
  const connection = owner();
  const first = presentForeground(
    state,
    profile.id,
    () => true,
    { notify },
    connection,
  );
  const joined = presentForeground(
    state,
    profile.id,
    () => true,
    { notify },
    connection,
  );
  expect(joined).toBe(first);
  release();
  await first;
  expect(notify).toHaveBeenCalledTimes(3);
  // One initial pass plus the single rerun requested by the joining trigger.
  expect(limits).toEqual([50, 50]);
});
it("a new connection generation starts its own drain and the stale one stops before claiming", async () => {
  const { state, profile, claimed } = await harness({ total: 4 });
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const connection = owner();
  const stale = presentForeground(
    state,
    profile.id,
    () => true,
    { notify: async () => (await gate, true) },
    connection,
  );
  await vi.waitFor(() => expect(claimed.size).toBe(1));
  connection.epoch++;
  const notify = vi.fn(async () => true);
  const current = presentForeground(
    state,
    profile.id,
    () => true,
    { notify },
    connection,
  );
  expect(current).not.toBe(stale);
  release();
  await Promise.all([stale, current]);
  // The stale drain's report stayed exact, so the new drain found it pending.
  expect(notify).not.toHaveBeenCalled();
  expect(claimed.size).toBe(1);
  expect(state.profile(profile.id).pending?.operation).toBe(Operation.Report);
});
it("stops without further claims when the generation changes between candidates", async () => {
  const { state, profile, claimed } = await harness({ total: 5 });
  const connection = owner();
  const notify = vi.fn(async () => {
    connection.epoch++;
    return true;
  });
  await presentForeground(
    state,
    profile.id,
    () => true,
    { notify },
    connection,
  );
  expect(notify).toHaveBeenCalledTimes(1);
  expect(claimed.size).toBe(1);
  // The report for the already submitted alert stays pending and exact.
  const pending = state.profile(profile.id).pending;
  expect(pending?.operation).toBe(Operation.Report);
  expect(pending?.phase).toBe(PendingPhase.Prepared);
});
it("stops when the app leaves the foreground or the profile changes", async () => {
  for (const change of ["background", "profile", "setting"] as const) {
    const { state, profile, claimed } = await harness({ total: 4 });
    let live = true;
    const notify = vi.fn(async () => {
      if (change === "background") live = false;
      else
        await state.update((s) => {
          if (change === "profile") s.selectedProfile = "";
          else s.notifications = false;
        });
      return true;
    });
    await presentForeground(state, profile.id, () => live, { notify }, owner());
    expect(notify).toHaveBeenCalledTimes(1);
    expect(claimed.size).toBe(1);
  }
});
it("stops on a non-progressing batch of replayed claims without polling", async () => {
  const total = 60;
  const replayed = new Set(
    Array.from({ length: total }, (_, i) => `inbox-${i}`),
  );
  const { state, profile, limits, claimed } = await harness({
    total,
    replayed,
  });
  const notify = vi.fn(async () => true);
  await presentForeground(state, profile.id, () => true, { notify }, owner());
  expect(notify).not.toHaveBeenCalled();
  expect(limits).toEqual([50]);
  expect(claimed.size).toBe(50);
  expect(state.profile(profile.id).pending).toBeUndefined();
});
it("denied OS submission is reported once and never resubmitted", async () => {
  const { state, profile, reported } = await harness({ total: 2 });
  const notify = vi.fn(async () => false);
  await presentForeground(state, profile.id, () => true, { notify }, owner());
  await presentForeground(state, profile.id, () => true, { notify }, owner());
  expect(notify).toHaveBeenCalledTimes(2);
  expect(
    reported.every((r) => r.endsWith(":" + NotificationState.DENIED)),
  ).toBe(true);
});
it("lost report response leaves the exact pending report and stops without duplicate OS submission", async () => {
  const { state, profile, claimed } = await harness({
    total: 4,
    failReport: true,
  });
  const notify = vi.fn(async () => true);
  const connection = owner();
  await expect(
    presentForeground(state, profile.id, () => true, { notify }, connection),
  ).rejects.toThrow();
  const pending = state.profile(profile.id).pending;
  expect(pending?.operation).toBe(Operation.Report);
  expect(pending?.phase).toBe(PendingPhase.Uncertain);
  const request = pending?.request;
  // A later trigger does nothing while the report is unresolved.
  await presentForeground(
    state,
    profile.id,
    () => true,
    { notify },
    connection,
  );
  expect(notify).toHaveBeenCalledTimes(1);
  expect(claimed.size).toBe(1);
  expect(state.profile(profile.id).pending?.request).toBe(request);
});
it("transport failure listing candidates stops without retry", async () => {
  const { state, profile, events } = await harness({
    total: 3,
    failList: true,
  });
  const notify = vi.fn(async () => true);
  await expect(
    presentForeground(state, profile.id, () => true, { notify }, owner()),
  ).rejects.toThrow();
  expect(events).toEqual(["list"]);
  expect(notify).not.toHaveBeenCalled();
});
