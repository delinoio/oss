import { Code, ConnectError } from "@connectrpc/connect";

const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function isEntityId(value: string): boolean {
  return idPattern.test(value);
}

export function requireEntityId(value: string): void {
  if (!isEntityId(value)) throw new ConnectError("A canonical UUID v7 is required.", Code.InvalidArgument);
}

// One identity per logical mutation. A retry must reuse the complete original
// request, including this identity, rather than generating another operation.
export function newRequestId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  let timestamp = BigInt(Date.now());
  for (let i = 5; i >= 0; i--) {
    bytes[i] = Number(timestamp & 255n);
    timestamp >>= 8n;
  }
  bytes[6] = (bytes[6] & 15) | 0x70;
  bytes[8] = (bytes[8] & 63) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function serverOrigin(value: string): string {
  const invalid = () => new ConnectError("Select an HTTPS server origin, or an exact HTTP loopback origin.", Code.InvalidArgument);
  // Inspect the original authority before WHATWG normalization. Numeric IPv4
  // aliases, backslashes, paths and encoded credentials must not become trusted.
  const match = /^(https?):\/\/([^\s/?#\\]+)\/?$/.exec(value);
  if (!match) throw invalid();
  let url: URL;
  try { url = new URL(value); } catch { throw invalid(); }
  if (url.username || url.password || url.port === "0") throw invalid();
  if (match[1] === "http" && !/^(127\.0\.0\.1|localhost|\[::1\])(?::[1-9][0-9]{0,4})?$/.test(match[2])) throw invalid();
  return url.origin;
}


export type NativeAppsToolSnapshot = {
  kind: "native-apps"; status: "running" | "completed" | "failed"; changes?: null;
  apps: { app_id: string; name: string; tool_name: string; arguments_present: boolean;
    result?: { content: string[]; structured_content?: string }; error_present: boolean; duration_ms?: number };
};
const appObject = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === "object" && !Array.isArray(v);
const appKeys = (v: Record<string, unknown>, required: string[], optional: string[] = []) => required.every(k => Object.hasOwn(v, k)) && Object.keys(v).every(k => required.includes(k) || optional.includes(k));
function appText(v: unknown, max: number, required = false): v is string {
  return typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max && (!required || v.trim().length > 0);
}
function appJSON(v: unknown): v is string {
  if (!appText(v, 384 << 10)) return false;
  try {
    const pending: unknown[] = [JSON.parse(v)];
    while (pending.length) {
      const item = pending.pop();
      if (typeof item === "string" && /[\uD800-\uDFFF]/u.test(item)) return false;
      if (item !== null && typeof item === "object") {
        if (Object.keys(item).some(key => /[\uD800-\uDFFF]/u.test(key))) return false;
        for (const value of Object.values(item)) pending.push(value);
      }
    }
    return true;
  } catch { return false; }
}
/** Closed native result metadata grants no fetching, embedding or execution authority. */
export function nativeAppsToolSnapshot(value: unknown): NativeAppsToolSnapshot | undefined {
  if (!appObject(value) || !appKeys(value, ["kind", "status", "apps"], ["changes"]) || value.kind !== "native-apps" || value.changes != null || typeof value.status !== "string" || !["running", "completed", "failed"].includes(value.status)) return;
  const a = value.apps;
  if (!appObject(a) || !appKeys(a, ["app_id", "name", "tool_name", "arguments_present", "error_present"], ["result", "duration_ms"]) || !appText(a.app_id, 1024, true) || a.app_id.trim() !== a.app_id || !appText(a.name, 1024, true) || !appText(a.tool_name, 1024, true) || typeof a.arguments_present !== "boolean" || typeof a.error_present !== "boolean") return;
  if (Object.hasOwn(a, "duration_ms") && (!Number.isSafeInteger(a.duration_ms) || Number(a.duration_ms) < 0)) return;
  if (value.status === "running" && (Object.hasOwn(a, "result") || a.error_present || Object.hasOwn(a, "duration_ms"))) return;
  if (value.status === "completed" && (!Object.hasOwn(a, "result") || a.error_present)) return;
  if (value.status === "failed" && !Object.hasOwn(a, "result") && !a.error_present) return;
  if (Object.hasOwn(a, "result")) {
    const r = a.result;
    if (!appObject(r) || !appKeys(r, ["content"], ["structured_content"]) || !Array.isArray(r.content) || r.content.length > 256 || !r.content.every(appJSON) || (Object.hasOwn(r, "structured_content") && !appJSON(r.structured_content))) return;
    const bytes = r.content.reduce((n, v) => n + new TextEncoder().encode(v).length, 0) + (typeof r.structured_content === "string" ? new TextEncoder().encode(r.structured_content).length : 0);
    if (bytes > 384 << 10) return;
  }
  return value as NativeAppsToolSnapshot;
}
