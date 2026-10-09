// SPDX-License-Identifier: Apache-2.0
// Automated synthetic browser evidence, without app/native launch or screenshots.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-reset-credit-check-"));
let browser, server;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/reset-credit-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try { const pathname = new URL(request.url, "http://127.0.0.1").pathname, file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`); if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path"); response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css" }[extname(file)] ?? "application/octet-stream"); response.end(await readFile(file)); }
    catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage(), errors = []; page.on("pageerror", error => errors.push(error.message));
  let checks=0;
  for(const language of ["en","ko"]) for(const theme of ["light","dark"]) for(const width of [640,320]) for(const zoom of [1,2]) {
    await page.setViewportSize({width:width*zoom,height:800*zoom});
    await page.goto(`http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&zoom=${zoom}`);
    const use=page.getByRole("button",{name:language==="en"?"Use":"사용",exact:true});await use.waitFor();
    await page.waitForFunction(()=>!document.querySelector(".reset-credit-summary button").disabled);
    await use.focus();await page.keyboard.press("Enter");
    await page.waitForFunction(()=>document.activeElement?.tagName==="H4");
    assert(await page.getByRole("heading",{name:language==="en"?"Credit details":"리셋권 상세",exact:true}).evaluate(node=>node===document.activeElement));
    const rows=page.locator(".reset-credit-row");assert.equal(await rows.count(),2);
    assert.deepEqual(await rows.locator("strong").allTextContents(),language==="en"?["Reset credit 1","Reset credit 2"]:["리셋권 1","리셋권 2"]);
    for(const row of await rows.all()) {assert(await row.evaluate(node=>node.scrollWidth<=node.clientWidth));assert((await row.locator("button").boundingBox()).height>=40*zoom);assert.equal(await row.locator("time").getAttribute("datetime"),"2099-01-01T00:00:00Z");}
    const select=page.getByRole("button",{name:language==="en"?"Select Reset credit 2":"리셋권 2 선택",exact:true});await select.focus();await page.keyboard.press("Enter");await page.waitForFunction(()=>document.activeElement===document.querySelector(".reset-credit-confirmation h4"));
    assert(await page.locator(".reset-credit-confirmation h4").evaluate(node=>node===document.activeElement));
    assert((await page.locator(".reset-credit-confirmation").textContent()).includes(language==="en"?"Consume reset credit 2":"리셋권 2 사용"));
    assert((await page.locator(".reset-credit-confirmation").textContent()).includes("long_original_identity_".repeat(4)+"b"));
    assert(await page.locator(".reset-credit-confirmation").evaluate(node=>node.scrollWidth<=node.clientWidth));
    await page.getByRole("button",{name:language==="en"?"Keep credit":"유지",exact:true}).click();assert(await select.evaluate(node=>node===document.activeElement));
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));checks++;
  }
  assert.deepEqual(errors,[]);console.log(JSON.stringify({fixture:"reset-credit-numbering",checks,screenshots:false,nativeAcceptance:false}));
} finally { await browser?.close(); if (server) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
