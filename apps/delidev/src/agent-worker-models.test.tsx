// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { AgentModelReader, AgentWorkerMetadataProvider, ModelSummaryState, modelSummary } from "./agent-worker-models";
import { AgentWorkerRow } from "./agent-worker-row";
const ready = { state: ModelSummaryState.Ready, nativeID: "native", name: "Display" };
const settle = () => new Promise(resolve => setTimeout(resolve, 0));
it("shares four slots across page requests, deduplicates and preserves queued order", async () => {
  const pending: (() => void)[] = [], ids = Array.from({ length: 12 }, () => newRequestId());
  const read = vi.fn((_id: string, _signal: AbortSignal) => new Promise<typeof ready>(resolve => pending.push(() => resolve(ready))));
  const reader = new AgentModelReader(read); reader.setActive(true);
  reader.request(ids.slice(0, 6)); reader.request(ids.slice(4));
  expect(read).toHaveBeenCalledTimes(4); expect(read.mock.calls.map(args => args[0])).toEqual(ids.slice(0, 4));
  pending.splice(0).forEach(resolve => resolve()); await settle();
  expect(read).toHaveBeenCalledTimes(8); expect(read.mock.calls.slice(4).map(args => args[0])).toEqual(ids.slice(4, 8));
  reader.dispose(); pending.splice(0).forEach(resolve => resolve()); await settle();
  expect(read).toHaveBeenCalledTimes(8); expect(reader.snapshot().size).toBe(0);
});
it("fences refresh, suspension and disposal without exceeding slots for abort-ignoring reads", async () => {
  const pending: { signal: AbortSignal; resolve: (value: typeof ready) => void }[] = [], ids = Array.from({ length: 8 }, () => newRequestId());
  const read = vi.fn((_id: string, signal: AbortSignal) => new Promise<typeof ready>(resolve => pending.push({ signal, resolve })));
  const reader = new AgentModelReader(read); reader.setActive(true); reader.request(ids);
  reader.refresh(); reader.request(ids); expect(read).toHaveBeenCalledTimes(4); expect(pending.every(value => value.signal.aborted)).toBe(true);
  pending.splice(0).forEach(value => value.resolve({ ...ready, nativeID: "stale" })); await settle();
  expect(read).toHaveBeenCalledTimes(8); expect([...reader.snapshot().values()].every(value => value.state === ModelSummaryState.Loading)).toBe(true);
  reader.setActive(false); pending.splice(0).forEach(value => value.resolve(ready)); await settle(); expect(reader.snapshot().size).toBe(0);
  reader.setActive(true); reader.request([ids[0]]); pending.splice(0).forEach(value => value.resolve(ready)); await settle(); expect(reader.snapshot().get(ids[0])).toEqual(ready);
  reader.dispose(); reader.reopen(); reader.setActive(true); reader.request([ids[1]]); expect(read).toHaveBeenCalledTimes(10); reader.dispose();
});
it("retains only mounted projections and keeps a failed first identity without substituting later models", async () => {
  const log = vi.spyOn(console, "warn").mockImplementation(() => {});
  const ids = Array.from({ length: 2050 }, () => newRequestId());
  const reader = new AgentModelReader(async id => { if (id === ids[0]) throw new Error("Denied"); return ready; }); reader.retain("page", ids); reader.setActive(true); reader.request(ids);
  await settle(); expect(reader.snapshot().size).toBe(2050); expect(reader.snapshot().get(ids[0])?.state).toBe(ModelSummaryState.Unavailable); expect(reader.snapshot().get(ids[1])).toEqual(ready); reader.release("page"); expect(reader.snapshot().size).toBe(0); reader.dispose(); log.mockRestore();
});
it("accepts only the requested Model kind, supported schema, positive revision and bounded native identity", () => {
  const id = newRequestId(), row = create(ResourceSchema, { id, kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Display", native_id: "native", credential: "never projected" }) });
  expect(modelSummary(row, id)).toEqual(ready);
  for (const invalid of [undefined, { ...row, id: newRequestId() }, { ...row, kind: EntityKind.AGENT }, { ...row, revision: 0n }, { ...row, schemaVersion: 99 }, { ...row, documentJson: encode({ native_id: "native" }) }, { ...row, documentJson: encode({ name: "Display", native_id: "한".repeat(100) }) }, { ...row, documentJson: encode({ name: "Display", native_id: "a\0b" }) }, { ...row, documentJson: new Uint8Array([255]) }]) expect(() => modelSummary(invalid, id)).toThrow();
});
function fixture() {
  const models = ["native-first", "native-second"].map(native => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `${native} display`, native_id: native }) }));
  const rows = [create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: 3, revision: 1n, documentJson: encode({ name: "Ordered", harness: "codex", routes: [models[0], models[1], models[0]].map((row, index) => ({ model_id: row.id, accounts: Array.from({ length: index + 1 }, (_, account) => ({ id: newRequestId(), weight: account === 0 ? 1000 : 1 })) })) }) }), create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: 99, revision: 1n, documentJson: encode({ name: "Future", harness: "codex", model_id: models[1].id }) })];
  const get = vi.fn(async (request: { id: string }) => ({ resource: models.find(row => row.id === request.id) }));
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: get }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const actions = { edit: vi.fn(), preview: vi.fn(), remove: vi.fn() };
  function Fixture({ active = true, refresh = 0 }: { active?: boolean; refresh?: number }) { return <StrictMode><QueryClientProvider client={client}><TransportProvider transport={transport}><AgentWorkerMetadataProvider active={active} refresh={refresh}>{rows.map(row => <section key={row.id}><AgentWorkerRow row={row} {...actions} /></section>)}</AgentWorkerMetadataProvider></TransportProvider></QueryClientProvider></StrictMode>; }
  return { Fixture, get, rows, models, client, actions };
}
it("lazily reads model routes in StrictMode, keeps repetitions, refreshes only displayed summaries and cancels on disposal", async () => {
  const value = fixture(), view = render(<value.Fixture />);
  await screen.findAllByText("native-first"); expect(value.get).toHaveBeenCalledTimes(1);
  expect(screen.getByText("Codex")).toBeTruthy(); expect(screen.getByText("Future").closest("article")?.querySelector(".worker-harness-mark")).toBeNull();
  const future = screen.getByText("Future").closest("article")!; expect([...future.querySelectorAll("button")].every(button => button.disabled)).toBe(true);
  const toggle = screen.getByRole("button", { name: "+2 more" }); fireEvent.click(toggle);
  await screen.findByText("native-second"); expect(value.get).toHaveBeenCalledTimes(2); expect(toggle.getAttribute("aria-expanded")).toBe("true");
  const region = screen.getByRole("region", { name: "Configured models in saved order" }); expect(within(region).getAllByRole("listitem").map(row => row.querySelector("code")?.textContent)).toEqual(["native-first", "native-second", "native-first"]);
  expect(within(region).getAllByRole("listitem").map(row => row.querySelector(".agent-route-account-count")?.textContent)).toEqual(["1 account", "2 accounts", "3 accounts"]);
  expect(future.querySelector(".agent-route-account-count")).toBeNull();
  expect(value.get.mock.calls.every(([request]) => value.models.some(model => model.id === request.id))).toBe(true);
  fireEvent.click(toggle); view.rerender(<value.Fixture refresh={1} />); await waitFor(() => expect(value.get).toHaveBeenCalledTimes(3));
  expect(value.get.mock.calls.at(-1)?.[0].id).toBe(value.models[0].id); expect(value.actions.edit).not.toHaveBeenCalled();
  view.rerender(<value.Fixture active={false} refresh={1} />); await act(settle); expect(value.get).toHaveBeenCalledTimes(3);
  view.unmount(); await act(settle); expect(value.client.getQueryCache().getAll().length).toBe(0);
});
it("keeps the failed first route unavailable until explicit refresh and never reads another route implicitly", async () => {
  const value = fixture(), logs = vi.spyOn(console, "warn").mockImplementation(() => {});
  value.get.mockImplementation(async request => ({ resource: request.id === value.models[0].id ? undefined : value.models[1] }));
  const view = render(<value.Fixture />); await screen.findAllByText("Model unavailable"); expect(value.get).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "+2 more" })); await screen.findByText("native-second");
  const summary = screen.getByText("Configured model").closest(".agent-model-summary")!; expect(within(summary as HTMLElement).getByText("Model unavailable")).toBeTruthy(); expect(summary.querySelector("code")).toBeNull(); expect(within(summary as HTMLElement).getByText("1 account")).toBeTruthy();
  view.rerender(<value.Fixture refresh={1} />); await waitFor(() => expect(value.get).toHaveBeenCalledTimes(4)); expect(value.actions.remove).not.toHaveBeenCalled(); view.unmount(); logs.mockRestore();
});
it("shares four active exact reads across three mounted page fragments and cancels the opening", async () => {
  const pending: (() => void)[] = [], models = Array.from({ length: 12 }, (_, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Model ${index}`, native_id: `native-${index}` }) }));
  const rows = models.map((model, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Worker ${index}`, harness: "codex", model_id: model.id }) }));
  const get = vi.fn(async (request: { id: string }) => { await new Promise<void>(resolve => pending.push(resolve)); return { resource: models.find(model => model.id === request.id) }; });
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: get })), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><AgentWorkerMetadataProvider active refresh={0}>{[0, 4, 8].map(start => <section key={start}>{rows.slice(start, start + 4).map(row => <AgentWorkerRow key={row.id} row={row} edit={() => {}} preview={() => {}} remove={() => {}} />)}</section>)}</AgentWorkerMetadataProvider></TransportProvider></QueryClientProvider>);
  await waitFor(() => expect(get).toHaveBeenCalledTimes(4)); expect(get.mock.calls.map(args => args[0].id)).toEqual(models.slice(0, 4).map(model => model.id));
  await act(async () => { pending.splice(0).forEach(resolve => resolve()); await settle(); }); await waitFor(() => expect(get).toHaveBeenCalledTimes(8));
  view.unmount(); await act(async () => { pending.splice(0).forEach(resolve => resolve()); await settle(); }); expect(get).toHaveBeenCalledTimes(8); expect(client.getQueryCache().getAll()).toHaveLength(0);
});
it("rejects late metadata after its row reference is released and remounted", async () => {
  const pending: ((value: typeof ready) => void)[] = [], id = newRequestId();
  const reader = new AgentModelReader(async () => new Promise<typeof ready>(resolve => pending.push(resolve)));
  reader.retain("old-page", [id]); reader.setActive(true); reader.request([id]);
  reader.release("old-page"); reader.retain("new-page", [id]); reader.request([id]);
  pending[0]({ ...ready, nativeID: "late-original" }); await settle(); expect(reader.snapshot().get(id)?.state).toBe(ModelSummaryState.Loading);
  pending[1](ready); await settle(); expect(reader.snapshot().get(id)).toEqual(ready); reader.dispose();
});

