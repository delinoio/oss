import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { releaseVariables } from "./devhud-public-release.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const release = readFileSync(`${root}/.github/workflows/release-devhud.yml`, "utf8");
const privateCandidate = readFileSync(`${root}/.github/workflows/package-devhud-private.yml`, "utf8");
const releaseMetadata = JSON.parse(readFileSync(`${root}/packaging/devhud/release-metadata.json`, "utf8"));

function job(name) {
  const start = release.indexOf(`\n  ${name}:`);
  assert.notEqual(start, -1, `missing ${name} job`);
  const remainder = release.slice(start + name.length + 5);
  const nextMatch = remainder.match(/^  [a-z][a-z_]+:/mu);
  const next = nextMatch ? start + name.length + 5 + nextMatch.index - 1 : release.length;
  return release.slice(start, next);
}

test("public release is manual-only, exact-versioned, and denied permissions by default", () => {
  const trigger = release.slice(release.indexOf("on:"), release.indexOf("permissions:"));
  assert.match(trigger, /workflow_dispatch:/u);
  assert.doesNotMatch(trigger, /\b(push|pull_request|schedule):/u);
  assert.match(trigger, /version:[\s\S]*required: true/u);
  assert.match(release, /^permissions: \{\}$/mu);
  assert.match(job("identity"), /--ref "\$GITHUB_REF"/u);
  assert.match(job("identity"), /devhud@v\$\{\{ inputs\.version \}\}/u);
});

test("release dispatch is project-serialized and shell inputs cross only through env", () => {
  assert.match(release, /concurrency:\n  group: release-devhud\n  cancel-in-progress: false/u);
  const identity = job("identity");
  const validationStep = identity.slice(identity.indexOf("- name: Validate release identity"));
  const run = validationStep.slice(validationStep.indexOf("run: |"));
  assert.match(validationStep, /RELEASE_MODE: \$\{\{ inputs\.mode \}\}/u);
  assert.match(validationStep, /RELEASE_VERSION: \$\{\{ inputs\.version \}\}/u);
  assert.doesNotMatch(run, /\$\{\{ inputs\.(?:mode|version) \}\}/u);
  assert.match(run, /--version "\$RELEASE_VERSION" --mode "\$RELEASE_MODE"/u);
});

test("the complete reusable private candidate is the sole publication prerequisite", () => {
  assert.match(privateCandidate, /workflow_call:/u);
  assert.match(privateCandidate, /plan-only\|signed-private/u);
  assert.match(job("private_candidate"), /package-devhud-private\.yml/u);
  assert.match(job("private_candidate"), /contents: read[\s\S]*id-token: write/u);
  assert.match(job("private_candidate"), /reuse_candidate != 'true'/u);
  assert.match(job("candidate"), /needs: \[identity, private_candidate\]/u);
  assert.match(job("candidate"), /artifact-ids: \$\{\{ steps\.select\.outputs\.artifact_id \}\}/u);
  assert.match(job("candidate"), /DEVHUD_PROVENANCE_RUN_ID/u);
  assert.match(job("candidate"), /validate-devhud-private-build\.mjs/u);
  assert.match(job("preflight"), /needs: \[identity, candidate\]/u);
  assert.match(job("preflight"), /devhud-live-preflight\.mjs/u);
  assert.match(job("preflight"), /DEVHUD_PUBLIC_API_URL: \$\{\{ vars\.DEVHUD_PUBLIC_API_URL \}\}/u);
  assert.match(privateCandidate, /private-signed-candidate-\$\{\{ github\.sha \}\}-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}[\s\S]*retention-days: 35/u);
  assert.match(privateCandidate, /artifact_id:[\s\S]*steps\.candidate\.outputs\.artifact-id/u);
  assert.match(job("identity"), /devhud-candidate-artifact\.mjs/u);
});

