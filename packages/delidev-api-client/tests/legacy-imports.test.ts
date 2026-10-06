import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { createRegistry, createFileRegistry } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { GetStatusRequestSchema, ResourceSchema, SystemCapability, SystemService, SessionService, SwitchSessionAccountRequestSchema, file_delidev_v1_delidev } from "../src/gen/delidev/v1/delidev_pb.js";
import { switchSessionAccount } from "../src/gen/delidev/v1/delidev-SessionService_connectquery.js";
import { SessionQuery } from "../src/index.js";
import { getStatus } from "../src/gen/delidev/v1/delidev-SystemService_connectquery.js";
import { ResourceSchema as CurrentResourceSchema } from "../src/gen/delidev/v1/common_pb.js";
import { SystemService as CurrentSystemService } from "../src/gen/delidev/v1/system_pb.js";
import { RequestSubscriptionRequestSchema, SubscriptionService } from "../src/gen/delidev/v1/subscription_pb.js";
import { RequestDiagnosticSchema, ListRequestDiagnosticsRequestSchema, RequestDiagnosticStateSchema } from "../src/gen/delidev/v1/session_pb.js";
import { NativeAccountingSummarySchema, NativeAccountingPricingSchema, UsageAccountingProfileSchema } from "../src/gen/delidev/v1/usage_pb.js";
import { WorkspaceStorageService } from "../src/gen/delidev/v1/workspace_storage_pb.js";
import { NetworkService } from "../src/gen/delidev/v1/network_pb.js";

// Fixed snapshot of the last intact historical map; never derive expectations
// from the editable generator input, which can regress along with the facades.
const historicalOrder = JSON.parse(readFileSync(new URL("../../../protos/delidev/v1/contracttest/testdata/legacy-declaration-order.json", import.meta.url), "utf8")) as {
  sourceRevision: string;
  relocationSha256: string;
  message: string[];
  enum: string[];
  service: string[];
};

describe("historical generated imports", () => {
  it("retains declaration identity, wire names and query methods", () => {
    expect(ResourceSchema).toBe(CurrentResourceSchema);
    expect(SystemService).toBe(CurrentSystemService);
    expect(GetStatusRequestSchema.typeName).toBe("delidev.v1.GetStatusRequest");
    expect(getStatus).toBe(SystemService.method.getStatus);
    expect(SystemCapability.AUTOMATIC_TITLES_V1).toBe(1);
    expect(SystemCapability.SESSION_FORWARDING_V1).toBe(2);
    expect(SystemCapability.USER_SERVICES_V1).toBe(3);
    expect(SystemCapability.PERMANENT_SESSION_DELETION_V1).toBe(9);
    expect(SystemCapability.STOPPED_CODEX_ACCOUNT_SWITCH_V1).toBe(5);
    expect(SwitchSessionAccountRequestSchema.typeName).toBe("delidev.v1.SwitchSessionAccountRequest");
    expect(switchSessionAccount).toBe(SessionService.method.switchSessionAccount);
    expect(SessionQuery.switchSessionAccount).toBe(switchSessionAccount);
    expect(SessionQuery.deleteSession).toBe(SessionService.method.deleteSession);
    expect(SessionQuery.getSessionDeletion).toBe(SessionService.method.getSessionDeletion);
  });
});

it("includes additive diagnostics and accounting declarations in both aggregate registry forms", () => {
  const registry = createRegistry(file_delidev_v1_delidev);
  const reconstructed = createFileRegistry(file_delidev_v1_delidev.proto, name => file_delidev_v1_delidev.dependencies.find(file => file.proto.name === name));
  for (const schema of [RequestDiagnosticSchema, ListRequestDiagnosticsRequestSchema, NativeAccountingSummarySchema, NativeAccountingPricingSchema]) {
    expect(file_delidev_v1_delidev.messages.find(value => value.typeName === schema.typeName)).toBe(schema);
    expect(registry.getMessage(schema.typeName)).toBe(schema);
    expect(reconstructed.getMessage(schema.typeName)?.proto).toEqual(schema.proto);
  }
  for (const schema of [RequestDiagnosticStateSchema, UsageAccountingProfileSchema]) {
    expect(file_delidev_v1_delidev.enums.find(value => value.typeName === schema.typeName)).toBe(schema);
    expect(registry.getEnum(schema.typeName)).toBe(schema);
    expect(reconstructed.getEnum(schema.typeName)?.proto).toEqual(schema.proto);
  }
  expect(reconstructed.getService("delidev.v1.SessionService")?.method.listRequestDiagnostics.output.typeName).toBe("delidev.v1.ListRequestDiagnosticsResponse");
});

