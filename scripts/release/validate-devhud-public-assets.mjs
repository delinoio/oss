#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { closeSync, existsSync, openSync, readFileSync, readSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { artifactGroups, loadReleaseMetadata } from "./devhud-release.mjs";
import { cosignVerifyArguments, sigstoreVerificationPolicy, validateUpdater } from "./validate-devhud-private-build.mjs";

const publicBinaries = Object.freeze([...artifactGroups.desktop, "devhud-chrome-github-validation.zip"]);
const channels = Object.freeze(["apple-app-store", "google-play", "chrome-web-store", "github-release", "desktop-updater", "api", "public-docs"]);

function sha256File(path) {
  const hash = createHash("sha256");
  const descriptor = openSync(path, "r");
  const buffer = Buffer.allocUnsafe(1024 * 1024);
  try {
    for (;;) {
      const count = readSync(descriptor, buffer, 0, buffer.length, null);
      if (count === 0) break;
      hash.update(buffer.subarray(0, count));
    }
  } finally { closeSync(descriptor); }
  return hash.digest("hex");
}

export function publicReleaseAssetNames(version) {
  return [...publicBinaries, `devhud-v${version}-release-evidence.tar.gz`, "devhud-release-index.json"].sort();
}

export function validatePublicAssetInventory(root, release, metadata = loadReleaseMetadata()) {
  if (release?.isDraft !== false || release?.isPrerelease !== false || release?.tagName !== `devhud@v${metadata.version}`) throw new Error("GitHub Release identity or visibility is invalid");
  if (!Array.isArray(release.assets)) throw new Error("GitHub Release asset inventory is missing");
  const expected = publicReleaseAssetNames(metadata.version);
  const names = release.assets.map((asset) => asset?.name);
  if (new Set(names).size !== names.length || JSON.stringify([...names].sort()) !== JSON.stringify(expected)) throw new Error("GitHub Release asset set is not exact");
  for (const asset of release.assets) {
    const path = join(root, asset.name);
    if (asset.state !== "uploaded") throw new Error(`public release asset is not uploaded: ${asset.name}`);
    if (!existsSync(path) || !statSync(path).isFile() || statSync(path).size <= 0) throw new Error(`missing or empty public release asset: ${asset.name}`);
    if (!Number.isSafeInteger(asset.size) || asset.size <= 0 || statSync(path).size !== asset.size) throw new Error(`public release asset size mismatch: ${asset.name}`);
  }

  const index = JSON.parse(readFileSync(join(root, "devhud-release-index.json"), "utf8"));
  const expectedIndex = {
    schemaVersion: 1,
    project: "devhud",
    version: metadata.version,
    tag: `devhud@v${metadata.version}`,
    storeBuildNumber: metadata.storeBuildNumber,
    channels: [...channels],
  };
  if (JSON.stringify(index) !== JSON.stringify(expectedIndex)) throw new Error("public release index does not match the exact release identity");
}

export function validatePublishedChecksums(root, { verifySigstore = true, environment = process.env } = {}) {
  const checksumPath = join(root, "SHA256SUMS");
  const checksums = new Map();
  for (const line of readFileSync(checksumPath, "utf8").trim().split("\n")) {
    const match = /^([a-f0-9]{64})  (.+)$/u.exec(line);
    if (!match || checksums.has(match[2])) throw new Error(`invalid or duplicate SHA256SUMS entry: ${line}`);
    checksums.set(match[2], match[1]);
  }
  const policy = verifySigstore ? sigstoreVerificationPolicy(environment) : undefined;
  if (verifySigstore) execFileSync("cosign", cosignVerifyArguments(join(root, "sigstore/SHA256SUMS.sigstore.json"), checksumPath, policy), { stdio: "inherit" });
  for (const artifact of publicBinaries) {
    const path = join(root, artifact);
    if (checksums.get(artifact) !== sha256File(path)) throw new Error(`published asset checksum mismatch: ${artifact}`);
    if (verifySigstore) execFileSync("cosign", cosignVerifyArguments(join(root, "sigstore", `${artifact}.sigstore.json`), path, policy), { stdio: "inherit" });
  }
}

export function validatePublicReleaseAssets(root, release, { metadata = loadReleaseMetadata(), trustRoot, verifySigstore = true, environment = process.env } = {}) {
  validatePublicAssetInventory(root, release, metadata);
  validatePublishedChecksums(root, { verifySigstore, environment });
  validateUpdater(root, trustRoot);
}

function parse(arguments_) {
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (!name?.startsWith("--") || value === undefined) throw new Error(`invalid argument: ${name ?? "missing"}`);
    options[name.slice(2)] = value;
  }
  if (!options["assets-dir"] || !options["release-json"]) throw new Error("usage: validate-devhud-public-assets.mjs --assets-dir <dir> --release-json <path>");
  return options;
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parse(arguments_);
  const root = resolve(options["assets-dir"]);
  validatePublicReleaseAssets(root, JSON.parse(readFileSync(resolve(options["release-json"]), "utf8")));
  console.error(`[devhud.public-assets] verified ${publicReleaseAssetNames(loadReleaseMetadata().version).length} remote GitHub Release assets and all updater targets`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); } catch (error) {
    process.stderr.write(`[devhud.public-assets] ${error.message}\n`);
    process.exit(1);
  }
}
