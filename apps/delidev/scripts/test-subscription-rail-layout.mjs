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

  for (const language of ["en", "ko"]) for(const theme of ["light","dark"]) for(const [width,height] of [[1440,900],[960,640],[480,320]]) for(const windows of ["one","many"]) {
    await page.setViewportSize({width,height});await page.goto(origin+"/?language="+language+"&theme="+theme+"&windows="+windows);
    const first=page.locator(".subscription-rail-account").first();await first.waitFor();
    const before=await page.locator(".sidebar-rail-button").last().evaluate(node=>node.offsetTop);
    await first.click();const popup=page.locator(".subscription-rail-popover");await popup.waitFor();
    const box=await popup.boundingBox();assert(box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=height+1,JSON.stringify(box));
    const presentation=await popup.evaluate(node=>{const style=getComputedStyle(node);return {width:node.getBoundingClientRect().width,padding:style.paddingTop,radius:style.borderRadius,windows:[...node.querySelectorAll('.subscription-quota-id')].map(row=>row.textContent),details:node.querySelectorAll('details').length,percent:node.querySelector('.subscription-quota-value strong').textContent,bar:node.querySelector('.subscription-quota-bar span').style.width,scroll:node.scrollHeight>node.clientHeight};});
    assert.equal(presentation.width,320);assert.equal(presentation.padding,'16px');assert.equal(presentation.radius,'12px');assert.equal(presentation.details,0);assert.equal(presentation.percent,'57%');assert.equal(presentation.bar,'57%');assert.equal(presentation.windows.length,windows==='many'?12:1);assert.equal(presentation.windows[0],'codex:primary');
    if(windows==='many'){assert(presentation.scroll);assert(presentation.windows.at(-1).includes('window:11:'));}
    const actions=popup.locator('.subscription-account-actions button');await actions.last().focus();assert(await actions.last().evaluate(node=>document.activeElement===node));if(windows==='many'){await page.keyboard.press('Shift+Tab');await page.keyboard.press('Shift+Tab');assert(await popup.locator('header button').evaluate(node=>document.activeElement===node));}
    await page.keyboard.press("Escape");assert(await first.evaluate(node=>document.activeElement===node));
    await first.focus();await first.press('Enter');await popup.waitFor();await popup.locator('header button').click();assert(await first.evaluate(node=>document.activeElement===node));
    await first.press('Space');await popup.waitFor();await page.mouse.click(width-2,2);assert.equal(await popup.count(),0);assert(await first.evaluate(node=>document.activeElement===node));
    for(const index of [1,2,3]){const account=page.locator('.subscription-rail-account').nth(index);await account.click();await popup.waitFor();assert((await popup.locator('.subscription-account-alias').textContent()).length>100);const bounds=await popup.boundingBox();assert(bounds.x>=0&&bounds.y>=0&&bounds.x+bounds.width<=width+1&&bounds.y+bounds.height<=height+1);assert.equal(await popup.locator('header img').getAttribute('width'),'24');await popup.locator('.subscription-account-actions button').last().focus();await page.keyboard.press('Escape');assert(await account.evaluate(node=>document.activeElement===node));}
    const scroll=page.locator(".subscription-rail-scroll");await scroll.evaluate(node=>{node.scrollTop=node.scrollHeight;node.dispatchEvent(new Event("scroll"));});
    if(await page.locator(".subscription-rail-account").count()<70){const more=scroll.locator(".sidebar-continuation button");await more.focus();await more.press("Enter");}
    await page.waitForFunction(()=>document.querySelectorAll(".subscription-rail-account").length===70);
    const after=await page.locator(".sidebar-rail-button").last().evaluate(node=>node.offsetTop);assert.equal(before,after);
    assert(await first.evaluate(node=>node.getBoundingClientRect().width===44&&node.getBoundingClientRect().height===44));
    checks++;console.log(JSON.stringify({operation:"subscription-rail-layout",language,theme,width,height,windows,result:"passed"}));
    if(screenshots){await mkdir(screenshots,{recursive:true});await page.screenshot({path:join(screenshots,language+"-"+theme+"-"+width+".png")});}
  }
  // The independent saved-read problem retains its original geometry and presentation.
  await page.setViewportSize({width:1440,height:900});await page.goto(`${origin}/?readProblem=true`);
  const problem=page.getByRole('button',{name:'Explain subscription read problem',exact:true});await problem.click();
  const problemPopup=page.locator('.subscription-rail-popover');await problemPopup.waitFor();
  const original=await problemPopup.evaluate(node=>{const style=getComputedStyle(node);return {width:node.getBoundingClientRect().width,padding:style.paddingTop,radius:style.borderRadius,account:node.classList.contains('subscription-account-popover')};});
  assert.deepEqual(original,{width:280,padding:'14px',radius:'8px',account:false});await page.keyboard.press('Escape');assert(await problem.evaluate(node=>document.activeElement===node));
  await page.goto(`${origin}/?grow=true&windows=one`);const retained=page.locator('.subscription-rail-account').first();await retained.click();const growing=page.locator('.subscription-account-popover');await growing.waitFor();
  await growing.getByRole('button',{name:'Recheck saved quota evidence',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.subscription-account-popover .subscription-quota-window').length===12);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));const grown=await growing.boundingBox();assert(grown.x>=0&&grown.y>=0&&grown.x+grown.width<=1441&&grown.y+grown.height<=901,'Accepted saved replacement remains clamped');assert.equal(await page.evaluate(()=>document.documentElement.dataset.accountReads),'2');await page.keyboard.press('Escape');assert(await retained.evaluate(node=>document.activeElement===node));
  assert.deepEqual(failures,[]);console.log(JSON.stringify({operation:"subscription-rail-layout",source,checks,result:"passed",nativeAcceptance:false}));
} finally {await browser?.close();await new Promise(done=>server?server.close(done):done());await rm(directory,{recursive:true,force:true});}