it.each(["refresh", "reconnect"])("holds four actual transport permits across mounted-provider %s with abort-ignoring original waits", async mode => {
  const models = Array.from({ length: 8 }, (_, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Permit model ${index}`, native_id: `permit-native-${index}` }) }));
  const rows = models.map((model, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Permit Worker ${index}`, harness: "codex", model_id: model.id }) }));
  const pending: (() => void)[] = [];
  let concurrent = 0, maximum = 0;
  const get = vi.fn(async ({ id }: { id: string }) => {
    concurrent++; maximum = Math.max(maximum, concurrent);
    await new Promise<void>(resolve => pending.push(() => { concurrent--; resolve(); }));
    return { resource: models.find(model => model.id === id) };
  });
  const first = createRouterTransport(router => router.service(ResourceService, { getResource: get }));
  const second = createRouterTransport(router => router.service(ResourceService, { getResource: get }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (refresh: number, transport = first) => <QueryClientProvider client={client}><TransportProvider transport={transport}><AgentWorkerMetadataProvider active refresh={refresh}>{rows.map(row => <AgentWorkerRow key={row.id} row={row} edit={() => {}} preview={() => {}} remove={() => {}} />)}</AgentWorkerMetadataProvider></TransportProvider></QueryClientProvider>;
  const mounted = render(view(0)); await waitFor(() => expect(get).toHaveBeenCalledTimes(4));
  if (mode === "reconnect") { mounted.rerender(view(0, second)); await act(settle); expect(get).toHaveBeenCalledTimes(4); }
  mounted.rerender(view(1, mode === "reconnect" ? second : first));
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
  expect(get).toHaveBeenCalledTimes(4);
  await act(async () => { pending.splice(0).forEach(resolve => resolve()); await settle(); });
  await waitFor(() => expect(get).toHaveBeenCalledTimes(8));
  await act(async () => { pending.splice(0).forEach(resolve => resolve()); await settle(); });
  await waitFor(() => expect(get).toHaveBeenCalledTimes(12));
  await act(async () => { pending.splice(0).forEach(resolve => resolve()); await settle(); });
  await screen.findByText("permit-native-7"); expect(maximum).toBe(4);
  expect(client.getQueryCache().getAll()).toHaveLength(0); mounted.unmount();
});

