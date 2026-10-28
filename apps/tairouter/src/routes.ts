// SPDX-License-Identifier: Apache-2.0
export const pagePaths = {
  overview: "/overview",
  providers: "/providers",
  pools: "/pools",
  routing: "/routing",
  keys: "/keys",
  usage: "/usage",
  connect: "/connect",
  members: "/members",
  invitations: "/invitations",
  "organization-usage": "/organization-usage",
  audit: "/audit",
  settings: "/settings",
  account: "/account",
  create: "/onboarding",
  instance: "/instance/health",
  device: "/device",
  invitation: "/invitation",
  ownership: "/ownership",
  "sign-in": "/sign-in",
} as const;
export type Page = keyof typeof pagePaths;
export const operatorPaths = {
  health: "/instance/health",
  limits: "/instance/settings",
  retention: "/instance/retention",
  destinations: "/instance/destinations",
  prices: "/instance/prices",
  audit: "/instance/audit",
} as const;
export type OperatorSection = keyof typeof operatorPaths;
export function operatorSection(path: string): OperatorSection {
  return (
    (Object.keys(operatorPaths) as OperatorSection[]).find(
      (section) => operatorPaths[section] === path,
    ) ?? "health"
  );
}
export const organizationPages: readonly Page[] = [
  "overview",
  "providers",
  "pools",
  "routing",
  "keys",
  "usage",
  "connect",
  "members",
  "invitations",
  "organization-usage",
  "audit",
  "settings",
  "account",
];
function validOrganization(id: string) {
  return !!id && id !== "." && id !== ".." && !/[\\/\x00-\x1f\x7f]/.test(id);
}
export function pageHref(page: Page, organization?: string) {
  return organization && organizationPages.includes(page)
    ? `/organizations/${encodeURIComponent(organization)}${pagePaths[page]}`
    : pagePaths[page];
}
export function resolveRoute(url: URL) {
  let pathname = url.pathname;
  let organization: string | undefined;
  if (pathname.startsWith("/organizations/")) {
    const parts = pathname.split("/");
    if (parts.length !== 4) return undefined;
    try {
      organization = decodeURIComponent(parts[2]);
    } catch {
      return undefined;
    }
    if (!validOrganization(organization)) return undefined;
    pathname = `/${parts[3]}`;
  }
  let page = (Object.keys(pagePaths) as Page[]).find(
    (key) => pagePaths[key] === pathname,
  );
  if (Object.values(operatorPaths).some((path) => path === pathname))
    page = "instance";
  if (!page || (organization && !organizationPages.includes(page)))
    return undefined;
  const standalone = ["device", "invitation", "ownership", "sign-in"].includes(
    page,
  );
  if (
    url.searchParams.has("page") ||
    url.searchParams.has("org") ||
    (!standalone && url.searchParams.has("organization_id")) ||
    (organization && url.searchParams.has("organization"))
  )
    return undefined;
  const canonical = new URL(url);
  if (!standalone) {
    // Accept existing bookmarks while emitting only path-based workspace links.
    organization ??= canonical.searchParams.get("organization") || undefined;
    if (organization && !validOrganization(organization)) return undefined;
    if (organizationPages.includes(page))
      canonical.pathname = pageHref(page, organization);
    else organization = undefined;
    canonical.searchParams.delete("organization");
  }
  return { page, organization, url: canonical };
}
