// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, NativeModelService, ProviderService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useModelPages, useNativeModelPages } from "./model-pagination";

it("bounds complete model documents to three pages and restores the original reached token", async () => {
  const models = Array.from({ length: 5 }, () => create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.MODEL }));
  const read = vi.fn(async (request: { pageToken: string }) => {
    const index = Number(request.pageToken || 0);
    return { models: [models[index]], nextPageToken: index < 4 ? String(index + 1) : "" };
  });
  const transport = createRouterTransport(router => router.service(ProviderService, { searchModels: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() { const query = useModelPages("", true); return <><output>{query.rows.length}:{query.payloadPages.length}</output><button onClick={query.append}>Append</button><button onClick={() => query.restore("")}>Restore</button></>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><View /></QueryClientProvider></TransportProvider>);
  await screen.findByText("1:1");
  for (let index = 2; index <= 5; index++) { fireEvent.click(screen.getByText("Append")); await screen.findByText(`${index}:${Math.min(index, 3)}`); }
  fireEvent.click(screen.getByText("Restore")); await waitFor(() => expect(read).toHaveBeenCalledTimes(6));
  expect(read.mock.calls.map(([request]) => request.pageToken)).toEqual(["", "1", "2", "3", "4", ""]);
  expect(client.getQueryCache().getAll().every(query => query.state.data === null)).toBe(true);
});
it("rejects a foreign native observation page and retries only its original token", async () => {
  const source = newRequestId(), job = create(ResourceSchema, { id: source, revision: 2n, kind: EntityKind.JOB });
  const read = vi.fn(async (request: { pageToken: string }) => ({ job: request.pageToken ? { ...job, id: newRequestId() } : job, modelsJson: encode([{ id: "picker", model: "executable", display_name: "Fixture" }]), nextPageToken: request.pageToken ? "" : "original" }));
  const transport = createRouterTransport(router => router.service(NativeModelService, { listNativeModels: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() { const query = useNativeModelPages(source, true); return <><output>{query.rows.length}:{query.error ? "failed" : "ready"}</output><button onClick={query.append}>Append</button><button onClick={query.retry}>Retry</button></>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><View /></QueryClientProvider></TransportProvider>);
  await screen.findByText("1:ready"); fireEvent.click(screen.getByText("Append")); await screen.findByText("1:failed");
  fireEvent.click(screen.getByText("Append")); expect(read).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByText("Retry")); await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  expect(read.mock.calls.map(([request]) => request.pageToken)).toEqual(["", "original", "original"]);
});

it("provider envelope pages deduplicate newer revisions in the original visible position", async () => {
  const { ApiProviderSettings } = await import("./provider-model-settings");
  const { MutationIntents } = await import("./mutation");
  const { ProviderInventoryCapability } = await import("@delinoio/delidev-api-client");
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER];
  const id = newRequestId(), secondId = newRequestId();
  const entry = (id: string, name: string, revision: bigint) => ({ providerId: id, displayName: name, provider: create(ResourceSchema, { id, kind: EntityKind.PROVIDER, revision, schemaVersion: 1, documentJson: encode({ name }) }) });
  const read = vi.fn(async (request: { enabledOnly: boolean; pageToken: string }) => ({ capabilities, entries: request.enabledOnly ? [] : request.pageToken ? [entry(id, "Updated provider", 2n), entry(secondId, "Second provider", 1n)] : [entry(id, "Original provider", 1n)], nextPageToken: request.enabledOnly || request.pageToken ? "" : "next" }));
  const transport = createRouterTransport(router => router.service(ProviderService, { listProviderInventory: read, listProviderPresets: () => ({ presetsJson: encode([]) }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ApiProviderSettings active changed={() => {}} createCustom={() => {}} editCustom={() => {}} manageAccounts={() => {}} addAccount={() => {}} deleteCustom={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  await screen.findByRole("heading", { name: "Original provider" });
  fireEvent.click(screen.getByRole("button", { name: /^Load more/ }));
  await screen.findByRole("heading", { name: "Second provider" });
  expect(screen.getAllByRole("heading", { name: "Updated provider" })).toHaveLength(1);
  expect(screen.queryByRole("heading", { name: "Original provider" })).toBeNull();
  expect(read.mock.calls.filter(([request]) => !request.enabledOnly).map(([request]) => request.pageToken)).toEqual(["", "next"]);
});
