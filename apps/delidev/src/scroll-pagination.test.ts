// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { FailureCode } from "@delinoio/delidev-api-client";
import {
  PaginationChain,
  pagesResident,
  ReadStage,
  type PaginationReader,
} from "./scroll-pagination";

type Row = { id: string; revision: bigint; title: string };
type Payload = { id: string; privateContents: string };
const reader = vi.fn<PaginationReader<Row, Payload>>(async (token) => {
  const index = Number(token || 0);
  return {
    rows: [{ id: String(index), revision: 1n, title: `Page ${index}` }],
    nextPageToken: index < 5 ? String(index + 1) : "",
    payload: [{ id: String(index), privateContents: `Full ${index}` }],
  };
});

it("keeps only three full payload pages and restores reached content by its accepted token", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 5; index++) {
    chain.measure(String(index === 0 ? "" : index), 120 + index);
    await chain.append(reader);
    expect(chain.getSnapshot().payloadPages.length).toBeLessThanOrEqual(3);
  }
  expect(chain.getSnapshot().rows).toHaveLength(6);
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual([
    "3",
    "4",
    "5",
  ]);
  expect(chain.getSnapshot().pages[0].height).toBe(120);
  expect(chain.getSnapshot().pages[0]).not.toHaveProperty("payload");
  const restore = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.restore("", restore);
  expect(restore.mock.calls[0][0]).toBe("");
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(
    chain.getSnapshot().payloadPages.find((page) => page.token === "")
      ?.payload[0].privateContents,
  ).toBe("Full 0");
  expect(chain.getSnapshot().rows.map((row) => row.id)).toEqual([
    "0",
    "1",
    "2",
    "3",
    "4",
    "5",
  ]);
  expect(chain.getSnapshot().nextPageToken).toBe("");
});

it("rejects a changed restored range atomically and requires explicit reload", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 3; index++) await chain.append(reader);
  const previous = chain.getSnapshot();
  const changed = vi.fn<PaginationReader<Row, Payload>>(async () => ({
    rows: [{ id: "replacement", revision: 1n, title: "Different" }],
    nextPageToken: "1",
    payload: [],
  }));
  await chain.restore("", changed);
  expect(chain.getSnapshot().rows).toBe(previous.rows);
  expect(chain.getSnapshot().payloadPages).toBe(previous.payloadPages);
  expect(chain.getSnapshot().error?.failure.code).toBe(
    FailureCode.CursorExpired,
  );
  await chain.retry(changed);
  await chain.restore("", changed);
  expect(changed).toHaveBeenCalledTimes(1);
});

it("retains failed restoration ownership and retries only the original reached token", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 4; index++) await chain.append(reader);
  const failed = vi.fn<PaginationReader<Row, Payload>>(async () => {
    throw new ConnectError("offline", Code.Unavailable);
  });
  await chain.restore("1", failed);
  await chain.restore("1", failed);
  await chain.append(failed);
  expect(failed).toHaveBeenCalledTimes(1);
  expect(chain.getSnapshot().error).toMatchObject({
    stage: ReadStage.Restore,
    token: "1",
  });
  const retry = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.retry(retry);
  expect(retry.mock.calls.map(([token]) => token)).toEqual(["1"]);
  expect(chain.getSnapshot().pages.map((page) => page.token)).toEqual([
    "",
    "1",
    "2",
    "3",
    "4",
  ]);
});

it("disposes full payloads on suspension and fences late restore responses", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 3; index++) await chain.append(reader);
  let release!: (value: Awaited<ReturnType<typeof reader>>) => void;
  const pending = chain.restore(
    "",
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  chain.suspend();
  chain.activate();
  release(await reader("", new AbortController().signal));
  await pending;
  expect(chain.getSnapshot().payloadPages).toEqual([]);
  expect(chain.getSnapshot().rows).toHaveLength(4);
});

