// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { DeviceService, EntityKind, ResourceSchema, ResourceService, type ListResourcesRequest, type Resource } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { deviceDate } from "./device-settings";
import { document, encode } from "./documents";

const ids = [1, 2, 3, 4].map((value) => `019a0000-0000-7000-8000-${String(value).padStart(12, "0")}`);
const rows = [
  { name: "DeliDev desktop", type: "client", revoked: true, paired_at: "2026-09-28T23:52:02.761239Z", revoked_at: "2026-09-29T02:03:40.902482Z" },
  { name: "DeliDev local Worker", type: "worker", revoked: false, paired_at: "2026-09-29T00:58:53.515872Z", machine_id: ids[3] },
  { name: "DeliDev desktop", type: "client", revoked: false, paired_at: "2026-09-29T02:29:00.978919Z" },
].map((value, index) => create(ResourceSchema, { id: ids[index], kind: EntityKind.DEVICE, schemaVersion: 1, revision: 3n, documentJson: encode(value) }));
const authority = { endpoint: "https://paired.example.test", serverId: ids[3] };
type Page = { resources: Resource[]; nextPageToken?: string };
function fixture(initial = rows) {
  const state = { rows: initial, fail: false, next: "", pending: undefined as Promise<Page> | undefined };
  const list = vi.fn(async (request: ListResourcesRequest): Promise<Page> => {
    if (request.filter?.kind !== EntityKind.DEVICE) return { resources: [] };
    if (state.fail) throw new ConnectError("Synthetic read failure", Code.PermissionDenied, { "x-delidev-correlation-id": ids[3] });
    return state.pending ?? { resources: state.rows, nextPageToken: state.next };
  });
  const read = vi.fn((request: { id: string }) => ({ resource: state.rows.find((row) => row.id === request.id) }));
  const revoke = vi.fn(async (request: { mutation?: { id: string } }) => {
    const original = state.rows.find((row) => row.id === request.mutation?.id)!;
    const device = create(ResourceSchema, { ...original, revision: 4n, documentJson: encode({ ...document(original), revoked: true }) });
    state.rows = state.rows.map((row) => row.id === device.id ? device : row);
    return { device };
  });
  const issue = vi.fn();
  const transport = () => createRouterTransport((router) => {
    router.service(ResourceService, { listResources: list, getResource: read });
    router.service(DeviceService, { revokeDevice: revoke, createPairing: issue });
  });
  const firstTransport = transport();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (options: { visible?: boolean; pairing?: boolean; transport?: Transport } = {}) => <StrictMode><TransportProvider transport={options.transport ?? firstTransport}><QueryClientProvider client={client}><Settings currentDeviceId={ids[2]} visible={options.visible ?? true} pairingAuthority={options.pairing ? authority : undefined} /></QueryClientProvider></TransportProvider></StrictMode>;
  return { state, list, read, revoke, issue, transport, client, view };
}
async function open() {
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  await screen.findByRole("button", { name: "Details for DeliDev local Worker" });
}
function workerDetails() { return screen.getByRole("button", { name: "Details for DeliDev local Worker" }); }

it("renders the approved server order and local Details without an RPC or inferred connection state", async () => {
  const value = fixture(); render(value.view({ pairing: true })); await open();
  const panel = workerDetails().closest<HTMLElement>(".paired-device-list")!;
  expect([...panel.querySelectorAll("article h3")].map((node) => node.textContent)).toEqual(rows.map((row) => document(row).name));
  expect(within(panel).getAllByText("Authorized")).toHaveLength(2);
  expect(within(panel).getAllByText("Revoked")).toHaveLength(1);
  expect(within(panel).getAllByText("This desktop client")).toHaveLength(1);
  expect(screen.getAllByText("Authorization does not mean this device is currently connected.")).toHaveLength(1);
  expect(screen.getAllByRole("button", { name: /^Revoke / })).toHaveLength(1);
  expect(screen.getByText("Paired: 29 Sep 2026, 00:58 UTC")).toBeTruthy();
  expect(screen.getByText("Paired: 28 Sep 2026, 23:52 UTC · Revoked: 29 Sep 2026, 02:03 UTC")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "Create pairing document" })).toHaveLength(1);
  const control = workerDetails(), before = value.list.mock.calls.length;
  expect(control.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(control);
  expect(control.getAttribute("aria-expanded")).toBe("true");
  const disclosure = window.document.getElementById(control.getAttribute("aria-controls")!)!;
  expect(disclosure.hidden).toBe(false);
  expect(within(disclosure).getByText(ids[1])).toBeTruthy();
  expect(within(disclosure).getByText(ids[3])).toBeTruthy();
  expect(within(disclosure).getByText("Runner Device ID")).toBeTruthy();
  expect(within(disclosure).getByText("2026-09-29T00:58:53.515872Z")).toBeTruthy();
  expect(value.list).toHaveBeenCalledTimes(before); expect(value.read).not.toHaveBeenCalled(); expect(value.revoke).not.toHaveBeenCalled(); expect(value.issue).not.toHaveBeenCalled();
});

