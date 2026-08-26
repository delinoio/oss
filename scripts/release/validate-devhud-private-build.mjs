#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash, createPublicKey, verify } from "node:crypto";
import { closeSync, existsSync, openSync, readFileSync, readSync, readdirSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { artifactGroups, loadReleaseMetadata, repositoryRoot, updaterTargets, validateExtensionParity } from "./devhud-release.mjs";
import { validateEvidenceEntries } from "./devhud-evidence.mjs";
import { validateProvenanceMetadata, validateSpdx } from "./generate-devhud-supply-chain.mjs";
import { assertUpdaterArtifactSize } from "./generate-devhud-updater.mjs";

const ARTIFACT_DOMAIN = Buffer.from("devhud-update-artifact-v1\0", "utf8");
const MANIFEST_DOMAIN = Buffer.from("devhud-update-manifest-v1\0", "utf8");
const GITHUB_ACTIONS_OIDC_ISSUER = "https://token.actions.githubusercontent.com";
const PRIVATE_WORKFLOW_REF = /^delinoio\/oss\/\.github\/workflows\/package-devhud-private\.yml@refs\/(?:heads|tags)\/[^\s]+$/u;
const primaryArtifacts = Object.values(artifactGroups).flat();
const secretNames = [
  "DEVHUD_UPDATER_SIGNING_KEY_B64", "DEVHUD_MACOS_DEVELOPER_ID_P12_B64", "DEVHUD_MACOS_DEVELOPER_ID_P12_PASSWORD",
  "APPLE_API_PRIVATE_KEY_B64", "DEVHUD_WINDOWS_SIGNING_PFX_B64", "DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD",
  "DEVHUD_IOS_DISTRIBUTION_P12_B64", "DEVHUD_IOS_DISTRIBUTION_P12_PASSWORD", "DEVHUD_ANDROID_UPLOAD_KEYSTORE_B64",
  "DEVHUD_ANDROID_KEYSTORE_PASSWORD", "DEVHUD_ANDROID_KEY_PASSWORD",
];

function sha256(bytes) { return createHash("sha256").update(bytes).digest("hex"); }
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
function json(path) { return JSON.parse(readFileSync(path, "utf8")); }

function files(root) {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const path = join(root, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  });
}

export function validateUpdater(root, trustRoot = json(join(repositoryRoot, "apps/devhud/updater-trust-root.json"))) {
  if (trustRoot.productionReady !== true) throw new Error("updater trust root is not production-ready");
  const spkiPrefix = Buffer.from("302a300506032b6570032100", "hex");
  const publicKey = createPublicKey({ key: Buffer.concat([spkiPrefix, Buffer.from(trustRoot.publicKey, "base64")]), format: "der", type: "spki" });
  for (const target of updaterTargets) {
    const base = join("stable", target.platform, target.architecture);
    const envelope = json(join(root, "updater/manifests", base, `${target.packageKind}.json`));
    const payloadBytes = Buffer.from(envelope.signedPayload, "base64");
    const manifestSignature = Buffer.from(envelope.manifestSignature, "base64");
    if (!verify(null, Buffer.concat([MANIFEST_DOMAIN, payloadBytes]), publicKey, manifestSignature)) throw new Error(`invalid updater manifest signature: ${target.id}`);
    const payload = JSON.parse(payloadBytes);
    const artifactPath = join(root, target.artifact);
    assertUpdaterArtifactSize(statSync(artifactPath).size, target.artifact);
    const artifact = readFileSync(artifactPath);
    assertUpdaterArtifactSize(artifact.length, target.artifact);
    const artifactSignature = Buffer.from(payload.artifact.signature, "base64");
    if (payload.version !== loadReleaseMetadata().version || payload.platform !== target.platform || payload.architecture !== target.architecture || payload.packageKind !== target.packageKind) throw new Error(`updater target metadata mismatch: ${target.id}`);
    if (payload.artifact.size !== artifact.length || payload.artifact.sha256 !== sha256(artifact)) throw new Error(`updater artifact digest mismatch: ${target.id}`);
    if (!verify(null, Buffer.concat([ARTIFACT_DOMAIN, artifact]), publicKey, artifactSignature)) throw new Error(`invalid updater artifact signature: ${target.id}`);
    const detachedArtifact = readFileSync(join(root, "updater/signatures", base, `${target.packageKind}.artifact.ed25519`), "utf8").trim();
    const detachedManifest = readFileSync(join(root, "updater/signatures", base, `${target.packageKind}.manifest.ed25519`), "utf8").trim();
    if (detachedArtifact !== payload.artifact.signature || detachedManifest !== envelope.manifestSignature) throw new Error(`detached updater signature mismatch: ${target.id}`);
  }
}

export function validateEvidence(root) {
  const evidence = json(join(root, "validation/evidence.json"));
  if (evidence.schemaVersion !== 1 || evidence.readiness !== "private-signed-candidate" || evidence.publicReady !== false) throw new Error("validation evidence must identify a non-public private signed candidate");
  validateEvidenceEntries(evidence.targets ?? []);
}

