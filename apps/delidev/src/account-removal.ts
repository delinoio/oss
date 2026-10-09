// SPDX-License-Identifier: Apache-2.0
import { EntityKind, isEntityId, type Resource } from "@delinoio/delidev-api-client";

// Read direct object members from already validated JSON, retaining the original
// value tokens. JSON.parse is used for syntax and strings only; numeric retry
// authority must never come from its rounded Number representation.
function objectTokens(raw: string): Map<string, string> {
  let cursor = 0;
  const whitespace = () => { while (cursor < raw.length && /[ \t\r\n]/.test(raw[cursor])) cursor++; };
  const stringEnd = () => {
    if (raw[cursor++] !== '"') throw new Error("Invalid object member");
    while (cursor < raw.length) {
      const character = raw[cursor++];
      if (character === "\\") cursor++;
      else if (character === '"') return cursor;
    }
    throw new Error("Unterminated string");
  };
  whitespace();
  if (raw[cursor++] !== "{") throw new Error("Expected an object");
  const fields = new Map<string, string>();
  while (cursor < raw.length) {
    whitespace();
    if (raw[cursor] === "}") break;
    const startKey = cursor;
    const key = JSON.parse(raw.slice(startKey, stringEnd())) as string;
    if (fields.has(key)) throw new Error("Duplicate object member");
    whitespace();
    if (raw[cursor++] !== ":") throw new Error("Invalid object member");
    whitespace();
    const startValue = cursor;
    let depth = 0;
    while (cursor < raw.length) {
      const character = raw[cursor];
      if (character === '"') { stringEnd(); continue; }
      if (character === "[" || character === "{") depth++;
      if (character === "]" || character === "}") {
        if (depth === 0) break;
        depth--;
      }
      if (character === "," && depth === 0) break;
      cursor++;
    }
    fields.set(key, raw.slice(startValue, cursor).trim());
    if (raw[cursor] === "}") break;
    if (raw[cursor++] !== ",") throw new Error("Invalid object member");
  }
  return fields;
}

export function accountRemovalMutation(resource: Resource): { id: string; requestId: string; expectedRevision: bigint } | undefined {
  if (resource.kind !== EntityKind.ACCOUNT || (resource.schemaVersion !== 1 && resource.schemaVersion !== 2) || !isEntityId(resource.id) || resource.documentJson.byteLength > 1 << 20) return;
  try {
    const raw = new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson);
    JSON.parse(raw); // Validate the complete document before scanning its tokens.
    const fields = objectTokens(raw);
    const type = JSON.parse(fields.get("type") ?? "null");
    const keyAccount = type === "api" && resource.schemaVersion === 1 || type === "subscription" && resource.schemaVersion === 2 && JSON.parse(fields.get("subscription_service") ?? "null") === "opencode_go";
    if (!keyAccount || JSON.parse(fields.get("health") ?? "null") !== "disconnected" || fields.has("connection")) return;
    const removal = objectTokens(fields.get("removal") ?? "");
    if (removal.size !== 2 || !removal.has("request_id") || !removal.has("expected_revision")) return;
    const requestId: unknown = JSON.parse(removal.get("request_id")!);
    const revision = removal.get("expected_revision")!;
    if (typeof requestId !== "string" || !isEntityId(requestId) || !/^[1-9][0-9]{0,19}$/.test(revision)) return;
    const expectedRevision = BigInt(revision);
    if (expectedRevision > 18446744073709551615n) return;
    return { id: resource.id, requestId, expectedRevision };
  } catch { return; }
}
