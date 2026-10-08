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
const directory = await mkdtemp(join(tmpdir(), "delidev-api-details-layout-"));
const screenshots = process.env.DELIDEV_API_DETAILS_SCREENSHOT_DIR;
let browser, server;
let checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/api-details-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en","ko"]) for (const theme of ["light","dark"]) for (const [width,height,zoom] of [[1440,900,1],[1280,820,1],[960,640,1],[640,480,1],[320,240,2]]) {
    await page.setViewportSize({width,height}); await page.goto(`${origin}/?theme=${theme}&language=${language}&zoom=${zoom}`);
    const first=page.locator(".api-entry-row").first(), opener=first.locator(".api-entry-more > button"), label=language==="en"?"Details":"상세";
    await page.waitForFunction(()=>window.__apiDetailsFixture.reads.length===2);
    assert.equal(await first.locator(".api-entry-footer button").count(),1,"Only View usage remains in the footer");
    const open=async()=>{await opener.click(); await first.getByRole("button",{name:label,exact:true}).click();};
    await open(); const dialog=page.getByRole("dialog",{name:label,exact:true}),heading=dialog.getByRole("heading",{name:label,exact:true}); await heading.waitFor();
    await page.waitForFunction(()=>document.activeElement?.tagName==="H2"); assert.equal(await page.locator("dialog[open]").count(),1);
    const ids=await page.evaluate(()=>window.__apiDetailsFixture.ids); assert(await dialog.getByText(ids[0],{exact:true}).isVisible()); assert.equal(await dialog.getByText(ids[1],{exact:true}).count(),0);
    assert.equal(await first.locator("button").first().isEnabled(),false,"Mounted background is disabled");
    assert(await page.locator(".settings-task-background").evaluate(node=>node.hasAttribute("inert")),"Background is inert");
    const close=dialog.locator(".settings-task-close"); await close.focus(); for(const key of ["Tab","Tab","Tab","Shift+Tab","Shift+Tab","Shift+Tab"]) { await page.keyboard.press(key); assert(await dialog.evaluate(node=>node.contains(document.activeElement)),"Tab and Shift+Tab stay within the modal"); }
    const bounds=await dialog.boundingBox(); assert(bounds.width<=Math.min(768,width-32)+1); assert(bounds.x>=0&&bounds.x+bounds.width<=width+1);
    const body=dialog.locator(".settings-task-body"); assert(await body.evaluate(node=>node.scrollHeight>node.clientHeight),"Only retained body scrolls"); assert(await body.evaluate(node=>node.scrollWidth<=node.clientWidth),"Full identifiers and evidence wrap");
    await body.evaluate(node=>node.scrollTop=node.scrollHeight); assert(await close.isVisible());
    await close.click(); await dialog.waitFor({state:"detached"}); assert(await opener.evaluate(node=>node===document.activeElement),"X returns to permanent More actions opener");
    await open(); await page.keyboard.press("Escape"); await dialog.waitFor({state:"detached"}); assert(await opener.evaluate(node=>node===document.activeElement),"Escape returns to permanent opener");
    assert.equal(await page.evaluate(()=>window.__apiDetailsFixture.reads.length),2,"Reopening has no new reads");
    const unknown=page.locator(".api-entry-row").nth(2); await unknown.locator(".api-entry-more > button").click(); const actions=unknown.locator(".api-entry-more-panel button"); assert.equal(await actions.nth(0).isEnabled(),true); assert.equal(await actions.nth(1).isEnabled(),false); assert.equal(await actions.nth(2).isEnabled(),false); await actions.nth(0).click(); await dialog.waitFor();
    assert(await dialog.getByText(ids[2],{exact:true}).isVisible()); await page.keyboard.press("Escape"); await dialog.waitFor({state:"detached"});
    assert.deepEqual(await page.evaluate(()=>window.__apiDetailsFixture.effects),[]); assert.equal(await page.evaluate(()=>window.__apiDetailsFixture.reads.length),2);
    checks++;
    if (screenshots) {await mkdir(resolve(screenshots),{recursive:true}); await open(); await page.screenshot({path:join(resolve(screenshots),`api-details-${language}-${theme}-${width}-${zoom}.png`)}); await page.keyboard.press("Escape");}
  }

  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ fixture: "api-details", checks, effectiveZoomViewport: "320x240 represents 640x480 at effective 200%", nativeAcceptance: false }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
