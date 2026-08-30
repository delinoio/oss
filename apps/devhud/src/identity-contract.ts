export function normalizeLogtoIssuer(value: unknown): string | null {
  if (typeof value !== "string" || value !== value.trim()) return null;
  try {
    const url = new URL(value);
    if (!url.hostname || url.username || url.password || url.search || url.hash) return null;
    if (url.protocol !== "https:" && !(url.protocol === "http:" && isLoopbackHostname(url.hostname))) return null;
    return url.toString();
  } catch {
    return null;
  }
}

export function logtoEndpointFromIssuer(value: unknown): string | null {
  const normalizedIssuer = normalizeLogtoIssuer(value);
  if (normalizedIssuer === null) return null;

  const endpoint = new URL(normalizedIssuer);
  const issuerPath = endpoint.pathname.replace(/\/+$/u, "");
  if (issuerPath.endsWith("/oidc")) {
    endpoint.pathname = issuerPath.slice(0, -"/oidc".length) || "/";
    return endpoint.toString();
  }
  return normalizedIssuer;
}

export function isValidLogtoAudience(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

export function isLoopbackHostname(hostname: string): boolean {
  const octets = hostname.split(".");
  return hostname.replace(/\.$/u, "").toLowerCase() === "localhost" || hostname === "[::1]" || hostname === "::1" || (octets.length === 4 && octets[0] === "127" && octets.every((octet) => /^\d+$/u.test(octet) && Number(octet) <= 255));
}

export function normalizePublicAssetUrl(value: unknown): string | null {
  if (typeof value !== "string" || value !== value.trim()) return null;
  try {
    const url = new URL(value);
    if (!url.hostname || url.username || url.password || url.search || url.hash) return null;
    if (url.protocol !== "https:" && !(url.protocol === "http:" && isLoopbackHostname(url.hostname))) return null;
    return url.toString().replaceAll("(", "%28").replaceAll(")", "%29");
  } catch {
    return null;
  }
}

export function normalizeNetworkOrigin(value: unknown): string | null {
	const normalized = normalizePublicAssetUrl(value);
	if (normalized === null) return null;
	const url = new URL(normalized);
	if (url.pathname !== "/") return null;
	return url.origin;
}
