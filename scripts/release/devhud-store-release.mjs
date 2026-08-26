#!/usr/bin/env node

import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { appleToken, googleAssertion } from "./devhud-live-preflight.mjs";
import { loadReleaseMetadata } from "./devhud-release.mjs";
import { redact } from "./devhud-public-release.mjs";

export const StoreProvider = Object.freeze({ Apple: "apple", GooglePlay: "google-play", ChromeWebStore: "chrome-web-store" });
export const StoreStatus = Object.freeze({ Pending: "pending-review", ApprovedHeld: "approved-held", Public: "public", Rejected: "rejected", Withdrawn: "withdrawn" });
export const StoreBuildStatus = Object.freeze({ Processing: "processing", Processed: "processed" });

const appleSubmittedReviewStates = new Set(["WAITING_FOR_REVIEW", "IN_REVIEW", "COMPLETING", "COMPLETE"]);

async function checked(fetchImpl, url, options, label) {
  const response = await fetchImpl(url, { redirect: "error", ...options });
  if (!response.ok) throw new Error(`${label} failed with HTTP ${response.status}`);
  if (response.status === 204) return {};
  return response.json();
}

const bearer = (token) => ({ authorization: `Bearer ${token}` });
const jsonHeaders = (token) => ({ ...bearer(token), "content-type": "application/json" });

async function oauthToken(fetchImpl, url, body, label) {
  const response = await checked(fetchImpl, url, { method: "POST", headers: { "content-type": "application/x-www-form-urlencoded" }, body: new URLSearchParams(body) }, label);
  if (typeof response.access_token !== "string") throw new Error(`${label} returned no access token`);
  return response.access_token;
}

async function googleToken(environment, fetchImpl) {
  const serviceAccount = JSON.parse(environment.DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON);
  return oauthToken(fetchImpl, serviceAccount.token_uri, { grant_type: "urn:ietf:params:oauth:grant-type:jwt-bearer", assertion: googleAssertion(serviceAccount) }, "Google Play authentication");
}

async function chromeToken(environment, fetchImpl) {
  return oauthToken(fetchImpl, "https://oauth2.googleapis.com/token", {
    client_id: environment.DEVHUD_CHROME_WEB_STORE_CLIENT_ID,
    client_secret: environment.DEVHUD_CHROME_WEB_STORE_CLIENT_SECRET,
    refresh_token: environment.DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN,
    grant_type: "refresh_token",
  }, "Chrome Web Store authentication");
}

function chromeName(environment) {
  return `publishers/${environment.DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID}/items/${environment.DEVHUD_CHROME_EXTENSION_ID}`;
}

export function classifyApple(state) {
  if (state === "DEVELOPER_REJECTED") return StoreStatus.Withdrawn;
  if (["PENDING_DEVELOPER_RELEASE", "PENDING_DEVELOPER_RELEASE_FOR_APP_STORE"].includes(state)) return StoreStatus.ApprovedHeld;
  if (["READY_FOR_SALE", "READY_FOR_DISTRIBUTION"].includes(state)) return StoreStatus.Public;
  if (typeof state === "string" && state.includes("REJECT")) return StoreStatus.Rejected;
  return StoreStatus.Pending;
}

export function classifyGoogle(state) {
  if (state === "RELEASE_LIFECYCLE_STATE_APPROVED_NOT_PUBLISHED") return StoreStatus.ApprovedHeld;
  if (state === "RELEASE_LIFECYCLE_STATE_PUBLISHED") return StoreStatus.Public;
  if (state === "RELEASE_LIFECYCLE_STATE_NOT_APPROVED") return StoreStatus.Rejected;
  if (["RELEASE_LIFECYCLE_STATE_DRAFT", "RELEASE_LIFECYCLE_STATE_NOT_SENT_FOR_REVIEW"].includes(state)) return StoreStatus.Withdrawn;
  return StoreStatus.Pending;
}

export function classifyChrome({ submitted, published, version }) {
  const publicChannel = published?.distributionChannels?.find(({ crxVersion, deployPercentage }) => crxVersion === version && deployPercentage === 100);
  if (publicChannel) return StoreStatus.Public;
  const submittedChannel = submitted?.distributionChannels?.find(({ crxVersion, deployPercentage }) => crxVersion === version && deployPercentage === 100);
  if (submittedChannel && ["STAGED", "APPROVED"].includes(submitted?.state)) return StoreStatus.ApprovedHeld;
  if (submitted?.state === "CANCELLED") return StoreStatus.Withdrawn;
  if (submitted?.state === "REJECTED") return StoreStatus.Rejected;
  return StoreStatus.Pending;
}

