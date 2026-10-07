// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ErrorDetailSchema, ProviderService, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { LocalWorkerAction, LocalWorkerManagementState, LocalWorkerState, type ControlLocalWorker, type LocalWorkerStatus } from "./local-worker-controls";
import { Settings } from "./settings";

type Page = { resources: Resource[]; nextPageToken?: string };
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function machine(name: string, schemaVersion = 1) { return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 7n, schemaVersion, documentJson: encode({ name, health: "Observed health", harness: "Observed harness" }) }); }
function status(state = LocalWorkerState.Uncertain): LocalWorkerStatus { return { state, machine_id: newRequestId(), generation: newRequestId(), controller_active: false }; }
function fixture(rows: Resource[] = []) {
  const list = vi.fn(async (_kind: EntityKind, _page: string, _size: number): Promise<Page> => ({ resources: rows }));
  const get = vi.fn(async (id: string) => ({ resource: rows.find(row => row.id === id) }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: request => list(request.filter?.kind ?? EntityKind.UNSPECIFIED, request.filter?.pageToken ?? "", request.filter?.pageSize ?? 0), getResource: request => get(request.id) });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [] }), listProviderPresets: () => ({ presetsJson: encode([]) }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode, currentTransport = transport) => <StrictMode><TransportProvider transport={currentTransport}><QueryClientProvider client={client}>{children}</QueryClientProvider></TransportProvider></StrictMode>;
  return { list, get, view, transport };
}
function open(value: ReturnType<typeof fixture>, control?: ControlLocalWorker) {
  render(value.view(<Settings controlLocalWorker={control} />));
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
}
function failure(code: Code) {
  const correlationId = newRequestId();
  return { correlationId, error: new ConnectError("The server denied this read.", code, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "unavailable", guidance: "Retry the selected server read.", correlationId }) }]) };
}