it("keeps a focused page inside the bounded window and refreshes only accepted ranges", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  chain.protect("");
  for (let index = 0; index < 4; index++) await chain.append(reader);
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(
    chain.getSnapshot().payloadPages.some((page) => page.token === ""),
  ).toBe(true);
  const refresh = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  const previous = chain.getSnapshot().payloadPages.map((page) => page.token);
  await chain.refresh(refresh);
  expect(refresh.mock.calls.map(([token]) => token)).toEqual([
    "",
    "1",
    "2",
    "3",
    "4",
  ]);
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual(
    previous,
  );
  expect(chain.getSnapshot().nextPageToken).toBe("5");
  chain.protect();
  await chain.append(reader);
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(
    chain.getSnapshot().payloadPages.some((page) => page.token === ""),
  ).toBe(false);
});

it("rehydrates the prior bounded payload window after suspended scope returns", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 4; index++) await chain.append(reader);
  const window = chain.getSnapshot().payloadPages.map((page) => page.token);
  chain.suspend();
  expect(chain.getSnapshot().payloadPages).toEqual([]);
  chain.activate();
  await chain.refresh(reader);
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual(
    window,
  );
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
});

it("resumes a failed refresh atomically from its exact failed accepted token", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  await chain.append(reader);
  await chain.append(reader);
  const previous = chain.getSnapshot();
  const refresh = vi.fn<PaginationReader<Row, Payload>>(
    async (token, signal) => {
      if (token === "1") throw new ConnectError("offline", Code.Unavailable);
      const batch = await reader(token, signal);
      return {
        ...batch,
        rows: batch.rows.map((row) => ({ ...row, revision: 2n })),
      };
    },
  );
  await chain.refresh(refresh);
  expect(chain.getSnapshot().rows).toBe(previous.rows);
  expect(chain.getSnapshot().payloadPages).toBe(previous.payloadPages);
  const retry = vi.fn<PaginationReader<Row, Payload>>(async (token, signal) => {
    const batch = await reader(token, signal);
    return {
      ...batch,
      rows: batch.rows.map((row) => ({ ...row, revision: 2n })),
    };
  });
  await chain.retry(retry);
  expect(retry.mock.calls.map(([token]) => token)).toEqual(["1", "2"]);
  expect(chain.getSnapshot().rows.map((row) => row.revision)).toEqual([
    2n,
    2n,
    2n,
  ]);
  expect(
    chain.getSnapshot().payloadPages.some((page) => page.token === ""),
  ).toBe(false);
  await chain.restore("", retry);
  expect(retry.mock.calls.at(-1)?.[0]).toBe("");
  expect(chain.getSnapshot().payloadPages.length).toBeLessThanOrEqual(3);
});

it("retains only resident payloads for an inert owner while fencing paused reads and final disposal", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 3; index++) await chain.append(reader);
  const resident = chain.getSnapshot().payloadPages;
  expect(resident).toHaveLength(3);
  let finish!: (
    batch: Awaited<ReturnType<PaginationReader<Row, Payload>>>,
  ) => void;
  const late = vi.fn<PaginationReader<Row, Payload>>(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const pending = chain.append(late);
  chain.suspend(true);
  const before = chain.getSnapshot();
  await chain.append(late);
  await chain.restore("", late);
  await chain.refresh(late);
  expect(late).toHaveBeenCalledTimes(1);
  finish({
    rows: [{ id: "late", revision: 1n, title: "Late" }],
    payload: [{ id: "late", privateContents: "Ignored" }],
    nextPageToken: "",
  });
  await pending;
  expect(chain.getSnapshot()).toBe(before);
  expect(chain.getSnapshot().payloadPages).toBe(resident);
  chain.reset();
  expect(chain.getSnapshot().payloadPages).toEqual([]);
  expect(chain.getSnapshot().rows).toEqual([]);
});

it("does not treat a terminal token as resident payload evidence after eviction", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 5; index++) await chain.append(reader);
  const evicted = chain.getSnapshot();
  expect(evicted.loaded).toBe(true);
  expect(evicted.nextPageToken).toBe("");
  expect(evicted.pages.length).toBeGreaterThan(evicted.payloadPages.length);
  expect(evicted.payloadPages.length).toBeLessThanOrEqual(3);
  expect(pagesResident(evicted)).toBe(false);
  // Restoration stays reachable and refills the evicted page.
  await chain.restore("", reader);
  expect(chain.getSnapshot().payloadPages.length).toBeLessThanOrEqual(3);
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toContain(
    "",
  );
});

