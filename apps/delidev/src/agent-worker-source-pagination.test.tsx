// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { AgentWorkerSourceWizard } from "./agent-worker-source-wizard";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";

function fixture() {
  const resource = (kind: EntityKind, data: Record<string, unknown>) => create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode(data) });
  const provider = resource(EntityKind.PROVIDER, { name: "Fixture API", protocol: "openai-responses", endpoint: "https://api.example.test/v1", enabled: true, authentication: "keyless", discovery: true });
  const account = resource(EntityKind.ACCOUNT, { alias: "Fixture account", type: "api", provider_id: provider.id, health: "ready", enabled: true, connection: { authentication: "keyless" } });
  const models = Array.from({ length: 5 }, (_, index) => resource(EntityKind.MODEL, { name: `Fixture model ${index}`, native_id: `fixture-${index}`, provider_id: provider.id, hidden: false, harnesses: [] }));
  const search = vi.fn(async (_request:unknown) => ({accountId:account.id,accountRevision:account.revision,providerId:provider.id,providerRevision:provider.revision,connectionId:"",models:models.map((_row,index)=>({nativeId:`fixture-${index}`,displayName:`Fixture model ${index}`,inputModalities:["text"]}))}));
  const get = vi.fn(async (request: { id: string }) => ({ resource: [provider, account, ...models].find(row => row.id === request.id) }));
  const save = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [{ providerId: provider.id, provider, displayName: "Fixture API", enabled: true }], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER] }), listEndpointModels: search });
    router.service(ResourceService, { getResource: get, listResources: () => ({ resources: [account] }) });
    router.service(ConfigurationService, { saveAgentWorker: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><AgentWorkerSourceWizard active={active} saved={() => {}} cancel={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>;
  const rendered = render(view());
  return { provider, account, models, search, get, save, client, rendered, view };
}
async function modelStep(value: ReturnType<typeof fixture>) {
  fireEvent.click(screen.getByRole("radio", { name: "Codex" }));
  const source = await screen.findByRole("combobox", { name: "Account source 1" });
  fireEvent.click(source); fireEvent.click(await screen.findByRole("option", { name: "Fixture API" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: /Fixture account/ }));
  await screen.findByRole("checkbox", { name: "Select Fixture account" });
  // Seeded display labels precede the independent exact account proof.
  await waitFor(() => expect(value.client.getQueryCache().getAll().some(query => {
    const resource = (query.state.data as { resource?: Resource } | undefined)?.resource;
    return query.state.status === "success" && query.state.fetchStatus === "idle" && resource?.id === value.account.id && resource.revision === value.account.revision;
  })).toBe(true));
  await act(async () => {});
  await waitFor(() => expect(screen.getByRole("button", { name: "Next" }).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  const input = await screen.findByRole("combobox", { name: "Model for Fixture API" });
  fireEvent.focus(input); await screen.findByRole("option", { name: /Fixture model 0/ });
  return input as HTMLInputElement;
}

it("selects an endpoint native ID without a saved Model read",async()=>{const value=fixture(),input=await modelStep(value);fireEvent.click(screen.getByRole("option",{name:/Fixture model 0/}));expect(input.value).toBe("fixture-0");expect(value.get.mock.calls.every(([request])=>!value.models.some(row=>row.id===request.id))).toBe(true);expect(value.save).not.toHaveBeenCalled();});
it("keeps selected native ID stable after endpoint refresh and an inactive visit",async()=>{const value=fixture(),input=await modelStep(value);fireEvent.click(screen.getByRole("option",{name:/Fixture model 0/}));value.rendered.rerender(value.view(false));value.rendered.rerender(value.view());expect(input.value).toBe("fixture-0");expect(value.save).not.toHaveBeenCalled();});

it("does not treat the seeded account label as independent source proof", async () => {
  const value = fixture();
  let release!: (response: { resource: Resource }) => void;
  value.get.mockImplementation(async request => request.id === value.account.id
    ? await new Promise(resolve => { release = resolve; })
    : { resource: [value.provider, ...value.models].find(row => row.id === request.id) });
  const advancing = modelStep(value);
  await screen.findByRole("checkbox", { name: "Select Fixture account" });
  await waitFor(() => expect(release).toBeTypeOf("function"));
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(screen.queryByRole("combobox", { name: "Model for Fixture API" })).toBeNull();
  expect(value.search).not.toHaveBeenCalled();
  await act(async () => release({ resource: value.account }));
  expect(await advancing).toBe(screen.getByRole("combobox", { name: "Model for Fixture API" }));
  expect(value.save).not.toHaveBeenCalled();
});
