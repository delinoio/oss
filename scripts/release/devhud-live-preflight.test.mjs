import assert from "node:assert/strict";
import { createHash, generateKeyPairSync } from "node:crypto";
import test from "node:test";

import { googleAssertion, runLivePreflight } from "./devhud-live-preflight.mjs";
import { releaseFingerprintVariables, releaseSecrets } from "./devhud-public-release.mjs";

const { privateKey: googlePrivateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const { privateKey: applePrivateKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });

function environment() {
  const result = Object.fromEntries([...releaseFingerprintVariables, ...releaseSecrets].map((name) => [name, `fixture-${name}`]));
  Object.assign(result, {
    APPLE_API_ISSUER: "fixture-issuer", APPLE_API_KEY_ID: "fixture-key",
    APPLE_API_PRIVATE_KEY_B64: Buffer.from(applePrivateKey.export({ type: "pkcs8", format: "pem" })).toString("base64"),
    DEVHUD_APP_STORE_APP_ID: "123", DEVHUD_GOOGLE_PLAY_PACKAGE_NAME: "io.delino.devhud", DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING: "enabled",
    DEVHUD_GOOGLE_PLAY_PRODUCTION_RELEASE_SERVICE_ACCOUNT: "release@example.test",
    DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON: JSON.stringify({ client_email: "release@example.test", token_uri: "https://oauth2.example.test/token", private_key: googlePrivateKey.export({ type: "pkcs8", format: "pem" }) }),
    DEVHUD_RELEASE_CONTROLLER_URL: "https://controller.example.test/", DEVHUD_RELEASE_CONTROLLER_TOKEN: "controller-token", DEVHUD_PUBLIC_API_URL: "https://devhud.api.delino.io",
    DEVHUD_PUBLIC_DOCS_URL: "https://docs.example.test/devhud", DEVHUD_PUBLIC_ASSET_BASE_URL: "https://assets.example.test",
    DEVHUD_OCI_REGISTRY: "registry.example.test", DEVHUD_OCI_API_REPOSITORY: "devhud/api", DEVHUD_OCI_SWEEPER_REPOSITORY: "devhud/sweeper",
    DEVHUD_OCI_PRODUCTION_PUSH_PRINCIPAL: "fixture-DEVHUD_OCI_REGISTRY_USERNAME",
    DEVHUD_LOGTO_ISSUER: "https://auth.example.test/oidc", DEVHUD_CHROME_EXTENSION_ID: "a".repeat(32),
    DEVHUD_RELEASE_VERSION: "0.1.0", DEVHUD_RELEASE_AUTHORIZATION_REVISION: "a".repeat(40),
    GITHUB_REPOSITORY: "delinoio/oss", GITHUB_SHA: "a".repeat(40), GITHUB_TOKEN: "github-token",
  });
  return result;
}

const jsonResponse = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

