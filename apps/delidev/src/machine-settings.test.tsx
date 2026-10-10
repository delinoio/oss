// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { encode } from "./documents";
import { useMachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";

const t0 = "2026-10-09T10:00:00Z", t1 = "2026-10-09T10:00:05Z";
function fixture() {
  const id = newRequestId();
  const row = (revision: bigint, lastSeen = t0, path = "/original/codex") => create(ResourceSchema, { id, kind: EntityKind.MACHINE, schemaVersion: 1, revision, documentJson: encode({ name: "Original Runner", last_seen: lastSeen, installations: [{ harness: "codex", explicit_path: path, state: "unchecked" }] }) });
  const initial = row(7n);
  let reading = initial, failure: ConnectError | undefined;
  const get = vi.fn(async () => { if (failure) throw failure; return { resource: reading }; });
  const discovery = vi.fn(async (_request: unknown) => ({ machine: row(8n, t0, "/acknowledged/codex") }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { getResource: get });
    router.service(WorkerService, { discoverHarnesses: discovery });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  const hook = renderHook(() => useMachineSettingsController(initial, true), { wrapper });
  const refresh = async (next: Resource, error?: ConnectError) => {
    reading = next; failure = error;
    await act(async () => { await hook.result.current.refetch(); });
  };
  return { ...hook, row, initial, refresh, discovery, get };
}

it("refreshes equal-revision heartbeat without changing path drafts or outgoing revision", async () => {
  const value = fixture();
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  act(() => value.result.current.setEdit({ revision: 7n, paths: { codex: "/edited/codex" } }));
  await value.refresh(value.row(7n, t1));
  await waitFor(() => expect(value.result.current.data.last_seen).toBe(t1));
  expect(value.result.current.current?.revision).toBe(7n);
  expect(value.result.current.edit).toEqual({ revision: 7n, paths: { codex: "/edited/codex" } });
  expect(value.result.current.stale).toBe(false);
  await act(async () => { await value.result.current.discovery.send({ mutation: { id: value.initial.id, expectedRevision: value.result.current.edit!.revision, requestId: newRequestId() }, selectionsJson: encode({ executables: [{ harness: "codex", path: "/edited/codex" }] }) }); });
  expect(value.discovery.mock.calls[0][0]).toMatchObject({ mutation: { id: value.initial.id, expectedRevision: 7n } });
});

it("retains a newer successful observation through lower-revision and failed reads", async () => {
  const value = fixture();
  await value.refresh(value.row(8n, t1, "/new/codex"));
  await waitFor(() => expect(value.result.current.current?.revision).toBe(8n));
  await value.refresh(value.row(7n));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.data.last_seen).toBe(t1);
  await value.refresh(value.row(7n), new ConnectError("fixture read unavailable", Code.Unavailable));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.readError).toBeTruthy();
});

it("keeps acknowledged configuration until a compatible read refreshes its heartbeat", async () => {
  const value = fixture();
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  await act(async () => { await value.result.current.discovery.send({ mutation: { id: value.initial.id, expectedRevision: 7n, requestId: newRequestId() } }); });
  await waitFor(() => expect(value.result.current.current?.revision).toBe(8n));
  await value.refresh(value.row(7n, t1));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.data.installations).toMatchObject([{ explicit_path: "/acknowledged/codex" }]);
  await value.refresh(value.row(8n, t1, "/acknowledged/codex"));
  await waitFor(() => expect(value.result.current.data.last_seen).toBe(t1));
  expect(value.result.current.current?.revision).toBe(8n);
});

it("rejects malformed and foreign reads while retaining current observations and drafts", async () => {
  const value = fixture();
  await value.refresh(value.row(7n, t1));
  await waitFor(() => expect(value.result.current.data.last_seen).toBe(t1));
  act(() => value.result.current.setEdit({ revision: 7n, paths: { codex: "/edited/codex" } }));
  for (const invalid of [create(ResourceSchema, { ...value.row(9n), id: newRequestId() }), create(ResourceSchema, { ...value.row(9n), documentJson: encode({ disabled: "false" }) })]) {
    await value.refresh(invalid);
    expect(value.result.current.current?.revision).toBe(7n);
    expect(value.result.current.data.last_seen).toBe(t1);
    expect(value.result.current.edit?.paths.codex).toBe("/edited/codex");
    expect(value.result.current.readError).toBeTruthy();
    expect(value.result.current.stale).toBe(false);
  }
});

it("refreshes heartbeat without unlocking an uncertain original inspection", async () => {
  const value = fixture();
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  value.discovery.mockRejectedValueOnce(new ConnectError("fixture acknowledgment lost", Code.Unavailable));
  const request = { mutation: { id: value.initial.id, expectedRevision: 7n, requestId: newRequestId() }, verifyProtocol: true };
  await act(async () => { await value.result.current.discovery.send(request); });
  await waitFor(() => expect(value.result.current.discovery.uncertain).toBe(true));
  await value.refresh(value.row(7n, t1));
  await waitFor(() => expect(value.result.current.data.last_seen).toBe(t1));
  expect(value.result.current.pending).toBe(true);
  expect(value.result.current.discovery.uncertain).toBe(true);
  await act(async () => { await value.result.current.discovery.retry(); });
  expect(value.discovery.mock.calls[1][0]).toEqual(value.discovery.mock.calls[0][0]);
  expect(value.discovery.mock.calls[0][0]).toMatchObject(request);
});
