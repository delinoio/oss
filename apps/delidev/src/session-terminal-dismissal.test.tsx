// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, TerminalService, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTabsProvider } from "./session-tabs";
import { SessionTerminals } from "./session-terminals";
vi.mock("./terminal-emulator", () => ({ openTerminalScreen: (host: HTMLElement, input: (bytes: Uint8Array) => void) => {
 const field = document.createElement("textarea"); field.setAttribute("aria-label", "Terminal input"); field.addEventListener("keydown", event => { if (event.key === "Enter" && !field.disabled) input(new Uint8Array([120])); }); host.append(field);
 return { write: async () => {}, enabled: (value: boolean) => { field.disabled = !value; }, focus: () => field.focus(), dispose: () => host.replaceChildren() };
} }));
function fixture(count = 1, initialExited = false, offPage = false) {
 const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
 const rows = Array.from({ length: count }, () => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 4n, documentJson: encode({ state: initialExited ? "exited" : "running", cleanup_verified: initialExited }) }));
 let inventory = offPage ? [] : rows, release = () => {}, event: typeof rows[number] | undefined, fail = false;
 const held = new Promise<void>(resolve => { release = resolve; });
 const open = vi.fn(), close = vi.fn(), control = vi.fn(), createTerminal = vi.fn(), getResource = vi.fn(() => ({ resource: rows[0] }));
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
  router.service(ResourceService, { getResource, listResources: () => ({ resources: inventory }) });
  router.service(TerminalService, { controlTerminal: control, createTerminal, watchTerminalOutput: async function* (request, context) {
   const terminal = rows.find(value => value.id === request.terminalId)!; const epoch = newRequestId();
   yield { epoch, sequence: 0n, terminal, heartbeat: true };
   await held; if (context.signal.aborted) return;
   if (fail) throw new ConnectError("fixture disconnect", Code.Unavailable);
   if (event) yield { epoch, sequence: 0n, terminal: event, heartbeat: true };
  } });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTabsProvider><SessionTerminals session={session} close={close} selectedId={offPage ? rows[0]!.id : undefined} openTerminal={open} /></SessionTabsProvider></MutationIntents></TransportProvider></QueryClientProvider>);
 return { rows, open, close, control, createTerminal, getResource, exit(at = 0, change: object = {}) { event = create(ResourceSchema, { ...rows[at]!, revision: 5n, documentJson: encode({ state: "exited", cleanup_verified: true }), ...change }); release(); }, end(error = false) { fail = error; release(); }, async inventoryExit(at: number) { inventory = rows.map((row, index) => index === at ? create(ResourceSchema, { ...row, revision: 5n, documentJson: encode({ state: "exited", cleanup_verified: true }) }) : row); fireEvent.click(screen.getByRole("button", { name: "Details" })); fireEvent.click(screen.getByRole("button", { name: "Refresh terminals" })); }, cleanup() { view.unmount(); release(); client.clear(); } };
}
it("dismisses matching newer watch metadata and hides the final dock exactly once without process operations", async () => {
 const test = fixture(); try {
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ })); await screen.findByRole("textbox", { name: "Terminal input" });
  test.exit(); await waitFor(() => expect(screen.queryByRole("tab")).toBeNull()); expect(test.close).toHaveBeenCalledTimes(1);
  expect(test.control).not.toHaveBeenCalled(); expect(test.createTerminal).not.toHaveBeenCalled();
  await test.inventoryExit(0); await waitFor(() => expect(screen.queryByRole("tab")).toBeNull()); expect(test.close).toHaveBeenCalledTimes(1);
 } finally { test.cleanup(); }
});
it.each([0, 1])("selected exit %i selects the nearest remaining terminal", async at => {
 const test = fixture(3); try {
  fireEvent.click(await screen.findByRole("tab", { name: new RegExp(`Terminal ${at + 1}`) })); await screen.findByRole("textbox", { name: "Terminal input" });
  test.exit(at); await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
  const selected = screen.getAllByRole("tab").find(tab => tab.getAttribute("aria-selected") === "true"); expect(selected?.id).toBe(`terminal-tab-${test.rows[at === 0 ? 1 : 0]!.id}`); expect(test.close).not.toHaveBeenCalled();
 } finally { test.cleanup(); }
});
it("inactive inventory exit preserves selection and focus", async () => {
 const test = fixture(3); try {
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 2/ })); const input = await screen.findByRole("textbox", { name: "Terminal input" }); await waitFor(() => expect(document.activeElement).toBe(input));
  await test.inventoryExit(0); input.focus(); await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
  expect(document.activeElement).toBe(input); expect(screen.getByRole("tab", { selected: true }).id).toBe(`terminal-tab-${test.rows[1]!.id}`); expect(test.close).not.toHaveBeenCalled();
 } finally { test.cleanup(); }
});
it("an explicitly opened exited-only inventory keeps creation available without hiding", async () => {
 const test = fixture(2, true); try { await waitFor(() => expect(screen.getByRole("button", { name: "Create terminal" })).toHaveProperty("disabled", false)); expect(screen.queryAllByRole("tab")).toHaveLength(0); expect(test.close).not.toHaveBeenCalled(); } finally { test.cleanup(); }
});
it.each([false, true])("stream completion or failure %s retains the terminal and reattachment", async error => {
 const test = fixture(); try { fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ })); await screen.findByRole("textbox", { name: "Terminal input" }); test.end(error); await screen.findByRole("button", { name: "Reattach original terminal" }); expect(screen.getAllByRole("tab")).toHaveLength(1); expect(test.close).not.toHaveBeenCalled(); } finally { test.cleanup(); }
});
it.each([{ sessionId: "foreign" }, { schemaVersion: 2 }, { documentJson: encode({ state: "exited", cleanup_verified: "true" }) }, { revision: 3n }, { documentJson: new TextEncoder().encode("invalid") }])("untrusted or incomplete watch evidence retains presentation", async change => {
 const test = fixture(); try { fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ })); await screen.findByRole("textbox", { name: "Terminal input" }); test.exit(0, change); await waitFor(() => expect(screen.getByRole("button", { name: "Reattach original terminal" })).toBeTruthy()); expect(screen.getAllByRole("tab")).toHaveLength(1); expect(test.close).not.toHaveBeenCalled(); } finally { test.cleanup(); }
});