it("reports full residency for fully resident pages", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  expect(pagesResident(chain.getSnapshot())).toBe(true);
  await chain.append(reader);
  await chain.append(reader);
  expect(chain.getSnapshot().pages).toHaveLength(3);
  expect(pagesResident(chain.getSnapshot())).toBe(true);
  expect(pagesResident({ pages: [], payloadPages: [] })).toBe(true);
});

it("runs the accepted observer for every reached page, including evicted ones, while payloads stay bounded", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  const observed: string[] = [];
  const observing = (token: string): ReturnType<typeof reader> =>
    reader(token, new AbortController().signal).then((batch) => ({
      ...batch,
      accepted: () => observed.push(token || "0"),
    }));
  await chain.refresh(observing);
  for (let index = 0; index < 3; index++) await chain.append(observing);
  observed.length = 0;
  // Page "0" (token "") is no longer inside the three-page payload window.
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual([
    "1",
    "2",
    "3",
  ]);
  await chain.refresh(observing);
  expect(observed).toEqual(["0", "1", "2", "3"]);
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual([
    "1",
    "2",
    "3",
  ]);
});

it("does not run the accepted observer for canceled, superseded or boundary-rejected reads", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 2; index++) await chain.append(reader);
  const accepted = vi.fn();
  let release!: (value: Awaited<ReturnType<typeof reader>>) => void;
  const pending = chain.refresh(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  chain.suspend();
  chain.activate();
  release({
    ...(await reader("", new AbortController().signal)),
    accepted,
  });
  await pending;
  expect(accepted).not.toHaveBeenCalled();
  // A changed continuation boundary rejects the refresh before observation.
  await chain.refresh(async (token) => ({
    ...(await reader(token, new AbortController().signal)),
    nextPageToken: "",
    accepted,
  }));
  expect(accepted).not.toHaveBeenCalled();
  expect(chain.getSnapshot().error?.failure.code).toBe(
    FailureCode.CursorExpired,
  );
});

it("keeps a read accepted when its observer throws", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  await chain.refresh(async (token) => ({
    ...(await reader(token, new AbortController().signal)),
    accepted: () => {
      throw new Error("observer failed");
    },
  }));
  warn.mockRestore();
  expect(chain.getSnapshot().loaded).toBe(true);
  expect(chain.getSnapshot().error).toBeUndefined();
});

const counted =
  (counts: Record<string, number>): PaginationReader<Row, Payload> =>
  async (token, signal) => ({
    ...(await reader(token, signal)),
    rawCount: counts[token || "0"] ?? 0,
  });
const rawTotal = (chain: PaginationChain<Row, Payload>) =>
  chain
    .getSnapshot()
    .pages.reduce((total, page) => total + (page.rawCount ?? 0), 0);

it("retains the raw page scalar for every reached page after payload eviction", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  const read = counted({ "0": 7 });
  await chain.refresh(read);
  for (let index = 0; index < 4; index++) await chain.append(read);
  expect(chain.getSnapshot().pages).toHaveLength(5);
  expect(chain.getSnapshot().payloadPages.map((page) => page.token)).toEqual([
    "2",
    "3",
    "4",
  ]);
  expect(chain.getSnapshot().pages[0].rawCount).toBe(7);
  expect(rawTotal(chain)).toBe(7);
  chain.measure("", 90);
  expect(chain.getSnapshot().pages[0].rawCount).toBe(7);
});

it("replaces raw page scalars when a refresh, reload or reset replaces the chain", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(counted({ "0": 3 }));
  for (let index = 0; index < 4; index++)
    await chain.append(counted({ "0": 3 }));
  expect(rawTotal(chain)).toBe(3);
  await chain.refresh(counted({}));
  expect(chain.getSnapshot().pages).toHaveLength(5);
  expect(rawTotal(chain)).toBe(0);
  await chain.reload(counted({ "0": 2, "1": 1 }));
  expect(chain.getSnapshot().pages).toHaveLength(1);
  expect(rawTotal(chain)).toBe(2);
  chain.reset();
  expect(chain.getSnapshot().pages).toEqual([]);
  expect(rawTotal(chain)).toBe(0);
});

