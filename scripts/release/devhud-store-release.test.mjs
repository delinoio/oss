import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { StoreBuildStatus, StoreProvider, StoreStatus, classifyApple, classifyChrome, classifyGoogle, run } from "./devhud-store-release.mjs";

const source = readFileSync(fileURLToPath(new URL("devhud-store-release.mjs", import.meta.url)), "utf8");
const { privateKey: applePrivateKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });
const { privateKey: googlePrivateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });

function environment() {
  return {
    APPLE_API_ISSUER: "fixture-issuer",
    APPLE_API_KEY_ID: "fixture-key",
    APPLE_API_PRIVATE_KEY_B64: Buffer.from(applePrivateKey.export({ type: "pkcs8", format: "pem" })).toString("base64"),
    DEVHUD_APP_STORE_APP_ID: "123",
    DEVHUD_GOOGLE_PLAY_PACKAGE_NAME: "io.delino.devhud",
    DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON: JSON.stringify({ client_email: "release@example.test", token_uri: "https://oauth2.example.test/token", private_key: googlePrivateKey.export({ type: "pkcs8", format: "pem" }) }),
    DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID: "publisher",
    DEVHUD_CHROME_EXTENSION_ID: "a".repeat(32),
    DEVHUD_CHROME_WEB_STORE_CLIENT_ID: "client",
    DEVHUD_CHROME_WEB_STORE_CLIENT_SECRET: "secret",
    DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN: "refresh",
  };
}

const jsonResponse = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

test("store states distinguish unsubmitted, pending, approved-held, public, and terminal results", () => {
  assert.equal(classifyApple("PREPARE_FOR_SUBMISSION"), StoreStatus.Unsubmitted);
  assert.equal(classifyApple("READY_FOR_REVIEW"), StoreStatus.Unsubmitted);
  assert.equal(classifyApple("WAITING_FOR_REVIEW"), StoreStatus.Pending);
  assert.equal(classifyApple("PENDING_DEVELOPER_RELEASE"), StoreStatus.ApprovedHeld);
  assert.equal(classifyApple("READY_FOR_SALE"), StoreStatus.Public);
  assert.equal(classifyApple("REJECTED"), StoreStatus.Rejected);
  assert.equal(classifyApple("DEVELOPER_REJECTED"), StoreStatus.Withdrawn);
  assert.equal(classifyGoogle("RELEASE_LIFECYCLE_STATE_IN_REVIEW"), StoreStatus.Pending);
  assert.equal(classifyGoogle("RELEASE_LIFECYCLE_STATE_APPROVED_NOT_PUBLISHED"), StoreStatus.ApprovedHeld);
  assert.equal(classifyGoogle("RELEASE_LIFECYCLE_STATE_PUBLISHED"), StoreStatus.Public);
  assert.equal(classifyGoogle("RELEASE_LIFECYCLE_STATE_NOT_APPROVED"), StoreStatus.Rejected);
  assert.equal(classifyGoogle("RELEASE_LIFECYCLE_STATE_NOT_SENT_FOR_REVIEW"), StoreStatus.Withdrawn);
});

test("Chrome requires the exact version at 100 percent before public", () => {
  assert.equal(classifyChrome({ submitted: undefined, published: undefined, version: "0.1.0" }), StoreStatus.Unsubmitted);
  const pending = classifyChrome({ submitted: { state: "PENDING_REVIEW", distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] }, published: {}, version: "0.1.0" });
  assert.equal(pending, StoreStatus.Pending);
  const approved = classifyChrome({ submitted: { state: "STAGED", distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] }, published: {}, version: "0.1.0" });
  assert.equal(approved, StoreStatus.ApprovedHeld);
  const wrongHeldVersion = classifyChrome({ submitted: { state: "STAGED", distributionChannels: [{ crxVersion: "0.0.9", deployPercentage: 100 }] }, published: {}, version: "0.1.0" });
  assert.equal(wrongHeldVersion, StoreStatus.Unsubmitted);
  const partial = classifyChrome({ submitted: {}, published: { distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 50 }] }, version: "0.1.0" });
  assert.equal(partial, StoreStatus.Pending);
  const complete = classifyChrome({ submitted: {}, published: { distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] }, version: "0.1.0" });
  assert.equal(complete, StoreStatus.Public);
  assert.equal(classifyChrome({ submitted: { state: "CANCELLED" }, published: {}, version: "0.1.0" }), StoreStatus.Withdrawn);
});

