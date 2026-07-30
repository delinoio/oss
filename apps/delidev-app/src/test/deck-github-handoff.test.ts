import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  deckReturnPathStorageKey,
  handoffDeckGitHubAuthorization,
  isValidDeckGitHubAuthorizationTarget,
} from "../deck/githubHandoff";

const configuration = {
  githubAppClientId: "Iv1.fixture-client",
  githubAppSlug: "deli-dev-deck",
  githubCallbackUri: "https://deck.deli.dev/github/oauth/callback",
};
const state = "abcdefghijklmnopqrstuvwxyz0123456789";

describe("Deck GitHub.com authorization handoff", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
  });

  it("consumes an exact configured App installation target without persisting it", () => {
    const navigate = vi.fn();
    const target =
      `https://github.com/apps/deli-dev-deck/installations/new?state=${state}`;

    handoffDeckGitHubAuthorization(
      target,
      "/o/acme/settings",
      configuration,
      navigate,
    );

    expect(navigate).toHaveBeenCalledWith(target);
    expect(sessionStorage.getItem(deckReturnPathStorageKey)).toBe(
      "/o/acme/settings",
    );
    expect(JSON.stringify(sessionStorage)).not.toContain(state);
    expect(JSON.stringify(sessionStorage)).not.toContain(target);
    expect(localStorage.length).toBe(0);
  });

  it("accepts only the exact OAuth client and callback", () => {
    const target = new URL("https://github.com/login/oauth/authorize");
    target.searchParams.set("client_id", configuration.githubAppClientId);
    target.searchParams.set(
      "redirect_uri",
      configuration.githubCallbackUri,
    );
    target.searchParams.set("state", state);

    expect(
      isValidDeckGitHubAuthorizationTarget(target.href, configuration),
    ).toBe(true);
    target.searchParams.set("client_id", "substituted-client");
    expect(
      isValidDeckGitHubAuthorizationTarget(target.href, configuration),
    ).toBe(false);
  });

  it.each([
    `https://ghe.example.com/apps/deli-dev-deck/installations/new?state=${state}`,
    `https://github.com.evil.test/apps/deli-dev-deck/installations/new?state=${state}`,
    `https://user@github.com/apps/deli-dev-deck/installations/new?state=${state}`,
    `https://github.com:444/apps/deli-dev-deck/installations/new?state=${state}`,
    `https://github.com/apps/deli-dev-deck/installations/new?state=${state}#fragment`,
    `https://github.com/apps/deli-dev-deck/%2e%2e/installations/new?state=${state}`,
    `https://github.com/apps/deli-dev-deck%2finstallations/new?state=${state}`,
    `https://github.com/apps/another-app/installations/new?state=${state}`,
  ])("rejects GHES and unsafe target %s", (target) => {
    const navigate = vi.fn();

    expect(() =>
      handoffDeckGitHubAuthorization(
        target,
        "/account",
        configuration,
        navigate,
      ),
    ).toThrow("invalid GitHub.com authorization handoff");
    expect(navigate).not.toHaveBeenCalled();
    expect(sessionStorage.length).toBe(0);
  });
});
