// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { document, encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("keeps subscription-only metadata CRUD reachable, sends no native lifecycle RPC and resets filters on close", async () => {
  const config = createClient(ConfigurationService, fixture.transport);
  const provider = (await config.saveConfiguration({ mutation: { requestId: newRequestId() }, kind: EntityKind.PROVIDER, schemaVersion: 1, documentJson: encode({ name: "ChatGPT editable provider", protocol: "native-subscription", authentication: "subscription", endpoint: "", enabled: true, discovery: false }) })).resource!;
  const account = (await config.saveConfiguration({ mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ alias: "ChatGPT editable alias", provider_id: provider.id, type: "subscription", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "disconnected", quota: [], confirmed_exhausted: false }) })).resource!;
  const apiProvider = (await createClient(ResourceService, fixture.transport).listResources({ filter: { kind: EntityKind.PROVIDER } })).resources.find((entry) => document(entry).protocol !== "native-subscription")!;
  await config.saveConfiguration({ mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ alias: "Unrelated API account", provider_id: apiProvider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "disconnected", quota: [], confirmed_exhausted: false }) });
  const writes = vi.fn();
  const transport: Transport = { ...fixture.transport, unary: (method, ...args) => {
    if (["ConnectAccount", "DisconnectAccount", "ValidateAccount", "DiscoverModels"].includes(method.name)) writes(method.name);
    return fixture.transport.unary(method, ...args);
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  function Harness() {
    const [visible, setVisible] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><button onClick={() => setVisible(true)}>Open Settings fixture</button><button onClick={(event) => { event.currentTarget.focus(); setVisible(false); }}>Leave Settings fixture</button><Settings visible={visible} /></QueryClientProvider></TransportProvider>;
  }
  render(<Harness />);
  const subscription = await screen.findByRole("article", { name: "ChatGPT editable alias" });
  expect(subscription.querySelector("img")).toBeNull();
  expect(within(subscription).getByText("Disconnected")).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Unrelated API account" })).toBeNull();
  for (const name of ["Refresh all", "Refresh ChatGPT editable alias", "Disconnect ChatGPT editable alias", "ChatGPT · Coming soon", "Claude · Coming soon", "Grok · Coming soon"]) {
    const button = screen.getByRole("button", { name }) as HTMLButtonElement;
    expect(button.disabled).toBe(true); fireEvent.click(button);
  }
  expect(writes).not.toHaveBeenCalled();
  const advanced = screen.getByText("Advanced settings").closest("details")!;
  expect(advanced.open).toBe(false); fireEvent.click(screen.getByText("Advanced settings")); advanced.open = true;
  const providerDisclosure = screen.getByText("Subscription provider configurations").closest("details")!;
  providerDisclosure.open = true;
  expect(screen.getByRole("button", { name: "Add native subscription provider" })).toBeTruthy();
  expect(await screen.findByRole("button", { name: "Add subscription configuration" })).toBeTruthy();
  fireEvent.change(within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Search providers"), { target: { value: "unmatched native provider" } });
  await waitFor(() => expect(within(within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Filter accounts by provider")).queryByRole("option", { name: /ChatGPT editable provider/ })).toBeNull());
  expect(screen.getByRole("article", { name: "ChatGPT editable alias" })).toBeTruthy();
  fireEvent.change(within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Search providers"), { target: { value: "ChatGPT" } });
  await within(within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Filter accounts by provider")).findByRole("option", { name: /ChatGPT editable provider/ });
  fireEvent.change(within(screen.getByRole("region", { name: "AI subscription account settings" })).getByLabelText("Filter accounts by provider"), { target: { value: provider.id } });
  expect(await screen.findByText("Provider filter: ChatGPT editable provider")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(screen.getByRole("button", { name: "AI Subscription" }));
  expect(await screen.findByText("Provider filter: ChatGPT editable provider")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Clear provider filter" }));
  await waitFor(() => expect(screen.queryByText("Provider filter: ChatGPT editable provider")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: "More actions for ChatGPT editable alias" }));
  fireEvent.click(screen.getByRole("button", { name: "Edit preferences" }));
  fireEvent.change(screen.getByLabelText("Account alias"), { target: { value: "Edited subscription" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  await screen.findByRole("article", { name: "Edited subscription" });
  expect(document((await createClient(ResourceService, fixture.transport).getResource({ kind: EntityKind.ACCOUNT, id: account.id })).resource).alias).toBe("Edited subscription");
  fireEvent.click(screen.getByRole("button", { name: "More actions for Edited subscription" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  // The existing ConfigurationDeletion workflow keeps its explicit confirmation.
  fireEvent.click(await screen.findByRole("button", { name: "Confirm configuration deletion" }));
  await screen.findByRole("heading", { name: "No subscriptions yet" });
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  fireEvent.click(screen.getByRole("button", { name: "Open Settings fixture" }));
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
  expect((screen.getByText("Advanced settings").closest("details") as HTMLDetailsElement).open).toBe(false);
  expect(writes).not.toHaveBeenCalled();
}, 30000);