it.each([
  { schema: 1, accounts: [], expected: "0 accounts" },
  { schema: 1, accounts: undefined, expected: "Account count unavailable" },
  { schema: 1, accounts: {}, expected: "Account count unavailable" },
  { schema: 1, accounts: [{ id: "disabled" }, { id: "disconnected", weight: 1000 }], expected: "2 accounts" },
  { schema: 2, accounts: [], expected: "0 accounts" },
])("shows saved schema $schema counts independently of model availability", async ({ schema, accounts, expected }) => {
  const value = fixture();
  value.rows[0].schemaVersion = schema;
  value.rows[0].documentJson = encode({ name: "Ordered", harness: "codex", ...(schema === 2 ? { reconfiguration_required: true } : {}), ...(schema === 3 ? { routes: [{ model_id: value.models[0].id, accounts }] } : { model_id: value.models[0].id, accounts }) });
  value.get.mockImplementation(async () => ({ resource: undefined }));
  const view = render(<value.Fixture />);
  expect(screen.getByText(expected)).toBeTruthy();
  await screen.findByText("Model unavailable");
  expect(screen.getByText(expected)).toBeTruthy();
  expect(value.get).toHaveBeenCalledTimes(1);
  value.rows[0].revision++;
  value.rows[0].documentJson = encode({ name: "Ordered", harness: "codex", ...(schema === 2 ? { reconfiguration_required: true } : {}), ...(schema === 3 ? { routes: [{ model_id: value.models[0].id, accounts: [{ id: "updated" }] }] } : { model_id: value.models[0].id, accounts: [{ id: "updated" }] }) });
  view.rerender(<value.Fixture />);
  expect(screen.getByText("1 account")).toBeTruthy();
  expect(value.get).toHaveBeenCalledTimes(1);
});

it("keeps unreadable known documents unavailable without inferring counts or account reads", async () => {
  const value = fixture();
  value.rows[0].documentJson = Uint8Array.of(255);
  render(<value.Fixture />);
  expect(screen.getByText("Account count unavailable")).toBeTruthy();
  expect(value.get).not.toHaveBeenCalled();
  const future = screen.getByText("Future").closest("article")!;
  expect(future.querySelector(".agent-route-account-count")).toBeNull();
});
