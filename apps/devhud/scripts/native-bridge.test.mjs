import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, isAuthCallback, validateExternalRequest, validateSecretValue, validateSecureSettingRef } from "../src/native-bridge.ts";

const fixtures = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../fixtures/deep-links.json"), "utf8"));

test("deep-link fixtures accept only the contracted auth callback", () => {
  for (const candidate of fixtures.accepted) assert.equal(isAuthCallback(candidate), true, candidate);
  for (const candidate of fixtures.rejected) assert.equal(isAuthCallback(candidate), false, candidate);
});

test("secure setting references and values are bounded before native invocation", () => {
  assert.doesNotThrow(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "work-profile" }));
  assert.throws(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "../escape" }), (error) => error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.InvalidArgument);
  assert.throws(() => validateSecretValue("x".repeat(65 * 1024)), NativeBridgeError);
});

test("external navigation is restricted to account destinations", () => {
  assert.doesNotThrow(() => validateExternalRequest({ target: "authentication", apiOrigin: "https://api.delino.io/" }));
  assert.doesNotThrow(() => validateExternalRequest({ target: "authentication", apiOrigin: "http://127.0.0.1:8787/" }));
  assert.doesNotThrow(() => validateExternalRequest({ target: "pat", apiOrigin: "ignored" }));
  assert.throws(() => validateExternalRequest({ target: "authentication", apiOrigin: "http://example.com/" }), NativeBridgeError);
  assert.throws(() => validateExternalRequest({ target: "authentication", apiOrigin: "https://user@example.com/" }), NativeBridgeError);
});