function successfulFetch(environment_) {
  return async (input) => {
    const url = String(input);
    if (url.includes("/v1/users?")) return jsonResponse({ data: [] });
    if (url.includes("api.appstoreconnect.apple.com")) return jsonResponse({ data: { attributes: { bundleId: "io.delino.devhud" } } });
    if (url.includes("oauth2.example.test") || url === "https://oauth2.googleapis.com/token") return jsonResponse({ access_token: "fixture-access" });
    if (url.includes("chromewebstore.googleapis.com")) return jsonResponse({ itemId: environment_.DEVHUD_CHROME_EXTENSION_ID, publicKey: environment_.DEVHUD_CHROME_EXTENSION_PUBLIC_KEY });
    if (url.endsWith(".well-known/openid-configuration")) return jsonResponse({ issuer: environment_.DEVHUD_LOGTO_ISSUER, jwks_uri: "https://auth.example.test/jwks" });
    if (url.endsWith("/upload-token")) return jsonResponse({ result: { jwt: "fixture-pages-upload-token" } });
    if (url.includes("/pages/projects/")) return jsonResponse({ result: { production_branch: "main", subdomain: "devhud.pages.dev", domains: [new URL(environment_.DEVHUD_PUBLIC_DOCS_URL).host] } });
    if (url.includes("controller.example.test")) return jsonResponse({
      ok: true,
      project: "devhud",
      version: environment_.DEVHUD_RELEASE_VERSION,
      revision: environment_.DEVHUD_RELEASE_REVISION ?? environment_.GITHUB_SHA,
      authorizationRevision: environment_.DEVHUD_RELEASE_AUTHORIZATION_REVISION ?? environment_.GITHUB_SHA,
      checks: { postgresql: true, r2: true, "public-asset-authority": true, "release-controller": true },
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
  const successful = successfulFetch(env);
  let controllerRequest;
  const fetchImpl = async (input, options) => {
    if (String(input).includes("controller.example.test")) controllerRequest = JSON.parse(options.body);
    return successful(input, options);
  };
  assert.ok(Object.values(await runLivePreflight(env, fetchImpl)).every(Boolean));
  assert.deepEqual(controllerRequest, {
    schemaVersion: 1,
    project: "devhud",
    version: env.DEVHUD_RELEASE_VERSION,
    tag: `devhud@v${env.DEVHUD_RELEASE_VERSION}`,
    revision: env.GITHUB_SHA,
    authorizationRevision: env.DEVHUD_RELEASE_AUTHORIZATION_REVISION,
    publicAssetBaseUrlSha256: createHash("sha256").update(env.DEVHUD_PUBLIC_ASSET_BASE_URL, "utf8").digest("hex"),
  });
});

test("live preflight binds controller identity to the selected historical revision", async () => {
  const env = { ...environment(), DEVHUD_RELEASE_REVISION: "b".repeat(40) };
  const successful = successfulFetch(env);
  let controllerRequest;
  const fetchImpl = async (input, options) => {
    if (String(input).includes("controller.example.test")) controllerRequest = JSON.parse(options.body);
    return successful(input, options);
  };
  await runLivePreflight(env, fetchImpl);
  assert.equal(controllerRequest.revision, env.DEVHUD_RELEASE_REVISION);
  assert.equal(controllerRequest.authorizationRevision, env.DEVHUD_RELEASE_AUTHORIZATION_REVISION);
});

test("live preflight rejects a malformed workflow authorization revision before network access", async () => {
  const env = { ...environment(), DEVHUD_RELEASE_AUTHORIZATION_REVISION: "bad" };
  let requests = 0;
  await assert.rejects(runLivePreflight(env, async () => { requests += 1; return jsonResponse({ ok: true }); }), /workflow authorization revision/u);
  assert.equal(requests, 0);
});

test("live preflight rejects App Store credentials without submission authority", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).includes("/v1/users?")
    ? jsonResponse({ errors: [{ status: "403" }] }, 403)
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /app-store-submission-authority/u);
});

test("live preflight requires the protected Google Play production-release principal before network access", async () => {
  const env = { ...environment(), DEVHUD_GOOGLE_PLAY_PRODUCTION_RELEASE_SERVICE_ACCOUNT: "reader@example.test" };
  let requests = 0;
  await assert.rejects(runLivePreflight(env, async () => { requests += 1; return jsonResponse({ ok: true }); }), /production-release authority prerequisite/u);
  assert.equal(requests, 0);
});

test("live preflight requires the protected OCI push principal before network access", async () => {
  const env = { ...environment(), DEVHUD_OCI_PRODUCTION_PUSH_PRINCIPAL: "read-only-registry-user" };
  let requests = 0;
  await assert.rejects(runLivePreflight(env, async () => { requests += 1; return jsonResponse({ ok: true }); }), /production push authority prerequisite/u);
  assert.equal(requests, 0);
});

test("live preflight fails closed when the controller omits PostgreSQL", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).includes("controller.example.test")
    ? jsonResponse({ ok: true, project: "devhud", version: env.DEVHUD_RELEASE_VERSION, revision: env.GITHUB_SHA, authorizationRevision: env.DEVHUD_RELEASE_AUTHORIZATION_REVISION, checks: { postgresql: false, r2: true, "public-asset-authority": true, "release-controller": true } })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /postgresql/u);
});

test("live preflight fails closed when the controller does not bind its public-asset authority", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).includes("controller.example.test")
    ? jsonResponse({ ok: true, project: "devhud", version: env.DEVHUD_RELEASE_VERSION, revision: env.GITHUB_SHA, authorizationRevision: env.DEVHUD_RELEASE_AUTHORIZATION_REVISION, checks: { postgresql: true, r2: true, "release-controller": true } })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /public-asset-authority/u);
});

