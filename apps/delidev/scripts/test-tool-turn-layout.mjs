// SPDX-License-Identifier: Apache-2.0
// Automated synthetic browser evidence, without app/native launch or screenshots.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-tool-turn-check-"));
let browser, server;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/tool-turn-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try { const pathname = new URL(request.url, "http://127.0.0.1").pathname, file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`); if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path"); response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css" }[extname(file)] ?? "application/octet-stream"); response.end(await readFile(file)); }
    catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage(), errors = []; page.on("pageerror", error => errors.push(error.message));
  let cases=0;
  for (const language of ["en","ko"]) for (const theme of ["light","dark","system"]) for (const kind of ["session","general-chat"]) for (const width of [1440,480,320]) {
    await page.setViewportSize({width,height:800});await page.goto(`http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&kind=${kind}`);
    const group=page.locator('.tool-turn'),summary=group.locator(':scope > summary'),entry=group.locator('li > details').first(),trigger=entry.locator(':scope > summary');
    await group.waitFor();assert.equal(await group.evaluate(n=>n.open),false);assert.equal(await entry.evaluate(n=>n.open),false);assert.equal(await group.count(),1);
    await summary.focus();assert.equal(await summary.evaluate(n=>getComputedStyle(n).outlineWidth),'2px');await page.keyboard.press('Enter');await trigger.waitFor({state:'visible'});assert.equal(await group.locator('li').count(),3);
    await trigger.focus();await page.keyboard.press('Space');await entry.locator('pre').first().waitFor({state:'visible'});assert.equal(await entry.locator('pre').first().textContent(),'echo  exact\n  original');
    // Original aggregate disclosure remains independently available; its exact localized title is owned by TranscriptItem.
    const nested=entry.locator('details:not([data-tool-primary])').first();await nested.locator(':scope > summary').click();assert.equal(await nested.evaluate(n=>n.open),true);
    await trigger.focus();await page.locator('[data-update]').evaluate(n=>n.click());assert.equal(await trigger.evaluate(n=>n===document.activeElement),true);assert.equal(await entry.evaluate(n=>n.open),true);assert.equal(await nested.evaluate(n=>n.open),true);
    await page.locator('[data-language]').evaluate(n=>n.click());assert.equal(await trigger.evaluate(n=>n===document.activeElement),true);assert.equal(await group.evaluate(n=>n.open),true);assert.equal(await page.locator('[data-reads]').textContent(),'0');
    await page.locator('[data-evict]').click();assert.equal(await group.count(),1);assert.equal(await group.evaluate(n=>n.open),true);assert.equal(await entry.evaluate(n=>n.open),true);if(await entry.getByRole('button').isVisible()) await entry.getByRole('button').click();await page.waitForFunction(()=>document.querySelector('[data-reads]').textContent==='1');assert.equal(await page.locator('[data-reads]').getAttribute('data-tokens'),'[""]');assert.equal(await nested.evaluate(n=>n.open),true);
    assert.equal(await page.locator('[data-open-request] input').isVisible(),true);assert.equal(await page.locator('[data-open-request] input').inputValue(),'unsent original response');assert.equal(await page.getByLabel('Original composer').inputValue(),'retained draft');
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.locator('[data-dispose]').click();assert.equal(await group.evaluate(n=>n.open),false);cases++;
  }
  assert.deepEqual(errors,[]);console.log(JSON.stringify({operation:'tool-turn-layout',result:'passed',cases,languages:2,themes:3,sessionKinds:2,widths:3,nativeAcceptance:'not-performed'}));
} finally { await browser?.close(); if (server) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
