// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { SubscriptionServiceIdentity, EntityKind, ResourceSchema } from "./gen/delidev/v1/delidev_pb.js";
import { subscriptionServiceFromWire, SubscriptionServiceId, configurationSchemaVersion, supportsResourceSchema } from "./configuration-identity.js";

function resource(kind: EntityKind, body: unknown, schemaVersion = 2) {
  return create(ResourceSchema, { kind, schemaVersion, documentJson: new TextEncoder().encode(JSON.stringify(body)) });
}
describe("service-native configuration schema negotiation", () => {
  it("accepts independently attributed subscription accounts and exact service harness models", () => {
    for (const [service, harness] of [["chatgpt", "codex"], ["claude", "claude-code"], ["grok", "grok-build"]]) {
      expect(supportsResourceSchema(resource(EntityKind.ACCOUNT, { type: "subscription", subscription_service: service }))).toBe(true);
      expect(supportsResourceSchema(resource(EntityKind.MODEL, { source_kind: "subscription", subscription_service: service, harnesses: [harness] }))).toBe(true);
      expect(supportsResourceSchema(resource(EntityKind.MODEL, { source_kind: "subscription", subscription_service: service, harnesses: [harness, "opencode"] }))).toBe(false);
    }
  });
  it("rejects mixed provider identity, unknown services and unrelated v2 families", () => {
    for (const body of [{ type: "subscription", subscription_service: "chatgpt", provider_id: "" }, { type: "subscription", subscription_service: "other" }, { type: "api", subscription_service: "chatgpt" }]) {
      expect(supportsResourceSchema(resource(EntityKind.ACCOUNT, body))).toBe(false);
    }
    expect(supportsResourceSchema(resource(EntityKind.SESSION, { subscription_service: "chatgpt" }))).toBe(false);
    expect(supportsResourceSchema(resource(EntityKind.MODEL, { source_kind: "subscription", subscription_service: "chatgpt", harnesses: ["claude-code"] }))).toBe(false);
    expect(supportsResourceSchema(resource(EntityKind.ACCOUNT, {}, 3))).toBe(false);
  });
  it("keeps retired original documents readable only in their owning wrapper", () => {
    const body = { retired: true, original_schema_version: 1, original_document: { provider_id: "historical" } };
    expect(supportsResourceSchema(resource(EntityKind.ACCOUNT, body))).toBe(true);
    expect(supportsResourceSchema(resource(EntityKind.AGENT, body))).toBe(false);
    expect(supportsResourceSchema(resource(EntityKind.PROVIDER, { ...body, original_schema_version: 2 }))).toBe(false);
    expect(supportsResourceSchema(resource(EntityKind.AGENT, { reconfiguration_required: true }))).toBe(true);
    expect(supportsResourceSchema(resource(EntityKind.AGENT, { reconfiguration_required: false }))).toBe(false);
  });
  it("preserves API v1 and selects v2 without granting native readiness", () => {
    expect(supportsResourceSchema(resource(EntityKind.ACCOUNT, { type: "api", provider_id: "provider" }, 1))).toBe(true);
    expect(configurationSchemaVersion(EntityKind.ACCOUNT, { type: "subscription" })).toBe(2);
    expect(configurationSchemaVersion(EntityKind.MODEL, { source_kind: "subscription" })).toBe(2);
    expect(configurationSchemaVersion(EntityKind.AGENT, { reconfiguration_required: true })).toBe(2);
    expect(configurationSchemaVersion(EntityKind.ACCOUNT, { type: "api" })).toBe(1);
    expect(supportsResourceSchema(create(ResourceSchema, { kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: Uint8Array.of(255) }))).toBe(false);
  });
});

it("maps only closed generated service enums without inferring an API provider", () => {
  expect(subscriptionServiceFromWire(SubscriptionServiceIdentity.CHATGPT)).toBe(SubscriptionServiceId.ChatGPT);
  expect(subscriptionServiceFromWire(SubscriptionServiceIdentity.CLAUDE)).toBe(SubscriptionServiceId.Claude);
  expect(subscriptionServiceFromWire(SubscriptionServiceIdentity.GROK)).toBe(SubscriptionServiceId.Grok);
  expect(subscriptionServiceFromWire(SubscriptionServiceIdentity.UNSPECIFIED)).toBeUndefined();
  expect(subscriptionServiceFromWire(99 as SubscriptionServiceIdentity)).toBeUndefined();
});

it("accepts only the ordered Agent schema-3 family and preserves legacy versions", () => {
  const body = { routes: [{ model_id: "model", accounts: [{ id: "account", weight: 1 }] }] };
  expect(supportsResourceSchema(resource(EntityKind.AGENT, body, 3))).toBe(true);
  expect(configurationSchemaVersion(EntityKind.AGENT, body)).toBe(3);
  expect(supportsResourceSchema(resource(EntityKind.AGENT, { ...body, reconfiguration_required: true }, 3))).toBe(true);
  for (const invalid of [{ routes: [] }, { ...body, model_id: "other" }, { ...body, accounts: [{ id: "other" }] }, { ...body, routing: "priority" }]) {
    expect(supportsResourceSchema(resource(EntityKind.AGENT, invalid, 3))).toBe(false);
  }
  expect(supportsResourceSchema(resource(EntityKind.MODEL, body, 3))).toBe(false);
  expect(supportsResourceSchema(resource(EntityKind.AGENT, body, 4))).toBe(false);
});

 it("preserves the project behavior schema family without accepting destructive legacy projections", () => {
 expect(supportsResourceSchema(resource(EntityKind.PROJECT, { settings: {} }, 2))).toBe(true);
 expect(supportsResourceSchema(resource(EntityKind.PROJECT, { settings: {} }, 1))).toBe(false);
 expect(supportsResourceSchema(resource(EntityKind.SETTINGS, { automatic_plan_approval: false }, 2))).toBe(true);
 expect(supportsResourceSchema(resource(EntityKind.SETTINGS, { automatic_plan_approval: null }, 2))).toBe(false);
 });
