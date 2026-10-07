// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
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
