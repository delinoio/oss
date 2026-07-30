import { describe, expect, it } from "vitest";

import {
  canonicalAudience,
  canonicalRealQAAudience,
  readRuntimeConfig,
} from "../config";

describe("runtime configuration", () => {
  it("accepts only the canonical audience and HTTPS public origins", () => {
    const valid = readRuntimeConfig(
      {
        PUBLIC_DELIBASE_API_ORIGIN: canonicalAudience,
        PUBLIC_LOGTO_APP_ID: "spa-id",
        PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
        PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
      },
      "https://deli.dev",
    );
    expect(valid.issues).toEqual([]);
    expect(valid.realqa.issues).toHaveLength(5);

    const realqa = readRuntimeConfig(
      {
        PUBLIC_DELIBASE_API_ORIGIN: canonicalAudience,
        PUBLIC_LOGTO_APP_ID: "spa-id",
        PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
        PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
        PUBLIC_REALQA_API_ORIGIN: canonicalRealQAAudience,
        PUBLIC_REALQA_GITHUB_APP_CLIENT_ID: "fixture-client",
        PUBLIC_REALQA_GITHUB_APP_SLUG: "fixture-realqa",
        PUBLIC_REALQA_GITHUB_CALLBACK_URI:
          "https://realqa.deli.dev/github/oauth/callback",
        PUBLIC_REALQA_LOGTO_AUDIENCE: canonicalRealQAAudience,
      },
      "https://deli.dev",
    );
    expect(realqa.realqa.issues).toEqual([]);

    const ghes = readRuntimeConfig(
      {
        PUBLIC_DELIBASE_API_ORIGIN: canonicalAudience,
        PUBLIC_LOGTO_APP_ID: "spa-id",
        PUBLIC_LOGTO_AUDIENCE: canonicalAudience,
        PUBLIC_LOGTO_ENDPOINT: "https://tenant.logto.app",
        PUBLIC_REALQA_API_ORIGIN: "https://ghe.example.com",
        PUBLIC_REALQA_GITHUB_APP_CLIENT_ID: "fixture-client",
        PUBLIC_REALQA_GITHUB_APP_SLUG: "fixture-realqa",
        PUBLIC_REALQA_GITHUB_CALLBACK_URI:
          "https://ghe.example.com/github/oauth/callback",
        PUBLIC_REALQA_LOGTO_AUDIENCE: "https://ghe.example.com",
      },
      "https://deli.dev",
    );
    expect(ghes.realqa.issues).toHaveLength(3);

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
  });
});
