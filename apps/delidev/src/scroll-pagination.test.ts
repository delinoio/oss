// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { FailureCode } from "@delinoio/delidev-api-client";
import { PaginationChain, ReadStage, type PaginationReader } from "./scroll-pagination";

type Row = { id: string; revision: bigint; title: string };
type Payload = { id: string; privateContents: string };
const reader = vi.fn<PaginationReader<Row, Payload>>(async token => {
  const index = Number(token || 0);
  return { rows: [{ id: String(index), revision: 1n, title: `Page ${index}` }],
    nextPageToken: index < 5 ? String(index + 1) : "",
    payload: [{ id: String(index), privateContents: `Full ${index}` }] };
});

it("keeps only three full payload pages and restores reached content by its accepted token", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader);
  for (let index = 0; index < 5; index++) {
    chain.measure(String(index === 0 ? "" : index), 120 + index);
    await chain.append(reader);
    expect(chain.getSnapshot().payloadPages.length).toBeLessThanOrEqual(3);
  }
  expect(chain.getSnapshot().rows).toHaveLength(6);
  expect(chain.getSnapshot().payloadPages.map(page => page.token)).toEqual(["3", "4", "5"]);
  expect(chain.getSnapshot().pages[0].height).toBe(120);
  expect(chain.getSnapshot().pages[0]).not.toHaveProperty("payload");
  const restore = vi.fn<PaginationReader<Row, Payload>>((token, signal) => reader(token, signal));
  await chain.restore("", restore);
  expect(restore.mock.calls[0][0]).toBe("");
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(chain.getSnapshot().payloadPages.find(page => page.token === "")?.payload[0].privateContents).toBe("Full 0");
  expect(chain.getSnapshot().rows.map(row => row.id)).toEqual(["0", "1", "2", "3", "4", "5"]);
  expect(chain.getSnapshot().nextPageToken).toBe("");
});

it("rejects a changed restored range atomically and requires explicit reload", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); for (let index = 0; index < 3; index++) await chain.append(reader);
  const previous = chain.getSnapshot();
  const changed = vi.fn<PaginationReader<Row, Payload>>(async () => ({ rows: [{ id: "replacement", revision: 1n, title: "Different" }], nextPageToken: "1", payload: [] }));
  await chain.restore("", changed);
  expect(chain.getSnapshot().rows).toBe(previous.rows);
  expect(chain.getSnapshot().payloadPages).toBe(previous.payloadPages);
  expect(chain.getSnapshot().error?.failure.code).toBe(FailureCode.CursorExpired);
  await chain.retry(changed); await chain.restore("", changed);
  expect(changed).toHaveBeenCalledTimes(1);
});

it("retains failed restoration ownership and retries only the original reached token", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); for (let index = 0; index < 4; index++) await chain.append(reader);
  const failed = vi.fn<PaginationReader<Row, Payload>>(async () => { throw new ConnectError("offline", Code.Unavailable); });
  await chain.restore("1", failed); await chain.restore("1", failed); await chain.append(failed);
  expect(failed).toHaveBeenCalledTimes(1);
  expect(chain.getSnapshot().error).toMatchObject({ stage: ReadStage.Restore, token: "1" });
  const retry = vi.fn<PaginationReader<Row, Payload>>((token, signal) => reader(token, signal)); await chain.retry(retry);
  expect(retry.mock.calls.map(([token]) => token)).toEqual(["1"]);
  expect(chain.getSnapshot().pages.map(page => page.token)).toEqual(["", "1", "2", "3", "4"]);
});

it("disposes full payloads on suspension and fences late restore responses", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); for (let index = 0; index < 3; index++) await chain.append(reader);
  let release!: (value: Awaited<ReturnType<typeof reader>>) => void;
  const pending = chain.restore("", () => new Promise(resolve => { release = resolve; }));
  chain.suspend(); chain.activate(); release(await reader("", new AbortController().signal)); await pending;
  expect(chain.getSnapshot().payloadPages).toEqual([]);
  expect(chain.getSnapshot().rows).toHaveLength(4);
});

