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


async function guidance(page, plus, language) {
 await page.mouse.move(0,0); await page.locator('textarea').focus(); await page.waitForTimeout(150);
 assert.equal(await page.getByRole('tooltip').count(),0);
 const before=await page.evaluate(()=>({reads:window.imageFixture.reads.length,events:window.imageFixture.events.length,draft:document.querySelector('textarea').value}));
 await plus.scrollIntoViewIfNeeded();
 const box=await plus.boundingBox();
 await plus.hover(); const tip=page.getByRole('tooltip'); await tip.waitFor();
 const text=language==='ko' ? ['이미지 첨부','정지 PNG, JPEG 또는 WebP만 지원합니다.','메시지당 최대 8개, 이미지당 10 MiB, 총 40 MiB.','이미지당 최대 4천만 픽셀.','이미지 입력을 지원하는 모델을 사용하는 Codex Agent Worker와 이미지 입력을 지원하는 Runner Device가 필요합니다.','DeliDev에서는 Claude Code, OpenCode, Grok Build의 이미지 입력을 지원하지 않습니다.'] : ['Image attachments','Still PNG, JPEG or WebP only.','Up to 8 images per message, 10 MiB each, 40 MiB total.','Up to 40 million pixels per image.','Requires a Codex Agent Worker with an image-capable model and a Runner Device that supports image inputs.','Claude Code, OpenCode and Grok Build image inputs are not supported in DeliDev.'];
 assert.equal(await tip.textContent(),text.join(''));assert.equal(await plus.getAttribute('aria-describedby'),await tip.getAttribute('id'));
 await page.waitForTimeout(100);
 assert(await tip.evaluate(node=>{const r=node.getBoundingClientRect();return r.left>=7&&r.top>=7&&r.right<=innerWidth-7&&r.bottom<=innerHeight-7&&node.scrollWidth<=node.clientWidth;}),'Tooltip stays in viewport with wrapped readable text');
 assert.deepEqual(await plus.boundingBox(),box,'Guidance never shifts toolbar');
 const tipBox=await tip.boundingBox();await page.mouse.move(tipBox.x+tipBox.width/2,tipBox.y+Math.min(10,tipBox.height/2)); await page.waitForTimeout(150);assert.equal(await tip.count(),1);
 await plus.focus();await page.mouse.move(0,0);await page.waitForTimeout(150);assert.equal(await tip.count(),1);
 await page.keyboard.press('Escape');assert.equal(await tip.count(),0);assert(await plus.evaluate(node=>node===document.activeElement));assert.equal(await plus.getAttribute('aria-describedby'),null);
 await page.waitForTimeout(150);assert.equal(await tip.count(),0);
 await page.locator('textarea').focus();await plus.focus();await tip.waitFor();
 await page.locator('textarea').focus();await page.waitForTimeout(150);assert.equal(await tip.count(),0);
 assert.deepEqual(await page.evaluate(()=>({reads:window.imageFixture.reads.length,events:window.imageFixture.events.length,draft:document.querySelector('textarea').value})),before,'Guidance adds no reads, picker, writes or draft mutations');
 // Exercise collision below a top-edge trigger without changing layout authority.
 await page.locator('main').evaluate(node=>node.style.transform='translateY(-'+document.querySelector('.new-session-attach').getBoundingClientRect().top+'px)');
 await plus.focus();await tip.waitFor();await page.waitForTimeout(100);
 assert(await tip.evaluate(node=>node.getBoundingClientRect().top>=document.querySelector('.new-session-attach').getBoundingClientRect().bottom),'Top-edge tooltip flips below');
 await page.keyboard.press('Escape');await page.locator('main').evaluate(node=>node.style.transform='');await page.locator('textarea').focus();
 await plus.focus();await tip.waitFor();await page.getByRole('button',{name:'Fixture navigate',exact:true}).click();assert.equal(await tip.count(),0,'Inactive mounted composer disposes its portal');await page.getByRole('button',{name:'Fixture navigate',exact:true}).click();await page.locator('textarea').focus();
}

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-image-layout-"));
const screenshots = process.env.DELIDEV_LAYOUT_SCREENSHOTS;
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
 for (const language of ["en","ko"]) for (const theme of ["light","dark"]) for (const [width,height,zoom] of [[1440,1000,1],[960,640,1],[320,640,1],[640,640,2]]) {
  const compact = zoom === 2;
  await page.setViewportSize({width,height});
  await page.goto(`http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&zoom=${compact ? 2 : 1}`);
  const input = page.locator('input[type=file]');
  const plus = page.locator('.new-session-attach');
  await plus.waitFor(); await page.waitForFunction(() => !document.querySelector('.new-session-attach').disabled);
  assert.equal(await plus.textContent(), '+');
  assert.equal(await plus.getAttribute('aria-label'), language === 'ko' ? '이미지 첨부' : 'Attach images');
  assert.equal(await plus.getAttribute('title'), null);
  assert.deepEqual(await plus.evaluate(node => [node.clientWidth + 2, node.clientHeight + 2]), [40, 40]);
  if (width >= 960 && !compact) assert(await plus.evaluate(node => Math.abs(node.getBoundingClientRect().top - node.parentElement.nextElementSibling.querySelector('[role=combobox]').getBoundingClientRect().top) < 1), 'Plus aligns with the actual Agent Worker input');
  assert(await plus.evaluate(node => node.parentElement.nextElementSibling.classList.contains('resource-choice')));
  assert.equal(await page.locator('.new-session-composer small, .composer-attachment-help, #project-prompt-history-guidance').count(), 0);
  assert.equal(await page.locator('textarea[aria-describedby="project-prompt-history-guidance"]').count(), 0);
  await guidance(page, plus, language);
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
  if (screenshots) await page.screenshot({path:join(screenshots,`${language}-${theme}-${width}-${zoom}-images.png`)});
  await page.locator("button.new-session-submit").click();
  await page.waitForFunction(()=>window.imageFixture.events.some(event=>event.kind==="create"));
  const receipt=await page.evaluate(()=>window.imageFixture.events.find(event=>event.kind==="create"));
  assert.equal(receipt.images.length,4); assert.equal(receipt.prompt,""); assert.equal(receipt.documentHasImages,false);
  assert.deepEqual(receipt.images.map(image=>image.media),[1,3,2,2]);
  assert.equal(await page.locator(".image-preview-list img").count(),0,"Matching typed receipt clears accepted draft");
  await page.getByRole("button",{name:"Fixture switch composer",exact:true}).click();
  assert.equal(await page.locator('.new-session-attach').count(), 1, "General Chat uses the same plus toolbar");
  await guidance(page, plus, language);
  assert.equal(await page.locator('.new-session-composer small, .composer-attachment-help').count(), 0);
  await page.locator('input[type=file]').setInputFiles({name:"fixture.png",mimeType:files[0].mime,buffer:Buffer.from(files[0].bytes)});
  await page.waitForFunction(()=>document.querySelectorAll(".image-preview-list img").length===1);
  await page.locator("button.new-session-submit").click();
  await page.waitForFunction(()=>window.imageFixture.events.filter(event=>event.kind==="create").length===2);
  assert.equal(await page.locator(".image-preview-list img").count(),0,"General Chat accepts its independent image-only input");
  assert(await page.locator("main").evaluate(node=>node.scrollWidth<=node.clientWidth),"Image controls fit effective 200% zoom");
  if (screenshots) await page.screenshot({path:join(screenshots,`${language}-${theme}-${width}-${zoom}.png`)});
 }
 assert.deepEqual(errors,[]);
 process.stdout.write(JSON.stringify({operation:"image-input-layout",cases:16,creationGuidanceSurfaces:32,screenshots: screenshots ?? "not-performed",nativeAcceptance:"not-performed",accountAcceptance:"not-performed",actualChromeZoom:"effective-CSS-200-percent"})+"\n");
} finally {
 await browser?.close();
 if (server) await new Promise(done => server.close(done));
 await rm(directory,{recursive:true,force:true});
}
