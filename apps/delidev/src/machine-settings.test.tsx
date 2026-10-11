// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, type ReactNode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId, type DiscoverHarnessesRequest, type Resource } from "@delinoio/delidev-api-client";
import { Harness } from "./configuration-fields";
import { document, encode } from "./documents";
import { useMachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";

const T0 = "2026-10-01T00:00:00Z", T1 = "2026-10-01T00:00:05Z", T2 = "2026-10-01T00:00:10Z";
function machine(id: string, revision = 7n, lastSeen: string | null = T0, path = "/original/codex") {
  return create(ResourceSchema, { id, kind: EntityKind.MACHINE, revision, schemaVersion: 1, documentJson: encode({ name: "Fixture Worker", last_seen: lastSeen ?? undefined, installations: [{ harness: Harness.Codex, explicit_path: path }] }) });
}
function fixture() {
  const initial = machine(newRequestId());
  let observed: Resource = initial;
  let failure: Error | undefined;
  const get = vi.fn(async () => { if (failure) throw failure; return { resource: observed }; });
  const discover = vi.fn(async (_request: DiscoverHarnessesRequest) => ({ machine: machine(initial.id, 8n, T1, "/acknowledged/codex") }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { getResource: get });
    router.service(WorkerService, { discoverHarnesses: discover });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  const hook = renderHook(() => useMachineSettingsController(initial, true), { wrapper });
  const refresh = async (row?: Resource, error?: Error) => {
    if (row) observed = row;
    failure = error;
    await act(async () => { await hook.result.current.refetch(); });
  };
  return { initial, ...hook, refresh, discover, get };
}

it("refreshes an equal-revision heartbeat and sends the original business revision", async () => {
  const value = fixture();
  await waitFor(() => expect(value.get).toHaveBeenCalled());
  await value.refresh(machine(value.initial.id, 7n, T1));
  expect(value.result.current.data.last_seen).toBe(T1);
  expect(value.result.current.current?.revision).toBe(7n);
  await act(async () => { await value.result.current.discovery.send({ mutation: { id: value.initial.id, expectedRevision: value.result.current.current!.revision, requestId: newRequestId() } }); });
  expect(value.discover.mock.calls[0][0].mutation?.expectedRevision).toBe(7n);
});

it("retains the highest accepted revision after stale and failed reads", async () => {
  const value = fixture();
  await value.refresh(machine(value.initial.id, 8n, T1, "/new/codex"));
  await value.refresh(machine(value.initial.id, 7n, T2));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(document(value.result.current.current).installations).toEqual([{ harness: Harness.Codex, explicit_path: "/new/codex" }]);
  expect(value.result.current.data.last_seen).toBe(T1);
  await value.refresh(undefined, new ConnectError("Fixture read failed", Code.Unavailable));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.readError).toBeTruthy();
});

it("merges only compatible heartbeat observations into acknowledged configuration", async () => {
  const value = fixture();
  await waitFor(() => expect(value.get).toHaveBeenCalled());
  await act(async () => { await value.result.current.discovery.send({ mutation: { id: value.initial.id, expectedRevision: 7n, requestId: newRequestId() } }); });
  await value.refresh(machine(value.initial.id, 7n, T2));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.data.last_seen).toBe(T1);
  await value.refresh(machine(value.initial.id, 8n, T2, "/read-must-not-replace/ack"));
  expect(value.result.current.data.last_seen).toBe(T2);
  expect(value.result.current.data.installations).toEqual([{ harness: Harness.Codex, explicit_path: "/acknowledged/codex" }]);
  expect(value.result.current.pending).toBe(true);
  await value.refresh(machine(value.initial.id, 8n, null));
  expect(value.result.current.data.last_seen).toBeUndefined();
  expect(value.result.current.current?.revision).toBe(8n);
});

it("preserves path drafts and their revision across heartbeat refreshes", async () => {
  const value = fixture();
  const paths = { [Harness.Codex]: "/draft/codex" };
  act(() => value.result.current.setEdit({ revision: 7n, paths }));
  await value.refresh(machine(value.initial.id, 7n, T1));
  expect(value.result.current.edit).toEqual({ revision: 7n, paths });
  expect(value.result.current.stale).toBe(false);
  expect(value.result.current.locked).toBe(true);
  await value.refresh(machine(value.initial.id, 8n, T2));
  expect(value.result.current.stale).toBe(true);
  expect(value.result.current.edit?.paths).toEqual(paths);
});

it.each(["foreign", "malformed", "failed"])("keeps retained observations and read restrictions for %s reads", async kind => {
  const value = fixture();
  await value.refresh(machine(value.initial.id, 7n, T1));
  const rejected = kind === "foreign" ? machine(newRequestId(), 9n, T2) : create(ResourceSchema, { ...machine(value.initial.id, 9n, T2), documentJson: encode({ installations: "invalid" }) });
  await value.refresh(kind === "failed" ? undefined : rejected, kind === "failed" ? new ConnectError("Fixture read failed", Code.Unavailable) : undefined);
  expect(value.result.current.current?.revision).toBe(7n);
  expect(value.result.current.data.last_seen).toBe(T1);
  expect(value.result.current.readError).toBeTruthy();
  expect(value.discover).not.toHaveBeenCalled();
});

it("preserves the original uncertain discovery request through heartbeat refresh", async () => {
  const value = fixture();
  await waitFor(() => expect(value.get).toHaveBeenCalled());
  value.discover.mockRejectedValueOnce(new ConnectError("Fixture lost acknowledgement", Code.Unavailable));
  const requestId = newRequestId();
  await act(async () => { await value.result.current.discovery.send({ mutation: { id: value.initial.id, expectedRevision: 7n, requestId }, verifyProtocol: true }); });
  expect(value.result.current.discovery.uncertain).toBe(true);
  await value.refresh(machine(value.initial.id, 7n, T1));
  expect(value.result.current.data.last_seen).toBe(T1);
  expect(value.result.current.pending).toBe(true);
  await act(async () => { await value.result.current.discovery.retry(); });
  expect(value.discover.mock.calls[1][0]).toEqual(value.discover.mock.calls[0][0]);
  expect(value.discover.mock.calls[1][0].mutation?.requestId).toBe(requestId);
});
