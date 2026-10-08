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
 assert.deepEqual(errors,[]);
 process.stdout.write(JSON.stringify({operation:"scroll-pagination-layout",cases:8,screenshots,nativeAcceptance:"not-performed",accountAcceptance:"not-performed",actualChromeZoom:"effective-CSS-200-percent"})+"\n");
} finally {
 await browser?.close();
 if (server) await new Promise(done => server.close(done));
 await rm(directory,{recursive:true,force:true});
}
