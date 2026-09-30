// SPDX-License-Identifier: Apache-2.0
import { type Server } from "node:http";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ConfigurationQuery, EntityKind, ResourceService } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("creates and edits singleton server preferences with the exact Go defaults", async () => {
  const { runCLI } = fixture;
  // A real owned-server save and its invalidation refetch can take longer than
  // Testing Library's one-second default. Exercise that latency explicitly.
  let saves = 0;
  const transport: Transport = {
    ...fixture.transport,
    async unary(method, signal, timeoutMs, header, input, contextValues) {
      if (method.parent.typeName === ConfigurationQuery.saveConfiguration.parent.typeName && method.name === ConfigurationQuery.saveConfiguration.name) {
        saves += 1;
        await new Promise((resolve) => setTimeout(resolve, 1100));
      }
      return fixture.transport.unary(method, signal, timeoutMs, header, input, contextValues);
    },
  };
  const defaults = JSON.parse(await runCLI(["settings", "defaults"])).result;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Server preferences" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  // The real Go save and subsequent list refresh can exceed the default
  // one-second DOM wait on CI; retain a bounded wait for the committed result.
  const edit = await screen.findByRole("button", { name: "Edit Server preferences" }, { timeout: 5000 });
  const resources = createClient(ResourceService, transport);
  const first = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(first).toHaveLength(1);
  expect(document(first[0])).toEqual(defaults);
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  fireEvent.click(edit);
  fireEvent.change(screen.getByLabelText("Default account routing"), { target: { value: "priority" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  await screen.findByRole("button", { name: "Edit Server preferences" }, { timeout: 5000 });
  const latest = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(latest).toHaveLength(1);
  expect(latest[0].id).toBe(first[0].id);
  expect(latest[0].revision).toBe(first[0].revision + 1n);
  expect(document(latest[0])).toEqual({ ...defaults, default_routing: "priority", automatic_fetch: false });
  expect(saves).toBe(2);
}, 15000);
