import type { DescMessage, DescService } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import * as AccountQuery from "../src/gen/devhud/v1/account-AccountService_connectquery.js";
import { AccountService, file_devhud_v1_account } from "../src/gen/devhud/v1/account_pb.js";
import * as AdminQuery from "../src/gen/devhud/v1/admin-AdminService_connectquery.js";
import { AdminService, file_devhud_v1_admin } from "../src/gen/devhud/v1/admin_pb.js";
import * as BootstrapQuery from "../src/gen/devhud/v1/bootstrap-BootstrapService_connectquery.js";
import { BootstrapService, file_devhud_v1_bootstrap } from "../src/gen/devhud/v1/bootstrap_pb.js";
import { file_devhud_v1_common } from "../src/gen/devhud/v1/common_pb.js";
import * as DiagnosticsQuery from "../src/gen/devhud/v1/diagnostics-DiagnosticsService_connectquery.js";
import { DiagnosticsService, file_devhud_v1_diagnostics } from "../src/gen/devhud/v1/diagnostics_pb.js";
import * as SettingsQuery from "../src/gen/devhud/v1/settings-SettingsService_connectquery.js";
import { SettingsService, file_devhud_v1_settings } from "../src/gen/devhud/v1/settings_pb.js";
import * as UploadQuery from "../src/gen/devhud/v1/upload-UploadService_connectquery.js";
import { UploadService, file_devhud_v1_upload } from "../src/gen/devhud/v1/upload_pb.js";

const services: ReadonlyArray<[DescService, ReadonlyArray<string>]> = [
  [BootstrapService, ["GetBootstrap"]],
  [SettingsService, ["GetSettings", "ReplaceSettings"]],
  [UploadService, ["CreateUpload", "FinalizeUpload", "ListUploads", "DeleteUpload"]],
  [AccountService, ["GetAccount", "DeleteAccount", "RestoreAccount"]],
  [DiagnosticsService, ["SubmitCrashReport"]],
  [
    AdminService,
    [
      "ListUsers",
      "SetUserBlocked",
      "GetUserUsage",
      "ListUploads",
      "QuarantineUpload",
      "DeleteUpload",
      "ListAuditEvents",
    ],
  ],
];

describe("devhud.v1 service contract", () => {
  it("exposes the exact contracted unary RPC inventory", () => {
    expect(services.flatMap(([service]) => service.methods)).toHaveLength(18);
    for (const [service, expected] of services) {
      expect(service.methods.map((method) => method.name)).toEqual(expected);
      expect(service.methods.every((method) => method.methodKind === "unary")).toBe(true);
    }
  });

  it("puts response correlation metadata on every successful response", () => {
    for (const [service] of services) {
      for (const method of service.methods) {
        expect(method.output.field.metadata?.message?.typeName).toBe("devhud.v1.ResponseMetadata");
      }
    }
  });

  it("generates a Connect Query export for every RPC", () => {
    expect(Object.keys(BootstrapQuery)).toEqual(["getBootstrap"]);
    expect(Object.keys(SettingsQuery)).toEqual(["getSettings", "replaceSettings"]);
    expect(Object.keys(UploadQuery)).toEqual([
      "createUpload",
      "finalizeUpload",
      "listUploads",
      "deleteUpload",
    ]);
    expect(Object.keys(AccountQuery)).toEqual(["getAccount", "deleteAccount", "restoreAccount"]);
    expect(Object.keys(DiagnosticsQuery)).toEqual(["submitCrashReport"]);
    expect(Object.keys(AdminQuery)).toEqual([
      "listUsers",
      "setUserBlocked",
      "getUserUsage",
      "listUploads",
      "quarantineUpload",
      "deleteUpload",
      "listAuditEvents",
    ]);
  });

  it("keeps forbidden content out of the wire model and settings out of admin graphs", () => {
    const files = [
      file_devhud_v1_common,
      file_devhud_v1_bootstrap,
      file_devhud_v1_settings,
      file_devhud_v1_upload,
      file_devhud_v1_account,
      file_devhud_v1_diagnostics,
      file_devhud_v1_admin,
    ];
    const forbiddenFields = new Set([
      "access_key",
      "agent_output",
      "authorization",
      "cookie",
      "deck_result",
      "dom",
      "html",
      "issue_body",
      "local_path",
      "password",
      "pat",
      "r2_secret",
      "screenshot",
      "secret_key",
    ]);

    for (const file of files) {
      for (const message of file.messages) {
        for (const field of message.fields) {
          expect(forbiddenFields.has(field.name), `${message.typeName}.${field.name}`).toBe(false);
        }
      }
    }

    const reachable = collectReachableAdminMessages(AdminService);
    expect([...reachable].some((message) => message.typeName === "devhud.v1.SettingsSnapshot")).toBe(false);
    expect(
      [...reachable].some(
        (message) =>
          message.typeName === "devhud.v1.Upload" ||
          message.typeName === "devhud.v1.UploadReservation",
      ),
    ).toBe(false);
    expect([...reachable].some((message) => message.field.canonicalJson !== undefined)).toBe(false);
    expect(
      [...reachable].some((message) =>
        message.fields.some(
          (field) => field.name === "public_url" || field.name === "signed_put_url",
        ),
      ),
    ).toBe(false);
  });
});

function collectReachableAdminMessages(service: DescService): Set<DescMessage> {
  const seen = new Set<DescMessage>();
  const pending = service.methods.flatMap((method) => [method.input, method.output]);

  while (pending.length > 0) {
    const message = pending.pop();
    if (message === undefined || seen.has(message)) {
      continue;
    }
    seen.add(message);
    for (const field of message.fields) {
      if (field.fieldKind === "message") {
        pending.push(field.message);
      } else if (field.fieldKind === "list" && field.listKind === "message") {
        pending.push(field.message);
      } else if (field.fieldKind === "map" && field.mapKind === "message") {
        pending.push(field.message);
      }
    }
  }
  return seen;
}
