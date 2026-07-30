import { describe, expect, it } from "vitest";

import {
  canonicalAudience,
  canonicalDeckAudience,
  canonicalDeckGitHubCallbackUri,
  readRuntimeConfig,
} from "../config";

describe("runtime configuration", () => {
  it("accepts only the canonical audience and HTTPS public origins", () => {
    const valid = readRuntimeConfig(
      {
        PUBLIC_DECK_API_ORIGIN: canonicalDeckAudience,
        PUBLIC_DECK_GITHUB_APP_CLIENT_ID: "Iv1.fixture",
        PUBLIC_DECK_GITHUB_APP_SLUG: "deli-dev-deck",
        PUBLIC_DECK_GITHUB_CALLBACK_URI:
          canonicalDeckGitHubCallbackUri,
        PUBLIC_DECK_LOGTO_AUDIENCE: canonicalDeckAudience,
        PUBLIC_DELIBASE_API_ORIGIN: canonicalAudience,
        PUBLIC_LOGTO_APP_ID: "spa-id",
        PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
        PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
      },
      "https://deli.dev",
    );
    expect(valid.issues).toEqual([]);
    expect(valid.deck.issues).toEqual([]);

    const invalid = readRuntimeConfig(
      {
        PUBLIC_DELIBASE_API_ORIGIN: "http://insecure.example",
        PUBLIC_LOGTO_APP_ID: "",
        PUBLIC_LOGTO_AUDIENCE: "https://wrong.example",
        PUBLIC_LOGTO_ENDPOINT: "not-a-url",
      },
      "https://deli.dev",
    );
    expect(invalid.issues).toHaveLength(4);

    const wrongApiOrigin = readRuntimeConfig(
      {
        PUBLIC_DELIBASE_API_ORIGIN: "https://staging.example",
        PUBLIC_LOGTO_APP_ID: "spa-id",
        PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
        PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
      },
      "https://deli.dev",
    );
    expect(wrongApiOrigin.issues).toEqual([
      `PUBLIC_DELIBASE_API_ORIGIN must be ${canonicalAudience}.`,
    ]);
    expect(wrongApiOrigin.deck.issues).toHaveLength(5);
  });

  it("disables only Deck for missing or cross-origin authorization configuration", () => {
    const config = readRuntimeConfig({
      PUBLIC_DECK_API_ORIGIN: "https://github.example.test",
      PUBLIC_DECK_GITHUB_APP_CLIENT_ID: "bad/client",
      PUBLIC_DECK_GITHUB_APP_SLUG: "bad slug",
      PUBLIC_DECK_GITHUB_CALLBACK_URI:
        "https://github.example.test/callback",
      PUBLIC_DECK_LOGTO_AUDIENCE: canonicalAudience,
      PUBLIC_DELIBASE_API_ORIGIN: canonicalAudience,
      PUBLIC_LOGTO_APP_ID: "spa-id",
      PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
      PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
    });

    expect(config.issues).toEqual([]);
    expect(config.deck.issues).toHaveLength(5);
  });

  it("rejects URL-significant GitHub identifiers before enabling Deck", () => {
    const config = readRuntimeConfig({
      PUBLIC_DECK_API_ORIGIN: canonicalDeckAudience,
      PUBLIC_DECK_GITHUB_APP_CLIENT_ID: "Iv1.client&scope=repo",
      PUBLIC_DECK_GITHUB_APP_SLUG: "deck%2finstallations",
      PUBLIC_DECK_GITHUB_CALLBACK_URI:
        canonicalDeckGitHubCallbackUri,
      PUBLIC_DECK_LOGTO_AUDIENCE: canonicalDeckAudience,
      PUBLIC_DELIBASE_API_ORIGIN: canonicalAudience,
      PUBLIC_LOGTO_APP_ID: "spa-id",
      PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
      PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
    });

    expect(config.issues).toEqual([]);
    expect(config.deck.issues).toEqual([
      "PUBLIC_DECK_GITHUB_APP_CLIENT_ID is missing or invalid.",
      "PUBLIC_DECK_GITHUB_APP_SLUG is missing or invalid.",
    ]);
  });
});
