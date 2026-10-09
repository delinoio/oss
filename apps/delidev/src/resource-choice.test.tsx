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
import { RunnerRemediationProvider } from "./runner-remediation";
import { i18n } from "./localization";

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

it("retains exact selected Runner changes without a shortcut or inspection presentation", async () => {
  const first = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "First Runner", disabled: false, installations: null }) });
  const second = create(ResourceSchema, { ...first, id: newRequestId(), revision: 11n, documentJson: encode({ name: "Second Runner", disabled: false, installations: null }) });
  const change = vi.fn(), reads = vi.fn(request => ({ resource: request.id === first.id ? first : second }));
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [first, second] }), getResource: reads }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const [value, setValue] = useState(first.id); return <ResourceChoice label="Runner" kind={EntityKind.MACHINE} value={value} active change={(id, data, resource) => { change(id, data, resource); setValue(id); }} />; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  const picker = screen.getByRole("combobox", { name: "Runner" }); fireEvent.click(picker);
  fireEvent.click(await screen.findByRole("option", { name: "Second Runner" }));
  await waitFor(() => expect(change).toHaveBeenCalledOnce());
  expect(change.mock.calls[0][0]).toBe(second.id); expect(change.mock.calls[0][2]).toMatchObject({ id: second.id, kind: second.kind, revision: second.revision, schemaVersion: second.schemaVersion });
  expect(Array.from(change.mock.calls[0][2].documentJson as Uint8Array)).toEqual(Array.from(second.documentJson));
  expect(picker.dataset.value).toBe(second.id); expect(reads.mock.calls.some(([request]) => request.id === second.id && request.kind === EntityKind.MACHINE)).toBe(true);
  expect(screen.queryByRole("button", { name: "Inspect this Runner" })).toBeNull(); expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.queryByRole("button", { name: "이 Runner 검사" })).toBeNull(); expect(picker.dataset.value).toBe(second.id);
  client.clear();
});

it("decorates exact Agent IDs from their top-level harness without model, account or provider reads", async () => {
  const harnesses = ["codex", "claude-code", "opencode", "grok-build"];
  const rows = harnesses.map(harness => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 3, documentJson: encode({ name: "Equal Agent name", harness, routes: [{ model_id: newRequestId(), accounts: [{ id: newRequestId() }] }, { model_id: newRequestId(), harness: "grok-build", accounts: [{ id: newRequestId() }] }] }) }));
  const reads = vi.fn(request => ({ resource: rows.find(row => row.id === request.id) })), change = vi.fn();
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: rows }), getResource: reads }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Form() { const [value, setValue] = useState(""); return <ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={value} active change={(id, data, resource) => { change(id, data, resource); setValue(id); }} />; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Form /></QueryClientProvider></TransportProvider>);
  const trigger = screen.getByRole("combobox", { name: "Agent Worker" });
  expect(trigger.querySelector(".scroll-picker-decoration")).toBeTruthy();
  expect(trigger.querySelector(".worker-harness-mark")).toBeNull();
  for (const [index, row] of rows.entries()) {
    fireEvent.click(trigger);
    const options = await screen.findAllByRole("option", { name: "Equal Agent name" });
    expect(options).toHaveLength(4);
    const option = options[index];
    expect(option.querySelector(".worker-harness-mark")?.getAttribute("data-harness")).toBe(harnesses[index]);
    expect(option.querySelector(".scroll-picker-decoration")?.getAttribute("aria-hidden")).toBe("true");
    if (index % 2) {
      fireEvent.keyDown(trigger, { key: "Home" });
      for (let next = 0; next <= index; next++) fireEvent.keyDown(trigger, { key: "ArrowDown" });
      fireEvent.keyDown(trigger, { key: "Enter" });
    } else fireEvent.click(option);
    await waitFor(() => expect(trigger.getAttribute("data-value")).toBe(row.id));
    await waitFor(() => expect(trigger.querySelector(".worker-harness-mark")?.getAttribute("data-harness")).toBe(harnesses[index]));
    expect(trigger.textContent).toBe("Equal Agent name");
    expect(change.mock.calls[index][0]).toBe(row.id);
    expect(change.mock.calls[index][2]?.id).toBe(row.id);
  }
  expect(reads.mock.calls.every(([request]) => request.kind === EntityKind.AGENT)).toBe(true);
  client.clear();
});

it.each([false, true])("uses the exact off-page Agent Resource, including resolvedChoice fallback %s", async resolved => {
  const onPage = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Equal name", harness: "codex" }) });
  const selected = create(ResourceSchema, { ...onPage, id: newRequestId(), revision: 5n, documentJson: encode({ name: "Equal name", harness: "grok-build" }) });
  const reads = vi.fn((_request: { id: string; kind: EntityKind }) => resolved ? {} : { resource: selected });
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [onPage] }), getResource: reads }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent" kind={EntityKind.AGENT} value={selected.id} resolvedChoice={resolved ? selected : undefined} active change={vi.fn()} /></QueryClientProvider></TransportProvider>);
  const trigger = screen.getByRole("combobox", { name: "Agent" });
  await waitFor(() => expect(trigger.querySelector("[data-harness=grok-build]")).toBeTruthy());
  fireEvent.click(trigger);
  expect((await screen.findByRole("option", { name: "Equal name" })).querySelector("[data-harness=codex]")).toBeTruthy();
  expect(trigger.querySelector("[data-harness=codex]")).toBeNull();
  expect(reads.mock.calls.every(([request]) => request.id === selected.id && request.kind === EntityKind.AGENT)).toBe(true);
  client.clear();
});