it("preserves automatic presentation through the category lifetime before native status arrives", async () => {
  const value = fixture(), pending = deferred<LocalWorkerStatus>();
  const control = Object.assign(vi.fn(async (_action: LocalWorkerAction) => pending.promise), { automatic: true });
  open(value, control);
  expect(screen.getByText("DeliDev automatically starts and maintains this Worker while the app is running. Harnesses must already be installed.")).toBeTruthy();
  expect(screen.getByText("Waiting for the authenticated local connection before checking this Worker.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Register this computer" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Start local Worker" })).toBeNull();
  await act(async () => pending.resolve({ ...status(LocalWorkerState.Running), controller_active: true, management: { state: LocalWorkerManagementState.Running, attempts: 0, retry_ms: 0, owned_by_app: false } }));
  await screen.findByText("Running");
  expect(control.mock.calls.every(([action]) => action === LocalWorkerAction.Status)).toBe(true);
});

it("keeps keyboard focus in the main region when blocked recovery opens diagnostics", async () => {
  const value = fixture(), current: LocalWorkerStatus = { ...status(), management: { state: LocalWorkerManagementState.Blocked, attempts: 1, retry_ms: 0, owned_by_app: false, failure: "unconfirmed-exit" } };
  const control = Object.assign(vi.fn(async (_action: LocalWorkerAction) => current), { automatic: true });
  render(value.view(<main id="main" tabIndex={-1}><Settings controlLocalWorker={control} /></main>));
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  const worker = await screen.findByRole("region", { name: "Worker on this computer" });
  const diagnostics = await within(worker).findByRole("button", { name: "Connection & diagnostics" });
  diagnostics.focus();
  expect(document.activeElement).toBe(diagnostics);
  fireEvent.click(diagnostics);
  expect(document.activeElement).toBe(screen.getByRole("main"));
  expect(screen.getByRole("heading", { level: 1, name: "Connection & diagnostics" })).toBeTruthy();
  expect(control.mock.calls.every(([action]) => action === LocalWorkerAction.Status)).toBe(true);
});

it("renders the approved uncertain/loading hierarchy without duplicate guidance or fake records", async () => {
  const value = fixture(), pending = deferred<Page>(), current = status(); value.list.mockReturnValue(pending.promise);
  const control = vi.fn(async (_action: LocalWorkerAction) => current); open(value, control);
  await screen.findByText("Exit unconfirmed");
  expect(screen.getAllByText("Worker exit is unconfirmed. Inspect its private log and original session recovery before explicitly replacing the controller.")).toHaveLength(1);
  expect(screen.getByText(`Execution machine: ${current.machine_id}`)).toBeTruthy();
  const inventory = screen.getByRole("region", { name: "Saved runner devices" });
  expect(within(inventory).getByRole("status").textContent).toBe("Loading runner devices...");
  expect(inventory.querySelectorAll(".settings-runner-skeleton-row")).toHaveLength(2);
  expect(inventory.querySelector(".settings-runner-skeletons")?.getAttribute("aria-hidden")).toBe("true");
  expect(inventory.querySelectorAll("article")).toHaveLength(0);
  expect(screen.queryByText("No saved entries.")).toBeNull();
  expect(screen.getByRole("button", { name: "Start local Worker" }).className).toBe("primary");
  expect((screen.getByRole("button", { name: "Stop local Worker" }) as HTMLButtonElement).disabled).toBe(false);
  expect(screen.queryByRole("button", { name: "Load more Settings pages" })).toBeNull();
  await waitFor(() => expect(value.list).toHaveBeenCalledWith(EntityKind.MACHINE, "", 50));
  expect(control.mock.calls.every(([action]) => action === LocalWorkerAction.Status)).toBe(true);
});
it("retains original order, parser fallback, full IDs and schema-gated inspect actions", async () => {
  const rows = [machine("Zulu"), machine("Alpha"), machine("Hidden future name", 2)], value = fixture(rows); open(value);
  const inventory = screen.getByRole("region", { name: "Saved runner devices" }); await within(inventory).findByText("Zulu");
  expect(Array.from(inventory.querySelectorAll("article h3"), node => node.textContent)).toEqual(["Zulu", "Alpha", "Unnamed"]);
  for (const row of rows) expect(within(inventory).getByText(row.id)).toBeTruthy();
  expect((within(inventory).getAllByRole("button", { name: "Inspect installed harnesses" })[2] as HTMLButtonElement).disabled).toBe(true);
  expect(within(inventory).getAllByText("Status: Observed health")).toHaveLength(2);
  expect(value.get).not.toHaveBeenCalled();
  expect(screen.getByText("Configure these entries through the DeliDev CLI.")).toBeTruthy();
});
it("hides pages only for successful empty first pages without continuation", async () => {
  const value = fixture(); open(value); await screen.findByText("No saved entries.");
  expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
});
it("retains empty continuation/later pages and uses the original opaque cursor", async () => {
  const value = fixture(), token = "opaque/+==?token";
  value.list.mockImplementation(async (_kind, page) => ({ resources: [], nextPageToken: page ? "" : token })); open(value);
  await screen.findByText("No saved entries on this page."); fireEvent.click(screen.getByRole("button", { name: "Load more Settings pages" }));
  await waitFor(() => expect(value.list).toHaveBeenCalledWith(EntityKind.MACHINE, token, 50));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Settings pages" })).toBeNull());
  expect(screen.getByText("No saved entries.")).toBeTruthy();
});
it.each([Code.PermissionDenied, Code.Unavailable])("keeps initial failure %s distinct from successful emptiness", async code => {
  const value = fixture(), pending = deferred<Page>(), problem = failure(code); value.list.mockReturnValue(pending.promise); open(value);
  expect(screen.getByRole("status").textContent).toBe("Loading runner devices...");
  await act(async () => pending.reject(problem.error));
  expect((await screen.findByRole("alert")).textContent).toContain(problem.correlationId);
  expect(screen.queryByText(/No saved entries/)).toBeNull();
  expect(screen.queryByText("Loading runner devices...")).toBeNull();
  expect(screen.getByRole("button", { name: "Refresh settings" })).toBeTruthy();
});
it.each([false, true])("retains the previous observation on failed refresh (empty=%s) without a Worker read", async empty => {
  const value = fixture(empty ? [] : [machine("Retained machine")]), current = status(), control = vi.fn(async (_action: LocalWorkerAction) => current); open(value, control);
  await screen.findByText(empty ? "No saved entries." : "Retained machine"); await screen.findByText("Exit unconfirmed");
  const calls = control.mock.calls.length, pending = deferred<Page>(), problem = failure(Code.Unavailable); value.list.mockReturnValue(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  expect(screen.getByText(empty ? "No saved entries." : "Retained machine")).toBeTruthy();
  await act(async () => pending.reject(problem.error));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.getByText(empty ? "No saved entries." : "Retained machine")).toBeTruthy();
  expect(screen.getByRole("alert").textContent).toContain(problem.correlationId);
  expect(control.mock.calls).toHaveLength(calls);
});
it("retains the Runner Devices list beneath detail and removes its scope for other categories", async () => {
  const row = machine("Detail machine"), value = fixture([row]); open(value, async () => status());
  const worker = await screen.findByRole("region", { name: "Worker on this computer" });
  const column = worker.closest(".settings-runner-column");
  expect(column).not.toBeNull();
  const content = screen.getByRole("region", { name: "Settings content" }); expect(content.classList.contains("settings-runner-devices")).toBe(true);
  fireEvent.click(await screen.findByRole("button", { name: "Inspect installed harnesses" }));
  expect(content.classList.contains("settings-runner-devices")).toBe(true); expect(screen.getByRole("heading", { name: "Saved runner devices", hidden: true })).toBeTruthy();
  expect(worker.closest("[hidden]")).toBeNull();
  expect(worker.closest(".settings-runner-column")).toBe(column);
  expect(worker.closest("fieldset")?.hasAttribute("inert")).toBe(true);
  expect(screen.getByText(/These optional checks help troubleshoot failures/)).toBeTruthy(); fireEvent.click(screen.getByRole("button", { name: "Back to Runner Devices" }));
  expect(content.classList.contains("settings-runner-devices")).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Instructions" })); expect(screen.getByRole("region", { name: "Settings content" }).classList.contains("settings-runner-devices")).toBe(false);
});
it("observes an accepted Start after category return without restoring uncertainty or replaying it", async () => {
  const value = fixture(); let current: LocalWorkerStatus = { ...status(LocalWorkerState.NotStarted), generation: undefined };
  const control = vi.fn(async (action: LocalWorkerAction): Promise<LocalWorkerStatus> => {
    if (action === LocalWorkerAction.Start) { current = { ...status(LocalWorkerState.Starting), controller_active: true }; throw new Error("unknown launch"); }
    return current;
  });
  const view = render(value.view(<Settings controlLocalWorker={control} />)); fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start local Worker" })); await screen.findByText("Starting");
  fireEvent.click(screen.getByRole("button", { name: "Instructions" })); fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  view.rerender(value.view(<Settings controlLocalWorker={control} />, fixture().transport));
  await screen.findByText("Starting");
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh local Worker" }));
  await waitFor(() => expect(control.mock.calls.filter(([action]) => action === LocalWorkerAction.Start)).toHaveLength(1));
});
it.each(["navigation", "Escape then navigation"])("rejects an old native result after %s leaves the visit", async route => {
  const value = fixture(), pending = deferred<LocalWorkerStatus>(), original = status(LocalWorkerState.NotStarted), replacement = status(LocalWorkerState.Running);
  let current = original;
  const control = vi.fn(async (action: LocalWorkerAction) => action === LocalWorkerAction.Start ? pending.promise : current);
  function Harness() { const [visible, show] = useState(false); return <><button onClick={() => show(true)}>Open Settings fixture</button><button onClick={(event) => { event.currentTarget.focus(); show(false); }}>Leave Settings fixture</button><Settings visible={visible} controlLocalWorker={control} /></>; }
  render(value.view(<Harness />)); const opener = screen.getByRole("button", { name: "Open Settings fixture" }); opener.focus(); fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" })); fireEvent.click(await screen.findByRole("button", { name: "Start local Worker" }));
  if (route === "Escape then navigation") { fireEvent.keyDown(screen.getByRole("region", { name: "Settings content" }), { key: "Escape" }); expect(screen.getByRole("region", { name: "Settings content" })).toBeTruthy(); }
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Leave Settings fixture" })); current = replacement; fireEvent.click(opener);
  expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy(); fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  await screen.findByText(`Execution machine: ${replacement.machine_id}`); const calls = control.mock.calls.length;
  await act(async () => pending.resolve(status(LocalWorkerState.Uncertain)));
  expect(screen.getByText(`Execution machine: ${replacement.machine_id}`)).toBeTruthy(); expect(control.mock.calls).toHaveLength(calls);
});