it("keeps the previous raw page scalars when a refresh fails", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(counted({ "0": 4 }));
  await chain.append(counted({ "0": 4 }));
  const failing: PaginationReader<Row, Payload> = async () => {
    throw new ConnectError("denied", Code.PermissionDenied);
  };
  await chain.refresh(failing);
  expect(chain.getSnapshot().error).toBeDefined();
  expect(rawTotal(chain)).toBe(4);
});

// A reader whose every call stays open until the test settles it.
function deferredReader(label = "v", last = 2) {
  const tokens: string[] = [];
  const calls: {
    token: string;
    resolve: () => void;
    reject: (error: unknown) => void;
  }[] = [];
  const read: PaginationReader<Row, Payload> = (token) =>
    new Promise((resolve, reject) => {
      tokens.push(token);
      calls.push({
        token,
        reject,
        resolve: () => {
          const index = Number(token || 0);
          resolve({
            rows: [
              { id: String(index), revision: 1n, title: `${label} ${index}` },
            ],
            nextPageToken: index < last ? String(index + 1) : "",
            payload: [{ id: String(index), privateContents: `Full ${index}` }],
          });
        },
      });
    });
  return { calls, read, tokens };
}
const flush = async () => {
  for (let index = 0; index < 8; index++) await Promise.resolve();
};
const titles = (chain: PaginationChain<Row, Payload>) =>
  chain.getSnapshot().rows.map((row) => row.title);
async function loadedChain(pages = 2) {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  for (let index = 1; index < pages; index++) await chain.append(reader);
  return chain;
}

// Settles every deferred call of the given readers until `pending` resolves.
async function settleAll(
  pending: Promise<unknown>,
  ...readers: ReturnType<typeof deferredReader>[]
) {
  let done = false;
  void pending.then(() => {
    done = true;
  });
  for (let guard = 0; !done && guard < 200; guard++) {
    for (const deferred of readers) deferred.calls.shift()?.resolve();
    await flush();
  }
  expect(done).toBe(true);
}

it.each([
  ReadStage.Initial,
  ReadStage.Additional,
  ReadStage.Restore,
  ReadStage.Refresh,
  ReadStage.Reload,
])(
  "queues one reached-range refresh behind an active %s read",
  async (stage) => {
    const chain = await loadedChain(stage === ReadStage.Restore ? 5 : 2);
    const last = stage === ReadStage.Restore ? 5 : 2;
    const active = deferredReader("active", last);
    const fresh = deferredReader("fresh", last);
    let pending: Promise<void>;
    if (stage === ReadStage.Initial) {
      chain.reset();
      chain.activate();
      pending = chain.refresh(active.read);
    } else if (stage === ReadStage.Additional) {
      pending = chain.append(active.read);
    } else if (stage === ReadStage.Restore) {
      pending = chain.restore("", active.read);
    } else if (stage === ReadStage.Refresh) {
      pending = chain.refresh(active.read);
    } else {
      pending = chain.reload(active.read);
    }
    expect(chain.getSnapshot().loading).toBe(stage);
    // Several invalidations while busy coalesce into one queued refresh.
    await chain.refresh(fresh.read);
    await chain.refresh(fresh.read);
    await chain.refresh(fresh.read);
    expect(fresh.calls).toHaveLength(0);
    await settleAll(pending, active, fresh);
    const snapshot = chain.getSnapshot();
    expect(snapshot.loading).toBeUndefined();
    expect(snapshot.error).toBeUndefined();
    // Exactly one refresh pass ran: it read each accepted request token once and
    // never discovered a new tail, so the reached range is unchanged.
    const reached: Record<string, string[]> = {
      [ReadStage.Initial]: [""],
      [ReadStage.Additional]: ["", "1", "2"],
      [ReadStage.Restore]: ["", "1", "2", "3", "4"],
      [ReadStage.Refresh]: ["", "1"],
      [ReadStage.Reload]: [""],
    };
    const expectedTokens = reached[stage];
    expect(snapshot.pages.map((page) => page.token)).toEqual(expectedTokens);
    expect(fresh.tokens).toEqual(expectedTokens);
    expect(titles(chain)).toEqual(expectedTokens.map((_, i) => `fresh ${i}`));
  },
);

