// SPDX-License-Identifier: Apache-2.0
import { resolve } from "node:path";
import { type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("saves and renames GitHub profiles through the real Go server and CLI", async () => {
  const { transport, runCLI } = fixture;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  // Exercise the hosted CI timeout with sequential save and list latency. The
  // fixture must wait for the actual refreshed profile, without replaying saves.
  const slowTransport: Transport = {
    ...transport,
    async unary(method, signal, timeoutMs, header, input, contextValues) {
      const profileList = method.name === "ListResources" && (input as { filter?: { kind?: EntityKind } }).filter?.kind === EntityKind.INTEGRATION;
      if (method.name === "SaveIntegrationProfile" || profileList) {
        await new Promise(resolve => setTimeout(resolve, 600));
      }
      return transport.unary(method, signal, timeoutMs, header, input, contextValues);
    },
  };
  render(<TransportProvider transport={slowTransport}><QueryClientProvider client={client}><MutationIntents><Settings /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Integrations" }));
  fireEvent.click(await screen.findByRole("button", { name: "New GitHub profile" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Profile name" }), { target: { value: "Real server profile" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Resource owner" }), { target: { value: "fixture-owner" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  // Each save crosses the real Go mutation and list refetch. Use the ordinary
  // one-second wait only if this fixture stops exercising the native server.
  fireEvent.click(await screen.findByRole("button", { name: "Rename Real server profile" }, { timeout: 15000 }));
  fireEvent.change(screen.getByRole("textbox", { name: "Profile name" }), { target: { value: "Renamed server profile" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await screen.findByRole("button", { name: "Manage Renamed server profile" }, { timeout: 15000 });
  const output = JSON.parse(await runCLI(["integration", "list"]));
  const row = output.result.resources.find((value: { data: { name: string } }) => value.data.name === "Renamed server profile");
  expect(row).toBeTruthy();
  expect(row.data).toEqual({ name: "Renamed server profile", provider: "github.com", token_kind: "fine-grained", resource_owner: "fixture-owner" });
  expect(row.revision).toBe(2);
  client.clear(); cleanup();
}, 30000);
