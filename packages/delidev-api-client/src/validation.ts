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
