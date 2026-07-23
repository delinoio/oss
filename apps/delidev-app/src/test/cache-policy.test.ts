import { describe, expect, it } from "vitest";

import {
  CacheTarget,
  classifyCacheRequest,
} from "../pwa/cachePolicy";

const shellPaths = new Set([
  "/",
  "/index.html",
  "/static/js/app.abc123.js",
]);

describe("service worker cache policy", () => {
  it("allows only exact anonymous public catalog RPCs", () => {
    expect(
      classifyCacheRequest(
        {
          method: "POST",
          url: "https://delibase.deli.dev/delibase.v1.CatalogService/ListCatalogApps",
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.PublicCatalog);

    expect(
      classifyCacheRequest(
        {
          authorization: "Bearer secret",
          method: "POST",
          url: "https://delibase.deli.dev/delibase.v1.CatalogService/ListCatalogApps",
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.None);
  });

  it.each([
    "AccountService/GetAccountState",
    "OrganizationService/ListOrganizations",
    "TeamService/ListTeams",
    "BillingService/GetBillingSummary",
    "BillingService/ListLedgerEntries",
    "BillingService/ListUsageRecords",
    "UsageService/ReserveUsage",
  ])("never caches sensitive RPC %s", (rpc) => {
    expect(
      classifyCacheRequest(
        {
          method: "POST",
          url: `https://delibase.deli.dev/delibase.v1.${rpc}`,
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.None);
  });

  it("allows only generated same-origin shell paths", () => {
    expect(
      classifyCacheRequest(
        {
          method: "GET",
          url: "https://deli.dev/static/js/app.abc123.js",
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.StaticShell);
    expect(
      classifyCacheRequest(
        { method: "GET", url: "https://deli.dev/account" },
        shellPaths,
      ),
    ).toBe(CacheTarget.None);
    expect(
      classifyCacheRequest(
        {
          method: "GET",
          url: "https://third-party.example/static/js/app.abc123.js",
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.None);
  });
});
