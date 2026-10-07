import { toBinary } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, BackupCreationState, BackupDeletionState, newRequestId, DeleteBackupRequestSchema, RequestBackupRequestSchema, type DeleteBackupRequest, type RequestBackupRequest } from "@delinoio/delidev-api-client";
import { Backups } from "./backups";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";

function fixture() {
  const id = newRequestId();
  const backup = { id, revision: 1n, sizeBytes: 9007199254740993n, modifiedAt: "2026-09-29T00:00:00Z" };
  const list = vi.fn(async (_input: { pageToken: string }): Promise<{ backups: typeof backup[]; nextPageToken?: string }> => ({ backups: [backup] }));
  const inspect = vi.fn(async (_input: { id: string }) => ({ backup, sha256: "a".repeat(64), schemaVersion: 20, serverId: newRequestId() }));
  const creation = { id: newRequestId(), backupId: id, revision: 1n, state: BackupCreationState.PENDING, problemCode: "" };
  const create = vi.fn(async (_input: unknown) => ({ job: creation, requestId: newRequestId(), replayed: false }));
  const creations = vi.fn(async (_input: { pageToken: string }): Promise<{ jobs: typeof creation[]; nextPageToken?: string }> => ({ jobs: [creation] }));
  const deletion = { id: newRequestId(), backupId: id, revision: 1n, state: BackupDeletionState.PENDING };
  const getCreation = vi.fn(async (_input: { id: string }) => ({ job: creation }));
  const getDeletion = vi.fn(async (_input: { id: string }) => ({ job: deletion }));
  const remove = vi.fn(async (_input: unknown) => ({ job: deletion }));
  const deletions = vi.fn(async (_input: { pageToken: string }): Promise<{ jobs: typeof deletion[]; nextPageToken?: string }> => ({ jobs: [] }));
  const transport = createRouterTransport(router => router.service(SystemService, { listBackups: list, inspectBackup: inspect, requestBackup: create, getBackupCreation: getCreation, getBackupDeletion: getDeletion, listBackupCreations: creations, deleteBackup: remove, listBackupDeletions: deletions }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Backups active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  const lifetimeView = (active = true) => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><SettingsLifetime>{() => <MutationIntents><Backups active={active} /></MutationIntents>}</SettingsLifetime></QueryClientProvider></TransportProvider></StrictMode>;
  return { id, backup, lifetimeView, list, inspect, create, creation, creations, deletion, getCreation, getDeletion, remove, deletions, client, view };
}

it("lists metadata without inspecting automatically and preserves exact byte counts", async () => {
  const f = fixture();
  const view = render(f.view(false));
  expect(f.list).not.toHaveBeenCalled();
  view.rerender(f.view());
  await screen.findByText(/9,007,199,254,740,993 bytes/);
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
  expect(toBinary(RequestBackupRequestSchema, f.create.mock.calls[0]![0] as RequestBackupRequest)).toEqual(toBinary(RequestBackupRequestSchema, f.create.mock.calls[1]![0] as RequestBackupRequest));
  await screen.findByRole("article", { name: `Backup ${f.creation.backupId} creation` });
});


it("requires inspected confirmation and retains the exact deletion after an uncertain response", async () => {
  const f = fixture();
  f.remove.mockRejectedValueOnce(new ConnectError("Acknowledgement lost", Code.Unavailable));
  const view = render(f.view());
  expect(screen.queryByRole("button", { name: "Permanently delete selected backup" })).toBeNull();
  fireEvent.click(await screen.findByRole("button", { name: `Inspect backup ${f.id}` }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete selected backup…" }));
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
  expect(toBinary(DeleteBackupRequestSchema, f.remove.mock.calls[1]![0] as DeleteBackupRequest)).toEqual(toBinary(DeleteBackupRequestSchema, first as DeleteBackupRequest));
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
  fireEvent.click(await screen.findByRole("button", { name: "Delete selected backup…" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: checkboxName }));
  expect((screen.getByRole("button", { name: "Permanently delete selected backup" }) as HTMLButtonElement).disabled).toBe(false);
  f.inspect.mockResolvedValueOnce({ backup: { id: f.id, revision: 1n, sizeBytes: 123n, modifiedAt: "2026-09-29T01:00:00Z" }, sha256: "b".repeat(64), schemaVersion: 21, serverId: newRequestId() });
  if (action === "hide") {
    view.rerender(f.view(false));
    view.rerender(f.view());
  } else {
    fireEvent.click(screen.getByRole("button", { name: "Keep backup" }));
    fireEvent.click(screen.getByRole("button", { name: "Recheck selected backup" }));
  }
  await screen.findByText("b".repeat(64));
  if (!screen.queryByRole("checkbox")) fireEvent.click(screen.getByRole("button", { name: "Delete selected backup…" }));
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

it("polls each accepted operation beyond the first history page and refreshes inventory on completion", async () => {
  const f = fixture();
  const oldJobs = Array.from({ length: 20 }, () => ({ ...f.creation, id: newRequestId(), backupId: newRequestId(), state: BackupCreationState.SUCCEEDED }));
  f.creations.mockResolvedValue({ jobs: oldJobs });
  f.deletions.mockResolvedValue({ jobs: [] });
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  const created = await screen.findByRole("article", { name: `Backup ${f.creation.backupId} creation` });
  await within(created).findByText("pending");
  expect(f.getCreation.mock.calls[0]![0].id).toBe(f.creation.id);
  fireEvent.click(await screen.findByRole("button", { name: `Inspect backup ${f.id}` }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete selected backup…" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: `I confirm permanent deletion of backup ${f.id}` }));
  fireEvent.click(screen.getByRole("button", { name: "Permanently delete selected backup" }));
  const removed = await screen.findByRole("article", { name: `Backup ${f.deletion.backupId} deletion` });
  await within(removed).findByText("pending");
  expect(f.getDeletion.mock.calls[0]![0].id).toBe(f.deletion.id);
  // Pause still-pending direct reads without discarding either accepted identity.
  view.rerender(f.view(false));
  const creationReads = f.getCreation.mock.calls.length, deletionReads = f.getDeletion.mock.calls.length;
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 2100)); });
  expect(f.getCreation).toHaveBeenCalledTimes(creationReads);
  expect(f.getDeletion).toHaveBeenCalledTimes(deletionReads);
  const reads = f.list.mock.calls.length;
  f.getCreation.mockResolvedValue({ job: { ...f.creation, revision: 2n, state: BackupCreationState.SUCCEEDED } });
  view.rerender(f.view());
  await within(created).findByRole("button", { name: `Dismiss backup ${f.creation.backupId} creation tracking` });
  await waitFor(() => expect(f.list.mock.calls.length).toBeGreaterThan(reads));
  const afterCreation = f.list.mock.calls.length;
  f.getDeletion.mockResolvedValue({ job: { ...f.deletion, revision: 2n, state: BackupDeletionState.SUCCEEDED } });
  await within(removed).findByRole("button", { name: `Dismiss backup ${f.deletion.backupId} deletion tracking` }, { timeout: 4000 });
  await waitFor(() => expect(f.list.mock.calls.length).toBeGreaterThan(afterCreation), { timeout: 4000 });
}, 15000);

it("retains multiple accepted creations and marks a failed direct refresh stale", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await screen.findByText("pending");
  const next = { ...f.creation, id: newRequestId(), backupId: newRequestId() };
  f.create.mockResolvedValueOnce({ job: next, requestId: newRequestId(), replayed: false });
  f.getCreation.mockImplementation(async input => ({ job: input.id === next.id ? next : f.creation }));
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await screen.findByRole("article", { name: `Backup ${next.backupId} creation` });
  const first = screen.getByRole("article", { name: `Backup ${f.creation.backupId} creation` });
  f.getCreation.mockRejectedValueOnce(new ConnectError("read failed", Code.Unavailable));
  await f.client.invalidateQueries();
  await within(first).findByText("The last observation is stale; current job status is unavailable.");
  expect(within(first).getByText("status unavailable")).toBeTruthy();
  expect(f.create).toHaveBeenCalledTimes(2);
});

it("renders the approved two-row table with UTC labels, full metadata and inert empty-history paging", async () => {
  const f = fixture();
  const backups = [
    { id: "01a0f137-12d3-73eb-b850-4fca15c1e840", revision: 1n, sizeBytes: 512000n, modifiedAt: "2026-09-30T07:28:33.494524877Z" },
    { id: "01a0eb55-e84d-7918-a97d-334f1de92886", revision: 1n, sizeBytes: 499712n, modifiedAt: "2026-09-29T04:04:31.251391719Z" },
  ];
  f.list.mockResolvedValue({ backups });
  f.creations.mockResolvedValue({ jobs: [] });
  render(f.view());
  const table = await screen.findByRole("table", { name: "Database backups" });
  await within(table).findByText("512,000 bytes");
  expect(within(table).getByText("499,712 bytes")).toBeTruthy();
  expect(within(table).getByText("Sep 30, 2026 · 07:28:33 UTC")).toBeTruthy();
  expect(within(table).getByText("Sep 29, 2026 · 04:04:31 UTC")).toBeTruthy();
  expect(within(table).getAllByRole("button").map(button => button.getAttribute("aria-label"))).toEqual(backups.map(backup => `Inspect backup ${backup.id}`));
  expect(within(table).getAllByText("Not checked")).toHaveLength(2);
  for (const backup of backups) {
    expect(within(table).getByText(`Original modification timestamp: ${backup.modifiedAt}`)).toBeTruthy();
    expect(table.querySelector(`time[datetime="${backup.modifiedAt}"]`)).toBeTruthy();
  }
  expect(screen.getByText("2 loaded")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /backup page/ })).toBeNull();
  await screen.findByText("No creation jobs on this page.");
  expect(screen.queryByRole("navigation", { name: "Creation job pages" })).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  await screen.findByText("No deletion jobs on this page.");
  expect(screen.queryByRole("navigation", { name: "Deletion job pages" })).toBeNull();
  expect(f.inspect).not.toHaveBeenCalled();
  expect(f.create).not.toHaveBeenCalled();
  expect(f.remove).not.toHaveBeenCalled();
});

it("preserves fractional timestamps and exact BigInt deletion operands, with raw invalid-date fallback", async () => {
  const f = fixture();
  const backup = { ...f.backup, modifiedAt: "2026-09-30T07:28:33.494524877Z" };
  const invalid = { ...backup, id: newRequestId(), modifiedAt: "unformattable-timestamp" };
  f.list.mockResolvedValue({ backups: [backup, invalid] });
  f.inspect.mockResolvedValue({ backup, sha256: "a".repeat(64), schemaVersion: 20, serverId: newRequestId() });
  render(f.view());
  await screen.findByText("unformattable-timestamp");
  fireEvent.click(screen.getByRole("button", { name: `Inspect backup ${f.id}` }));
  const detail = screen.getByRole("region", { name: "Backup integrity inspection" });
  await within(detail).findByText("9,007,199,254,740,993 bytes");
  expect(within(detail).getByText(backup.modifiedAt)).toBeTruthy();
  fireEvent.click(within(detail).getByRole("button", { name: "Delete selected backup…" }));
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByRole("button", { name: "Permanently delete selected backup" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
  expect(f.remove.mock.calls[0]![0]).toMatchObject({ backup, sha256: "a".repeat(64) });
});

it("keeps three cursor owners independent and changes tabs without extra reads", async () => {
  const f = fixture();
  f.list.mockImplementation(async input => ({ backups: [f.backup], nextPageToken: input.pageToken ? "" : "inventory-page-2" }));
  f.creations.mockImplementation(async input => ({ jobs: [f.creation], nextPageToken: input.pageToken ? "" : "creation-page-2" }));
  f.deletions.mockImplementation(async input => ({ jobs: [f.deletion], nextPageToken: input.pageToken ? "" : "deletion-page-2" }));
  render(f.view());
  await screen.findByText("Backup creation pending");
  fireEvent.click(screen.getByRole("button", { name: "Load more Database backups" }));
  await waitFor(() => expect(f.list.mock.calls.at(-1)![0].pageToken).toBe("inventory-page-2"));
  fireEvent.click(screen.getByRole("button", { name: "Load more Creation jobs" }));
  await waitFor(() => expect(f.creations.mock.calls.at(-1)![0].pageToken).toBe("creation-page-2"));
  const before = [f.list.mock.calls.length, f.creations.mock.calls.length, f.deletions.mock.calls.length];
  fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  expect([f.list.mock.calls.length, f.creations.mock.calls.length, f.deletions.mock.calls.length]).toEqual(before);
  fireEvent.click(screen.getByRole("button", { name: "Load more Deletion jobs" }));
  await waitFor(() => expect(f.deletions.mock.calls.at(-1)![0].pageToken).toBe("deletion-page-2"));
  fireEvent.click(screen.getByRole("tab", { name: "Creation jobs" }));
  expect(screen.queryByRole("button", { name: "Load more Creation jobs" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Load more Database backups" })).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  expect(screen.queryByRole("button", { name: "Load more Deletion jobs" })).toBeNull();
  expect(f.inspect).not.toHaveBeenCalled();
  expect(f.create).not.toHaveBeenCalled();
  expect(f.remove).not.toHaveBeenCalled();
});

it("manually activates stable history tabs and excludes hidden controls from keyboard navigation", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Backup creation pending");
  const creation = screen.getByRole("tab", { name: "Creation jobs" });
  const deletion = screen.getByRole("tab", { name: "Deletion jobs" });
  creation.focus();
  fireEvent.keyDown(creation, { key: "ArrowRight" });
  expect(document.activeElement).toBe(deletion);
  expect(deletion.getAttribute("aria-selected")).toBe("false");
  expect(creation.getAttribute("aria-selected")).toBe("true");
  fireEvent.keyDown(deletion, { key: "Enter" });
  expect(deletion.getAttribute("aria-selected")).toBe("true");
  expect(screen.queryByRole("button", { name: "Refresh creation jobs" })).toBeNull();
  expect(document.getElementById(creation.getAttribute("aria-controls")!)?.hidden).toBe(true);
  fireEvent.keyDown(deletion, { key: "Home" });
  expect(document.activeElement).toBe(creation);
  fireEvent.keyDown(creation, { key: " " });
  expect(creation.getAttribute("aria-selected")).toBe("true");
  fireEvent.keyDown(creation, { key: "End" });
  expect(document.activeElement).toBe(deletion);
  fireEvent.keyDown(deletion, { key: "ArrowLeft" });
  expect(document.activeElement).toBe(creation);
  expect(creation.tabIndex).toBe(0);
  expect(deletion.tabIndex).toBe(-1);
});

it("focuses inspection only at activation and restores the opener or list-heading fallback", async () => {
  const f = fixture();
  let finish!: (value: Awaited<ReturnType<typeof f.inspect>>) => void;
  f.inspect.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  render(f.view());
  const opener = await screen.findByRole("button", { name: `Inspect backup ${f.id}` });
  fireEvent.click(opener);
  const heading = screen.getByRole("heading", { name: `Inspection: ${f.id}` });
  expect(document.activeElement).toBe(heading);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Backup integrity inspection" })));
  const stableAction = screen.getByRole("button", { name: "Close Backup integrity inspection" });
  stableAction.focus();
  await waitFor(() => expect(f.inspect).toHaveBeenCalledTimes(1));
  await act(async () => finish({ backup: f.backup, sha256: "a".repeat(64), schemaVersion: 20, serverId: newRequestId() }));
  expect(document.activeElement).toBe(stableAction);
  fireEvent.click(screen.getByRole("button", { name: "Close Backup integrity inspection" }));
  await waitFor(() => expect(document.activeElement).toBe(opener));
  fireEvent.click(opener);
  await screen.findByText("Database integrity and original server identity verified.");
  const reads = f.list.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Refresh backups" }));
  await act(async () => { await Promise.resolve(); });
  expect(f.list).toHaveBeenCalledTimes(reads);
  // The paused inventory must keep its original row. Independently exercise
  // safe focus fallback when an external DOM owner detaches that opener.
  opener.remove();
  fireEvent.click(screen.getByRole("button", { name: "Close Backup integrity inspection" }));
  expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Database backups" }));
});

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])("keeps initial failures (%s) separate from successful empty reads", async code => {
  const f = fixture();
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  const fail = async () => { await gate; throw new ConnectError("private fixture message", code, { "x-delidev-correlation-id": "fixture-reference" }); };
  f.list.mockImplementation(fail);
  f.creations.mockImplementation(fail);
  f.deletions.mockImplementation(fail);
  render(f.view());
  expect(screen.getByText("Reading managed backups…")).toBeTruthy();
  expect(screen.getByText("Reading creation jobs…")).toBeTruthy();
  fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  expect(screen.getByText("Reading deletion jobs…")).toBeTruthy();
  await act(async () => release());
  await screen.findAllByRole("alert");
  expect(screen.queryByText("No managed backups.")).toBeNull();
  expect(screen.queryByText("No creation jobs on this page.")).toBeNull();
  expect(screen.queryByText("No deletion jobs on this page.")).toBeNull();
  expect(screen.queryByText("private fixture message")).toBeNull();
});

it("retains cached inventory during an updating read and its failed refresh", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("9,007,199,254,740,993 bytes");
  let reject!: (reason: unknown) => void;
  f.list.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
  fireEvent.click(screen.getByRole("button", { name: "Refresh backups" }));
  await screen.findByText("Updating managed backups…");
  expect(screen.getByText(f.id)).toBeTruthy();
  expect((screen.getByRole("button", { name: `Inspect backup ${f.id}` }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => reject(new ConnectError("refresh failed", Code.Unavailable)));
  await screen.findByText("The previous backup list is shown. Refresh before relying on it.");
  expect(screen.getByText("9,007,199,254,740,993 bytes")).toBeTruthy();
  expect(screen.queryByText("No managed backups.")).toBeNull();
  expect((screen.getByRole("button", { name: "Create database backup" }) as HTMLButtonElement).disabled).toBe(false);
});

it("accumulates empty inventory pages and exhausts without a manual reset", async () => {
  const f = fixture();
  f.list.mockResolvedValueOnce({ backups: [], nextPageToken: "inventory-page-2" }).mockResolvedValue({ backups: [] });
  render(f.view());
  await screen.findByText("No managed backups.");
  fireEvent.click(screen.getByRole("button", { name: "Load more Database backups" }));
  await waitFor(() => expect(f.list).toHaveBeenCalledTimes(2));
  expect(f.list.mock.calls[1]![0].pageToken).toBe("inventory-page-2");
  expect(screen.queryByRole("button", { name: "Load more Database backups" })).toBeNull();
  expect(f.inspect).not.toHaveBeenCalled();
});

it.each(["creation", "deletion"])("stops repeated %s history cursors until explicit reload", async kind => {
  const f = fixture();
  const jobs = kind === "creation" ? f.creations : f.deletions;
  jobs.mockResolvedValue({ jobs: [], nextPageToken: `${kind}-page-2` });
  render(f.view());
  if (kind === "deletion") fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  await screen.findByText(`No ${kind} jobs on this page.`);
  fireEvent.click(screen.getByRole("button", { name: `Load more ${kind === "creation" ? "Creation jobs" : "Deletion jobs"}` }));
  await screen.findByRole("button", { name: "Reload list" });
  expect(jobs).toHaveBeenCalledTimes(2);
  expect(f.create).not.toHaveBeenCalled();
  expect(f.remove).not.toHaveBeenCalled();
});

it("clears fresh confirmation on a failed or in-flight reinspection", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.click(await screen.findByRole("button", { name: `Inspect backup ${f.id}` }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete selected backup…" }));
  fireEvent.click(await screen.findByRole("checkbox"));
  let fail!: (reason: unknown) => void;
  f.inspect.mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject; }));
  fireEvent.click(screen.getByRole("button", { name: "Keep backup" }));
  fireEvent.click(screen.getByRole("button", { name: "Recheck selected backup" }));
  await screen.findByText("Checking the selected backup…");
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.queryByText("Database integrity and original server identity verified.")).toBeNull();
  await waitFor(() => expect(f.inspect).toHaveBeenCalledTimes(2));
  await act(async () => fail(new ConnectError("changed", Code.FailedPrecondition)));
  await screen.findByRole("alert");
  expect(screen.queryByRole("button", { name: "Permanently delete selected backup" })).toBeNull();
  expect(f.remove).not.toHaveBeenCalled();
});

it("disposes tab, inspection and uncertain-write presentation on a Strict Mode opening replacement", async () => {
  const f = fixture();
  f.create.mockRejectedValueOnce(new ConnectError("lost acknowledgement", Code.Unavailable));
  const view = render(f.lifetimeView());
  fireEvent.click(await screen.findByRole("button", { name: `Inspect backup ${f.id}` }));
  await screen.findByRole("button", { name: "Delete selected backup…" });
  fireEvent.click(screen.getByRole("button", { name: "Close Backup integrity inspection" }));
  fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await screen.findByRole("button", { name: "Retry the same backup creation" });
  view.rerender(f.lifetimeView(false));
  view.rerender(f.lifetimeView());
  expect(screen.getByRole("tab", { name: "Deletion jobs" }).getAttribute("aria-selected")).toBe("true");
  expect(screen.getByRole("button", { name: "Retry the same backup creation" })).toBeTruthy();
  view.unmount();
  render(f.lifetimeView());
  await screen.findByRole("button", { name: `Inspect backup ${f.id}` });
  expect(screen.getByRole("tab", { name: "Creation jobs" }).getAttribute("aria-selected")).toBe("true");
  expect(screen.queryByRole("button", { name: "Retry the same backup creation" })).toBeNull();
  expect(screen.queryByRole("region", { name: "Backup integrity inspection" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await screen.findByRole("article", { name: `Backup ${f.creation.backupId} creation` });
  expect(f.create).toHaveBeenCalledTimes(2);
  expect(f.create.mock.calls[0]![0]).not.toEqual(f.create.mock.calls[1]![0]);
});

it("ignores a late accepted response from a disposed opening without replaying the operation", async () => {
  const f = fixture();
  let release!: (value: Awaited<ReturnType<typeof f.create>>) => void;
  f.create.mockImplementationOnce(() => new Promise(resolve => { release = resolve; }));
  const view = render(f.lifetimeView());
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await waitFor(() => expect(f.create).toHaveBeenCalledTimes(1));
  view.unmount();
  render(f.lifetimeView());
  await act(async () => release({ job: f.creation, requestId: newRequestId(), replayed: false }));
  await screen.findByText("Backup creation pending");
  expect(screen.queryByRole("region", { name: "Accepted backup operations" })).toBeNull();
  expect(screen.queryByText(`Backup creation accepted: ${f.creation.id}`)).toBeNull();
  expect(f.create).toHaveBeenCalledTimes(1);
});

it("keeps backup metadata across four pages and restores an evicted payload with its original token", async () => {
  const f = fixture();
  const backups = Array.from({ length: 4 }, (_, index) => ({ ...f.backup, id: newRequestId(), sizeBytes: BigInt(index + 1) }));
  f.creations.mockResolvedValue({ jobs: [] });
  f.list.mockImplementation(async input => {
    const index = input.pageToken ? Number(input.pageToken.slice(1)) : 0;
    return { backups: [backups[index]!], nextPageToken: index < 3 ? `p${index + 1}` : "" };
  });
  render(f.view());
  await screen.findByRole("button", { name: `Inspect backup ${backups[0]!.id}` });
  for (let index = 1; index < 4; index++) {
    fireEvent.click(screen.getByRole("button", { name: "Load more Database backups" }));
    await screen.findByRole("button", { name: `Inspect backup ${backups[index]!.id}` });
  }
  expect(screen.getByText("4 loaded")).toBeTruthy();
  expect(screen.queryByRole("button", { name: `Inspect backup ${backups[0]!.id}` })).toBeNull();
  expect(screen.getAllByRole("table", { name: "Database backups" })).toHaveLength(3);
  fireEvent.click(screen.getByRole("button", { name: "Restore previously loaded items" }));
  await screen.findByRole("button", { name: `Inspect backup ${backups[0]!.id}` });
  expect(f.list.mock.calls.map(([input]) => input.pageToken)).toEqual(["", "p1", "p2", "p3", ""]);
  expect(f.inspect).not.toHaveBeenCalled();
  expect(f.create).not.toHaveBeenCalled();
  expect(f.remove).not.toHaveBeenCalled();
});

it("pauses all backup inventory readers during original inspection without replacing the opener", async () => {
  const f = fixture(); render(f.view());
  const opener = await screen.findByRole("button", { name: `Inspect backup ${f.id}` });
  fireEvent.click(opener); await screen.findByText("Database integrity and original server identity verified.");
  const reads = [f.list.mock.calls.length, f.creations.mock.calls.length, f.deletions.mock.calls.length];
  await act(async () => { await f.client.invalidateQueries(); await new Promise(resolve => setTimeout(resolve, 20)); });
  expect([f.list.mock.calls.length, f.creations.mock.calls.length, f.deletions.mock.calls.length]).toEqual(reads);
  expect(opener.isConnected).toBe(true); expect(f.inspect).toHaveBeenCalledTimes(2);
  expect(f.inspect.mock.calls.every(([request]) => request.id === f.id)).toBe(true);
  expect(f.create).not.toHaveBeenCalled(); expect(f.remove).not.toHaveBeenCalled();
});


it.each(["inventory", "creation", "deletion"])("rejects duplicate IDs in a whole %s page and retries its exact token", async kind => {
  const f = fixture();
  const nextID = newRequestId();
  const token = `${kind}-duplicate-page`;
  const continuation = `${kind}-must-not-adopt`;
  if (kind === "inventory") f.list.mockResolvedValueOnce({ backups: [f.backup], nextPageToken: token }).mockResolvedValueOnce({ backups: [{ ...f.backup, id: nextID }, { ...f.backup, id: nextID }], nextPageToken: continuation }).mockResolvedValue({ backups: [{ ...f.backup, id: nextID }] });
  if (kind === "creation") f.creations.mockResolvedValueOnce({ jobs: [f.creation], nextPageToken: token }).mockResolvedValueOnce({ jobs: [{ ...f.creation, id: nextID, backupId: nextID }, { ...f.creation, id: nextID, backupId: nextID }], nextPageToken: continuation }).mockResolvedValue({ jobs: [{ ...f.creation, id: nextID, backupId: nextID }] });
  if (kind === "deletion") f.deletions.mockResolvedValueOnce({ jobs: [f.deletion], nextPageToken: token }).mockResolvedValueOnce({ jobs: [{ ...f.deletion, id: nextID, backupId: nextID }, { ...f.deletion, id: nextID, backupId: nextID }], nextPageToken: continuation }).mockResolvedValue({ jobs: [{ ...f.deletion, id: nextID, backupId: nextID }] });
  render(f.view());
  if (kind === "deletion") fireEvent.click(screen.getByRole("tab", { name: "Deletion jobs" }));
  const label = kind === "inventory" ? "Database backups" : kind === "creation" ? "Creation jobs" : "Deletion jobs";
  const owner = kind === "inventory" ? f.list : kind === "creation" ? f.creations : f.deletions;
  const original = f.backup.id;
  const result = kind === "inventory" ? screen : within(screen.getByRole("tabpanel", { name: label }));
  const idText = (id: string) => kind === "inventory" ? id : `Backup ${id}`;
  await result.findByText(idText(original));
  fireEvent.click(await screen.findByRole("button", { name: `Load more ${label}` }));
  const retry = await screen.findByRole("button", { name: "Retry" });
  expect(result.getByText(idText(original))).toBeTruthy();
  expect(result.queryByText(idText(nextID))).toBeNull();
  expect(screen.queryByRole("button", { name: `Load more ${label}` })).toBeNull();
  expect(owner.mock.calls.map(([request]) => request.pageToken)).toEqual(["", token]);
  fireEvent.click(retry);
  await result.findByText(idText(nextID));
  expect(result.getByText(idText(original))).toBeTruthy();
  expect(owner.mock.calls.map(([request]) => request.pageToken)).toEqual(["", token, token]);
  expect(f.inspect).not.toHaveBeenCalled(); expect(f.create).not.toHaveBeenCalled(); expect(f.remove).not.toHaveBeenCalled();
});
