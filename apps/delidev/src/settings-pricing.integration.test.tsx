// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { UsageService, UsageAccountingProfile, ConfigurationService, EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { Usage } from "./usage";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("saves and inspects a real immutable model price through desktop settings and CLI", async () => {
  const { transport, providerOrigin, runCLI } = fixture;
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const provider = await save(EntityKind.PROVIDER, { name: "Pricing API", endpoint: providerOrigin, protocol: "openai-chat", authentication: "keyless", discovery: false, enabled: true });
  const model = await save(EntityKind.MODEL, { name: "Pricing model", provider_id: provider.id, native_id: "pricing-fixture", harnesses: ["codex"], manual: true, metadata_source: "user-declared" });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Usage active open={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  await screen.findByRole("option", { name: "Pricing model" });
  fireEvent.change(screen.getByLabelText("Model"), { target: { value: model.id } });
  fireEvent.click(screen.getByRole("button", { name: "Model details and token pricing" }));
  fireEvent.click(await screen.findByRole("button", { name: "Token pricing" }));
  await screen.findByText(/No pricing basis has been configured/);
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit token pricing" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  change("Currency", "USD"); change("Pricing source", "Owned explicit source"); change("As-of date", "2026-09-25"); change("Input rate per million", "0.000000001");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  // Pricing uses an inline settings task. A missing dialog does not signal a
  // completed save; the editor heading disappears after its mutation settles.
  await waitFor(() => expect(screen.queryByRole("heading", { name: "New pricing version" })).toBeNull());
  const usage = createClient(UsageService, transport);
  const original = await usage.getModelPricing({ modelId: model.id });
  expect(original.modelRevision).toBe(model.revision);
  expect(original.pricing?.basis?.inputPerMillion).toBe("0.000000001");
  expect(original.pricing?.basis?.outputPerMillion).toBeUndefined();
  const cli = JSON.parse(await runCLI(["usage", "pricing", "get", "--model-id", model.id]));
  expect(cli.result.pricing.id).toBe(original.pricing?.id);
  await waitFor(() => expect((screen.getByRole("button", { name: "Edit token pricing" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  change("Input rate per million", "2.5");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  await waitFor(() => expect(screen.queryByRole("heading", { name: "New pricing version" })).toBeNull());
  const retained = await usage.getPricingVersion({ id: original.pricing!.id });
  expect(retained.pricing?.basis?.inputPerMillion).toBe("0.000000001");
  const summary = await usage.getUsageSummary({ modelId: model.id, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 });
  expect(summary.estimates?.currencies).toEqual([]);
  expect(summary.totals?.responses).toBe(0);
}, 30000);