it.each(["unknown", "missing", "unsupported", "wrong-id", "unavailable", "zero-revision"])("keeps a blank selected Agent decoration for %s evidence", async state => {
  const selected = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: state === "zero-revision" ? 0n : 1n, schemaVersion: state === "unsupported" ? 99 : 1, documentJson: encode({ name: "Selected name", ...(state === "missing" ? {} : { harness: state === "unknown" ? "future-harness" : "codex" }) }) });
  const response = state === "wrong-id" ? create(ResourceSchema, { ...selected, id: newRequestId() }) : selected;
  const read = vi.fn(() => { if (state === "unavailable") throw new ConnectError("Not found", Code.NotFound); return { resource: response }; });
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [] }), getResource: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent" kind={EntityKind.AGENT} value={selected.id} active change={vi.fn()} /></QueryClientProvider></TransportProvider>);
  await waitFor(() => expect(read).toHaveBeenCalled());
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
  const trigger = screen.getByRole("combobox", { name: "Agent" });
  expect(trigger.getAttribute("data-value")).toBe(selected.id);
  expect(trigger.querySelector(".scroll-picker-decoration")).toBeTruthy();
  expect(trigger.querySelector(".worker-harness-mark")).toBeNull();
  client.clear();
});

it.each([EntityKind.PROJECT, EntityKind.MACHINE, EntityKind.MODEL, EntityKind.ACCOUNT])("does not decorate non-Agent kind %s even if its data contains a harness", async kind => {
  const row = create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Plain choice", harness: "codex" }) });
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [row] }), getResource: () => ({ resource: row }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Plain" kind={kind} value={row.id} active change={vi.fn()} /></QueryClientProvider></TransportProvider>);
  const trigger = screen.getByRole("combobox", { name: "Plain" }); fireEvent.click(trigger);
  await screen.findByRole("option", { name: /Plain choice/ });
  expect(document.querySelector(".scroll-picker-decoration")).toBeNull();
  client.clear();
});

it("does not borrow a reached row's harness for its independently resolved selected identity", async () => {
  const row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Same Agent", harness: "codex" }) });
  const exact = create(ResourceSchema, { ...row, revision: 2n, documentJson: encode({ name: "Same Agent", harness: "claude-code" }) });
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [row] }), getResource: () => ({ resource: exact }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent" kind={EntityKind.AGENT} value={row.id} active change={vi.fn()} /></QueryClientProvider></TransportProvider>);
  const trigger = screen.getByRole("combobox", { name: "Agent" });
  await waitFor(() => expect(trigger.querySelector("[data-harness=claude-code]")).toBeTruthy());
  fireEvent.click(trigger);
  expect((await screen.findByRole("option", { name: "Same Agent" })).querySelector("[data-harness=codex]")).toBeTruthy();
  expect(trigger.querySelector("[data-harness=codex]")).toBeNull();
  client.clear();
});

it("reserves blank option slots for missing and unknown harnesses instead of inferring their names", async () => {
  const rows = [undefined, "future-harness"].map(harness => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Codex", harness }) }));
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: rows }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent" kind={EntityKind.AGENT} value="" active change={vi.fn()} /></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("combobox", { name: "Agent" }));
  for (const option of await screen.findAllByRole("option", { name: "Codex" })) {
    expect(option.querySelector(".scroll-picker-decoration")).toBeTruthy();
    expect(option.querySelector(".worker-harness-mark")).toBeNull();
  }
  client.clear();
});

it("fences a pending Agent selection and its decoration when the original connection is replaced", async () => {
  const first = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Retained Agent", harness: "codex" }) });
  const target = create(ResourceSchema, { ...first, id: newRequestId(), documentJson: encode({ name: "Pending Agent", harness: "claude-code" }) });
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const reads = vi.fn(async request => { if (request.id === target.id) await gate; return { resource: request.id === first.id ? first : target }; });
  const original = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [first, target] }), getResource: reads }));
  const updated = create(ResourceSchema, { ...first, revision: 2n, documentJson: encode({ name: "Retained Agent", harness: "opencode" }) });
  const replacement = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [updated] }), getResource: () => ({ resource: updated }) }));
  const oldClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }), newClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }), change = vi.fn();
  const view = (transport: typeof original, client: QueryClient) => <TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent" kind={EntityKind.AGENT} value={first.id} active change={change} /></QueryClientProvider></TransportProvider>;
  const rendered = render(view(original, oldClient));
  const trigger = screen.getByRole("combobox", { name: "Agent" });
  await waitFor(() => expect(trigger.querySelector("[data-harness=codex]")).toBeTruthy());
  fireEvent.click(trigger); fireEvent.click(await screen.findByRole("option", { name: "Pending Agent" }));
  await waitFor(() => expect(reads.mock.calls.some(([request]) => request.id === target.id)).toBe(true));
  rendered.rerender(view(replacement, newClient));
  await waitFor(() => expect(trigger.querySelector("[data-harness=opencode]")).toBeTruthy());
  await act(async () => release());
  expect(change).not.toHaveBeenCalled();
  expect(trigger.getAttribute("data-value")).toBe(first.id);
  expect(trigger.querySelector("[data-harness=claude-code]")).toBeNull();
  expect(trigger.querySelector("[data-harness=opencode]")).toBeTruthy();
  oldClient.clear(); newClient.clear();
});
