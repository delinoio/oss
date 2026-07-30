import type { RestorableDraftUrl } from "./contracts";

const MAX_URL_BYTES = 8_192;
const INVALID_PERCENT_ESCAPE = /%(?![0-9a-f]{2})/iu;
const HTTP_URL_PREFIX = /^https?:\/\//iu;

export type CapturedUrlResult =
  | { readonly ok: true; readonly url: RestorableDraftUrl }
  | {
      readonly ok: false;
      readonly reason:
        | "invalid-url"
        | "unsupported-scheme"
        | "credentials-forbidden";
    };

function isPrivateIpv4(hostname: string): boolean {
  const parts = hostname.split(".").map(Number);
  if (
    parts.length !== 4 ||
    parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)
  ) {
    return false;
  }
  const [first = -1, second = -1] = parts;
  return (
    first === 10 ||
    first === 127 ||
    first === 0 ||
    (first === 169 && second === 254) ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168)
  );
}

function isLocalOrPrivateHost(hostname: string): boolean {
  const host = hostname
    .toLowerCase()
    .replace(/^\[|\]$/gu, "")
    .replace(/\.$/u, "");
  const mappedIpv4 = host.match(
    /^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/u,
  );
  const high = Number.parseInt(mappedIpv4?.[1] ?? "", 16);
  const low = Number.parseInt(mappedIpv4?.[2] ?? "", 16);
  const mappedAddress = mappedIpv4
    ? `${high >> 8}.${high & 0xff}.${low >> 8}.${low & 0xff}`
    : null;
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    isPrivateIpv4(host) ||
    (mappedAddress !== null && isPrivateIpv4(mappedAddress)) ||
    host === "::1" ||
    host.startsWith("fc") ||
    host.startsWith("fd") ||
    /^fe[89ab][0-9a-f]:/u.test(host)
  );
}

function retainBoundedUrlPart(value: string): string | null {
  return value !== "" &&
    new TextEncoder().encode(value).byteLength <= MAX_URL_BYTES
    ? value
    : null;
}

function containsRawControl(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? -1;
    if (codePoint <= 0x1f || codePoint === 0x7f) return true;
  }
  return false;
}

function containsInvalidPercentEscape(
  value: string,
  allowRawQueryPercent: boolean,
): boolean {
  if (!allowRawQueryPercent) return INVALID_PERCENT_ESCAPE.test(value);
  const fragmentIndex = value.indexOf("#");
  const queryIndex = value.indexOf("?");
  if (queryIndex < 0 || (fragmentIndex >= 0 && fragmentIndex < queryIndex)) {
    return INVALID_PERCENT_ESCAPE.test(value);
  }
  const outsideQuery =
    value.slice(0, queryIndex) +
    (fragmentIndex < 0 ? "" : value.slice(fragmentIndex));
  return INVALID_PERCENT_ESCAPE.test(outsideQuery);
}

function sanitizeUrl(
  value: string,
  allowRawQueryPercent: boolean,
): CapturedUrlResult {
  if (
    containsRawControl(value) ||
    containsInvalidPercentEscape(value, allowRawQueryPercent) ||
    value.includes("\\")
  ) {
    return { ok: false, reason: "invalid-url" };
  }
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return { ok: false, reason: "invalid-url" };
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return { ok: false, reason: "unsupported-scheme" };
  }
  const prefix = value.match(HTTP_URL_PREFIX)?.[0];
  if (prefix === undefined) {
    return { ok: false, reason: "invalid-url" };
  }
  const authority = value
    .slice(prefix.length)
    .split(/[/?#]/u, 1)[0] ?? "";
  if (
    authority === "" ||
    authority.includes("%") ||
    authority.includes(" ")
  ) {
    return { ok: false, reason: "invalid-url" };
  }
  if (parsed.username !== "" || parsed.password !== "") {
    return { ok: false, reason: "credentials-forbidden" };
  }
  const strippedQuery = retainBoundedUrlPart(parsed.search);
  const strippedFragment = retainBoundedUrlPart(parsed.hash);
  parsed.search = "";
  parsed.hash = "";
  const rawPath =
    value
      .slice(prefix.length + authority.length)
      .split(/[?#]/u, 1)[0] ?? "";
  // Synchronized rules retain Go's raw scheme, authority, and path spelling.
  const canonicalValue = allowRawQueryPercent
    ? `${prefix}${authority}${rawPath}`
    : parsed.toString();
  if (new TextEncoder().encode(canonicalValue).byteLength > MAX_URL_BYTES) {
    return { ok: false, reason: "invalid-url" };
  }
  return {
    ok: true,
    url: {
      value: canonicalValue,
      strippedQuery,
      strippedFragment,
      warning: isLocalOrPrivateHost(parsed.hostname)
        ? "localhost-or-private-host"
        : null,
    },
  };
}

export function sanitizeCapturedUrl(value: string): CapturedUrlResult {
  return sanitizeUrl(value, false);
}

/** Go's URL parser preserves raw percent text in queries without decoding it. */
export function sanitizeResolvedRuleUrl(value: string): CapturedUrlResult {
  return sanitizeUrl(value, true);
}

export function restoreCapturedUrlParts(
  url: RestorableDraftUrl,
): string {
  return `${url.value}${url.strippedQuery ?? ""}${url.strippedFragment ?? ""}`;
}
