// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { existsSync, readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
export const Identity = "io.delino.delidev.mobile";
const digest = (value) => createHash("sha256").update(value).digest("hex");
export function validateInputs(input) {
  if (
    !input ||
    input.identity !== Identity ||
    !/^\d+\.\d+\.\d+$/.test(input.version) ||
    !/^[1-9]\d{0,17}$/.test(input.iosBuild) ||
    !/^[1-9]\d{0,9}$/.test(input.androidCode) ||
    Number(input.androidCode) < 1 ||
    Number(input.androidCode) > 2100000000 ||
    !/^[a-f0-9]{40}$/.test(input.sourceSha) ||
    input.appleGroupType !== "INTERNAL" ||
    input.playTrack !== "internal"
  )
    throw new Error("Invalid internal beta candidate input");
  if (
    !/^[a-f0-9]{64}$/.test(input.appleSigner) ||
    !/^[a-f0-9]{64}$/.test(input.androidSigner)
  )
    throw new Error("Expected signing fingerprints are required");
  return input;
}
export function candidate(input, ios, android) {
  validateInputs(input);
  for (const [artifact, platform, version, signer] of [
    [ios, "ios", input.iosBuild, input.appleSigner],
    [android, "android", input.androidCode, input.androidSigner],
  ]) {
    if (
      !artifact ||
      artifact.identity !== Identity ||
      artifact.version !== input.version ||
      artifact.build !== version ||
      artifact.signer !== signer ||
      artifact.sourceSha !== input.sourceSha ||
      !Buffer.isBuffer(artifact.bytes) ||
      artifact.bytes.length === 0 ||
      artifact.architectures.join(",") !==
        (platform === "ios" ? "arm64" : "arm64-v8a,armeabi-v7a")
    )
      throw new Error(
        "Candidate identity, version, signer or architecture mismatch",
      );
  }
  const manifest = {
    schema: 1,
    identity: Identity,
    sourceSha: input.sourceSha,
    version: input.version,
    iosBuild: input.iosBuild,
    androidCode: input.androidCode,
    appleGroupType: "INTERNAL",
    playTrack: "internal",
    artifacts: {
      ios: {
        sha256: digest(ios.bytes),
        bytes: ios.bytes.length,
        signer: ios.signer,
      },
      android: {
        sha256: digest(android.bytes),
        bytes: android.bytes.length,
        signer: android.signer,
      },
    },
  };
  return { ...manifest, candidateId: digest(JSON.stringify(manifest)) };
}
export function retainCandidate(path, manifest) {
  const body = JSON.stringify(manifest, null, 2) + "\n";
  if (existsSync(path)) {
    if (readFileSync(path, "utf8") !== body)
      throw new Error(
        "Candidate/version reuse conflicts with the retained manifest",
      );
    return;
  }
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, body, { flag: "wx", mode: 0o600 });
}
export const Stage = Object.freeze({
  Ready: "ready",
  Sending: "sending",
  Unknown: "unknown",
  Uploaded: "uploaded",
  Distributed: "distributed",
});
/** Provider adapters inspect the exact identity, version, hash and internal group. */
export async function distribute(
  manifest,
  receipt,
  provider,
  checkpoint,
  { dryRun = true } = {},
) {
  if (
    !manifest?.candidateId ||
    !receipt ||
    receipt.candidateId !== manifest.candidateId ||
    !["ios", "android"].includes(receipt.platform)
  )
    throw new Error("Original candidate receipt is required");
  const artifact = manifest.artifacts[receipt.platform];
  if (!artifact) throw new Error("Candidate artifact missing");
  if (dryRun)
    return {
      operation: "beta-preflight",
      candidateId: manifest.candidateId,
      platform: receipt.platform,
      outcome: "dry-run",
    };
  await provider.preflight(manifest); // Must validate account, signer and INTERNAL-only target before any mutation.
  if (receipt.stage === Stage.Unknown || receipt.stage === Stage.Sending) {
    const original = await provider.inspect(manifest, receipt);
    if (
      original.state !== "present" ||
      original.sha256 !== artifact.sha256 ||
      original.identity !== Identity ||
      original.internal !== true
    )
      throw new Error(
        "Unknown upload requires exact provider reconciliation; no automatic retry",
      );
    receipt = { ...receipt, stage: Stage.Uploaded, providerId: original.id };
    await checkpoint(receipt);
  }
  if (receipt.stage === Stage.Ready) {
    receipt = { ...receipt, stage: Stage.Sending };
    await checkpoint(receipt);
    try {
      const result = await provider.upload(manifest, receipt);
      if (
        result.sha256 !== artifact.sha256 ||
        result.identity !== Identity ||
        result.internal !== true
      )
        throw new Error("Upload proof mismatch");
      receipt = { ...receipt, stage: Stage.Uploaded, providerId: result.id };
      await checkpoint(receipt);
    } catch {
      await checkpoint({ ...receipt, stage: Stage.Unknown });
      throw new Error(
        "Upload outcome unknown; retain original candidate and reconcile before retry",
      );
    }
  }
  if (receipt.stage === Stage.Uploaded) {
    // Assigning the existing original build is receipt-safe. An uncertain result
    // must be inspected, never replaced by another upload or public track.
    const current = await provider.inspect(manifest, receipt);
    if (
      current.id !== receipt.providerId ||
      current.sha256 !== artifact.sha256 ||
      current.internal !== true
    )
      throw new Error("Original uploaded build is not verified");
    if (!current.distributed) await provider.assignInternal(manifest, receipt);
    const final = await provider.inspect(manifest, receipt);
    if (
      final.id !== receipt.providerId ||
      final.sha256 !== artifact.sha256 ||
      !final.distributed ||
      final.internal !== true
    )
      throw new Error("Internal distribution remains uncertain");
    receipt = { ...receipt, stage: Stage.Distributed };
    await checkpoint(receipt);
  }
  return receipt;
}
export function credentials(environment) {
  for (const name of [
    "DELIDEV_MOBILE_APPLE_ISSUER",
    "DELIDEV_MOBILE_APPLE_KEY_ID",
    "DELIDEV_MOBILE_APPLE_PRIVATE_KEY",
    "DELIDEV_MOBILE_APPLE_TEAM",
    "DELIDEV_MOBILE_APPLE_APP_ID",
    "DELIDEV_MOBILE_APPLE_INTERNAL_GROUP",
    "DELIDEV_MOBILE_GOOGLE_SERVICE_ACCOUNT",
    "DELIDEV_MOBILE_GOOGLE_PRINCIPAL",
    "DELIDEV_MOBILE_ANDROID_KEYSTORE",
    "DELIDEV_MOBILE_ANDROID_KEY_ALIAS",
    "DELIDEV_MOBILE_ANDROID_KEY_PASSWORD",
    "DELIDEV_MOBILE_ANDROID_STORE_PASSWORD",
  ]) {
    if (typeof environment[name] !== "string" || !environment[name].trim())
      throw new Error("Protected beta credentials are incomplete");
  }
  let account;
  try {
    account = JSON.parse(environment.DELIDEV_MOBILE_GOOGLE_SERVICE_ACCOUNT);
  } catch {
    throw new Error("Invalid protected Google identity");
  }
  if (
    account.type !== "service_account" ||
    account.token_uri !== "https://oauth2.googleapis.com/token" ||
    !account.private_key?.includes("PRIVATE KEY") ||
    !account.client_email?.endsWith(".gserviceaccount.com")
  )
    throw new Error("Invalid protected Google identity");
  return true;
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    if (process.argv.slice(2).join(" ") !== "--dry-run")
      throw new Error(
        "This entry point requires --dry-run; live provider adapters require the protected beta workflow",
      );
    const result = await distribute(
      { candidateId: "fixture", artifacts: { ios: { sha256: "fixture" } } },
      { candidateId: "fixture", platform: "ios", stage: Stage.Ready },
      {},
      () => {},
      { dryRun: true },
    );
    process.stdout.write(JSON.stringify(result) + "\n");
  } catch {
    process.stderr.write("Internal beta validation failed.\n");
    process.exitCode = 1;
  }
}
