import assert from "node:assert/strict";
import { createHash, generateKeyPairSync } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { artifactGroups, loadReleaseMetadata, updaterTargets } from "./devhud-release.mjs";
import { generateUpdater, rawPublicKey } from "./generate-devhud-updater.mjs";

function fixture() {
  const { privateKey } = generateKeyPairSync("ed25519");
  const encodedPrivateKey = privateKey.export({ format: "der", type: "pkcs8" }).toString("base64");
  const publicKey = rawPublicKey(privateKey);
  return {
    privateKey,
    encodedPrivateKey,
    trustRoot: {
      schemaVersion: 1,
      keyId: "devhud-release-root-v1",
      algorithm: "ed25519",
      publicKey: publicKey.toString("base64"),
      fingerprint: createHash("sha256").update(publicKey).digest("hex"),
      productionReady: true,
    },
  };
}

test("target-specific updater manifests and detached signatures are deterministic", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-updater-test-"));
  const artifacts = join(root, "artifacts");
  const first = join(root, "first");
  const second = join(root, "second");
  mkdirSync(artifacts);
  for (const artifact of artifactGroups.desktop) writeFileSync(join(artifacts, artifact), `fixture:${artifact}`);
  const keys = fixture();
  const options = {
    artifactsDirectory: artifacts,
    encodedPrivateKey: keys.encodedPrivateKey,
    metadata: loadReleaseMetadata(),
    trustRoot: keys.trustRoot,
    timestamp: "2026-08-25T00:00:00Z",
  };
  const firstGenerated = generateUpdater({ ...options, outputDirectory: first });
  const secondGenerated = generateUpdater({ ...options, outputDirectory: second });
  assert.equal(firstGenerated.length, updaterTargets.length);
  for (let index = 0; index < firstGenerated.length; index += 1) {
    const firstManifest = readFileSync(firstGenerated[index].manifestPath);
    const secondManifest = readFileSync(secondGenerated[index].manifestPath);
    assert.deepEqual(firstManifest, secondManifest);
    const envelope = JSON.parse(firstManifest);
    const payload = JSON.parse(Buffer.from(envelope.signedPayload, "base64"));
    assert.equal(payload.packageKind, updaterTargets[index].packageKind);
    assert.ok(payload.artifact.url.endsWith(`/${updaterTargets[index].artifact}`));
    assert.equal(envelope.rollbackAuthorization, null);
  }
});

test("updater generation rejects a mismatched private key", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-updater-key-test-"));
  const artifacts = join(root, "artifacts");
  mkdirSync(artifacts);
  for (const target of updaterTargets) writeFileSync(join(artifacts, target.artifact), "fixture");
  const trusted = fixture();
  const untrusted = fixture();
  assert.throws(() => generateUpdater({
    artifactsDirectory: artifacts,
    outputDirectory: join(root, "output"),
    encodedPrivateKey: untrusted.encodedPrivateKey,
    trustRoot: trusted.trustRoot,
    metadata: loadReleaseMetadata(),
    timestamp: "2026-08-25T00:00:00Z",
  }), /does not match/u);
});
