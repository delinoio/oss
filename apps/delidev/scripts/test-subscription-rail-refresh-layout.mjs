// SPDX-License-Identifier: Apache-2.0
// Synthetic browser geometry only; no native, account or execution acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const { chromium } = await import(pathToFileURL(resolve(process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE)).href);
const directory = await mkdtemp(join(tmpdir(), "delidev-rail-layout-"));
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/subscription-rail-refresh-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage();
  page.on("pageerror", error => console.error("fixture_page_error", error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width,height] of [[1440,900],[480,420]]) for (const tail of [false,true]) {
    await page.setViewportSize({width,height});await page.goto(`${origin}/?language=${language}&theme=${theme}&tail=${tail}`);
    const accounts=page.locator(".subscription-rail-account:has(img)");await accounts.first().waitFor();
    assert.equal(await page.locator(".subscription-rail-reload").count(),0);
    await page.evaluate(()=>{window.fixtureNodes=[...document.querySelectorAll(".subscription-rail-account")];window.fixtureImages=window.fixtureNodes.map(node=>node.querySelector("img"));});
    const geometry=()=>page.locator(".subscription-rail-group").evaluate(node=>({height:node.getBoundingClientRect().height,scroll:node.querySelector(".subscription-rail-scroll").scrollTop,positions:[...node.querySelectorAll(".subscription-rail-account")].map(button=>button.getBoundingClientRect().top)}));
    for(const trigger of ["focus","timer"]){
      await page.waitForTimeout(50);console.log(JSON.stringify({language,theme,width,height,tail,trigger,reads:await page.locator("html").getAttribute("data-reads")}));const before=await geometry();const reads=Number(await page.locator("html").getAttribute("data-reads"));
      await page.evaluate(trigger=>{window.fixtureHoldRail();if(trigger==="focus")window.dispatchEvent(new Event("focus"));else window.fixtureTimerRail();},trigger);
      try{await page.waitForFunction(()=>document.documentElement.dataset.pending==="true");}catch(error){console.log(await page.locator("html").evaluate(node=>({dataset:{...node.dataset},visible:document.visibilityState,text:document.body.textContent})));throw error;}
      assert.deepEqual(await geometry(),before);
      assert(await page.evaluate(()=>window.fixtureNodes.every((node,index)=>node===document.querySelectorAll(".subscription-rail-account")[index]&&node.querySelector("img")===window.fixtureImages[index])));
      assert.equal(await page.locator(".sidebar-continuation [role=status]").count(),0);
      assert.equal(await page.locator(".sidebar-continuation").count(),tail?1:0);
      if(tail)assert(await page.locator(".sidebar-continuation button").isDisabled());
      await page.evaluate(()=>window.fixtureReleaseRail());await page.waitForFunction(()=>document.documentElement.dataset.pending==="false");
      if(tail)await page.waitForFunction(()=>!document.querySelector(".sidebar-continuation button").disabled);
      assert.deepEqual(await geometry(),before);assert.equal(Number(await page.locator("html").getAttribute("data-reads")),reads+1);checks++;
    }
  }
  console.log(JSON.stringify({operation:"subscription_rail_layout",result:"passed",checks,languages:2,themes:2,effectiveZoom:"200%",nativeAcceptance:"not-performed"}));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
