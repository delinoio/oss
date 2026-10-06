// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, NetworkService, ResourceService } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();
// These controls follow real Go reads or a save plus invalidation/refetch.
// Allow the round trip to settle while retaining the test's overall 15s deadline.
const serverRoundTripWait = { timeout: 5000 };

it("edits singleton server preferences inline while preserving the separate Git workflow", async () => {
  const { transport, runCLI } = fixture;
  const defaults = JSON.parse(await runCLI(["settings", "defaults"])).result;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  const routing = await screen.findByLabelText("Default account routing", {}, serverRoundTripWait) as HTMLSelectElement;
  expect(routing.value).toBe(defaults.default_routing);
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Edit Server preferences" })).toBeNull();
  expect((screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement).disabled).toBe(true);
  const resources = createClient(ResourceService, transport);
  const waitForSettings = async (expected: unknown) => {
    await waitFor(async () => {
      const current = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
      expect(current).toHaveLength(1);
      expect(document(current[0])).toEqual(expected);
    }, serverRoundTripWait);
  };
  expect((await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources).toHaveLength(0);
  fireEvent.change(routing, { target: { value: "priority" } });
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitForSettings({ ...defaults, default_routing: "priority" });
  const first = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(first).toHaveLength(1);
  expect(document(first[0])).toEqual({ ...defaults, default_routing: "priority" });
  expect(screen.getByRole("form", { name: "Server preferences form" })).toBeTruthy();
  const latest = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(latest).toHaveLength(1);
  expect(latest[0].id).toBe(first[0].id);
  expect(latest[0].revision).toBe(first[0].revision);
  expect(document(latest[0])).toEqual({ ...defaults, default_routing: "priority" });
  expect(routing.value).toBe("priority");
  expect(screen.queryByText("Worktree fetch")).toBeNull();
  const network = createClient(NetworkService, transport);
  const originalRoute = await network.getNetworkRoute({});
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  expect(screen.getByRole("heading", { level: 1, name: "Git" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Network settings" })).toBeNull();
  const editGit = await screen.findByRole("button", { name: "Edit Git workflow" }, serverRoundTripWait);
  expect(screen.queryByRole("button", { name: "New Git workflow" })).toBeNull();
  fireEvent.click(editGit);
  expect(screen.queryByLabelText("Default account routing")).toBeNull();
  expect(screen.getByText("Remediation details").closest("details")!.open).toBe(false);
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Automatically fix required CI failures" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Git workflow" }));
  await waitForSettings({ ...defaults, default_routing: "priority", automatic_fetch: false, remediation: { ...defaults.remediation, ci_failure: true } });
  const git = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(git).toHaveLength(1); expect(git[0].id).toBe(first[0].id);
  expect(git[0].revision).toBe(latest[0].revision + 1n);
  expect(document(git[0])).toEqual({ ...defaults, default_routing: "priority", automatic_fetch: false, remediation: { ...defaults.remediation, ci_failure: true } });
  expect(await network.getNetworkRoute({})).toEqual(originalRoute);
  expect(await screen.findByText("Disabled", {}, serverRoundTripWait)).toBeTruthy();
  expect(screen.getAllByText("Off")).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  const finalRouting = await screen.findByLabelText("Default account routing", {}, serverRoundTripWait) as HTMLSelectElement;
  fireEvent.change(finalRouting, { target: { value: "fixed" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement).disabled).toBe(false), serverRoundTripWait);
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitForSettings({ ...document(git[0]), default_routing: "fixed" });
  const final = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(final).toHaveLength(1); expect(final[0].id).toBe(first[0].id);
  expect(document(final[0])).toEqual({ ...document(git[0]), default_routing: "fixed" });
}, 15000);
