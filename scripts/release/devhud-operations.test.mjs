import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));
const operations = readFileSync(`${root}/docs/apps-devhud-operations-contract.md`, "utf8");
const workflowContract = readFileSync(`${root}/docs/repository-workflow-contract.md`, "utf8");
const support = readFileSync(`${root}/docs/apps-devhud-support-contract.md`, "utf8");
const workflow = readFileSync(`${root}/.github/workflows/devhud-cef-security-review.yml`, "utf8");
const release = readFileSync(`${root}/.github/workflows/release-devhud.yml`, "utf8");
const privateWorkflow = readFileSync(`${root}/.github/workflows/package-devhud-private.yml`, "utf8");
const fixture = JSON.parse(readFileSync(`${root}/scripts/release/fixtures/devhud-cef-security-review.json`, "utf8"));
const pins = JSON.parse(readFileSync(`${root}/apps/devhud/cef-pins.json`, "utf8"));
const runtimeRevisionConsumers = [
  "apps/devhud/src/diagnostics.ts",
  "packages/devhud-api-client/src/validation.ts",
  "servers/devhud-api/internal/rpc/diagnostics.go",
].map((path) => [path, readFileSync(`${root}/${path}`, "utf8")]);

const privateJobs = ["plan", "preflight", "desktop", "extension", "mobile", "oci", "assemble"];
const publicJobs = ["identity", "private_candidate", "candidate", "preflight", "submit_stores", "review_gate", "docs_candidate", "registry", "prepare_infrastructure", "stores_public", "github_release", "updater_public", "public_docs", "verify_all", "ga", "rollback_pre_store"];
const primaryArtifacts = [
  "devhud-macos-x64.dmg", "devhud-macos-x64-macos-app.tar.gz", "devhud-macos-arm64.dmg", "devhud-macos-arm64-macos-app.tar.gz",
  "devhud-windows-x64-windows-msi.msi", "devhud-windows-x64-windows-nsis.exe", "devhud-windows-arm64-windows-msi.msi", "devhud-windows-arm64-windows-nsis.exe",
  "devhud-ubuntu-x64-linux-appimage.AppImage", "devhud-ubuntu-x64-linux-deb.deb", "devhud-ubuntu-arm64-linux-appimage.AppImage", "devhud-ubuntu-arm64-linux-deb.deb",
  "devhud-ios-arm64-app-store.ipa", "devhud-android-arm64-armv7-google-play.aab", "devhud-chrome-web-store.zip", "devhud-chrome-github-validation.zip",
  "devhud-api-linux-amd64-arm64.oci.tar", "devhud-api-sweeper-linux-amd64-arm64.oci.tar",
];

test("operations contract names every implemented release job and artifact", () => {
  for (const name of privateJobs) assert.match(privateWorkflow, new RegExp(`\\n  ${name}:`, "u"));
  for (const name of publicJobs) assert.match(release, new RegExp(`\\n  ${name}:`, "u"));
  for (const artifact of primaryArtifacts) assert.match(operations, new RegExp(artifact.replaceAll(".", "\\."), "u"));
  for (const name of ["BootstrapService", "SettingsService", "UploadService", "AccountService", "AdminService", "DiagnosticsService", "devhud-api", "devhud-api-sweeper", "apple", "google-play", "chrome-web-store", "/devhud", "DevHudWidgetProvider", "io.delino.devhud.native_messaging", "io.delino.devhud.widget"]) {
    assert.match(operations, new RegExp(name.replaceAll("/", "\\/"), "u"), name);
  }
});

test("CEF review is scheduled, read-only, bounded, and non-publishing", () => {
  assert.match(workflow, /schedule:[\s\S]*cron:/u);
  assert.match(workflow, /workflow_dispatch:/u);
  assert.match(workflow, /permissions:\n  contents: read/u);
  assert.match(workflow, /pins\.tauri\.revision/u);
  assert.match(workflow, /feat\/cef/u);
  assert.match(workflow, /gh api --paginate --slurp/u);
  assert.match(workflow, /compare\/\$pin\.\.\.\$upstream_revision/u);
  assert.match(workflow, /pages\.flatMap\(/u);
  assert.doesNotMatch(workflow, /EXPECTED_REVISION/u);
  assert.match(workflow, /vulnerab\\w\*/u);
  assert.match(workflow, /securitySignalTotal/u);
  assert.match(workflow, /securitySignalsTruncated/u);
  assert.match(workflow, /mutationPerformed: false/u);
  assert.match(workflow, /publicationPerformed: false/u);
  assert.match(workflow, /actions\/upload-artifact/u);
  assert.deepEqual(fixture.securitySignals[0], { sha: fixture.securitySignals[0].sha, securityKeyword: true });
  assert.equal(fixture.securitySignalTotal, 1);
  assert.equal(fixture.securitySignalsTruncated, false);
  assert.equal(fixture.mutationPerformed, false);
  assert.equal(fixture.publicationPerformed, false);
  for (const forbidden of ["git push", "gh release", "workflow_dispatch --", "promote-updater", "cosign sign", "wrangler pages deploy", "curl -X POST"]) {
    assert.doesNotMatch(workflow, new RegExp(forbidden.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "iu"), forbidden);
  }
  for (const [path, source] of runtimeRevisionConsumers) {
    assert.match(source, new RegExp(pins.tauri.revision.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "u"), path);
    assert.match(source, new RegExp(pins.runtime.cefVersion.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "u"), path);
  }
  for (const path of runtimeRevisionConsumers.map(([path]) => path)) {
    assert.match(operations, new RegExp(path.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "u"), path);
  }
});

test("operations contract preserves high-risk CEF, rollback, retention, and redaction boundaries", () => {
  for (const phrase of [
    "no automatic downgrade", "partial GA", "remote-alert service", "kill switch", "Cargo.lock", "compatibility matrix",
    "new complete signed private candidate", "all ten signed manifests", "rollback is forbidden", "30 days", "PostgreSQL", "R2",
    "QuarantineUpload", "DeleteUpload", "RestoreAccount", "DiagnosticsService", "Never print secrets",
  ]) assert.match(operations, new RegExp(phrase.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "iu"), phrase);
  assert.match(workflowContract, /may upload only its bounded metadata report artifact/u);
  assert.match(support, /high-severity response/iu);
});
