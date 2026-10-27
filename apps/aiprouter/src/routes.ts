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
const aliases: Record<string, Page> = {
  "/": "overview",
  "/organizations": "overview",
  "/connect-client": "connect",
  "/invitations/accept": "invitation",
  "/instance": "instance",
  "/instance/usage": "instance",
};
export function resolveRoute(url: URL) {
  let page =
    (Object.keys(pagePaths) as Page[]).find(
      (key) => pagePaths[key] === url.pathname,
    ) ?? aliases[url.pathname];
  if (Object.values(operatorPaths).some((path) => path === url.pathname))
    page = "instance";
  const standalone = ["device", "invitation", "ownership", "sign-in"].includes(
    page ?? "",
  );
  // Older links used ?page= on whichever pathname the browser was visiting.
  const requestedPage = url.searchParams.get("page");
  const legacy =
    requestedPage &&
    Object.hasOwn(pagePaths, requestedPage) &&
    !["device", "invitation", "ownership", "sign-in"].includes(requestedPage)
      ? requestedPage
      : null;
  if (page && !standalone && legacy && Object.hasOwn(pagePaths, legacy))
    page = legacy as Page;
  if (!page) return undefined;
  const canonical = new URL(url);
  canonical.pathname =
    page === "instance" && !legacy
      ? operatorPaths[operatorSection(url.pathname)]
      : pagePaths[page];
  canonical.searchParams.delete("page");
  const organization =
    url.searchParams.get("organization") ??
    url.searchParams.get("organization_id") ??
    url.searchParams.get("org");
  if (!standalone) {
    canonical.searchParams.delete("organization_id");
    canonical.searchParams.delete("org");
    if (organization !== null)
      canonical.searchParams.set("organization", organization);
  }
  return { page, url: canonical };
}
