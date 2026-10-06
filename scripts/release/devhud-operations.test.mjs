import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import yaml from "js-yaml";

const root = fileURLToPath(new URL("../..", import.meta.url));
const operations = readFileSync(`${root}/docs/apps-devhud-operations-contract.md`, "utf8");
const workflowContract = readFileSync(`${root}/docs/repository-workflow-contract.md`, "utf8");
const support = readFileSync(`${root}/docs/apps-devhud-support-contract.md`, "utf8");
const workflow = readFileSync(`${root}/.github/workflows/devhud-cef-security-review.yml`, "utf8");
const release = readFileSync(`${root}/.github/workflows/release-devhud.yml`, "utf8");
const privateWorkflow = readFileSync(`${root}/.github/workflows/package-devhud-private.yml`, "utf8");
const ciWorkflow = readFileSync(`${root}/.github/workflows/CI.yml`, "utf8");
const fixture = JSON.parse(readFileSync(`${root}/scripts/release/fixtures/devhud-cef-security-review.json`, "utf8"));
const pins = JSON.parse(readFileSync(`${root}/apps/devhud/cef-pins.json`, "utf8"));
const ciPaths = JSON.parse(readFileSync(`${root}/scripts/ci/job-paths.json`, "utf8"));
const runtimeRevisionConsumers = [
  "apps/devhud/src/diagnostics.ts",
  "packages/devhud-api-client/src/validation.ts",
  "servers/devhud-api/internal/rpc/diagnostics.go",
].map((path) => [path, readFileSync(`${root}/${path}`, "utf8")]);

const privateJobs = ["plan", "preflight", "desktop", "ios-simulators", "extension", "mobile", "oci", "assemble"];
const publicJobs = ["identity", "private_candidate", "candidate", "preflight", "submit_stores", "review_gate", "docs_candidate", "registry", "prepare_infrastructure", "stores_public", "github_release", "updater_public", "public_docs", "verify_all", "ga", "rollback_pre_store"];
const primaryArtifacts = [
  "devhud-macos-x64.dmg", "devhud-macos-x64-macos-app.tar.gz", "devhud-macos-arm64.dmg", "devhud-macos-arm64-macos-app.tar.gz",
  "devhud-windows-x64-windows-msi.msi", "devhud-windows-x64-windows-nsis.exe", "devhud-windows-arm64-windows-msi.msi", "devhud-windows-arm64-windows-nsis.exe",
  "devhud-ubuntu-x64-linux-appimage.AppImage", "devhud-ubuntu-x64-linux-deb.deb", "devhud-ubuntu-arm64-linux-appimage.AppImage", "devhud-ubuntu-arm64-linux-deb.deb",
  "devhud-ios-arm64-app-store.ipa", "devhud-android-arm64-armv7-google-play.aab", "devhud-chrome-web-store.zip", "devhud-chrome-github-validation.zip",
  "devhud-api-linux-amd64-arm64.oci.tar", "devhud-api-sweeper-linux-amd64-arm64.oci.tar",
];

test("CI evidence distinguishes PR validation from full native packaging", () => {
  for (const text of [workflowContract, operations, support]) {
    assert.match(text, /PR/u);
    assert.match(text, /main/u);
    assert.match(text, /manual/u);
    assert.match(text, /CI Result/u);
  }
  for (const id of ["devhud-desktop", "devhud-ios-simulator", "devhud-android-emulator"]) assert.equal(ciPaths[id].native, true);
  assert.equal(ciPaths["devhud-mobile-contracts"].native, undefined);
  assert.match(ciWorkflow, /node scripts\/ci\/run-affected\.mjs devhud ci:check/u);
  assert.match(workflowContract, /counterfactual estimate, not a measured post-change improvement/u);
});

