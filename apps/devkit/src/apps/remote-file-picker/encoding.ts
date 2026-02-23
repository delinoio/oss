const BASE64_PADDING = "=";

function normalizeBase64Value(value: string): string {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const remainder = normalized.length % 4;
  if (remainder === 0) {
    return normalized;
  }
  return normalized + BASE64_PADDING.repeat(4 - remainder);
}

export function decodeBase64Url(value: string): string {
  const binary = atob(normalizeBase64Value(value));
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

export function encodeBase64Url(value: string): string {
  const bytes = new TextEncoder().encode(value);
  const binary = String.fromCharCode(...bytes);
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

export function encodeJsonBase64Url(payload: object): string {
  return encodeBase64Url(JSON.stringify(payload));
}