test("jobs request only their channel-specific GitHub permissions", () => {
  for (const name of ["identity", "private_candidate", "candidate", "preflight", "submit_stores", "review_gate", "docs_candidate", "registry", "prepare_infrastructure", "publish_stores", "stores_public", "updater_public", "public_docs", "verify_all", "rollback_pre_store"]) {
    assert.doesNotMatch(job(name), /contents: write/u, `${name} must not write repository contents`);
  }
  assert.match(job("github_release"), /contents: write/u);
  assert.match(job("ga"), /contents: write/u);
  for (const name of ["private_candidate", "preflight", "registry", "prepare_infrastructure", "updater_public", "verify_all", "ga", "rollback_pre_store"]) {
    assert.match(job(name), /id-token: write/u, `${name} needs provider-neutral OIDC`);
  }
  for (const name of ["identity", "candidate", "submit_stores", "registry", "prepare_infrastructure", "github_release", "updater_public"]) {
    assert.match(job(name), /actions: read/u, `${name} needs immutable artifact lookup or cross-run download`);
  }
});

test("protected review gates preserve pending review and recover partial publication", () => {
  assert.match(job("review_gate"), /devhud-store-review-approved/u);
  assert.match(job("review_gate"), /\.status == "approved-held" or \.status == "public"/u);
  assert.match(job("stores_public"), /devhud-store-publication/u);
  assert.match(job("ga"), /environment: devhud-ga/u);
  assert.match(job("submit_stores"), /STAGED_PUBLISH|deferred review at 100 percent/u);
  assert.doesNotMatch(release, /userFraction|phasedRelease|beta|prerelease: true/ui);
});

test("same-commit retries reconcile public stores without repeating store mutations", () => {
  assert.match(job("identity"), /devhud-release-plan-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/u);
  assert.match(privateCandidate, /release-plan-devhud-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/u);
  assert.match(job("identity"), /retry: \$\{\{ steps\.identity\.outputs\.retry \}\}/u);
  assert.match(job("submit_stores"), /Reconcile the exact current store state/u);
  assert.match(job("submit_stores"), /needs\.identity\.outputs\.retry != 'true'/u);
  assert.match(job("submit_stores"), /steps\.store_state\.outputs\.status == 'unsubmitted'.*steps\.store_state\.outputs\.status == 'withdrawn'/u);
  assert.match(job("submit_stores"), /devhud-store-submission-\$\{\{ matrix\.provider \}\}-\$\{\{ github\.run_attempt \}\}/u);
  assert.match(job("submit_stores"), /\[ "\$RELEASE_RETRY" = true \] && \[ "\$status" != public \]/u);
  assert.match(job("review_gate"), /needs\.identity\.outputs\.retry == 'true'.*devhud-publication.*devhud-store-review-approved/u);
  assert.match(job("review_gate"), /\[ "\$RELEASE_RETRY" = true \][\s\S]*\.status == "public"/u);
  assert.doesNotMatch(job("publish_stores"), /needs\.identity\.outputs\.retry != 'true'/u);
  assert.match(job("stores_public"), /needs\.identity\.outputs\.retry == 'true'.*devhud-publication.*devhud-store-publication/u);
});

test("same-commit retries publish recovered GitHub drafts after replacing assets", () => {
  const publication = job("github_release");
  const existingRelease = publication.indexOf('if gh release view "$RELEASE_TAG"');
  const deleteUnexpected = publication.indexOf('gh release delete-asset "$RELEASE_TAG" --yes');
  const upload = publication.indexOf('gh release upload "$RELEASE_TAG"');
  const publish = publication.indexOf('gh release edit "$RELEASE_TAG" --draft=false --latest=false');
  const freshRelease = publication.indexOf("\n          else", existingRelease);
  const visibility = publication.indexOf("--json isDraft,isPrerelease");
  assert.ok(existingRelease > 0 && deleteUnexpected > existingRelease, "the retry path must delete unexpected assets on the exact existing release");
  assert.ok(upload > deleteUnexpected, "the retry path must replace expected assets after deleting unexpected assets");
  assert.ok(publish > upload && publish < freshRelease, "the retry path must publish only after asset replacement succeeds");
  assert.ok(visibility > freshRelease && visibility > publish, "visibility must be asserted after either release path publishes");
});

test("store publication waits boundedly for every exact public version", () => {
  const publication = job("stores_public");
  assert.match(publication, /timeout-minutes: 35/u);
  assert.match(publication, /for attempt in \$\(seq 1 30\)/u);
  assert.match(publication, /for provider in apple google-play chrome-web-store/u);
  assert.match(publication, /sleep 60/u);
});

