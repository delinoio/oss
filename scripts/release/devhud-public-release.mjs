#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { loadReleaseMetadata, releasePlan, signingInputs, validateVersion } from "./devhud-release.mjs";

export const ReleaseMode = Object.freeze({ DryRun: "dry-run", Release: "release" });

export const ReleaseState = Object.freeze({
  Planned: "planned",
  PrivatelyValidated: "privately-validated",
  Preflighted: "preflighted",
  ReviewsSubmitted: "reviews-submitted",
  ReviewsPending: "reviews-pending",
  ReviewsApproved: "reviews-approved",
  InfrastructureReady: "infrastructure-ready",
  StoresPublic: "stores-public",
  GitHubPublic: "github-public",
  UpdaterPublic: "updater-public",
  DocsPublic: "docs-public",
  IndependentlyVerified: "independently-verified",
  GeneralAvailability: "ga",
});

export const ReleaseEvent = Object.freeze({
  ValidatePrivate: "validate-private",
  PassPreflight: "pass-preflight",
  SubmitReviews: "submit-reviews",
  WaitForReviews: "wait-for-reviews",
  ApproveReviews: "approve-reviews",
  PrepareInfrastructure: "prepare-infrastructure",
  PublishStores: "publish-stores",
  PublishGitHub: "publish-github",
  PublishUpdater: "publish-updater",
  PublishDocs: "publish-docs",
  VerifyAll: "verify-all",
  MarkGeneralAvailability: "mark-ga",
  RetryPendingReview: "retry-pending-review",
});

const transitions = new Map([
  [`${ReleaseState.Planned}:${ReleaseEvent.ValidatePrivate}`, ReleaseState.PrivatelyValidated],
  [`${ReleaseState.PrivatelyValidated}:${ReleaseEvent.PassPreflight}`, ReleaseState.Preflighted],
  [`${ReleaseState.Preflighted}:${ReleaseEvent.SubmitReviews}`, ReleaseState.ReviewsSubmitted],
  [`${ReleaseState.ReviewsSubmitted}:${ReleaseEvent.WaitForReviews}`, ReleaseState.ReviewsPending],
  [`${ReleaseState.ReviewsPending}:${ReleaseEvent.RetryPendingReview}`, ReleaseState.ReviewsPending],
  [`${ReleaseState.ReviewsPending}:${ReleaseEvent.ApproveReviews}`, ReleaseState.ReviewsApproved],
  [`${ReleaseState.ReviewsApproved}:${ReleaseEvent.PrepareInfrastructure}`, ReleaseState.InfrastructureReady],
  [`${ReleaseState.InfrastructureReady}:${ReleaseEvent.PublishStores}`, ReleaseState.StoresPublic],
  [`${ReleaseState.StoresPublic}:${ReleaseEvent.PublishGitHub}`, ReleaseState.GitHubPublic],
  [`${ReleaseState.GitHubPublic}:${ReleaseEvent.PublishUpdater}`, ReleaseState.UpdaterPublic],
  [`${ReleaseState.UpdaterPublic}:${ReleaseEvent.PublishDocs}`, ReleaseState.DocsPublic],
  [`${ReleaseState.DocsPublic}:${ReleaseEvent.VerifyAll}`, ReleaseState.IndependentlyVerified],
  [`${ReleaseState.IndependentlyVerified}:${ReleaseEvent.MarkGeneralAvailability}`, ReleaseState.GeneralAvailability],
]);

export const releaseVariables = Object.freeze([
  "DEVHUD_APP_STORE_APP_ID",
  "DEVHUD_GOOGLE_PLAY_PACKAGE_NAME",
  "DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING",
  "DEVHUD_GOOGLE_PLAY_PRODUCTION_RELEASE_SERVICE_ACCOUNT",
  "DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID",
  "DEVHUD_OCI_REGISTRY",
  "DEVHUD_OCI_API_REPOSITORY",
  "DEVHUD_OCI_SWEEPER_REPOSITORY",
  "DEVHUD_OCI_PRODUCTION_PUSH_PRINCIPAL",
  "DEVHUD_RELEASE_CONTROLLER_URL",
  "DEVHUD_RELEASE_CONTROLLER_AUDIENCE",
  "DEVHUD_PUBLIC_API_URL",
  "DEVHUD_LOGTO_ISSUER",
  "DEVHUD_PUBLIC_ASSET_BASE_URL",
  "DEVHUD_PUBLIC_DOCS_URL",
  "DEVHUD_PUBLIC_DOCS_ACCOUNT_ID",
  "DEVHUD_PUBLIC_DOCS_PROJECT_NAME",
]);