it("keeps the queued refresh after a failed read and honors it after explicit retry", async () => {
  const chain = await loadedChain(2);
  const active = deferredReader("active");
  const fresh = deferredReader("fresh");
  const pending = chain.refresh(active.read);
  await chain.refresh(fresh.read);
  active.calls[0].reject(new ConnectError("offline", Code.Unavailable));
  await pending;
  expect(chain.getSnapshot().error?.stage).toBe(ReadStage.Refresh);
  // No automatic retry loop and no refresh while the error is held.
  await flush();
  expect(active.calls).toHaveLength(1);
  expect(fresh.calls).toHaveLength(0);
  // An invalidation during the held error is retained too.
  await chain.refresh(fresh.read);
  expect(fresh.calls).toHaveLength(0);
  const recovery = deferredReader("recovered");
  const retrying = chain.retry(recovery.read);
  for (let index = 0; index < 2; index++) {
    while (!recovery.calls.length) await flush();
    recovery.calls.shift()!.resolve();
    await flush();
  }
  while (!fresh.calls.length) await flush();
  expect(fresh.calls).toHaveLength(1);
  for (let index = 0; index < 2; index++) {
    while (!fresh.calls.length) await flush();
    fresh.calls.shift()!.resolve();
    await flush();
  }
  await retrying;
  expect(chain.getSnapshot().error).toBeUndefined();
  expect(titles(chain).every((title) => title.startsWith("fresh"))).toBe(true);
});

it("lets an explicit reload supersede a queued refresh recorded before it started", async () => {
  const chain = await loadedChain(2);
  const changed: PaginationReader<Row, Payload> = async () => ({
    rows: [{ id: "x", revision: 1n, title: "changed" }],
    nextPageToken: "",
  });
  await chain.refresh(changed);
  expect(chain.getSnapshot().error?.failure.code).toBe(
    FailureCode.CursorExpired,
  );
  const fresh = deferredReader("fresh");
  await chain.refresh(fresh.read);
  expect(fresh.calls).toHaveLength(0);
  // Reload reads every page from the start token after the invalidation, so
  // no second coalesced refresh follows it.
  await chain.reload(reader);
  await flush();
  expect(chain.getSnapshot().error).toBeUndefined();
  expect(fresh.calls).toHaveLength(0);
  // An invalidation arriving while the reload runs still queues one refresh.
  const reloading = deferredReader("reloading");
  const pending = chain.reload(reloading.read);
  await chain.refresh(fresh.read);
  await settleAll(pending, reloading, fresh);
  expect(fresh.tokens).toEqual([""]);
  expect(chain.getSnapshot().error).toBeUndefined();
});

it("lets an explicit retry of a failed initial read supersede the queued refresh", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(async () => {
    throw new ConnectError("offline", Code.Unavailable);
  });
  expect(chain.getSnapshot().error?.stage).toBe(ReadStage.Initial);
  const fresh = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.refresh(fresh);
  expect(fresh).not.toHaveBeenCalled();
  const recovery = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.retry(recovery);
  await flush();
  // The retry re-reads from the start token after the invalidation.
  expect(recovery).toHaveBeenCalledTimes(1);
  expect(fresh).not.toHaveBeenCalled();
  expect(chain.getSnapshot().error).toBeUndefined();
});

it("clears a queued refresh when the owner suspends, resets or is replaced", async () => {
  for (const clear of ["suspend", "reset"] as const) {
    const chain = await loadedChain(2);
    const active = deferredReader("active");
    const fresh = deferredReader("fresh");
    const pending = chain.refresh(active.read);
    await chain.refresh(fresh.read);
    if (clear === "suspend") chain.suspend();
    else chain.reset();
    chain.activate();
    active.calls[0].resolve();
    await pending;
    await flush();
    expect(fresh.calls).toHaveLength(0);
    expect(chain.getSnapshot().loading).toBeUndefined();
  }
  // Inactive owners never record invalidations.
  const idle = new PaginationChain<Row, Payload>();
  const fresh = deferredReader("fresh");
  await idle.refresh(fresh.read);
  expect(fresh.calls).toHaveLength(0);
  idle.activate();
  const first = idle.refresh(fresh.read);
  expect(fresh.calls).toHaveLength(1);
  await settleAll(first, fresh);
});

