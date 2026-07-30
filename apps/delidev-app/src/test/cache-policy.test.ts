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
    expect(
      classifyCacheRequest(
        {
          method: "POST",
          url: "https://untrusted.example/delibase.v1.CatalogService/ListCatalogApps",
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
    "BillingService/CreateSubscriptionCheckout",
    "BillingService/CreateBillingPortalSession",
    "BillingService/UpdateOverageLimit",
    "BillingService/GetBackgroundUsageAuthorization",
    "BillingService/ListBackgroundUsageAuthorizations",
    "BillingService/CreateBackgroundUsageAuthorization",
    "BillingService/RevokeBackgroundUsageAuthorization",
    "UsageService/ReserveUsage",
    "UsageService/CommitUsage",
    "UsageService/ReleaseUsage",
    "RealQATrackerService/GetGitHubConnection",
    "RealQATrackerService/StartGitHubConnection",
    "RealQATrackerService/ListGitHubInstallations",
    "RealQATrackerService/DisconnectGitHubConnection",
    "RealQATrackerService/ListRepositories",
    "RealQATrackerService/GetRepositoryIssueSchema",
  ])("never caches sensitive RPC %s", (rpc) => {
    const realqa = rpc.startsWith("RealQA");
    expect(
      classifyCacheRequest(
        {
          method: "POST",
          url: realqa
            ? `https://realqa.deli.dev/devhud.realqa.v1.${rpc}`
            : `https://delibase.deli.dev/delibase.v1.${rpc}`,
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.None);
  });

  it.each([
    "GetGitHubConnection",
    "StartGitHubConnection",
    "ListGitHubInstallations",
    "DisconnectGitHubConnection",
  ])("never caches Deck integration RPC %s", (method) => {
    expect(
      classifyCacheRequest(
        {
          authorization: "Bearer deck-token",
          method: "POST",
          url: `https://deck.deli.dev/devhud.deck.v1.DeckIntegrationService/${method}`,
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
          url: "https://deli.dev/auth/devhud/callback?code=sensitive",
        },
        shellPaths,
      ),
    ).toBe(CacheTarget.None);
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
