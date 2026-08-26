import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import test from "node:test";

import { googleAssertion, runLivePreflight } from "./devhud-live-preflight.mjs";
import { releaseSecrets, releaseVariables } from "./devhud-public-release.mjs";
import { signingInputs } from "./devhud-release.mjs";

const { privateKey: googlePrivateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const { privateKey: applePrivateKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });

function environment() {
  const result = Object.fromEntries([...releaseVariables, ...releaseSecrets, ...signingInputs].map((name) => [name, `fixture-${name}`]));
  Object.assign(result, {
    APPLE_API_ISSUER: "fixture-issuer", APPLE_API_KEY_ID: "fixture-key",
    APPLE_API_PRIVATE_KEY_B64: Buffer.from(applePrivateKey.export({ type: "pkcs8", format: "pem" })).toString("base64"),
    DEVHUD_APP_STORE_APP_ID: "123", DEVHUD_GOOGLE_PLAY_PACKAGE_NAME: "io.delino.devhud", DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING: "enabled",
    DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON: JSON.stringify({ client_email: "release@example.test", token_uri: "https://oauth2.example.test/token", private_key: googlePrivateKey.export({ type: "pkcs8", format: "pem" }) }),
    DEVHUD_RELEASE_CONTROLLER_URL: "https://controller.example.test/", DEVHUD_RELEASE_CONTROLLER_TOKEN: "controller-token", DEVHUD_PUBLIC_API_URL: "https://api.example.test",
    DEVHUD_PUBLIC_DOCS_URL: "https://docs.example.test/devhud", DEVHUD_PUBLIC_ASSET_BASE_URL: "https://assets.example.test",
    DEVHUD_OCI_REGISTRY: "registry.example.test", DEVHUD_OCI_API_REPOSITORY: "devhud/api", DEVHUD_OCI_SWEEPER_REPOSITORY: "devhud/sweeper",
    DEVHUD_LOGTO_ISSUER: "https://auth.example.test/oidc", DEVHUD_CHROME_EXTENSION_ID: "a".repeat(32),
    DEVHUD_RELEASE_VERSION: "0.1.0", GITHUB_REPOSITORY: "delinoio/oss", GITHUB_SHA: "a".repeat(40), GITHUB_TOKEN: "github-token",
  });
  return result;
}

const jsonResponse = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

function successfulFetch(environment_) {
  return async (input) => {
    const url = String(input);
    if (url.includes("api.appstoreconnect.apple.com")) return jsonResponse({ data: { attributes: { bundleId: "io.delino.devhud" } } });
    if (url.includes("oauth2.example.test") || url === "https://oauth2.googleapis.com/token") return jsonResponse({ access_token: "fixture-access" });
    if (url.includes("chromewebstore.googleapis.com")) return jsonResponse({ itemId: environment_.DEVHUD_CHROME_EXTENSION_ID, publicKey: environment_.DEVHUD_CHROME_EXTENSION_PUBLIC_KEY });
    if (url.endsWith(".well-known/openid-configuration")) return jsonResponse({ issuer: environment_.DEVHUD_LOGTO_ISSUER, jwks_uri: "https://auth.example.test/jwks" });
    if (url.includes("controller.example.test")) return jsonResponse({
      ok: true,
      project: "devhud",
      version: environment_.DEVHUD_RELEASE_VERSION,
      revision: environment_.GITHUB_SHA,
      checks: { postgresql: true, r2: true, "release-controller": true },
    });
    return jsonResponse({ ok: true });
  };
}

test("Google service-account assertion is a bounded signed JWT", () => {
  const serviceAccount = JSON.parse(environment().DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON);
  const assertion = googleAssertion(serviceAccount, 1_700_000_000);
  assert.equal(assertion.split(".").length, 3);
  const payload = JSON.parse(Buffer.from(assertion.split(".")[1], "base64url"));
  assert.equal(payload.exp - payload.iat, 600);
});

test("live preflight composes every independent read-only check", async () => {
  const env = environment();
  assert.ok(Object.values(await runLivePreflight(env, successfulFetch(env))).every(Boolean));
});

test("live preflight fails closed when the controller omits PostgreSQL", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).includes("controller.example.test")
    ? jsonResponse({ ok: true, project: "devhud", version: env.DEVHUD_RELEASE_VERSION, revision: env.GITHUB_SHA, checks: { postgresql: false, r2: true, "release-controller": true } })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /postgresql/u);
});

test("live preflight rejects every mismatched controller identity field", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const valid = { ok: true, project: "devhud", version: env.DEVHUD_RELEASE_VERSION, revision: env.GITHUB_SHA, checks: { postgresql: true, r2: true, "release-controller": true } };
  for (const mismatch of [{ ok: false }, { project: "other" }, { version: "9.9.9" }, { revision: "b".repeat(40) }]) {
    const fetchImpl = async (input, options) => String(input).includes("controller.example.test")
      ? jsonResponse({ ...valid, ...mismatch })
      : successful(input, options);
    await assert.rejects(runLivePreflight(env, fetchImpl), /mismatched release state/u);
  }
});

test("live preflight probes a generated asset route and accepts an empty origin", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  let assetRequest;
  const fetchImpl = async (input, options) => {
    const url = String(input);
    if (url.startsWith(env.DEVHUD_PUBLIC_ASSET_BASE_URL)) {
      assetRequest = { url, options };
      return new Response(null, { status: 404 });
    }
    return successful(input, options);
  };
  await runLivePreflight(env, fetchImpl);
  assert.match(assetRequest.url, /\/[A]{43}\.png$/u);
  assert.equal(assetRequest.options.method, "HEAD");
});

test("live preflight rejects an unavailable asset boundary", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).startsWith(env.DEVHUD_PUBLIC_ASSET_BASE_URL)
    ? new Response(null, { status: 503 })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /asset-domain/u);
});

test("live preflight rejects a Chrome identity that breaks Native Messaging parity", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).includes("chromewebstore.googleapis.com")
    ? jsonResponse({ itemId: env.DEVHUD_CHROME_EXTENSION_ID, publicKey: "different-key" })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /Native Messaging/u);
});
