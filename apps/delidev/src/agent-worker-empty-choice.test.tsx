// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";

function fixture() {
  const agent = create(ResourceSchema, { kind: EntityKind.AGENT, schemaVersion: 4, id: newRequestId(), revision: 1n, documentJson: encode({ name: "Existing worker", harness: "codex", routes: [{ model: { subscription_service: "chatgpt", native_id: "fixture-model" }, accounts: [{ id: newRequestId(), weight: 1 }] }], templates: [], options: { permission: "default" } }) });
  const list = vi.fn(async (_request: { filter?: { pageToken: string } }) => ({ resources: [] as typeof agent[], nextPageToken: "" }));
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: list, getResource: async () => ({ resource: agent }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }), change = vi.fn(), createAgent = vi.fn();
  const view = ({ active = true, allowed, value = "", resolvedChoice }: { active?: boolean; allowed?: string[]; value?: string; resolvedChoice?: typeof agent } = {}) => <TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={value} resolvedChoice={resolvedChoice} active={active} allowed={allowed} change={change} createEmptyAgent={createAgent} omitEmptyStatus showStatus /></QueryClientProvider></TransportProvider>;
  return { agent, list, client, change, createAgent, view };
}

it("replaces only a validated complete first page and grants no selection authority", async () => {
  const value = fixture(); render(value.view());
  const button = await screen.findByRole("button", { name: "Create agent worker" });
  expect(button.getAttribute("type")).toBe("button");
  expect(screen.queryByRole("combobox", { name: "Agent Worker" })).toBeNull();
  expect(screen.queryByText("No selectable Agent Worker choices are on this page.")).toBeNull();
  fireEvent.click(button); expect(value.createAgent).toHaveBeenCalledOnce(); expect(value.change).not.toHaveBeenCalled();
  expect(value.list).toHaveBeenCalledTimes(1);
});

it.each(["restricted-empty", "filtered", "continuing", "selected", "retained", "malformed", "denied", "unavailable"])("never treats %s inventory as absence", async state => {
  const value = fixture();
  value.list.mockImplementation(async () => {
    if (state === "denied" || state === "unavailable") throw new ConnectError("Read failed", state === "denied" ? Code.PermissionDenied : Code.Unavailable);
    return { resources: state === "filtered" ? [value.agent] : state === "malformed" ? [create(ResourceSchema, { ...value.agent, revision: 0n })] : [], nextPageToken: state === "continuing" ? "next" : "" };
  });
  render(value.view({ allowed: state === "filtered" || state === "restricted-empty" ? [] : undefined, value: state === "selected" ? value.agent.id : "", resolvedChoice: state === "retained" ? value.agent : undefined }));
  await waitFor(() => expect(value.list).toHaveBeenCalled());
  await waitFor(() => expect(screen.queryByText("Loading Agent Worker choices…")).toBeNull());
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  expect(value.change).not.toHaveBeenCalled();
});

it("does not infer absence from a later empty page", async () => {
  const value = fixture(); value.list.mockImplementation(async request => ({ resources: request.filter?.pageToken ? [] : [value.agent], nextPageToken: request.filter?.pageToken ? "" : "next" }));
  render(value.view()); fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  await screen.findByRole("option", { name: "Existing worker" });
  fireEvent.click(screen.getByRole("button", { name: "Load more Agent Worker" }));
  await waitFor(() => expect(value.list).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Agent Worker" })).toBeNull());
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
});

it("withdraws stale absence during refresh, failure and suspension then restores ordinary choices", async () => {
  const value = fixture(); const mounted = render(value.view());
  await screen.findByRole("button", { name: "Create agent worker" });
  let reject!: (error: unknown) => void;
  value.list.mockImplementationOnce(async () => await new Promise((_resolve, fail) => { reject = fail; }));
  await act(async () => { void value.client.invalidateQueries(); });
  await screen.findByText("Loading Agent Worker choices…");
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  await act(async () => reject(new ConnectError("Connection failed", Code.Unavailable)));
  await screen.findByText(/server connection failed/);
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  mounted.rerender(value.view({ active: false }));
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  value.list.mockImplementation(async () => ({ resources: [value.agent], nextPageToken: "" }));
  mounted.rerender(value.view());
  await waitFor(() => expect(screen.queryByText("Loading Agent Worker choices…")).toBeNull());
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
});
