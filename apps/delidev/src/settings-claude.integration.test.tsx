// SPDX-License-Identifier: Apache-2.0
import { resolve } from "node:path";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ConfigurationService, EntityKind, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("persists native Claude permission selection through the desktop and real Go configuration service", async () => {
  const { transport, providerOrigin } = fixture;
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const provider = await save(EntityKind.PROVIDER, { name: "Claude settings API", endpoint: providerOrigin, protocol: "anthropic-messages", authentication: "keyless", discovery: false });
  const model = await save(EntityKind.MODEL, { name: "Claude settings model", provider_id: provider.id, native_id: "claude-settings-fixture", harnesses: ["claude-code"], manual: true, metadata_source: "user-declared" });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  // Model choices wait for a provider-capability read and then model search.
  // Keep both real RPCs and exercise ordinary delayed replies deterministically;
  // the component-test library's default one-second wait is not a server SLA.
  const slowTransport: Transport = {
    ...transport,
    async unary(method, signal, timeoutMs, header, input, contextValues) {
      if ((method.name === "ListProviderInventory" && (input as { pageSize?: number }).pageSize === 200) || method.name === "SearchModels") {
        await new Promise(resolve => setTimeout(resolve, 600));
      }
      return transport.unary(method, signal, timeoutMs, header, input, contextValues);
    },
  };
  render(<TransportProvider transport={slowTransport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  change("Name", "Native Claude settings"); change("Harness", "claude-code");
  await screen.findByRole("option", { name: "Claude settings model" }, { timeout: 5000 });
  change("Model", model.id); change("Claude permission mode", "dontAsk");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("heading", { name: "Native Claude settings" });
  const resources = createClient(ResourceService, transport);
  const agents = await resources.listResources({ filter: { kind: EntityKind.AGENT } });
  const agent = agents.resources.find((row) => document(row).name === "Native Claude settings")!;
  expect(document(agent)).toMatchObject({ harness: "claude-code", options: { permission: "default", claude_permission: "dontAsk" } });
  const prior = document(agent);
  await expect(configurations.saveConfiguration({ kind: EntityKind.AGENT, mutation: { id: agent.id, expectedRevision: agent.revision, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ ...prior, options: { permission: "read-only", claude_permission: "plan" } }) })).rejects.toThrow();
  expect(document((await resources.getResource({ id: agent.id, kind: EntityKind.AGENT })).resource)).toEqual(prior);
}, 15000);