test("App Store review uses manual release and the dedicated build linkage endpoint", () => {
  assert.match(source, /attributes: \{ releaseType: "MANUAL" \}/u);
  assert.match(source, /appStoreVersions\/\$\{version\.id\}\/relationships\/build/u);
  assert.match(source, /appStoreVersionReleaseRequests/u);
  assert.match(source, /appStoreVersionPhasedRelease/u);
});

test("App Store build processing is polled without mutation", async () => {
  const requests = [];
  const fetchImpl = async (input, options = {}) => {
    requests.push({ url: String(input), options });
    return jsonResponse({ data: [{ id: "build-id", attributes: { processingState: "VALID" } }] });
  };
  const result = await run("build-status", StoreProvider.Apple, {}, environment(), fetchImpl);
  assert.equal(result.status, StoreBuildStatus.Processed);
  assert.equal(result.buildId, "build-id");
  assert.ok(requests.every(({ options }) => options.method === undefined));
});

test("App Store build polling distinguishes an absent build from one still processing", async () => {
  for (const [data, expected] of [
    [[], StoreBuildStatus.Absent],
    [[{ id: "build-id", attributes: { processingState: "PROCESSING" } }], StoreBuildStatus.Processing],
  ]) {
    const requests = [];
    const result = await run("build-status", StoreProvider.Apple, {}, environment(), async (input, options = {}) => {
      requests.push({ url: String(input), options });
      return jsonResponse({ data });
    });
    assert.equal(result.status, expected);
    assert.ok(requests.every(({ options }) => options.method === undefined));
  }
});

test("an absent App Store version is unsubmitted and can be withdrawn without mutation", async () => {
  for (const command of ["status", "withdraw"]) {
    const requests = [];
    const result = await run(command, StoreProvider.Apple, {}, environment(), async (input, options = {}) => {
      requests.push({ url: String(input), options });
      return jsonResponse({ data: [] });
    });
    assert.equal(result.status, command === "status" ? StoreStatus.Unsubmitted : StoreStatus.Withdrawn);
    assert.ok(requests.every(({ options }) => options.method === undefined));
  }
});

test("App Store submission creates one absent exact version after build processing", async () => {
  const requests = [];
  const fetchImpl = async (input, options = {}) => {
    const url = String(input);
    requests.push({ url, options });
    if (url.includes("appStoreVersions?")) return jsonResponse({ data: [] });
    if (url.includes("/v1/builds?")) return jsonResponse({ data: [{ id: "build-id", attributes: { processingState: "VALID" } }] });
    if (url.endsWith("/v1/appStoreVersions") && options.method === "POST") return jsonResponse({ data: { type: "appStoreVersions", id: "version-id" } });
    if (url.endsWith("appStoreVersionPhasedRelease")) return new Response(null, { status: 404 });
    if (url.includes("/reviewSubmissions?")) return jsonResponse({ data: [] });
    return jsonResponse({ data: { id: "mutation-result" } });
  };
  await run("submit", StoreProvider.Apple, {}, environment(), fetchImpl);
  const creations = requests.filter(({ url, options }) => url.endsWith("/v1/appStoreVersions") && options.method === "POST");
  assert.equal(creations.length, 1);
  assert.deepEqual(JSON.parse(creations[0].options.body), {
    data: {
      type: "appStoreVersions",
      attributes: { platform: "IOS", versionString: "0.1.0" },
      relationships: { app: { data: { type: "apps", id: "123" } } },
    },
  });
});

