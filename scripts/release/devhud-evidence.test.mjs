import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { mergeEvidence, recordEvidence } from "./devhud-evidence.mjs";
import { updaterTargets } from "./devhud-release.mjs";

const desktop = ["namesVersionsTargets", "cefHelpers", "cefSandbox", "trayLifecycle", "updaterMaterial", "nativeMessaging", "installLaunchQuitUninstall"];
const mobile = ["namesVersionsTargets", "platformSignature", "nativeDeckWidget"];

test("evidence fails closed on an incomplete check set", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-evidence-incomplete-"));
  assert.throws(() => recordEvidence("macos-x64", join(root, "evidence.json"), desktop), /platformSignature/u);
});

test("mobile evidence rejects an install lifecycle claim that was not performed", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-evidence-mobile-"));
  assert.throws(() => recordEvidence("ios-app-store", join(root, "evidence.json"), [...mobile, "installLaunchQuit"]), /unexpected=installLaunchQuit/u);
});

test("only the complete exact target set merges as a private non-public candidate", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-evidence-complete-"));
  const input = join(root, "targets");
  mkdirSync(input);
  for (const { id } of updaterTargets) recordEvidence(id, join(input, `${id}.json`), [...desktop, ...(id.startsWith("macos") || id.startsWith("windows") ? ["platformSignature"] : [])]);
  recordEvidence("ios-app-store", join(input, "ios.json"), mobile);
  recordEvidence("android-google-play", join(input, "android.json"), mobile);
  recordEvidence("chrome-extension", join(input, "chrome.json"), ["namesVersionsTargets", "permissions", "reproducible", "byteParity", "nativeMessagingIdentity"]);
  const oci = ["namesVersionsTargets", "multiArch", "nonRoot", "health", "migrations", "administratorAssets"];
  recordEvidence("devhud-api-oci", join(input, "api.json"), oci);
  recordEvidence("devhud-api-sweeper-oci", join(input, "sweeper.json"), oci);
  const output = join(root, "evidence.json");
  mergeEvidence(input, output);
  const merged = JSON.parse(readFileSync(output));
  assert.equal(merged.publicReady, false);
  assert.equal(merged.readiness, "private-signed-candidate");
  assert.equal(merged.targets.length, updaterTargets.length + 5);
});