test("operations contract names every implemented release job and artifact", () => {
  for (const name of privateJobs) assert.match(privateWorkflow, new RegExp(`\\n  ${name}:`, "u"));
  for (const name of publicJobs) assert.match(release, new RegExp(`\\n  ${name}:`, "u"));
  for (const artifact of primaryArtifacts) assert.match(operations, new RegExp(artifact.replaceAll(".", "\\."), "u"));
  for (const name of ["BootstrapService", "SettingsService", "UploadService", "AccountService", "AdminService", "DiagnosticsService", "devhud-api", "devhud-api-sweeper", "apple", "google-play", "chrome-web-store", "/devhud", "DevHudWidgetProvider", "io.delino.devhud.native_messaging", "io.delino.devhud.widget"]) {
    assert.match(operations, new RegExp(name.replaceAll("/", "\\/"), "u"), name);
  }
});

test("signed candidate requires both unsigned iOS simulator builds", () => {
  const jobs = yaml.load(privateWorkflow).jobs;
  const simulators = jobs["ios-simulators"];
  assert.ok(jobs.assemble.needs.includes("ios-simulators"));
  assert.equal(simulators.needs, "preflight");
  assert.equal(simulators.if, "${{ inputs.mode == 'signed-private' }}");
  assert.deepEqual(simulators.strategy.matrix.include, [
    { target: "aarch64-sim", runner: "macos-15" },
    { target: "x86_64", runner: "macos-15-intel" },
  ]);
  assert.equal(simulators.environment, undefined);
  assert.doesNotMatch(JSON.stringify(simulators), /secrets\.|vars\./u);
  assert.ok(simulators.steps.some(({ run }) => run === "node scripts/run-mobile.mjs ios build --target ${{ matrix.target }} --ci --no-sign"));
});

