// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { X509Certificate } from "node:crypto";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

export function verifyAndroidSigning(
  artifact,
  expectedFingerprint,
  run = spawnSync,
) {
  const expected = expectedFingerprint
    ?.trim()
    .replaceAll(":", "")
    .toLowerCase();
  if (!/^[a-f0-9]{64}$/.test(expected ?? ""))
    throw new Error(
      "Set DELITOOLS_ANDROID_CERTIFICATE_SHA256 to the expected upload certificate SHA-256 fingerprint",
    );
  const result = run("keytool", ["-printcert", "-rfc", "-jarfile", artifact], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.error || result.status !== 0)
    throw new Error("Could not read the Android upload certificate");
  const pem = result.stdout.match(
    /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/,
  );
  if (!pem) throw new Error("The Android artifact has no signing certificate");
  const actual = new X509Certificate(pem[0]).fingerprint256
    .replaceAll(":", "")
    .toLowerCase();
  if (actual !== expected)
    throw new Error(
      "Android upload certificate does not match DELITOOLS_ANDROID_CERTIFICATE_SHA256",
    );
  return actual;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const sha256 = verifyAndroidSigning(
      process.argv[2],
      process.env.DELITOOLS_ANDROID_CERTIFICATE_SHA256,
    );
    console.log(
      JSON.stringify({ event: "android_upload_certificate_verified", sha256 }),
    );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