function googleRelease(releases, metadata) {
  return releases.releases?.find(({ activeArtifacts }) => activeArtifacts?.some(({ versionCode }) => String(versionCode) === String(metadata.storeBuildNumber)));
}

async function googleProductionReleases(environment, metadata, fetchImpl) {
  const token = await googleToken(environment, fetchImpl);
  const packageName = encodeURIComponent(environment.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME);
  const releases = await checked(fetchImpl, `https://androidpublisher.googleapis.com/androidpublisher/v3/applications/${packageName}/tracks/production/releases`, { headers: bearer(token) }, "Google Play release status");
  return googleRelease(releases, metadata);
}

async function appleVersion(environment, metadata, fetchImpl, token) {
  const query = new URLSearchParams({ "filter[app]": environment.DEVHUD_APP_STORE_APP_ID, "filter[platform]": "IOS", "filter[versionString]": metadata.version, limit: "1" });
  const result = await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/appStoreVersions?${query}`, { headers: bearer(token) }, "App Store version lookup");
  if (result.data?.length !== 1) throw new Error("App Store version lookup did not return exactly one version");
  return result.data[0];
}

async function appleBuild(environment, metadata, fetchImpl, token) {
  const query = new URLSearchParams({
    "filter[app]": environment.DEVHUD_APP_STORE_APP_ID,
    "filter[version]": String(metadata.storeBuildNumber),
    "fields[builds]": "processingState,version",
    limit: "2",
  });
  const result = await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/builds?${query}`, { headers: bearer(token) }, "App Store build lookup");
  if (!Array.isArray(result.data) || result.data.length > 1) throw new Error("App Store build lookup did not return at most one exact build");
  const build = result.data[0];
  if (!build) return null;
  const processingState = build.attributes?.processingState;
  if (["FAILED", "INVALID"].includes(processingState)) throw new Error(`App Store build processing failed with state ${processingState}`);
  return processingState === "VALID" ? build : null;
}

async function appleBuildStatus(environment, metadata, fetchImpl) {
  const build = await appleBuild(environment, metadata, fetchImpl, appleToken(environment));
  return {
    provider: StoreProvider.Apple,
    status: build ? StoreBuildStatus.Processed : StoreBuildStatus.Processing,
    ...(build ? { buildId: build.id } : {}),
  };
}

function appleSubmissionVersionIds(submission, includedById) {
  const versionIds = new Set();
  const direct = submission.relationships?.appStoreVersionForReview?.data;
  if (direct?.type === "appStoreVersions" && typeof direct.id === "string") versionIds.add(direct.id);
  const items = submission.relationships?.items?.data ?? [];
  for (const item of items) {
    const included = includedById.get(`${item.type}:${item.id}`);
    const version = included?.relationships?.appStoreVersion?.data;
    if (version?.type === "appStoreVersions" && typeof version.id === "string") versionIds.add(version.id);
  }
  return { versionIds, itemCount: items.length };
}

async function appleReviewSubmission(environment, version, fetchImpl, token) {
  const query = new URLSearchParams({
    "filter[platform]": "IOS",
    include: "items,appStoreVersionForReview",
    "fields[reviewSubmissionItems]": "state,appStoreVersion",
    "fields[reviewSubmissions]": "platform,state,items,appStoreVersionForReview",
    limit: "200",
    "limit[items]": "50",
  });
  const result = await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/apps/${encodeURIComponent(environment.DEVHUD_APP_STORE_APP_ID)}/reviewSubmissions?${query}`, { headers: bearer(token) }, "App Store review submission lookup");
  if (!Array.isArray(result.data)) throw new Error("App Store review submission lookup returned invalid data");
  const includedById = new Map((result.included ?? []).map((resource) => [`${resource.type}:${resource.id}`, resource]));
  const exact = [];
  const emptyReady = [];
  for (const submission of result.data) {
    const { versionIds, itemCount } = appleSubmissionVersionIds(submission, includedById);
    if (versionIds.has(version.id)) exact.push({ submission, hasVersionItem: true });
    else if (submission.attributes?.state === "READY_FOR_REVIEW" && itemCount === 0) emptyReady.push({ submission, hasVersionItem: false });
  }
  if (exact.length > 1) throw new Error("App Store returned multiple review submissions for the exact version");
  if (exact.length === 1) return exact[0];
  if (emptyReady.length > 1) throw new Error("App Store returned multiple unbound review submissions");
  return emptyReady[0] ?? null;
}

async function assertNoApplePhasedRelease(version, fetchImpl, token) {
  const response = await fetchImpl(`https://api.appstoreconnect.apple.com/v1/appStoreVersions/${version.id}/appStoreVersionPhasedRelease`, { redirect: "error", headers: bearer(token) });
  if (response.status === 404) return;
  if (!response.ok) throw new Error(`App Store phased-release preflight failed with HTTP ${response.status}`);
  if ((await response.json()).data) throw new Error("App Store phased release must be absent");
}

