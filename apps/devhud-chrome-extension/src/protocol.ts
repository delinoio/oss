export const HostName = "io.delino.devhud.native_messaging";
export const ProtocolVersion = 1;
export const SchemaVersion = 1;
export const MaximumJsonBytes = 256 * 1024;
const SignedProofLength = 43;

export type NativeMessageType = "pair" | "configure" | "capture" | "ping";

export interface NativeResponse {
  readonly version: number;
  readonly schema_version: number;
  readonly request_id: string;
  readonly ok: boolean;
  readonly state: "paired" | "accepted" | "disconnected" | "denied" | "malformed";
  readonly payload: unknown;
}

function uuidV7(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const timestamp = BigInt(Date.now());
  for (let index = 0; index < 6; index += 1) bytes[5 - index] = Number((timestamp >> BigInt(index * 8)) & 0xffn);
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function nonce(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "");
}

export function createRequest(type: NativeMessageType, payload: unknown, pairingNonce?: string) {
  return {
    version: ProtocolVersion,
    schema_version: SchemaVersion,
    request_id: uuidV7(),
    type,
    deadline_unix_ms: Date.now() + 5_000,
    nonce: nonce(),
    ...(pairingNonce ? { pairing_nonce: pairingNonce } : {}),
    payload,
  };
}

function byteLength(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value)).byteLength;
}

function fitsSharedEnvelopes(request: ReturnType<typeof createRequest>): boolean {
  // The host consumes pairing_nonce during authentication, then adds the
  // largest safe millisecond timestamp and a 43-byte Base64URL SHA-256 proof.
  const forwardedRequest = {
    version: request.version,
    schema_version: request.schema_version,
    request_id: request.request_id,
    type: request.type,
    issued_at_unix_ms: Number.MAX_SAFE_INTEGER,
    deadline_unix_ms: request.deadline_unix_ms,
    nonce: request.nonce,
    payload: request.payload,
    proof: "A".repeat(SignedProofLength),
  };
  return byteLength(request) <= MaximumJsonBytes && byteLength(forwardedRequest) <= MaximumJsonBytes;
}

export function createBoundedRequest(type: NativeMessageType, payload: unknown, pairingNonce?: string) {
  let request = createRequest(type, payload, pairingNonce);
  if (!fitsSharedEnvelopes(request) && type === "capture" && typeof payload === "object" && payload !== null) {
    const capture = payload as { readonly context?: unknown };
    if (typeof capture.context === "object" && capture.context !== null) request = createRequest(type, { ...payload, context: { ...capture.context, outerHtml: "" } }, pairingNonce);
  }
  return { request, withinLimit: fitsSharedEnvelopes(request) } as const;
}

export function isNativeResponse(value: unknown): value is NativeResponse {
  if (typeof value !== "object" || value === null) return false;
  const response = value as Partial<NativeResponse>;
  return response.version === ProtocolVersion && response.schema_version === SchemaVersion
    && typeof response.request_id === "string" && typeof response.ok === "boolean"
    && ["paired", "accepted", "disconnected", "denied", "malformed"].includes(response.state ?? "");
}
