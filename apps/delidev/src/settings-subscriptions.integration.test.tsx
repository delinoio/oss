// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { document, encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("manages service-native subscription metadata through real authenticated RPC without Provider requests or implicit login", async () => {
  const config = createClient(ConfigurationService, fixture.transport);
  const account = (await config.saveConfiguration({ mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ alias: "ChatGPT account", subscription_service: "chatgpt", type: "subscription", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [], confirmed_exhausted: false }) })).resource!;
  const apiProvider = (await createClient(ResourceService, fixture.transport).listResources({ filter: { kind: EntityKind.PROVIDER } })).resources[0];
  await config.saveConfiguration({ mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ alias: "Unrelated API account", provider_id: apiProvider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "disconnected", quota: [], confirmed_exhausted: false }) });
  const unexpected = vi.fn();
  const transport: Transport = { ...fixture.transport, unary: (method, ...args) => {
    if (method.parent.name === "ProviderService" || ["ConnectAccount", "DisconnectAccount", "ValidateAccount", "RequestSubscription"].includes(method.name) || method.name === "ListResources" && (args[3] as { filter?: { kind?: EntityKind } })?.filter?.kind === EntityKind.PROVIDER) unexpected(method.name);
    return fixture.transport.unary(method, ...args);
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  function Harness() {
    const [visible, setVisible] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><button onClick={() => setVisible(true)}>Open Settings fixture</button><button onClick={() => setVisible(false)}>Leave Settings fixture</button><Settings visible={visible} /></QueryClientProvider></TransportProvider>;
  }
  render(<Harness />);
  const subscription = await screen.findByRole("article", { name: "ChatGPT account" });
  expect(subscription.querySelector("img")).toBeTruthy(); expect(within(subscription).getByText("Disconnected")).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Unrelated API account" })).toBeNull(); expect(screen.queryByLabelText("Search providers")).toBeNull();
  expect((screen.getByRole("button", { name: "Refresh all" }) as HTMLButtonElement).disabled).toBe(false);
 fireEvent.click(screen.getByRole("button",{name:"Refresh all"}));
 await waitFor(()=>expect((screen.getByRole("button",{name:"Refresh all"}) as HTMLButtonElement).disabled).toBe(false));
  expect(unexpected).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "More actions for ChatGPT account" })); fireEvent.click(screen.getByRole("button", { name: "Edit preferences" }));
  expect(screen.getByText("Subscription service: ChatGPT")).toBeTruthy(); expect(screen.queryByLabelText("Provider")).toBeNull();
  fireEvent.change(screen.getByLabelText("Account alias"), { target: { value: "Edited subscription" } }); fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  await screen.findByRole("article", { name: "Edited subscription" });
  const saved = (await createClient(ResourceService, fixture.transport).getResource({ kind: EntityKind.ACCOUNT, id: account.id })).resource!;
  expect(saved.schemaVersion).toBe(2); expect(document(saved)).toMatchObject({ alias: "Edited subscription", subscription_service: "chatgpt", recovery_notifications: false }); expect(document(saved)).not.toHaveProperty("provider_id");
  fireEvent.click(screen.getByRole("button", { name: "More actions for Edited subscription" })); fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  fireEvent.click(await screen.findByRole("button", { name: "Disconnect and delete account" }));
  await screen.findByRole("heading", { name: "No subscriptions yet" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("heading", { name: "Account configuration deleted" })).toBeNull();
  expect((screen.getByRole("button", { name: "Claude · Coming soon" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" })); fireEvent.click(screen.getByRole("button", { name: "Open Settings fixture" }));
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page"); expect(screen.queryByLabelText("Account name")).toBeNull(); expect(unexpected).not.toHaveBeenCalled();
}, 30000);

it("automatically closes API entry deletion and refreshes the current inventory through real authenticated RPC", async () => {
  const configurations = createClient(ConfigurationService, fixture.transport);
  const provider = (await configurations.saveConfiguration({ kind: EntityKind.PROVIDER, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ name: "Deletion keyless provider", endpoint: fixture.providerOrigin, protocol: "openai-chat", authentication: "keyless", discovery: false }) })).resource!;
  const metadata = { provider_id: provider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [], confirmed_exhausted: false };
  const account = (await configurations.saveConfiguration({ kind: EntityKind.ACCOUNT, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ ...metadata, alias: "Deleted API entry" }) })).resource!;
  await configurations.saveConfiguration({ kind: EntityKind.ACCOUNT, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ ...metadata, alias: "Retained API entry" }) });
  const cleanupRead = vi.fn();
  const transport: Transport = { ...fixture.transport, unary: (method, ...args) => {
    if (method.name === "GetAccountBrowserCleanup") cleanupRead();
    return fixture.transport.unary(method, ...args);
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Settings /></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  await screen.findByRole("article", { name: "Deleted API entry" });
  fireEvent.click(screen.getByRole("button", { name: "More actions for Deleted API entry" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete entry" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() => expect(screen.queryByRole("article", { name: "Deleted API entry" })).toBeNull());
  expect(screen.getByRole("heading", { name: "AI API Keys", level: 1 })).toBeTruthy();
  expect(await screen.findByRole("article", { name: "Retained API entry" })).toBeTruthy();
  expect(screen.queryByText("API key entry deleted")).toBeNull();
  expect(cleanupRead).not.toHaveBeenCalled();
  const remaining = await createClient(ResourceService, fixture.transport).listResources({ filter: { kind: EntityKind.ACCOUNT } });
  expect(remaining.resources.some(row => row.id === account.id)).toBe(false);
}, 30000);