// Keep retained-state transitions and the two reset boundaries independent so
// each scenario fits the normal test deadline when CI runs files concurrently.
it("retains disclosure within a category and resets it after category departure", async () => {
  const value = fixture();
  const view = render(value.view()); await open(); fireEvent.click(workerDetails());
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.DEVICE)).toHaveLength(2));
  expect(workerDetails().getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Instructions" })); await open();
  expect(workerDetails().getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(workerDetails());
  view.rerender(value.view({ transport: value.transport() }));
  fireEvent(window, new Event("resize"));
  expect(workerDetails().getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Revoke DeliDev local Worker" }));
  fireEvent.click(screen.getByRole("button", { name: "Keep device authorized" }));
  expect(workerDetails().getAttribute("aria-expanded")).toBe("true");
  expect(window.document.activeElement).toBe(screen.getByRole("button", { name: "Revoke DeliDev local Worker" }));
});

it("preserves disclosure through appended paired-device pages within a visit", async () => {
  const value = fixture(); value.state.next = "opaque-next";
  render(value.view()); await open(); fireEvent.click(workerDetails());
  fireEvent.click(screen.getByRole("button", { name: "Load more Settings pages" }));
  await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.filter?.pageToken === "opaque-next")).toBe(true));
  expect(workerDetails().getAttribute("aria-expanded")).toBe("true");
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.DEVICE).every(([request]) => request.filter?.pageSize === 50)).toBe(true);
});

it("disposes disclosure when the Settings visit ends and reopens at AI Subscription", async () => {
  const value = fixture(); const view = render(value.view()); await open();
  fireEvent.click(workerDetails());
  expect(workerDetails().getAttribute("aria-expanded")).toBe("true");
  view.rerender(value.view({ visible: false })); view.rerender(value.view());
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
  expect(screen.queryByRole("combobox", { name: "Settings category" })).toBeNull();
  await open(); expect(workerDetails().getAttribute("aria-expanded")).toBe("false");
});

it("prunes removed disclosures only after successful replacement and retains them on failed refresh", async () => {
  const value = fixture(); render(value.view()); await open(); fireEvent.click(workerDetails());
  value.state.fail = true; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(workerDetails().getAttribute("aria-expanded")).toBe("true");
  value.state.fail = false; value.state.rows = [rows[0], rows[2]];
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Details for DeliDev local Worker" })).toBeNull());
  value.state.rows = rows; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByRole("button", { name: "Details for DeliDev local Worker" });
  expect(workerDetails().getAttribute("aria-expanded")).toBe("false");
});

it("keeps unknown, unsupported and long values inert without inventing authorization or dates", async () => {
  const name = `<b>${"LongDevice".repeat(60)}</b>`;
  const unknown = create(ResourceSchema, { ...rows[1], documentJson: encode({ name, type: "future-type", paired_at: "2026-02-30T00:00:00Z", revoked_at: "<invalid>" }) });
  const future = create(ResourceSchema, { ...rows[0], schemaVersion: 2 });
  const value = fixture([unknown, future]); render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  const details = await screen.findByRole("button", { name: `Details for ${name}` });
  const row = details.closest("article")!;
  expect(within(row).getByRole("heading", { name }).querySelector("b")).toBeNull();
  expect(within(row).getAllByText("Unknown")).toHaveLength(2);
  expect(within(row).getByText("Paired: Unknown")).toBeTruthy();
  fireEvent.click(details);
  expect(within(row).getByText("2026-02-30T00:00:00Z")).toBeTruthy(); expect(within(row).getByText("<invalid>")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Revoke / })).toBeNull();
  expect(screen.getByRole("button", { name: "Details for Unknown" })).toBeTruthy();
  expect(screen.queryByText("Revoked")).toBeNull();
});

it.each(["", "bad", "2026-02-30T00:00:00Z", "2026-13-01T00:00:00Z", "2026-09-29T24:00:00Z"])("does not normalize invalid summary date %s", (raw) => expect(deviceDate(raw)).toBe("Unknown"));
it("formats offsets and leap dates in UTC without changing the source", () => {
  expect(deviceDate("2024-02-29T09:58:53.515872+09:00")).toBe("29 Feb 2024, 00:58 UTC");
});

it.each([true, false])("shows a successful final empty first page with authority %s and no duplicate action", async (pairing) => {
  const value = fixture([]); render(value.view({ pairing }));
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  await screen.findByRole("heading", { name: "No paired devices yet" });
  expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
  expect(screen.queryAllByRole("button", { name: "Create pairing document" })).toHaveLength(pairing ? 1 : 0);
  expect(screen.getByText(pairing ? "Choose Create pairing document to pair a desktop client or manually installed Worker." : "Pairing document creation is unavailable for this connection.")).toBeTruthy();
  value.state.fail = true; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.queryByRole("heading", { name: "No paired devices yet" })).toBeNull();
  expect(screen.queryByText("No paired devices on this page")).toBeNull();
});

