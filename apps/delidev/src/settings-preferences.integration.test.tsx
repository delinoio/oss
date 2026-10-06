// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { ConfigurationService, EntityKind, NetworkService, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();
// These controls follow real Go reads or a save plus invalidation/refetch.
// Allow the round trip to settle while retaining the test's overall 15s deadline.
const serverRoundTripWait = { timeout: 5000 };

it("edits both scoped singleton forms inline without rewriting hidden settings", async () => {
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
  // The dirty status clears while the mutation is still in flight. Wait for
  // the explicit saving state to settle before reading the Go-owned resource.
  await waitFor(() => {
    expect(screen.queryByText("Saving changes…")).toBeNull();
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  }, serverRoundTripWait);
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
  expect(screen.queryByLabelText("Allow automatic fetch before Worktree preparation")).toBeNull();
  const retained = { ...defaults, default_routing: "priority", notifications: false, remediation: { ...defaults.remediation, attempt_limit: 9, reviewer_selectors: [{ kind: "bot", id: "9007199254740993", node_id: "BOT_exact" }] } };
  const configured = await createClient(ConfigurationService, transport).saveConfiguration({ kind: EntityKind.SETTINGS, schemaVersion: 1, documentJson: encode(retained), mutation: { id: first[0].id, expectedRevision: first[0].revision, requestId: newRequestId() } });
  expect(configured.resource).toBeTruthy();
  const network = createClient(NetworkService, transport);
  const originalRoute = await network.getNetworkRoute({});
  fireEvent.click(screen.getByRole("button", { name: "Git" }));
  expect(screen.getByRole("heading", { level: 1, name: "Git" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Network settings" })).toBeNull();
  const gitForm = await screen.findByRole("form", { name: "Git workflow form" }, serverRoundTripWait);
  expect(screen.queryByRole("button", { name: "New Git workflow" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Edit Git workflow" })).toBeNull();
  expect(screen.queryByLabelText("Default account routing")).toBeNull();
  expect(screen.getByText("Remediation details").closest("details")!.open).toBe(false);
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Automatically fix required CI failures" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement).disabled).toBe(false), serverRoundTripWait);
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => {
    expect(screen.queryByText("Saving changes…")).toBeNull();
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  }, serverRoundTripWait);
  expect(screen.getByRole("form", { name: "Git workflow form" })).toBe(gitForm);
  const git = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(git).toHaveLength(1); expect(git[0].id).toBe(first[0].id);
  expect(git[0].revision).toBe(configured.resource!.revision + 1n);
  expect(document(git[0])).toEqual({ ...retained, automatic_fetch: false, remediation: { ...retained.remediation, ci_failure: true } });
  expect(await network.getNetworkRoute({})).toEqual(originalRoute);
  expect((screen.getByLabelText("Allow automatic fetch before Worktree preparation") as HTMLInputElement).checked).toBe(false);
  expect((screen.getByLabelText("Automatically fix required CI failures") as HTMLInputElement).checked).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  const finalRouting = await screen.findByLabelText("Default account routing", {}, serverRoundTripWait) as HTMLSelectElement;
  fireEvent.change(finalRouting, { target: { value: "fixed" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement).disabled).toBe(false), serverRoundTripWait);
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => {
    expect(screen.queryByText("Saving changes…")).toBeNull();
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  }, serverRoundTripWait);
  await waitForSettings({ ...document(git[0]), default_routing: "fixed" });
  const final = (await resources.listResources({ filter: { kind: EntityKind.SETTINGS } })).resources;
  expect(final).toHaveLength(1); expect(final[0].id).toBe(first[0].id);
  expect(document(final[0])).toEqual({ ...document(git[0]), default_routing: "fixed" });
}, 15000);
