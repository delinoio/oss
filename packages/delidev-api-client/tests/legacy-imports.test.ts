import { readFileSync } from "node:fs";
import { createRegistry, createFileRegistry } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { GetStatusRequestSchema, ResourceSchema, SystemCapability, SystemService, file_delidev_v1_delidev } from "../src/gen/delidev/v1/delidev_pb.js";
import { getStatus } from "../src/gen/delidev/v1/delidev-SystemService_connectquery.js";
import { ResourceSchema as CurrentResourceSchema } from "../src/gen/delidev/v1/common_pb.js";
import { SystemService as CurrentSystemService } from "../src/gen/delidev/v1/system_pb.js";
import { RequestSubscriptionRequestSchema, SubscriptionService } from "../src/gen/delidev/v1/subscription_pb.js";

describe("historical generated imports", () => {
  it("retains declaration identity, wire names and query methods", () => {
    expect(ResourceSchema).toBe(CurrentResourceSchema);
    expect(SystemService).toBe(CurrentSystemService);
    expect(GetStatusRequestSchema.typeName).toBe("delidev.v1.GetStatusRequest");
    expect(getStatus).toBe(SystemService.method.getStatus);
    expect(SystemCapability.AUTOMATIC_TITLES_V1).toBe(1);
    expect(SystemCapability.SESSION_FORWARDING_V1).toBe(2);
    expect(SystemCapability.USER_SERVICES_V1).toBe(3);
  });
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
  const layout = JSON.parse(readFileSync(new URL("../../../scripts/delidev/proto-layout.json", import.meta.url), "utf8"));
  for (const [kind, actual] of [["message", file_delidev_v1_delidev.messages], ["enum", file_delidev_v1_delidev.enums], ["service", file_delidev_v1_delidev.services]] as const) {
    const expected = Object.entries(layout.declarations).filter(([, value]) => (value as { kind: string }).kind === kind).map(([name]) => name);
    // New service-owned declarations append after the historical order without
    // requiring this compatibility test or the relocation manifest to change.
    expect(actual.slice(0, expected.length).map(value => value.name)).toEqual(expected);
    expect(new Set(actual.map(value => value.name)).size).toBe(actual.length);
  }
});
