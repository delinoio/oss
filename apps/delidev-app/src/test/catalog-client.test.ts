import { createClient } from "@connectrpc/connect";
import {
  AccountService,
  CatalogService,
} from "@delinoio/delibase-connect";
import {
  DeckIntegrationService,
  DeckViewService,
  OwnerScope,
} from "@delinoio/devhud-deck-connect";
import { describe, expect, it, vi } from "vitest";

import {
  createAuthenticatedTransport,
  createDeckIntegrationTransport,
  createPublicTransport,
} from "../api/transports";
import {
  canonicalAudience,
  canonicalDeckAudience,
} from "../config";

function connectJsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status: 200,
  });
}

describe("delibase browser transports", () => {
  it("lists public catalog data without requesting or sending a token", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const headers = new Headers(
        init?.headers ??
          (request instanceof Request ? request.headers : undefined),
      );
      expect(headers.has("authorization")).toBe(false);
      expect(String(request)).toContain(
        "/delibase.v1.CatalogService/ListCatalogApps",
      );
      return connectJsonResponse({
        apps: [
          {
            appId: { value: "01912345-0000-7000-8000-000000000001" },
            enabled: true,
            name: "JSON Lens",
            slug: "json-lens",
            summary: "Inspect JSON",
          },
        ],
      });
    });
    const client = createClient(
      CatalogService,
      createPublicTransport({
        baseUrl: "https://delibase.deli.dev",
        fetch: fetchMock,
      }),
    );

    const response = await client.listCatalogApps({});
    expect(response.apps[0]?.slug).toBe("json-lens");
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("fails closed before public requests can reach a non-canonical origin", async () => {
    const fetchMock = vi.fn<typeof fetch>();
    const client = createClient(
      CatalogService,
      createPublicTransport({
        baseUrl: "https://typo.example",
        fetch: fetchMock,
      }),
    );

    await expect(client.listCatalogApps({})).rejects.toThrow(
      `PUBLIC_DELIBASE_API_ORIGIN=${canonicalAudience}`,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("fails closed when another required public setting is invalid", async () => {
    const fetchMock = vi.fn<typeof fetch>();
    const client = createClient(
      CatalogService,
      createPublicTransport({
        baseUrl: canonicalAudience,
        configurationValid: false,
        fetch: fetchMock,
      }),
    );

    await expect(client.listCatalogApps({})).rejects.toThrow(
      "valid public configuration",
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("requests the canonical Logto audience for protected calls", async () => {
    const tokenGetter = vi.fn(async () => "test-access-token");
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const headers = new Headers(
        init?.headers ??
          (request instanceof Request ? request.headers : undefined),
      );
      expect(headers.get("authorization")).toBe("Bearer test-access-token");
      expect(headers.get("cache-control")).toBe("no-store");
      return connectJsonResponse({
        onboardingRequired: true,
        organizations: [],
      });
    });
    const client = createClient(
      AccountService,
      createAuthenticatedTransport({
        audience: canonicalAudience,
        baseUrl: "https://delibase.deli.dev",
        fetch: fetchMock,
        getAccessToken: tokenGetter,
      }),
    );

    const response = await client.getAccountState({});
    expect(response.onboardingRequired).toBe(true);
    expect(tokenGetter).toHaveBeenCalledWith(canonicalAudience);
  });

  it("keeps both Deck credentials request-only and restricts procedures", async () => {
    localStorage.clear();
    sessionStorage.clear();
    const tokenGetter = vi.fn(async (audience: string) =>
      audience === canonicalDeckAudience
        ? "deck-access-token"
        : "delibase-access-token",
    );
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const headers = new Headers(
        init?.headers ??
          (request instanceof Request ? request.headers : undefined),
      );
      expect(headers.get("authorization")).toBe(
        "Bearer deck-access-token",
      );
      expect(
        headers.get("x-devhud-deck-forwarded-delibase-token"),
      ).toBe("delibase-access-token");
      expect(headers.has("cache-control")).toBe(false);
      return connectJsonResponse({ installations: [] });
    });
    const transport = createDeckIntegrationTransport({
      baseUrl: canonicalDeckAudience,
      fetch: fetchMock,
      getAccessToken: tokenGetter,
    });
    const integrationClient = createClient(
      DeckIntegrationService,
      transport,
    );

    await integrationClient.listGitHubInstallations({
      owner: {
        ownerId: {
          case: "accountId",
          value: { value: "account-id" },
        },
        scope: OwnerScope.PERSONAL,
      },
    });

    expect(tokenGetter).toHaveBeenCalledTimes(2);
    expect(tokenGetter).toHaveBeenCalledWith(canonicalDeckAudience);
    expect(tokenGetter).toHaveBeenCalledWith(canonicalAudience);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);

    const viewClient = createClient(DeckViewService, transport);
    await expect(
      viewClient.listViews({
        owner: {
          ownerId: {
            case: "accountId",
            value: { value: "account-id" },
          },
          scope: OwnerScope.PERSONAL,
        },
      }),
    ).rejects.toThrow(
      "Only the configured Deck integration service is available.",
    );
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
