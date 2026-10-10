// SPDX-License-Identifier: Apache-2.0
// Use host-supplied Playwright; it is not a product or workspace dependency.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createClient } from "@connectrpc/connect";
import { QaRun } from "./run.mjs";
import { RegistrationStep, registrationDiagnostics } from "./browser-registration-diagnostics.mjs";
import { document } from "./environment.mjs";
import { list } from "./cleanup.mjs";
import { until } from "./processes.mjs";

const module = process.env.DELIDEV_QA_PLAYWRIGHT_MODULE;
const { chromium, errors } = await import(module ? pathToFileURL(resolve(module)).href : "playwright");
const screenshotPolicy = process.env.DELIDEV_QA_SCREENSHOTS ?? "enabled";
assert(["enabled", "disabled"].includes(screenshotPolicy), "Invalid QA screenshot policy");
const screenshotsEnabled = screenshotPolicy === "enabled";
const run = new QaRun({ workers: 2 });
let browser, stage = "startup", passed = false;
const checks = [];
const registration = registrationDiagnostics(errors.TimeoutError);
try {
  await run.start();
  browser = await chromium.launch({ headless: true, channel: process.env.DELIDEV_QA_BROWSER_CHANNEL ?? "chrome" });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const pages = await Promise.all(run.environments.map(() => context.newPage()));
  const pageErrors = [];
  pages.forEach(page => page.on("pageerror", () => pageErrors.push("renderer-error")));
  await Promise.all(pages.map((page, index) => page.goto(run.environments[index].host.origin)));
  const category = async (page, name) => {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const toggle = page.getByRole("button", { name: "Open settings categories", exact: true });
    if (await toggle.isVisible()) await toggle.click();
    await page.getByRole("button", { name, exact: true }).click();
    await page.locator(".settings-content h1:visible").filter({ hasText: name }).waitFor();
  };
  stage = "worker-repository-registration";
  await Promise.all(pages.map(async (page, index) => {
    const environment = run.environments[index], path = join(environment.root, "browser-git-fixture");
    const step = (substage, operation) => registration.step(index + 1, substage, operation);
    await step(RegistrationStep.GitFixture, async () => {
      await mkdir(path);
      await run.processes.run("git", ["init", "--initial-branch=main", path]);
      await run.processes.run("git", ["-C", path, "-c", "user.name=QA", "-c", "user.email=qa@example.invalid", "commit", "--allow-empty", "-m", "Browser QA fixture"]);
      await run.processes.run("git", ["-C", path, "remote", "add", "origin", "https://github.com/fixture/browser-git-fixture.git"]);
    });
    await step(RegistrationStep.OpenRepositories, () => category(page, "Repositories"));
    await step(RegistrationStep.OpenDialog, () => page.getByRole("button", { name: "Add repository", exact: true }).click());
    await step(RegistrationStep.FillURL, () => page.getByRole("textbox", { name: "Git URL", exact: true }).fill("https://github.com/fixture/browser-git-fixture.git"));
    await step(RegistrationStep.OpenLocalFolder, () => page.getByRole("button", { name: "Connect a Local folder (optional)", exact: true }).click());
    await step(RegistrationStep.NativePickerDisabled, async () => assert(await page.getByRole("button", { name: "Choose folder", exact: true }).isDisabled()));
    await step(RegistrationStep.OpenManualPath, () => page.getByRole("button", { name: "Enter a path…", exact: true }).click());
    await step(RegistrationStep.FillPath, () => page.getByLabel("Absolute checkout path", { exact: true }).fill(path));
    await step(RegistrationStep.Inspect, () => page.getByRole("button", { name: "Inspect folder", exact: true }).click());
    await step(RegistrationStep.ObserveInspection, () => page.getByRole("region", { name: "Repository detected" }).waitFor({ timeout: 30_000 }));
    await step(RegistrationStep.Save, () => page.getByRole("dialog", { name: "Add repository", exact: true }).getByRole("button", { name: "Add repository", exact: true }).click());
    const repository = await step(RegistrationStep.ObserveResource, () => until(async () => {
      const resources = await list(environment, run.api.EntityKind.REPOSITORY);
      return resources.length === 1 ? resources[0] : undefined;
    }));
    await step(RegistrationStep.ObserveRow, async () => {
      assert.equal(document(repository).name, "browser-git-fixture");
      // Settings action names include their original target identity. Bind this
      // exact selector to the resource just observed on this environment;
      // a name-only selector cannot match the scoped action presentation.
      await page.getByRole("button", { name: `Edit browser-git-fixture · ${repository.id}`, exact: true }).waitFor({ timeout: 30_000 });
    });
  }));
  checks.push("real-worker-git-inspection-and-save");

  stage = "parallel-same-name-writes";
  const create = async (page, kind) => {
    await category(page, kind === "Project" ? "Projects" : "Instructions");
    await page.getByRole("button", { name: `New ${kind}`, exact: true }).click();
    await page.getByLabel("Name", { exact: true }).fill(`Parallel ${kind}`);
    if (kind === "Project") {
      const select = page.getByRole("combobox", { name: /^Add Repository/ });
      const options = select.locator('option[value]:not([value=""])');
      await options.first().waitFor({ state: "attached" });
      const option = await options.first().getAttribute("value");
      await select.selectOption(option); await page.getByRole("button", { name: "Add selected", exact: true }).click();
      await page.getByRole("combobox", { name: /^Primary repository/ }).selectOption(option);
    } else await page.getByLabel("Instructions", { exact: true }).fill("Synthetic QA instructions.");
    await page.getByRole("button", { name: `Save ${kind}`, exact: true }).click();
    await page.getByRole("button", { name: `Edit Parallel ${kind}`, exact: true }).waitFor();
  };
  for (const kind of ["Project", "Instructions"]) {
    await Promise.all(pages.map(page => create(page, kind)));
    await pages[0].getByRole("button", { name: `Edit Parallel ${kind}`, exact: true }).click();
    await pages[0].getByLabel("Name", { exact: true }).fill(`Changed ${kind}`);
    await pages[0].getByRole("button", { name: `Save ${kind}`, exact: true }).click();
    await pages[0].getByRole("button", { name: `Edit Changed ${kind}`, exact: true }).waitFor();
    await Promise.all(pages.map(page => page.reload()));
    await Promise.all(pages.map(page => category(page, kind === "Project" ? "Projects" : "Instructions")));
    assert.equal(await pages[0].getByRole("button", { name: `Edit Parallel ${kind}`, exact: true }).count(), 0);
    await pages[1].getByRole("button", { name: `Edit Parallel ${kind}`, exact: true }).waitFor();
    await pages[0].getByRole("button", { name: `Delete Changed ${kind}`, exact: true }).click();
    await pages[0].getByRole("button", { name: "Confirm configuration deletion", exact: true }).click();
    await pages[0].getByRole("button", { name: `New ${kind}`, exact: true }).waitFor();
    await pages[1].reload(); await category(pages[1], kind === "Project" ? "Projects" : "Instructions");
    await pages[1].getByRole("button", { name: `Edit Parallel ${kind}`, exact: true }).waitFor();
    const kindValue = kind === "Project" ? run.api.EntityKind.PROJECT : run.api.EntityKind.TEMPLATE;
    assert.equal((await list(run.environments[0], kindValue)).length, 0);
    assert.equal(document((await list(run.environments[1], kindValue))[0]).name, `Parallel ${kind}`);
  }
  checks.push("same-profile-parallel-project-and-instructions-create-edit-delete-reload");

  stage = "settings-task-disposal";
  const taskPage = pages[0], environment = run.environments[0];
  await category(taskPage, "Instructions");
  for (const dismissal of ["X", "Escape", "Cancel"]) {
    stage = `settings-task-disposal-${dismissal}`;
    let admitted = false, settled = false, requests = 0, release;
    const responseGate = new Promise(resolve => { release = resolve; });
    // The real Go server accepts this write. Delay only its browser response so
    // dismissal is checked independently from authoritative server completion.
    const routePattern = "**/delidev.v1.ConfigurationService/SaveConfiguration";
    const routeHandler = async route => {
      requests++;
      const response = await route.fetch();
      admitted = true;
      await responseGate;
      try { await route.fulfill({ response }); } catch { /* The closed task aborted its client wait. */ }
      settled = true;
    };
    await taskPage.route(routePattern, routeHandler);
    try {
      await taskPage.getByRole("button", { name: "New Instructions", exact: true }).click();
      // Settle the modal's initial focus before driving rapid scripted edits.
      await until(() => taskPage.getByLabel("Name", { exact: true }).evaluate(node => node === document.activeElement));
      await taskPage.getByLabel("Name", { exact: true }).fill(`Closed ${dismissal} task`);
      await taskPage.getByLabel("Instructions", { exact: true }).fill("Synthetic task lifecycle fixture.");
      await taskPage.getByRole("button", { name: "Save Instructions", exact: true }).click();
      await until(() => admitted);
      if (dismissal === "Escape") await taskPage.keyboard.press("Escape");
      else await taskPage.getByRole("button", { name: dismissal === "X" ? "Close New Instructions" : "Cancel edit", exact: true }).click();
      assert.equal(await taskPage.getByRole("dialog").count(), 0);
      assert.equal(await taskPage.locator(".settings-task-background[disabled], .settings-task-background[inert]").count(), 0);
      assert.equal(await taskPage.getByRole("button", { name: "View original operation", exact: true }).count(), 0);
      const opener = taskPage.getByRole("button", { name: "New Instructions", exact: true });
      await until(() => opener.evaluate(node => node === document.activeElement));
      await opener.click();
      const name = taskPage.getByLabel("Name", { exact: true });
      assert.equal(await name.inputValue(), "");
      await name.fill("Fresh task draft"); await name.focus();
      release(); await until(() => settled);
      assert.equal(await name.inputValue(), "Fresh task draft");
      assert(await name.evaluate(node => node === document.activeElement));
      assert.equal(await taskPage.getByRole("dialog").count(), 1);
      assert.equal(requests, 1);
      assert((await list(environment, run.api.EntityKind.TEMPLATE)).some(resource => document(resource).name === `Closed ${dismissal} task`));
      await taskPage.keyboard.press("Escape");
    } finally { release(); await taskPage.unroute(routePattern, routeHandler); }
  }
  checks.push("settings-X-Escape-local-cancel-disposal-admitted-save-fresh-task-and-late-focus-fence");

  stage = "appearance-isolation";
  await Promise.all(pages.map(page => category(page, "Appearance")));
  await pages[0].getByRole("radio", { name: "Dark", exact: true }).check();
  await pages[1].getByRole("radio", { name: "Light", exact: true }).check();
  await Promise.all(pages.map(page => page.reload()));
  await pages[0].waitForFunction(() => document.documentElement.dataset.theme === "dark");
  await pages[1].waitForFunction(() => document.documentElement.dataset.theme === "light");
  for (const page of pages) assert(await page.evaluate(() => Object.keys(localStorage).every(key => key.startsWith("delidev-qa-appearance:")) && sessionStorage.length === 0 && !("__TAURI_INTERNALS__" in window)));
  checks.push("appearance-only-storage-and-no-native-emulation");

  stage = "server-stop-reload-explicit-start";
  await pages[0].locator("details.local-server summary").click();
  await pages[0].getByRole("button", { name: "Stop local server", exact: true }).click();
  await pages[0].getByRole("button", { name: "Confirm server stop", exact: true }).click();
  await until(async () => (await run.environments[0].status()).state === "stopped");
  await pages[0].reload();
  await pages[0].getByRole("heading", { name: "DeliDev browser QA unavailable", exact: true }).waitFor();
  await pages[0].getByRole("button", { name: "Start local server", exact: true }).click();
  await pages[0].getByRole("button", { name: "Settings", exact: true }).waitFor();
  assert.equal((await run.environments[1].status()).state, "ready");
  checks.push("server-product-stop-reload-and-explicit-start");

  stage = "worker-stop-restart";
  await category(pages[0], "Runner Devices");
  await pages[0].getByRole("button", { name: "Stop local Worker", exact: true }).click();
  await pages[0].getByRole("button", { name: "Confirm Worker stop", exact: true }).click();
  await pages[0].getByRole("button", { name: "Start local Worker", exact: true }).waitFor({ timeout: 30_000 });
  assert.equal((await run.environments[1].workerStatus()).state, "running");
  await pages[0].getByRole("button", { name: "Start local Worker", exact: true }).click();
  await pages[0].getByRole("button", { name: "Stop local Worker", exact: true }).waitFor({ timeout: 70_000 });
  await run.environments[0].verifyWorker(); checks.push("actual-worker-explicit-stop-and-restart");

  stage = "failure-and-revocation-isolation";
  const [a, b] = run.environments, api = run.api;
  await a.processes.stop(a.server);
  await until(async () => (await a.status()).state === "stopped");
  assert.equal((await b.status()).state, "ready");
  await a.startServer();
  const row = (await list(a, api.EntityKind.DEVICE)).find(resource => resource.id === a.clientCredential.device_id);
  await createClient(api.DeviceService, a.ownerTransport).revokeDevice({ mutation: { id: row.id, expectedRevision: row.revision, requestId: api.newRequestId() } });
  await until(async () => (await a.status()).state === "blocked");
  await pages[0].reload(); await pages[0].getByRole("heading", { name: "DeliDev browser QA unavailable", exact: true }).waitFor();
  await pages[1].reload(); await category(pages[1], "Projects");
  await pages[1].getByRole("button", { name: "Edit Parallel Project", exact: true }).waitFor();
  checks.push("server-failure-and-paired-auth-revocation-isolation");
  assert.equal(pageErrors.length, 0);
  if (screenshotsEnabled) await Promise.all(pages.map((page, index) => page.screenshot({ path: join(run.artifacts, "screenshots", `worker-${index + 1}.png`) })));
  passed = true;
} finally {
  if (screenshotsEnabled && !passed && browser && run.artifacts) await Promise.all(browser.contexts()[0].pages().map((page, index) => page.screenshot({ path: join(run.artifacts, "screenshots", `failed-worker-${index + 1}.png`) }).catch(() => {})));
  await browser?.close(); const cleanup = await run.close();
  const record = { operation: "qa-browser-validation", command: "pnpm test:qa:browser", result: passed ? "passed" : "failed", stage, source: run.source, checks, registrationFailures: registration.records(), cleanup: cleanup.environments, nativeAcceptance: "not-performed", accountAcceptance: "not-performed", screenshots: screenshotPolicy };
  if (run.artifacts) await writeFile(join(run.artifacts, "browser-validation.json"), JSON.stringify(record, null, 2), { mode: 0o600 });
  console.log(JSON.stringify({ ...record, artifacts: run.artifacts }));
  if (passed) assert(cleanup.environments.every(environment => environment.state === "deleted"), "Original environment cleanup remains unconfirmed");
}