test("App Store version lookup rejects duplicate exact records", async () => {
  await assert.rejects(run("status", StoreProvider.Apple, {}, environment(), async () => jsonResponse({ data: [{ id: "one" }, { id: "two" }] })), /at most one exact version/u);
});

test("App Store submission reconciles an already-submitted exact version without mutation", async () => {
  const requests = [];
  const fetchImpl = async (input, options = {}) => {
    const url = String(input);
    requests.push({ url, options });
    if (url.includes("appStoreVersions?")) return jsonResponse({ data: [{ id: "version-id", attributes: { appStoreState: "WAITING_FOR_REVIEW" } }] });
    if (url.endsWith("appStoreVersionPhasedRelease")) return new Response(null, { status: 404 });
    if (url.includes("/v1/builds?")) return jsonResponse({ data: [{ id: "build-id", attributes: { processingState: "VALID" } }] });
    if (url.includes("/reviewSubmissions?")) return jsonResponse({ data: [{
      id: "submission-id",
      attributes: { state: "WAITING_FOR_REVIEW" },
      relationships: { appStoreVersionForReview: { data: { type: "appStoreVersions", id: "version-id" } }, items: { data: [] } },
    }] });
    throw new Error(`unexpected request: ${url}`);
  };
  const result = await run("submit", StoreProvider.Apple, {}, environment(), fetchImpl);
  assert.equal(result.submissionId, "submission-id");
  assert.ok(requests.every(({ options }) => options.method === undefined));
});

test("App Store submission reuses one unbound draft and creates only its missing item", async () => {
  const requests = [];
  const fetchImpl = async (input, options = {}) => {
    const url = String(input);
    requests.push({ url, options });
    if (url.includes("appStoreVersions?")) return jsonResponse({ data: [{ id: "version-id", attributes: { appStoreState: "PREPARE_FOR_SUBMISSION" } }] });
    if (url.endsWith("appStoreVersionPhasedRelease")) return new Response(null, { status: 404 });
    if (url.includes("/v1/builds?")) return jsonResponse({ data: [{ id: "build-id", attributes: { processingState: "VALID" } }] });
    if (url.includes("/reviewSubmissions?")) return jsonResponse({ data: [{ id: "submission-id", attributes: { state: "READY_FOR_REVIEW" }, relationships: { items: { data: [] } } }] });
    return jsonResponse({ data: { id: "mutation-result" } });
  };
  await run("submit", StoreProvider.Apple, {}, environment(), fetchImpl);
  assert.equal(requests.filter(({ url, options }) => url.endsWith("/v1/reviewSubmissions") && options.method === "POST").length, 0);
  assert.equal(requests.filter(({ url, options }) => url.endsWith("/v1/reviewSubmissionItems") && options.method === "POST").length, 1);
  assert.equal(requests.filter(({ url, options }) => url.endsWith("/reviewSubmissions/submission-id") && options.method === "PATCH").length, 1);
});

test("App Store submission replaces a canceled exact review submission", async () => {
  const requests = [];
  const fetchImpl = async (input, options = {}) => {
    const url = String(input);
    requests.push({ url, options });
    if (url.includes("appStoreVersions?")) return jsonResponse({ data: [{ id: "version-id", attributes: { appStoreState: "DEVELOPER_REJECTED" } }] });
    if (url.endsWith("appStoreVersionPhasedRelease")) return new Response(null, { status: 404 });
    if (url.includes("/v1/builds?")) return jsonResponse({ data: [{ id: "build-id", attributes: { processingState: "VALID" } }] });
    if (url.includes("/reviewSubmissions?")) return jsonResponse({ data: [{
      id: "canceled-submission-id",
      attributes: { state: "CANCELED" },
      relationships: { appStoreVersionForReview: { data: { type: "appStoreVersions", id: "version-id" } }, items: { data: [] } },
    }] });
    if (url.endsWith("/v1/reviewSubmissions") && options.method === "POST") return jsonResponse({ data: { type: "reviewSubmissions", id: "replacement-submission-id" } });
    return jsonResponse({ data: { id: "mutation-result" } });
  };
  const result = await run("submit", StoreProvider.Apple, {}, environment(), fetchImpl);
  assert.equal(result.submissionId, "replacement-submission-id");
  assert.equal(requests.filter(({ url, options }) => url.endsWith("/v1/reviewSubmissions") && options.method === "POST").length, 1);
  assert.equal(requests.filter(({ url }) => url.endsWith("/reviewSubmissions/canceled-submission-id")).length, 0);
});

