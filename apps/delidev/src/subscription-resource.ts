// SPDX-License-Identifier: Apache-2.0
import { EntityKind, isEntityId, subscriptionService, supportsResourceSchema, type Resource, type SubscriptionServiceId } from "@delinoio/delidev-api-client";
import { document } from "./documents";
import { accountPreferencesDocument } from "./account-preferences";
export function serviceAccount(resource: Resource | undefined, id?: string, service?: SubscriptionServiceId, minimumRevision = 1n): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || resource.schemaVersion !== 2 || !supportsResourceSchema(resource) || !isEntityId(resource.id) || resource.revision < minimumRevision || id && resource.id !== id) return false;
  const data = document(resource);
  return data.type === "subscription" && Boolean(subscriptionService(data.subscription_service)) && (!service || data.subscription_service === service);
}

export function subscriptionAliasDocument(resource: Resource, alias: string): Uint8Array {
  return accountPreferencesDocument(resource, { alias });
}