test("App Store build polling is read-only and submission runs once afterward", () => {
  const submission = job("submit_stores");
  const reconciliation = submission.indexOf("Reconcile the exact App Store build before upload");
  const upload = submission.indexOf("Upload the processed App Store package");
  const polling = submission.slice(submission.indexOf("Wait for App Store package processing"), submission.indexOf("Submit or reconcile the App Store review once"));
  assert.ok(reconciliation > 0 && upload > reconciliation, "the exact build must be queried before transporter upload");
  assert.match(submission.slice(upload, submission.indexOf("Wait for App Store package processing")), /steps\.apple_build\.outputs\.status == 'absent'/u);
  assert.match(polling, /build-status apple/u);
  assert.doesNotMatch(polling, /submit apple/u);
  assert.match(submission, /Submit or reconcile the App Store review once[\s\S]*submit apple/u);
});

test("channel dependencies place GA after every independently verified public surface", () => {
  const dependencies = {
    registry: "review_gate",
    prepare_infrastructure: "registry",
    publish_stores: "prepare_infrastructure",
    stores_public: "publish_stores",
    github_release: "stores_public",
    updater_public: "github_release",
    public_docs: "updater_public",
    verify_all: "public_docs",
    ga: "verify_all",
  };
  for (const [name, predecessor] of Object.entries(dependencies)) {
    assert.match(job(name), new RegExp(`needs: [^\\n]*${predecessor}`, "u"), `${name} must wait for ${predecessor}`);
  }
  assert.match(job("registry"), /needs: \[[^\n]*docs_candidate/u);
  assert.match(job("ga"), /transition --state independently-verified --event mark-ga/u);
});

test("documentation deployment is bound to the exact candidate before publication", () => {
  assert.match(job("docs_candidate"), /needs: \[identity, preflight\]/u);
  assert.match(job("review_gate"), /needs: \[identity, submit_stores, docs_candidate\]/u);
  assert.match(job("docs_candidate"), /doc_build\/devhud\.html/u);
  assert.match(job("docs_candidate"), /devhud-release-identity/u);
  assert.match(job("docs_candidate"), /needs\.identity\.outputs\.version/u);
  assert.match(job("docs_candidate"), /name: devhud-public-docs-candidate-\$\{\{ github\.run_attempt \}\}/u);
  assert.match(job("public_docs"), /name: devhud-public-docs-candidate-\$\{\{ github\.run_attempt \}\}/u);
  for (const name of ["public_docs", "verify_all"]) {
    assert.match(job(name), /new URL\("\/devhud", process\.env\.DEVHUD_PUBLIC_DOCS_URL\)/u);
    assert.match(job(name), /devhud-release-identity/u);
  }
});

test("infrastructure verification sends a conforming unary Connect request", () => {
  const infrastructure = job("prepare_infrastructure");
  const bootstrap = infrastructure.split("\n").find((line) => line.includes("bootstrap=$(curl"));
  assert.ok(bootstrap, "missing Bootstrap verification request");
  assert.match(bootstrap, /-H 'Connect-Protocol-Version: 1'/u);
  assert.match(bootstrap, /BootstrapService\/GetBootstrap/u);
});

test("independent verification fetches and verifies both remote OCI digests", () => {
  const verification = job("verify_all");
  assert.match(verification, /sigstore\/cosign-installer/u);
  assert.match(verification, /skopeo inspect/u);
  assert.match(verification, /cosign login/u);
  assert.match(verification, /actual_digest[\s\S]*expected_digest/u);
  assert.match(verification, /cosign verify --certificate-identity/u);
  assert.match(verification, /release-devhud\.yml@\$GITHUB_REF/u);
});

test("registry publication binds signed digests to the selected OCI archives", () => {
  const registry = job("registry");
  const source = registry.indexOf("source_digest=$(skopeo inspect");
  const copy = registry.indexOf("skopeo copy --all --preserve-digests --digestfile");
  const remote = registry.indexOf("remote_digest=$(skopeo inspect");
  const compare = registry.indexOf('test "$remote_digest" = "$copied_digest"');
  const sign = registry.indexOf('cosign sign --yes "$immutable"');
  assert.ok(source > 0 && copy > source, "the source archive digest must be captured before copying");
  assert.ok(remote > copy && compare > remote, "the copied digest must match the remote tag before signing");
  assert.ok(sign > compare, "the immutable candidate digest must be signed only after every comparison passes");
  assert.match(registry, /test "\$source_digest" = "\$copied_digest"/u);
  assert.match(registry, /immutable="\$DEVHUD_OCI_REGISTRY\/\$repository@\$copied_digest"/u);
});

test("independent verification downloads and authenticates the exact public GitHub assets", () => {
  const verification = job("verify_all");
  assert.match(verification, /gh api "repos\/\$GITHUB_REPOSITORY\/commits\/\$RELEASE_TAG" --jq '\.sha'/u);
  assert.match(verification, /test "\$remote_target" = "\$GITHUB_SHA"/u);
  assert.match(verification, /gh release view[\s\S]*--json assets,isDraft,isPrerelease,tagName/u);
  assert.match(verification, /gh release download/u);
  assert.match(verification, /devhud-v\$\{RELEASE_VERSION\}-release-evidence\.tar\.gz/u);
  assert.match(verification, /X-DevHud-Package/u);
  assert.match(verification, /validate-devhud-public-assets\.mjs/u);
  assert.match(verification, /--revision "\$GITHUB_SHA"/u);
  assert.match(verification, /DEVHUD_PRIVATE_WORKFLOW_REF/u);
});

test("independent verification requeries every exact store before GA", () => {
  const verification = job("verify_all");
  for (const name of ["APPLE_API_PRIVATE_KEY_B64", "DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON", "DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN"]) {
    assert.match(verification, new RegExp(`${name}:`, "u"));
  }
  assert.match(verification, /for provider in apple google-play chrome-web-store/u);
  assert.match(verification, /devhud-store-release\.mjs status "\$provider"/u);
  assert.match(verification, /\.status == "public"/u);
});

test("GA repeats every public-channel verification after approval and before mutation", () => {
  const ga = job("ga");
  assert.match(ga, /needs: \[identity, preflight, registry, verify_all\]/u);
  assert.match(job("preflight"), /release_configuration_fingerprint:[\s\S]*configuration-fingerprint/u);
  for (const name of releaseVariables) assert.match(ga, new RegExp(`${name}: \\$\\{\\{ vars\\.${name} \\}\\}`, "u"));
  const configuration = ga.indexOf("Bind GA configuration to the validated preflight environment");
  const firstVerification = ga.indexOf("Reverify every exact store version after GA approval");
  assert.ok(configuration > 0 && firstVerification > configuration, "GA configuration must match preflight before public-channel verification");
  assert.match(ga, /EXPECTED_CONFIGURATION_FINGERPRINT: \$\{\{ needs\.preflight\.outputs\.release_configuration_fingerprint \}\}/u);
  assert.match(ga, /configuration-fingerprint[\s\S]*test "\$actual" = "\$EXPECTED_CONFIGURATION_FINGERPRINT"/u);
  for (const name of ["APPLE_API_PRIVATE_KEY_B64", "DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON", "DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN", "DEVHUD_OCI_REGISTRY_TOKEN", "DEVHUD_RELEASE_CONTROLLER_AUDIENCE"]) {
    assert.match(ga, new RegExp(`${name}:`, "u"));
  }
  for (const pattern of [
    /devhud-store-release\.mjs status "\$provider"/u,
    /gh release download/u,
    /validate-devhud-public-assets\.mjs/u,
    /DEVHUD_PUBLIC_API_URL\/readyz/u,
    /devhud-release-identity/u,
    /skopeo inspect/u,
    /cosign verify --certificate-identity/u,
    /devhud-release-controller\.mjs status/u,
    /gh api "repos\/\$GITHUB_REPOSITORY\/commits\/\$RELEASE_TAG" --jq '\.sha'/u,
    /test "\$remote_target" = "\$GITHUB_SHA"/u,
    /--revision "\$GITHUB_SHA"/u,
  ]) {
    assert.match(ga, pattern);
  }
  const mutation = ga.indexOf('gh release edit "$RELEASE_TAG"');
  assert.ok(mutation > ga.indexOf("Reverify every exact store version after GA approval"));
  assert.ok(mutation > ga.indexOf("Reverify the exact public GitHub Release assets after GA approval"));
  assert.ok(mutation > ga.indexOf("Reverify public API, docs, release, and immutable images after GA approval"));
  assert.ok(mutation > ga.indexOf("Reverify final deployment state after GA approval"));
  assert.ok(ga.indexOf("transition --state independently-verified --event mark-ga") > mutation);
});

test("GA notes are public and existing tags are dereferenced to commits", () => {
  assert.doesNotMatch(releaseMetadata.releaseNotes.en, /private|candidate/ui);
  assert.doesNotMatch(releaseMetadata.releaseNotes.ko, /비공개|후보/u);
  assert.match(job("github_release"), /git rev-parse --verify "refs\/tags\/\$RELEASE_TAG\^\{commit\}"/u);
});

test("dry-run cannot enter publication and rollback stops at store publication", () => {
  assert.match(job("private_candidate"), /inputs\.mode == 'release'.*signed-private.*plan-only/u);
  for (const name of ["preflight", "submit_stores", "registry", "publish_stores", "github_release", "ga"]) {
    assert.match(job(name), /if: \$\{\{ inputs\.mode == 'release' \}\}/u);
  }
  assert.match(job("rollback_pre_store"), /always\(\)/u);
  for (const failed of ["submit_stores", "review_gate", "docs_candidate", "registry", "prepare_infrastructure"]) {
    assert.match(job("rollback_pre_store"), new RegExp(`needs\\.${failed}\\.result == 'failure'`, "u"));
  }
  assert.match(job("rollback_pre_store"), /needs\.publish_stores\.result == 'failure'/u);
  assert.match(job("rollback_pre_store"), /needs\.publish_stores\.result == 'skipped'/u);
  assert.match(job("rollback_pre_store"), /needs\.stores_public\.result == 'failure'/u);
  assert.match(job("rollback_pre_store"), /needs: \[[^\n]*stores_public/u);
  assert.match(job("rollback_pre_store"), /needs\.identity\.outputs\.retry != 'true'/u);
  assert.match(job("prepare_infrastructure"), /promotion_attempted: \$\{\{ steps\.promote\.outputs\.attempted \}\}/u);
  assert.match(job("prepare_infrastructure"), /promoted: \$\{\{ steps\.promote\.outputs\.promoted \}\}/u);
  const promotion = job("prepare_infrastructure");
  assert.ok(promotion.indexOf("attempted=true") < promotion.indexOf("promote-api"), "the attempt output must precede the remote mutation");
  assert.match(job("rollback_pre_store"), /if: \$\{\{ needs\.prepare_infrastructure\.outputs\.promotion_attempted == 'true' \}\}/u);
  assert.match(job("rollback_pre_store"), /rollback-policy --state infrastructure-ready/u);
  assert.doesNotMatch(job("rollback_pre_store"), /node -e.*rollbackPolicy/u);
  const rollback = job("rollback_pre_store");
  const storeStatus = rollback.indexOf("status \"$provider\"");
  const googleWithdrawal = rollback.indexOf("withdraw google-play");
  const appleWithdrawal = rollback.indexOf("withdraw apple");
  const chromeWithdrawal = rollback.indexOf("withdraw chrome-web-store");
  const controllerStatus = rollback.indexOf("devhud-release-controller.mjs status");
  const controllerRollback = rollback.indexOf("devhud-release-controller.mjs rollback");
  assert.ok(storeStatus > 0 && googleWithdrawal > storeStatus, "every exact store state must be checked before cleanup mutation");
  assert.ok(appleWithdrawal > googleWithdrawal && chromeWithdrawal > appleWithdrawal);
  assert.ok(controllerStatus > chromeWithdrawal, "controller status must follow every store withdrawal");
  assert.ok(controllerRollback > controllerStatus, "controller rollback must follow live reconciliation");
  assert.ok(controllerRollback > chromeWithdrawal, "controller rollback must follow every store withdrawal");
  assert.match(rollback, /api_status[\s\S]*sweeper_status/u);
  assert.match(rollback, /for provider in apple google-play chrome-web-store/u);
  assert.match(rollback, /status == "public"/u);
  assert.match(rollback, /\.status == "unsubmitted"/u);
  assert.match(rollback, /\.status == "withdrawn"/u);
});
