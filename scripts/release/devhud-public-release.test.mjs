import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  ReleaseEvent, ReleaseMode, ReleaseState, advanceRelease, configurationStatus,
  controllerRuntimeInputs, livePreflightChecks, publicReleasePlan, redact,
  releaseConfigurationBinding, releaseConfigurationFingerprint, releaseFingerprintVariables, releaseSecrets, releaseVariables, reviewEvent,
  releaseStoreIdentityFingerprint, releaseStoreIdentityVariables, rollbackPolicy, validateLivePreflight, validateReleaseConfiguration,
  validateReleaseConfigurationBinding, validateReleaseIdentity, validateReleaseVariables,
} from "./devhud-public-release.mjs";
import { signingInputs } from "./devhud-release.mjs";

const fixture = JSON.parse(readFileSync(fileURLToPath(new URL("fixtures/devhud-public-release.json", import.meta.url)), "utf8"));
const sha = "a".repeat(40);

function completeEnvironment() {
  const environment = Object.fromEntries([...releaseVariables, ...releaseSecrets, ...signingInputs].map((name) => [name, `configured-${name.toLowerCase()}`]));
  environment.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME = "io.delino.devhud";
  environment.DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING = "enabled";
  environment.DEVHUD_RELEASE_CONTROLLER_URL = "https://release-controller.example.test/v1";
  environment.DEVHUD_PUBLIC_API_URL = "https://devhud.api.delino.io";
  environment.DEVHUD_PUBLIC_DOCS_URL = "https://docs.example.test/devhud";
  environment.DEVHUD_OCI_REGISTRY = "registry.example.test";
  environment.DEVHUD_OCI_API_REPOSITORY = "devhud/api";
  environment.DEVHUD_OCI_SWEEPER_REPOSITORY = "devhud/sweeper";
  return environment;
}

test("release identity is exact and permits only a same-commit idempotent retry", () => {
  assert.deepEqual(validateReleaseIdentity({ requestedVersion: "0.1.0", ref: "refs/heads/main", sha }), { version: "0.1.0", tag: "devhud@v0.1.0", sha, retry: false });
  assert.equal(validateReleaseIdentity({ requestedVersion: "0.1.0", ref: "refs/heads/main", sha, existingTag: sha }).retry, true);
  for (const requestedVersion of fixture.identityRejections) assert.throws(() => validateReleaseIdentity({ requestedVersion, ref: "refs/heads/main", sha }));
  assert.throws(() => validateReleaseIdentity({ requestedVersion: "0.1.0", ref: "refs/tags/devhud@v0.1.0", sha }), /main/u);
  assert.throws(() => validateReleaseIdentity({ requestedVersion: "0.1.0", ref: "refs/heads/main", sha, existingTag: "b".repeat(40) }), /does not target/u);
});

test("missing release credentials fail closed without exposing values", () => {
  const environment = completeEnvironment();
  const liveConfiguration = [...new Set([...releaseFingerprintVariables, ...releaseSecrets])];
  assert.doesNotThrow(() => validateReleaseConfiguration(environment));
  assert.throws(() => validateReleaseConfiguration({ ...environment, DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING: "disabled" }), /managed publishing/u);
  for (const name of liveConfiguration) {
    const missing = { ...environment, [name]: "" };
    assert.throws(() => validateReleaseConfiguration(missing), new RegExp(name, "u"));
  }
  const withoutPrivateSigning = { ...environment };
  for (const name of signingInputs.filter((name) => !liveConfiguration.includes(name))) withoutPrivateSigning[name] = "";
  assert.doesNotThrow(() => validateReleaseConfiguration(withoutPrivateSigning));
  for (const value of ["http://api.example.test", "https://user:password@api.example.test"]) {
    assert.throws(() => validateReleaseConfiguration({ ...environment, DEVHUD_PUBLIC_API_URL: value }), /DEVHUD_PUBLIC_API_URL must be a credential-free HTTPS URL/u);
  }
  for (const value of [
    "https://staging.devhud.api.delino.io",
    "https://devhud.api.delino.io/",
    "https://devhud.api.delino.io/v1",
    "https://devhud.api.delino.io:444",
  ]) {
    assert.throws(() => validateReleaseConfiguration({ ...environment, DEVHUD_PUBLIC_API_URL: value }), /must exactly match the compiled production origin/u);
  }
  for (const value of [
    "trusted.example@attacker.example",
    "registry.example.test/path",
    "registry.example.test?scope=push",
    "registry.example.test#credentials",
    "https://registry.example.test",
  ]) {
    assert.throws(() => validateReleaseConfiguration({ ...environment, DEVHUD_OCI_REGISTRY: value }), /host-only registry authority/u);
  }
  const serialized = JSON.stringify(configurationStatus(environment));
  assert.ok(!serialized.includes("configured-"));
});

