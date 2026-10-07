// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, ProviderService, newRequestId } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";

it("applies a delayed exact selection to the current sibling form draft", async () => {
  const row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Runner" }) });
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let reads = 0;
  const transport = createRouterTransport(router => router.service(ResourceService, {
    listResources: () => ({ resources: [row] }), getResource: async () => { reads++; await gate; return { resource: row }; },
  }));
  function Form() {
    const [draft, setDraft] = useState({ alias: "Original", machine: "" });
    return <><input aria-label="Alias" value={draft.alias} onChange={event => setDraft({ ...draft, alias: event.target.value })} />
      <ResourceChoice label="Runner" kind={EntityKind.MACHINE} value={draft.machine} active change={machine => setDraft({ ...draft, machine })} />
      <output>{draft.alias}:{draft.machine}</output></>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Form /></QueryClientProvider></TransportProvider>);
  const control = screen.getByRole("combobox", { name: "Runner" }); fireEvent.click(control);
  fireEvent.click(await screen.findByRole("option", { name: "Runner" }));
  await waitFor(() => expect(reads).toBe(1));
  fireEvent.change(screen.getByLabelText("Alias"), { target: { value: "Edited during read" } });
  await act(async () => release());
  await waitFor(() => expect(screen.getByRole("status").textContent).toBe(`Edited during read:${row.id}`));
});

it("does not apply a cached provider inventory failure to unrelated account choices", async () => {
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 1, documentJson: encode({ alias: "Account" }) });
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: () => ({ resources: [account] }) });
    router.service(ProviderService, { listProviderInventory: () => { throw new ConnectError("Denied", Code.PermissionDenied); } });
  });
  render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <section aria-label="Provider scope"><ResourceChoice label="Provider" kind={EntityKind.PROVIDER} value="" active activeApiOnly showStatus change={() => {}} /></section>
    <section aria-label="Account scope"><ResourceChoice label="Account" kind={EntityKind.ACCOUNT} value="" active showStatus change={() => {}} /></section>
  </QueryClientProvider></TransportProvider>);
  await within(screen.getByRole("region", { name: "Provider scope" })).findByText(/server denied access to these choices/);
  expect(within(screen.getByRole("region", { name: "Account scope" })).queryByText(/server denied access to these choices/)).toBeNull();
});

it.each(["duplicate", "oversized"])("rejects an entire malformed %s append before the allowed-ID filter", async malformed => {
  const row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Accepted runner" }) });
  const hidden = create(ResourceSchema, { ...row, id: newRequestId(), documentJson: encode({ name: "Filtered runner" }) });
  const invalid = malformed === "duplicate" ? [hidden, hidden] : Array.from({ length: 51 }, () => create(ResourceSchema, { ...hidden, id: newRequestId() }));
  const read = vi.fn(request => request.filter?.pageToken ? { resources: invalid, nextPageToken: "" } : { resources: [row], nextPageToken: "later" });
  const change = vi.fn();
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: read, getResource: () => ({ resource: row }) }));
  render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <ResourceChoice label="Runner" kind={EntityKind.MACHINE} value={row.id} allowed={[row.id]} active change={change} />
  </QueryClientProvider></TransportProvider>);
  const control = screen.getByRole("combobox", { name: "Runner" }); fireEvent.click(control);
  await screen.findByRole("option", { name: "Accepted runner" });
  fireEvent.click(await screen.findByRole("button", { name: "Load more Runner" }));
  await screen.findByRole("button", { name: "Retry" });
  expect(read).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("option", { name: "Accepted runner" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Filtered runner" })).toBeNull();
  expect(control.dataset.value).toBe(row.id); expect(change).not.toHaveBeenCalled();
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
  expect(read).toHaveBeenCalledTimes(2);
});
