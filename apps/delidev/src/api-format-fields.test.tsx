// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { apiFormatToWire, configurationSchemaVersion, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId, type APIFormatId, type Resource } from "@delinoio/delidev-api-client";
import { AccountAPIFormatField, ProviderAPIFormatFields } from "./api-format-fields";
import { document, encode, type Document } from "./documents";

const profiles = [{ protocol: "openai-responses", endpoint: "https://api.example.test/v1", authentication: "bearer" }, { protocol: "openai-chat", endpoint: "https://chat.example.test/v1", authentication: "bearer" }, { protocol: "anthropic-messages", endpoint: "http://127.0.0.1:1234/v1", authentication: "keyless" }];
const row = (kind: EntityKind, data: Document) => create(ResourceSchema, { kind, id: newRequestId(), revision: 1n, schemaVersion: configurationSchemaVersion(kind, data), documentJson: encode(data) });
const provider = row(EntityKind.PROVIDER, { name: "Gateway", ...profiles[0], api_formats: profiles });

function fixture(initial?: Resource, accounts: Resource[] = [], supported = true, providerForm = false, keepsKey = false) {
  const blocked = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(ProviderService, { listProviderInventory: () => ({ capabilities: supported ? [ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1, ...(keepsKey ? [ProviderInventoryCapability.ACCOUNT_API_FORMAT_CHANGE_V1] : [])] : [] }) });
    router.service(ResourceService, { getResource: () => ({ resource: provider }), listResources: request => {
      const filtered = accounts.filter(account => !request.apiProtocol || apiFormatToWire((document(account).api_protocol || document(provider).protocol) as APIFormatId) === request.apiProtocol);
      return { resources: filtered.slice(0, request.filter?.pageSize || 200), nextPageToken: filtered.length > (request.filter?.pageSize || 200) ? "fixture-next" : "" };
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  function Draft() {
    const [data, change] = useState(initial ? document(initial) : { name: "New gateway", protocol: "openai-responses", authentication: "bearer", endpoint: "", api_formats: [] });
    const Field = providerForm ? ProviderAPIFormatFields : AccountAPIFormatField;
    return <><Field data={data} change={change} active initial={initial} saveBlocked={blocked} /><output data-testid="draft">{JSON.stringify(data)}</output></>;
  }
  const view = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Draft /></QueryClientProvider></TransportProvider>);
  return { view, blocked };
}

it("requires one provider format and exposes independently configured cards", async () => {
  const value = fixture(undefined, [], true, true);
  const responses = screen.getByRole("checkbox", { name: "OpenAI Responses" });
  await waitFor(() => expect(responses.matches(":disabled")).toBe(false));
  expect(value.blocked).toHaveBeenLastCalledWith(true);
  fireEvent.click(responses);
  fireEvent.change(screen.getByRole("textbox", { name: "API base URL" }), { target: { value: profiles[0].endpoint } });
  fireEvent.click(screen.getByRole("checkbox", { name: "OpenAI Chat Completions" }));
  fireEvent.change(screen.getAllByRole("textbox", { name: "API base URL" })[1], { target: { value: profiles[1].endpoint } });
  expect(JSON.parse(screen.getByTestId("draft").textContent!).api_formats).toEqual(profiles.slice(0, 2));
  expect(value.blocked).toHaveBeenLastCalledWith(false);
  expect(screen.getByRole("checkbox", { name: "Anthropic Messages" }).getAttribute("checked")).toBeNull();
});

it("locks an account-referenced URL and authentication even while disconnected", async () => {
  const account = row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, api_protocol: "openai-responses", health: "disconnected" });
  fixture(provider, [account], true, true);
  await screen.findByText("An existing entry uses this profile. Its URL and authentication cannot be changed or removed.");
  expect(screen.getByRole("checkbox", { name: "OpenAI Responses" }).matches(":disabled")).toBe(true);
  expect(screen.getAllByRole("textbox", { name: "API base URL" })[0].matches(":disabled")).toBe(true);
  expect(screen.getAllByRole("textbox", { name: "API base URL" })[1].matches(":disabled")).toBe(false);
});

it("checks references in each format before pagination without blocking providers with many accounts", async () => {
  const accounts = Array.from({ length: 201 }, () => row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, api_protocol: "openai-chat", health: "disconnected" }));
  accounts.push(row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, health: "disconnected" }));
  const value = fixture(provider, accounts, true, true);
  await waitFor(() => expect(value.blocked).toHaveBeenLastCalledWith(false));
  expect(screen.getByRole("checkbox", { name: "OpenAI Responses" }).matches(":disabled")).toBe(true);
  expect(screen.getByRole("checkbox", { name: "OpenAI Chat Completions" }).matches(":disabled")).toBe(true);
  expect(screen.getByRole("checkbox", { name: "Anthropic Messages" }).matches(":disabled")).toBe(false);
});

it.each(["connected", "cleanup", "unsupported"])("blocks account format selection during %s", async state => {
  const account = row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, api_protocol: "openai-chat", health: state === "connected" ? "unverified" : "disconnected", ...(state === "connected" ? { connection: { id: newRequestId() } } : state === "cleanup" ? { removal: { request_id: newRequestId() } } : {}) });
  fixture(account, [], state !== "unsupported");
  await screen.findByRole("option", { name: "OpenAI Chat Completions" });
  expect(screen.getByRole("combobox", { name: "API format" }).matches(":disabled")).toBe(true);
});

it("allows a cleaned account to change format without changing credential ownership", async () => {
  const account = row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, api_protocol: "openai-chat", health: "disconnected", alias: "Gateway key" });
  const value = fixture(account);
  const select = screen.getByRole("combobox", { name: "API format" });
  await waitFor(() => expect(select.matches(":disabled")).toBe(false));
  expect(screen.queryByRole("option", { name: "Anthropic Messages" })).toBeNull();
  fireEvent.change(select, { target: { value: "openai-responses" } });
  expect(JSON.parse(screen.getByTestId("draft").textContent!).api_protocol).toBe("openai-responses");
  expect(value.blocked).toHaveBeenLastCalledWith(false);
});

it("locks retained provider profiles after a key-preserving format change", async () => {
  const account = row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, api_protocol: "openai-chat", retained_connections: [{ connection: { api_format: profiles[0] } }] });
  const value = fixture(provider, [account], true, true, true);
  await waitFor(() => expect(value.blocked).toHaveBeenLastCalledWith(false));
  expect(screen.getByRole("checkbox", { name: "OpenAI Responses" }).matches(":disabled")).toBe(true);
  expect(screen.getByRole("checkbox", { name: "OpenAI Chat Completions" }).matches(":disabled")).toBe(true);
  expect(screen.getByRole("checkbox", { name: "Anthropic Messages" }).matches(":disabled")).toBe(false);
});

it("keeps provider saves blocked when retained reference metadata is malformed", async () => {
  const account = row(EntityKind.ACCOUNT, { type: "api", provider_id: provider.id, api_protocol: "openai-chat", retained_connections: [{ connection: { api_format: { protocol: "unknown" } } }] });
  const value = fixture(provider, [account], true, true, true);
  await screen.findByRole("button", { name: "Retry profile checks" });
  expect(screen.getByText("Entry references are loading or could not be read completely. Profile editing is paused.")).toBeTruthy();
  await waitFor(() => expect(value.blocked).toHaveBeenLastCalledWith(true));
  expect(screen.getByRole("checkbox", { name: "OpenAI Responses" }).matches(":disabled")).toBe(true);
});
