import type { RestorableDraftUrl } from "./contracts";

const MAX_URL_BYTES = 8_192;
const INVALID_PERCENT_ESCAPE = /%(?![0-9a-f]{2})/iu;
const HTTP_URL_PREFIX = /^https?:\/\//iu;
const GO_INVALID_UNBRACKETED_HOST = /[\\^`{|}]/u;

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

function synchronizedHostname(authority: string): string | null {
  if (
    authority === "" ||
    authority.includes("%") ||
    authority.includes(" ") ||
    GO_INVALID_UNBRACKETED_HOST.test(authority)
  ) {
    return null;
  }
  if (authority.startsWith("[")) {
    const closingBracket = authority.lastIndexOf("]");
    if (
      closingBracket < 0 ||
      !/^(?::\d*)?$/u.test(authority.slice(closingBracket + 1))
    ) {
      return null;
    }
    try {
      return new URL(`http://${authority}/`).hostname;
    } catch {
      return null;
    }
  }
  if (authority.includes("[")) return null;
  const firstColon = authority.indexOf(":");
  if (
    firstColon >= 0 &&
    (firstColon !== authority.lastIndexOf(":") ||
      !/^:\d*$/u.test(authority.slice(firstColon)))
  ) {
    return null;
  }
  return (firstColon < 0 ? authority : authority.slice(0, firstColon))
    .toLowerCase()
    .replace(/\.$/u, "");
}

function rawUrlParts(
  value: string,
  contentStart: number,
): {
  readonly path: string;
  readonly query: string | null;
  readonly fragment: string | null;
} {
  const fragmentIndex = value.indexOf("#", contentStart);
  const queryIndex = value.indexOf("?", contentStart);
  const pathEnd =
    queryIndex >= 0 && (fragmentIndex < 0 || queryIndex < fragmentIndex)
      ? queryIndex
      : fragmentIndex >= 0
        ? fragmentIndex
        : value.length;
  return {
    path: value.slice(contentStart, pathEnd),
    query:
      queryIndex >= 0 && (fragmentIndex < 0 || queryIndex < fragmentIndex)
        ? value.slice(queryIndex, fragmentIndex < 0 ? value.length : fragmentIndex)
        : null,
    fragment: fragmentIndex >= 0 ? value.slice(fragmentIndex) : null,
  };
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
  const prefix = value.match(HTTP_URL_PREFIX)?.[0];
  if (prefix === undefined) {
    try {
      const parsed = new URL(value);
      return parsed.protocol === "http:" || parsed.protocol === "https:"
        ? { ok: false, reason: "invalid-url" }
        : { ok: false, reason: "unsupported-scheme" };
    } catch {
      return { ok: false, reason: "invalid-url" };
    }
  }
  const authority = value
    .slice(prefix.length)
    .split(/[/?#]/u, 1)[0] ?? "";
  if (authority.includes("@")) {
    return { ok: false, reason: "credentials-forbidden" };
  }
  const synchronizedHost = synchronizedHostname(authority);
  if (synchronizedHost === null) {
    return { ok: false, reason: "invalid-url" };
  }
  const parts = rawUrlParts(value, prefix.length + authority.length);
  let strippedQuery: string | null;
  let strippedFragment: string | null;
  let canonicalValue: string;
  let warningHostname: string;
  if (allowRawQueryPercent) {
    strippedQuery = retainBoundedUrlPart(parts.query ?? "");
    strippedFragment = retainBoundedUrlPart(parts.fragment ?? "");
    canonicalValue = `${prefix}${authority}${parts.path}`;
    warningHostname = synchronizedHost;
  } else {
    let parsed: URL;
    try {
      parsed = new URL(value);
    } catch {
      return { ok: false, reason: "invalid-url" };
    }
    strippedQuery = retainBoundedUrlPart(parsed.search);
    strippedFragment = retainBoundedUrlPart(parsed.hash);
    parsed.search = "";
    parsed.hash = "";
    canonicalValue = parsed.toString();
    warningHostname = parsed.hostname;
  }
  if (new TextEncoder().encode(canonicalValue).byteLength > MAX_URL_BYTES) {
    return { ok: false, reason: "invalid-url" };
  }
  return {
    ok: true,
    url: {
      value: canonicalValue,
      strippedQuery,
      strippedFragment,
      warning: isLocalOrPrivateHost(warningHostname)
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