it("retains the legacy aggregate descriptor for reflection registries", () => {
  const registry = createRegistry(file_delidev_v1_delidev);
  expect(registry.getMessage("delidev.v1.Resource")).toBe(ResourceSchema);
  expect(registry.getService("delidev.v1.SystemService")).toBe(SystemService);
  expect(registry.getMessage("delidev.v1.RequestSubscriptionRequest")).toBe(RequestSubscriptionRequestSchema);
  expect(registry.getService("delidev.v1.SubscriptionService")).toBe(SubscriptionService);
  const reconstructed = createFileRegistry(file_delidev_v1_delidev.proto, name => file_delidev_v1_delidev.dependencies.find(file => file.proto.name === name));
  expect(reconstructed.getMessage("delidev.v1.Resource")?.field.id.number).toBe(1);
  expect(reconstructed.getService("delidev.v1.SystemService")?.method.getStatus.output.typeName).toBe("delidev.v1.GetStatusResponse");
  expect(reconstructed.getService("delidev.v1.SubscriptionService")?.method.getSubscriptionProgress.output.typeName).toBe("delidev.v1.GetSubscriptionProgressResponse");
  for (const service of [WorkspaceStorageService, NetworkService, SubscriptionService]) {
    expect(file_delidev_v1_delidev.services.indexOf(service)).toBeGreaterThanOrEqual(19);
    expect(registry.getService(service.typeName)).toBe(service);
    expect(reconstructed.getService(service.typeName)?.proto).toEqual(service.proto);
  }
});

it("preserves the complete fixed historical declaration prefix", () => {
  expect(historicalOrder.sourceRevision).toBe("54187b780d48e94a49763e87fc140f449869d9f4");
  expect([historicalOrder.message.length, historicalOrder.enum.length, historicalOrder.service.length]).toEqual([303, 36, 19]);
  for (const [kind, actual] of [["message", file_delidev_v1_delidev.messages], ["enum", file_delidev_v1_delidev.enums], ["service", file_delidev_v1_delidev.services]] as const) {
    const expected = historicalOrder[kind];
    // New service-owned declarations append after the historical order without
    // requiring this compatibility test or the relocation manifest to change.
    expect(actual.slice(0, expected.length).map(value => value.name)).toEqual(expected);
    expect(new Set(actual.map(value => value.name)).size).toBe(actual.length);
  }
});

it("retains historical relocation kinds, files and full map order", () => {
  const layout = JSON.parse(readFileSync(new URL("../../../scripts/delidev/proto-layout.json", import.meta.url), "utf8")) as {
    declarations: Record<string, { kind: string; file: string }>;
  };
  // Pin the complete map as well as per-kind aggregate positions. Moving an
  // entry across another kind can leave reflection order unchanged.
  const entries = Object.entries(layout.declarations).map(([name, { kind, file }]) => [name, kind, file]);
  expect(createHash("sha256").update(JSON.stringify(entries)).digest("hex")).toBe(historicalOrder.relocationSha256);
});

it.each([
  ["message", 272, "GetPullRequestFixCapabilitiesRequest"],
  ["message", 276, "CreateTerminalRequest"],
  ["message", 280, "WatchTerminalOutputRequest"],
  ["enum", 32, "PullRequestFixProfile"],
  ["enum", 33, "TerminalAction"],
  ["service", 16, "PullRequestFixService"],
  ["service", 17, "TerminalService"],
  ["service", 18, "BrowserService"],
] as const)("retains %s[%i] as %s", (kind, index, name) => {
  const declarations = { message: file_delidev_v1_delidev.messages, enum: file_delidev_v1_delidev.enums, service: file_delidev_v1_delidev.services };
  expect(declarations[kind][index]?.name).toBe(name);
});
