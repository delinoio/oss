import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, BackupCreationState, BackupDeletionState, newRequestId } from "@delinoio/delidev-api-client";
import { Backups } from "./backups";
import { MutationIntents } from "./mutation";

function fixture() {
  const id = newRequestId();
  const backup = { id, revision: 1n, sizeBytes: 9007199254740993n, modifiedAt: "2026-09-29T00:00:00Z" };
  const list = vi.fn(async () => ({ backups: [backup] }));
  const inspect = vi.fn(async () => ({ backup, sha256: "a".repeat(64), schemaVersion: 20, serverId: newRequestId() }));
  const creation = { id: newRequestId(), backupId: id, revision: 1n, state: BackupCreationState.PENDING, problemCode: "" };
  const create = vi.fn(async (_input: unknown) => ({ job: creation, requestId: newRequestId(), replayed: false }));
  const creations = vi.fn(async () => ({ jobs: [creation] }));
  const remove = vi.fn(async (_input: unknown) => ({ job: { id: newRequestId(), backupId: id, revision: 1n, state: BackupDeletionState.PENDING } }));
  const deletions = vi.fn(async () => ({ jobs: [] }));
  const transport = createRouterTransport(router => router.service(SystemService, { listBackups: list, inspectBackup: inspect, requestBackup: create, listBackupCreations: creations, deleteBackup: remove, listBackupDeletions: deletions }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Backups active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { id, list, inspect, create, creation, creations, remove, deletions, view };
}

it("lists metadata without inspecting automatically and preserves exact byte counts", async () => {
  const f = fixture();
  const view = render(f.view(false));
  expect(f.list).not.toHaveBeenCalled();
  view.rerender(f.view());
  await screen.findByText(/9007199254740993 bytes/);
  expect(f.inspect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: `Inspect backup ${f.id}` }));
  await screen.findByText("Database integrity and original server identity verified.");
  f.inspect.mockRejectedValueOnce(new ConnectError("Backup changed", Code.FailedPrecondition));
  fireEvent.click(screen.getByRole("button", { name: "Recheck selected backup" }));
  await screen.findByRole("alert");
  expect(f.inspect).toHaveBeenCalledTimes(2);
  expect(screen.queryByText("Database integrity and original server identity verified.")).toBeNull();
});

it("retries an uncertain creation with its original request after hiding settings", async () => {
  const f = fixture();
  f.create.mockRejectedValueOnce(new ConnectError("Acknowledgement lost", Code.Unavailable));
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await screen.findByRole("button", { name: "Retry the same backup creation" });
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect((screen.getByRole("button", { name: "Create database backup" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same backup creation" }));
  await waitFor(() => expect(f.create).toHaveBeenCalledTimes(2));
  expect(f.create.mock.calls[0]![0]).toEqual(f.create.mock.calls[1]![0]);
  await screen.findByText(`Backup creation accepted: ${f.creation.id}`);
});


it("requires inspected confirmation and retains the exact deletion after an uncertain response", async () => {
  const f = fixture();
  f.remove.mockRejectedValueOnce(new ConnectError("Acknowledgement lost", Code.Unavailable));
  const view = render(f.view());
  expect(screen.queryByRole("button", { name: "Permanently delete selected backup" })).toBeNull();
  fireEvent.click(await screen.findByRole("button", { name: `Inspect backup ${f.id}` }));
  const button = await screen.findByRole("button", { name: "Permanently delete selected backup" });
  expect((button as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("checkbox", { name: `I confirm permanent deletion of backup ${f.id}` }));
  fireEvent.click(button);
  await screen.findByRole("button", { name: "Retry the same backup deletion" });
  const first = f.remove.mock.calls[0]![0];
  expect(first).toMatchObject({ backup: { id: f.id, revision: 1n, sizeBytes: 9007199254740993n }, sha256: "a".repeat(64) });
  view.rerender(f.view(false));
  view.rerender(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Retry the same backup deletion" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(2));
  expect(f.remove.mock.calls[1]![0]).toEqual(first);
  await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull());
});


it("shows durable creation outcomes without treating pending or stale observations as publication", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Backup creation pending");
  expect(screen.queryByText("Backup creation completed")).toBeNull();
  f.creations.mockResolvedValueOnce({ jobs: [{ ...f.creation, revision: 2n, state: BackupCreationState.SUCCEEDED }] });
  fireEvent.click(screen.getByRole("button", { name: "Refresh creation jobs" }));
  await screen.findByText("Backup creation completed");
  await waitFor(() => expect(f.list.mock.calls.length).toBeGreaterThan(1));
  f.creations.mockRejectedValueOnce(new ConnectError("Server unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh creation jobs" }));
  await screen.findByText("Previous creation observations are shown; current state is unavailable.");
});

it.each(["hide", "reinspect"])("requires fresh deletion confirmation after %s replaces inspection", async action => {
  const f = fixture();
  const view = render(f.view());
  fireEvent.click(await screen.findByRole("button", { name: `Inspect backup ${f.id}` }));
  const checkboxName = `I confirm permanent deletion of backup ${f.id}`;
  fireEvent.click(await screen.findByRole("checkbox", { name: checkboxName }));
  expect((screen.getByRole("button", { name: "Permanently delete selected backup" }) as HTMLButtonElement).disabled).toBe(false);
  f.inspect.mockResolvedValueOnce({ backup: { id: f.id, revision: 1n, sizeBytes: 123n, modifiedAt: "2026-09-29T01:00:00Z" }, sha256: "b".repeat(64), schemaVersion: 21, serverId: newRequestId() });
  if (action === "hide") {
    view.rerender(f.view(false));
    view.rerender(f.view());
  } else {
    fireEvent.click(screen.getByRole("button", { name: `Inspect backup ${f.id}` }));
  }
  await screen.findByText("b".repeat(64));
  const checkbox = screen.getByRole("checkbox", { name: checkboxName }) as HTMLInputElement;
  const remove = screen.getByRole("button", { name: "Permanently delete selected backup" }) as HTMLButtonElement;
  expect(checkbox.checked).toBe(false);
  expect(remove.disabled).toBe(true);
  fireEvent.click(remove);
  expect(f.remove).not.toHaveBeenCalled();
  fireEvent.click(checkbox);
  fireEvent.click(remove);
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
  expect(f.remove.mock.calls[0]![0]).toMatchObject({ backup: { id: f.id, sizeBytes: 123n }, sha256: "b".repeat(64) });
});
