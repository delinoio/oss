// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { claudeLoginURL } from "./claude-subscription-login";

// Claude sign-in is server-owned (see claude-server-login.test.tsx); this file
// keeps the pure authorization-profile validator contract.
const authorization = new URL("https://claude.com/cai/oauth/authorize");
for (const [key, value] of Object.entries({
  client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  response_type: "code",
  redirect_uri: "https://platform.claude.com/oauth/code/callback",
  scope: "user:profile user:inference user:sessions:claude_code",
  code_challenge: "a".repeat(43),
  code_challenge_method: "S256",
  state: "b".repeat(32),
  code: "true",
}))
  authorization.searchParams.set(key, value);
const originalURL = authorization.href;

it("rejects a substituted browser profile", () => {
  expect(claudeLoginURL(originalURL)).toBe(true);
  expect(claudeLoginURL(originalURL + "&state=replacement-state-value")).toBe(
    false,
  );
  expect(
    claudeLoginURL(originalURL.replace("claude.com", "other.invalid")),
  ).toBe(false);
});
