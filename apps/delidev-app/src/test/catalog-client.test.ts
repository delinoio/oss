import { createClient } from "@connectrpc/connect";
import {
  AccountService,
  CatalogService,
} from "@delinoio/delibase-connect";
import { describe, expect, it, vi } from "vitest";

import {
  createAuthenticatedTransport,
  createPublicTransport,
} from "../api/transports";
import { canonicalAudience } from "../config";

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
});
