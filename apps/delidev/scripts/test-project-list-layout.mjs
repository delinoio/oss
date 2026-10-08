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
const directory = await mkdtemp(join(tmpdir(), "delidev-project-list-"));
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
    await page.goto(`${origin}/?theme=${theme}&populated=true&language=${language}&projectList=true`);
    await page.getByRole("button", { name: l("Settings"), exact: true }).click();
    if (width < 760) await page.getByRole("button", { name: catalogs[language]["App.open_a007d6"].replace("<s0/>", catalogs[language]["App.extra.a1de4eceaa3b"]), exact: true }).click();
    await page.getByRole("button", { name: l("Projects"), exact: true }).click();
    const row = page.locator('.project-metadata-row').first(); await row.waitFor();
    await row.getByText(`Configured repository 1 ${"complete-name-".repeat(10)}`, { exact: true }).waitFor();
    assert.equal(await row.locator('.project-repository-rows > li').count(), 3);
    assert.equal(await row.locator('.project-primary-badge').count(), 0);
    assert.equal(await row.locator('a').count(), 0);
    assert.equal(await row.locator('.project-original-details').evaluate(node => node.open), false);
    assert.equal(await row.getByText(l("URL not configured"), { exact: true }).count(), 1);
    const show = row.locator('.project-show-repositories'); await show.focus(); await page.keyboard.press('Space');
    assert.equal(await row.locator('.project-repository-rows > li').count(), 5);
    assert.equal(await row.locator('.project-primary-badge').count(), 1);
    assert(await show.evaluate(node => node === document.activeElement), "Show all keeps focus");
    assert.equal(await row.locator('.project-repository-url').last().textContent(), 'git@example.org:team/complete-source.git');
    await page.keyboard.press('Space'); assert.equal(await row.locator('.project-repository-rows > li').count(), 3);
    const summary = row.locator('.project-original-details summary'); await summary.focus(); await page.keyboard.press('Enter');
    assert.equal(await row.locator('.project-original-details').evaluate(node => node.open), true);
    assert.equal(await row.locator('.project-original-details dd ol li').count(), 5);
    const geometry = await page.evaluate(() => {
      const row = document.querySelector('.project-metadata-row'), content = document.querySelector('.settings-content');
      return { overflow: content.scrollWidth > content.clientWidth, rowOverflow: row.scrollWidth > row.clientWidth, pageOverflow: document.documentElement.scrollWidth > innerWidth, column: document.querySelector('.settings-content-column').getBoundingClientRect().width, buttonHeights: [...row.querySelectorAll('button')].map(node => node.getBoundingClientRect().height), direction: getComputedStyle(row.querySelector('.project-row-heading')).flexDirection };
    });
    assert(!geometry.overflow && !geometry.rowOverflow && !geometry.pageOverflow, JSON.stringify({ width, height, language, theme, ...geometry }));
    assert(geometry.column <= 1041 && geometry.buttonHeights.every(value => value >= 40), JSON.stringify(geometry));
    assert.equal(geometry.direction, width < 1100 ? 'column' : 'row');
    const refresh = page.getByRole('button', { name: l('Refresh settings'), exact: true }); await refresh.focus(); await refresh.click();
    await row.getByText(`Configured repository 1 ${"complete-name-".repeat(10)}`, { exact: true }).waitFor();
    assert(await refresh.evaluate(node => node === document.activeElement), "Refresh cannot move focus");
    cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: 'project_list_layout', cases, nativeAcceptance: 'not-performed' }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
