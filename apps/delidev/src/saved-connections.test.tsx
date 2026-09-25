import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { SavedConnections, SavedConnectionState, type SavedConnection, type SavedConnectionActions } from "./saved-connections";

function fixture() {
  const profile: SavedConnection = { version: 1, id: newRequestId(), name: "Saved server", endpoint: "https://fixture.example.test", server_id: newRequestId(), pairing_id: newRequestId(), device_id: newRequestId(), state: SavedConnectionState.Paired, created_at: "2026-09-25T00:00:00Z" };
  const grant = JSON.stringify({ version: 1, endpoint: profile.endpoint, server_id: profile.server_id, pairing_id: profile.pairing_id, code: "private-single-use-fixture-code" });
  const list = vi.fn(async () => [profile]);
  const pair = vi.fn(async (id: string, _name: string, _grant: string) => ({ ...profile, id }));
  const retry = vi.fn(async (_id: string) => profile);
  const open = vi.fn(async (_id: string) => {});
  const actions: SavedConnectionActions = { list, pair, retry, open };
  return { profile, grant, actions, list, pair, retry, open };
}
it("retains an uncertain pairing and masked original input across dialog visibility", async () => {
  const value = fixture(); value.pair.mockRejectedValueOnce("timed-out");
  const view = render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  await screen.findByRole("button", { name: "Open Saved server" });
  fireEvent.change(screen.getByLabelText("Connection name"), { target: { value: "New server" } });
  const input = screen.getByLabelText("Private client pairing document") as HTMLInputElement;
  expect(input.type).toBe("password");
  fireEvent.change(input, { target: { value: value.grant } });
  expect(screen.getByText(`Pairing endpoint: ${value.profile.endpoint}`)).toBeTruthy();
  expect(screen.queryByText("private-single-use-fixture-code")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  await screen.findByText(/operation has not been confirmed/);
  expect((screen.getByLabelText("Connection name") as HTMLInputElement).disabled).toBe(true);
  view.rerender(<SavedConnections visible={false} close={() => {}} actions={value.actions} />);
  view.rerender(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(screen.getByRole("button", { name: "Retry original server pairing" }));
  await screen.findByText(/client pairing is saved/);
  expect(value.pair).toHaveBeenCalledTimes(2);
  expect(value.pair.mock.calls[0]).toEqual(value.pair.mock.calls[1]);
  expect(value.pair.mock.calls[0][2]).toBe(value.grant);
  expect(input.value).toBe("");
  expect(value.open).not.toHaveBeenCalled();
});
it("loads only while visible and separates opening from exact persisted pairing retry", async () => {
  const value = fixture();
  const pending = { ...value.profile, id: newRequestId(), name: "Pending server", state: SavedConnectionState.Pending, device_id: "" };
  value.list.mockResolvedValue([value.profile, pending]);
  value.retry.mockImplementation(async (id) => ({ ...pending, id, state: SavedConnectionState.Paired, device_id: newRequestId() }));
  const view = render(<SavedConnections visible={false} close={() => {}} actions={value.actions} />);
  expect(value.list).not.toHaveBeenCalled();
  view.rerender(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(await screen.findByRole("button", { name: "Open Saved server" }));
  await waitFor(() => expect(value.open).toHaveBeenCalledWith(value.profile.id));
  await waitFor(() => expect((screen.getByRole("button", { name: "Retry Pending server" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Retry Pending server" }));
  await screen.findByText("The original client pairing is saved.");
  expect(value.retry).toHaveBeenCalledWith(pending.id);
  expect(value.pair).not.toHaveBeenCalled();
});
it("does not let an older inventory read erase a new pairing failure", async () => {
  const value = fixture();
  let release!: (profiles: SavedConnection[]) => void;
  value.list.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
  value.pair.mockRejectedValueOnce("timed-out");
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.change(screen.getByLabelText("Connection name"), { target: { value: "Retained" } });
  fireEvent.change(screen.getByLabelText("Private client pairing document"), { target: { value: value.grant } });
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  await screen.findByText(/operation has not been confirmed/);
  release([value.profile]);
  await waitFor(() => expect(screen.getByText(/operation has not been confirmed/)).toBeTruthy());
  expect(value.pair).toHaveBeenCalledTimes(1);
});