export const releaseFingerprintVariables = Object.freeze([
  ...releaseVariables,
  "DEVHUD_CHROME_EXTENSION_ID",
]);

export const releaseStoreIdentityVariables = Object.freeze([
  "DEVHUD_APP_STORE_APP_ID",
  "DEVHUD_GOOGLE_PLAY_PACKAGE_NAME",
  "DEVHUD_GOOGLE_PLAY_PRODUCTION_RELEASE_SERVICE_ACCOUNT",
  "DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID",
  "DEVHUD_CHROME_EXTENSION_ID",
]);

export const releaseSecrets = Object.freeze([
  "DEVHUD_CHROME_EXTENSION_PUBLIC_KEY",
  "APPLE_API_ISSUER",
  "APPLE_API_KEY_ID",
  "APPLE_API_PRIVATE_KEY_B64",
  "DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON",
  "DEVHUD_CHROME_WEB_STORE_CLIENT_ID",
  "DEVHUD_CHROME_WEB_STORE_CLIENT_SECRET",
  "DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN",
  "DEVHUD_OCI_REGISTRY_USERNAME",
  "DEVHUD_OCI_REGISTRY_TOKEN",
  "DEVHUD_PUBLIC_DOCS_API_TOKEN",
]);

export const controllerRuntimeInputs = Object.freeze([
  "DEVHUD_ENVIRONMENT",
  "DEVHUD_DATABASE_URL",
  "DEVHUD_PUBLIC_API_URL",
  "DEVHUD_LISTEN_ADDRESS",
  "DEVHUD_TRUSTED_PROXY_CIDRS",
  "DEVHUD_LOGTO_ISSUER",
  "DEVHUD_LOGTO_AUDIENCE",
  "DEVHUD_LOGTO_DESKTOP_CLIENT_ID",
  "DEVHUD_LOGTO_IOS_CLIENT_ID",
  "DEVHUD_LOGTO_ANDROID_CLIENT_ID",
  "DEVHUD_LOGTO_ADMIN_CLIENT_ID",
  "DEVHUD_ADMIN_REDIRECT_URI",
  "DEVHUD_IDENTITY_HMAC_KEYS",
  "DEVHUD_R2_ENDPOINT",
  "DEVHUD_R2_ACCESS_KEY_ID",
  "DEVHUD_R2_SECRET_ACCESS_KEY",
  "DEVHUD_R2_STAGING_BUCKET",
  "DEVHUD_R2_PUBLIC_BUCKET",
  "DEVHUD_PUBLIC_ASSET_BASE_URL",
  "DEVHUD_CLOUDFLARE_API_TOKEN",
  "DEVHUD_CLOUDFLARE_ZONE_ID",
  "DEVHUD_CLOUDFLARE_RATE_LIMIT_RULE_ID",
  "DEVHUD_UPDATE_MANIFEST_DIR",
  "DEVHUD_SWEEPER_BATCH_SIZE",
  "DEVHUD_SWEEPER_INTERVAL",
]);

export const livePreflightChecks = Object.freeze([
  "updater",
  "macos-notarization",
  "windows-signing",
  "app-store",
  "google-play",
  "chrome-web-store",
  "github",
  "logto",
  "postgresql",
  "r2",
  "asset-domain",
  "public-asset-authority",
  "oci-registry",
  "public-docs",
  "release-controller",
]);

const sensitiveNames = new Set([...signingInputs, ...releaseSecrets, ...controllerRuntimeInputs.filter((name) => /(?:KEY|TOKEN|SECRET|PASSWORD|DATABASE_URL)/u.test(name))]);
const productionApiOrigin = "https://devhud.api.delino.io";
const ociRepositoryNamePattern = /^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$/u;

function present(environment, name) {
  return typeof environment[name] === "string" && environment[name].trim() !== "";
}

