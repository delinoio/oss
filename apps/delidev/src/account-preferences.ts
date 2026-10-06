// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";

interface AccountPreferences {
  alias: string;
  enabled: boolean;
  exclude_automatic: boolean;
  recovery_notifications: boolean;
}

const preferenceTypes = { alias: "string", enabled: "boolean", exclude_automatic: "boolean", recovery_notifications: "boolean" } as const;

// Configuration accepts a complete document. Replace only preference tokens so
// protected numbers never pass through Number or JSON.stringify. Remove this
// scanner when a separately negotiated preference-only mutation is available.
export function accountPreferencesDocument(resource: Resource, preferences: Partial<AccountPreferences>): Uint8Array {
  const replacements = new Map<string, string>();
  for (const [key, value] of Object.entries(preferences)) {
    if (!Object.hasOwn(preferenceTypes, key) || typeof value !== preferenceTypes[key as keyof AccountPreferences]) throw new Error("Invalid account preference");
    replacements.set(key, JSON.stringify(value));
  }
  if (resource.documentJson.byteLength > 1 << 20) throw new Error("Account document is too large");
  const raw = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(resource.documentJson);
  let cursor = 0;
  const invalid = (): never => { throw new Error("Invalid account document"); };
  const whitespace = () => { while (/[ \t\r\n]/.test(raw[cursor] ?? "") && cursor < raw.length) cursor++; };
  const string = (): string => {
    const start = cursor;
    if (raw[cursor++] !== '"') return invalid();
    while (cursor < raw.length) {
      const ch = raw[cursor++];
      if (ch === "\\") cursor++;
      else if (ch === '"') return JSON.parse(raw.slice(start, cursor)) as string;
    }
    return invalid();
  };
  const ranges: { key: string; start: number; end: number }[] = [];
  let closing = 0;
  const value = (depth: number): void => {
    if (depth > 128) return invalid();
    whitespace();
    const ch = raw[cursor];
    if (ch === '"') { string(); return; }
    if (ch === "{" || ch === "[") {
      const isObject = ch === "{", end = isObject ? "}" : "]", keys = new Set<string>();
      cursor++; whitespace();
      if (raw[cursor] !== end) {
        while (cursor < raw.length) {
          let key = "";
          if (isObject) {
            whitespace(); key = string();
            if (keys.has(key)) return invalid();
            keys.add(key); whitespace();
            if (raw[cursor++] !== ":") return invalid();
          }
          whitespace(); const start = cursor;
          value(depth + 1);
          if (depth === 0 && isObject) ranges.push({ key, start, end: cursor });
          whitespace();
          if (raw[cursor] === end) break;
          if (raw[cursor++] !== ",") return invalid();
        }
      }
      if (depth === 0) closing = cursor;
      if (raw[cursor++] !== end) return invalid();
      return;
    }
    const token = /^(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)/.exec(raw.slice(cursor))?.[0];
    if (!token) return invalid();
    cursor += token.length;
  };
  whitespace();
  if (raw[cursor] !== "{") return invalid();
  value(0); whitespace();
  if (cursor !== raw.length) return invalid();
  const alias = ranges.find((range) => range.key === "alias");
  if (!alias || raw[alias.start] !== '"') return invalid();
  const parts: string[] = [];
  let copied = 0;
  for (const range of ranges) {
    const replacement = replacements.get(range.key);
    if (replacement === undefined) continue;
    parts.push(raw.slice(copied, range.start), replacement);
    copied = range.end; replacements.delete(range.key);
  }
  parts.push(raw.slice(copied, closing));
  for (const [key, replacement] of replacements) {
    parts.push(",", JSON.stringify(key), ":", replacement);
  }
  parts.push(raw.slice(closing));
  const bytes = new TextEncoder().encode(parts.join(""));
  if (bytes.byteLength > 1 << 20) throw new Error("Account document is too large");
  return bytes;
}
