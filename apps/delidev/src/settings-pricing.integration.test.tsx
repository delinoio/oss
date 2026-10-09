// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { UsageService, ConfigurationService, EntityKind, TokenPricingMode, newRequestId } from "@delinoio/delidev-api-client";
import { Usage } from "./usage";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

async function choose(control: HTMLElement, name: string | RegExp) {
  await waitFor(() => expect(control.matches(":disabled")).toBe(false));
  fireEvent.click(control);
  const popup = window.document.getElementById(control.getAttribute("aria-controls")!)!;
  const option = await within(popup).findByRole("option", { name });
  const id = option.dataset.pickerId;
  fireEvent.click(option);
  await waitFor(() => expect(control.dataset.value).toBe(id));
}

it("saves and inspects a real immutable model price through desktop settings and CLI", async () => {
  const { transport, providerOrigin, runCLI } = fixture;
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const provider = await save(EntityKind.PROVIDER, { name: "Pricing API", endpoint: providerOrigin, protocol: "openai-chat", authentication: "keyless", discovery: false });
  const model = {providerId:provider.id,nativeId:"pricing-fixture"};
  const usage = createClient(UsageService, transport);
  await usage.setTokenPricingMode({model,mode:TokenPricingMode.MANUAL,expectedProviderRevision:provider.revision,expectedPolicyRevision:0n,requestId:newRequestId()});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Usage active open={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("tab", { name: "Model prices" }));
  await choose(screen.getByRole("combobox", { name: "Pricing model" }), "Pricing API · pricing-fixture");
  await screen.findByText(/No exact reference rate is available/);
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit token pricing" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  change("Currency", "USD"); change("Pricing source", "Owned explicit source"); change("As-of date", "2026-09-25"); change("Input rate per million", "0.000000001");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  // Pricing uses an inline settings task. A missing dialog does not signal a
  // completed save; the editor heading disappears after its mutation settles.
  await waitFor(() => expect(screen.queryByRole("heading", { name: "New pricing version" })).toBeNull());
  const original = await usage.getTokenPricing({ model });
  expect(original.providerRevision).toBe(provider.revision);
  expect(original.pricing?.basis?.inputPerMillion).toBe("0.000000001");
  expect(original.pricing?.basis?.outputPerMillion).toBeUndefined();
  const cli = JSON.parse(await runCLI(["usage", "pricing", "get", "--provider-id", provider.id, "--native-id", model.nativeId]));
  expect(cli.result.pricing.id).toBe(original.pricing?.id);
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit token pricing" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  change("Input rate per million", "2.5");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  await waitFor(() => expect(screen.queryByRole("heading", { name: "New pricing version" })).toBeNull());
  const retained = await usage.getPricingVersion({ id: original.pricing!.id });
  expect(retained.pricing?.basis?.inputPerMillion).toBe("0.000000001");
  const summary = await usage.getUsageSummary({ model });
  expect(summary.estimates?.currencies).toEqual([]);
  expect(summary.totals?.responses).toBe(0);
}, 30000);