export function configurationStatus(environment = process.env) {
  return {
    variables: releaseVariables.map((name) => ({ name, present: present(environment, name) })),
    secrets: releaseSecrets.map((name) => ({ name, present: present(environment, name) })),
    privateSigning: signingInputs.map((name) => ({ name, present: present(environment, name) })),
  };
}

function validateReleaseVariableValues(environment) {
  if (environment.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME !== "io.delino.devhud") throw new Error("Google Play package must be io.delino.devhud");
  if (environment.DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING !== "enabled") throw new Error("Google Play managed publishing must be explicitly enabled");
  for (const name of ["DEVHUD_RELEASE_CONTROLLER_URL", "DEVHUD_PUBLIC_API_URL", "DEVHUD_PUBLIC_DOCS_URL"]) {
    let parsed;
    try { parsed = new URL(environment[name]); } catch { throw new Error(`${name} must be an absolute HTTPS URL`); }
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error(`${name} must be a credential-free HTTPS URL`);
  }
  if (environment.DEVHUD_PUBLIC_API_URL !== productionApiOrigin) {
    throw new Error(`DEVHUD_PUBLIC_API_URL must exactly match the compiled production origin ${productionApiOrigin}`);
  }
  let registry;
  try { registry = new URL(`https://${environment.DEVHUD_OCI_REGISTRY}/`); } catch { throw new Error("DEVHUD_OCI_REGISTRY must be a host-only registry authority"); }
  if (registry.username || registry.password || registry.pathname !== "/" || registry.search || registry.hash || registry.host !== environment.DEVHUD_OCI_REGISTRY) {
    throw new Error("DEVHUD_OCI_REGISTRY must be a host-only registry authority without credentials, path, query, or fragment");
  }
  if (!ociRepositoryNamePattern.test(environment.DEVHUD_OCI_API_REPOSITORY) || !ociRepositoryNamePattern.test(environment.DEVHUD_OCI_SWEEPER_REPOSITORY)) {
    throw new Error("OCI repository names must use slash-separated OCI distribution name components");
  }
}

export function validateReleaseVariables(environment = process.env) {
  const missing = releaseFingerprintVariables.filter((name) => !present(environment, name));
  if (missing.length > 0) throw new Error(`DevHud release variables are missing: ${missing.join(", ")}`);
  validateReleaseVariableValues(environment);
}

export function validateReleaseConfiguration(environment = process.env) {
  const missing = [...releaseFingerprintVariables, ...releaseSecrets].filter((name) => !present(environment, name));
  if (missing.length > 0) throw new Error(`DevHud release configuration is missing: ${missing.join(", ")}`);
  validateReleaseVariableValues(environment);
}

export function releaseConfigurationFingerprint(environment = process.env) {
  validateReleaseVariables(environment);
  const values = releaseFingerprintVariables.map((name) => [name, environment[name]]);
  return createHash("sha256").update(JSON.stringify(values), "utf8").digest("hex");
}

export function releaseStoreIdentityFingerprint(environment = process.env) {
  const missing = releaseStoreIdentityVariables.filter((name) => !present(environment, name));
  if (missing.length > 0) throw new Error(`DevHud store identity variables are missing: ${missing.join(", ")}`);
  const values = releaseStoreIdentityVariables.map((name) => [name, environment[name]]);
  return createHash("sha256").update(JSON.stringify(values), "utf8").digest("hex");
}

function releaseCandidateIdentity({ artifactId, artifactName, runId, runAttempt }) {
  if (!/^[1-9]\d*$/u.test(String(artifactId)) || !/^[1-9]\d*$/u.test(String(runId)) || !/^[1-9]\d*$/u.test(String(runAttempt))) {
    throw new Error("release configuration candidate identity must use positive decimal integers");
  }
  if (typeof artifactName !== "string" || !/^[A-Za-z0-9._-]+$/u.test(artifactName)) throw new Error("release configuration candidate artifact name is invalid");
  return { artifactId: String(artifactId), artifactName, runId: String(runId), runAttempt: String(runAttempt) };
}

export function releaseConfigurationBinding({ version, revision, candidate, environment = process.env }) {
  validateVersion(version);
  if (!/^[a-f0-9]{40}$/u.test(revision)) throw new Error("release configuration revision must be an exact lowercase 40-hex commit");
  return {
    schemaVersion: 1,
    project: "devhud",
    version,
    revision,
    candidate: releaseCandidateIdentity(candidate),
    releaseConfigurationFingerprint: releaseConfigurationFingerprint(environment),
  };
}