test("live preflight rejects every mismatched controller identity field", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const valid = { ok: true, project: "devhud", version: env.DEVHUD_RELEASE_VERSION, revision: env.GITHUB_SHA, authorizationRevision: env.DEVHUD_RELEASE_AUTHORIZATION_REVISION, checks: { postgresql: true, r2: true, "public-asset-authority": true, "release-controller": true } };
  for (const mismatch of [{ ok: false }, { project: "other" }, { version: "9.9.9" }, { revision: "b".repeat(40) }, { authorizationRevision: "b".repeat(40) }]) {
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

test("live preflight proves Cloudflare Pages deployment authority without mutation", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  let projectRequest;
  let pagesRequest;
  const fetchImpl = async (input, options) => {
    const url = String(input);
    if (/\/pages\/projects\/[^/]+$/u.test(url)) projectRequest = { url, options };
    if (url.endsWith("/upload-token")) pagesRequest = { url, options };
    return successful(input, options);
  };
  await runLivePreflight(env, fetchImpl);
  assert.match(projectRequest.url, /\/accounts\/[^/]+\/pages\/projects\/[^/]+$/u);
  assert.equal(projectRequest.options.method, undefined);
  assert.match(pagesRequest.url, /\/accounts\/[^/]+\/pages\/projects\/[^/]+\/upload-token$/u);
  assert.equal(pagesRequest.options.method, undefined);
});

test("live preflight accepts the Pages subdomain as the public docs host", async () => {
  const env = { ...environment(), DEVHUD_PUBLIC_DOCS_URL: "https://devhud.pages.dev/devhud" };
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => /\/pages\/projects\/[^/]+$/u.test(String(input))
    ? jsonResponse({ result: { production_branch: "main", subdomain: "devhud.pages.dev", domains: [] } })
    : successful(input, options);
  assert.ok(Object.values(await runLivePreflight(env, fetchImpl)).every(Boolean));
});

test("live preflight rejects a Pages project whose production branch is not main", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  let uploadTokenRequests = 0;
  const fetchImpl = async (input, options) => {
    const url = String(input);
    if (/\/pages\/projects\/[^/]+$/u.test(url)) return jsonResponse({ result: { production_branch: "preview", subdomain: "devhud.pages.dev", domains: [new URL(env.DEVHUD_PUBLIC_DOCS_URL).host] } });
    if (url.endsWith("/upload-token")) uploadTokenRequests += 1;
    return successful(input, options);
  };
  await assert.rejects(runLivePreflight(env, fetchImpl), /production branch must be main/u);
  assert.equal(uploadTokenRequests, 0);
});

test("live preflight rejects a public docs host outside the Pages project", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  let uploadTokenRequests = 0;
  const fetchImpl = async (input, options) => {
    const url = String(input);
    if (/\/pages\/projects\/[^/]+$/u.test(url)) return jsonResponse({ result: { production_branch: "main", subdomain: "other.pages.dev", domains: ["other.example.test"] } });
    if (url.endsWith("/upload-token")) uploadTokenRequests += 1;
    return successful(input, options);
  };
  await assert.rejects(runLivePreflight(env, fetchImpl), /does not serve the configured public docs host/u);
  assert.equal(uploadTokenRequests, 0);
});

test("live preflight rejects Pages credentials without deployment authority", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).endsWith("/upload-token")
    ? jsonResponse({ errors: [{ status: "403" }] }, 403)
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /public-docs-deployment-authority/u);
});

test("live preflight rejects malformed Pages deployment authority", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).endsWith("/upload-token")
    ? jsonResponse({ result: { jwt: "" } })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /did not return deployment authority/u);
});

test("live preflight rejects a Chrome identity that breaks Native Messaging parity", async () => {
  const env = environment();
  const successful = successfulFetch(env);
  const fetchImpl = async (input, options) => String(input).includes("chromewebstore.googleapis.com")
    ? jsonResponse({ itemId: env.DEVHUD_CHROME_EXTENSION_ID, publicKey: "different-key" })
    : successful(input, options);
  await assert.rejects(runLivePreflight(env, fetchImpl), /Native Messaging/u);
});