async function submitApple(environment, metadata, fetchImpl) {
  const token = appleToken(environment);
  const version = await appleVersion(environment, metadata, fetchImpl, token);
  await assertNoApplePhasedRelease(version, fetchImpl, token);
  const current = classifyApple(version.attributes?.appStoreState);
  if (current === StoreStatus.Rejected) throw new Error("App Store version was rejected");
  const build = await appleBuild(environment, metadata, fetchImpl, token);
  if (!build) throw new Error("App Store build is not processed yet");
  let review = await appleReviewSubmission(environment, version, fetchImpl, token);
  if ([StoreStatus.ApprovedHeld, StoreStatus.Public].includes(current)) {
    return { provider: StoreProvider.Apple, status: current, version: metadata.version, ...(review ? { submissionId: review.submission.id } : {}) };
  }
  if (review && appleSubmittedReviewStates.has(review.submission.attributes?.state)) {
    return { provider: StoreProvider.Apple, status: StoreStatus.Pending, submissionId: review.submission.id };
  }
  if (review?.submission.attributes?.state === "UNRESOLVED_ISSUES") throw new Error("App Store review submission has unresolved issues");
  if (review?.submission.attributes?.state === "CANCELING") throw new Error("App Store review submission is canceling");
  await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/appStoreVersions/${version.id}`, { method: "PATCH", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "appStoreVersions", id: version.id, attributes: { releaseType: "MANUAL" } } }) }, "App Store manual-release configuration");
  await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/appStoreVersions/${version.id}/relationships/build`, { method: "PATCH", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "builds", id: build.id } }) }, "App Store build attachment");
  if (!review) {
    const created = await checked(fetchImpl, "https://api.appstoreconnect.apple.com/v1/reviewSubmissions", { method: "POST", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "reviewSubmissions", attributes: { platform: "IOS" }, relationships: { app: { data: { type: "apps", id: environment.DEVHUD_APP_STORE_APP_ID } } } } }) }, "App Store review submission creation");
    review = { submission: created.data, hasVersionItem: false };
  }
  if (!review.hasVersionItem) {
    await checked(fetchImpl, "https://api.appstoreconnect.apple.com/v1/reviewSubmissionItems", { method: "POST", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "reviewSubmissionItems", relationships: { reviewSubmission: { data: { type: "reviewSubmissions", id: review.submission.id } }, appStoreVersion: { data: { type: "appStoreVersions", id: version.id } } } } }) }, "App Store review item creation");
  }
  await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/reviewSubmissions/${review.submission.id}`, { method: "PATCH", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "reviewSubmissions", id: review.submission.id, attributes: { submitted: true } } }) }, "App Store review submission");
  return { provider: StoreProvider.Apple, status: StoreStatus.Pending, submissionId: review.submission.id };
}

async function submitGoogle(environment, metadata, artifact, fetchImpl) {
  const token = await googleToken(environment, fetchImpl);
  const root = `https://androidpublisher.googleapis.com/androidpublisher/v3/applications/${encodeURIComponent(environment.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME)}`;
  const edit = await checked(fetchImpl, `${root}/edits`, { method: "POST", headers: jsonHeaders(token), body: "{}" }, "Google Play edit creation");
  const upload = await checked(fetchImpl, `https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/${encodeURIComponent(environment.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME)}/edits/${edit.id}/bundles?uploadType=media`, { method: "POST", headers: { ...bearer(token), "content-type": "application/octet-stream" }, body: readFileSync(resolve(artifact)) }, "Google Play bundle upload");
  await checked(fetchImpl, `${root}/edits/${edit.id}/tracks/production`, { method: "PUT", headers: jsonHeaders(token), body: JSON.stringify({ track: "production", releases: [{ name: `DevHud ${metadata.version}`, versionCodes: [String(upload.versionCode)], status: "completed", releaseNotes: [{ language: "en-US", text: metadata.releaseNotes.en }, { language: "ko-KR", text: metadata.releaseNotes.ko }] }] }) }, "Google Play production-track update");
  await checked(fetchImpl, `${root}/edits/${edit.id}:commit?changesInReviewBehavior=ERROR_IF_IN_REVIEW`, { method: "POST", headers: jsonHeaders(token), body: "{}" }, "Google Play review submission");
  return { provider: StoreProvider.GooglePlay, status: StoreStatus.Pending, versionCode: String(upload.versionCode) };
}

