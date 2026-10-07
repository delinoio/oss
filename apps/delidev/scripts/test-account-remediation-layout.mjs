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
const directory = await mkdtemp(join(tmpdir(), "delidev-account-remediation-layout-"));
const screenshots = process.env.DELIDEV_ACCOUNT_REMEDIATION_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/account-remediation-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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

  // 480x320 is the effective content viewport of 960x640 at 200% zoom.
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width,height] of [[1440,900],[960,640],[390,844],[480,320]]) {
    await page.setViewportSize({width,height});await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    const trigger=page.getByRole("button", {name:language === "ko" ? "구독 읽기 문제 설명" : "Explain subscription read problem"});await trigger.waitFor();
    const inspect=page.getByRole("button", {name:language === "ko" ? "원래 로그인 상태 조회" : "Inspect original sign-in status"});await inspect.waitFor();
    assert.equal(await page.evaluate(()=>window.__accountRemediationFixture.inspect),0);
    assert(!(await page.locator("main").textContent()).includes("private-native-sentinel"));
    assert(!(await page.locator("main").textContent()).includes("private-path-sentinel"));
    const geometry=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth>innerWidth+1,rail:document.querySelector(".sidebar-rail").getBoundingClientRect().width}));
    assert(!geometry.overflow && geometry.rail<=52,JSON.stringify(geometry));
    await inspect.focus();await inspect.press("Enter");assert.equal(await page.evaluate(()=>window.__accountRemediationFixture.inspect),1);
    const before=await page.evaluate(()=>window.__accountRemediationFixture.inventory);await trigger.focus();await trigger.press("Enter");
    const popup=page.getByRole("dialog",{name:language === "ko" ? "구독 읽기 문제 설명" : "Explain subscription read problem"});await popup.waitFor();
    const box=await popup.boundingBox();assert(box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=height+1,JSON.stringify(box));
    assert((await popup.textContent()).includes("0195c9c0-7b13-7000-8000-000000000003"));
    const retry=popup.getByRole("button",{name:language === "ko" ? "다시 시도" : "Retry",exact:true});await retry.focus();await retry.press("Enter");
    await page.waitForFunction(previous=>window.__accountRemediationFixture.inventory===previous+1,before);
    assert.equal(await page.evaluate(()=>window.__accountRemediationFixture.unsupportedMutation),0);
    if(screenshots){await mkdir(resolve(screenshots),{recursive:true});await page.screenshot({path:join(resolve(screenshots),`${language}-${theme}-${width}.png`)});}
    await popup.press("Escape");assert.equal(await trigger.evaluate(node=>node===document.activeElement),true);
    checks++;console.log(JSON.stringify({operation:"account-remediation-layout",language,theme,width,height,result:"passed"}));
  }
  assert.deepEqual(failures,[]);console.log(JSON.stringify({operation:"account-remediation-layout",source,checks,result:"passed",nativeAcceptance:false}));
} finally {await browser?.close();await new Promise(done=>server?server.close(done):done());await rm(directory,{recursive:true,force:true});}
