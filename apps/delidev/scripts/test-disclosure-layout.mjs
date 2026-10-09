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
const directory = await mkdtemp(join(tmpdir(), "delidev-disclosure-check-"));
let browser, server;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/disclosure-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try { const pathname = new URL(request.url, "http://127.0.0.1").pathname, file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`); if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path"); response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css" }[extname(file)] ?? "application/octet-stream"); response.end(await readFile(file)); }
    catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage(), errors = []; page.on("pageerror", error => errors.push(error.message));
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark", "system"]) for (const zoom of [1, 2]) {
    await page.setViewportSize({ width: 320 * zoom, height: 800 * zoom });
    await page.goto(`http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&zoom=${zoom}`);
    const field = page.locator("[data-native-field]"), summary = page.locator("details > summary").first(), native = page.locator("details").first();
    await field.waitFor(); await field.focus();
    await summary.evaluate(node => { const focus = node.focus; node.focus = function(options) { if (node.parentElement.contains(document.activeElement) && document.activeElement !== node) node.parentElement.querySelector("input").dataset.beforeHide = String(node.parentElement.open); return focus.call(this, options); }; });
    await page.locator("[data-owner-close]").evaluate(button => button.click());
    await page.waitForFunction(() => document.querySelector("details").open === false);
    assert.equal(await summary.evaluate(node => node === document.activeElement), true);
    assert.equal(await field.getAttribute("data-before-hide"), "true", "Controlled React close restores focus while native content is still open");
    assert.equal(await summary.evaluate(node => document.getElementById(node.getAttribute("aria-controls"))?.className), "disclosure-native-content");
    for (const operation of ["property", "remove", "toggle"]) {
      await page.locator("[data-owner-open]").evaluate(button => button.click()); await field.focus();
      await native.evaluate((node, operation) => { node.setAttribute("open", ""); if (operation === "property") node.open = false; else if (operation === "remove") node.removeAttribute("open"); else node.toggleAttribute("open", false); }, operation);
      assert.equal(await summary.evaluate(node => node === document.activeElement), true);
      assert.equal(await field.getAttribute("data-before-hide"), "true");
      await page.locator("[data-owner-close]").evaluate(button => button.click());
    }
    await summary.focus(); await page.keyboard.press("Enter"); assert.equal(await native.evaluate(node => node.open), true);
    await page.keyboard.press("Space"); assert.equal(await native.evaluate(node => node.open), false);
    const controlled = page.locator("button.disclosure-header"), draft = page.locator("[data-controlled-field]");
    await controlled.focus(); await page.keyboard.press("Space"); assert.equal(await draft.isVisible(), false);
    await page.keyboard.press("Enter"); assert.equal(await draft.isVisible(), true); assert.equal(await draft.inputValue(), "unsent controlled draft");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  }
  assert.deepEqual(errors, []); console.log("Disclosure browser checks passed: controlled/native focus before hide, property/attribute paths, keyboard, associations, themes and reflow.");
} finally { await browser?.close(); if (server) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
