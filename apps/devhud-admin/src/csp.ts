const BASE_CONNECT_SOURCES = ["'self'", "http://127.0.0.1:46307", "https:"];

function contentSecurityPolicy(connectSources: readonly string[]): string {
  return [
    "default-src 'self'",
    "script-src 'self'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self'",
    `connect-src ${[...new Set(connectSources)].join(" ")}`,
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
    "frame-ancestors 'none'",
  ].join("; ");
}

export const ADMIN_CSP = contentSecurityPolicy(BASE_CONNECT_SOURCES);

export function developmentAdminCsp(rawIssuer: string | undefined): string {
  if (rawIssuer === undefined || rawIssuer.trim() === "") {
    throw new Error("DEVHUD_LOGTO_ISSUER is required for DevHud Admin development");
  }

  let issuer: URL;
  try {
    issuer = new URL(rawIssuer);
  } catch {
    throw new Error("DEVHUD_LOGTO_ISSUER must be an absolute HTTP URL");
  }
  if (
    issuer.username !== "" ||
    issuer.password !== "" ||
    issuer.search !== "" ||
    issuer.hash !== ""
  ) {
    throw new Error(
      "DEVHUD_LOGTO_ISSUER must not contain credentials, a query, or a fragment",
    );
  }
  if (issuer.protocol === "http:" && !isLoopbackHost(issuer.hostname)) {
    throw new Error("DEVHUD_LOGTO_ISSUER must use HTTPS outside loopback");
  }
  if (issuer.protocol !== "http:" && issuer.protocol !== "https:") {
    throw new Error("DEVHUD_LOGTO_ISSUER must use HTTP or HTTPS");
  }

  return contentSecurityPolicy([...BASE_CONNECT_SOURCES, issuer.origin]);
}

function isLoopbackHost(hostname: string): boolean {
  const normalized = hostname.toLowerCase().replace(/\.$/u, "");
  if (normalized === "localhost" || normalized === "[::1]") return true;
  const octets = normalized.split(".");
  return octets.length === 4 && octets[0] === "127" && octets.every(isIPv4Octet);
}

function isIPv4Octet(value: string): boolean {
  if (!/^\d{1,3}$/u.test(value)) return false;
  const number = Number(value);
  return number >= 0 && number <= 255;
}
