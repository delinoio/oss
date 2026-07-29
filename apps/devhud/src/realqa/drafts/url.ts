import type { RestorableDraftUrl } from "./contracts";

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
  const host = hostname.toLowerCase().replace(/^\[|\]$/gu, "");
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    isPrivateIpv4(host) ||
    host === "::1" ||
    host.startsWith("fc") ||
    host.startsWith("fd") ||
    /^fe[89ab][0-9a-f]:/u.test(host)
  );
}

export function sanitizeCapturedUrl(value: string): CapturedUrlResult {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return { ok: false, reason: "invalid-url" };
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return { ok: false, reason: "unsupported-scheme" };
  }
  if (parsed.username !== "" || parsed.password !== "") {
    return { ok: false, reason: "credentials-forbidden" };
  }
  const strippedQuery = parsed.search === "" ? null : parsed.search;
  const strippedFragment = parsed.hash === "" ? null : parsed.hash;
  parsed.search = "";
  parsed.hash = "";
  return {
    ok: true,
    url: {
      value: parsed.toString(),
      strippedQuery,
      strippedFragment,
      warning: isLocalOrPrivateHost(parsed.hostname)
        ? "localhost-or-private-host"
        : null,
    },
  };
}

export function restoreCapturedUrlParts(
  url: RestorableDraftUrl,
): RestorableDraftUrl {
  const parsed = new URL(url.value);
  parsed.search = url.strippedQuery ?? "";
  parsed.hash = url.strippedFragment ?? "";
  return {
    ...url,
    value: parsed.toString(),
    strippedQuery: null,
    strippedFragment: null,
  };
}
