import { createClient } from "@connectrpc/connect";
import { RealQATrackerService } from "@delinoio/devhud-realqa-connect";
import { describe, expect, it, vi } from "vitest";

import { createRealQAAuthenticatedTransport } from "../api/transports";
import type { RealQAConfig } from "../config";
import {
  isRealQAGitHubAuthorizationTarget,
  navigateToRealQAGitHubAuthorization,
} from "../utils/realqaGitHub";

const config: RealQAConfig = {
  apiOrigin: "https://realqa.deli.dev",
  audience: "https://realqa.deli.dev",
  githubAppClientId: "fixture-client",
  githubAppSlug: "fixture-realqa",
  githubCallbackUri: "https://realqa.deli.dev/github/oauth/callback",
  issues: [],
};
const state = "abcdefghijklmnopqrstuvwxyz123456";

describe("RealQA GitHub authorization guard", () => {
  it.each([
    `https://github.example.com/login/oauth/authorize?client_id=fixture-client&state=${state}`,
    `https://ghe.example.com/login/oauth/authorize?client_id=fixture-client&state=${state}`,
    `http://github.com/login/oauth/authorize?client_id=fixture-client&state=${state}`,
    `https://github.com:8443/login/oauth/authorize?client_id=fixture-client&state=${state}`,
    `https://user@github.com/login/oauth/authorize?client_id=fixture-client&state=${state}`,
    `https://github.com/login/oauth/authorize?client_id=wrong&state=${state}`,
    `https://github.com/login/oauth/authorize?client_id=fixture-client&state=${state}#token`,
    `https://github.com/%2e%2e/login/oauth/authorize?client_id=fixture-client&state=${state}`,
    `https://github.com/apps/another-app/installations/new?state=${state}`,
    `https://github.com/apps/fixture-realqa/installations/new?state=${state}&target=other`,
  ])("rejects GHES and unsafe target %s", (target) => {
    expect(isRealQAGitHubAuthorizationTarget(target, config)).toBe(false);
    expect(() =>
      navigateToRealQAGitHubAuthorization(target, config, vi.fn()),
    ).toThrow("invalid GitHub.com authorization target");
  });

  it("allows only the configured OAuth and GitHub App installation shapes", () => {
    expect(
      isRealQAGitHubAuthorizationTarget(
        `https://github.com/login/oauth/authorize?client_id=fixture-client&state=${state}`,
        config,
      ),
    ).toBe(true);
    expect(
      isRealQAGitHubAuthorizationTarget(
        `https://github.com/login/oauth/authorize?client_id=fixture-client&redirect_uri=${encodeURIComponent(config.githubCallbackUri)}&state=${state}`,
        config,
      ),
    ).toBe(true);
    expect(
      isRealQAGitHubAuthorizationTarget(
        `https://github.com/apps/fixture-realqa/installations/new?state=${state}`,
        config,
      ),
    ).toBe(true);
  });
});

describe("RealQA authenticated transport", () => {
  it("adds both audience tokens only to the network request", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const headers = new Headers(
        init?.headers ?? (request instanceof Request ? request.headers : {}),
      );
      expect(headers.get("authorization")).toBe("Bearer realqa-token");
      expect(headers.get("x-delibase-forwarded-user-token")).toBe(
        "delibase-token",
      );
      expect(headers.get("cache-control")).toBe("no-store");
      return new Response(JSON.stringify({ connection: {} }), {
        headers: { "content-type": "application/json" },
      });
    });
    const tokenCalls: string[] = [];
    const transport = createRealQAAuthenticatedTransport({
      audience: "https://realqa.deli.dev",
      baseUrl: "https://realqa.deli.dev",
      delibaseAudience: "https://delibase.deli.dev",
      fetch: fetchMock,
      getAccessToken: async (audience) => {
        tokenCalls.push(audience);
        return audience.includes("realqa")
          ? "realqa-token"
          : "delibase-token";
      },
    });

    const client = createClient(RealQATrackerService, transport);
    await client.getGitHubConnection({});

    expect(tokenCalls).toEqual([
      "https://realqa.deli.dev",
      "https://delibase.deli.dev",
    ]);
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
