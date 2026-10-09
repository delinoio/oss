import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { ErrorDetailSchema, FailureCode, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { HomeNavigation, NavigationChain, ReadStage, navigationRow, type NavigationReader } from "./home-navigation";
import { encode } from "./documents";

const row = (id = newRequestId(), revision = 1n) => navigationRow(create(ResourceSchema, { id, revision, schemaVersion: 1, documentJson: encode({ name: id, workspace: "general-chat", outcome: "not-started", archive: "active", private_content: "must not be retained" }) }));

it("retains more than 5,000 projected rows with original identity/revision and no full documents", async () => {
  const chain = new NavigationChain();
  chain.activate();
  const ids = Array.from({ length: 5050 }, () => newRequestId());
  const reader: NavigationReader = vi.fn(async (token) => {
    const offset = Number(token || 0);
    return { rows: ids.slice(offset, offset + 50).map((id) => row(id, 9007199254740993n)), nextPageToken: offset + 50 < ids.length ? String(offset + 50) : "" };
  });
  await chain.refresh(reader);
  while (chain.getSnapshot().nextPageToken) await chain.append(reader);
  expect(chain.getSnapshot().rows).toHaveLength(5050);
  expect(chain.getSnapshot().rows[0].revision).toBe(9007199254740993n);
  expect(chain.getSnapshot().rows.at(-1)?.id).toBe(ids.at(-1));
  expect(chain.getSnapshot().pages).toHaveLength(101);
  for (const page of chain.getSnapshot().pages) {
    expect(page.rows.length).toBeLessThanOrEqual(50);
    for (const entry of page.rows) {
      expect(entry).not.toHaveProperty("documentJson");
      expect(entry).not.toHaveProperty("private_content");
      expect(Object.keys(entry)).toEqual(["id", "projectId", "revision", "name", "workspace", "outcome", "archive", "title"]);
    }
  }
});

it("deduplicates adjacent IDs without rounding revisions, traverses empty batches and stops repeated tokens", async () => {
  const chain = new NavigationChain(); chain.activate();
  const first = row(); const second = row();
  const reader = vi.fn<NavigationReader>(async (token) => token === "" ? { rows: [first], nextPageToken: "empty" } : token === "empty" ? { rows: [], nextPageToken: "last" } : { rows: [{ ...first, revision: 9007199254740993n }, second], nextPageToken: "" });
  await chain.refresh(reader); await chain.append(reader); await chain.append(reader);
  expect(chain.getSnapshot().rows.map(({ id }) => id)).toEqual([first.id, second.id]);
  expect(chain.getSnapshot().rows[0].revision).toBe(9007199254740993n);
  const stalled = new NavigationChain(); stalled.activate();
  const loop = vi.fn<NavigationReader>(async () => ({ rows: [first], nextPageToken: "same" }));
  await stalled.refresh(loop); await stalled.append(loop); await stalled.append(loop);
  expect(loop).toHaveBeenCalledTimes(2);
  expect(stalled.getSnapshot().error?.stalled).toBe(true);
  expect(stalled.getSnapshot().rows).toEqual([first]);
});

it("allows one continuation and rejects ignored-abort results after suspension/reset", async () => {
  const chain = new NavigationChain(); chain.activate();
  const first = row(), late = row();
  await chain.refresh(async () => ({ rows: [first], nextPageToken: "next" }));
  let release!: (batch: { rows: ReturnType<typeof row>[]; nextPageToken: string }) => void;
  const delayed = vi.fn<NavigationReader>(() => new Promise((resolve) => { release = resolve; }));
  const pending = chain.append(delayed);
  await chain.append(delayed); await chain.append(delayed);
  expect(delayed).toHaveBeenCalledTimes(1);
  chain.suspend(); chain.activate();
  release({ rows: [late], nextPageToken: "more" }); await pending;
  expect(chain.getSnapshot().rows).toEqual([first]);
  expect(chain.getSnapshot().loading).toBeUndefined();
  const resetPending = chain.append(delayed);
  chain.reset(); release({ rows: [late], nextPageToken: "more" }); await resetPending;
  expect(chain.getSnapshot().rows).toEqual([]);
});

it("retries the failed token once and typed expiry reloads only after accepted first-page success", async () => {
  const home = new HomeNavigation(); home.catalog.activate(); home.global.activate();
  const catalog = row(), previous = row(), replacement = row();
  await home.catalog.refresh(async () => ({ rows: [catalog], nextPageToken: "catalog-next" }));
  await home.global.refresh(async () => ({ rows: [previous], nextPageToken: "session-next" }));
  const failed = vi.fn<NavigationReader>(async () => { throw new ConnectError("Offline", Code.Unavailable); });
  await home.global.append(failed); await home.global.append(failed); await home.global.refresh(failed);
  expect(failed).toHaveBeenCalledTimes(1);
  await home.global.retry(failed);
  expect(failed.mock.calls.map(([token]) => token)).toEqual(["session-next", "session-next"]);
  const expired = vi.fn<NavigationReader>(async () => { throw new ConnectError("Expired", Code.OutOfRange, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: FailureCode.CursorExpired }) }]); });
  await home.global.retry(expired);
  expect(home.global.getSnapshot().error?.failure.code).toBe(FailureCode.CursorExpired);
  await home.global.retry(expired); expect(expired).toHaveBeenCalledTimes(1);
  let accept!: () => void;
  const pending = home.global.reload(async (token) => { expect(token).toBe(""); await new Promise<void>((resolve) => { accept = resolve; }); return { rows: [replacement], nextPageToken: "" }; });
  expect(home.global.getSnapshot().rows).toEqual([previous]);
  accept(); await pending;
  expect(home.global.getSnapshot().rows).toEqual([replacement]);
  expect(home.catalog.getSnapshot().rows).toEqual([catalog]);
  expect(home.catalog.getSnapshot().nextPageToken).toBe("catalog-next");
});

