// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceService } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();
// These controls follow real Go reads or a save plus invalidation/refetch.
// Allow the round trip to settle while retaining the test's overall 15s deadline.
const serverRoundTripWait = { timeout: 5000 };

it("edits the singleton inline from the exact Go defaults without writes on entry", async () => {
  const { transport, runCLI } = fixture;
  const defaults = JSON.parse(await runCLI(["settings", "defaults"])).result;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  const routing = await screen.findByLabelText("Default account routing", {}, serverRoundTripWait) as HTMLSelectElement;
  const form = screen.getByRole("form", { name: "Server preferences form" });
  const save = screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement;
  expect(routing.value).toBe(defaults.default_routing);
  expect(screen.getByText("Remediation details").closest("details")!.open).toBe(false);
  expect(screen.getAllByRole("checkbox")).toHaveLength(4);
  expect(save.disabled).toBe(true);
  const resources = createClient(ResourceService, transport);
  expect((await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources).toHaveLength(0);
  fireEvent.change(routing, { target: { value: "priority" } });
  fireEvent.click(save);
  await waitFor(() => expect(screen.queryByText("Unsaved changes")).toBeNull(), serverRoundTripWait);
  const first = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(first).toHaveLength(1);
  expect(document(first[0])).toEqual({ ...defaults, default_routing: "priority" });
  expect(screen.getByRole("form")).toBe(form);
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Edit Server preferences" })).toBeNull();
  fireEvent.change(routing, { target: { value: "fixed" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  await waitFor(() => expect(save.disabled).toBe(false), serverRoundTripWait);
  fireEvent.click(save);
  await waitFor(() => expect(screen.queryByText("Unsaved changes")).toBeNull(), serverRoundTripWait);
  const latest = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(latest).toHaveLength(1);
  expect(latest[0].id).toBe(first[0].id);
  expect(latest[0].revision).toBe(first[0].revision + 1n);
  expect(document(latest[0])).toEqual({ ...defaults, default_routing: "fixed", automatic_fetch: false });
  expect(routing.value).toBe("fixed");
  expect((screen.getByLabelText("Allow automatic fetch before Worktree preparation") as HTMLInputElement).checked).toBe(false);
  expect(save.disabled).toBe(true);

}, 15000);