it("runs the marker invalidation immediately when the chain is idle", async () => {
  const chain = await loadedChain(2);
  const read = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.refresh(read);
  expect(read.mock.calls.map((call) => call[0])).toEqual(["", "1"]);
});

it("keeps whole-chain observation evidence after the first payload page is evicted and clears it on suspend until a refresh completes", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  expect(chain.getSnapshot().observed).toBe(false);
  await chain.refresh(reader);
  expect(chain.getSnapshot().observed).toBe(true);
  for (let index = 0; index < 5; index++) await chain.append(reader);
  const grown = chain.getSnapshot();
  // The evictable first payload page is gone, yet the observation remains.
  expect(grown.payloadPages.map((page) => page.token)).toEqual(["3", "4", "5"]);
  expect(grown.observed).toBe(true);
  // Additional pages inside the visit never changed the evidence; suspending
  // ends the visit and revokes it.
  chain.suspend();
  expect(chain.getSnapshot().observed).toBe(false);
  expect(chain.getSnapshot().loaded).toBe(true);
  chain.activate();
  const refreshed = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.refresh(refreshed);
  const snapshot = chain.getSnapshot();
  expect(snapshot.observed).toBe(true);
  expect(snapshot.payloadPages.length).toBeLessThanOrEqual(3);
  expect(snapshot.payloadPages.some((page) => page.token === "")).toBe(false);
  expect(snapshot.pages.map((page) => page.token)).toEqual([
    "",
    "1",
    "2",
    "3",
    "4",
    "5",
  ]);
  expect(snapshot.nextPageToken).toBe("");
  // Suspending again revokes the evidence; a Restore read cannot grant it back
  // before a whole-chain refresh succeeds.
  chain.suspend();
  chain.activate();
  const afterSuspend = vi.fn<PaginationReader<Row, Payload>>((token, signal) =>
    reader(token, signal),
  );
  await chain.restore("", afterSuspend);
  expect(afterSuspend).toHaveBeenCalledTimes(1);
  expect(chain.getSnapshot().observed).toBe(false);
});

it("never grants observation evidence to failed, in-flight, suspended or reset whole-chain reads", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  const failing = vi.fn<PaginationReader<Row, Payload>>(async () => {
    throw new ConnectError("unavailable", Code.Unavailable);
  });
  await chain.refresh(failing);
  expect(chain.getSnapshot().observed).toBe(false);
  expect(chain.getSnapshot().error).toBeDefined();
  await chain.retry(reader);
  expect(chain.getSnapshot().observed).toBe(true);
  // A refresh that fails after a success drops the earlier evidence.
  chain.suspend();
  chain.activate();
  await chain.refresh(failing);
  expect(chain.getSnapshot().observed).toBe(false);
  await chain.retry(reader);
  expect(chain.getSnapshot().observed).toBe(true);
  // An in-flight refresh revokes the evidence until it succeeds.
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const pending = chain.refresh(async (token, signal) => {
    await gate;
    return reader(token, signal);
  });
  expect(chain.getSnapshot().loading).toBe(ReadStage.Refresh);
  expect(chain.getSnapshot().observed).toBe(false);
  // Suspension during the read leaves no admission after the stale read ends.
  chain.suspend();
  release();
  await pending;
  expect(chain.getSnapshot().observed).toBe(false);
  chain.activate();
  await chain.refresh(reader);
  expect(chain.getSnapshot().observed).toBe(true);
  chain.reset();
  expect(chain.getSnapshot().observed).toBe(false);
  expect(chain.getSnapshot().loaded).toBe(false);
  chain.activate();
  await chain.reload(reader);
  expect(chain.getSnapshot().observed).toBe(true);
});

it("keeps observation evidence across a failed additional page inside the same visit", async () => {
  const chain = new PaginationChain<Row, Payload>();
  chain.activate();
  await chain.refresh(reader);
  await chain.append(async () => {
    throw new ConnectError("unavailable", Code.Unavailable);
  });
  expect(chain.getSnapshot().error).toBeDefined();
  expect(chain.getSnapshot().observed).toBe(true);
  await chain.retry(reader);
  expect(chain.getSnapshot().observed).toBe(true);
});
