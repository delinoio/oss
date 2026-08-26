import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { StoreStatus, classifyApple, classifyChrome, classifyGoogle } from "./devhud-store-release.mjs";

const source = readFileSync(fileURLToPath(new URL("devhud-store-release.mjs", import.meta.url)), "utf8");

test("store review states distinguish pending, approved-held, public, and rejected", () => {
  assert.equal(classifyApple("WAITING_FOR_REVIEW"), StoreStatus.Pending);
  assert.equal(classifyApple("PENDING_DEVELOPER_RELEASE"), StoreStatus.ApprovedHeld);
  assert.equal(classifyApple("READY_FOR_SALE"), StoreStatus.Public);
  assert.equal(classifyApple("REJECTED"), StoreStatus.Rejected);
  assert.equal(classifyGoogle("IN_REVIEW"), StoreStatus.Pending);
  assert.equal(classifyGoogle("APPROVED"), StoreStatus.ApprovedHeld);
  assert.equal(classifyGoogle("AVAILABLE"), StoreStatus.Public);
  assert.equal(classifyGoogle("NOT_APPROVED"), StoreStatus.Rejected);
});

test("Chrome requires the exact version at 100 percent before public", () => {
  const approved = classifyChrome({ submitted: { state: "STAGED", distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] }, published: {}, version: "0.1.0" });
  assert.equal(approved, StoreStatus.ApprovedHeld);
  const wrongHeldVersion = classifyChrome({ submitted: { state: "STAGED", distributionChannels: [{ crxVersion: "0.0.9", deployPercentage: 100 }] }, published: {}, version: "0.1.0" });
  assert.equal(wrongHeldVersion, StoreStatus.Pending);
  const partial = classifyChrome({ submitted: {}, published: { distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 50 }] }, version: "0.1.0" });
  assert.equal(partial, StoreStatus.Pending);
  const complete = classifyChrome({ submitted: {}, published: { distributionChannels: [{ crxVersion: "0.1.0", deployPercentage: 100 }] }, version: "0.1.0" });
  assert.equal(complete, StoreStatus.Public);
});

test("App Store review uses manual release and the dedicated build linkage endpoint", () => {
  assert.match(source, /attributes: \{ releaseType: "MANUAL" \}/u);
  assert.match(source, /appStoreVersions\/\$\{version\.id\}\/relationships\/build/u);
  assert.match(source, /appStoreVersionReleaseRequests/u);
  assert.match(source, /appStoreVersionPhasedRelease/u);
});
