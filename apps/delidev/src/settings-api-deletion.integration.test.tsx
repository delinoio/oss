// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("disconnects and deletes a real Go keyless account from one Settings confirmation", async () => {
  const configuration = createClient(ConfigurationService, fixture.transport);
  const provider = await configuration.saveConfiguration({ kind: EntityKind.PROVIDER, schemaVersion: 1, mutation: { requestId: newRequestId() }, documentJson: encode({ name: "Owned deletion provider", protocol: "openai-chat", endpoint: fixture.providerOrigin, authentication: "keyless", enabled: true }) });
  const saved = await configuration.saveConfiguration({ kind: EntityKind.ACCOUNT, schemaVersion: 1, mutation: { requestId: newRequestId() }, documentJson: encode({ alias: "Owned deletion entry", type: "api", provider_id: provider.resource!.id, enabled: true, health: "disconnected" }) });
  const connected = await createClient(AccountService, fixture.transport).connectAccount({ mutation: { id: saved.resource!.id, expectedRevision: saved.resource!.revision, requestId: newRequestId() }, keyless: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<TransportProvider transport={fixture.transport}><QueryClientProvider client={client}><Settings /></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  const row = (await screen.findByRole("heading", { name: "Owned deletion entry" })).closest("article")!;
  fireEvent.click(within(row).getByRole("button", { name: "More actions for Owned deletion entry" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete entry" }));
  const dialog = screen.getByRole("dialog", { name: "Delete entry" });
  await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "Keep entry" })));
  fireEvent.click(within(dialog).getByRole("button", { name: "Disconnect and delete entry" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Owned deletion entry" })).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Add AI API key" })));
  const resources = createClient(ResourceService, fixture.transport);
  await expect(resources.getResource({ kind: EntityKind.ACCOUNT, id: connected.account!.id })).rejects.toMatchObject({ code: 5 });
}, 30000);