async function submitChrome(environment, artifact, fetchImpl) {
  const token = await chromeToken(environment, fetchImpl);
  const name = chromeName(environment);
  await checked(fetchImpl, `https://chromewebstore.googleapis.com/upload/v2/${name}:upload`, { method: "POST", headers: { ...bearer(token), "content-type": "application/zip" }, body: readFileSync(resolve(artifact)) }, "Chrome Web Store upload");
  await checked(fetchImpl, `https://chromewebstore.googleapis.com/v2/${name}:publish`, { method: "POST", headers: jsonHeaders(token), body: JSON.stringify({ publishType: "STAGED_PUBLISH", deployInfos: [{ deployPercentage: 100 }], skipReview: false, blockOnWarnings: true }) }, "Chrome Web Store review submission");
  return { provider: StoreProvider.ChromeWebStore, status: StoreStatus.Pending };
}

async function status(provider, environment, metadata, fetchImpl) {
  if (provider === StoreProvider.Apple) {
    const token = appleToken(environment);
    const version = await appleVersion(environment, metadata, fetchImpl, token);
    await assertNoApplePhasedRelease(version, fetchImpl, token);
    return { provider, status: classifyApple(version.attributes?.appStoreState), version: metadata.version };
  }
  if (provider === StoreProvider.GooglePlay) {
    const release = await googleProductionReleases(environment, metadata, fetchImpl);
    return { provider, status: release ? classifyGoogle(release.releaseLifecycleState) : StoreStatus.Withdrawn, versionCode: String(metadata.storeBuildNumber) };
  }
  const token = await chromeToken(environment, fetchImpl);
  const value = await checked(fetchImpl, `https://chromewebstore.googleapis.com/v2/${chromeName(environment)}:fetchStatus`, { headers: bearer(token) }, "Chrome Web Store release status");
  return { provider, status: classifyChrome({ submitted: value.submittedItemRevisionStatus, published: value.publishedItemRevisionStatus, version: metadata.version }), version: metadata.version };
}

async function publish(provider, environment, metadata, fetchImpl) {
  if (provider === StoreProvider.Apple) {
    const version = await appleVersion(environment, metadata, fetchImpl, appleToken(environment));
    const token = appleToken(environment);
    await assertNoApplePhasedRelease(version, fetchImpl, token);
    await checked(fetchImpl, "https://api.appstoreconnect.apple.com/v1/appStoreVersionReleaseRequests", { method: "POST", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "appStoreVersionReleaseRequests", relationships: { appStoreVersion: { data: { type: "appStoreVersions", id: version.id } } } } }) }, "App Store manual release");
  } else if (provider === StoreProvider.ChromeWebStore) {
    const token = await chromeToken(environment, fetchImpl);
    await checked(fetchImpl, `https://chromewebstore.googleapis.com/v2/${chromeName(environment)}:publish`, { method: "POST", headers: jsonHeaders(token), body: JSON.stringify({ publishType: "STAGED_PUBLISH", deployInfos: [{ deployPercentage: 100 }], blockOnWarnings: true }) }, "Chrome Web Store staged publication");
  } else {
    throw new Error("Google Play managed publication requires the protected operator publication gate");
  }
  return { provider, status: StoreStatus.Pending };
}

