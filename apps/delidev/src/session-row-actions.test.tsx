// SPDX-License-Identifier: Apache-2.0
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BudgetState, EntityKind, ResourceSchema, ResourceService, SessionAction, SessionService } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionControlProvider, SessionControlAcknowledgments, sessionControlEligibility, useSessionControl } from "./session-control";
import { SessionRowActions, SessionRowActionsProvider } from "./session-row-actions";

const source = create(ResourceSchema, { id: "session-A", kind: EntityKind.SESSION, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ archive: "active", recovery: "none", outcome: "running" }) });
function Detail() {
  const { control, resource } = useSessionControl(source.id, source);
  return <button disabled={control.busy || control.uncertain}>Detail revision {String(resource?.revision)}</button>;
}
function setup(fail = false, hold?: Promise<void>) {
  let failure = fail;
  const requests: object[] = [];
  const get = vi.fn(() => ({ resource: source }));
  const budget = vi.fn(() => ({ view: { session: source, state: BudgetState.ALLOW_INCOMPLETE } }));
  const control = vi.fn(async request => { requests.push(request); if (hold) await hold; if (failure) throw new ConnectError("lost", Code.Unavailable); return { change: { session: create(ResourceSchema, { ...source, revision: source.revision + 1n }) } }; });
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { getResource: get });
    router.service(SessionService, { getSessionBudget: budget, controlSession: control });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionControlProvider><SessionRowActionsProvider active><div><SessionRowActions id={source.id} dismissHover={vi.fn()} /><Detail /><input aria-label="B draft" defaultValue="Keep me" /></div></SessionRowActionsProvider></SessionControlProvider></MutationIntents></QueryClientProvider></TransportProvider>);
  return { get, budget, control, requests, recover: () => { failure = false; } };
}
it("reads only on open, uses authoritative bigint revision and preserves another draft", async () => {
  const value = setup(); expect(value.get).not.toHaveBeenCalled(); expect(value.budget).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "More session actions" }));
  const stop = await screen.findByRole("menuitem", { name: "Stop" });
  await waitFor(() => expect((stop as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(stop);
  await waitFor(() => expect(value.control).toHaveBeenCalledTimes(1));
  expect(value.requests[0]).toMatchObject({ mutation: { id: source.id, expectedRevision: source.revision }, action: SessionAction.STOP });
  expect((screen.getByRole("textbox", { name: "B draft" }) as HTMLInputElement).value).toBe("Keep me");
  await screen.findByRole("button", { name: `Detail revision ${source.revision + 1n}` });
});
it("retains uncertainty across close, shares the detail lock and retries exact original bytes", async () => {
  const value = setup(true);
  const opener = screen.getByRole("button", { name: "More session actions" }); fireEvent.click(opener);
  const stop = await screen.findByRole("menuitem", { name: "Stop" }); await waitFor(() => expect((stop as HTMLButtonElement).disabled).toBe(false)); fireEvent.click(stop);
  await screen.findByRole("button", { name: "Retry the same control request" });
  expect((screen.getByRole("button", { name: `Detail revision ${source.revision}` }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" }); expect(document.activeElement).toBe(opener);
  fireEvent.click(opener); expect(value.control).toHaveBeenCalledTimes(1);
  value.recover(); fireEvent.click(await screen.findByRole("button", { name: "Retry the same control request" }));
  await waitFor(() => expect(value.control).toHaveBeenCalledTimes(2)); expect(value.requests[1]).toEqual(value.requests[0]);
});
it("shares resume gates and keeps restore independent", () => {
  for (const values of [{ archive: "archived" }, { archive: "active", startup_rejection: {} }, { archive: "active", startup: { failure: {} } }]) {
    const row = create(ResourceSchema, { ...source, documentJson: encode(values) }); expect(sessionControlEligibility(row, false).resume).toBe(false);
  }
  expect(sessionControlEligibility(source, true).resume).toBe(false);
  expect(sessionControlEligibility(create(ResourceSchema, { ...source, documentJson: encode({ archive: "archived" }) }), false).archiveAction).toBe(SessionAction.RESTORE);
});

it("keeps a late accepted acknowledgment after closing the menu", async () => {
  let accept!: () => void;
  const hold = new Promise<void>(resolve => { accept = resolve; });
  const value = setup(false, hold);
  fireEvent.click(screen.getByRole("button", { name: "More session actions" }));
  const stop = await screen.findByRole("menuitem", { name: "Stop" });
  await waitFor(() => expect((stop as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(stop); await waitFor(() => expect(value.control).toHaveBeenCalledTimes(1));
  fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
  accept(); await screen.findByRole("button", { name: `Detail revision ${source.revision + 1n}` });
  expect(value.control).toHaveBeenCalledTimes(1);
});
it("blocks all controls after a denied original read and retries only that read", async () => {
  const value = setup();
  value.get.mockImplementation(() => { throw new ConnectError("denied", Code.PermissionDenied); });
  fireEvent.click(screen.getByRole("button", { name: "More session actions" }));
  const retry = await screen.findByRole("button", { name: "Retry session read" });
  for (const button of screen.getAllByRole("menuitem")) expect((button as HTMLButtonElement).disabled).toBe(true);
  const budgetReads = value.budget.mock.calls.length;
  fireEvent.click(retry); await waitFor(() => expect(value.get).toHaveBeenCalledTimes(2));
  expect(value.budget).toHaveBeenCalledTimes(budgetReads); expect(value.control).not.toHaveBeenCalled();
});
it("refreshes after a revision conflict without replaying the action", async () => {
  const value = setup();
  value.control.mockImplementationOnce(async request => { value.requests.push(request); throw new ConnectError("revision conflict", Code.Aborted); });
  fireEvent.click(screen.getByRole("button", { name: "More session actions" }));
  const stop = await screen.findByRole("menuitem", { name: "Stop" });
  await waitFor(() => expect((stop as HTMLButtonElement).disabled).toBe(false)); fireEvent.click(stop);
  const refresh = await screen.findByRole("button", { name: "Refresh the current session before a new action" });
  value.get.mockImplementation(() => ({ resource: create(ResourceSchema, { ...source, revision: source.revision + 42n }) }));
  fireEvent.click(refresh); await waitFor(() => expect(value.get).toHaveBeenCalledTimes(2));
  expect(value.control).toHaveBeenCalledTimes(1);
  await waitFor(() => expect((stop as HTMLButtonElement).disabled).toBe(false)); fireEvent.click(stop);
  await waitFor(() => expect(value.control).toHaveBeenCalledTimes(2));
  expect(value.requests[1]).toMatchObject({ mutation: { expectedRevision: source.revision + 42n } });
});

it("bounds accepted snapshots by count and exact wire bytes while protecting active conversations", () => {
  const store = new SessionControlAcknowledgments(2, 8 << 20);
  const active = create(ResourceSchema, { ...source, id: "active" });
  store.accept(active); const release = store.pin(active.id);
  for (const id of ["old", "new", "latest"]) store.accept(create(ResourceSchema, { ...source, id }));
  expect(store.snapshot().size).toBe(2); expect(store.snapshot().has(active.id)).toBe(true); expect(store.snapshot().has("latest")).toBe(true);
  release(); store.accept(create(ResourceSchema, { ...source, id: "next" })); expect(store.snapshot().has(active.id)).toBe(false);
  const bytes = toBinary(ResourceSchema, source).byteLength;
  const bounded = new SessionControlAcknowledgments(1000, bytes * 2);
  for (const id of ["first-AAA", "second-AA", "third-AAA"]) bounded.accept(create(ResourceSchema, { ...source, id }));
  expect([...bounded.snapshot().values()].reduce((sum, resource) => sum + toBinary(ResourceSchema, resource).byteLength, 0)).toBeLessThanOrEqual(bytes * 2);
  expect(bounded.snapshot().size).toBeLessThanOrEqual(2);
  const oversized = create(ResourceSchema, { ...source, id: "oversized", documentJson: encode({ archive: "active", outcome: "running", padding: "x".repeat(bytes * 3) }) });
  expect(bounded.accept(oversized)).toBe(false); expect(bounded.snapshot().has(oversized.id)).toBe(false);
});
it("retains a late original acknowledgment after cache eviction without changing its source bytes", () => {
  const store = new SessionControlAcknowledgments(2, 8 << 20);
  const active = create(ResourceSchema, { ...source, id: "detail-B" }); store.accept(active); const release = store.pin(active.id);
  store.accept(source); store.accept(create(ResourceSchema, { ...source, id: "unrelated" })); expect(store.snapshot().has(source.id)).toBe(false);
  const receipt = create(ResourceSchema, { ...source, revision: source.revision + 1n, documentJson: source.documentJson.slice() }); store.accept(receipt);
  receipt.documentJson.fill(0);
  expect(store.snapshot().get(source.id)?.revision).toBe(source.revision + 1n);
  expect(Array.from(store.snapshot().get(source.id)!.documentJson)).toEqual(Array.from(source.documentJson));
  expect(store.snapshot().has(active.id)).toBe(true); release();
});
it.each([
  { name: "zero revision", values: { revision: 0n } },
  { name: "broken JSON", values: { documentJson: new TextEncoder().encode("{broken") } },
  { name: "invalid UTF-8", values: { documentJson: new Uint8Array([0xff]) } },
  { name: "array document", values: { documentJson: encode([]) } },
  { name: "null document", values: { documentJson: encode(null) } },
  { name: "missing Session fields", values: { documentJson: encode({}) } },
  { name: "oversized document", values: { documentJson: new Uint8Array((1 << 20) + 1) } },
])("rejects an invalid original Session read without admitting controls ($name)", async ({ values }) => {
  const value = setup(); value.get.mockImplementation(() => ({ resource: create(ResourceSchema, { ...source, ...values }) }));
  fireEvent.click(screen.getByRole("button", { name: "More session actions" }));
  const retry = await screen.findByRole("button", { name: "Retry session read" });
  for (const button of screen.getAllByRole("menuitem")) expect((button as HTMLButtonElement).disabled).toBe(true);
  expect(value.control).not.toHaveBeenCalled();
  value.get.mockImplementation(() => ({ resource: source })); fireEvent.click(retry);
  await waitFor(() => expect((screen.getByRole("menuitem", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(false));
  expect(value.control).not.toHaveBeenCalled();
});
it("enforces the default 1000-entry and 8 MiB accepted snapshot limits", () => {
  const count = new SessionControlAcknowledgments();
  for (let index = 0; index < 1003; index++) count.accept(create(ResourceSchema, { ...source, id: `session-${index}` }));
  expect(count.snapshot().size).toBe(1000); expect(count.snapshot().has("session-0")).toBe(false);
  const bytes = new SessionControlAcknowledgments();
  const documentJson = encode({ archive: "active", outcome: "running", padding: "x".repeat(900_000) });
  for (let index = 0; index < 10; index++) bytes.accept(create(ResourceSchema, { ...source, id: `large-session-${index}`, documentJson }));
  expect(bytes.snapshot().size).toBeLessThan(10);
  expect([...bytes.snapshot().values()].reduce((sum, row) => sum + toBinary(ResourceSchema, row).byteLength, 0)).toBeLessThanOrEqual(8 << 20);
});
