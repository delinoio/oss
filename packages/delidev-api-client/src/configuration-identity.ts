// SPDX-License-Identifier: Apache-2.0
import { EntityKind, SubscriptionServiceIdentity, type Resource } from "./gen/delidev/v1/common_pb.js";
import { isEntityId } from "./validation.js";
import { apiFormat, apiFormatProfile } from "./api-formats.js";

export enum SubscriptionServiceId { ChatGPT = "chatgpt", Claude = "claude", Grok = "grok", OpenCodeGo = "opencode_go" }
export enum NativeModelSourceKind { Subscription = "subscription" }
export const subscriptionServiceNames: Readonly<Record<SubscriptionServiceId, string>> = {
  [SubscriptionServiceId.ChatGPT]: "ChatGPT", [SubscriptionServiceId.Claude]: "Claude", [SubscriptionServiceId.Grok]: "Grok", [SubscriptionServiceId.OpenCodeGo]: "OpenCode Go",
};
export const subscriptionServiceHarnesses: Readonly<Record<SubscriptionServiceId, string>> = {
  [SubscriptionServiceId.ChatGPT]: "codex", [SubscriptionServiceId.Claude]: "claude-code", [SubscriptionServiceId.Grok]: "grok-build", [SubscriptionServiceId.OpenCodeGo]: "opencode",
};
export function subscriptionService(value: unknown): SubscriptionServiceId | undefined {
  return Object.values(SubscriptionServiceId).find((service) => service === value);
}
function object(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value); }

export type ResourceDocument = Record<string, unknown>;

enum LargeJobType { Compaction = "compact-session", WorkspaceStorage = "workspace-storage" }
enum StorageDocumentAction { Recover = "recover" }

// Only these original typed jobs retain duplicated bounded evidence. Remove
// this exception when their owning contracts store that evidence by reference.
function largeJob(value: ResourceDocument): boolean {
  if (!object(value.input)) return false;
  return value.type === LargeJobType.Compaction ||
    value.type === LargeJobType.WorkspaceStorage && value.input.action === StorageDocumentAction.Recover;
}

export function decodeResourceDocument(resource: Resource): ResourceDocument | undefined {
  const size = resource.documentJson.byteLength;
  const typedJob = resource.kind === EntityKind.JOB && resource.schemaVersion === 1;
  if (size > (typedJob ? 4 << 20 : 1 << 20) || ![1, 2, 3, 4].includes(resource.schemaVersion)) return;
  try {
    const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson));
    if (!object(value) || size > 1 << 20 && !largeJob(value)) return;
    return supportsDocumentSchema(resource, value) ? value : undefined;
  } catch { return; }
}

export function supportsResourceSchema(resource: Resource): boolean {
  return decodeResourceDocument(resource) !== undefined;
}

