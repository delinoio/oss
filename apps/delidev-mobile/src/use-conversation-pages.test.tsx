// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, ResourceService, SessionService } from "@delinoio/delidev-api-client";
import { ConversationLane, type ConversationPage } from "./conversation-pages";
import { useConversationPages } from "./use-conversation-pages";
import { documentBytes } from "./state";
import type { ReactNode } from "react";
afterEach(cleanup);
const row = (id: string, lane = ConversationLane.Queue) => create(ResourceSchema, { kind: lane === ConversationLane.Queue ? EntityKind.QUEUE : EntityKind.INTERACTION, id, revision: 9n, sessionId: "session", schemaVersion: 1, documentJson: documentBytes({ prompt: id, closure: id === "51" ? "open" : "closed" }) });
function fixture(read: (token: string, lane: ConversationLane) => ConversationPage | Promise<ConversationPage>, lane = ConversationLane.Queue) {
  const requests: { token: string; lane: ConversationLane; pageSize: number }[] = [];
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const transport = createRouterTransport(router => {
    router.service(SessionService, { listQueue: async request => { requests.push({ token: request.pageToken, lane: ConversationLane.Queue, pageSize: request.pageSize }); const result = await read(request.pageToken, ConversationLane.Queue); return { inputs: result.resources, nextPageToken: result.nextPageToken }; } });
    router.service(ResourceService, { listResources: request => { requests.push({ token: request.filter!.pageToken, lane: ConversationLane.Interactions, pageSize: request.filter!.pageSize }); return read(request.filter!.pageToken, ConversationLane.Interactions); } });
  });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={cache}><TransportProvider transport={transport}>{children}</TransportProvider></QueryClientProvider>;
  const hook = renderHook(({ enabled }) => useConversationPages(lane, "session", enabled), { wrapper, initialProps: { enabled: true } });
  return { ...hook, requests, cache };
}
it.each([ConversationLane.Queue, ConversationLane.Interactions])("loads the original 51st %s only on explicit continuation", async lane => {
  const original = row("51", lane);
  const f = fixture(token => ({ resources: token ? [original] : Array.from({ length: 50 }, (_, index) => row(`${index}`, lane)), nextPageToken: token ? "" : "exact-signed-cursor" }), lane);
  await waitFor(() => expect(f.result.current.resources).toHaveLength(50));
  expect(f.requests).toEqual([{ token: "", lane, pageSize: 50 }]);
  await act(() => f.result.current.more());
  expect(f.result.current.resources[50]).toMatchObject({ id: "51", revision: 9n });
  expect(f.result.current.canWrite(original)).toBe(true);
  expect(f.requests[1]).toEqual({ token: "exact-signed-cursor", lane, pageSize: 50 });
});
it.each([Code.Unavailable, Code.InvalidArgument])("retains page one and explicitly retries the same failed/expired cursor (%s)", async code => {
  let fail = true;
  const f = fixture(token => { if (token && fail) throw new ConnectError("fixture", code); return { resources: [row(token ? "second" : "first")], nextPageToken: token ? "" : "exact" }; });
  await waitFor(() => expect(f.result.current.loaded).toBe(true));
  await act(() => f.result.current.more());
  expect(f.result.current.error).toBe(true);
  expect(f.result.current.resources.map(resource => resource.id)).toEqual(["first"]);
  expect(f.result.current.canWrite(f.result.current.resources[0]!)).toBe(false);
  fail = false;
  await act(async () => { f.result.current.retry(); });
  await waitFor(() => expect(f.result.current.resources).toHaveLength(2));
  expect(f.requests.map(request => request.token)).toEqual(["", "exact", "exact"]);
});
it("refreshes all reached pages using new cursors without changing original resource revisions", async () => {
  let cursor = "old";
  const f = fixture(token => ({ resources: [row(token ? "second" : "first")], nextPageToken: token ? "" : cursor }));
  await waitFor(() => expect(f.result.current.loaded).toBe(true));
  await act(() => f.result.current.more());
  cursor = "new";
  act(() => f.result.current.refresh());
  await waitFor(() => expect(f.requests.map(request => request.token)).toEqual(["", "old", "", "new"]));
  await waitFor(() => expect(f.result.current.loading).toBe(false));
  expect(f.result.current.resources.map(resource => [resource.id, resource.revision])).toEqual([["first", 9n], ["second", 9n]]);
});
it("fences a late continuation on background and reloads from the original first page", async () => {
  let finish!: (page: ConversationPage) => void;
  const f = fixture(token => token ? new Promise(resolve => { finish = resolve; }) : { resources: [row("first")], nextPageToken: "exact" });
  await waitFor(() => expect(f.result.current.loaded).toBe(true));
  let continuation!: Promise<void>;
  act(() => { continuation = f.result.current.more(); });
  await waitFor(() => expect(finish).toBeDefined());
  f.rerender({ enabled: false });
  expect(f.result.current.resources).toEqual([]);
  expect(f.result.current.canWrite(row("first"))).toBe(false);
  await act(async () => { finish({ resources: [row("late")], nextPageToken: "" }); await continuation; });
  expect(f.result.current.resources).toEqual([]);
  act(() => { f.cache.clear(); });
  f.rerender({ enabled: true });
  await waitFor(() => expect(f.result.current.resources.map(resource => resource.id)).toEqual(["first"]));
});
it("keeps malformed continuation incomplete and never adopts duplicate IDs", async () => {
  const f = fixture(token => ({ resources: [row("same")], nextPageToken: token ? "" : "exact" }));
  await waitFor(() => expect(f.result.current.loaded).toBe(true));
  await act(() => f.result.current.more());
  expect(f.result.current.error).toBe(true);
  expect(f.result.current.resources.map(resource => resource.id)).toEqual(["same"]);
});

it("does not publish an old profile's late continuation into a replacement cache", async () => {
  let finish!: (page: ConversationPage) => void;
  const original = fixture(token => token ? new Promise(resolve => { finish = resolve; }) : { resources: [row("old-profile")], nextPageToken: "old-exact" });
  await waitFor(() => expect(original.result.current.loaded).toBe(true));
  let pending!: Promise<void>;
  act(() => { pending = original.result.current.more(); });
  await waitFor(() => expect(finish).toBeDefined());
  const originalAuthority = original.result.current;
  original.unmount();
  expect(originalAuthority.canWrite(row("old-profile"))).toBe(false);
  const replacement = fixture(() => ({ resources: [row("new-profile")], nextPageToken: "" }));
  await waitFor(() => expect(replacement.result.current.loaded).toBe(true));
  await act(async () => { finish({ resources: [row("late-private")], nextPageToken: "" }); await pending; });
  expect(replacement.result.current.resources.map(resource => resource.id)).toEqual(["new-profile"]);
  expect(replacement.requests.map(request => request.token)).toEqual([""]);
});
