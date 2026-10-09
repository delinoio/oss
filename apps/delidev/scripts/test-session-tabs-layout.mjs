// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-browser-layout-"));
const screenshots = process.env.DELIDEV_BROWSER_SCREENSHOT_DIR;
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/session-browser-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const sizes = [{width:1440,height:900},{width:960,height:640},{width:560,height:480},{width:320,height:480},{width:1120,height:960,zoom:2}];
  for (const language of ["en","ko"]) for (const theme of ["light","dark","system"]) for (const size of sizes) {
    await page.setViewportSize({width:size.width,height:size.height});
    await page.emulateMedia({colorScheme:"dark"});
    await page.goto(`${origin}/?language=${language}&theme=${theme}&zoom=${size.zoom??1}`);
    await page.getByRole("heading",{name:"Synthetic Browser session"}).waitFor();
    const label=language==="en"?{conversation:"Conversation",browser:"Browser",open:"Open account browser",files:"Files"}:{conversation:"대화",browser:"브라우저",open:"계정 브라우저 열기",files:"파일"};
    const composer=page.locator(".composer textarea");
    await composer.evaluate(node=>{window.originalComposer=node;});
    await page.getByRole("button",{name:label.browser,exact:true}).click();
    assert.equal(await page.getByRole("tabpanel").count(),1);
    assert.equal(await page.locator(".composer:visible").count(),0);
    await page.getByRole("textbox",{name:language==="en"?"Address":"주소"}).fill("https://example.com/");
    await page.getByRole("button",{name:label.open,exact:true}).click();
    await page.getByRole("tab",{name:"https://example.com/",exact:true}).waitFor();
    await page.locator(".browser-viewport:visible").waitFor();
    const matrix=await page.evaluate(()=>{
      const box=node=>{const r=node.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width,height:r.height};};
      const pane=document.querySelector('[role="tabpanel"]'),tabs=document.querySelector('[role="tablist"]'),info=document.querySelector('.session-information'),view=document.querySelector('.browser-viewport');
      return {pane:box(pane),tabs:box(tabs),info:box(info),view:box(view),page:document.documentElement.scrollWidth,viewport:innerWidth,splitters:document.querySelectorAll('.browser-splitter,.terminal-dock-separator').length,selected:tabs.querySelectorAll('[aria-selected="true"]').length,scale:parseFloat(getComputedStyle(document.body).zoom)||1};
    });
    assert.equal(matrix.selected,1);assert.equal(matrix.splitters,0);
    assert(matrix.tabs.bottom<=matrix.pane.top+1,JSON.stringify(matrix));
    assert(matrix.pane.width>0&&matrix.view.height>0,JSON.stringify(matrix));
    assert(matrix.page<=matrix.viewport+1,JSON.stringify(matrix));
    const intersects=(a,b)=>a.left<b.right-1&&a.right>b.left+1&&a.top<b.bottom-1&&a.bottom>b.top+1;
    assert(!intersects(matrix.pane,matrix.info),JSON.stringify(matrix));
    assert(Math.abs(matrix.view.width-matrix.pane.width)<=20*matrix.scale,JSON.stringify(matrix));
    await page.getByRole("tab",{name:label.conversation,exact:true}).click();
    await page.waitForFunction(()=>browserFixture.native.some(call=>call.action==="hide"));
    assert.equal(await composer.inputValue(),"Retained synthetic Browser draft");
    assert(await composer.evaluate(node=>node===window.originalComposer));
    assert.equal(await page.locator('.browser-viewport:visible').count(),0);
    const before=await page.evaluate(()=>browserFixture.registrations.length);
    await page.getByRole("tab",{name:"https://example.com/",exact:true}).click();
    await page.locator('.browser-viewport:visible').waitFor();
    assert.equal(await page.evaluate(()=>browserFixture.registrations.length),before);
    await page.getByRole("tab",{name:label.conversation,exact:true}).focus();
    await page.keyboard.press("ArrowRight");
    assert.equal(await page.getByRole("tab",{name:label.browser,exact:true}).getAttribute("aria-selected"),"true");
    assert.equal(await page.locator('.browser-viewport:visible').count(),0,"Browser picker does not show an unselected page");
    process.stdout.write(JSON.stringify({operation:"session-tabs-case",language,theme,...size,geometry:matrix})+"\n");cases++;
  }
  assert.deepEqual(errors,[]);
  process.stdout.write(JSON.stringify({operation:"session-tabs-layout",source,cases,result:"passed"})+"\n");
} finally { await browser?.close();await new Promise(done=>server?server.close(done):done());await rm(directory,{recursive:true,force:true}); }
