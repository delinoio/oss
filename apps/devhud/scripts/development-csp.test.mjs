import assert from "node:assert/strict";
import test from "node:test";

import { hasExactCspDirectiveSources } from "./frontend-output-policy.mjs";
import {
  createDevHudDevelopmentCsp,
  developmentLogtoOrigin,
} from "./development-csp.mjs";

test("limits development connections to the fixed APIs, exact issuer origin, and HMR", () => {
  const policy = createDevHudDevelopmentCsp("https://auth.delino.io/oidc");
  assert.equal(
    hasExactCspDirectiveSources(policy, "connect-src", [
      "'self'",
      "http://127.0.0.1:46307",
      "https://devhud.api.delino.io",
      "https://api.github.com",
      "https://auth.delino.io",
      "ws://127.0.0.1:46305",
    ]),
    true,
  );
  assert.equal(
    hasExactCspDirectiveSources(policy, "img-src", [
      "'self'",
      "data:",
      "realqa:",
      "http://realqa.localhost",
    ]),
    true,
  );
  assert.doesNotMatch(policy, /\/oidc/u);
  assert.doesNotMatch(policy, /(?:^|\s)(?:https:|http:)(?:\s|$)/u);
  assert.doesNotMatch(policy, /\*/u);
});

test("accepts the fixed OSS loopback issuer and deduplicates a fixed API issuer", () => {
  assert.equal(
    developmentLogtoOrigin("http://localhost:3001/oidc"),
    "http://localhost:3001",
  );
  const policy = createDevHudDevelopmentCsp("https://devhud.api.delino.io/oidc");
  assert.equal(policy.match(/https:\/\/devhud\.api\.delino\.io/gu)?.length, 1);
});

test("rejects untrusted development issuer spellings", () => {
  for (const value of [
    "",
    " https://auth.example.test/oidc",
    "http://auth.example.test/oidc",
    "http://127.1/oidc",
    "http://0x7f000001/oidc",
    "https://user@auth.example.test/oidc",
    "https://auth%2eexample.test/oidc",
    "https://auth.example.test/oidc?tenant=one",
    "https://auth.example.test/oidc#fragment",
  ]) {
    assert.throws(() => developmentLogtoOrigin(value), /issuer/u, value);
  }
});
