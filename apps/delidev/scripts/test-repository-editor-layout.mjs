// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-repository-editor-"));
const previews = process.env.DELIDEV_LAYOUT_REPOSITORY_SCREENSHOTS === "1" ? await mkdtemp(join(tmpdir(), "delidev-github-preview-")) : null;
let browser, server, page;
let cases = 0;
const messages = new Map(), catalogs = { en: {}, ko: {} };
for (const file of await readdir(join(app, "src/locales/en"))) {
  if (!file.endsWith(".json")) continue;
  const en = JSON.parse(await readFile(join(app, "src/locales/en", file))), ko = JSON.parse(await readFile(join(app, "src/locales/ko", file)));
  Object.assign(catalogs.en, en); Object.assign(catalogs.ko, ko);
  for (const [key, value] of Object.entries(en)) if (!messages.has(value)) messages.set(value, ko[key]);
}
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`);
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  page = await browser.newPage();
  const errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440, 900], [1280, 820], [960, 640], [640, 480], [480, 320]]) {
    const l = value => language === "ko" ? messages.get(value) ?? value : value;
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?theme=${theme}&populated=true&language=${language}`);
    await page.getByRole("button", { name: l("Settings"), exact: true }).click();
    if (width < 760) await page.getByRole("button", { name: catalogs[language]["App.open_a007d6"].replace("<s0/>", catalogs[language]["App.extra.a1de4eceaa3b"]), exact: true }).click();
    await page.getByRole("button", { name: l("Repositories"), exact: true }).click();
    assert.equal(await page.locator(".repository-github-tools").count(), 0, "Saved repository cards contain no GitHub content browser or empty tools wrapper");
    assert.equal(await page.getByRole("button", { name: l("Browse GitHub items"), exact: true }).count(), 0);
    assert.equal(await page.getByRole("button", { name: l("Close GitHub items"), exact: true }).count(), 0);
    const opener = page.locator('.repository-row .repository-manage-actions button').first();
    await opener.click();
    const dialog = page.locator('dialog.settings-task-dialog[open]');
    const form = dialog.locator('form.repository-editor'); await form.waitFor();
    assert.deepEqual(await form.locator('.repository-disclosure').evaluateAll(nodes => nodes.map(node => node.open)), [false, false]);
    assert.equal(await form.locator('input').first().inputValue(), `Example REPOSITORY ${"long-name-".repeat(15)}`);
    assert.equal(await dialog.locator('.settings-task-footer [data-settings-task-cancel]').count(), 0);
    const dimensions = await dialog.evaluate(node => {
      const body = node.querySelector('.settings-task-body'), form = node.querySelector('form'), footer = node.querySelector('.settings-task-footer'), rect = node.getBoundingClientRect();
      const inner = body.clientWidth - parseFloat(getComputedStyle(body).paddingLeft) - parseFloat(getComputedStyle(body).paddingRight);
      return { width: rect.width, overflow: body.scrollWidth > body.clientWidth, formWidth: form.getBoundingClientRect().width, inner, footerBottom: footer.getBoundingClientRect().bottom, bottom: rect.bottom };
    });
    assert(!dimensions.overflow && dimensions.width <= width - 31, JSON.stringify(dimensions));
    assert(Math.abs(dimensions.formWidth - dimensions.inner) < 2, `Full width: ${JSON.stringify(dimensions)}`);
    assert(dimensions.footerBottom <= dimensions.bottom && dimensions.bottom <= height, "Visible fixed footer");
    const columns = await form.locator('.repository-github-columns').evaluate(node => ({ columns: getComputedStyle(node).gridTemplateColumns.split(' ').length, width: node.getBoundingClientRect().width }));
    assert.equal(columns.columns, columns.width < 640 ? 1 : 2);
    for (const key of ["Tab", "Shift+Tab"]) for (let index = 0; index < 12; index++) { await page.keyboard.press(key); assert(await dialog.evaluate(node => node.contains(document.activeElement)), "Focus remains contained"); }
    const advanced = form.locator('.repository-disclosure').last(); await advanced.locator('summary').click();
    const remote = form.getByRole('textbox', { name: l('Preferred Git remote'), exact: true }); await remote.fill('upstream');
    await advanced.locator('summary').click(); await advanced.locator('summary').click(); assert.equal(await remote.inputValue(), 'upstream');
    assert(await dialog.locator('.settings-task-body').evaluate(node => node.scrollWidth <= node.clientWidth), "Expanded body wraps");
    await page.keyboard.press('Escape'); assert.equal(await page.locator('dialog.settings-task-dialog[open]').count(), 0);
    assert(await opener.evaluate(node => node === document.activeElement), "Opener focus restored"); cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: 'repository_editor_layout', cases, nativeAcceptance: 'not-performed' }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