test("release variable fingerprints bind protected environments without exposing values", () => {
  const environment = completeEnvironment();
  const fingerprint = releaseConfigurationFingerprint(environment);
  assert.match(fingerprint, /^[a-f0-9]{64}$/u);
  assert.doesNotThrow(() => validateReleaseVariables(environment));
  assert.ok(releaseFingerprintVariables.includes("DEVHUD_CHROME_EXTENSION_ID"));
  for (const name of releaseFingerprintVariables) {
    assert.throws(() => assert.equal(releaseConfigurationFingerprint({ ...environment, [name]: `${environment[name]}-changed` }), fingerprint), undefined, name);
  }

  const script = fileURLToPath(new URL("devhud-public-release.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [script, "configuration-fingerprint"], { encoding: "utf8", env: environment });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), fingerprint);
});

test("store identity fingerprints bind review and cleanup targets", () => {
  const environment = completeEnvironment();
  const fingerprint = releaseStoreIdentityFingerprint(environment);
  assert.match(fingerprint, /^[a-f0-9]{64}$/u);
  assert.deepEqual(releaseStoreIdentityVariables, [
    "DEVHUD_APP_STORE_APP_ID",
    "DEVHUD_GOOGLE_PLAY_PACKAGE_NAME",
    "DEVHUD_GOOGLE_PLAY_PRODUCTION_RELEASE_SERVICE_ACCOUNT",
    "DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID",
    "DEVHUD_CHROME_EXTENSION_ID",
  ]);
  for (const name of releaseStoreIdentityVariables) {
    assert.notEqual(releaseStoreIdentityFingerprint({ ...environment, [name]: `${environment[name]}-changed` }), fingerprint, name);
  }
  assert.throws(() => releaseStoreIdentityFingerprint({ ...environment, DEVHUD_APP_STORE_APP_ID: "" }), /DEVHUD_APP_STORE_APP_ID/u);

  const script = fileURLToPath(new URL("devhud-public-release.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [script, "store-identity-fingerprint"], { encoding: "utf8", env: environment });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), fingerprint);
});

test("retained release configuration binds the candidate without exposing destinations", () => {
  const environment = completeEnvironment();
  const expected = {
    version: "0.1.0",
    revision: sha,
    candidate: { artifactId: "501", artifactName: `devhud-v0.1.0-private-signed-candidate-${sha}-100-1`, runId: "100", runAttempt: "1" },
    environment,
  };
  const binding = releaseConfigurationBinding(expected);
  assert.equal(binding.releaseConfigurationFingerprint, releaseConfigurationFingerprint(environment));
  assert.ok(!JSON.stringify(binding).includes("configured-"));
  assert.doesNotThrow(() => validateReleaseConfigurationBinding(binding, expected));
  assert.throws(() => validateReleaseConfigurationBinding(binding, { ...expected, environment: { ...environment, DEVHUD_PUBLIC_DOCS_URL: "https://other.example.test/devhud" } }), /does not match/u);
  assert.throws(() => validateReleaseConfigurationBinding({ ...binding, unexpected: true }, expected), /does not match/u);
  assert.throws(() => releaseConfigurationBinding({ ...expected, candidate: { ...expected.candidate, artifactId: "" } }), /positive decimal/u);
});

test("live preflight requires every closed check and rejects additions", () => {
  assert.deepEqual(Object.keys(fixture.livePreflight), [...livePreflightChecks]);
  assert.doesNotThrow(() => validateLivePreflight(fixture.livePreflight));
  assert.throws(() => validateLivePreflight({ ...fixture.livePreflight, r2: false }), /r2/u);
  assert.throws(() => validateLivePreflight({ ...fixture.livePreflight, invented: true }), /unexpected=invented/u);
});

test("release channel ordering, review delay, and GA barrier are closed", () => {
  const ordered = [
    ReleaseEvent.ValidatePrivate, ReleaseEvent.PassPreflight, ReleaseEvent.SubmitReviews,
    ReleaseEvent.WaitForReviews, ReleaseEvent.ApproveReviews, ReleaseEvent.PrepareInfrastructure,
    ReleaseEvent.PublishStores, ReleaseEvent.PublishGitHub, ReleaseEvent.PublishUpdater,
    ReleaseEvent.PublishDocs, ReleaseEvent.VerifyAll, ReleaseEvent.MarkGeneralAvailability,
  ];
  let state = ReleaseState.Planned;
  for (const event of ordered) state = advanceRelease(state, event);
  assert.equal(state, ReleaseState.GeneralAvailability);
  assert.equal(advanceRelease(ReleaseState.ReviewsPending, ReleaseEvent.RetryPendingReview), ReleaseState.ReviewsPending);
  assert.equal(reviewEvent(fixture.pendingReviewResults), ReleaseEvent.RetryPendingReview);
  assert.equal(reviewEvent(fixture.validReviewResults), ReleaseEvent.ApproveReviews);
  assert.throws(() => reviewEvent({ ...fixture.validReviewResults, apple: "rejected" }), /rejected/u);
  assert.throws(() => advanceRelease(ReleaseState.ReviewsPending, ReleaseEvent.PublishStores), /forbidden/u);
  assert.throws(() => advanceRelease(ReleaseState.UpdaterPublic, ReleaseEvent.MarkGeneralAvailability), /forbidden/u);
});

test("rollback becomes roll-forward-only at the first store publication", () => {
  assert.equal(rollbackPolicy(ReleaseState.InfrastructureReady).automatic, true);
  for (const state of [ReleaseState.StoresPublic, ReleaseState.GitHubPublic, ReleaseState.UpdaterPublic, ReleaseState.DocsPublic, ReleaseState.IndependentlyVerified]) {
    assert.deepEqual(rollbackPolicy(state), { automatic: false, action: "roll-forward-or-coordinated-emergency-withdrawal" });
  }
});

test("rollback policy is available through the direct CLI", () => {
  const script = fileURLToPath(new URL("devhud-public-release.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [script, "rollback-policy", "--state", ReleaseState.InfrastructureReady], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), rollbackPolicy(ReleaseState.InfrastructureReady));
});

test("plans and errors redact secret values including decoded base64", () => {
  const environment = completeEnvironment();
  environment.DEVHUD_UPDATER_SIGNING_KEY_B64 = Buffer.from("private-updater-material").toString("base64");
  environment.DEVHUD_OCI_REGISTRY_TOKEN = "registry-token-value";
  const result = redact({ message: `tokens registry-token-value private-updater-material`, DEVHUD_OCI_REGISTRY_TOKEN: "registry-token-value" }, environment);
  const serialized = JSON.stringify(result);
  assert.ok(!serialized.includes("registry-token-value"));
  assert.ok(!serialized.includes("private-updater-material"));
  const plan = publicReleasePlan({ requestedVersion: "0.1.0", ref: "refs/heads/main", sha, mode: ReleaseMode.DryRun, environment });
  assert.equal(plan.publicationEnabled, false);
  assert.ok(!JSON.stringify(plan).includes("configured-"));
});

test("controller runtime contract lists names only", () => {
  assert.ok(controllerRuntimeInputs.includes("DEVHUD_DATABASE_URL"));
  assert.ok(controllerRuntimeInputs.includes("DEVHUD_UPDATE_MANIFEST_DIR"));
  assert.equal(new Set(controllerRuntimeInputs).size, controllerRuntimeInputs.length);
});
