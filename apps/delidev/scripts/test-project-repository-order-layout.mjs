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
const directory = await mkdtemp(join(tmpdir(), "delidev-project-order-"));
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
    await page.goto(`${origin}/?theme=${theme}&populated=true&language=${language}&projectList=true&projectWizard=true`);
    await page.getByRole("button", { name: l("Settings"), exact: true }).click();
    if (width < 760) await page.getByRole("button", { name: catalogs[language]["App.open_a007d6"].replace("<s0/>", catalogs[language]["App.extra.a1de4eceaa3b"]), exact: true }).click();
    await page.getByRole("button", { name: l("Projects"), exact: true }).click();
    const row = page.locator('.project-metadata-row').first(); await row.waitFor();
    await row.locator('button').filter({ hasText: l('Edit') }).click();
    await page.getByRole("tab", { name: language === "ko" ? "저장소" : "Repositories", exact: true }).click();
    const dialog = page.locator('.settings-task-dialog').filter({ has: page.locator('.project-repository-order') });
    const list = dialog.locator('.project-repository-order'), grips = list.locator('.project-repository-grip');
    await grips.last().waitFor();
    await page.waitForFunction(() => document.querySelector('.project-repository-order .project-repository-name')?.textContent?.includes('Configured repository'));
    const ids = await list.locator('li').evaluateAll(rows => rows.map(row => row.dataset.repositoryId));
    assert.equal(ids.length, 5);
    const primary = await dialog.getByRole('combobox', { name: catalogs[language]['configuration-fields.primaryRepository_b2bbc5'], exact: true }).inputValue();
    const last = grips.last(); await last.focus(); await page.keyboard.press('Space');
    for (let i = 0; i < 4; i++) await page.keyboard.press('ArrowUp');
    assert.deepEqual(await list.locator('li').evaluateAll(rows => rows.map(row => row.dataset.repositoryId)), [ids[4], ...ids.slice(0, 4)]);
    assert(await page.locator(`[data-repository-id="${ids[4]}"] .project-repository-grip`).evaluate(node => node === document.activeElement));
    await page.keyboard.press('Escape');
    assert.deepEqual(await list.locator('li').evaluateAll(rows => rows.map(row => row.dataset.repositoryId)), ids);
    assert(await dialog.isVisible(), 'movement Escape keeps dialog open');
    await grips.last().focus(); await page.keyboard.press('Enter'); await page.keyboard.press('ArrowUp'); await page.keyboard.press('Enter');
    assert.deepEqual(await list.locator('li').evaluateAll(rows => rows.map(row => row.dataset.repositoryId)), [...ids.slice(0, 3), ids[4], ids[3]]);
    assert.equal(await dialog.getByRole('combobox', { name: catalogs[language]['configuration-fields.primaryRepository_b2bbc5'], exact: true }).inputValue(), primary);
    if (height >= 640) {
      await list.locator('li').first().scrollIntoViewIfNeeded();
      const source = await grips.nth(1).boundingBox(), destination = await grips.first().boundingBox();
      const beforePointer = await list.locator('li').evaluateAll(rows => rows.map(row => row.dataset.repositoryId));
      await page.mouse.move(source.x + source.width / 2, source.y + source.height / 2); await page.mouse.down();
      await page.mouse.move(destination.x + destination.width / 2, destination.y + destination.height / 2, { steps: 4 });
      await page.mouse.up();
      assert.deepEqual(await list.locator('li').evaluateAll(rows => rows.map(row => row.dataset.repositoryId)), [beforePointer[1], beforePointer[0], ...beforePointer.slice(2)]);
    }
    assert.equal(await page.evaluate(() => Number(document.documentElement.dataset.fixtureProjectSaveCount ?? '0')), 0);
    assert.equal(await list.locator('.project-repository-identity').count(), 0);
    assert.equal(await list.locator('.project-repository-secondary-id').count(), 0);
    const geometry = await dialog.evaluate(node => ({ overflow: node.scrollWidth > node.clientWidth, rows: [...node.querySelectorAll('.project-repository-order li')].some(row => row.scrollWidth > row.clientWidth), heights: [...node.querySelectorAll('.project-repository-grip')].map(grip => grip.getBoundingClientRect().height) }));
    assert(!geometry.overflow && !geometry.rows && geometry.heights.every(height => height >= 40), JSON.stringify({width, height, language, theme, geometry}));
    cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: 'project_repository_order_layout', cases, nativeAcceptance: 'not-performed' }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
