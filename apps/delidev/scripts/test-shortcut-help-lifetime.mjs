// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-shortcut-help-"));
let browser, server, page;
let cases = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/shortcut-help-lifetime.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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

  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[960,640],[480,320]]) {
    await page.setViewportSize({width,height}); await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    const opener=page.locator("[data-help]"), dialog=page.locator(".shortcut-help"); await opener.focus();
    await page.keyboard.down("Shift"); await page.keyboard.down("?"); await dialog.waitFor({state:"visible"});
    assert(await dialog.locator("header button").evaluate(node=>node===document.activeElement));
    const first=await dialog.elementHandle(); await page.keyboard.down("?"); assert(await first.evaluate(node=>node===document.querySelector(".shortcut-help")));
    await page.keyboard.up("Shift"); await page.keyboard.press("a"); assert(await dialog.isVisible());
    await page.keyboard.up("?"); await dialog.waitFor({state:"detached"}); assert(await opener.evaluate(node=>node===document.activeElement));
    await page.keyboard.down("?"); await dialog.waitFor({state:"visible"}); await page.keyboard.press("Escape");
    await page.keyboard.down("?"); assert.equal(await dialog.count(),0); await page.keyboard.up("?");
    await page.keyboard.down("?"); await dialog.waitFor({state:"visible"});
    const primary=await page.evaluate(()=>/mac/i.test(navigator.platform)?"Meta":"Control"); await page.keyboard.press(`${primary}+Shift+N`);
    await dialog.waitFor({state:"detached"}); assert.equal(await page.locator("output").textContent(),"1");
    await page.keyboard.up("?"); assert(await page.getByRole("textbox").evaluate(node=>node===document.activeElement));
    await opener.click(); await dialog.waitFor({state:"visible"}); await page.keyboard.press("?"); assert(await dialog.isVisible());
    await page.evaluate(()=>window.dispatchEvent(new Event("blur"))); assert(await dialog.isVisible()); await page.keyboard.press("Escape");
    await opener.focus(); await page.keyboard.down("?"); await dialog.waitFor({state:"visible"}); await page.evaluate(()=>window.dispatchEvent(new Event("blur"))); await dialog.waitFor({state:"detached"}); await page.keyboard.up("?");
    cases++;
  }
  assert.deepEqual(errors,[]); console.log(JSON.stringify({operation:"shortcut-help-lifetime",cases,nativeAcceptance:"not-performed",blur:"synthetic-window-event",reflow:"narrow-viewport"}));
} finally {
  await browser?.close(); if(server) await new Promise(done=>server.close(done));
  await rm(directory,{recursive:true,force:true});
}