export function validateProvenance(statement, artifact, digest, environment = process.env) {
  if (statement.subject?.[0]?.name !== artifact || statement.subject[0].digest?.sha256 !== digest) {
    throw new Error(`provenance subject mismatch: ${artifact}`);
  }
  const metadata = validateProvenanceMetadata(statement.predicate?.runDetails?.metadata ?? {});
  const { GITHUB_REPOSITORY: repository, GITHUB_RUN_ID: runId, GITHUB_RUN_ATTEMPT: runAttempt } = environment;
  if (repository !== "delinoio/oss" || !/^[1-9]\d*$/u.test(runId ?? "") || !/^[1-9]\d*$/u.test(runAttempt ?? "")) {
    throw new Error("provenance validation requires the current delinoio/oss GitHub run attempt");
  }
  const expectedInvocationId = `https://github.com/${repository}/actions/runs/${runId}/attempts/${runAttempt}`;
  if (metadata.invocationId !== expectedInvocationId) throw new Error(`provenance invocation mismatch: ${artifact}`);
}

export function sigstoreVerificationPolicy(environment = process.env) {
  const workflowRef = environment.DEVHUD_PRIVATE_WORKFLOW_REF ?? environment.GITHUB_WORKFLOW_REF;
  if (typeof workflowRef !== "string" || !PRIVATE_WORKFLOW_REF.test(workflowRef)) {
    throw new Error("Sigstore verification requires the DevHud private packaging GitHub workflow ref");
  }
  return {
    certificateIdentity: `https://github.com/${workflowRef}`,
    certificateOidcIssuer: GITHUB_ACTIONS_OIDC_ISSUER,
  };
}

export function cosignVerifyArguments(bundlePath, artifactPath, policy) {
  return [
    "verify-blob",
    "--bundle", bundlePath,
    "--certificate-identity", policy.certificateIdentity,
    "--certificate-oidc-issuer", policy.certificateOidcIssuer,
    artifactPath,
  ];
}

export function validateChecksums(root, verifySigstore = true, environment = process.env) {
  const manifestPath = join(root, "SHA256SUMS");
  const sigstorePolicy = verifySigstore ? sigstoreVerificationPolicy(environment) : undefined;
  const lines = readFileSync(manifestPath, "utf8").trim().split("\n");
  const actual = new Map(files(root)
    .map((path) => relative(root, path).replaceAll("\\", "/"))
    .filter((path) => path !== "SHA256SUMS" && !path.endsWith(".sigstore.json") && !path.startsWith("sigstore/"))
    .sort()
    .map((path) => [path, sha256File(join(root, path))]));
  if (lines.length !== actual.size) throw new Error("SHA256SUMS entry count does not match release files");
  for (const line of lines) {
    const match = /^([a-f0-9]{64})  (.+)$/u.exec(line);
    if (!match || actual.get(match[2]) !== match[1]) throw new Error(`invalid SHA256SUMS entry: ${line}`);
    if (verifySigstore) execFileSync("cosign", cosignVerifyArguments(join(root, "sigstore", `${match[2]}.sigstore.json`), join(root, match[2]), sigstorePolicy), { stdio: "inherit" });
    actual.delete(match[2]);
  }
  if (actual.size > 0) throw new Error("SHA256SUMS omits release files");
  if (verifySigstore) execFileSync("cosign", cosignVerifyArguments(join(root, "sigstore/SHA256SUMS.sigstore.json"), manifestPath, sigstorePolicy), { stdio: "inherit" });
}

export function validateNoSecrets(root, environment = process.env) {
  const needles = ["-----BEGIN PRIVATE KEY-----", "-----BEGIN ENCRYPTED PRIVATE KEY-----"].map((value) => Buffer.from(value));
  for (const name of secretNames) {
    const value = environment[name];
    if (!value || value.length < 8) continue;
    needles.push(Buffer.from(value));
    if (name.endsWith("_B64")) {
      const decoded = Buffer.from(value, "base64");
      if (decoded.length >= 8) needles.push(decoded);
    }
  }
  for (const path of files(root)) {
    if (statSync(path).size === 0) continue;
    const descriptor = openSync(path, "r");
    const buffer = Buffer.allocUnsafe(1024 * 1024);
    const overlap = Math.max(...needles.map(({ length }) => length)) - 1;
    let carry = Buffer.alloc(0);
    try {
      for (;;) {
        const count = readSync(descriptor, buffer, 0, buffer.length, null);
        if (count === 0) break;
        const bytes = Buffer.concat([carry, buffer.subarray(0, count)]);
        for (const needle of needles) if (bytes.includes(needle)) throw new Error(`signing secret material found in ${relative(root, path)}`);
        carry = bytes.subarray(Math.max(0, bytes.length - overlap));
      }
    } finally {
      closeSync(descriptor);
    }
  }
}

export function validatePrivateBuild(root, { verifySigstore = true, environment = process.env } = {}) {
  for (const artifact of primaryArtifacts) {
    const path = join(root, artifact);
    if (!existsSync(path) || statSync(path).size === 0) throw new Error(`missing or empty artifact: ${artifact}`);
    const sbom = json(join(root, "sbom", `${artifact}.spdx.json`));
    validateSpdx(sbom, artifact);
    const statement = json(join(root, "provenance", `${artifact}.intoto.jsonl`));
    validateProvenance(statement, artifact, sha256File(path), environment);
  }
  validateExtensionParity(root);
  validateUpdater(root);
  validateEvidence(root);
  validateChecksums(root, verifySigstore, environment);
  validateNoSecrets(root, environment);
}

function main(arguments_) {
  if (arguments_.length !== 2 || arguments_[0] !== "--artifacts-dir") throw new Error("usage: validate-devhud-private-build.mjs --artifacts-dir <dir>");
  validatePrivateBuild(resolve(arguments_[1]));
  console.error(`[devhud.validate] validated ${primaryArtifacts.length} private signed artifacts`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(process.argv.slice(2)); } catch (error) {
    console.error(`[devhud.validate] ${error.message}`);
    process.exit(1);
  }
}
