// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
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
const directory = await mkdtemp(join(tmpdir(), "delidev-project-session-layout-"));
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/project-session-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const frame = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const root=page.locator('.new-project-session-page');
  const normalize=async()=>{await frame();await page.locator('main').evaluate(node=>{node.scrollTop=0;window.scrollTo(0,0);});await frame();};
  const geometry=()=>root.evaluate(node=>{const top=selector=>node.querySelector(selector).getBoundingClientRect().top;return {heading:top('.new-session-header'),project:top('.new-session-content > .resource-choice'),workspace:top('.workspace-mode'),composer:top('.new-session-composer')};});
  const stable=async (before,composer=true)=>{await normalize();const after=await geometry();for(const key of ['heading','project','workspace',...(composer?['composer']:[])])assert(Math.abs(before[key]-after[key])<=1,JSON.stringify({key,before,after}));return after;};
  const choose=async index=>{const control=root.locator('.resource-choice [role=combobox]').nth(index);await control.focus();await page.keyboard.press('End');await page.keyboard.press('Enter');await page.waitForFunction(node=>Boolean(node.dataset.value),await control.elementHandle());};
  const open=async details=>{const summary=details.locator(':scope > summary');await summary.focus();await summary.press('Enter');await frame();};
  for(const language of ['en','ko'])for(const theme of ['light','dark'])for(const size of [{width:1840,height:1164},{width:960,height:640},{width:480,height:320}])for(const supported of [false,true])for(const repositories of [1,2]){
    await page.setViewportSize(size);await page.goto(`${origin}/?language=${language}&theme=${theme}&supported=${supported}&repositories=${repositories}`);
    await root.locator('.starting-branch-autocomplete').first().waitFor();await choose(1);await choose(2);
    await root.locator('textarea').fill('Retained synthetic project draft');await normalize();
    const before=await geometry(),primary=root.locator('.starting-branches > .starting-branch-autocomplete');
    if(size.width>760){const alignment=await root.evaluate(node=>node.querySelector('.workspace-mode label').getBoundingClientRect().top-node.querySelector('.starting-branch-autocomplete > input').getBoundingClientRect().top);assert(Math.abs(alignment)<=1,`control band ${alignment}`);}
    if(size.height>760)assert(before.heading>80,'Tall initial primary content remains centered');
    const branch=primary.locator('input');await branch.focus();await stable(before);
    if(supported){await branch.fill('feature');await primary.getByRole('option',{name:'feature/topic',exact:true}).click();await stable(before);assert.equal(await branch.inputValue(),'feature/topic');await branch.focus();await stable(before);await primary.getByRole('button',{name:language==='en'?'Refresh':'새로 고침',exact:true}).click();await stable(before);await branch.press('Escape');await stable(before);}
    else {assert.equal(await page.evaluate(()=>document.documentElement.dataset.discoveries),'0');await branch.fill('feature/new');await stable(before);await branch.press('Escape');await stable(before);}
    if(repositories===2){const additional=root.locator('.starting-branches > details').first();await open(additional);const added=await stable(before,false);assert(added.composer>before.composer);const secondary=additional.locator('.starting-branch-autocomplete input');await secondary.focus();await stable(added);await secondary.fill('main');await stable(added);await secondary.press('Escape');await stable(added);await open(additional);await stable(before);}
    const options=root.locator('.new-session-options-toggle');await options.focus();await options.press('Enter');await stable(before);await options.press('Enter');await stable(before);
    if(language==='en'&&theme==='light'&&size.width===1840&&!supported&&repositories===1){
      const textarea=root.locator('textarea'),height=await textarea.evaluate(node=>node.getBoundingClientRect().height);
      await textarea.evaluate((node,height)=>{node.style.height=`${height+32}px`;},height);await normalize();const grown=await textarea.evaluate(node=>node.getBoundingClientRect().height);assert(Math.abs((await geometry()).heading-before.heading+(grown-height)/2)<=1,JSON.stringify({operation:'primary resize',before,after:await geometry(),height,grown}));
      await textarea.evaluate((node,height)=>{node.style.height=`${height}px`;},height);await stable(before);
      await page.getByRole('button',{name:'Fixture Korean',exact:true}).click();await normalize();assert.equal(await textarea.inputValue(),'Retained synthetic project draft');
      await page.getByRole('button',{name:'Fixture English',exact:true}).click();await stable(before);
      await page.setViewportSize({width:960,height:640});await normalize();const short=await geometry();await branch.focus();await stable(short);await branch.press("Escape");await stable(short);
      await page.setViewportSize(size);await stable(before);
    }
    assert.equal(await root.locator('textarea').inputValue(),'Retained synthetic project draft');assert(await root.evaluate(node=>node.scrollWidth<=node.clientWidth+1),'No horizontal clipping');
    await root.locator('.workspace-mode input').nth(1).focus();await page.keyboard.press('Space');await page.waitForFunction(()=>!document.querySelector('.starting-branches'));
    const runner=root.locator('.resource-choice [role=combobox]').nth(2);assert(await runner.isDisabled());assert(Number(await page.evaluate(()=>document.documentElement.dataset.proofReads))>=1);
    await root.locator('.workspace-mode input').first().focus();await page.keyboard.press('Space');await root.locator('.starting-branch-autocomplete').first().waitFor();assert.equal(await runner.getAttribute('data-value'),'');
    assert.equal(await page.locator('[data-fixture-creates]').getAttribute('data-fixture-creates'),'0');cases++;console.log(JSON.stringify({operation:'project-session-layout',language,theme,...size,supported,repositories,result:'passed'}));
  }
  for(const creation of ['pending','uncertain']){
    await page.setViewportSize({width:1840,height:1164});await page.goto(`${origin}/?creation=${creation}`);await root.locator('.starting-branch-autocomplete').first().waitFor();await choose(1);await choose(2);await root.locator('textarea').fill('Original request');
    const primary=root.locator('.starting-branches > .starting-branch-autocomplete'),branch=primary.locator('input');await branch.focus();await primary.getByRole('option',{name:'feature/topic',exact:true}).click();
    await root.locator('button[type=submit]').click();await page.waitForFunction(()=>document.querySelector('[data-fixture-creates]').dataset.fixtureCreates==='1');
    assert(await root.locator('textarea').isDisabled());const before=await geometry();assert(await branch.isDisabled());await stable(before);
    assert.equal(await page.locator('[data-fixture-creates]').getAttribute('data-fixture-creates'),'1','Layout cannot resubmit the original request');
    const original=JSON.parse(await page.locator('[data-fixture-creates]').textContent());assert.equal(original.starting[0].reference.name,'feature/topic');assert.equal(original.starting[0].reference.remote,'upstream');assert.equal(original.prompt,'Original request');
    if(creation==='pending')await page.getByRole('button',{name:'Fixture release',exact:true}).click();cases++;
  }
  assert.deepEqual(errors,[]);console.log(JSON.stringify({operation:'project-session-layout',source,cases,result:'passed',nativeAcceptance:'not-performed'}));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
