import { describe, expect, it } from "vitest";

import { canonicalAudience, readRuntimeConfig } from "../config";

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
