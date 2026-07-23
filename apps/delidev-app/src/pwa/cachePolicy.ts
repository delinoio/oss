export const PUBLIC_CATALOG_METHODS = new Set([
  "ListCatalogApps",
  "GetCatalogApp",
  "ListCatalogMeters",
  "GetCatalogMeter",
]);
const PUBLIC_CATALOG_ORIGIN = "https://delibase.deli.dev";

export enum CacheTarget {
  None = "none",
  PublicCatalog = "public-catalog",
  StaticShell = "static-shell",
}

interface CacheRequest {
  method: string;
  url: string;
  authorization?: string | null;
}

export function classifyCacheRequest(
  request: CacheRequest,
  shellPaths: ReadonlySet<string>,
  appOrigin = "https://deli.dev",
): CacheTarget {
  const url = new URL(request.url, appOrigin);
  if (
    request.method === "GET" &&
    url.origin === appOrigin &&
    shellPaths.has(url.pathname)
  ) {
    return CacheTarget.StaticShell;
  }

  const prefix = "/delibase.v1.CatalogService/";
  const catalogMethod = url.pathname.startsWith(prefix)
    ? url.pathname.slice(prefix.length)
    : "";
  if (
    request.method === "POST" &&
    url.origin === PUBLIC_CATALOG_ORIGIN &&
    !request.authorization &&
    PUBLIC_CATALOG_METHODS.has(catalogMethod)
  ) {
    return CacheTarget.PublicCatalog;
  }

  return CacheTarget.None;
}
