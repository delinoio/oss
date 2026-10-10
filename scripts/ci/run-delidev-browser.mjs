// SPDX-License-Identifier: Apache-2.0
// Closed hosted fixture inventory. No native app or hosted account is admitted.
import { spawn, execFileSync } from "node:child_process";
import { mkdir, readFile, realpath, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
export const browserChecks = Object.freeze([
  { id: "qa-browser", script: "apps/delidev/scripts/qa/test-browser.mjs", operation: "qa-browser-validation" },
  { id: "worker-model-layout", script: "apps/delidev/scripts/test-worker-model-layout.mjs", operation: "worker-model-layout" },
  { id: "sidebar-collapse-layout", script: "apps/delidev/scripts/test-sidebar-collapse-layout.mjs", operation: "sidebar-collapse-layout" },
  { id: "command-menu-layout", script: "apps/delidev/scripts/test-command-menu-layout.mjs", operation: "command-menu-layout" },
  { id: "settings-layout", script: "apps/delidev/scripts/test-settings-layout.mjs", operation: "settings_layout", counts: ["categoryChecks", "childFormChecks", "keyboardChecks"], passed: true },
  { id: "settings-actions-layout", script: "apps/delidev/scripts/test-settings-actions-layout.mjs", operation: "settings_action_icons", counts: ["categoryChecks", "tooltipChecks"], passed: true },
  { id: "settings-search-layout", script: "apps/delidev/scripts/test-settings-search-layout.mjs", operation: "settings_search_layout", counts: ["checks"], passed: true },
  { id: "shortcut-settings-layout", script: "apps/delidev/scripts/test-shortcut-settings-layout.mjs", operation: "shortcut-settings-layout", counts: ["cases"] },
  { id: "creation-skill-layout", script: "apps/delidev/scripts/test-creation-skill-layout.mjs", operation: "creation-skill-layout", counts: ["cases", "overlayCases"] },
  { id: "pr-cards-layout", script: "apps/delidev/scripts/test-pr-cards-layout.mjs", operation: "pr-list-cards-browser", counts: ["checks"], passed: true },
  { id: "session-actions-layout", script: "apps/delidev/scripts/test-session-actions-layout.mjs", evidence: "Synthetic Chromium session status/action geometry and keyboard checks; no native CEF acceptance", counts: ["checks"] },
]);
const repository = fileURLToPath(new URL("../..", import.meta.url));
export function browserEnvironment(source, root = repository) {
  const module = source.DELIDEV_BROWSER_PLAYWRIGHT_MODULE, evidence = source.DELIDEV_BROWSER_EVIDENCE_DIR;
  if (!module || !isAbsolute(module) || !evidence || !isAbsolute(evidence)) throw new Error("browser-fixture-host-missing");
  const directory = resolve(evidence), checkout = resolve(root);
  if (directory === checkout || directory.startsWith(`${checkout}${sep}`)) throw new Error("browser-evidence-must-be-external");
  const safe = { ...source };
  // Existing layout scripts expose optional image capture and narrower modes.
  // Hosted validation always runs their complete assertions without image output.
  for (const key of ["DELIDEV_LAYOUT_SCREENSHOT_DIR", "DELIDEV_LAYOUT_SCREENSHOT", "DELIDEV_SEARCH_SCREENSHOT_DIR", "DELIDEV_PR_CARDS_SCREENSHOT_DIR",
    "DELIDEV_LAYOUT_GITHUB_ONLY", "DELIDEV_LAYOUT_WIZARD_ACCOUNTS_ONLY", "DELIDEV_LAYOUT_ACCOUNTS_ONLY", "DELIDEV_LAYOUT_DISMISSAL_ONLY", "DELIDEV_LAYOUT_PROJECTS_ONLY", "DELIDEV_LAYOUT_PROJECT_ROWS_ONLY", "DELIDEV_LAYOUT_LANGUAGE_ONLY"]) delete safe[key];
  return { ...safe, DELIDEV_QA_PLAYWRIGHT_MODULE: module, DELIDEV_LAYOUT_PLAYWRIGHT_MODULE: module,
    DELIDEV_QA_BROWSER_CHANNEL: "chromium", DELIDEV_LAYOUT_BROWSER_CHANNEL: "chromium", DELIDEV_QA_SCREENSHOTS: "disabled" };
}
export function completedEvidence(check, records) {
  const record = records.findLast(value => check.operation ? value.operation === check.operation : value.evidence === check.evidence);
  if (!record || record.result === "failed") throw new Error("browser-fixture-evidence-missing");
  if (check.id === "qa-browser" && (record.result !== "passed" || record.screenshots !== "disabled" || !record.checks?.length || !Array.isArray(record.cleanup) || record.cleanup.length !== 2 || record.cleanup.some(value => value.state !== "deleted"))) throw new Error("browser-qa-acceptance-incomplete");
  if (["worker-model-layout", "sidebar-collapse-layout"].includes(check.id) && (record.result !== "passed" || !(record.checks > 0))) throw new Error("browser-layout-acceptance-incomplete");
  if (check.id === "command-menu-layout" && !(record.cases > 0)) throw new Error("browser-layout-acceptance-incomplete");
  if (check.counts && ((check.passed && record.result !== "passed") || check.counts.some(field => !Number.isSafeInteger(record[field]) || record[field] <= 0))) throw new Error("browser-layout-acceptance-incomplete");
  return record;
}
async function execute(check, env, record) {
  const values = []; let pending = "", overflow = false, stderrBytes = 0, structuredBytes = 0;
  const line = value => {
    if (value.length > 262144 || structuredBytes + value.length > 8 * 1024 * 1024) { overflow = true; return; }
    try { const parsed = JSON.parse(value); if (parsed && typeof parsed === "object") { structuredBytes += value.length; values.push(parsed); record({ check: check.id, command: [process.execPath, check.script], evidence: parsed }); } }
    catch { /* Build progress and raw exceptions are not validation artifacts. */ }
  };
  const child = spawn(process.execPath, [check.script], { cwd: repository, env, shell: false, stdio: ["ignore", "pipe", "pipe"] });
  child.stdout.on("data", bytes => {
    pending += bytes.toString(); let end;
    while ((end = pending.indexOf("\n")) >= 0) { line(pending.slice(0, end)); pending = pending.slice(end + 1); }
    if (pending.length > 262144) { overflow = true; pending = ""; }
  });
  child.stderr.on("data", bytes => { stderrBytes += bytes.length; });
  const result = await new Promise((resolveResult, reject) => {
    child.once("error", () => reject(new Error("browser-fixture-spawn-failed")));
    child.once("close", (code, signal) => resolveResult({ code, signal }));
  });
  if (pending.trim()) line(pending);
  if (result.code !== 0 || overflow) {
    record({ operation: "delidev-browser-check", check: check.id, command: [process.execPath, check.script], result: "failed", classification: overflow ? "evidence-bound-exceeded" : "fixture-exit-failed", exitCode: result.code, signal: result.signal, stderrBytes });
    throw new Error("browser-fixture-failed");
  }
  try { completedEvidence(check, values); }
  catch { record({ operation: "delidev-browser-check", check: check.id, command: [process.execPath, check.script], result: "failed", classification: "fixture-evidence-incomplete" }); throw new Error("browser-fixture-evidence-missing"); }
  record({ operation: "delidev-browser-check", check: check.id, command: [process.execPath, check.script], result: "passed", screenshots: "disabled", nativeAcceptance: "not-performed", accountAcceptance: "not-performed" });
}
async function main() {
  const env = browserEnvironment(process.env), directory = resolve(env.DELIDEV_BROWSER_EVIDENCE_DIR);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const actual = await realpath(directory), checkout = await realpath(repository);
  if (actual === checkout || actual.startsWith(`${checkout}${sep}`)) throw new Error("browser-evidence-must-be-external");
  const metadata = JSON.parse(await readFile(join(dirname(env.DELIDEV_BROWSER_PLAYWRIGHT_MODULE), "package.json"), "utf8"));
  if (metadata.name !== "playwright" || metadata.version !== "1.58.2") throw new Error("browser-fixture-version-mismatch");
  const revision = execFileSync("git", ["rev-parse", "HEAD"], { cwd: repository, encoding: "utf8" }).trim();
  const records = [], record = value => { const item = { revision, ...value }; records.push(item); process.stdout.write(`${JSON.stringify(item)}\n`); };
  let passed = false;
  try {
    record({ operation: "delidev-browser-fixtures", result: "running", playwright: metadata.version, checks: browserChecks.map(check => check.id), screenshots: "disabled" });
    for (const check of browserChecks) await execute(check, env, record);
    passed = true;
  } finally {
    record({ operation: "delidev-browser-fixtures", result: passed ? "passed" : "failed", nativeAcceptance: "not-performed", accountAcceptance: "not-performed", screenshots: "disabled" });
    await writeFile(join(directory, "browser-evidence.jsonl"), records.map(value => JSON.stringify(value)).join("\n") + "\n", { mode: 0o600 });
  }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch(error => { const categories = new Set(["browser-fixture-host-missing", "browser-evidence-must-be-external", "browser-fixture-version-mismatch", "browser-fixture-spawn-failed", "browser-fixture-evidence-missing", "browser-fixture-failed"]); console.error(JSON.stringify({ operation: "delidev-browser-fixtures", result: "failed", classification: categories.has(error.message) ? error.message : "browser-fixture-validation-failed" })); process.exitCode = 1; });
