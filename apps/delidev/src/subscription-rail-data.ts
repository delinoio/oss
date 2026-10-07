// SPDX-License-Identifier: Apache-2.0
import { subscriptionService, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text } from "./documents";
import { serviceAccount } from "./subscription-resource";
export interface RailWindow { id: string; state: string; remaining?: number; observedAt: string; resetAt: string; blocking?: boolean; comparisonGroup: string; valid?: boolean }
export interface RailAccount { id: string; revision: bigint; alias: string; service: string; connected: boolean; disabled: boolean; windows: RailWindow[] }
export function railAccount(resource: Resource): RailAccount {
  if (!serviceAccount(resource)) throw new Error("Invalid subscription resource");
  const data = document(resource);
  return { id: resource.id, revision: resource.revision, alias: resourceName(resource), service: subscriptionService(data.subscription_service)!, disabled: data.enabled === false,
    connected: !data.removal && object(data.subscription).recovery_required !== true && Boolean(text(object(data.connection).id)),
    windows: items(data.quota).map(entry => { const value = object(entry);
      const stringFields = ["id", "state", "observed_at", "reset_at", "comparison_group"];
      const valid = stringFields.every(key => value[key] === undefined || typeof value[key] === "string") && (value.remaining === undefined || typeof value.remaining === "number") && (value.reset_at === undefined || value.reset_at === "" || typeof value.reset_at === "string" && Number.isFinite(Date.parse(value.reset_at)));
      return { valid, id: text(value.id), state: text(value.state), remaining: typeof value.remaining === "number" ? value.remaining : undefined, observedAt: text(value.observed_at), resetAt: text(value.reset_at), blocking: typeof value.blocking === "boolean" ? value.blocking : undefined, comparisonGroup: text(value.comparison_group) }; }) };
}
export function freshWindow(window: RailWindow, now: number) {
  const observed = Date.parse(window.observedAt), reset = Date.parse(window.resetAt);
  return window.valid !== false && typeof window.blocking === "boolean" && window.state === "observed" && typeof window.remaining === "number" && Number.isFinite(window.remaining) && window.remaining >= 0 && window.remaining <= 1 && Number.isFinite(observed) && observed <= now && now - observed <= 300000 && (window.resetAt === "" || Number.isFinite(reset) && reset > now);
}
export function remainingBadge(windows: readonly RailWindow[], now: number): number | undefined {
  if (windows.some(window => typeof window.blocking !== "boolean")) return undefined;
  const blocking = windows.filter(window => window.blocking);
  return blocking.length && blocking.every(window => freshWindow(window, now) && window.comparisonGroup) ? Math.round(Math.min(...blocking.map(window => window.remaining!)) * 100) : undefined;
}