// Version 2 is accepted only for its owning identity family. This does not grant
// mutation, native readiness or interpretation of a retired original document.
function supportsDocumentSchema(resource: Resource, value: ResourceDocument): boolean {
  if (resource.kind === EntityKind.MODEL) return false;
  if (resource.kind === EntityKind.AGENT) return resource.schemaVersion === 4 && Array.isArray(value.routes) && value.routes.length > 0 && value.routes.length <= 1000 && !Object.hasOwn(value, "model_id") && !Object.hasOwn(value, "accounts") && !Object.hasOwn(value, "routing") && value.routes.every(route => {
    if (!object(route) || Object.hasOwn(route,"model_id") || !object(route.model) || !Array.isArray(route.accounts) || route.accounts.length < 1) return false;
    const model=route.model;
    return typeof model.native_id === "string" && model.native_id.length > 0 && new TextEncoder().encode(model.native_id).length <= 256 && (!Object.hasOwn(model,"subscription_service") && typeof model.provider_id === "string" && isEntityId(model.provider_id) || !Object.hasOwn(model,"provider_id") && Boolean(subscriptionService(model.subscription_service)));
  });
  if (resource.schemaVersion === 4) return false;

  if (resource.schemaVersion === 1) return !(resource.kind === EntityKind.PROJECT && Object.hasOwn(value, "settings") || resource.kind === EntityKind.SETTINGS && (Object.hasOwn(value, "automatic_plan_approval") || Object.hasOwn(value, "plan_mode_default") || Object.hasOwn(value, "branch_prefix")) || resource.kind === EntityKind.ACCOUNT && Object.hasOwn(value, "api_protocol") || resource.kind === EntityKind.PROVIDER && Object.hasOwn(value, "api_formats"));
  if (resource.schemaVersion === 2 && [EntityKind.PROJECT, EntityKind.SETTINGS].includes(resource.kind) && configurationSchemaVersion(resource.kind, value) === 3) return false;
 if (resource.schemaVersion === 2 && [EntityKind.PROJECT, EntityKind.SETTINGS].includes(resource.kind)) return resource.kind === EntityKind.PROJECT ? object(value.settings) : typeof value.automatic_plan_approval === "boolean";
  if (resource.schemaVersion === 3) {
 if (resource.kind === EntityKind.PROJECT) return object(value.settings) && ["inherit", "enabled", "disabled"].includes(value.settings.plan_mode_default as string) && (!Object.hasOwn(value.settings, "branch_prefix") || typeof value.settings.branch_prefix === "string");
 if (resource.kind === EntityKind.SETTINGS) return typeof value.plan_mode_default === "boolean" && typeof value.branch_prefix === "string" && typeof value.automatic_plan_approval === "boolean";
    if (resource.kind === EntityKind.ACCOUNT) return value.type === "api" && Boolean(apiFormat(value.api_protocol)) && typeof value.provider_id === "string" && !Object.hasOwn(value, "subscription_service") && !Object.hasOwn(value, "subscription");
    if (resource.kind === EntityKind.PROVIDER) return Array.isArray(value.api_formats) && value.api_formats.length > 0 && value.api_formats.length <= 3 && value.api_formats.every(profile => Boolean(apiFormatProfile(profile))) && new Set(value.api_formats.map(profile => (profile as Record<string, unknown>).protocol)).size === value.api_formats.length;
    return false;
  }
  if ([EntityKind.ACCOUNT, EntityKind.PROVIDER].includes(resource.kind) && value.retired === true) {
    return value.original_schema_version === 1 && object(value.original_document);
  }

  const service = subscriptionService(value.subscription_service);
  if (!service || Object.hasOwn(value, "provider_id")) return false;
  if (resource.kind === EntityKind.ACCOUNT) return value.type === "subscription";
  return false;
}
export function configurationSchemaVersion(kind: EntityKind, value: Record<string, unknown>): number {
 if (kind === EntityKind.SETTINGS && (Object.hasOwn(value, "plan_mode_default") || Object.hasOwn(value, "branch_prefix")) || kind === EntityKind.PROJECT && object(value.settings) && (Object.hasOwn(value.settings, "plan_mode_default") || Object.hasOwn(value.settings, "branch_prefix"))) return 3;
  if (kind === EntityKind.PROJECT && Object.hasOwn(value, "settings") || kind === EntityKind.SETTINGS && Object.hasOwn(value, "automatic_plan_approval")) return 2;
  if (kind === EntityKind.ACCOUNT && value.type === "api" && apiFormat(value.api_protocol) || kind === EntityKind.PROVIDER && Array.isArray(value.api_formats) && value.api_formats.length > 0) return 3;
  if (kind === EntityKind.AGENT) return 4;
  return kind === EntityKind.ACCOUNT && value.type === "subscription" ||
    kind === EntityKind.MODEL && value.source_kind === NativeModelSourceKind.Subscription ? 2 : 1;
}

export function subscriptionServiceFromWire(value: SubscriptionServiceIdentity): SubscriptionServiceId | undefined {
  switch (value) {
    case SubscriptionServiceIdentity.CHATGPT: return SubscriptionServiceId.ChatGPT;
    case SubscriptionServiceIdentity.CLAUDE: return SubscriptionServiceId.Claude;
    case SubscriptionServiceIdentity.GROK: return SubscriptionServiceId.Grok;
    case SubscriptionServiceIdentity.OPENCODE_GO: return SubscriptionServiceId.OpenCodeGo;
    default: return undefined;
  }
}
export function subscriptionServiceLabel(value: SubscriptionServiceIdentity): string | undefined {
  const service = subscriptionServiceFromWire(value);
  return service ? subscriptionServiceNames[service] : undefined;
}
