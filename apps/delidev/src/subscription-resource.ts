// SPDX-License-Identifier: Apache-2.0
import { EntityKind, isEntityId, subscriptionService, supportsResourceSchema, type Resource, type SubscriptionServiceId } from "@delinoio/delidev-api-client";
import { document } from "./documents";
export function serviceAccount(resource: Resource | undefined, id?: string, service?: SubscriptionServiceId, minimumRevision = 1n): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || resource.schemaVersion !== 2 || !supportsResourceSchema(resource) || !isEntityId(resource.id) || resource.revision < minimumRevision || id && resource.id !== id) return false;
  const data = document(resource);
  return data.retired !== true && data.type === "subscription" && Boolean(subscriptionService(data.subscription_service)) && (!service || data.subscription_service === service);
}

// Configuration currently accepts a complete JSON document. Patch only the
// alias token so JS cannot round server-owned uint64 lease revisions. Remove
// this scanner when a separately negotiated name-only mutation is available.
export function subscriptionAliasDocument(resource: Resource, alias: string): Uint8Array {
  const raw = new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson);
  const parsed = JSON.parse(raw);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) || typeof parsed.alias !== "string") throw new Error("Invalid account document");
  let cursor = 0;
  const whitespace = () => { while (/\s/.test(raw[cursor] ?? "") && cursor < raw.length) cursor++; };
  const stringEnd = () => {
    if (raw[cursor++] !== '"') throw new Error("Invalid account document");
    while (cursor < raw.length) { const ch = raw[cursor++]; if (ch === "\\") cursor++; else if (ch === '"') return cursor; }
    throw new Error("Invalid account document");
  };
  whitespace(); if (raw[cursor++] !== "{") throw new Error("Invalid account document");
  const keys = new Set<string>(); let range: [number, number] | undefined;
  while (cursor < raw.length) {
    whitespace(); if (raw[cursor] === "}") break;
    const keyStart = cursor; const key = JSON.parse(raw.slice(keyStart, stringEnd())) as string;
    if (keys.has(key)) throw new Error("Duplicate account field"); keys.add(key);
    whitespace(); if (raw[cursor++] !== ":") throw new Error("Invalid account document"); whitespace();
    const start = cursor; let depth = 0;
    while (cursor < raw.length) {
      const ch = raw[cursor];
      if (ch === '"') { stringEnd(); continue; }
      if (ch === "[" || ch === "{") depth++;
      if (ch === "]" || ch === "}") { if (depth === 0) break; depth--; }
      if (ch === "," && depth === 0) break;
      cursor++;
    }
    if (key === "alias") range = [start, cursor];
    if (raw[cursor] === "}") break;
    if (raw[cursor++] !== ",") throw new Error("Invalid account document");
  }
  if (!range) throw new Error("Missing account name");
  return new TextEncoder().encode(raw.slice(0, range[0]) + JSON.stringify(alias) + raw.slice(range[1]));
}
