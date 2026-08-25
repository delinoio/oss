import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { validateChecksums, validateNoSecrets } from "./validate-devhud-private-build.mjs";

const checksumScript = fileURLToPath(new URL("./generate-checksums.sh", import.meta.url));

test("validates a complete deterministic checksum manifest without Sigstore", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-validation-checksum-"));
  mkdirSync(join(root, "nested"));
  writeFileSync(join(root, "artifact.bin"), "artifact");
  writeFileSync(join(root, "nested/signature.ed25519"), "signature");
  execFileSync("bash", [checksumScript, "--artifacts-dir", root, "--sigstore-dir", join(root, "sigstore")], { env: { ...process.env, REQUIRE_COSIGN: "0" } });
  assert.doesNotThrow(() => validateChecksums(root, false));
  writeFileSync(join(root, "artifact.bin"), "tampered");
  assert.throws(() => validateChecksums(root, false), /invalid SHA256SUMS/u);
});

test("secret scanning covers raw and decoded signing values", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-validation-secret-"));
  writeFileSync(join(root, "safe.txt"), "safe output");
  assert.doesNotThrow(() => validateNoSecrets(root, { DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD: "long-secret-value" }));
  writeFileSync(join(root, "leaked.txt"), "prefix long-secret-value suffix");
  assert.throws(() => validateNoSecrets(root, { DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD: "long-secret-value" }), /secret material/u);
  writeFileSync(join(root, "leaked.txt"), Buffer.from("decoded-private-key"));
  assert.throws(() => validateNoSecrets(root, { DEVHUD_UPDATER_SIGNING_KEY_B64: Buffer.from("decoded-private-key").toString("base64") }), /secret material/u);
});