it("refreshes only accepted ranges atomically, retaining previous rows on failure", async () => {
  const chain = new NavigationChain(); chain.activate();
  const first = row(), second = row();
  const reader = vi.fn<NavigationReader>(async (token) => token === "" ? { rows: [first], nextPageToken: "next" } : { rows: [second], nextPageToken: "unseen" });
  await chain.refresh(reader); await chain.append(reader); await chain.refresh(reader);
  expect(reader.mock.calls.map(([token]) => token)).toEqual(["", "next", "", "next"]);
  const refresh = vi.fn<NavigationReader>(async (token) => { if (token) throw new ConnectError("Offline", Code.Unavailable); return { rows: [{ ...first, revision: 2n }], nextPageToken: "next" }; });
  await chain.refresh(refresh);
  expect(chain.getSnapshot().rows).toEqual([first, second]);
  expect(chain.getSnapshot().error?.stage).toBe(ReadStage.Refresh);
  chain.suspend(); await chain.refresh(reader); expect(reader).toHaveBeenCalledTimes(4);
});


it.each([
  { firstNext: "shifted-next", lastNext: "", reads: [""] },
  { firstNext: "", lastNext: "", reads: [""] },
  { firstNext: "accepted-next", lastNext: "new-tail", reads: ["", "accepted-next"] },
])("requires explicit reload when refresh boundaries drift: $firstNext/$lastNext", async ({ firstNext, lastNext, reads }) => {
  const chain = new NavigationChain(); chain.activate();
  const first = row(), second = row(), replacement = row();
  const original: NavigationReader = async (token) => ({ rows: [token ? second : first], nextPageToken: token ? "" : "accepted-next" });
  await chain.refresh(original); await chain.append(original);
  const accepted = chain.getSnapshot();
  const shifted = vi.fn<NavigationReader>(async (token) => ({ rows: token || firstNext !== "accepted-next" ? [replacement] : [{ ...first, revision: 2n }], nextPageToken: token ? lastNext : firstNext }));
  await chain.refresh(shifted);
  expect(shifted.mock.calls.map(([token]) => token)).toEqual(reads);
  expect(chain.getSnapshot().rows).toEqual([first, second]);
  expect(chain.getSnapshot().pages).toBe(accepted.pages);
  expect(chain.getSnapshot().nextPageToken).toBe("");
  expect(chain.getSnapshot().error?.failure.code).toBe(FailureCode.CursorExpired);
  expect(chain.getSnapshot().error?.stage).toBe(ReadStage.Refresh);
  await chain.append(shifted); await chain.refresh(shifted); await chain.retry(shifted);
  expect(shifted).toHaveBeenCalledTimes(reads.length);
  await chain.reload(async (token) => { expect(token).toBe(""); return { rows: [replacement], nextPageToken: "reload-next" }; });
  expect(chain.getSnapshot().rows).toEqual([replacement]);
  expect(chain.getSnapshot().nextPageToken).toBe("reload-next");
  expect(chain.getSnapshot().error).toBeUndefined();
});

