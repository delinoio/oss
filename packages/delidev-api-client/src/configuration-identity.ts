// SPDX-License-Identifier: Apache-2.0
import { EntityKind, SubscriptionServiceIdentity, type Resource } from "./gen/delidev/v1/delidev_pb.js";

export enum SubscriptionServiceId { ChatGPT = "chatgpt", Claude = "claude", Grok = "grok" }
export enum NativeModelSourceKind { Subscription = "subscription" }
export const subscriptionServiceNames: Readonly<Record<SubscriptionServiceId, string>> = {
  [SubscriptionServiceId.ChatGPT]: "ChatGPT", [SubscriptionServiceId.Claude]: "Claude", [SubscriptionServiceId.Grok]: "Grok",
};
export const subscriptionServiceHarnesses: Readonly<Record<SubscriptionServiceId, string>> = {
  [SubscriptionServiceId.ChatGPT]: "codex", [SubscriptionServiceId.Claude]: "claude-code", [SubscriptionServiceId.Grok]: "grok-build",
};
export function subscriptionService(value: unknown): SubscriptionServiceId | undefined {
  return Object.values(SubscriptionServiceId).find((service) => service === value);
}
function object(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value); }

// Version 2 is accepted only for its owning identity family. This does not grant
// mutation, native readiness or interpretation of a retired original document.
export function supportsResourceSchema(resource: Resource): boolean {
  if (resource.documentJson.byteLength > 1 << 20) return false;
  if (resource.schemaVersion === 1) return true;
  if (resource.schemaVersion !== 2) return false;
  try {
    const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson));
    if (!object(value)) return false;
    if ([EntityKind.ACCOUNT, EntityKind.PROVIDER, EntityKind.MODEL].includes(resource.kind) && value.retired === true) {
      return value.original_schema_version === 1 && object(value.original_document);
    }
    if (resource.kind === EntityKind.AGENT) return value.reconfiguration_required === true;
    const service = subscriptionService(value.subscription_service);
    if (!service || Object.hasOwn(value, "provider_id")) return false;
    if (resource.kind === EntityKind.ACCOUNT) return value.type === "subscription";
    return resource.kind === EntityKind.MODEL && value.source_kind === NativeModelSourceKind.Subscription &&
      Array.isArray(value.harnesses) && value.harnesses.length === 1 && value.harnesses[0] === subscriptionServiceHarnesses[service];
  } catch { return false; }
}
export function configurationSchemaVersion(kind: EntityKind, value: Record<string, unknown>): number {
  return kind === EntityKind.ACCOUNT && value.type === "subscription" ||
    kind === EntityKind.MODEL && value.source_kind === NativeModelSourceKind.Subscription ||
    kind === EntityKind.AGENT && value.reconfiguration_required === true ? 2 : 1;
}

export function subscriptionServiceFromWire(value: SubscriptionServiceIdentity): SubscriptionServiceId | undefined {
  switch (value) {
    case SubscriptionServiceIdentity.CHATGPT: return SubscriptionServiceId.ChatGPT;
    case SubscriptionServiceIdentity.CLAUDE: return SubscriptionServiceId.Claude;
    case SubscriptionServiceIdentity.GROK: return SubscriptionServiceId.Grok;
    default: return undefined;
  }
}
export function subscriptionServiceLabel(value: SubscriptionServiceIdentity): string | undefined {
  const service = subscriptionServiceFromWire(value);
  return service ? subscriptionServiceNames[service] : undefined;
}
