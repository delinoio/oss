import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ProviderService, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Settings, ConfigurationEditor } from "./settings";
import { AccountConnection } from "./account-connection";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";

function resource(kind: EntityKind, value: Document, revision = 1n) { return create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision, documentJson: encode(value) }); }
function fixture(resources: Resource[]) {
  const save = vi.fn(async (_request: unknown) => ({ resource: resources[0] }));
  const connect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const disconnect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { listResources: (request) => ({ resources: resources.filter((row) => row.kind === request.filter?.kind) }), getResource: (request) => ({ resource: resources.find((row) => row.id === request.id) }) });
    router.service(AccountService, { getAccountStatus: (request) => ({ account: resources.find((row) => row.id === request.id) }), connectAccount: connect, disconnectAccount: disconnect });
    router.service(ProviderService, { listProviderPresets: () => ({ presetsJson: encode([{ id: "ollama", provider: { name: "Local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true }, key_guidance: "Run your local model server first.", compatibility: "Requires a compatible model." }]) }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { resources, save, connect, disconnect, client, view };
}
function input(value: unknown) { return value as { mutation: { requestId: string; expectedRevision: bigint }; documentJson: Uint8Array }; }

it("keeps a settings draft across closing the modal and retries the original provider document", async () => {
  const value = fixture([]);
  value.save.mockRejectedValueOnce(new ConnectError("acknowledgement lost", Code.Unavailable));
  const view = render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New Provider" }));
  await screen.findByRole("option", { name: "Local provider" });
  fireEvent.change(screen.getByRole("combobox", { name: "Provider preset" }), { target: { value: "ollama" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "My local provider" } });
  view.rerender(value.view(<Settings visible={false} close={() => {}} />));
  view.rerender(value.view(<Settings visible close={() => {}} />));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("My local provider");
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(0n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name: "My local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true });
});

it("preserves server-owned account observations during a preference edit", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider" });
  const observed = { alias: "Original", provider_id: provider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "ready", connection: { id: newRequestId(), authentication: "bearer", connected_at: "2026-09-25T00:00:00Z" }, quota: [{ id: "window", remaining: 0 }], confirmed_exhausted: true, validation: { state: "observed" }, catalog: { state: "stale" } };
  const account = resource(EntityKind.ACCOUNT, observed, 7n);
  const value = fixture([account, provider]);
  render(value.view(<ConfigurationEditor kind={EntityKind.ACCOUNT} initial={account} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Account alias" }), { target: { value: "Renamed" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(7n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ ...observed, alias: "Renamed" });
});

it("blocks stale settings writes without erasing the staged instructions", async () => {
  const initial = resource(EntityKind.TEMPLATE, { name: "Instructions", contents: "Original instructions" }, 3n);
  const value = fixture([create(ResourceSchema, { ...initial, revision: 4n })]);
  render(value.view(<ConfigurationEditor kind={EntityKind.TEMPLATE} initial={initial} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "My staged instructions" } });
  await screen.findByText(/This entry changed elsewhere/);
  fireEvent.submit(screen.getByRole("button", { name: "Save Instructions" }).closest("form")!);
  expect(value.save).not.toHaveBeenCalled();
  expect((screen.getByRole("textbox", { name: "Instructions" }) as HTMLTextAreaElement).value).toBe("My staged instructions");
});

it("retains a secret only for its exact uncertain connection and excludes it from read cache keys", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "API provider", authentication: "bearer" });
  const account = resource(EntityKind.ACCOUNT, { alias: "API account", provider_id: provider.id, type: "api", health: "disconnected" }, 5n);
  const value = fixture([account, provider]);
  value.connect.mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable));
  render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  const key = screen.getByLabelText("API key");
  await waitFor(() => expect((key as HTMLInputElement).disabled).toBe(false));
  fireEvent.change(key, { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect account" }));
  const retry = await screen.findByRole("button", { name: "Retry the same connection" });
  expect((key as HTMLInputElement).value).toBe("");
  expect(JSON.stringify(value.client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("fixture-only-secret");
  fireEvent.click(retry);
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(2));
  expect(value.connect.mock.calls[0][0]).toEqual(value.connect.mock.calls[1][0]);
  expect(value.connect.mock.calls[0][0]).toMatchObject({ mutation: { expectedRevision: 5n }, keyless: false });
  expect(new TextDecoder().decode((value.connect.mock.calls[0][0] as { apiKey: Uint8Array }).apiKey)).toBe("fixture-only-secret");
  await waitFor(() => expect(value.client.getMutationCache().getAll()).toHaveLength(0));
});

it("resumes pending credential cleanup using the original server-retained mutation", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider", authentication: "bearer" });
  const requestId = newRequestId();
  const account = resource(EntityKind.ACCOUNT, { alias: "Cleanup", provider_id: provider.id, type: "api", health: "disconnected", removal: { request_id: requestId, expected_revision: 8 } }, 12n);
  const value = fixture([account, provider]);
  render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Retry original credential cleanup" }));
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0]).toMatchObject({ mutation: { id: account.id, requestId, expectedRevision: 8n } });
  expect(value.connect).not.toHaveBeenCalled();
});
