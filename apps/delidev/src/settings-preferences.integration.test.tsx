// SPDX-License-Identifier: Apache-2.0
import { type Server } from "node:http";
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceService } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();
// Each save waits for a real Go mutation and the active-query refetch. Allow
// these two transitions up to five seconds on shared CI, keeping other waits
// unchanged. Replace this allowance if the fixture gains a completion barrier.
const savedPreferencesWait = { timeout: 5000 };

it("creates and edits singleton server preferences with the exact Go defaults", async () => {
  const { transport, runCLI } = fixture;
  const defaults = JSON.parse(await runCLI(["settings", "defaults"])).result;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Server preferences" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  const edit = await screen.findByRole("button", { name: "Edit Server preferences" }, savedPreferencesWait);
  const resources = createClient(ResourceService, transport);
  const first = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(first).toHaveLength(1);
  expect(document(first[0])).toEqual(defaults);
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  fireEvent.click(edit);
  fireEvent.change(screen.getByLabelText("Default account routing"), { target: { value: "priority" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  await screen.findByRole("button", { name: "Edit Server preferences" }, savedPreferencesWait);
  const latest = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(latest).toHaveLength(1);
  expect(latest[0].id).toBe(first[0].id);
  expect(latest[0].revision).toBe(first[0].revision + 1n);
  expect(document(latest[0])).toEqual({ ...defaults, default_routing: "priority", automatic_fetch: false });
}, 15000);