it("verified exit retains exact uncertain input recovery until the original receipt resolves", async () => {
 const test = fixture(); test.control.mockRejectedValueOnce(new ConnectError("fixture acknowledgment lost", Code.Unavailable));
 try {
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
  const input = await screen.findByRole("textbox", { name: "Terminal input" });
  await waitFor(() => expect(input).toHaveProperty("disabled", false)); fireEvent.keyDown(input, { key: "Enter" });
  await screen.findByRole("button", { name: "Retry the same terminal operation" });
  test.exit(); await waitFor(() => expect(screen.getByRole("button", { name: "Retry the same terminal operation" })).toBeTruthy());
  await new Promise(resolve => setTimeout(resolve, 30));
  expect(screen.getAllByRole("tab")).toHaveLength(1); expect(test.close).not.toHaveBeenCalled();
  test.control.mockResolvedValueOnce({ terminal: create(ResourceSchema, { ...test.rows[0]!, revision: 5n, documentJson: encode({ state: "exited", cleanup_verified: true }) }) });
  fireEvent.click(screen.getByRole("button", { name: "Retry the same terminal operation" }));
  await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
  expect(test.control.mock.calls[1]![0]).toEqual(test.control.mock.calls[0]![0]);
 } finally { test.cleanup(); }
});

it("an explicit ID outside the inventory dismisses only after its own exit without extra reads", async () => {
 const test = fixture(1, false, true); try {
  await screen.findByRole("textbox", { name: "Terminal input" }); await screen.findByRole("tab", { name: /Terminal 1/ });
  expect(test.getResource).toHaveBeenCalledTimes(1); expect(test.close).not.toHaveBeenCalled();
  test.exit(); await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
  expect(test.close).toHaveBeenCalledTimes(1); expect(test.getResource).toHaveBeenCalledTimes(1);
  expect(test.control).not.toHaveBeenCalled(); expect(test.createTerminal).not.toHaveBeenCalled();
 } finally { test.cleanup(); }
});

it("a late original creation acknowledgment cannot replace presentation after verified exit", async () => {
 const test = fixture(); let accept = () => {};
 const receipt = new Promise<{ terminal: typeof test.rows[number] }>(resolve => { accept = () => resolve({ terminal: test.rows[0]! }); });
 test.createTerminal.mockImplementation(() => receipt);
 try {
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ })); await screen.findByRole("textbox", { name: "Terminal input" });
  fireEvent.click(screen.getByRole("button", { name: "Create terminal" })); await waitFor(() => expect(test.createTerminal).toHaveBeenCalledTimes(1));
  test.exit(); await waitFor(() => expect(test.close).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
  accept(); await waitFor(() => expect(screen.getByRole("button", { name: "Create terminal" })).toHaveProperty("disabled", false));
  expect(test.open).toHaveBeenCalledTimes(1); expect(screen.queryByRole("tab")).toBeNull(); expect(test.control).not.toHaveBeenCalled();
 } finally { accept(); test.cleanup(); }
});
