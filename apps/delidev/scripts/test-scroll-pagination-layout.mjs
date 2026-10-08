// SPDX-License-Identifier: Apache-2.0
// Synthetic browser layout evidence; no native window or provider account is used.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-scroll-layout-"));
const screenshots = await mkdtemp(join(tmpdir(), "delidev-scroll-preview-"));
let browser, server;
try {
 const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/scroll-pagination-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
 await build.build();
 server = createServer(async (request,response) => {
  try {
   const pathname = new URL(request.url,"http://127.0.0.1").pathname;
   const file = resolve(directory,`.${pathname === "/" ? "/index.html" : pathname}`);
   if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
   response.setHeader("Content-Type", { ".html":"text/html", ".js":"application/javascript", ".css":"text/css", ".svg":"image/svg+xml" }[extname(file)] ?? "application/octet-stream");
   response.end(await readFile(file));
  } catch { response.writeHead(404); response.end(); }
 });
 await new Promise(done => server.listen(0,"127.0.0.1",done));
 browser = await chromium.launch({ headless:true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel:process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
 const page = await browser.newPage();
 const errors = []; page.on("pageerror",error => errors.push(error.message));
 for (const language of ["en","ko"]) for (const theme of ["light","dark"]) for (const compact of [false,true]) {
  const width = compact ? 640 : 1440, height = compact ? 640 : 1000;
  await page.setViewportSize({width,height});
  await page.goto(`http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&zoom=${compact ? 2 : 1}`);
  const root = page.locator("main > div").first(), count = page.locator("[data-fixture-count]");
  await page.getByRole("button",{name:"Fixture Load",exact:true}).waitFor();
  assert.equal(await count.textContent(),"0:0:0","Scrolling cannot initiate explicit Load");
  await page.getByRole("button",{name:"Fixture Load",exact:true}).click();
  await page.waitForFunction(() => Number(document.querySelector("[data-fixture-count]").textContent.split(":")[0]) >= 2);
  const composer = page.getByRole("textbox",{name:"Fixture composer"}); await composer.focus();
  for (let index=0;index<5;index++) {
   await root.evaluate(node=> { node.scrollTop=node.scrollHeight; });
   await page.waitForTimeout(80);
  }
  assert.equal((await count.textContent()).split(":")[0],"5");
  const continuation = root.locator("[data-continuation]");
  assert.equal(await continuation.count(),1,"Exhaustion retains the observer anchor");
  assert.equal(await continuation.textContent(),"","Exhaustion adds no completion notice or announcement");
  assert.equal(await continuation.locator("button, [role=status]").count(),0);
  assert(Number((await count.textContent()).split(":")[1]) <= 3,"Payload pages remain bounded");
  assert(await composer.evaluate(node=>node===document.activeElement),"Append preserves composer focus");
  await root.evaluate(node=> { node.scrollTop=0; });
  await page.getByRole("button",{name:"Fixture action row-0",exact:true}).waitFor();
  assert(Number((await count.textContent()).split(":")[1]) <= 3,"Restoration remains bounded");
  const picker=page.getByRole("combobox",{name:"Fixture picker",exact:true});
  assert.equal(await picker.getAttribute("data-value"),"off-page");
  await picker.focus(); await page.keyboard.press("End"); await page.keyboard.press("Enter");
  assert.equal(await picker.getAttribute("data-value"),"row-4");
  await picker.click(); await page.keyboard.press("Escape");
  assert(await picker.evaluate(node=>node===document.activeElement),"Picker restores trigger focus");
  assert(await page.locator("main").evaluate(node=>node.scrollWidth<=node.clientWidth),"Effective zoom cannot overflow horizontally");
  await page.screenshot({path:join(screenshots,`${language}-${theme}-${compact ? "compact-200" : "wide"}.png`)});
 }

 const stable = async () => page.evaluate(() => Object.fromEntries(["picker","refresh","helper","following"].map(key=>{const r=document.querySelector(`[data-measure-${key}]`).getBoundingClientRect();return [key,{top:r.top,left:r.left,height:r.height}];})));
 const assertStable = (before,after) => {for(const key of Object.keys(before)) for(const axis of ["top","left","height"]) assert(Math.abs(before[key][axis]-after[key][axis])<=1,`${key}.${axis} moved on picker disclosure`);};
 const containsPopup = async () => {const geometry=await page.locator(".scroll-picker-popup").evaluate(popup=>{const r=popup.getBoundingClientRect(),owner=popup.closest(".settings-task-body,.sidebar-pane,.sidebar-pane-dialog,.sidebar,[data-picker-boundary]")?.getBoundingClientRect()??{left:0,top:0,right:innerWidth,bottom:innerHeight};return {popup:{left:r.left,right:r.right,top:r.top,bottom:r.bottom},owner:{left:owner.left,right:owner.right,top:owner.top,bottom:owner.bottom},width:innerWidth,height:innerHeight,scale:r.width/popup.offsetWidth};});const {popup:r,owner,width,height}=geometry;assert(r.left>=Math.max(0,owner.left)-1 && r.right<=Math.min(width,owner.right)+1 && r.top>=Math.max(0,owner.top)-1 && r.bottom<=Math.min(height,owner.bottom)+1,`Popover must stay in original bounds: ${JSON.stringify(geometry)}`);};
 let overlayCases=0;
 for(const language of ["en","ko"]) for(const theme of ["light","dark"]) for(const surface of ["ordinary","dialog","sidebar"]) for(const compact of [false,true]) {
  await page.setViewportSize({width:compact?640:1440,height:compact?640:900});
  await page.goto(`http://127.0.0.1:${server.address().port}/?overlay=1&surface=${surface}&language=${language}&theme=${theme}&zoom=${compact?2:1}`);
  process.stdout.write(JSON.stringify({case:surface,language,theme,compact})+"\n");
  const picker=page.getByRole("combobox");await picker.waitFor();await picker.scrollIntoViewIfNeeded();
  const before=await stable();await picker.click();await page.getByRole("listbox").waitFor();await page.waitForTimeout(50);assertStable(before,await stable());await containsPopup();
  assert(await page.getByRole("listbox").evaluate(node=>node.matches(":popover-open")),"Native top-layer popup required");
  const popupId=await page.getByRole("listbox").getAttribute("id");await picker.focus();await page.keyboard.press("ArrowDown");await page.keyboard.press("Escape");assertStable(before,await stable());assert(await picker.evaluate(node=>node===document.activeElement));
  assert.equal(await picker.getAttribute("data-value"),"off-page");
  if(surface==="dialog") assert(await page.locator("[data-overlay-parent]").evaluate(node=>node.open),"Picker Escape preserves parent task");
  await picker.click();await page.setViewportSize({width:compact?620:1200,height:compact?620:760});await page.waitForTimeout(50);assert.equal(await page.getByRole("listbox").getAttribute("id"),popupId);await containsPopup();
  if(surface!=="ordinary") {await page.locator("[data-overlay-owner]").evaluate(node=>node.scrollTop+=20);await page.waitForTimeout(50);assert.equal(await page.getByRole("listbox").getAttribute("id"),popupId);await containsPopup();}
  assert.equal(await picker.getAttribute("data-value"),"off-page");await page.screenshot({path:join(screenshots,`overlay-${surface}-${language}-${theme}-${compact?"effective-200":"wide"}.png`)});overlayCases++;
 }
 await page.setViewportSize({width:960,height:640});await page.goto(`http://127.0.0.1:${server.address().port}/?overlay=1&surface=ordinary&many=1`);
 await page.getByRole("button",{name:"Fixture picker Load"}).click();await page.waitForFunction(()=>Number(document.querySelector("[data-picker-count]").textContent)===20);
 const manyPicker=page.getByRole("combobox");const initial=await stable();await manyPicker.click();await page.getByRole("option").first().waitFor();assertStable(initial,await stable());
 const popup=page.getByRole("listbox");await popup.evaluate(node=>node.scrollTop=node.scrollHeight);await page.waitForTimeout(150);await page.waitForFunction(()=>document.querySelector("[data-overlay-state]").textContent.includes("page-two"));
 const retry=popup.getByRole("button",{name:"Retry",exact:true});await retry.waitFor();assert.equal((await page.locator("[data-overlay-state]").textContent()).split("page-two").length-1,1,"Failed page is not retried automatically");await retry.click();await page.waitForFunction(()=>document.querySelectorAll("[role=option]").length===40);assertStable(initial,await stable());await containsPopup();
 await manyPicker.focus();await page.keyboard.press("End");await page.keyboard.press("Enter");assert.equal(await manyPicker.getAttribute("data-value"),"option-39");assert((await page.locator("[data-overlay-state]").textContent()).startsWith("option-39:1:"),"Exact once-only callback");overlayCases++;
 assert.deepEqual(errors,[]);
 process.stdout.write(JSON.stringify({operation:"scroll-pagination-layout",cases:8,overlayCases,screenshots,nativeAcceptance:"not-performed",accountAcceptance:"not-performed",actualChromeZoom:"effective-CSS-200-percent"})+"\n");
} finally {
 await browser?.close();
 if (server) await new Promise(done => server.close(done));
 await rm(directory,{recursive:true,force:true});
}
