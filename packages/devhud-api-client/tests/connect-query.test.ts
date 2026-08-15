import { create } from "@bufbuild/protobuf";
import { createClient, createRouterTransport } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";

import * as clientPackage from "../src/index.js";
import {
  deleteAccount,
  getAccount,
  restoreAccount,
} from "../src/gen/devhud/v1/account-AccountService_connectquery.js";
import {
  deleteUpload as adminDeleteUpload,
  getUserUsage,
  listAuditEvents,
  listUploads as adminListUploads,
  listUsers,
  quarantineUpload,
  setUserBlocked,
} from "../src/gen/devhud/v1/admin-AdminService_connectquery.js";
import { AdminService } from "../src/gen/devhud/v1/admin_pb.js";
import { getBootstrap } from "../src/gen/devhud/v1/bootstrap-BootstrapService_connectquery.js";
import {
  BootstrapService,
  GetBootstrapResponseSchema,
} from "../src/gen/devhud/v1/bootstrap_pb.js";
import { submitCrashReport } from "../src/gen/devhud/v1/diagnostics-DiagnosticsService_connectquery.js";
import {
  getSettings,
  replaceSettings,
} from "../src/gen/devhud/v1/settings-SettingsService_connectquery.js";
import {
  createUpload,
  deleteUpload,
  finalizeUpload,
  listUploads,
} from "../src/gen/devhud/v1/upload-UploadService_connectquery.js";

describe("generated Connect Query exports", () => {
  it("exports every service method descriptor", () => {
    expect(getBootstrap).toBe(BootstrapService.method.getBootstrap);
    expect(getSettings.name).toBe("GetSettings");
    expect(replaceSettings.name).toBe("ReplaceSettings");
    expect(createUpload.name).toBe("CreateUpload");
    expect(finalizeUpload.name).toBe("FinalizeUpload");
    expect(listUploads.name).toBe("ListUploads");
    expect(deleteUpload.name).toBe("DeleteUpload");
    expect(getAccount.name).toBe("GetAccount");
    expect(deleteAccount.name).toBe("DeleteAccount");
    expect(restoreAccount.name).toBe("RestoreAccount");
    expect(submitCrashReport.name).toBe("SubmitCrashReport");

    expect(listUsers).toBe(AdminService.method.listUsers);
    expect(setUserBlocked).toBe(AdminService.method.setUserBlocked);
    expect(getUserUsage).toBe(AdminService.method.getUserUsage);
    expect(adminListUploads).toBe(AdminService.method.listUploads);
    expect(quarantineUpload).toBe(AdminService.method.quarantineUpload);
    expect(adminDeleteUpload).toBe(AdminService.method.deleteUpload);
    expect(listAuditEvents).toBe(AdminService.method.listAuditEvents);
  });

  it("is callable through an in-memory Connect transport", async () => {
    const transport = createRouterTransport((router) => {
      router.service(BootstrapService, {
        getBootstrap() {
          return create(GetBootstrapResponseSchema, {
            schemaVersion: 1,
            protocolVersion: "devhud.v1",
            apiVersion: "test",
          });
        },
      });
    });
    const client = createClient(BootstrapService, transport);

    await expect(client.getBootstrap({})).resolves.toMatchObject({
      schemaVersion: 1,
      protocolVersion: "devhud.v1",
    });
  });

  it("provides collision-free root query namespaces", () => {
    expect(clientPackage.BootstrapQueries.getBootstrap).toBe(getBootstrap);
    expect(clientPackage.UploadQueries.listUploads).toBe(listUploads);
    expect(clientPackage.AdminQueries.listUploads).toBe(adminListUploads);
  });
});
