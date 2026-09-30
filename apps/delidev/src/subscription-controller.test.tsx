// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { encode } from "./documents";

function fixture() {
  const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Native metadata", protocol: "native-subscription", authentication: "subscription", endpoint: "", enabled: true, discovery: false }) });
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 1, documentJson: encode({ alias: "Existing subscription", provider_id: provider.id, type: "subscription", enabled: true, health: "disconnected", quota: [] }) });
  const accounts = [account];
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
  let failure: Code | undefined;
  let release: (() => void) | undefined;
  const list = vi.fn((request) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? accounts : request.filter?.kind === EntityKind.PROVIDER ? [provider] : [] }));
  const inventory = vi.fn((_request: { query: string }) => {
    if (failure) throw new ConnectError("Capability fixture failure", failure);
    return { entries: [{ provider, providerId: provider.id, displayName: "Native metadata", enabled: true }], capabilities };
  });
  const lifecycle = vi.fn(() => ({}));
  const save = vi.fn(async (request) => {
    const result = create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: request.documentJson });
    accounts.push(result);
    await new Promise<void>((resolve) => { release = resolve; });
    return { requestId: request.mutation?.requestId, resource: result };
  });
  const transport = createRouterTransport((router) => {
    router.service(ProviderService, { listProviderInventory: inventory, listProviderPresets: () => ({ presetsJson: encode([]) }), discoverModels: lifecycle });
    router.service(ResourceService, { listResources: list, getResource: () => ({ resource: provider }) });
    router.service(AccountService, { connectAccount: lifecycle, disconnectAccount: lifecycle, validateAccount: lifecycle });
    router.service(ConfigurationService, { saveConfiguration: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  function Harness() {
    const [visible, setVisible] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><button onClick={() => setVisible(true)}>Open Settings fixture</button><Settings visible={visible} close={() => setVisible(false)} /></QueryClientProvider></TransportProvider>;
  }
  return { Harness, client, provider, list, inventory, save, lifecycle, fail: (code?: Code) => { failure = code; }, finish: () => release?.() };
}

it.each([Code.Unavailable, Code.PermissionDenied, Code.Unauthenticated])("keeps capability failure %s retryable without granting lifecycle or classifying it as Coming soon", async (code) => {
  const value = fixture(); value.fail(code); render(<value.Harness />);
  await screen.findByRole("button", { name: "Retry subscription read" });
  expect(screen.queryByText(/Update the selected server to manage subscriptions/)).toBeNull();
  expect(screen.queryByRole("heading", { name: "No subscriptions yet" })).toBeNull();
  expect(value.list.mock.calls.some(([request]) => request.filter?.kind === EntityKind.ACCOUNT)).toBe(false);
  value.fail(); fireEvent.click(screen.getByRole("button", { name: "Retry subscription read" }));
  await screen.findByRole("article", { name: "Existing subscription" });
  expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ accountType: 2, providerId: "", filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: "" } });
  for (const name of ["Refresh all", "Refresh Existing subscription", "Disconnect Existing subscription"]) {
    const button = screen.getByRole("button", { name }) as HTMLButtonElement;
    expect(button.disabled).toBe(true); fireEvent.click(button);
  }
  expect(value.lifecycle).not.toHaveBeenCalled();
});

it("retains subscriptions after a capability read failure", async () => {
  const value = fixture(); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  value.fail(Code.Unavailable);
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByText("Showing the last successfully loaded subscriptions.");
  expect(screen.getByRole("article", { name: "Existing subscription" })).toBeTruthy();
});

it("discards metadata drafts, filters and late acknowledgments on close while accepted server work continues", async () => {
  const value = fixture(); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  const advanced = screen.getByText("Advanced settings").closest("details")!;
  advanced.open = true;
  fireEvent.change(within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Filter accounts by provider"), { target: { value: value.provider.id } });
  fireEvent.change(screen.getByLabelText("Subscription provider"), { target: { value: value.provider.id } });
  fireEvent.change(screen.getByLabelText("Account name"), { target: { value: "Accepted subscription metadata" } });
  fireEvent.click(screen.getByRole("button", { name: "Add subscription configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Open Settings fixture" }));
  await screen.findByRole("article", { name: "Accepted subscription metadata" });
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  await act(async () => value.finish());
  expect(screen.getByRole("heading", { level: 1, name: "Instructions" })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1); expect(value.lifecycle).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "AI Subscription" }));
  expect((screen.getByText("Advanced settings").closest("details") as HTMLDetailsElement).open).toBe(false);
  expect((screen.getByLabelText("Account name") as HTMLInputElement).value).toBe("");
  expect((within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Filter accounts by provider") as HTMLSelectElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry the same subscription configuration" })).toBeNull();
});


it("applies provider search to bounded subscription creation choices without filtering account rows", async () => {
  const value = fixture();
  const other = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Other native provider", protocol: "native-subscription", authentication: "subscription", endpoint: "", enabled: true }) });
  const accountPage = value.list.getMockImplementation()!;
  value.list.mockImplementation((request) => request.filter?.kind === EntityKind.PROVIDER ? { resources: [value.provider, other] } : accountPage(request));
  render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  screen.getByText("Advanced settings").closest("details")!.open = true;
  const choices = screen.getByLabelText("Subscription provider");
  expect(within(choices).getByRole("option", { name: "Native metadata" })).toBeTruthy();
  expect(within(choices).getByRole("option", { name: "Other native provider" })).toBeTruthy();
  const region = screen.getByRole("region", { name: "AI subscription account settings" });
  fireEvent.change(within(region).getByLabelText("Search providers"), { target: { value: "OTHER" } });
  expect(within(choices).queryByRole("option", { name: "Native metadata" })).toBeNull();
  expect(within(choices).getByRole("option", { name: "Other native provider" })).toBeTruthy();
  await waitFor(() => expect(value.inventory.mock.calls.at(-1)?.[0]).toMatchObject({ query: "OTHER" }));
  expect(screen.getByRole("article", { name: "Existing subscription" })).toBeTruthy();
  for (const [request] of value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.ACCOUNT)) {
    expect(request).toMatchObject({ accountType: 2, providerId: "", filter: { pageToken: "" } });
  }
  expect(value.save).not.toHaveBeenCalled();
  expect(value.lifecycle).not.toHaveBeenCalled();
});
