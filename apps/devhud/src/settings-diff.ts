export const RedactedValue = "[redacted]" as const;

export interface SettingsDiffEntry {
  readonly path: string;
  readonly kind: "added" | "removed" | "changed";
  readonly local: unknown;
  readonly server: unknown;
}

const secretKeyPattern = /(?:^|[-_.])(?:token|password|passwd|pwd|secret|pat|access[-_.]?key(?:[-_.]?id)?|private[-_.]?key|authorization|cookie)(?:$|[-_.])/iu;
const secretValuePatterns = [
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/gu,
  /\bAKIA[0-9A-Z]{16}\b/gu,
  /\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/gu,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/gu,
] as const;

export function redactRecursively(value: unknown, key = "", seen = new WeakSet<object>()): unknown {
  if (secretKeyPattern.test(key)) return RedactedValue;
  if (typeof value === "string") {
    let redacted = value;
    for (const pattern of secretValuePatterns) redacted = redacted.replace(pattern, RedactedValue);
    return redacted;
  }
  if (value === null || typeof value !== "object") return value;
  if (seen.has(value)) return RedactedValue;
  seen.add(value);
  const result = Array.isArray(value)
    ? value.map((item, index) => redactRecursively(item, String(index), seen))
    : Object.fromEntries(Object.entries(value).map(([childKey, item]) => [childKey, redactRecursively(item, childKey, seen)]));
  seen.delete(value);
  return result;
}

export function diffSettings(localValue: unknown, serverValue: unknown): readonly SettingsDiffEntry[] {
  const entries: SettingsDiffEntry[] = [];
  visit(redactRecursively(localValue), redactRecursively(serverValue), "$", entries);
  return entries;
}

function visit(local: unknown, server: unknown, path: string, entries: SettingsDiffEntry[]): void {
  if (Object.is(local, server)) return;
  if (isRecord(local) && isRecord(server)) {
    const keys = [...new Set([...Object.keys(local), ...Object.keys(server)])].sort();
    for (const key of keys) {
      const next = `${path}.${key}`;
      if (!(key in local)) entries.push({ path: next, kind: "added", local: undefined, server: server[key] });
      else if (!(key in server)) entries.push({ path: next, kind: "removed", local: local[key], server: undefined });
      else visit(local[key], server[key], next, entries);
    }
    return;
  }
  if (Array.isArray(local) && Array.isArray(server)) {
    const length = Math.max(local.length, server.length);
    for (let index = 0; index < length; index += 1) {
      const next = `${path}[${index}]`;
      if (index >= local.length) entries.push({ path: next, kind: "added", local: undefined, server: server[index] });
      else if (index >= server.length) entries.push({ path: next, kind: "removed", local: local[index], server: undefined });
      else visit(local[index], server[index], next, entries);
    }
    return;
  }
  entries.push({ path, kind: "changed", local, server });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
