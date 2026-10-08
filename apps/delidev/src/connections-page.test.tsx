// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { TransportProvider } from "@connectrpc/connect-query";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { ConnectionsPage, PersistentConnectionView, type ConnectionPageSlots } from "./connections-page";
import { LocalConnectionPresentationProvider } from "./local-connection-presentation";
import { LocalServerControls, LocalServerState } from "./local-server";
import { SavedConnections, SavedConnectionState, type SavedConnectionActions } from "./saved-connections";
import { MutationIntents } from "./mutation";

function fixture() {
  const profile = { version: 1, revision: 2, id: newRequestId(), name: "Studio server", endpoint: "https://studio.example", server_id: newRequestId(), pairing_id: newRequestId(), device_id: newRequestId(), state: SavedConnectionState.Paired, created_at: "2026-10-08T00:00:00Z" };
  const actions: SavedConnectionActions = {
    list: vi.fn(async () => [profile, { ...profile, id: newRequestId(), name: "Build server", endpoint: "https://build.example" }]),
    removed: vi.fn(async () => ({ connections: [] })),
    remove: vi.fn(async () => profile), retainedWorker: vi.fn(), pair: vi.fn(async () => profile), retry: vi.fn(async () => profile), rename: vi.fn(async () => profile), open: vi.fn(async () => {}),
  };
  const stop = vi.fn(async (_request: unknown) => ({})), start = vi.fn();
  const transport = createRouterTransport(router => router.service(SystemService, { stopServer: stop }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  function Harness() {
    const [shown, show] = useState(true), [slots, setSlots] = useState<ConnectionPageSlots>();
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><button onClick={() => show(value => !value)}>Toggle Settings</button>
      {shown ? <ConnectionsPage local onSlots={setSlots} /> : null}
      <LocalConnectionPresentationProvider inline={false} target={slots?.current}><LocalServerControls connected endpoint="http://127.0.0.1:46300" status={{ state: LocalServerState.Ready, attempts: 0, retry_ms: 0 }} restart={start} busy={false} /></LocalConnectionPresentationProvider>
      <SavedConnections target={slots?.saved} advancedTarget={slots?.advanced} visible={Boolean(slots)} close={() => {}} actions={actions} />
      <PersistentConnectionView target={slots?.advanced} hidden><p role="alert">Retained update needs inspection</p></PersistentConnectionView>
    </MutationIntents></QueryClientProvider></TransportProvider>;
  }
  return { actions, stop, start, Harness, profile };
}
it("shows the current Local connection and stored profiles without probes or mutations", async () => {
  const value = fixture(); render(<value.Harness />);
  await screen.findByRole("button", { name: "Open Studio server" });
  expect(screen.getByText("Connected")).toBeTruthy();
  expect(screen.getByText("http://127.0.0.1:46300")).toBeTruthy();
  expect(screen.getAllByText("Saved")).toHaveLength(2);
  expect(screen.queryByRole("dialog", { name: "Saved servers" })).toBeNull();
  expect(value.stop).not.toHaveBeenCalled(); expect(value.start).not.toHaveBeenCalled(); expect(value.actions.open).not.toHaveBeenCalled(); expect(value.actions.removed).not.toHaveBeenCalled();
  expect(screen.getByText("Advanced controls need attention.")).toBeTruthy();
  const advanced = screen.getByText("Advanced").closest("details")!;
  expect(advanced.open).toBe(false); fireEvent.click(screen.getByText("Advanced"));
  expect(value.actions.removed).not.toHaveBeenCalled();
});
it("retains the original stop confirmation and exact uncertain request across page departure", async () => {
  const value = fixture(); value.stop.mockRejectedValueOnce(new ConnectError("Response lost", Code.Unavailable));
  render(<value.Harness />); await screen.findByRole("button", { name: "Open Studio server" });
  fireEvent.click(screen.getByRole("button", { name: "Stop local server" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" })); fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm server stop" }));
  await screen.findByRole("button", { name: "Retry the same server stop" });
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" })); fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same server stop" }));
  await waitFor(() => expect(value.stop).toHaveBeenCalledTimes(2));
  expect(value.stop.mock.calls[0]?.[0]).toEqual(value.stop.mock.calls[1]?.[0]); expect(value.start).not.toHaveBeenCalled();
});
it("retains saved rows after a failed inventory read and keeps pending private pairing bytes", async () => {
  const value = fixture(); render(<value.Harness />); await screen.findByRole("button", { name: "Open Studio server" });
  vi.mocked(value.actions.list).mockRejectedValueOnce("timed-out");
  fireEvent.click(screen.getByRole("button", { name: "Refresh saved servers" }));
  await screen.findByText("Saved server inventory is unavailable. Showing the last confirmed profiles.");
  expect(screen.getByRole("button", { name: "Open Studio server" })).toBeTruthy(); expect(screen.queryByText("No saved servers")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add server" }));
  const input = screen.getByLabelText("Private client pairing document") as HTMLInputElement;
  const grant = JSON.stringify({ version: 1, endpoint: value.profile.endpoint, server_id: value.profile.server_id, pairing_id: value.profile.pairing_id, code: "private-fixture" });
  fireEvent.change(screen.getByLabelText("Connection name"), { target: { value: "Original name" } }); fireEvent.change(input, { target: { value: grant } });
  vi.mocked(value.actions.pair).mockRejectedValueOnce("timed-out");
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" })); await screen.findByRole("button", { name: "Retry original server pairing" });
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" })); fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  expect((screen.getByLabelText("Private client pairing document") as HTMLInputElement).value).toBe(grant);
  fireEvent.click(screen.getByRole("button", { name: "Retry original server pairing" })); await waitFor(() => expect(value.actions.pair).toHaveBeenCalledTimes(2));
  expect(vi.mocked(value.actions.pair).mock.calls[0]).toEqual(vi.mocked(value.actions.pair).mock.calls[1]);
});
it("retains original saved-profile rename and removal receipts across page movement", async () => {
  const value = fixture(); render(<value.Harness />); await screen.findByRole("button", { name: "Open Studio server" });
  const menu = screen.getByLabelText("Actions for Studio server"); fireEvent.click(menu);
  fireEvent.click(screen.getByRole("button", { name: "Rename Studio server" }));
  fireEvent.change(screen.getByLabelText("New connection name"), { target: { value: "Original rename" } });
  vi.mocked(value.actions.rename).mockRejectedValueOnce("timed-out");
  fireEvent.click(screen.getByRole("button", { name: "Save connection name" })); await screen.findByRole("button", { name: "Retry original name edit" });
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" })); fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry original name edit" })); await waitFor(() => expect(value.actions.rename).toHaveBeenCalledTimes(2));
  expect(vi.mocked(value.actions.rename).mock.calls[0]).toEqual(vi.mocked(value.actions.rename).mock.calls[1]);
  fireEvent.click(screen.getByRole("button", { name: "Discard name edit" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove Studio server" }));
  vi.mocked(value.actions.remove).mockRejectedValueOnce("timed-out");
  fireEvent.click(screen.getByRole("button", { name: "Confirm connection removal" })); await screen.findByRole("button", { name: "Retry original connection removal" });
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" })); fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry original connection removal" })); await waitFor(() => expect(value.actions.remove).toHaveBeenCalledTimes(2));
  expect(vi.mocked(value.actions.remove).mock.calls[0]).toEqual(vi.mocked(value.actions.remove).mock.calls[1]);
  expect(vi.mocked(value.actions.remove).mock.calls[0]?.[0]).toBe(value.profile.id);
  expect(vi.mocked(value.actions.remove).mock.calls[0]?.[2]).toBe(value.profile.revision);
});
