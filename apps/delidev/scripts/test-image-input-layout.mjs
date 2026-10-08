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
const directory = await mkdtemp(join(tmpdir(), "delidev-image-layout-"));
const screenshots = await mkdtemp(join(tmpdir(), "delidev-image-preview-"));
let browser, server;
try {
 const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/image-input-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const input = page.locator('input[type=file]');
  const plus = page.locator('.new-session-attach');
  await plus.waitFor(); await page.waitForFunction(() => !document.querySelector('.new-session-attach').disabled);
  assert.equal(await plus.textContent(), '+');
  assert.equal(await plus.getAttribute('aria-label'), language === 'ko' ? '이미지 첨부' : 'Attach images');
  assert.equal(await plus.getAttribute('title'), language === 'ko' ? '이미지 첨부' : 'Attach images');
  assert.deepEqual(await plus.evaluate(node => [node.clientWidth + 2, node.clientHeight + 2]), [40, 40]);
  if (!compact) assert(await plus.evaluate(node => Math.abs(node.getBoundingClientRect().top - node.nextElementSibling.querySelector('[role=combobox]').getBoundingClientRect().top) < 1), 'Plus aligns with the actual Agent Worker input');
  assert(await plus.evaluate(node => node.nextElementSibling.classList.contains('resource-choice')));
  assert.equal(await page.locator('.new-session-composer small, .composer-attachment-help, #project-prompt-history-guidance').count(), 0);
  assert.equal(await page.locator('textarea[aria-describedby="project-prompt-history-guidance"]').count(), 0);
  for (const key of [null, 'Enter', 'Space']) {
   const chooser = page.waitForEvent('filechooser');
   if (key) { await plus.focus(); await page.keyboard.press(key); } else await plus.click();
   await chooser;
  }
  assert.equal(await page.evaluate(() => window.imageFixture.events.filter(event => event.kind === 'create').length), 0, 'Plus never submits');
  await input.waitFor({state:"attached"});
  const files = await page.evaluate(async () => {
   const canvas=document.createElement("canvas"); canvas.width=3; canvas.height=2; canvas.getContext("2d").fillRect(0,0,3,2);
   return Promise.all(["image/png","image/jpeg","image/webp"].map(async mime => { const blob=await new Promise(done=>canvas.toBlob(done,mime)); return {mime,bytes:Array.from(new Uint8Array(await blob.arrayBuffer()))}; }));
  });
  await input.setInputFiles(files.map((file,index)=>({name:`fixture-${index}`,mimeType:file.mime,buffer:Buffer.from(file.bytes)})));
  await page.waitForFunction(()=>document.querySelectorAll(".image-preview-list img").length===3);
  assert(await page.locator(".image-preview-list img").evaluateAll(images=>images.every(image=>image.complete&&image.naturalWidth===3)),"Actual browser decoding previews each original format");
  await page.evaluate(() => { window.imageFixture.input = document.querySelector('textarea'); window.imageFixture.before = window.imageFixture.events.length; });
  await page.evaluate(({language,theme}) => window.imageFixture.appearance(language === 'en' ? 'ko' : 'en', theme === 'light' ? 'dark' : 'light'), {language,theme});
  assert(await page.evaluate(() => window.imageFixture.input === document.querySelector('textarea')));
  assert.equal(await page.locator('.image-preview-list img').count(), 3);
  assert.equal(await page.evaluate(() => window.imageFixture.events.length), await page.evaluate(() => window.imageFixture.before));
  await page.evaluate(({language,theme}) => window.imageFixture.appearance(language,theme), {language,theme});
  await page.getByRole("button",{name:"Fixture navigate",exact:true}).click();
  await page.getByRole("button",{name:"Fixture navigate",exact:true}).click();
  assert.equal(await page.locator(".image-preview-list img").count(),3,"Navigation preserves images");
  await page.getByRole("button",{name:"Fixture switch composer",exact:true}).click();
  assert.equal(await page.locator(".image-preview-list img").count(),0,"General Chat owns an independent draft");
  await page.getByRole("button",{name:"Fixture switch composer",exact:true}).click();
  assert.equal(await page.locator(".image-preview-list img").count(),3);
  await page.locator(".image-preview-list button").nth(1).click();
  assert.equal(await page.locator(".image-preview-list img").count(),2);
  for(const method of ["paste","drop"]) { await page.locator("form").evaluate((form,{file,method})=>{
   const transfer=new DataTransfer(); transfer.items.add(new File([new Uint8Array(file.bytes)],"fixture",{type:file.mime}));
   form.dispatchEvent(method==="paste"?new ClipboardEvent("paste",{clipboardData:transfer,bubbles:true,cancelable:true}):new DragEvent("drop",{dataTransfer:transfer,bubbles:true,cancelable:true}));
  },{file:files[1],method});
  await page.waitForFunction(count=>document.querySelectorAll(".image-preview-list img").length===count,method==="paste"?3:4);
  }
  await page.waitForFunction(()=>document.querySelectorAll(".image-preview-list img").length===4);
  await page.waitForFunction(()=>Array.from(document.querySelectorAll(".image-preview-list img")).every(image=>image.complete&&image.naturalWidth===3));
  await page.screenshot({path:join(screenshots,`${language}-${theme}-${compact ? "compact-200" : "wide"}-images.png`)});
  await page.locator("button.new-session-submit").click();
  await page.waitForFunction(()=>window.imageFixture.events.some(event=>event.kind==="create"));
  const receipt=await page.evaluate(()=>window.imageFixture.events.find(event=>event.kind==="create"));
  assert.equal(receipt.images.length,4); assert.equal(receipt.prompt,""); assert.equal(receipt.documentHasImages,false);
  assert.deepEqual(receipt.images.map(image=>image.media),[1,3,2,2]);
  assert.equal(await page.locator(".image-preview-list img").count(),0,"Matching typed receipt clears accepted draft");
  await page.getByRole("button",{name:"Fixture switch composer",exact:true}).click();
  assert.equal(await page.locator('.new-session-attach').count(), 1, "General Chat uses the same plus toolbar");
  assert.equal(await page.locator('.new-session-composer small, .composer-attachment-help').count(), 0);
  await page.locator('input[type=file]').setInputFiles({name:"fixture.png",mimeType:files[0].mime,buffer:Buffer.from(files[0].bytes)});
  await page.waitForFunction(()=>document.querySelectorAll(".image-preview-list img").length===1);
  await page.locator("button.new-session-submit").click();
  await page.waitForFunction(()=>window.imageFixture.events.filter(event=>event.kind==="create").length===2);
  assert.equal(await page.locator(".image-preview-list img").count(),0,"General Chat accepts its independent image-only input");
  assert(await page.locator("main").evaluate(node=>node.scrollWidth<=node.clientWidth),"Image controls fit effective 200% zoom");
  await page.screenshot({path:join(screenshots,`${language}-${theme}-${compact ? "compact-200" : "wide"}.png`)});
 }
 assert.deepEqual(errors,[]);
 process.stdout.write(JSON.stringify({operation:"image-input-layout",cases:8,screenshots,nativeAcceptance:"not-performed",accountAcceptance:"not-performed",actualChromeZoom:"effective-CSS-200-percent"})+"\n");
} finally {
 await browser?.close();
 if (server) await new Promise(done => server.close(done));
 await rm(directory,{recursive:true,force:true});
}
