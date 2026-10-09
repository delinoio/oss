// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-project-registration-"));

let browser, server, page;
let cases = 0;
const messages = new Map(), catalogs = { en: {}, ko: {} };
for (const file of await readdir(join(app, "src/locales/en"))) {
  if (!file.endsWith(".json")) continue;
  const en = JSON.parse(await readFile(join(app, "src/locales/en", file))), ko = JSON.parse(await readFile(join(app, "src/locales/ko", file)));
  Object.assign(catalogs.en, en); Object.assign(catalogs.ko, ko);
  for (const [key, value] of Object.entries(en)) if (!messages.has(value)) messages.set(value, ko[key]);
}
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900],[1280,820],[960,640],[640,480],[480,320]]) for (const host of ["home","settings"]) {
    const l = key => catalogs[language][key];
    await page.setViewportSize({width,height});await page.goto(`${origin}/?theme=${theme}&populated=true&language=${language}&projectWizard=true&projectRegistration=true`);
    if (host === "settings") { await page.getByRole("button",{name:language==="ko"?"설정":"Settings",exact:true}).click(); if(width<760) await page.getByRole("button",{name:l("App.open_a007d6").replace("<s0/>",l("App.extra.a1de4eceaa3b")),exact:true}).click(); await page.getByRole("button",{name:language==="ko"?"프로젝트":"Projects",exact:true}).click(); await page.getByRole("button",{name:language==="ko"?"새 프로젝트":"New Project",exact:true}).click(); }
    else { if(width<760) await page.locator(".sidebar-context-trigger").click(); await page.getByRole("button",{name:language==="ko"?"새 프로젝트":"New project",exact:true}).click(); }
    const parent=page.locator(".settings-task-dialog").filter({has:page.locator(".project-creation")});await parent.getByRole("searchbox").fill("draft filter");
    const add=parent.getByRole("button",{name:l("project-creation.addRepository"),exact:true});await add.click();
    const child=page.locator(".settings-task-dialog").filter({has:page.locator(".repository-registration")});await child.waitFor();
    assert.equal(await parent.getAttribute("inert"),"");assert(await child.evaluate(node=>node.querySelectorAll("form form").length===0));
    const box=await child.boundingBox();assert(box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=height+1);assert(box.width<=640);
    assert(await child.locator(".settings-task-body").evaluate(node=>node.scrollWidth<=node.clientWidth+1));
    const controls=child.locator("button,input,select,textarea,summary,a[href],[tabindex]");
    await controls.evaluateAll(nodes=>{ const available=nodes.filter(node=>node.tabIndex>=0&&!node.matches(":disabled")&&!node.closest("[hidden],[inert]")&&node.getClientRects().length);available.at(-1).focus(); });
    await page.keyboard.press("Tab");assert(await child.locator(".settings-task-close").evaluate(node=>node===document.activeElement));await page.keyboard.press("Shift+Tab");assert(await controls.evaluateAll(nodes=>{const available=nodes.filter(node=>node.tabIndex>=0&&!node.matches(":disabled")&&!node.closest("[hidden],[inert]")&&node.getClientRects().length);return available.at(-1)===document.activeElement;}));
    await page.keyboard.press("Escape");await child.waitFor({state:"hidden"});assert(await add.evaluate(node=>node===document.activeElement));assert.equal(await parent.getByRole("searchbox").inputValue(),"draft filter");
    await add.click();await child.getByRole("textbox",{name:l("repository-registration.inline.cd01c2ef6a"),exact:true}).fill("https://github.com/delinoio/registered.git");
    await child.getByRole("button",{name:l("project-creation.addRepository"),exact:true}).click();await child.waitFor({state:"hidden"});
    await parent.locator(".project-repository-grip").first().waitFor();assert.equal(await page.locator("html").getAttribute("data-fixture-registration-count"),"1");assert.equal(await page.locator("html").getAttribute("data-fixture-project-save-count"),null);
    assert.equal(await parent.getByRole("searchbox").inputValue(),"draft filter");cases++;
  }
  assert.deepEqual(errors,[]);console.log(JSON.stringify({operation:"project_registration_layout",result:"passed",cases,languages:2,themes:2,hosts:2,widths:[1440,1280,960,640,480],equivalentReflowOnly:true,nativeAcceptance:"not-performed"}));
} finally { await browser?.close();if(server) await new Promise(done=>server.close(done));await rm(directory,{recursive:true,force:true}); }
