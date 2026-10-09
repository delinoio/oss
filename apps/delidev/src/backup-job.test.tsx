// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BackupCreationJobSchema, BackupCreationState, BackupDeletionJobSchema, BackupDeletionState, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { BackupJob, BackupJobKind } from "./backup-job";

it("distinguishes creation and deletion tracking for the same backup", async () => {
 const backupId = newRequestId();
 const creation = create(BackupCreationJobSchema, { id: newRequestId(), backupId, revision: 1n, state: BackupCreationState.SUCCEEDED });
 const deletion = create(BackupDeletionJobSchema, { id: newRequestId(), backupId, revision: 1n, state: BackupDeletionState.SUCCEEDED });
 const dismissCreation = vi.fn(), dismissDeletion = vi.fn();
 const transport = createRouterTransport(router => router.service(SystemService, { getBackupCreation: () => ({job: creation}), getBackupDeletion: () => ({job: deletion}) }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><BackupJob kind={BackupJobKind.Creation} accepted={creation} active completed={vi.fn()} dismiss={dismissCreation} /><BackupJob kind={BackupJobKind.Deletion} accepted={deletion} active completed={vi.fn()} dismiss={dismissDeletion} /></QueryClientProvider></TransportProvider>);
 const createArticle = screen.getByRole("article", { name: `Backup ${backupId} creation` });
 const deleteArticle = screen.getByRole("article", { name: `Backup ${backupId} deletion` });
 fireEvent.click(await within(createArticle).findByRole("button", { name: `Dismiss backup ${backupId} creation tracking` }));
 expect(dismissCreation).toHaveBeenCalledTimes(1); expect(dismissDeletion).not.toHaveBeenCalled();
 fireEvent.click(await within(deleteArticle).findByRole("button", { name: `Dismiss backup ${backupId} deletion tracking` }));
 expect(dismissDeletion).toHaveBeenCalledTimes(1);
 expect(screen.queryByText(creation.id)).toBeNull(); expect(screen.queryByText(deletion.id)).toBeNull();
});


it.each([BackupJobKind.Creation, BackupJobKind.Deletion])("rechecks only the original %s job and fences rechecks after disposal", async kind => {
 const backupId = newRequestId(), id = newRequestId();
 const creation = create(BackupCreationJobSchema, { id, backupId, revision: 1n, state: BackupCreationState.FAILED, problemCode: "permission_denied" });
 const deletion = create(BackupDeletionJobSchema, { id, backupId, revision: 1n, state: BackupDeletionState.PENDING, problemCode: "permission_denied" });
 const read = vi.fn((_request: { id: string }) => {});
 const write = vi.fn();
 const transport = createRouterTransport(router => router.service(SystemService, { getBackupCreation: request => { read(request); return { job: creation }; }, getBackupDeletion: request => { read(request); return { job: deletion }; }, createBackup: write, deleteBackup: write }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const completed = vi.fn(), dismiss = vi.fn();
 const task = (active: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}>{kind === BackupJobKind.Creation ? <BackupJob kind={kind} accepted={creation} active={active} completed={completed} dismiss={dismiss} /> : <BackupJob kind={kind} accepted={deletion} active={active} completed={completed} dismiss={dismiss} />}</QueryClientProvider></TransportProvider>;
 const view = render(task(true));
 const button = await screen.findByRole("button", { name: "Recheck original backup job" });
 expect(screen.getByText(kind === BackupJobKind.Creation ? /cannot be replayed by observation/ : /Do not submit a replacement deletion/)).toBeTruthy();
 expect(screen.getByRole("alert").textContent).toContain("permission");
 fireEvent.click(button);
 await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
 expect(read.mock.calls.every(([request]) => (request as { id: string }).id === id)).toBe(true);
 expect(write).not.toHaveBeenCalled(); expect(completed).not.toHaveBeenCalled();
 view.rerender(task(false)); expect((button as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(button); expect(read).toHaveBeenCalledTimes(2);
});

it("shows a failed original read without treating retained success as current completion", async () => {
 const accepted = create(BackupCreationJobSchema, { id: newRequestId(), backupId: newRequestId(), revision: 1n, state: BackupCreationState.PENDING });
 const read = vi.fn().mockRejectedValueOnce(new ConnectError("fixture", Code.Unavailable)).mockResolvedValue({ job: { ...accepted, state: BackupCreationState.SUCCEEDED } });
 const completed = vi.fn();
 const transport = createRouterTransport(router => router.service(SystemService, { getBackupCreation: read }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><BackupJob kind={BackupJobKind.Creation} accepted={accepted} active completed={completed} dismiss={vi.fn()} /></QueryClientProvider></TransportProvider>);
 expect(await screen.findByText(/observes the same job and does not repeat/)).toBeTruthy();
 expect(completed).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Retry original status read" }));
 await waitFor(() => expect(completed).toHaveBeenCalledTimes(1));
 expect(read.mock.calls.every(([request]) => request.id === accepted.id)).toBe(true);
});
