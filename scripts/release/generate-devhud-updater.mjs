#!/usr/bin/env node

import { createHash, createPrivateKey, createPublicKey, sign, verify } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { loadReleaseMetadata, metadataPath, repositoryRoot, updaterTargets } from "./devhud-release.mjs";

const ARTIFACT_DOMAIN = Buffer.from("devhud-update-artifact-v1\0", "utf8");
const MANIFEST_DOMAIN = Buffer.from("devhud-update-manifest-v1\0", "utf8");
export const MAX_UPDATER_ARTIFACT_BYTES = 512 * 1024 * 1024;

function json(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

export function ed25519PrivateKey(encoded) {
  let der;
  try {
    der = Buffer.from(encoded, "base64");
    if (der.length === 0 || der.toString("base64").replace(/=+$/u, "") !== encoded.replace(/=+$/u, "")) throw new Error();
    return createPrivateKey({ key: der, format: "der", type: "pkcs8" });
  } catch {
    throw new Error("updater signing key must be base64 PKCS#8 Ed25519 DER");
  }
}

export function rawPublicKey(privateKey) {
  const spki = createPublicKey(privateKey).export({ format: "der", type: "spki" });
  if (spki.length !== 44) throw new Error("updater signing key did not produce an Ed25519 public key");
  return spki.subarray(spki.length - 32);
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

function signature(privateKey, domain, bytes) {
  return sign(null, Buffer.concat([domain, bytes]), privateKey);
}

export function assertUpdaterArtifactSize(size, artifact) {
  if (!Number.isSafeInteger(size) || size < 1 || size > MAX_UPDATER_ARTIFACT_BYTES) {
    throw new Error(`updater artifact size is outside the supported range: ${artifact}`);
  }
}

function publishedAt(root = repositoryRoot) {
  const value = execFileSync("git", ["show", "-s", "--format=%cI", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
  const instant = new Date(value);
  if (Number.isNaN(instant.valueOf())) throw new Error("source commit has no valid publication timestamp");
  return instant.toISOString().replace(".000Z", "Z");
}

export function assertUpdaterTrust(privateKey, trustRoot) {
  if (trustRoot.schemaVersion !== 1 || trustRoot.algorithm !== "ed25519" || trustRoot.productionReady !== true) {
    throw new Error("updater trust root is not production-ready");
  }
  const publicKey = rawPublicKey(privateKey);
  if (publicKey.toString("base64") !== trustRoot.publicKey || sha256(publicKey) !== trustRoot.fingerprint) {
    throw new Error("updater signing key does not match the committed trust root");
  }
}

export function generateUpdater({
  artifactsDirectory,
  outputDirectory,
  encodedPrivateKey,
  metadata = loadReleaseMetadata(metadataPath),
  trustRoot = json(join(repositoryRoot, "apps/devhud/updater-trust-root.json")),
  timestamp = publishedAt(),
}) {
  const privateKey = ed25519PrivateKey(encodedPrivateKey);
  assertUpdaterTrust(privateKey, trustRoot);
  const publicKey = createPublicKey(privateKey);
  const generated = [];
  for (const target of updaterTargets) {
    const artifactPath = join(artifactsDirectory, target.artifact);
    assertUpdaterArtifactSize(statSync(artifactPath).size, target.artifact);
    const artifact = readFileSync(artifactPath);
    assertUpdaterArtifactSize(artifact.length, target.artifact);
    const artifactSignature = signature(privateKey, ARTIFACT_DOMAIN, artifact);
    if (!verify(null, Buffer.concat([ARTIFACT_DOMAIN, artifact]), publicKey, artifactSignature)) throw new Error("generated artifact signature did not verify");
    const payload = {
      schemaVersion: 1,
      channel: "stable",
      platform: target.platform,
      architecture: target.architecture,
      packageKind: target.packageKind,
      version: metadata.version,
      publishedAt: timestamp,
      releaseNotes: metadata.releaseNotes,
      artifact: {
        url: `https://github.com/delinoio/oss/releases/download/devhud@v${metadata.version}/${target.artifact}`,
        size: artifact.length,
        sha256: sha256(artifact),
        signature: artifactSignature.toString("base64"),
      },
      signerFingerprint: trustRoot.fingerprint,
    };
    const payloadBytes = Buffer.from(JSON.stringify(payload), "utf8");
    const manifestSignature = signature(privateKey, MANIFEST_DOMAIN, payloadBytes);
    if (!verify(null, Buffer.concat([MANIFEST_DOMAIN, payloadBytes]), publicKey, manifestSignature)) throw new Error("generated manifest signature did not verify");
    const envelope = {
      schemaVersion: 1,
      signedPayload: payloadBytes.toString("base64"),
      manifestSignature: manifestSignature.toString("base64"),
      keyChain: [],
      rollbackAuthorization: null,
    };
    const relativeRoot = join("stable", target.platform, target.architecture);
    const manifestPath = join(outputDirectory, "manifests", relativeRoot, `${target.packageKind}.json`);
    const artifactSignaturePath = join(outputDirectory, "signatures", relativeRoot, `${target.packageKind}.artifact.ed25519`);
    const manifestSignaturePath = join(outputDirectory, "signatures", relativeRoot, `${target.packageKind}.manifest.ed25519`);
    for (const path of [manifestPath, artifactSignaturePath, manifestSignaturePath]) mkdirSync(dirname(path), { recursive: true });
    writeFileSync(manifestPath, `${JSON.stringify(envelope)}\n`, { mode: 0o644 });
    writeFileSync(artifactSignaturePath, `${artifactSignature.toString("base64")}\n`, { mode: 0o644 });
    writeFileSync(manifestSignaturePath, `${manifestSignature.toString("base64")}\n`, { mode: 0o644 });
    generated.push({ target: target.id, manifestPath, artifactSignaturePath, manifestSignaturePath });
  }
  return generated;
}

function parseArguments(arguments_) {
  const options = {};
  for (let index = 0; index < arguments_.length; index += 1) {
    const name = arguments_[index];
    if (!["--artifacts-dir", "--output-dir", "--key-file"].includes(name)) throw new Error(`unknown argument: ${name}`);
    const value = arguments_[++index];
    if (!value || value.startsWith("--")) throw new Error(`${name} requires a value`);
    options[name.slice(2)] = value;
  }
  for (const name of ["artifacts-dir", "output-dir", "key-file"]) if (!options[name]) throw new Error(`--${name} is required`);
  return options;
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parseArguments(arguments_);
  const keyPath = resolve(options["key-file"]);
  const metadata = statSync(keyPath);
  if ((metadata.mode & 0o077) !== 0) throw new Error("updater key file must not be group- or world-accessible");
  const generated = generateUpdater({
    artifactsDirectory: resolve(options["artifacts-dir"]),
    outputDirectory: resolve(options["output-dir"]),
    encodedPrivateKey: readFileSync(keyPath, "utf8").trim(),
  });
  console.error(`[devhud.updater] generated ${generated.length} signed target manifests`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(`[devhud.updater] ${error.message}`);
    process.exit(1);
  }
}
