export function logtoEndpointFromIssuer(issuer: string): string {
  const endpoint = new URL(issuer);
  const issuerPath = endpoint.pathname.replace(/\/+$/u, "");
  if (issuerPath.endsWith("/oidc")) {
    endpoint.pathname = issuerPath.slice(0, -"/oidc".length) || "/";
  }
  return endpoint.toString();
}
