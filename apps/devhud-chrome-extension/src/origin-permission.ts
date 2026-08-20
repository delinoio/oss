export function configuredOriginPermissionPattern(value: string): string | null {
  try {
    const url = new URL(value);
    if ((url.protocol !== "http:" && url.protocol !== "https:") || url.origin !== value || url.username || url.password) return null;
    return `${url.protocol}//${url.hostname.toLowerCase()}/*`;
  } catch {
    return null;
  }
}
