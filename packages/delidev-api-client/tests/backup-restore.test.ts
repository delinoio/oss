import { create, createRegistry, fromBinary, toBinary } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { BackupRestoreState, RestoreBackupRequestSchema, SystemCapability, SystemQuery, SystemService } from "../src/index.js";
import { file_delidev_v1_delidev } from "../src/gen/delidev/v1/delidev_pb.js";
import { RestoreBackupRequestSchema as CurrentRestoreRequest } from "../src/gen/delidev/v1/system_pb.js";

it("retains restore queries and canonical schema identity through legacy imports", () => {
  expect(SystemCapability.MANAGED_BACKUP_RESTORE_V1).toBe(7);
  expect(RestoreBackupRequestSchema).toBe(CurrentRestoreRequest);
  expect(SystemQuery.restoreBackup).toBe(SystemService.method.restoreBackup);
  expect(SystemQuery.getBackupRestore).toBe(SystemService.method.getBackupRestore);
  expect(createRegistry(file_delidev_v1_delidev).getMessage("delidev.v1.RestoreBackupRequest")).toBe(CurrentRestoreRequest);
  expect(BackupRestoreState.PUBLISHED).not.toBe(BackupRestoreState.RESTORED);
});

it("keeps omitted live revision distinct from explicit zero and preserves exact counts", () => {
  const absent = fromBinary(CurrentRestoreRequest, toBinary(CurrentRestoreRequest, create(CurrentRestoreRequest)));
  expect(absent.expectedRestoreRevision).toBeUndefined();
  const original = create(CurrentRestoreRequest, {
    requestId: "0199a059-73a0-7000-8000-000000000001",
    expectedRestoreRevision: 0n,
    backup: { id: "0199a059-73a0-7000-8000-000000000002", revision: 1n, sizeBytes: 9007199254740993n },
    confirm: true,
  });
  const restored = fromBinary(CurrentRestoreRequest, toBinary(CurrentRestoreRequest, original));
  expect(restored.expectedRestoreRevision).toBe(0n);
  expect(restored.backup?.sizeBytes).toBe(9007199254740993n);
  expect(restored.requestId).toBe(original.requestId);
});
