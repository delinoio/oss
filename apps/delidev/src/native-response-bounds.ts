const encoder = new TextEncoder();
export const nativeResponseLimit = 256 * 1024;

export function nativeResponseByteLength(response: unknown): number {
  // Go's native response validation uses encoding/json, which escapes these
  // characters even when the renderer's RPC JSON leaves them literal.
  const json = JSON.stringify(response).replace(/[<>&\u2028\u2029]/g, (value) => `\\u${value.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return encoder.encode(json).byteLength;
}

export function nativeResponseOverflow(fields: { values: string[]; limit: number; guidance: string }[], response: () => unknown): string | undefined {
  // Retention checks sizes only. Empty answers and unfinished denial reasons
  // remain editable; submission validation still owns native answer validity.
  for (const field of fields) {
    if (field.values.some((value) => value.length > field.limit || encoder.encode(value).byteLength > field.limit)) return `${field.guidance} Your previous draft was kept.`;
  }
  if (nativeResponseByteLength(response()) > nativeResponseLimit) return "The complete response exceeds 256 KiB. Shorten it before adding more text. Your previous draft was kept.";
}
