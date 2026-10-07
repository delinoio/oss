// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";

function fixture() {
  const rows = ["First", "Second", "Retained"].map(name => create(ResourceSchema, { kind: EntityKind.MACHINE, schemaVersion: 1, id: newRequestId(), revision: 1n, documentJson: encode({ name, disabled: false }) }));
  const list = vi.fn(async (request: { filter?: { pageToken: string } }) => ({ resources: [rows[request.filter?.pageToken ? 1 : 0]], nextPageToken: request.filter?.pageToken ? "" : "second" }));
  const get = vi.fn(async (request: { id: string }) => ({ resource: rows.find(row => row.id === request.id) }));
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: list, getResource: get }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }), change = vi.fn();
  const view = (active = true, allowed?: string[]) => <TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Runner" kind={EntityKind.MACHINE} value={rows[2].id} active={active} change={change} allowed={allowed} showStatus /></QueryClientProvider></TransportProvider>;
  return { rows, list, get, change, view };
}

it("appends picker metadata while preserving an off-page identity and reads selected authority", async () => {
  const value = fixture(); render(value.view());
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  await waitFor(() => expect(trigger.textContent).toBe("Retained"));
  expect(trigger.dataset.value).toBe(value.rows[2].id);
  fireEvent.click(trigger); await screen.findByRole("option", { name: "First" });
  fireEvent.click(screen.getByRole("button", { name: "Load more Runner" }));
  const second = await screen.findByRole("option", { name: "Second" });
  expect(screen.getByRole("option", { name: "First" })).toBeTruthy();
  fireEvent.click(second);
  await waitFor(() => expect(value.change).toHaveBeenCalledTimes(1));
  expect(value.change.mock.calls[0][0]).toBe(value.rows[1].id);
  expect(value.change.mock.calls[0][1]).toEqual({ name: "Second", disabled: false });
  expect(value.change.mock.calls[0][2]).toMatchObject({ id: value.rows[1].id, revision: 1n, kind: EntityKind.MACHINE });
  expect(value.get.mock.calls.some(([request]) => request.id === value.rows[1].id)).toBe(true);
});

it("rejects invalid additional pages without losing accepted choices", async () => {
  const value = fixture(); value.list.mockImplementation(async request => ({ resources: [request.filter?.pageToken ? create(ResourceSchema, { ...value.rows[1], kind: EntityKind.ACCOUNT }) : value.rows[0]], nextPageToken: request.filter?.pageToken ? "" : "second" }));
  render(value.view()); fireEvent.click(screen.getByRole("combobox", { name: "Runner" }));
  await screen.findByRole("option", { name: "First" }); fireEvent.click(screen.getByRole("button", { name: "Load more Runner" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("option", { name: "First" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Second" })).toBeNull(); expect(value.change).not.toHaveBeenCalled();
});

it("preserves configured-empty restrictions and fences late exact-ID selection", async () => {
  const value = fixture(); const result = render(value.view(true, []));
  fireEvent.click(screen.getByRole("combobox", { name: "Runner" }));
  await waitFor(() => expect(value.list).toHaveBeenCalled());
  expect(screen.queryByRole("option", { name: "First" })).toBeNull();
  result.rerender(value.view()); await screen.findByRole("option", { name: "First" });
  let resolve!: (response: { resource: typeof value.rows[0] }) => void;
  value.get.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  fireEvent.click(screen.getByRole("option", { name: "First" }));
  await waitFor(() => expect(resolve).toBeTruthy());
  result.rerender(value.view(false));
  await act(async () => resolve({ resource: value.rows[0] }));
  expect(value.change).not.toHaveBeenCalled();
});
