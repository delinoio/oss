// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF or native platform acceptance.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = { revision: execFileSync("git", ["rev-parse", "HEAD"], { cwd: app, encoding: "utf8" }).trim(), dirty: Boolean(execFileSync("git", ["status", "--porcelain"], { cwd: app, encoding: "utf8" }).trim()) };
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-pr-sidebar-layout-"));
const screenshots = process.env.DELIDEV_SUBSCRIPTION_RAIL_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/subscription-rail-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const origin = `http://127.0.0.1:${server.address().port}`;

  for (const language of ["en", "ko"]) for(const theme of ["light","dark"]) for(const [width,height] of [[1440,900],[960,640],[480,320]]) {
    await page.setViewportSize({width,height});await page.goto(origin+"/?language="+language+"&theme="+theme);
    const first=page.locator(".subscription-rail-account").first();await first.waitFor();
    const before=await page.locator(".sidebar-rail-button").last().evaluate(node=>node.offsetTop);
    await first.click();const popup=page.locator(".subscription-rail-popover");await popup.waitFor();
    const box=await popup.boundingBox();assert(box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=height+1,JSON.stringify(box));
    await page.keyboard.press("Escape");assert(await first.evaluate(node=>document.activeElement===node));
    const scroll=page.locator(".subscription-rail-scroll");await scroll.evaluate(node=>{node.scrollTop=node.scrollHeight;node.dispatchEvent(new Event("scroll"));});
    if(await page.locator(".subscription-rail-account").count()<70){const more=scroll.locator(".sidebar-continuation button");await more.focus();await more.press("Enter");}
    await page.waitForFunction(()=>document.querySelectorAll(".subscription-rail-account").length===70);
    const after=await page.locator(".sidebar-rail-button").last().evaluate(node=>node.offsetTop);assert.equal(before,after);
    assert(await first.evaluate(node=>node.getBoundingClientRect().width===44&&node.getBoundingClientRect().height===44));
    checks++;console.log(JSON.stringify({operation:"subscription-rail-layout",language,theme,width,height,result:"passed"}));
    if(screenshots){await mkdir(screenshots,{recursive:true});await page.screenshot({path:join(screenshots,language+"-"+theme+"-"+width+".png")});}
  }
  assert.deepEqual(failures,[]);console.log(JSON.stringify({operation:"subscription-rail-layout",source,checks,result:"passed",nativeAcceptance:false}));
} finally {await browser?.close();await new Promise(done=>server?server.close(done):done());await rm(directory,{recursive:true,force:true});}