export function validateReleaseConfigurationBinding(binding, expected) {
  const current = releaseConfigurationBinding(expected);
  if (binding === null || typeof binding !== "object" || Array.isArray(binding) || JSON.stringify(binding) !== JSON.stringify(current)) {
    throw new Error("retained release configuration does not match the selected candidate and protected environment");
  }
  return current;
}

export function validateReleaseIdentity({ requestedVersion, metadata = loadReleaseMetadata(), ref, sha, existingTag = null }) {
  const version = validateVersion(requestedVersion);
  if (version !== metadata.version) throw new Error(`requested DevHud version ${version} does not match release metadata ${metadata.version}`);
  if (ref !== "refs/heads/main") throw new Error("DevHud release execution is restricted to refs/heads/main");
  if (!/^[a-f0-9]{40}$/u.test(sha)) throw new Error("DevHud release source must be an exact lowercase 40-hex commit");
  const tag = `devhud@v${version}`;
  if (existingTag !== null && existingTag !== sha) throw new Error(`existing ${tag} does not target the release source commit`);
  return { version, tag, sha, retry: existingTag === sha };
}

export function validateLivePreflight(results) {
  if (results === null || typeof results !== "object" || Array.isArray(results)) throw new Error("live preflight results must be an object");
  const missing = livePreflightChecks.filter((name) => results[name] !== true);
  const unexpected = Object.keys(results).filter((name) => !livePreflightChecks.includes(name));
  if (missing.length > 0 || unexpected.length > 0) throw new Error(`live preflight is incomplete: missing=${missing.join(",")} unexpected=${unexpected.join(",")}`);
}

export function advanceRelease(state, event) {
  const next = transitions.get(`${state}:${event}`);
  if (!next) throw new Error(`forbidden DevHud release transition: ${state} + ${event}`);
  return next;
}

export function reviewEvent(results) {
  const providers = ["apple", "google-play", "chrome-web-store"];
  if (results === null || typeof results !== "object" || Array.isArray(results)) throw new Error("store review results must be an object");
  const unexpected = Object.keys(results).filter((name) => !providers.includes(name));
  const missing = providers.filter((name) => typeof results[name] !== "string");
  if (unexpected.length > 0 || missing.length > 0) throw new Error(`store review set is invalid: missing=${missing.join(",")} unexpected=${unexpected.join(",")}`);
  if (providers.some((name) => results[name] === "rejected")) throw new Error("a DevHud store review was rejected");
  return providers.every((name) => results[name] === "approved-held") ? ReleaseEvent.ApproveReviews : ReleaseEvent.RetryPendingReview;
}

export function rollbackPolicy(state) {
  const preStoreStates = new Set([
    ReleaseState.Planned, ReleaseState.PrivatelyValidated, ReleaseState.Preflighted,
    ReleaseState.ReviewsSubmitted, ReleaseState.ReviewsPending, ReleaseState.ReviewsApproved,
    ReleaseState.InfrastructureReady,
  ]);
  return preStoreStates.has(state)
    ? { automatic: true, action: "restore-previous-controller-release-and-withdraw-held-submissions" }
    : { automatic: false, action: "roll-forward-or-coordinated-emergency-withdrawal" };
}

function secretNeedles(environment) {
  const needles = [];
  for (const name of sensitiveNames) {
    const value = environment[name];
    if (typeof value !== "string" || value.length < 4) continue;
    needles.push(value);
    if (name.endsWith("_B64")) {
      const decoded = Buffer.from(value, "base64").toString("utf8");
      if (decoded.length >= 4) needles.push(decoded);
    }
  }
  return needles.sort((left, right) => right.length - left.length);
}

export function redact(value, environment = process.env) {
  const needles = secretNeedles(environment);
  const visit = (candidate, key = "") => {
    if (sensitiveNames.has(key)) return "[REDACTED]";
    if (Array.isArray(candidate)) return candidate.map((item) => visit(item));
    if (candidate && typeof candidate === "object") return Object.fromEntries(Object.entries(candidate).map(([name, item]) => [name, visit(item, name)]));
    if (typeof candidate !== "string") return candidate;
    return needles.reduce((text, needle) => text.replaceAll(needle, "[REDACTED]"), candidate);
  };
  return visit(value);
}

