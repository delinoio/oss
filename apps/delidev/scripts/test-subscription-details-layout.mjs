// SPDX-License-Identifier: Apache-2.0
// Host-supplied headless Chromium validates the real App/Settings api-details flow
// against synthetic Connect handlers. No native or real account acceptance.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const playwright = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(playwright ? pathToFileURL(resolve(playwright)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-subscription-details-layout-"));
const screenshots = process.env.DELIDEV_SUBSCRIPTION_DETAILS_SCREENSHOT_DIR;
let browser, server;
let checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`);
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
      response.setHeader("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'none'; base-uri 'none'");
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage();
  page.on("pageerror", error => failures.push(error.message));
  await page.addInitScript(() => { document.addEventListener("securitypolicyviolation", event => { if (event.violatedDirective === "style-src-elem" || event.violatedDirective === "style-src-attr") console.error("api_details_style_csp_failure"); }); });
  page.on("console", message => { if (message.text() === "api_details_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width,height] of [[1280,800],[480,800],[640,400]]) {
    await page.setViewportSize({width,height}); await page.goto(`${origin}/?theme=${theme}&language=${language}&subscriptionDetails=true`);
    await page.getByRole("button", {name: language === "en" ? "Settings" : "설정", exact:true}).click();
    const row = page.locator(".subscription-row").first(); await row.waitFor();
    const before = await page.evaluate(() => ({reads:document.documentElement.dataset.subscriptionDetailReads ?? "0",lists:document.documentElement.dataset.subscriptionDetailLists}));
    const name = language === "en" ? "Account details" : "계정 상세 정보";
    const opener = row.locator(".subscription-more > button");
    await opener.click(); await row.getByRole("button", {name,exact:true}).click();
    const dialog=page.getByRole("dialog",{name,exact:true}); await dialog.waitFor();
    await page.waitForFunction(()=>document.activeElement?.tagName==="H2");
    assert.equal(await page.locator("dialog[open]:not([role=region])").count(),1); assert.equal(await row.locator(".subscription-details").count(),0);
    assert.equal(await row.locator("progress").count(),2); assert.equal(await dialog.locator("progress").count(),1);
    const box=await dialog.boundingBox();assert(box.width<=Math.min(768,width-32)+1&&box.x>=0&&box.x+box.width<=width+1,JSON.stringify(box));
    assert(await dialog.locator(".settings-task-body").evaluate(node=>node.scrollWidth<=node.clientWidth));
    assert(await page.locator(".settings-task-background").evaluate(node=>node.hasAttribute("inert")));
    for(const key of ["Tab","Tab","Tab","Shift+Tab","Shift+Tab"]) {await page.keyboard.press(key);assert(await dialog.evaluate(node=>node.contains(document.activeElement)));}
    await dialog.locator(".settings-task-close").click();await dialog.waitFor({state:"detached"});assert(await opener.evaluate(node=>node===document.activeElement));
    const extra=row.locator(".subscription-extra-windows");await extra.click();await dialog.waitFor();await page.keyboard.press("Escape");await dialog.waitFor({state:"detached"});assert(await extra.evaluate(node=>node===document.activeElement));
    const after=await page.evaluate(()=>({reads:document.documentElement.dataset.subscriptionDetailReads ?? "0",lists:document.documentElement.dataset.subscriptionDetailLists}));assert.deepEqual(after,before,"Details introduced resource reads or inventory refresh");
    if(screenshots){await mkdir(resolve(screenshots),{recursive:true});await extra.click();await dialog.waitFor();await page.screenshot({path:join(resolve(screenshots),`subscription-details-${language}-${theme}-${width}.png`)});await page.keyboard.press("Escape");}
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ fixture: "subscription-details", checks, effectiveZoomViewport: "640x400 represents 1280x800 at effective 200%", nativeAcceptance: false }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