it("keeps a focused page inside the bounded window and refreshes only accepted ranges", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); chain.protect("");
  for (let index = 0; index < 4; index++) await chain.append(reader);
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(chain.getSnapshot().payloadPages.some(page => page.token === "")).toBe(true);
  const refresh = vi.fn<PaginationReader<Row, Payload>>((token, signal) => reader(token, signal));
  const previous = chain.getSnapshot().payloadPages.map(page => page.token);
  await chain.refresh(refresh);
  expect(refresh.mock.calls.map(([token]) => token)).toEqual(["", "1", "2", "3", "4"]);
  expect(chain.getSnapshot().payloadPages.map(page => page.token)).toEqual(previous);
  expect(chain.getSnapshot().nextPageToken).toBe("5");
  chain.protect(); await chain.append(reader);
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
  expect(chain.getSnapshot().payloadPages.some(page => page.token === "")).toBe(false);
});

it("rehydrates the prior bounded payload window after suspended scope returns", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); for (let index = 0; index < 4; index++) await chain.append(reader);
  const window = chain.getSnapshot().payloadPages.map(page => page.token);
  chain.suspend(); expect(chain.getSnapshot().payloadPages).toEqual([]);
  chain.activate(); await chain.refresh(reader);
  expect(chain.getSnapshot().payloadPages.map(page => page.token)).toEqual(window);
  expect(chain.getSnapshot().payloadPages).toHaveLength(3);
});

it("resumes a failed refresh atomically from its exact failed accepted token", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); await chain.append(reader); await chain.append(reader);
  const previous = chain.getSnapshot();
  const refresh = vi.fn<PaginationReader<Row, Payload>>(async (token, signal) => {
    if (token === "1") throw new ConnectError("offline", Code.Unavailable);
    const batch = await reader(token, signal); return { ...batch, rows: batch.rows.map(row => ({ ...row, revision: 2n })) };
  });
  await chain.refresh(refresh);
  expect(chain.getSnapshot().rows).toBe(previous.rows);
  expect(chain.getSnapshot().payloadPages).toBe(previous.payloadPages);
  const retry = vi.fn<PaginationReader<Row, Payload>>(async (token, signal) => {
    const batch = await reader(token, signal); return { ...batch, rows: batch.rows.map(row => ({ ...row, revision: 2n })) };
  });
  await chain.retry(retry);
  expect(retry.mock.calls.map(([token]) => token)).toEqual(["1", "2"]);
  expect(chain.getSnapshot().rows.map(row => row.revision)).toEqual([2n, 2n, 2n]);
  expect(chain.getSnapshot().payloadPages.some(page => page.token === "")).toBe(false);
  await chain.restore("", retry);
  expect(retry.mock.calls.at(-1)?.[0]).toBe("");
  expect(chain.getSnapshot().payloadPages.length).toBeLessThanOrEqual(3);
});

it("retains only resident payloads for an inert owner while fencing paused reads and final disposal", async () => {
  const chain = new PaginationChain<Row, Payload>(); chain.activate();
  await chain.refresh(reader); for (let index = 0; index < 3; index++) await chain.append(reader);
  const resident = chain.getSnapshot().payloadPages;
  expect(resident).toHaveLength(3);
  let finish!: (batch: Awaited<ReturnType<PaginationReader<Row, Payload>>>) => void;
  const late = vi.fn<PaginationReader<Row, Payload>>(() => new Promise(resolve => { finish = resolve; }));
  const pending = chain.append(late);
  chain.suspend(true);
  const before = chain.getSnapshot();
  await chain.append(late); await chain.restore("", late); await chain.refresh(late);
  expect(late).toHaveBeenCalledTimes(1);
  finish({ rows: [{ id: "late", revision: 1n, title: "Late" }], payload: [{ id: "late", privateContents: "Ignored" }], nextPageToken: "" });
  await pending;
  expect(chain.getSnapshot()).toBe(before);
  expect(chain.getSnapshot().payloadPages).toBe(resident);
  chain.reset();
  expect(chain.getSnapshot().payloadPages).toEqual([]);
  expect(chain.getSnapshot().rows).toEqual([]);
});
