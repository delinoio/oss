import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { cosignVerifyArguments, sigstoreVerificationPolicy, validateChecksums, validateNoSecrets, validateProvenance } from "./validate-devhud-private-build.mjs";

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

test("Sigstore verification binds bundles to the private packaging workflow identity", () => {
  const policy = sigstoreVerificationPolicy({
    GITHUB_WORKFLOW_REF: "delinoio/oss/.github/workflows/package-devhud-private.yml@refs/heads/main",
  });
  assert.deepEqual(policy, {
    certificateIdentity: "https://github.com/delinoio/oss/.github/workflows/package-devhud-private.yml@refs/heads/main",
    certificateOidcIssuer: "https://token.actions.githubusercontent.com",
  });
  assert.deepEqual(cosignVerifyArguments("artifact.sigstore.json", "artifact.bin", policy), [
    "verify-blob",
    "--bundle", "artifact.sigstore.json",
    "--certificate-identity", policy.certificateIdentity,
    "--certificate-oidc-issuer", policy.certificateOidcIssuer,
    "artifact.bin",
  ]);
  assert.throws(() => sigstoreVerificationPolicy({}), /requires the DevHud private packaging GitHub workflow ref/u);
  assert.throws(() => sigstoreVerificationPolicy({ GITHUB_WORKFLOW_REF: "other/repo/.github/workflows/package-devhud-private.yml@refs/heads/main" }), /requires the DevHud private packaging GitHub workflow ref/u);
  assert.throws(() => sigstoreVerificationPolicy({ GITHUB_WORKFLOW_REF: "delinoio/oss/.github/workflows/other.yml@refs/heads/main" }), /requires the DevHud private packaging GitHub workflow ref/u);
});

test("Sigstore verification accepts the exact called private workflow identity", () => {
  const policy = sigstoreVerificationPolicy({
    GITHUB_WORKFLOW_REF: "delinoio/oss/.github/workflows/release-devhud.yml@refs/heads/main",
    DEVHUD_PRIVATE_WORKFLOW_REF: "delinoio/oss/.github/workflows/package-devhud-private.yml@refs/heads/main",
  });
  assert.equal(policy.certificateIdentity, "https://github.com/delinoio/oss/.github/workflows/package-devhud-private.yml@refs/heads/main");
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

test("provenance validation binds the statement to the current workflow run attempt", () => {
  const digest = "a".repeat(64);
  const statement = {
    subject: [{ name: "artifact.bin", digest: { sha256: digest } }],
    predicate: {
      buildDefinition: { externalParameters: { revision: "a".repeat(40) } },
      runDetails: { metadata: {
        invocationId: "https://github.com/delinoio/oss/actions/runs/123456789/attempts/2",
        startedOn: "2026-08-25T10:00:00Z",
        finishedOn: "2026-08-25T10:30:00Z",
      } },
    },
  };
  const environment = { GITHUB_REPOSITORY: "delinoio/oss", GITHUB_SHA: "a".repeat(40), GITHUB_RUN_ID: "123456789", GITHUB_RUN_ATTEMPT: "2" };
  assert.doesNotThrow(() => validateProvenance(statement, "artifact.bin", digest, environment));
  assert.throws(() => validateProvenance(statement, "artifact.bin", digest, { ...environment, GITHUB_SHA: "b".repeat(40) }), /revision mismatch/u);
  assert.doesNotThrow(() => validateProvenance(statement, "artifact.bin", digest, { ...environment, GITHUB_SHA: "b".repeat(40), DEVHUD_RELEASE_REVISION: "a".repeat(40) }));
  assert.throws(() => validateProvenance(statement, "artifact.bin", digest, { ...environment, GITHUB_RUN_ATTEMPT: "3" }), /invocation mismatch/u);
  assert.doesNotThrow(() => validateProvenance(statement, "artifact.bin", digest, {
    GITHUB_REPOSITORY: "delinoio/oss",
    GITHUB_SHA: "a".repeat(40),
    GITHUB_RUN_ID: "999",
    GITHUB_RUN_ATTEMPT: "1",
    DEVHUD_PROVENANCE_RUN_ID: "123456789",
    DEVHUD_PROVENANCE_RUN_ATTEMPT: "2",
  }));
  assert.throws(() => validateProvenance(statement, "artifact.bin", digest, {
    GITHUB_REPOSITORY: "delinoio/oss",
    GITHUB_SHA: "a".repeat(40),
    DEVHUD_PROVENANCE_RUN_ID: "123456789",
    DEVHUD_PROVENANCE_RUN_ATTEMPT: "3",
  }), /invocation mismatch/u);
});
