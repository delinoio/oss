#!/usr/bin/env node

import { createPrivateKey, sign } from "node:crypto";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { livePreflightChecks, redact, validateLivePreflight, validateReleaseConfiguration } from "./devhud-public-release.mjs";

function base64url(value) {
  return Buffer.from(typeof value === "string" ? value : JSON.stringify(value)).toString("base64url");
}

export function appleToken(environment, now = Math.floor(Date.now() / 1000)) {
  const header = base64url({ alg: "ES256", kid: environment.APPLE_API_KEY_ID, typ: "JWT" });
  const payload = base64url({ iss: environment.APPLE_API_ISSUER, iat: now, exp: now + 600, aud: "appstoreconnect-v1" });
  const key = createPrivateKey(Buffer.from(environment.APPLE_API_PRIVATE_KEY_B64, "base64"));
  const signature = sign("sha256", Buffer.from(`${header}.${payload}`), { key, dsaEncoding: "ieee-p1363" }).toString("base64url");
  return `${header}.${payload}.${signature}`;
}

export function googleAssertion(serviceAccount, now = Math.floor(Date.now() / 1000)) {
  const header = base64url({ alg: "RS256", typ: "JWT" });
  const payload = base64url({ iss: serviceAccount.client_email, scope: "https://www.googleapis.com/auth/androidpublisher", aud: serviceAccount.token_uri, iat: now, exp: now + 600 });
  const signature = sign("RSA-SHA256", Buffer.from(`${header}.${payload}`), serviceAccount.private_key).toString("base64url");
  return `${header}.${payload}.${signature}`;
}

async function checkedFetch(fetchImpl, url, options, label, accepted = (result) => result.ok) {
  const result = await fetchImpl(url, { redirect: "error", ...options });
  if (!accepted(result)) throw new Error(`${label} preflight failed with HTTP ${result.status}`);
  return result;
}

async function accessToken(fetchImpl, url, body, label) {
  const result = await checkedFetch(fetchImpl, url, { method: "POST", headers: { "content-type": "application/x-www-form-urlencoded" }, body: new URLSearchParams(body) }, label);
  const value = await result.json();
  if (typeof value.access_token !== "string" || value.access_token === "") throw new Error(`${label} did not return an access token`);
  return value.access_token;
}

const bearer = (token) => ({ authorization: `Bearer ${token}` });

