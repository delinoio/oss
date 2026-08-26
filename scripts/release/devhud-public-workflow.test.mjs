import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

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

test("the complete reusable private candidate is the sole publication prerequisite", () => {
  assert.match(privateCandidate, /workflow_call:/u);
  assert.match(privateCandidate, /plan-only\|signed-private/u);
  assert.match(job("private_candidate"), /package-devhud-private\.yml/u);
  assert.match(job("private_candidate"), /contents: read[\s\S]*id-token: write/u);
  assert.match(job("preflight"), /needs: \[identity, private_candidate\]/u);
  assert.match(job("preflight"), /validate-devhud-private-build\.mjs/u);
  assert.match(job("preflight"), /devhud-live-preflight\.mjs/u);
  assert.match(job("preflight"), /DEVHUD_PUBLIC_API_URL: \$\{\{ vars\.DEVHUD_PUBLIC_API_URL \}\}/u);
});

test("jobs request only their channel-specific GitHub permissions", () => {
  for (const name of ["identity", "private_candidate", "preflight", "submit_stores", "review_gate", "docs_candidate", "registry", "prepare_infrastructure", "publish_stores", "stores_public", "updater_public", "public_docs", "verify_all", "rollback_pre_store"]) {
    assert.doesNotMatch(job(name), /contents: write/u, `${name} must not write repository contents`);
  }
  assert.match(job("github_release"), /contents: write/u);
  assert.match(job("ga"), /contents: write/u);
  for (const name of ["private_candidate", "preflight", "registry", "prepare_infrastructure", "updater_public", "verify_all", "rollback_pre_store"]) {
    assert.match(job(name), /id-token: write/u, `${name} needs provider-neutral OIDC`);
  }
});

test("protected review gates preserve pending review and prohibit partial publication", () => {
  assert.match(job("review_gate"), /environment: devhud-store-review-approved/u);
  assert.match(job("stores_public"), /environment: devhud-store-publication/u);
  assert.match(job("ga"), /environment: devhud-ga/u);
  assert.match(job("submit_stores"), /STAGED_PUBLISH|deferred review at 100 percent/u);
  assert.doesNotMatch(release, /userFraction|phasedRelease|beta|prerelease: true/ui);
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
  assert.match(job("docs_candidate"), /doc_build\/devhud\.html/u);
  assert.match(job("docs_candidate"), /devhud-release-identity/u);
  assert.match(job("docs_candidate"), /needs\.identity\.outputs\.version/u);
  for (const name of ["public_docs", "verify_all"]) {
    assert.match(job(name), /new URL\("\/devhud", process\.env\.DEVHUD_PUBLIC_DOCS_URL\)/u);
    assert.match(job(name), /devhud-release-identity/u);
  }
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
  assert.match(job("rollback_pre_store"), /needs\.prepare_infrastructure\.result == 'failure'/u);
  assert.match(job("rollback_pre_store"), /needs\.publish_stores\.result == 'skipped'/u);
  assert.doesNotMatch(job("rollback_pre_store"), /needs\.publish_stores\.result != 'success'/u);
  assert.match(job("rollback_pre_store"), /rollbackPolicy\("infrastructure-ready"\)/u);
  const rollback = job("rollback_pre_store");
  const googleWithdrawal = rollback.indexOf("withdraw google-play");
  const appleWithdrawal = rollback.indexOf("withdraw apple");
  const chromeWithdrawal = rollback.indexOf("withdraw chrome-web-store");
  const controllerRollback = rollback.indexOf("devhud-release-controller.mjs rollback");
  assert.ok(googleWithdrawal > 0 && appleWithdrawal > googleWithdrawal && chromeWithdrawal > appleWithdrawal);
  assert.ok(controllerRollback > chromeWithdrawal, "controller rollback must follow every store withdrawal");
  assert.match(rollback, /\.status == "withdrawn"/u);
});
