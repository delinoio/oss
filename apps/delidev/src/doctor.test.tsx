// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { useState, type ReactNode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, ErrorDetailSchema, ResourceService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { Doctor } from "./doctor";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";

function report(): Document {
  return { schema_version: 2, server_id: newRequestId(), version: "0.1.0", listener: "http://127.0.0.1:46310", observed_at: "2026-09-25T12:34:56Z", os: "linux", architecture: "amd64", protocol_version: 1, database_schema_version: 13, database: "ready", credential_store: "owner-credential-ready", inference_probes: false,
    storage: { result: { state: "observed" }, database_bytes: "0", wal_bytes: "9007199254740993", logical_database_bytes: "18446744073709551615", volume_capacity_bytes: "4096", volume_available_bytes: "1024", resources: [{ kind: "session", count: "42" }] },
    machines: [{ machine_id: newRequestId(), name: "First Worker", version: "0.1.0", os: "darwin", architecture: "arm64", active_stream: true, disabled: false, last_seen: "2026-09-25T01:00:00Z", installations: ["codex", "claude-code", "opencode", "grok-build"].map((harness) => ({ harness, state: "detected", version: "1.2.3", protocol_verified: false, observed_at: "2026-09-25T00:00:00Z", capabilities: ["fixture-read"] })) }], more_machines: false,
    credentials: [{ account_id: newRequestId(), connection_id: newRequestId(), result: { state: "observed" } }], more_credentials: false };
}
function fixture(initial: Document = report()) {
  const state = { report: initial };
  const doctor = vi.fn(async (): Promise<{ reportJson: Uint8Array }> => ({ reportJson: encode(state.report) }));
  const save = vi.fn(async () => ({}));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getDoctor: doctor });
    router.service(ResourceService, { listResources: () => ({ resources: [] }) });
    router.service(ConfigurationService, { saveConfiguration: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { state, doctor, save, client, view };
}
function disclosure(parent: HTMLElement, title: string) { return within(parent).getByText(title, { selector: "summary" }).parentElement as HTMLDetailsElement; }
function toggle(details: HTMLDetailsElement) { details.open = !details.open; fireEvent(details, new Event("toggle")); }
function allClosed(container: HTMLElement) { expect([...container.querySelectorAll("details")].every((details) => !details.open)).toBe(true); }
async function refresh() { fireEvent.click(screen.getByRole("button", { name: "Refresh diagnostics" })); await waitFor(() => expect((screen.getByRole("button", { name: "Refresh diagnostics" }) as HTMLButtonElement).disabled).toBe(false)); }
function deferred<T>() { let resolve!: (value: T) => void, reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }

it("owns the single Settings heading, three independent observations and every report field", async () => {
  const value = fixture();
  const view = render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  await screen.findByText("Read succeeded");
  expect(screen.getAllByRole("heading", { level: 1, name: "Connection & diagnostics" })).toHaveLength(1);
  expect(screen.getAllByText(/Read-only observations from the selected server/)).toHaveLength(1);
  expect(screen.getByText("2026-09-25T12:34:56Z")).toBeTruthy();
  expect(view.container.querySelectorAll(".diagnostics-observation")).toHaveLength(3);
  const server = screen.getByRole("region", { name: "Server information" });
  for (const label of ["Server version", "Server platform", "Protocol version", "Database schema", "Bound endpoint"]) expect(within(server).getByText(label)).toBeTruthy();
  const storage = screen.getByRole("region", { name: "Storage diagnostics" });
  for (const size of [0n, 9007199254740993n, 18446744073709551615n, 4096n, 1024n]) expect(within(storage).getByText(`${size.toLocaleString()} bytes`)).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Runner Devices" })).toBeTruthy();
  expect(screen.getByText(/Use AI accounts for validation and Runner Devices for discovery and connection recovery/)).toBeTruthy();
  const worker = screen.getByText("First Worker").closest("article")!;
  expect(worker.querySelectorAll(".diagnostics-installations > li")).toHaveLength(4);
  expect(within(worker).getAllByText("Handshake not checked")).toHaveLength(4);
  allClosed(view.container);
  expect(within(server).getByText(value.state.report.server_id as string).closest("details")?.open).toBe(false);
  expect(within(worker).getByText(/Machine identity:/).closest("details")?.open).toBe(false);
  expect(screen.queryByText(/healthy|reclaimable bytes|total storage|%/i)).toBeNull();
});

it("keeps failed storage, handshake, superseded account and partial notices outside closed disclosures", async () => {
  const data = report(), storage = data.storage as Document, machines = data.machines as Document[], credentials = data.credentials as Document[];
  storage.result = { state: "failed", code: "permission_denied", guidance: "Inspect storage permissions." };
  const installation = (machines[0].installations as Document[])[0];
  Object.assign(installation, { protocol_state: "failed", problem_code: "unavailable", guidance: "Inspect the retained handshake." });
  credentials[0].result = { state: "superseded", code: "conflict", guidance: "Refresh for the current connection." };
  data.more_credentials = true;
  const value = fixture(data), view = render(value.view(<Doctor active />));
  await screen.findByText("Inspect storage permissions.");
  for (const text of ["permission_denied", "Handshake failed", "unavailable", "Inspect the retained handshake.", "Connection changed during inspection", "Refresh for the current connection."]) expect(screen.getByText(text, { exact: false }).closest("details")).toBeNull();
  expect(screen.getByText(/Only the first 50 accounts/).closest("details")).toBeNull();
  expect(screen.getByText("Handshake failed").closest("article")).toBe(screen.getByText("First Worker").closest("article"));
  expect(screen.getByText("Refresh for the current connection.").closest("article")?.textContent).toContain("Account:");
  allClosed(view.container);
});

for (const counter of ["0", "9007199254740993", "18446744073709551615", undefined, null, 42, "01", "-1", "18446744073709551616"]) it(`preserves exact or unavailable byte/count semantics for ${String(counter)}`, async () => {
  const data = report(), storage = data.storage as Document;
  storage.database_bytes = counter; storage.resources = [{ kind: "session", count: counter }];
  const value = fixture(data); render(value.view(<Doctor active />));
  await screen.findByText("Database file");
  const valid = typeof counter === "string" && ["0", "9007199254740993", "18446744073709551615"].includes(counter);
  expect(screen.getByText("Database file").nextElementSibling?.textContent).toBe(valid ? `${BigInt(counter as string).toLocaleString()} bytes` : "Unavailable");
  toggle(disclosure(screen.getByRole("region", { name: "Storage diagnostics" }), "Retained resources"));
  expect(screen.getByText(`session: ${valid ? BigInt(counter as string).toLocaleString() : "Unknown"}`)).toBeTruthy();
});

it("resets disclosures on category departure and preserves their identities through refresh", async () => {
  const data = report(), machines = data.machines as Document[], credentials = data.credentials as Document[];
  machines.push({ ...machines[0], machine_id: newRequestId(), name: "Second Worker" });
  credentials.push({ ...credentials[0], account_id: newRequestId(), connection_id: newRequestId() });
  const value = fixture(data), view = render(value.view(<Settings />));
  await act(async () => { await value.client.invalidateQueries(); });
  expect(value.doctor).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" })); await screen.findByText("First Worker");
  toggle(disclosure(screen.getByText("First Worker").closest("article")!, "Installation details"));
  toggle(disclosure(screen.getByRole("region", { name: "Server information" }), "Server identity"));
  toggle(disclosure(screen.getByRole("region", { name: "Storage diagnostics" }), "Retained resources"));
  const account = screen.getByText(`Account: ${credentials[0].account_id}`).closest("article")!;
  toggle(disclosure(account, "Connection identity"));
  expect(value.doctor).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  await act(async () => { await value.client.invalidateQueries(); });
  expect(value.doctor).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" })); await screen.findByText("First Worker");
  allClosed(view.container);
  toggle(disclosure(screen.getByText("First Worker").closest("article")!, "Installation details"));
  toggle(disclosure(screen.getByText(`Account: ${credentials[0].account_id}`).closest("article")!, "Connection identity"));
  value.state.report = { ...data, machines: [...machines].reverse(), credentials: [...credentials].reverse() };
  await refresh();
  expect(disclosure(screen.getByText("First Worker").closest("article")!, "Installation details").open).toBe(true);
  expect(disclosure(screen.getByText("Second Worker").closest("article")!, "Installation details").open).toBe(false);
  expect(disclosure(screen.getByText(`Account: ${credentials[0].account_id}`).closest("article")!, "Connection identity").open).toBe(true);
  value.state.report = { ...data, machines: [{ ...machines[0], machine_id: newRequestId() }], credentials: [{ ...credentials[0], connection_id: newRequestId() }] };
  await refresh();
  expect(disclosure(screen.getByText("First Worker").closest("article")!, "Installation details").open).toBe(false);
  expect(screen.getByText("Connection identity").parentElement?.hasAttribute("open")).toBe(false);
  expect(value.save).not.toHaveBeenCalled();
  view.rerender(value.view(<Settings visible={false} />));
  allClosed(view.container);
});

for (const exit of ["navigation", "Escape then navigation"]) it(`resets all details on actual Settings ${exit} and reopens collapsed without mutations`, async () => {
  const value = fixture();
  function Harness() {
    const [visible, setVisible] = useState(false);
    return <><button onClick={() => setVisible(true)}>Open settings</button><button onClick={(event) => { event.currentTarget.focus(); setVisible(false); }}>Navigate away</button><button onClick={(event) => { event.currentTarget.focus(); setVisible(false); }}>Leave Settings fixture</button><Settings visible={visible} /></>;
  }
  const view = render(value.view(<Harness />));
  const opener = screen.getByRole("button", { name: "Open settings" }); opener.focus(); fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" })); await screen.findByText("Read succeeded");
  for (const details of view.container.querySelectorAll<HTMLDetailsElement>(".diagnostics details")) toggle(details);
  expect([...view.container.querySelectorAll<HTMLDetailsElement>(".diagnostics details")].every((details) => details.open)).toBe(true);
  const count = value.doctor.mock.calls.length;
  if (exit === "Escape then navigation") { fireEvent.keyDown(screen.getByRole("region", { name: "Settings content" }), { key: "Escape" }); expect([...view.container.querySelectorAll<HTMLDetailsElement>(".diagnostics details")].every((details) => details.open)).toBe(true); }
  fireEvent.click(screen.getByRole("button", { name: "Navigate away" }));
  expect(value.doctor).toHaveBeenCalledTimes(count);
  allClosed(view.container);
  expect(document.activeElement).not.toBe(opener);
  fireEvent.click(opener);
  expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  await screen.findByText("Read succeeded");
  allClosed(view.container);
  expect(value.save).not.toHaveBeenCalled();
});

it("never shares disclosure state across missing or changed server/record identities or connection remounts", async () => {
  const data = report(); delete data.server_id;
  const machine = (data.machines as Document[])[0]; delete machine.machine_id;
  const credential = (data.credentials as Document[])[0]; delete credential.account_id;
  const value = fixture(data), view = render(value.view(<Doctor active />));
  await screen.findByText("Read succeeded");
  for (const details of view.container.querySelectorAll<HTMLDetailsElement>("details")) toggle(details);
  value.state.report = { ...data, listener: "http://127.0.0.1:46310/changed" }; await refresh(); allClosed(view.container);
  value.state.report = { ...data, server_id: newRequestId() }; await refresh();
  for (const details of view.container.querySelectorAll<HTMLDetailsElement>("details")) toggle(details);
  value.state.report = { ...value.state.report, machines: [{ ...machine, name: "Replacement" }], credentials: [{ ...credential, connection_id: newRequestId() }] }; await refresh();
  expect(disclosure(screen.getByText("Replacement").closest("article")!, "Installation details").open).toBe(false);
  expect(disclosure(screen.getByRole("region", { name: "Protected credential diagnostics" }), "Connection identity").open).toBe(false);
  const next = fixture(); view.rerender(next.view(<Doctor key="another-connection" active />)); await screen.findByText("First Worker"); allClosed(view.container);
});

it("announces deferred initial reads and refreshes while retaining the original timestamp on refresh failure", async () => {
  const data = report(), value = fixture(data), initial = deferred<{ reportJson: Uint8Array }>();
  value.doctor.mockImplementationOnce(() => initial.promise);
  render(value.view(<Doctor active />));
  await screen.findByRole("status"); expect((screen.getByRole("button", { name: "Refresh diagnostics" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByText("Read succeeded")).toBeNull();
  await act(async () => initial.resolve({ reportJson: encode(data) })); await screen.findByText("Read succeeded");
  const update = deferred<{ reportJson: Uint8Array }>(); value.doctor.mockImplementationOnce(() => update.promise);
  fireEvent.click(screen.getByRole("button", { name: "Refresh diagnostics" })); await screen.findByRole("status");
  expect(screen.getByText("2026-09-25T12:34:56Z")).toBeTruthy(); expect(screen.getByText("Read succeeded")).toBeTruthy();
  expect((screen.getByRole("button", { name: "Refresh diagnostics" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => update.reject(new ConnectError("private fixture failure", Code.Unavailable, { "x-delidev-correlation-id": newRequestId() })));
  await screen.findByText(/report below is the last returned observation/);
  expect(screen.getByText("2026-09-25T12:34:56Z")).toBeTruthy(); expect(screen.queryByText("private fixture failure")).toBeNull();
});

for (const code of [Code.Unauthenticated, Code.PermissionDenied]) it(`keeps initial authorization failure ${code} separate from summaries and empty inventories`, async () => {
  const value = fixture(), reference = newRequestId();
  value.doctor.mockRejectedValue(new ConnectError("Fixture authorization denied.", code, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: code === Code.Unauthenticated ? "unauthenticated" : "permission_denied", guidance: "Inspect the selected authorized connection.", correlationId: reference }) }]));
  render(value.view(<Doctor active />)); await screen.findByRole("alert");
  expect(screen.queryByText("Read succeeded")).toBeNull(); expect(screen.queryByText(/No Workers|No accounts/)).toBeNull();
  expect(screen.getByText("Inspect the selected authorized connection.")).toBeTruthy(); expect(screen.getByText(`Reference: ${reference}`)).toBeTruthy();
});

for (const encoding of ["future", "json", "utf8", "oversized"]) it(`rejects ${encoding} reports without a summary`, async () => {
  const value = fixture();
  const reportJson = encoding === "future" ? encode({ ...report(), schema_version: 999 }) : encoding === "json" ? new TextEncoder().encode("{") : encoding === "utf8" ? new Uint8Array([255]) : new Uint8Array((1 << 20) + 1);
  value.doctor.mockResolvedValue({ reportJson }); render(value.view(<Doctor active />)); await screen.findByRole("alert");
  expect(screen.queryByText("Read succeeded")).toBeNull();
});

for (const inventory of [undefined, [], Array.from({ length: 51 }, (_, i) => ({ machine_id: newRequestId(), name: `Worker ${i}`, installations: [] }))]) for (const more of [undefined, false, true]) it(`keeps missing/empty/bounded inventories distinct with completeness ${String(more)}`, async () => {
  const data = report(); data.machines = inventory; data.credentials = inventory?.map((machine) => ({ account_id: machine.machine_id, connection_id: newRequestId(), result: { state: "unavailable" } })); data.more_machines = more; data.more_credentials = more;
  const value = fixture(data); render(value.view(<Doctor active />)); await screen.findByText("Read succeeded");
  const workers = screen.getByRole("region", { name: "Worker diagnostics" }), accounts = screen.getByRole("region", { name: "Protected credential diagnostics" });
  if (inventory === undefined) { expect(within(workers).getByText("Worker observations are unavailable.")).toBeTruthy(); expect(within(accounts).getByText("Protected storage observations are unavailable.")).toBeTruthy(); }
  else if (!inventory.length) { expect(within(workers).getByText("No Workers are registered.")).toBeTruthy(); expect(within(accounts).getByText(/No accounts are configured/)).toBeTruthy(); }
  else { expect(workers.querySelectorAll("article")).toHaveLength(50); expect(accounts.querySelectorAll("article")).toHaveLength(50); expect(within(workers).queryByText("Worker 50")).toBeNull(); }
  for (const region of [workers, accounts]) { expect(within(region).queryByText("Inventory completeness is unknown.") !== null).toBe(more === undefined); expect(within(region).queryByText(/Only the first 50/) !== null).toBe(more === true); }
});

it("preserves legacy fields and field-level unknown classifications without inventing health or executing HTML", async () => {
  const data = report(), value = fixture({ version: "0.1.0", server_id: data.server_id, listener: data.listener, database: "ready", credential_store: "owner-credential-ready", inference_probes: false });
  const view = render(value.view(<Doctor active />)); await screen.findByText(/legacy report/);
  expect(screen.queryByRole("region", { name: "Storage diagnostics" })).toBeNull();
  const markup = '<img src="x" onerror="alert(1)">';
  (data.storage as Document).result = { state: "future", guidance: markup };
  const machine = (data.machines as Document[])[0]; machine.name = markup; machine.machine_id = "x".repeat(1024);
  (machine.installations as Document[])[0] = { harness: "codex", state: "future", protocol_state: "future", guidance: markup, capabilities: [markup] };
  (data.credentials as Document[])[0].result = { state: "future", guidance: markup };
  data.listener = "http://127.0.0.1/" + "x".repeat(1024);
  value.state.report = data; await refresh();
  expect(screen.getByText("Unknown installation state", { exact: false })).toBeTruthy(); expect(screen.getByText("Unknown protocol state")).toBeTruthy();
  expect(view.container.querySelector("img, a, [style]")).toBeNull(); expect(view.container.querySelector(".diagnostics-observation")?.textContent).toContain("Read succeeded");
  for (const text of screen.getAllByText(markup)) if (!text.textContent?.startsWith("Reported capabilities")) expect(text.closest("details")).toBeNull();
});
