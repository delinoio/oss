// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { ProviderInventoryCapability, ProviderPresetId, ProviderService, EntityKind, ResourceService } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("starts with hosted presets on without accounts or models and retains identity across off/on", async () => {
  const { transport } = fixture;
  const providers = createClient(ProviderService, transport);
  const initial = await providers.listProviderInventory({ pageSize: 50 });
  expect(initial.capabilities).toEqual(expect.arrayContaining([ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER]));
  expect(initial.entries.filter((entry) => entry.presetId !== ProviderPresetId.UNSPECIFIED)).toHaveLength(9);
  expect(initial.entries.filter((entry) => [ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId)).every((entry) => !entry.enabled && !entry.providerId && entry.accountCountsAvailable)).toBe(true);
  expect(initial.entries.filter((entry) => ![ProviderPresetId.OLLAMA, ProviderPresetId.LM_STUDIO, ProviderPresetId.VLLM].includes(entry.presetId)).every((entry) => entry.enabled && !!entry.providerId && entry.accountCountsAvailable && entry.totalAccounts === 0n && entry.connectedAccounts === 0n)).toBe(true);

  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const turnOff = await screen.findByRole("switch", { name: "Turn off OpenAI" });
  await waitFor(() => expect((turnOff as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByText("Account required")).toBeNull();
  let inventory = await providers.listProviderInventory({ pageSize: 50 });
  let saved = inventory.entries.find((entry) => entry.presetId === ProviderPresetId.OPENAI)!;
  expect(saved.enabled).toBe(true);
  expect(saved.providerId).not.toBe("");
  expect(saved.totalAccounts).toBe(0n);
  expect((await createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.ACCOUNT } })).resources).toHaveLength(0);
  expect((await providers.searchModels({ pageSize: 50 })).models).toHaveLength(0);

  fireEvent.click(turnOff);
  const turnOnAgain = await screen.findByRole("switch", { name: "Turn on OpenAI" });
  await waitFor(() => expect((turnOnAgain as HTMLButtonElement).disabled).toBe(false));
  inventory = await providers.listProviderInventory({ pageSize: 50 });
  saved = inventory.entries.find((entry) => entry.presetId === ProviderPresetId.OPENAI)!;
  expect(saved.enabled).toBe(false);
  const retainedID = saved.providerId;
  fireEvent.click(turnOnAgain);
  await screen.findByRole("switch", { name: "Turn off OpenAI" });
  saved = (await providers.listProviderInventory({ pageSize: 50 })).entries.find((entry) => entry.presetId === ProviderPresetId.OPENAI)!;
  expect(saved.providerId).toBe(retainedID);
  expect(saved.enabled).toBe(true);
}, 30000);