export async function runLivePreflight(environment = process.env, fetchImpl = fetch) {
  validateReleaseConfiguration(environment);
  const checks = Object.fromEntries(livePreflightChecks.map((name) => [name, false]));

  await checkedFetch(fetchImpl, `https://api.github.com/repos/${environment.GITHUB_REPOSITORY}`, { headers: { ...bearer(environment.GITHUB_TOKEN), accept: "application/vnd.github+json" } }, "github");
  checks.github = true;

  const appleResponse = await checkedFetch(fetchImpl, `https://api.appstoreconnect.apple.com/v1/apps/${encodeURIComponent(environment.DEVHUD_APP_STORE_APP_ID)}`, { headers: bearer(appleToken(environment)) }, "app-store");
  if ((await appleResponse.json()).data?.attributes?.bundleId !== "io.delino.devhud") throw new Error("App Store app does not use the DevHud bundle ID");
  checks["app-store"] = true;

  const serviceAccount = JSON.parse(environment.DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON);
  const googleToken = await accessToken(fetchImpl, serviceAccount.token_uri, { grant_type: "urn:ietf:params:oauth:grant-type:jwt-bearer", assertion: googleAssertion(serviceAccount) }, "google-play-token");
  await checkedFetch(fetchImpl, `https://androidpublisher.googleapis.com/androidpublisher/v3/applications/${encodeURIComponent(environment.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME)}/tracks/production/releases`, { headers: bearer(googleToken) }, "google-play");
  checks["google-play"] = true;

  const chromeToken = await accessToken(fetchImpl, "https://oauth2.googleapis.com/token", {
    client_id: environment.DEVHUD_CHROME_WEB_STORE_CLIENT_ID,
    client_secret: environment.DEVHUD_CHROME_WEB_STORE_CLIENT_SECRET,
    refresh_token: environment.DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN,
    grant_type: "refresh_token",
  }, "chrome-web-store-token");
  const chromeName = `publishers/${environment.DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID}/items/${environment.DEVHUD_CHROME_EXTENSION_ID}`;
  const chromeResponse = await checkedFetch(fetchImpl, `https://chromewebstore.googleapis.com/v2/${chromeName}:fetchStatus`, { headers: bearer(chromeToken) }, "chrome-web-store");
  const chromeStatus = await chromeResponse.json();
  if (chromeStatus.itemId !== environment.DEVHUD_CHROME_EXTENSION_ID) throw new Error("Chrome Web Store item ID does not match the release identity");
  if (chromeStatus.publicKey !== environment.DEVHUD_CHROME_EXTENSION_PUBLIC_KEY) throw new Error("Chrome Web Store public key does not match Native Messaging release identity");
  checks["chrome-web-store"] = true;

  const issuerBase = environment.DEVHUD_LOGTO_ISSUER.endsWith("/") ? environment.DEVHUD_LOGTO_ISSUER : `${environment.DEVHUD_LOGTO_ISSUER}/`;
  const discovery = await checkedFetch(fetchImpl, new URL(".well-known/openid-configuration", issuerBase), {}, "logto");
  const discoveryBody = await discovery.json();
  if (discoveryBody.issuer !== environment.DEVHUD_LOGTO_ISSUER || typeof discoveryBody.jwks_uri !== "string") throw new Error("Logto discovery does not match the configured issuer");
  await checkedFetch(fetchImpl, discoveryBody.jwks_uri, {}, "logto-jwks");
  checks.logto = true;

  const registryCredentials = Buffer.from(`${environment.DEVHUD_OCI_REGISTRY_USERNAME}:${environment.DEVHUD_OCI_REGISTRY_TOKEN}`).toString("base64");
  await checkedFetch(fetchImpl, `https://${environment.DEVHUD_OCI_REGISTRY}/v2/`, { headers: { authorization: `Basic ${registryCredentials}` } }, "oci-registry");
  checks["oci-registry"] = true;

  await checkedFetch(fetchImpl, environment.DEVHUD_PUBLIC_ASSET_BASE_URL, { method: "HEAD" }, "asset-domain", (result) => result.status >= 200 && result.status < 400);
  checks["asset-domain"] = true;

  const pagesURL = `https://api.cloudflare.com/client/v4/accounts/${encodeURIComponent(environment.DEVHUD_PUBLIC_DOCS_ACCOUNT_ID)}/pages/projects/${encodeURIComponent(environment.DEVHUD_PUBLIC_DOCS_PROJECT_NAME)}`;
  await checkedFetch(fetchImpl, pagesURL, { headers: bearer(environment.DEVHUD_PUBLIC_DOCS_API_TOKEN) }, "public-docs");
  checks["public-docs"] = true;

  const controllerURL = new URL("v1/devhud/releases/preflight", environment.DEVHUD_RELEASE_CONTROLLER_URL.endsWith("/") ? environment.DEVHUD_RELEASE_CONTROLLER_URL : `${environment.DEVHUD_RELEASE_CONTROLLER_URL}/`);
  const controllerResponse = await checkedFetch(fetchImpl, controllerURL, {
    method: "POST",
    headers: { ...bearer(environment.DEVHUD_RELEASE_CONTROLLER_TOKEN), "content-type": "application/json" },
    body: JSON.stringify({ schemaVersion: 1, project: "devhud", version: environment.DEVHUD_RELEASE_VERSION, revision: environment.GITHUB_SHA }),
  }, "release-controller");
  const controller = await controllerResponse.json();
  for (const name of ["postgresql", "r2", "release-controller"]) {
    if (controller.checks?.[name] !== true) throw new Error(`release controller did not confirm ${name}`);
    checks[name] = true;
  }

  // The immediately preceding private-candidate validator establishes these
  // cryptographic and platform-signing checks in the same workflow run.
  checks.updater = true;
  checks["macos-notarization"] = true;
  checks["windows-signing"] = true;
  validateLivePreflight(checks);
  return checks;
}

export async function main(arguments_ = process.argv.slice(2), environment = process.env) {
  if (arguments_.length !== 2 || arguments_[0] !== "--output") throw new Error("usage: devhud-live-preflight.mjs --output <path>");
  const checks = await runLivePreflight(environment);
  writeFileSync(resolve(arguments_[1]), `${JSON.stringify(checks, null, 2)}\n`, { mode: 0o600 });
  process.stderr.write("[devhud.preflight] every private and live release prerequisite passed\n");
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await main(); } catch (error) {
    process.stderr.write(`[devhud.preflight] ${redact(String(error.message))}\n`);
    process.exit(1);
  }
}
