// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-command-menu-"));
let browser, server, page;
let cases = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  page = await browser.newPage();
  const errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;

  const catalogs={};
  for(const language of ["en","ko"])catalogs[language]=JSON.parse(await readFile(join(app,`src/locales/${language}/command-menu.json`)));
  for(const language of ["en","ko"])for(const theme of ["light","dark"])for(const [width,height]of [[1440,900],[960,640],[480,320]]){
    const t=key=>catalogs[language][key];
    await page.setViewportSize({width,height});await page.goto(`${origin}/?language=${language}&theme=${theme}&commandPalette=true`);
    await page.getByRole("button",{name:t("command-menu.title"),exact:true}).waitFor();
    await page.waitForTimeout(400);
    const counts=await page.evaluate(()=>({...window.__commandMenuFixture}));
    const opener=page.getByRole("button",{name:t("command-menu.title"),exact:true});await opener.click();
    const dialog=page.getByRole("dialog",{name:t("command-menu.title"),exact:true}),input=dialog.getByRole("combobox");
    assert(await input.evaluate(node=>node===document.activeElement));
    const geometry=await dialog.evaluate(node=>{const box=node.getBoundingClientRect();return{left:box.left,right:box.right,top:box.top,bottom:box.bottom,width:box.width,radius:getComputedStyle(node).borderRadius,overflow:document.documentElement.scrollWidth>innerWidth,input:node.querySelector('input').getBoundingClientRect().height,rows:[...node.querySelectorAll('[cmdk-item]')].map(row=>row.getBoundingClientRect().height)};});
    assert(geometry.left>=16&&geometry.right<=width-16+1&&geometry.top>=16&&geometry.bottom<=height-16+1&&geometry.width<=640&&geometry.radius==="8px"&&!geometry.overflow&&geometry.input>=40&&geometry.rows.every(value=>value>=40),JSON.stringify(geometry));
    const order=await dialog.locator('[cmdk-item]').evaluateAll(nodes=>nodes.map(node=>node.dataset.value));assert.equal(order.length,new Set(order).size);assert.deepEqual(order.slice(0,7),['sessions','pull-requests','usage','schedules','inbox','search','settings'].map(id=>`navigate:${id}`));
    await input.fill(language==="en"?"Appearance Language":"외관 언어");await dialog.locator('[data-value="settings:appearance:language"]').waitFor();
    assert.deepEqual(await page.evaluate(()=>({...window.__commandMenuFixture})),counts,"Opening/filtering performs no RPC or writes");
    await page.keyboard.press("Tab");assert(await page.evaluate(()=>Boolean(document.activeElement.closest('.command-menu'))));await page.keyboard.press("Shift+Tab");assert(await input.evaluate(node=>node===document.activeElement));
    const primary=await page.evaluate(()=>/mac/i.test(navigator.platform)?"Meta":"Control");await page.keyboard.press(`${primary}+K`);assert.equal(await dialog.count(),0);assert(await opener.evaluate(node=>node===document.activeElement));
    await opener.click();assert.equal(await page.getByRole('combobox').inputValue(),"");await page.keyboard.press("Escape");
    await opener.click();await input.fill(language==="en"?"Appearance Language":"외관 언어");await dialog.locator('[data-value="settings:appearance:language"]').click();assert.equal(await dialog.count(),0);await page.waitForFunction(()=>document.activeElement?.getAttribute('data-settings-search-target')==='language');
    assert.equal((await page.evaluate(()=>({...window.__commandMenuFixture}))).writes,0,"Target navigation cannot save");
    const languageControl=page.locator('[data-settings-search-target="language"]');
    await opener.click();await input.fill(language==="en"?"Appearance Date format":"외관 날짜 형식");await dialog.locator('[data-value="settings:appearance:date-format"]').click();await page.waitForFunction(()=>document.activeElement?.getAttribute('data-settings-search-target')==='date-format');assert.equal(await languageControl.count(),1);
    await opener.click();await dialog.locator('[data-value="create:new-session"]').click();
    const composer=page.locator('textarea').filter({visible:true}).first();await composer.fill("Retained creation draft");const identity=await composer.evaluate(node=>{node.dataset.paletteDraft="original";return node.dataset.paletteDraft;});
    await page.keyboard.press(`${primary}+K`);await input.fill("no bundled match");await page.keyboard.press("Escape");assert.equal(await composer.inputValue(),"Retained creation draft");assert.equal(await composer.getAttribute('data-palette-draft'),identity);assert(await composer.evaluate(node=>node===document.activeElement));
    assert.equal((await page.evaluate(()=>({...window.__commandMenuFixture}))).writes,0,"Creation entry and palette dismissal cannot submit");
    cases++;
  }
  assert.deepEqual(errors,[]);console.log(JSON.stringify({operation:"command-menu-layout",cases,nativeAcceptance:"not-performed",actualZoomAcceptance:"not-performed",reflow:"equivalent-200-percent-viewport"}));
} finally { await browser?.close();if(server)await new Promise(done=>server.close(done));await rm(directory,{recursive:true,force:true}); }
