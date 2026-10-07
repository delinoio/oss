// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ApiProtocol, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId, type ChangeAccountApiFormatRequest, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationEditor } from "./settings";
import { MutationIntents } from "./mutation";
import { NotificationProvider } from "./toast-notifications";
import { document, encode } from "./documents";

function fixture(supported = true, cleanup = false) {
  const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, revision: 1n, schemaVersion: 3, documentJson: encode({ name: "Gateway", protocol: "openai-chat", authentication: "bearer", endpoint: "https://api.example.test/v1", api_formats: [{ protocol: "openai-chat", authentication: "bearer", endpoint: "https://api.example.test/v1" }, { protocol: "openai-responses", authentication: "bearer", endpoint: "https://api.example.test/v1" }, { protocol: "anthropic-messages", authentication: "keyless", endpoint: "http://127.0.0.1:1234/v1" }] }) });
  const original = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 3n, schemaVersion: 3, documentJson: encode({ alias: "Gateway key", type: "api", provider_id: provider.id, api_protocol: "openai-chat", enabled: true, exclude_automatic: false, recovery_notifications: true, health: cleanup ? "disconnected" : "ready", ...(cleanup ? { removal: { request_id: newRequestId() } } : { connection: { id: newRequestId(), authentication: "bearer", connected_at: "2026-10-07T00:00:00Z" } }) }) });
  let current = original;
  const save = vi.fn(async () => ({ resource: current }));
  const connect = vi.fn(), disconnect = vi.fn(), saved = vi.fn();
  const changeFormat = vi.fn(async (request: ChangeAccountApiFormatRequest) => {
    current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: encode({ ...document(current), api_protocol: "openai-responses", alias: request.alias, health: "unverified" }) });
    return { account: current, requestId: request.mutation?.requestId };
  });
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { getResource: request => ({ resource: request.id === provider.id ? provider : current }), listResources: () => ({ resources: [provider] }) });
    router.service(ProviderService, { listProviderInventory: () => ({ capabilities: [ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1, ...(supported ? [ProviderInventoryCapability.ACCOUNT_API_FORMAT_CHANGE_V1] : [])] }) });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(AccountService, { changeAccountApiFormat: changeFormat, connectAccount: connect, disconnectAccount: disconnect });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = render(<NotificationProvider><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationEditor kind={EntityKind.ACCOUNT} initial={original} active saved={saved} cancel={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider></NotificationProvider>);
  return { original, provider, view, client, changeFormat, save, connect, disconnect, saved, observe: async (resource: Resource) => { current = resource; await act(async () => { await client.invalidateQueries(); }); } };
}

it("saves a connected account format with its preferences and no disconnect or key input", async () => {
  const value = fixture();
  const select = screen.getByRole("combobox", { name: "API format" });
  await waitFor(() => expect(select.matches(":disabled")).toBe(false));
  expect(screen.getByText("Your saved API key is kept. The new format applies to future executions.")).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Anthropic Messages" })).toBeNull();
  expect(screen.queryByLabelText("API key")).toBeNull();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Kept key" } });
  fireEvent.change(select, { target: { value: "openai-responses" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(value.saved).toHaveBeenCalledTimes(1));
  expect(value.changeFormat).toHaveBeenCalledTimes(1);
  expect(value.changeFormat.mock.calls[0][0]).toMatchObject({ mutation: { id: value.original.id, expectedRevision: 3n }, apiProtocol: ApiProtocol.OPENAI_RESPONSES, alias: "Kept key", enabled: true, excludeAutomatic: false, recoveryNotifications: true });
  expect(value.save).not.toHaveBeenCalled(); expect(value.connect).not.toHaveBeenCalled(); expect(value.disconnect).not.toHaveBeenCalled();
});

it("adopts status-only revisions without discarding editable drafts", async () => {
  const value = fixture();
  await screen.findByRole("button", { name: "Save changes" });
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Retained draft" } });
  await value.observe(create(ResourceSchema, { ...value.original, revision: 4n, documentJson: encode({ ...document(value.original), health: "unverified" }) }));
  expect((screen.getByLabelText("Entry name") as HTMLInputElement).value).toBe("Retained draft");
  expect(screen.queryByText(/This entry changed elsewhere/)).toBeNull();
  fireEvent.change(screen.getByLabelText("API format"), { target: { value: "openai-responses" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(value.changeFormat).toHaveBeenCalledTimes(1));
  expect(value.changeFormat.mock.calls[0][0].mutation?.expectedRevision).toBe(4n);
});

it("retains the draft and blocks saving after an external preference change", async () => {
  const value = fixture(); await screen.findByRole("button", { name: "Save changes" });
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Retained draft" } });
  await value.observe(create(ResourceSchema, { ...value.original, revision: 4n, documentJson: encode({ ...document(value.original), alias: "External edit" }) }));
  expect(await screen.findByText(/This entry changed elsewhere/)).toBeTruthy();
  expect((screen.getByLabelText("Entry name") as HTMLInputElement).value).toBe("Retained draft");
  expect((screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.changeFormat).not.toHaveBeenCalled();
});

it.each(["unsupported", "cleanup"])("does not unlock a connected/retiring account during %s", async state => {
  fixture(state !== "unsupported", state === "cleanup");
  await screen.findByRole("option", { name: "OpenAI Responses" });
  expect(screen.getByRole("combobox", { name: "API format" }).matches(":disabled")).toBe(true);
});

it("retries only the original uncertain format request", async () => {
  const value = fixture();
  value.changeFormat.mockRejectedValueOnce(new ConnectError("Fixture lost response", Code.Unavailable));
  await screen.findByRole("button", { name: "Save changes" });
  fireEvent.change(screen.getByLabelText("API format"), { target: { value: "openai-responses" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.saved).toHaveBeenCalledTimes(1));
  expect(value.changeFormat.mock.calls).toHaveLength(2);
  expect(value.changeFormat.mock.calls[1][0]).toEqual(value.changeFormat.mock.calls[0][0]);
  expect(value.disconnect).not.toHaveBeenCalled();
});

it("rejects late completion after the editor lifetime ends", async () => {
  const value = fixture();
  let resolve!: (value: { account: Resource; requestId: string | undefined }) => void;
  value.changeFormat.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  await screen.findByRole("button", { name: "Save changes" });
  fireEvent.change(screen.getByLabelText("API format"), { target: { value: "openai-responses" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(value.changeFormat).toHaveBeenCalledTimes(1));
  value.view.unmount();
  await act(async () => { resolve({ requestId: value.changeFormat.mock.calls[0][0].mutation?.requestId, account: create(ResourceSchema, { ...value.original, revision: 4n, documentJson: encode({ ...document(value.original), api_protocol: "openai-responses" }) }) }); });
  expect(value.saved).not.toHaveBeenCalled(); expect(value.disconnect).not.toHaveBeenCalled();
});
