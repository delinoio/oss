export function configuredOriginPermissionPattern(value: string): string | null {
  try {
    const url = new URL(value);
    if ((url.protocol !== "http:" && url.protocol !== "https:") || url.origin !== value || url.username || url.password) return null;
    const effectivePort = url.port || (url.protocol === "https:" ? "443" : "80");
    return `${url.protocol}//${url.hostname.toLowerCase()}:${effectivePort}/*`;
  } catch {
    return null;
  }
}