it.each([Code.PermissionDenied, Code.Unavailable])("distinguishes loading and initial read failure %s from emptiness without changing pairing authority", async (code) => {
  const value = fixture([]);
  let reject!: (error: Error) => void;
  value.state.pending = new Promise((_resolve, fail) => { reject = fail; });
  render(value.view({ pairing: true })); fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  expect(screen.getByText("Loading paired devices…")).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "No paired devices yet" })).toBeNull();
  expect(screen.getAllByRole("button", { name: "Create pairing document" })).toHaveLength(1);
  reject(new ConnectError("Synthetic read failure", code));
  await screen.findByRole("alert");
  expect(screen.queryByRole("heading", { name: "No paired devices yet" })).toBeNull();
  expect(screen.queryByText("No paired devices on this page")).toBeNull();
  expect(screen.queryByText("Refresh failed. Showing the last successfully loaded results.")).toBeNull();
  expect(screen.getAllByRole("button", { name: "Create pairing document" })).toHaveLength(1);
});

it("preserves opaque continuation without unrelated cached rows", async () => {
  const value = fixture([]); value.state.next = "opaque-continuation"; render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" })); await screen.findByText("No paired devices on this page");
  fireEvent.click(screen.getByRole("button", { name: "Load more Settings pages" }));
  await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.filter?.pageToken === "opaque-continuation")).toBe(true));
  expect(screen.queryByRole("button", { name: "Details for DeliDev local Worker" })).toBeNull();
});

it("retains pairing through reconnect and discards it after category departure", async () => {
  const value = fixture(); const view = render(value.view({ pairing: true })); await open();
  fireEvent.click(screen.getByRole("button", { name: "Create pairing document" }));
  const name = screen.getByRole("textbox", { name: "Device name" });
  expect(window.document.activeElement).toBe(name);
  expect(screen.queryByRole("button", { name: "Create pairing document" })).toBeNull();
  fireEvent.change(name, { target: { value: "Retained draft" } });
  const type = screen.getByRole("combobox", { name: "Device type" }); type.focus();
  view.rerender(value.view({ pairing: true, transport: value.transport() })); fireEvent(window, new Event("resize"));
  expect(window.document.activeElement).toBe(type);
  fireEvent.click(screen.getByRole("button", { name: "Instructions" })); await open();
  expect(screen.queryByRole("textbox", { name: "Device name" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Create pairing document" }));
  expect((screen.getByRole("textbox", { name: "Device name" }) as HTMLInputElement).value).toBe("");
  fireEvent.click(screen.getByRole("button", { name: "Cancel pairing" }));
  const trigger = screen.getByRole("button", { name: "Create pairing document" });
  await waitFor(() => expect(window.document.activeElement).toBe(trigger));
  expect(trigger.closest(".settings-toolbar")).toBeTruthy();
  expect(value.issue).not.toHaveBeenCalled();
});

it("returns confirmed revocation to Details once and falls back to Refresh when the canceled row is absent", async () => {
  const value = fixture(); render(value.view({ pairing: true })); await open();
  const pairing = screen.getByRole("button", { name: "Create pairing document" });
  fireEvent.click(screen.getByRole("button", { name: "Revoke DeliDev local Worker" }));
  expect(pairing.isConnected).toBe(true);
  expect(pairing.closest("[hidden]")).toBeNull();
  expect(pairing.closest("fieldset")?.hasAttribute("inert")).toBe(true);
  const confirm = screen.getByRole("button", { name: "Confirm device revocation" }) as HTMLButtonElement;
  await waitFor(() => expect(confirm.disabled).toBe(false)); fireEvent.click(confirm);
  await screen.findByText("Authorization revoked for DeliDev local Worker.");
  expect(screen.getByText("Retained sessions stay saved. Revocation does not confirm native cleanup or erase the device's private files.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Return to devices" }));
  await waitFor(() => expect(window.document.activeElement).toBe(workerDetails()));
  const refresh = screen.getByRole("button", { name: "Refresh settings" }); refresh.focus();
  fireEvent.click(refresh); await waitFor(() => expect(screen.queryByRole("button", { name: "Revoke DeliDev local Worker" })).toBeNull());
  expect(window.document.activeElement).toBe(refresh);
  value.state.rows = rows; await value.client.invalidateQueries();
  fireEvent.click(await screen.findByRole("button", { name: "Revoke DeliDev local Worker" }));
  value.state.rows = []; await value.client.invalidateQueries();
  fireEvent.click(screen.getByRole("button", { name: "Keep device authorized" }));
  await waitFor(() => expect(window.document.activeElement).toBe(screen.getByRole("button", { name: "Refresh settings" })));
  expect(value.revoke).toHaveBeenCalledTimes(1);
});
