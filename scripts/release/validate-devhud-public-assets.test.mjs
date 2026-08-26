import assert from "node:assert/strict";
import { createHash, generateKeyPairSync } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, statSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import test from "node:test";

import { artifactGroups, loadReleaseMetadata } from "./devhud-release.mjs";
import { generateUpdater, rawPublicKey } from "./generate-devhud-updater.mjs";
import { publicReleaseAssetNames, validatePublicReleaseAssets } from "./validate-devhud-public-assets.mjs";

function files(root) {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const path = join(root, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  });
}

function fixture() {
  const root = mkdtempSync(join(tmpdir(), "devhud-public-assets-"));
  const metadata = loadReleaseMetadata();
  for (const artifact of [...artifactGroups.desktop, "devhud-chrome-github-validation.zip"]) writeFileSync(join(root, artifact), `fixture:${artifact}`);
  writeFileSync(join(root, `devhud-v${metadata.version}-release-evidence.tar.gz`), "evidence");
  writeFileSync(join(root, "devhud-release-index.json"), `${JSON.stringify({
    schemaVersion: 1,
    project: "devhud",
    version: metadata.version,
    tag: `devhud@v${metadata.version}`,
    storeBuildNumber: metadata.storeBuildNumber,
    channels: ["apple-app-store", "google-play", "chrome-web-store", "github-release", "desktop-updater", "api", "public-docs"],
  }, null, 2)}\n`);

  for (const [path, contents] of [
    ["sbom/devhud.spdx.json", "sbom"],
    ["provenance/devhud.intoto.jsonl", "provenance"],
    ["validation/evidence.json", "validation"],
  ]) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), contents);
  }

  const { privateKey } = generateKeyPairSync("ed25519");
  const publicKey = rawPublicKey(privateKey);
  const trustRoot = {
    schemaVersion: 1,
    keyId: "devhud-release-root-v1",
    algorithm: "ed25519",
    publicKey: publicKey.toString("base64"),
    fingerprint: createHash("sha256").update(publicKey).digest("hex"),
    productionReady: true,
  };
  generateUpdater({
    artifactsDirectory: root,
    outputDirectory: join(root, "updater"),
    encodedPrivateKey: privateKey.export({ format: "der", type: "pkcs8" }).toString("base64"),
    metadata,
    trustRoot,
    timestamp: "2026-08-25T00:00:00Z",
  });
  const evidence = ["sbom", "provenance", "updater/signatures", "validation"].flatMap((directory) => files(join(root, directory)))
    .map((path) => relative(root, path).replaceAll("\\", "/"));
  const checksums = [...artifactGroups.desktop, "devhud-chrome-github-validation.zip", ...evidence]
    .map((artifact) => `${createHash("sha256").update(readFileSync(join(root, artifact))).digest("hex")}  ${artifact}`);
  writeFileSync(join(root, "SHA256SUMS"), `${checksums.join("\n")}\n`);
  const release = {
    isDraft: false,
    isPrerelease: false,
    tagName: `devhud@v${metadata.version}`,
    assets: publicReleaseAssetNames(metadata.version).map((name) => ({ name, size: statSync(join(root, name)).size, state: "uploaded" })),
  };
  return { root, metadata, release, trustRoot };
}

test("remote public assets match the exact inventory, signed checksums, and updater manifests", () => {
  const value = fixture();
  assert.doesNotThrow(() => validatePublicReleaseAssets(value.root, value.release, { metadata: value.metadata, trustRoot: value.trustRoot, verifySigstore: false }));
});

test("remote public validation rejects missing, extra, and replaced assets", () => {
  const missing = fixture();
  missing.release.assets.pop();
  assert.throws(() => validatePublicReleaseAssets(missing.root, missing.release, { metadata: missing.metadata, trustRoot: missing.trustRoot, verifySigstore: false }), /asset set/u);

  const extra = fixture();
  extra.release.assets.push({ name: "unexpected.bin", size: 1, state: "uploaded" });
  assert.throws(() => validatePublicReleaseAssets(extra.root, extra.release, { metadata: extra.metadata, trustRoot: extra.trustRoot, verifySigstore: false }), /asset set/u);

  const replaced = fixture();
  writeFileSync(join(replaced.root, artifactGroups.desktop[0]), "replaced");
  replaced.release.assets.find(({ name }) => name === artifactGroups.desktop[0]).size = statSync(join(replaced.root, artifactGroups.desktop[0])).size;
  assert.throws(() => validatePublicReleaseAssets(replaced.root, replaced.release, { metadata: replaced.metadata, trustRoot: replaced.trustRoot, verifySigstore: false }), /checksum mismatch/u);
});

test("remote public validation rejects a release index or updater manifest mismatch", () => {
  const index = fixture();
  const parsed = JSON.parse(readFileSync(join(index.root, "devhud-release-index.json"), "utf8"));
  parsed.version = "0.0.0";
  writeFileSync(join(index.root, "devhud-release-index.json"), JSON.stringify(parsed));
  index.release.assets.find(({ name }) => name === "devhud-release-index.json").size = statSync(join(index.root, "devhud-release-index.json")).size;
  assert.throws(() => validatePublicReleaseAssets(index.root, index.release, { metadata: index.metadata, trustRoot: index.trustRoot, verifySigstore: false }), /release index/u);

  const updater = fixture();
  const artifact = artifactGroups.desktop.find((name) => name.includes("windows-x64-windows-msi"));
  writeFileSync(join(updater.root, artifact), "different updater bytes");
  const lines = readFileSync(join(updater.root, "SHA256SUMS"), "utf8").trim().split("\n").map((line) => line.endsWith(`  ${artifact}`)
    ? `${createHash("sha256").update(readFileSync(join(updater.root, artifact))).digest("hex")}  ${artifact}`
    : line);
  writeFileSync(join(updater.root, "SHA256SUMS"), `${lines.join("\n")}\n`);
  updater.release.assets.find(({ name }) => name === artifact).size = statSync(join(updater.root, artifact)).size;
  assert.throws(() => validatePublicReleaseAssets(updater.root, updater.release, { metadata: updater.metadata, trustRoot: updater.trustRoot, verifySigstore: false }), /updater artifact digest mismatch/u);
});

test("remote public validation authenticates the exact extracted evidence payloads", () => {
  const replaced = fixture();
  writeFileSync(join(replaced.root, "sbom/devhud.spdx.json"), "replaced");
  assert.throws(() => validatePublicReleaseAssets(replaced.root, replaced.release, { metadata: replaced.metadata, trustRoot: replaced.trustRoot, verifySigstore: false }), /checksum mismatch/u);

  const extra = fixture();
  writeFileSync(join(extra.root, "validation/unexpected.json"), "unexpected");
  assert.throws(() => validatePublicReleaseAssets(extra.root, extra.release, { metadata: extra.metadata, trustRoot: extra.trustRoot, verifySigstore: false }), /evidence payload inventory/u);

  const missing = fixture();
  unlinkSync(join(missing.root, "provenance/devhud.intoto.jsonl"));
  assert.throws(() => validatePublicReleaseAssets(missing.root, missing.release, { metadata: missing.metadata, trustRoot: missing.trustRoot, verifySigstore: false }), /evidence payload inventory/u);
});
