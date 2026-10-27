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
export function pageHref(page: Page, organization?: string) {
  const params = new URLSearchParams();
  if (organization) params.set("organization", organization);
  return pagePaths[page] + (params.size ? `?${params}` : "");
}
export function resolveRoute(url: URL) {
  let page = (Object.keys(pagePaths) as Page[]).find(
    (key) => pagePaths[key] === url.pathname,
  );
  if (Object.values(operatorPaths).some((path) => path === url.pathname))
    page = "instance";
  if (!page) return undefined;
  const standalone = ["device", "invitation", "ownership", "sign-in"].includes(
    page,
  );
  if (
    url.searchParams.has("page") ||
    url.searchParams.has("org") ||
    (!standalone && url.searchParams.has("organization_id"))
  )
    return undefined;
  const canonical = new URL(url);
  if (!standalone && !canonical.searchParams.get("organization"))
    canonical.searchParams.delete("organization");
  return { page, url: canonical };
}
