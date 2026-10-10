// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { submitPlatforms } from "./pipeline.mjs";
import { Identity, Stage } from "./beta.mjs";

const manifest = {
  candidateId: "fixture",
  artifacts: { ios: { sha256: "ios" }, android: { sha256: "android" } },
};
const read = (directory, platform) =>
  JSON.parse(readFileSync(join(directory, `${platform}-receipt.json`)));

test("iOS-only submission never constructs an Android provider or receipt", async () => {
  const directory = mkdtempSync(join(tmpdir(), "delidev-pipeline-"));
  const selected = { ...manifest, schema: 2, target: "ios", artifacts: { ios: manifest.artifacts.ios } };
  try {
    await assert.rejects(submitPlatforms(selected, directory, false, (platform) => {
      assert.equal(platform, "ios");
      return { preflight: async () => { throw Error("fixture"); } };
    }));
    assert.equal(read(directory, "ios").stage, Stage.Ready);
    assert.throws(() => read(directory, "android"), /ENOENT/);
    await assert.rejects(submitPlatforms({ ...selected, artifacts: manifest.artifacts }, directory, true, () => {
      assert.fail("mixed platform authority");
    }));
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("Android preflight failure preserves both intents and resume skips accepted iOS bytes", async () => {
  const directory = mkdtempSync(join(tmpdir(), "delidev-pipeline-"));
  let failAndroid = true;
  const uploads = { ios: 0, android: 0 }, assigned = { ios: false, android: false };
  const provider = (platform) => {
    assert.ok(read(directory, "ios"));
    assert.ok(read(directory, "android"));
    const proof = () => ({ state: "present", id: platform, identity: Identity,
      sha256: platform, internal: true, distributed: assigned[platform] });
    return {
      preflight: async () => { if (platform === "android" && failAndroid) throw Error("fixture"); },
      upload: async () => { uploads[platform]++; return proof(); },
      inspect: async () => proof(),
      assignInternal: async () => { assigned[platform] = true; },
    };
  };
  try {
    await assert.rejects(submitPlatforms(manifest, directory, false, provider));
    assert.equal(read(directory, "ios").stage, Stage.Distributed);
    assert.equal(read(directory, "android").stage, Stage.Ready);
    failAndroid = false;
    await submitPlatforms(manifest, directory, true, provider);
    assert.deepEqual(uploads, { ios: 1, android: 1 });
    assert.equal(read(directory, "android").stage, Stage.Distributed);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("lost receipt remains Unknown and cannot authorize a replacement upload", async () => {
  const directory = mkdtempSync(join(tmpdir(), "delidev-pipeline-"));
  let uploads = 0;
  try {
    await assert.rejects(submitPlatforms(manifest, directory, true, () => ({
      preflight: async () => {}, inspect: async () => ({ state: "unknown" }),
      upload: async () => { uploads++; },
    })));
    assert.equal(uploads, 0);
    assert.equal(read(directory, "ios").stage, Stage.Unknown);
    assert.equal(read(directory, "android").stage, Stage.Unknown);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("foreign candidate or invalid stage rejects before provider access and preserves bytes", async () => {
  for (const receipt of [
    { candidateId: "foreign", platform: "android", stage: Stage.Sending },
    { candidateId: "fixture", platform: "android", stage: "foreign" },
  ]) {
    const directory = mkdtempSync(join(tmpdir(), "delidev-pipeline-"));
    const file = join(directory, "android-receipt.json"), original = JSON.stringify(receipt);
    writeFileSync(file, original);
    try {
      await assert.rejects(submitPlatforms(manifest, directory, true, () => {
        assert.fail("provider access");
      }));
      assert.equal(readFileSync(file, "utf8"), original);
    } finally { rmSync(directory, { recursive: true, force: true }); }
  }
});

test("repair source checks preserve the original clean checkout and pin reviewed code independently", async () => {
  const { verifySourceRecords } = await import("./pipeline.mjs");
  const input = { sourceSha: "a".repeat(40) }, original = { sha: input.sourceSha, dirty: false },
    recovery = { sha: "b".repeat(40), dirty: false },
    environment = { MODE: "resume", GITHUB_SHA: recovery.sha, DELIDEV_MOBILE_RECOVERY_SHA: recovery.sha };
  assert.doesNotThrow(() => verifySourceRecords(input, original, recovery, environment));
  for (const record of [{ ...original, sha: recovery.sha }, { ...original, dirty: true }])
    assert.throws(() => verifySourceRecords(input, record, recovery, environment));
  for (const record of [{ ...recovery, sha: original.sha }, { ...recovery, dirty: true }])
    assert.throws(() => verifySourceRecords(input, original, record, environment));
  for (const delta of [{ MODE: "submit" }, { GITHUB_SHA: original.sha }, { DELIDEV_MOBILE_RECOVERY_SHA: "bad" }])
    assert.throws(() => verifySourceRecords(input, original, recovery, { ...environment, ...delta }));
});