it("retries only the failed accepted refresh range while keeping its boundaries", async () => {
  const chain = new NavigationChain(); chain.activate();
  const first = row(), second = row(), replacement = row();
  const original: NavigationReader = async (token) => ({ rows: [token ? second : first], nextPageToken: token ? "tail" : "accepted-next" });
  await chain.refresh(original); await chain.append(original);
  await chain.refresh(async (token) => { if (token) throw new ConnectError("Offline", Code.Unavailable); return { rows: [{ ...first, revision: 2n }], nextPageToken: "accepted-next" }; });
  const refreshedSecond = { ...second, revision: 2n };
  const retry = vi.fn<NavigationReader>(async () => ({ rows: [refreshedSecond], nextPageToken: "renewed-tail" }));
  await chain.retry(retry);
  expect(retry.mock.calls.map(([token]) => token)).toEqual(["accepted-next"]);
  expect(chain.getSnapshot().rows).toEqual([{ ...first, revision: 2n }, refreshedSecond]);
  expect(chain.getSnapshot().pages.map(({ token }) => token)).toEqual(["", "accepted-next"]);
  expect(chain.getSnapshot().nextPageToken).toBe("tail");
  expect(chain.getSnapshot().error).toBeUndefined();
});


it("accepts opaque cursor renewal while refreshing only the original range tokens", async () => {
  const chain = new NavigationChain(); chain.activate();
  const first = row(), second = row();
  await chain.refresh(async () => ({ rows: [first], nextPageToken: "accepted-next" }));
  await chain.append(async () => ({ rows: [second], nextPageToken: "accepted-tail" }));
  const refreshedFirst = { ...first, revision: 2n }, refreshedSecond = { ...second, revision: 3n };
  const renewed = vi.fn<NavigationReader>(async (token) => ({ rows: [token ? refreshedSecond : refreshedFirst], nextPageToken: token ? "renewed-tail" : "renewed-next" }));
  await chain.refresh(renewed);
  expect(renewed.mock.calls.map(([token]) => token)).toEqual(["", "accepted-next"]);
  expect(chain.getSnapshot().rows).toEqual([refreshedFirst, refreshedSecond]);
  expect(chain.getSnapshot().pages.map(({ token, nextPageToken }) => [token, nextPageToken])).toEqual([["", "accepted-next"], ["accepted-next", "accepted-tail"]]);
  expect(chain.getSnapshot().nextPageToken).toBe("accepted-tail");
  expect(chain.getSnapshot().error).toBeUndefined();
  await chain.refresh(renewed);
  expect(renewed.mock.calls.map(([token]) => token)).toEqual(["", "accepted-next", "", "accepted-next"]);
});
