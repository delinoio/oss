// SPDX-License-Identifier: Apache-2.0
import { resolve } from "node:path";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";
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

it("persists native Claude permission selection through the desktop and real Go configuration service", async () => {
  const { transport, providerOrigin } = fixture;
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) => (await configurations.saveConfiguration({ kind, mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(value) })).resource!;
  const provider = await save(EntityKind.PROVIDER, { name: "Claude settings API", endpoint: providerOrigin, protocol: "anthropic-messages", authentication: "keyless", discovery: false });
  const account = await save(EntityKind.ACCOUNT, { alias: "Claude API account", type: "api", provider_id: provider.id, enabled: true, health: "disconnected" });
  await createClient(AccountService, transport).connectAccount({ mutation: { id: account.id, expectedRevision: account.revision, requestId: newRequestId() }, keyless: true });

  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  // Model choices wait for a provider-capability read and then endpoint metadata.
  // Keep both real RPCs and exercise ordinary delayed replies deterministically;
  // the component-test library's default one-second wait is not a server SLA.
  const slowTransport: Transport = {
    ...transport,
    async unary(method, signal, timeoutMs, header, input, contextValues) {
      if ((method.name === "ListProviderInventory" && (input as { pageSize?: number }).pageSize === 200) || method.name === "ListEndpointModels") {
        await new Promise(resolve => setTimeout(resolve, 600));
      }
      return transport.unary(method, signal, timeoutMs, header, input, contextValues);
    },
  };
  render(<TransportProvider transport={slowTransport}><QueryClientProvider client={client}><MutationIntents><Settings /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
  const change = (name: string, value: string) => fireEvent.change(screen.getByLabelText(name), { target: { value } });
  await waitFor(() => expect((screen.getByRole("radio", { name: "Codex" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("radio", { name: "Claude Code" }));
  const next = async () => {
    await act(async () => {});
    const button = screen.getByRole("button", { name: "Next" });
    await waitFor(() => expect(button.matches(":disabled")).toBe(false));
    fireEvent.click(button);
  };
  await choose(screen.getByRole("combobox", { name: "Account source 1" }), "Claude settings API");
  fireEvent.click(await screen.findByRole("checkbox", { name: /Claude API account/ }));
  await waitFor(() => expect(window.document.querySelector("[data-source-group] .worker-routing ol strong")?.textContent).toBe("Claude API account"));
  await next();
  fireEvent.focus(await screen.findByRole("combobox", { name: /^Model for / }));
  fireEvent.change(screen.getByRole("combobox", { name: /^Model for / }), {target:{value:"claude-settings-fixture"}});
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); await next();
  change("Name", "Native Claude settings"); await choose(screen.getByRole("combobox", { name: "Claude permission mode" }), "dontAsk");
  await waitFor(() => expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("heading", { name: "Native Claude settings" });
  const resources = createClient(ResourceService, transport);
  const agents = await resources.listResources({ filter: { kind: EntityKind.AGENT } });
  const agent = agents.resources.find((row) => document(row).name === "Native Claude settings")!;
  expect(document(agent)).toMatchObject({ harness: "claude-code", options: { permission: "default", claude_permission: "dontAsk" } });
  const prior = document(agent);
  const retained = { ...prior, options: { permission: "read-only", claude_permission: "plan" } };
  await configurations.saveConfiguration({ kind: EntityKind.AGENT, mutation: { id: agent.id, expectedRevision: agent.revision, requestId: newRequestId() }, schemaVersion: 4, documentJson: encode(retained) });
  expect(document((await resources.getResource({ id: agent.id, kind: EntityKind.AGENT })).resource)).toEqual(retained);
}, 15000);