test("Google status uses the direct release lifecycle endpoint and current response schema", async () => {
  const requests = [];
  const fetchImpl = async (input, options = {}) => {
    requests.push({ url: String(input), options });
    if (String(input) === "https://oauth2.example.test/token") return jsonResponse({ access_token: "google-token" });
    return jsonResponse({ releases: [{ releaseLifecycleState: "RELEASE_LIFECYCLE_STATE_APPROVED_NOT_PUBLISHED", activeArtifacts: [{ versionCode: 1 }] }] });
  };
  const result = await run("status", StoreProvider.GooglePlay, {}, environment(), fetchImpl);
  assert.equal(result.status, StoreStatus.ApprovedHeld);
  assert.equal(requests.at(-1).url, "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/io.delino.devhud/tracks/production/releases");
  assert.equal(requests.at(-1).options.method, undefined);
});

test("store publication skips exact versions that are already public", async () => {
  const appleRequests = [];
  const appleFetch = async (input, options = {}) => {
    const url = String(input);
    appleRequests.push({ url, options });
    if (url.includes("appStoreVersions?")) return jsonResponse({ data: [{ id: "version-id", attributes: { appStoreState: "READY_FOR_SALE" } }] });
    if (url.endsWith("appStoreVersionPhasedRelease")) return new Response(null, { status: 404 });
    throw new Error(`unexpected request: ${url}`);
  };
  const apple = await run("publish", StoreProvider.Apple, {}, environment(), appleFetch);
  assert.equal(apple.status, StoreStatus.Public);
  assert.ok(appleRequests.every(({ options }) => options.method === undefined));

  const chromeRequests = [];
  const chromeFetch = async (input, options = {}) => {
    const url = String(input);
    chromeRequests.push({ url, options });
    if (url === "https://oauth2.googleapis.com/token") return jsonResponse({ access_token: "chrome-token" });
    if (url.endsWith(":fetchStatus")) return jsonResponse({ publishedItemRevisionStatus: { distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] } });
    throw new Error(`unexpected request: ${url}`);
  };
  const chrome = await run("publish", StoreProvider.ChromeWebStore, {}, environment(), chromeFetch);
  assert.equal(chrome.status, StoreStatus.Public);
  assert.equal(chromeRequests.filter(({ url }) => url.endsWith(":publish")).length, 0);
});