async function withdraw(provider, environment, metadata, options, fetchImpl) {
  if (provider === StoreProvider.Apple) {
    const token = appleToken(environment);
    const version = await appleVersion(environment, metadata, fetchImpl, token);
    const current = classifyApple(version.attributes?.appStoreState);
    if (current === StoreStatus.Public) throw new Error("App Store version is already public and cannot be withdrawn automatically");
    const review = await appleReviewSubmission(environment, version, fetchImpl, token);
    if (!review || current === StoreStatus.Withdrawn) return { provider, status: StoreStatus.Withdrawn };
    if (options["submission-id"] && options["submission-id"] !== review.submission.id) throw new Error("App Store submission ID does not match the exact release");
    if (review.submission.attributes?.state === "CANCELING") return { provider, withdrawalRequested: true, submissionId: review.submission.id };
    const submissionId = encodeURIComponent(review.submission.id);
    await checked(fetchImpl, `https://api.appstoreconnect.apple.com/v1/reviewSubmissions/${submissionId}`, { method: "PATCH", headers: jsonHeaders(token), body: JSON.stringify({ data: { type: "reviewSubmissions", id: review.submission.id, attributes: { canceled: true } } }) }, "App Store review withdrawal");
    return { provider, withdrawalRequested: true, submissionId: review.submission.id };
  }
  if (provider === StoreProvider.GooglePlay) {
    const release = await googleProductionReleases(environment, metadata, fetchImpl);
    const releaseStatus = release ? classifyGoogle(release.releaseLifecycleState) : StoreStatus.Withdrawn;
    if (releaseStatus === StoreStatus.Public) throw new Error("Google Play release is already public and cannot be withdrawn automatically");
    if (![StoreStatus.Withdrawn, StoreStatus.Rejected].includes(releaseStatus)) throw new Error("remove the DevHud release from Google Play managed publishing in the protected operator gate, then retry rollback");
    return { provider, status: StoreStatus.Withdrawn, versionCode: String(metadata.storeBuildNumber) };
  }
  const token = await chromeToken(environment, fetchImpl);
  const current = await checked(fetchImpl, `https://chromewebstore.googleapis.com/v2/${chromeName(environment)}:fetchStatus`, { headers: bearer(token) }, "Chrome Web Store release status");
  const exactPublic = current.publishedItemRevisionStatus?.distributionChannels?.some(({ crxVersion, deployPercentage }) => crxVersion === metadata.version && deployPercentage === 100);
  if (exactPublic) throw new Error("Chrome Web Store release is already public and cannot be withdrawn automatically");
  const exactSubmitted = current.submittedItemRevisionStatus?.distributionChannels?.some(({ crxVersion, deployPercentage }) => crxVersion === metadata.version && deployPercentage === 100);
  if (!exactSubmitted || current.submittedItemRevisionStatus?.state === "CANCELLED") return { provider, status: StoreStatus.Withdrawn };
  await checked(fetchImpl, `https://chromewebstore.googleapis.com/v2/${chromeName(environment)}:cancelSubmission`, { method: "POST", headers: bearer(token) }, "Chrome Web Store review withdrawal");
  return { provider, withdrawalRequested: true };
}

function parse(arguments_) {
  const command = arguments_.shift();
  const provider = arguments_.shift();
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) options[arguments_[index].replace(/^--/u, "")] = arguments_[index + 1];
  if (!Object.values(StoreProvider).includes(provider) || !["build-status", "submit", "status", "publish", "withdraw"].includes(command) || (command === "build-status" && provider !== StoreProvider.Apple)) {
    throw new Error("usage: devhud-store-release.mjs <build-status|submit|status|publish|withdraw> <apple|google-play|chrome-web-store> [--artifact path] [--submission-id id] [--output path]");
  }
  return { command, provider, options };
}

export async function run(command, provider, options, environment = process.env, fetchImpl = fetch) {
  const metadata = loadReleaseMetadata();
  if (command === "build-status") return appleBuildStatus(environment, metadata, fetchImpl);
  if (command === "status") return status(provider, environment, metadata, fetchImpl);
  if (command === "publish") return publish(provider, environment, metadata, fetchImpl);
  if (command === "withdraw") return withdraw(provider, environment, metadata, options, fetchImpl);
  if (provider === StoreProvider.Apple) return submitApple(environment, metadata, fetchImpl);
  if (!options.artifact) throw new Error("--artifact is required for Google Play and Chrome submissions");
  return provider === StoreProvider.GooglePlay ? submitGoogle(environment, metadata, options.artifact, fetchImpl) : submitChrome(environment, options.artifact, fetchImpl);
}

export async function main(arguments_ = process.argv.slice(2), environment = process.env) {
  const { command, provider, options } = parse([...arguments_]);
  const result = redact(await run(command, provider, options, environment), environment);
  const serialized = `${JSON.stringify(result, null, 2)}\n`;
  if (options.output) writeFileSync(resolve(options.output), serialized, { mode: 0o600 }); else process.stdout.write(serialized);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await main(); } catch (error) {
    process.stderr.write(`[devhud.store] ${redact(String(error.message))}\n`);
    process.exit(1);
  }
}
