// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { EntityKind, ResourceSchema, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { encode } from "./documents";
import { NavigationChain } from "./home-navigation";
import { HomeScope, refreshCreatedSessionNavigation, useNavigationQuery } from "./home-navigation-query";

const resource = (projectId = "", name = "Original") => create(ResourceSchema, { id: newRequestId(), projectId, kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ name, workspace: projectId ? "worktree" : "general-chat", archive: "active", outcome: "not-started" }) });
function fixture() {
  const project = newRequestId(), unrelated = newRequestId();
  const original = resource(project), added = resource(project, "Created");
  const global = new NavigationChain(), named = new NavigationChain(), other = new NavigationChain();
  const rows = [original];
  let failed = false;
  let held: (() => Promise<{ sessions: ReturnType<typeof resource>[] }>) | undefined;
  let release: (() => void) | undefined;
  const read = vi.fn(async (request: { projectId: string; includeArchived: boolean }) => {
    if (failed && request.projectId === project) throw new ConnectError("Read unavailable", Code.Unavailable);
    if (held && request.projectId === project) { const pending = held; held = undefined; return pending(); }
    return { sessions: request.projectId === unrelated ? [] : rows.filter(row => !request.projectId || row.projectId === request.projectId) };
  });
  const transport = createRouterTransport(router => router.service(SessionService, { listSessions: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Observers() {
    useNavigationQuery(global, HomeScope.Sessions, "", false, true);
    useNavigationQuery(named, HomeScope.Sessions, project, true, true);
    useNavigationQuery(other, HomeScope.Sessions, unrelated, false, true);
    return null;
  }
  render(<QueryClientProvider client={client}><TransportProvider transport={transport}><Observers /></TransportProvider></QueryClientProvider>);
  return { project, unrelated, original, added, rows, read, global, named, other, client, transport, fail: (value: boolean) => { failed = value; }, holdNamed: () => {
    const retained = [...rows];
    held = () => new Promise(resolve => { release = () => resolve({ sessions: retained }); });
  }, release: () => release?.() };
}

it("refreshes global and the accepted project immediately while preserving archive scope and accepted pages", async () => {
  const f = fixture();
  await waitFor(() => expect(f.named.getSnapshot().rows.map(row => row.id)).toEqual([f.original.id]));
  await waitFor(() => expect(f.other.getSnapshot().loaded).toBe(true));
  const unrelatedReads = f.read.mock.calls.filter(([request]) => request.projectId === f.unrelated).length;
  const tokens = f.named.getSnapshot().pages.map(page => page.token);
  f.rows.push(f.added);
  await act(() => refreshCreatedSessionNavigation(f.client, f.transport, f.added));
  expect(f.named.getSnapshot().rows.map(row => row.id)).toEqual([f.original.id, f.added.id]);
  expect(f.global.getSnapshot().rows.map(row => row.id)).toEqual([f.original.id, f.added.id]);
  expect(f.named.getSnapshot().pages.map(page => page.token)).toEqual(tokens);
  expect(f.read.mock.calls.filter(([request]) => request.projectId === f.unrelated)).toHaveLength(unrelatedReads);
  expect(f.read.mock.calls.filter(([request]) => request.projectId === f.project).every(([request]) => request.includeArchived)).toBe(true);
});

it("recovers the original retained read failure on explicit creation invalidation without replaying creation", async () => {
  const f = fixture();
  await waitFor(() => expect(f.named.getSnapshot().loaded).toBe(true));
  f.fail(true);
  await act(() => refreshCreatedSessionNavigation(f.client, f.transport, f.added));
  expect(f.named.getSnapshot().error).toBeTruthy();
  expect(f.named.getSnapshot().rows.map(row => row.id)).toEqual([f.original.id]);
  f.rows.push(f.added); f.fail(false);
  await act(() => refreshCreatedSessionNavigation(f.client, f.transport, f.added));
  expect(f.named.getSnapshot().error).toBeUndefined();
  expect(f.named.getSnapshot().rows.map(row => row.id)).toEqual([f.original.id, f.added.id]);
  expect(f.read.mock.calls.filter(([request]) => request.projectId === f.project).every(([request]) => request.includeArchived)).toBe(true);
});

it("keeps General Chat and unknown-project fallback on the global scope without refreshing unrelated named chains", async () => {
  const f = fixture();
  await waitFor(() => expect(f.named.getSnapshot().loaded).toBe(true));
  const namedReads = f.read.mock.calls.filter(([request]) => request.projectId === f.project).length;
  const general = resource(), unknown = resource(newRequestId());
  f.rows.push(general);
  await act(() => refreshCreatedSessionNavigation(f.client, f.transport, general));
  expect(f.global.getSnapshot().rows.some(row => row.id === general.id)).toBe(true);
  f.rows.push(unknown);
  await act(() => refreshCreatedSessionNavigation(f.client, f.transport, unknown));
  expect(f.global.getSnapshot().rows.some(row => row.id === unknown.id)).toBe(true);
  expect(f.read.mock.calls.filter(([request]) => request.projectId === f.project)).toHaveLength(namedReads);
});

it("serializes accepted-creation refresh after an already pending original read", async () => {
  const f = fixture();
  await waitFor(() => expect(f.named.getSnapshot().loaded).toBe(true));
  f.holdNamed();
  const pending = refreshCreatedSessionNavigation(f.client, f.transport, f.original);
  await waitFor(() => expect(f.named.getSnapshot().loading).toBeTruthy());
  f.rows.push(f.added);
  const created = refreshCreatedSessionNavigation(f.client, f.transport, f.added);
  await act(async () => { f.release(); await Promise.all([pending, created]); });
  expect(f.named.getSnapshot().rows.map(row => row.id)).toEqual([f.original.id, f.added.id]);
  expect(f.named.getSnapshot().pages.map(page => page.token)).toEqual([""]);
});