test("store publication mutates each exact approved-held version once", async () => {
  const appleRequests = [];
  const appleFetch = async (input, options = {}) => {
    const url = String(input);
    appleRequests.push({ url, options });
    if (url.includes("appStoreVersions?")) return jsonResponse({ data: [{ id: "version-id", attributes: { appStoreState: "PENDING_DEVELOPER_RELEASE" } }] });
    if (url.endsWith("appStoreVersionPhasedRelease")) return new Response(null, { status: 404 });
    if (url.endsWith("appStoreVersionReleaseRequests")) return jsonResponse({ data: { id: "release-id" } });
    throw new Error(`unexpected request: ${url}`);
  };
  assert.equal((await run("publish", StoreProvider.Apple, {}, environment(), appleFetch)).status, StoreStatus.Pending);
  assert.equal(appleRequests.filter(({ url, options }) => url.endsWith("appStoreVersionReleaseRequests") && options.method === "POST").length, 1);

  const chromeRequests = [];
  const chromeFetch = async (input, options = {}) => {
    const url = String(input);
    chromeRequests.push({ url, options });
    if (url === "https://oauth2.googleapis.com/token") return jsonResponse({ access_token: "chrome-token" });
    if (url.endsWith(":fetchStatus")) return jsonResponse({ submittedItemRevisionStatus: { state: "STAGED", distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] } });
    if (url.endsWith(":publish")) return jsonResponse({});
    throw new Error(`unexpected request: ${url}`);
  };
  assert.equal((await run("publish", StoreProvider.ChromeWebStore, {}, environment(), chromeFetch)).status, StoreStatus.Pending);
  const chromePublications = chromeRequests.filter(({ url, options }) => url.endsWith(":publish") && options.method === "POST");
  assert.equal(chromePublications.length, 1);
  assert.deepEqual(JSON.parse(chromePublications[0].options.body), {
    publishType: "DEFAULT_PUBLISH",
    deployInfos: [{ deployPercentage: 100 }],
    blockOnWarnings: true,
  });
});

test("withdrawal cancels the exact Apple submission and current Chrome submission", async () => {
  const appleRequests = [];
  const appleFetch = async (input, options = {}) => {
    appleRequests.push({ url: String(input), options });
    if (String(input).includes("appStoreVersions?")) return jsonResponse({ data: [{ id: "version-id", attributes: { appStoreState: "PENDING_DEVELOPER_RELEASE" } }] });
    if (String(input).includes("/reviewSubmissions?")) return jsonResponse({ data: [{ id: "submission-id", attributes: { state: "COMPLETE" }, relationships: { appStoreVersionForReview: { data: { type: "appStoreVersions", id: "version-id" } }, items: { data: [] } } }] });
    return jsonResponse({ data: { id: "submission-id" } });
  };
  await run("withdraw", StoreProvider.Apple, {}, environment(), appleFetch);
  const applePatch = appleRequests.at(-1);
  assert.equal(applePatch.url, "https://api.appstoreconnect.apple.com/v1/reviewSubmissions/submission-id");
  assert.equal(applePatch.options.method, "PATCH");
  assert.deepEqual(JSON.parse(applePatch.options.body), { data: { type: "reviewSubmissions", id: "submission-id", attributes: { canceled: true } } });

  const chromeRequests = [];
  const chromeFetch = async (input, options = {}) => {
    chromeRequests.push({ url: String(input), options });
    if (String(input) === "https://oauth2.googleapis.com/token") return jsonResponse({ access_token: "chrome-token" });
    if (String(input).endsWith(":fetchStatus")) return jsonResponse({ submittedItemRevisionStatus: { state: "STAGED", distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] } });
    return new Response(null, { status: 204 });
  };
  await run("withdraw", StoreProvider.ChromeWebStore, {}, environment(), chromeFetch);
  assert.match(chromeRequests.at(-1).url, /:cancelSubmission$/u);
  assert.equal(chromeRequests.at(-1).options.method, "POST");
});

test("Google withdrawal fails closed until the protected operator removes the held release", async () => {
  const googleFetch = (release) => async (input) => String(input) === "https://oauth2.example.test/token"
    ? jsonResponse({ access_token: "google-token" })
    : jsonResponse({ releases: release ? [release] : [] });
  const held = { releaseLifecycleState: "RELEASE_LIFECYCLE_STATE_APPROVED_NOT_PUBLISHED", activeArtifacts: [{ versionCode: 1 }] };
  await assert.rejects(run("withdraw", StoreProvider.GooglePlay, {}, environment(), googleFetch(held)), /protected operator gate/u);
  const result = await run("withdraw", StoreProvider.GooglePlay, {}, environment(), googleFetch(null));
  assert.equal(result.status, StoreStatus.Withdrawn);
});