export function publicReleasePlan({ requestedVersion, ref, sha, mode = ReleaseMode.DryRun, environment = process.env, existingTag = null } = {}) {
  if (!Object.values(ReleaseMode).includes(mode)) throw new Error(`unsupported DevHud release mode: ${mode}`);
  const identity = validateReleaseIdentity({ requestedVersion, ref, sha, existingTag });
  const privatePlan = releasePlan({ environment, signed: false });
  return redact({
    schemaVersion: 1,
    project: "devhud",
    mode,
    identity,
    initialState: ReleaseState.Planned,
    channelOrder: ["private", "preflight", "store-review", "infrastructure", "stores", "github", "updater", "public-docs", "verification", "ga"],
    publicationEnabled: mode === ReleaseMode.Release,
    artifacts: privatePlan.artifacts,
    updaterTargets: privatePlan.updaterTargets,
    configuration: configurationStatus(environment),
  }, environment);
}

function argumentOptions(arguments_) {
  const command = arguments_.shift() ?? "plan";
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (!name?.startsWith("--") || value === undefined || value.startsWith("--")) throw new Error(`invalid argument: ${name ?? "missing"}`);
    options[name.slice(2)] = value;
  }
  return { command, options };
}

export function main(arguments_ = process.argv.slice(2), environment = process.env) {
  const { command, options } = argumentOptions([...arguments_]);
  if (command === "plan") {
    const result = publicReleasePlan({ requestedVersion: options.version, ref: options.ref, sha: options.sha, mode: options.mode, environment, existingTag: options["existing-tag"] ?? null });
    const serialized = `${JSON.stringify(result, null, 2)}\n`;
    if (options.output) writeFileSync(resolve(options.output), serialized); else process.stdout.write(serialized);
    return;
  }
  if (command === "preflight") {
    validateReleaseConfiguration(environment);
    validateReleaseIdentity({ requestedVersion: options.version, ref: options.ref, sha: options.sha, existingTag: options["existing-tag"] ?? null });
    validateLivePreflight(JSON.parse(readFileSync(resolve(options.checks), "utf8")));
    process.stderr.write("[devhud.release] complete live preflight accepted\n");
    return;
  }
  if (command === "transition") {
    process.stdout.write(`${advanceRelease(options.state, options.event)}\n`);
    return;
  }
  if (command === "rollback-policy") {
    if (!Object.values(ReleaseState).includes(options.state)) throw new Error(`unsupported release state: ${options.state}`);
    process.stdout.write(`${JSON.stringify(rollbackPolicy(options.state))}\n`);
    return;
  }
  if (command === "configuration-fingerprint") {
    process.stdout.write(`${releaseConfigurationFingerprint(environment)}\n`);
    return;
  }
  if (command === "store-identity-fingerprint") {
    process.stdout.write(`${releaseStoreIdentityFingerprint(environment)}\n`);
    return;
  }
  const configurationBindingOptions = {
    version: options.version,
    revision: options.revision,
    candidate: {
      artifactId: options["candidate-artifact-id"],
      artifactName: options["candidate-artifact-name"],
      runId: options["candidate-run-id"],
      runAttempt: options["candidate-run-attempt"],
    },
    environment,
  };
  if (command === "configuration-binding") {
    if (!options.output) throw new Error("--output is required");
    writeFileSync(resolve(options.output), `${JSON.stringify(releaseConfigurationBinding(configurationBindingOptions), null, 2)}\n`, { mode: 0o600 });
    return;
  }
  if (command === "validate-configuration-binding") {
    if (!options.input) throw new Error("--input is required");
    validateReleaseConfigurationBinding(JSON.parse(readFileSync(resolve(options.input), "utf8")), configurationBindingOptions);
    process.stderr.write("[devhud.release] retained release configuration accepted\n");
    return;
  }
  throw new Error(`unsupported command: ${command}`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); } catch (error) {
    process.stderr.write(`[devhud.release] ${redact(String(error.message))}\n`);
    process.exit(1);
  }
}