test("repository contract catalog and validation commands use canonical paths", () => {
  const catalog = readFileSync(`${root}/docs/README.md`, "utf8");
  const agents = readFileSync(`${root}/AGENTS.md`, "utf8");
  assert.match(catalog, /Repository-level contract docs: `docs\/repository-<topic>-contract\.md`/u);
  assert.match(catalog, /`docs\/repository-workflow-contract\.md` \(repository-level workflow/u);
  assert.match(agents, /`docs\/repository-<topic>-contract\.md`: Canonical repository-level contract docs/u);
  assert.match(agents, /Use `repository-<topic>-contract\.md` for repository-level contract docs/u);
  assert.match(operations, /scripts\/release\/validate-devhud-public-assets\.mjs/u);
  assert.match(operations, /scripts\/release\/validate-devhud-ios-signing\.mjs/u);
  assert.match(operations, /generate and validate the ignored administrator bundle/u);
  assert.match(workflowContract, /Docker build independently generates that same contracted structure/u);
  assert.match(support, /never recover by committing or copying a `dist` tree/u);
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
  assert.match(workflow, /message: String\(commit\.commit\?\.message \?\? ''\),/u);
  assert.doesNotMatch(workflow, /commit\.commit\?\.message \?\? ''\)\.split\(/u);
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

test("CEF maintainer summary heredoc parses as a Node module", () => {
  const steps = yaml.load(workflow).jobs.compare.steps;
  const summaryStep = steps.find(({ name }) => name === "Write maintainer summary");
  const heredoc = summaryStep.run.match(/^node --input-type=module <<'NODE' >> "\$GITHUB_STEP_SUMMARY"\n([\s\S]*?)\nNODE$/mu);
  assert.ok(heredoc, "extract the actual workflow summary heredoc");
  const result = spawnSync(process.execPath, ["--input-type=module", "--check"], {
    input: heredoc[1], encoding: "utf8", timeout: 10_000,
  });
  assert.equal(result.status, 0, result.stderr || result.error?.message);

  const uploadStep = steps.find(({ name }) => name === "Upload bounded metadata report");
  assert.ok(steps.indexOf(uploadStep) > steps.indexOf(summaryStep));
  assert.equal(summaryStep.if, undefined);
  assert.equal(summaryStep["continue-on-error"], undefined);
  assert.equal(uploadStep.if, undefined);
  assert.match(uploadStep.uses, /^actions\/upload-artifact@/u);
  assert.equal(uploadStep.with.path, "devhud-cef-security-review.json");
  assert.equal(uploadStep.with["if-no-files-found"], "error");
  assert.equal(uploadStep.with["retention-days"], 35);
});

for (const { name, signals, total, comparison, truncated } of [
  { name: "zero signals", signals: [], total: 0, comparison: { status: "identical", aheadBy: 0, behindBy: 0, totalCommits: 0 }, truncated: false },
  { name: "truncated signals", signals: Array.from({ length: 50 }, () => fixture.securitySignals[0]), total: 75, comparison: fixture.comparison, truncated: true },
]) {
  test(`CEF maintainer summary writes safe metadata for ${name}`, (t) => {
    const directory = mkdtempSync(join(tmpdir(), "devhud-cef-summary-"));
    t.after(() => rmSync(directory, { recursive: true, force: true }));
    const rawContent = "fixture-only-raw-content-not-for-summary";
    const report = {
      ...fixture,
      comparison,
      securitySignals: signals.map((signal) => ({ ...signal, message: rawContent })),
      securitySignalTotal: total,
      securitySignalsTruncated: truncated,
      commitMessage: rawContent,
      credentials: rawContent,
      nativeContent: rawContent,
    };
    const reportPath = join(directory, "devhud-cef-security-review.json");
    const reportBytes = `${JSON.stringify(report)}\n`;
    writeFileSync(reportPath, reportBytes, { mode: 0o600 });
    const summaryPath = join(directory, "summary.md");
    const summaryStep = yaml.load(workflow).jobs.compare.steps.find(({ name }) => name === "Write maintainer summary");
    const result = spawnSync("bash", ["-c", summaryStep.run], {
      cwd: directory,
      env: { PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH}`, GITHUB_STEP_SUMMARY: summaryPath },
      encoding: "utf8", timeout: 10_000,
    });
    assert.equal(result.status, 0, result.stderr || result.error?.message);
    const summary = readFileSync(summaryPath, "utf8");
    for (const line of [
      "## DevHud CEF security review",
      `- Committed Tauri revision: \`${fixture.committedRevision}\``,
      `- Upstream \`feat/cef\`: \`${fixture.upstreamRevision}\``,
      `- Comparison: ${comparison.status}, ahead ${comparison.aheadBy}, behind ${comparison.behindBy}`,
      `- Security-related commit signals: ${signals.length}`,
      `- Security-related signals retained: ${signals.length} of ${total}${truncated ? " (truncated)" : ""}`,
      "- Mutation/publication performed: false/false",
    ]) assert.ok(summary.split("\n").includes(line), line);
    assert.equal(summary.includes("(truncated)"), truncated);
    assert.ok(!summary.includes(rawContent));
    assert.ok(!summary.includes(fixture.securitySignals[0].sha));
    assert.equal(result.stdout, "");
    assert.equal(result.stderr, "");
    assert.equal(readFileSync(reportPath, "utf8"), reportBytes);
  });
}

test("operations contract preserves high-risk CEF, rollback, retention, and redaction boundaries", () => {
  for (const phrase of [
    "no automatic downgrade", "partial GA", "remote-alert service", "kill switch", "Cargo.lock", "compatibility matrix",
    "new complete signed private candidate", "all ten signed manifests", "rollback is forbidden", "30 days", "PostgreSQL", "R2",
    "QuarantineUpload", "DeleteUpload", "RestoreAccount", "DiagnosticsService", "Never print secrets",
  ]) assert.match(operations, new RegExp(phrase.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "iu"), phrase);
  assert.match(workflowContract, /may upload only its bounded metadata report artifact/u);
  assert.match(support, /high-severity response/iu);
  assert.match(support, /cancelling the run can invoke the mutating `rollback_pre_store` cleanup path/iu);
  assert.match(support, /withhold the next protected approval instead of cancelling/iu);
  assert.match(support, /Report server-side purge completion once the applicable database, R2, and tombstone boundaries are confirmed/iu);
  assert.match(support, /track per-device secure-store cleanup separately as best-effort reconciliation/iu);
  assert.match(operations, /Report server-side purge completion once the applicable database, R2, and tombstone boundaries are confirmed/iu);
  assert.match(operations, /track per-device secure-store cleanup separately as best-effort reconciliation/iu);
});

test("Chrome operations and support retain explicit availability and exposure boundaries", () => {
  const controller = readFileSync(`${root}/docs/servers-devhud-release-controller-contract.md`, "utf8");
  const project = readFileSync(`${root}/docs/project-devhud.md`, "utf8");
  const agents = readFileSync(`${root}/AGENTS.md`, "utf8");
  for (const text of [operations, support, controller, project, agents]) {
    assert.match(text, /`PUBLISHED`/u);
    assert.match(text, /100 percent|equal to 100/u);
    assert.match(text, /unknown or missing|unknown, or missing/u);
    assert.match(text, /takedown|takenDown/u);
    assert.match(text, /automatic withdrawal|withdrawal refusal/iu);
    assert.match(text, /regardless of its state|including tester, unknown, or missing states/u);
  }
});

test("CI validates release fixtures without publication authority", () => {
  assert.match(ciWorkflow, /permissions:\n  contents: read\n  pull-requests: read/u);
  assert.doesNotMatch(ciWorkflow, /\$\{\{\s*secrets\./u);
  for (const job of [
    "ci-contracts", "devhud-frontend", "devhud-extension", "devhud-rust-conformance",
    "devhud-security", "devhud-desktop", "devhud-mobile-contracts", "devhud-ios-simulator",
    "devhud-android-emulator", "delidev-protocol", "delidev-client", "delidev-frontend", "devhud-admin", "devhud-api", "devhud-oci",
    "devhud-supply-chain", "devhud-release-contracts", "ci-result",
  ]) assert.match(ciWorkflow, new RegExp(`\\n  ${job}:`, "u"), job);
  for (const forbidden of [
    /\bgit push\b/iu,
    /\bgh release (?:create|edit|upload|delete)/iu,
    /\bdocker push\b/iu,
    /\bcosign sign\b/iu,
    /wrangler\s+pages\s+deploy/iu,
    /devhud-store-release\.mjs\s+(?:submit|publish|withdraw)/iu,
    /devhud-release-controller\.mjs\s+(?:prepare|promote|rollback)/iu,
  ]) assert.doesNotMatch(ciWorkflow, forbidden);
  assert.match(workflowContract, /CI never builds a signed private candidate and never publishes/iu);
  assert.match(operations, /CI never builds a signed private candidate and never publishes/iu);
  assert.match(support, /validation evidence only/iu);
});

test("CI cache identity has no release authority and preserves non-cacheable native checks", () => {
  for (const text of [workflowContract, operations, support]) assert.match(text, /cache-only|Cache-only|Cache access only|Remote Cache access only/u);
  const action = yaml.load(readFileSync(`${root}/.github/actions/setup-turbo-cache/action.yml`, "utf8"));
  const auth = action.runs.steps.find(({ id }) => id === "auth");
  assert.equal(auth.continueOnError, undefined);
  assert.equal(auth["continue-on-error"], true);
  assert.match(auth.uses, /@49d7b1b46ba4c9251e1977986bfe18336feabc8f$/u);
  assert.match(auth.if, /head.repo.full_name == github.repository/u);
  const tasks = JSON.parse(readFileSync(`${root}/apps/devhud/turbo.json`, "utf8")).tasks;
  for (const name of ["ci:clean-frontend", "mobile:check", "test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:native:updater"]) assert.equal(tasks[name].cache, false, name);
});
