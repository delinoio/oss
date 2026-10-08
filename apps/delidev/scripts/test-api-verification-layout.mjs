// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence, with no native command, account write or external read.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-api-verification-layout-"));
const screenshots = process.env.DELIDEV_API_VERIFICATION_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/api-verification-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage(); page.setDefaultTimeout(8000); page.on("pageerror", error => failures.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const sizes = [[1920,1080],[1440,900],[1280,820],[960,640],[640,480],[720,450],[480,320]];
  const labels = {
    en: { check: "Check again", retry: "Retry original authentication check", more: "More actions for", details: "Details", usage: "View usage" },
    ko: { check: "다시 확인", retry: "원래 인증 확인 재시도", more: "의 다른 작업", details: "상세", usage: "사용량 보기" },
  };
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width,height] of sizes) for (const scenario of ["accepted","long","pending","failed","unsupported","disconnected","uncertain"]) {
    const context = `${language}/${theme}/${width}x${height}/${scenario}`;
    console.log(JSON.stringify({ operation: "api-verification-case", context }));
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?language=${language}&theme=${theme}&scenario=${scenario}`);
    const row = page.locator(".api-entry-row"), band = row.locator(".api-verification-band");
    await band.waitFor();
    try { await page.waitForFunction(() => window.__apiVerificationFixture.counters.usage === 1); }
    catch (error) { console.log(JSON.stringify({context,counters:await page.evaluate(() => window.__apiVerificationFixture.counters),errors:failures,text:await row.textContent()})); throw error; }
    const fixture = () => page.evaluate(() => window.__apiVerificationFixture.counters);
    const check = () => row.getByRole("button", { name: labels[language].check, exact: true });
    const inspect = async () => {
      const geometry = await row.evaluate(node => {
        const band = node.querySelector(".api-verification-band"), summary = node.querySelector(".api-usage-main"), disclaimer = node.querySelector(".api-verification-disclaimer");
        const b = band.getBoundingClientRect(), s = summary.getBoundingClientRect(), d = disclaimer.getBoundingClientRect();
        const columns = getComputedStyle(node.querySelector(".api-verification-status")).gridTemplateColumns.split(" ").length;
        const visible = [...node.querySelectorAll("p,h2,strong,small,button")].filter(element => element.getClientRects().length && !element.closest("[hidden],.api-entry-sr-only"));
        return { band: { left:b.left,right:b.right,top:b.top,bottom:b.bottom }, summary: { left:s.left,right:s.right,bottom:s.bottom }, disclaimer: { left:d.left,right:d.right,top:d.top,text:disclaimer.textContent }, overflow: globalThis.document.documentElement.scrollWidth > innerWidth + 1 || node.scrollWidth > node.clientWidth + 1, clipped: visible.filter(element => element.scrollWidth > element.clientWidth + 2).map(element => element.tagName + ":" + element.textContent.slice(0,50)), columns, available: node.closest(".api-usage-list").clientWidth, rowWidth: node.getBoundingClientRect().width, icons:[...band.querySelectorAll(".api-verification-icon")].every(icon => icon.getAttribute("aria-hidden") === "true"), status:band.querySelector('[role="status"]') !== null };
      });
      assert(!geometry.overflow && !geometry.clipped.length, `${context}: overflow/clipping ${JSON.stringify(geometry)}`);
      assert(Math.abs(geometry.band.left-geometry.summary.left)<=1 && Math.abs(geometry.band.right-geometry.summary.right)<=1 && geometry.band.top >= geometry.summary.bottom && geometry.disclaimer.top >= geometry.band.bottom, `${context}: full-width hierarchy ${JSON.stringify(geometry)}`);
      assert(geometry.rowWidth <= 1041 && geometry.icons && geometry.status, `${context}: cap/status/decorative icons ${JSON.stringify(geometry)}`);
      assert.equal(geometry.columns, geometry.available < 640 ? 1 : 3, `${context}: retained 640px group stacking`);
      const summaryColumns = await row.locator(".api-usage-main").evaluate(element => getComputedStyle(element).gridTemplateColumns.split(" ").length);
      assert.equal(summaryColumns,geometry.available < 640 ? 1 : geometry.available < 900 ? 2 : 3,`${context}: unchanged summary reflow`);
      if (width >= 1440) { const actionBox = await row.locator(".api-verification-actions").boundingBox(); assert(Math.abs(geometry.band.right-actionBox.x-actionBox.width-12)<=1,`${context}: original action at band right`); }
      assert.equal(geometry.disclaimer.text,language === "en" ? "Verification does not establish model inference permission, harness readiness, usage or quota recovery." : "인증 확인은 모델 추론 권한, 하네스 준비 상태, 사용량 또는 할당량 복구를 확인하지 않습니다.", `${context}: complete disclaimer`);
      assert.equal(await band.locator(".api-verification-group").count(),3,context);
      if (["accepted","long","pending","uncertain"].includes(scenario)) assert(await row.locator(".api-verification-times time").evaluateAll(nodes => nodes.length > 0 && nodes.every(node => node.getAttribute("datetime") === "2026-10-08T00:08:14Z" && node.getAttribute("title") === "2026-10-08T00:08:14Z")), `${context}: exact original timestamp retained behind shared presentation`);
    };
    await inspect();
    const initial = await fixture(); assert.equal(initial.validate.length,0,context); assert.equal(initial.discover.length,0,context);
    const originalBand = await band.elementHandle();
    if (["pending","uncertain"].includes(scenario)) {
      await check().focus(); await check().press("Enter");
      await page.waitForFunction(() => window.__apiVerificationFixture.counters.validate.length === 1);
      if (scenario === "uncertain") {
        await row.getByRole("button",{ name:labels[language].retry, exact:true }).waitFor();
        const evidence = row.locator(".api-verification-problems details");
        await evidence.locator("summary").focus(); await evidence.locator("summary").press("Enter");
        assert((await evidence.textContent()).includes("Synthetic lost authentication receipt with complete original recovery guidance."),`${context}: original complete failure explanation retained`);
        await inspect();
      }
      assert(await check().isDisabled(), `${context}: pending/uncertain new checks locked`);
    }
    const beforePresentation = await fixture();
    await page.setViewportSize({ width:480,height:320 });
    await page.evaluate(next => window.__apiVerificationFixture.language(next),language === "ko" ? "en" : "ko");
    await page.evaluate(next => window.__apiVerificationFixture.language(next),language);
    await page.setViewportSize({width,height});
    const more = row.getByRole("button",{name:new RegExp(labels[language].more)});
    await more.focus(); await more.press("Enter");
    await row.getByRole("button",{name:labels[language].details,exact:true}).press("Enter");
    const details = page.getByRole("dialog", { name:labels[language].details, exact:true });
    await details.waitFor({state:"visible"});
    await page.keyboard.press("Escape");
    await details.waitFor({state:"hidden"});
    assert(await more.evaluate(element => element === document.activeElement),`${context}: Details Escape restores permanent More opener`);
    await more.focus(); await more.press("Enter"); await row.locator(".api-entry-more-panel").waitFor(); await more.press("Escape");
    assert(await more.evaluate(element => element === document.activeElement),`${context}: More Escape restores opener`);
    assert(await more.evaluate(element => getComputedStyle(element).outlineStyle !== "none"),`${context}: visible keyboard focus`);
    await inspect();
    assert(await originalBand.evaluate(element => element === document.querySelector(".api-verification-band")),`${context}: original mounted controller presentation retained`);
    const afterPresentation = await fixture();
    assert.deepEqual(afterPresentation.validate,beforePresentation.validate,`${context}: no presentation validation`);
    assert.deepEqual(afterPresentation.discover,beforePresentation.discover,`${context}: no presentation discovery`);
    if (screenshots && [1920,960,480].includes(width) && ["accepted","long","uncertain"].includes(scenario)) { await mkdir(resolve(screenshots),{recursive:true}); await page.screenshot({path:join(resolve(screenshots),`${scenario}-${language}-${theme}-${width}x${height}.png`),fullPage:true}); }
    if (scenario === "pending") await page.evaluate(() => window.__apiVerificationFixture.release());
    if (scenario === "uncertain") { const retry = row.getByRole("button",{name:labels[language].retry,exact:true}); await retry.focus(); await retry.press("Enter"); }
    if (["accepted","long","failed","unsupported"].includes(scenario)) { await check().focus(); await check().press("Enter"); }
    if (scenario !== "disconnected") {
      await page.waitForFunction(() => window.__apiVerificationFixture.counters.discover.length === 1);
      const final = await fixture(); assert.equal(final.discover[0].expectedRevision,"2",`${context}: confirmed authentication revision`);
      if (scenario === "uncertain") assert.deepEqual(final.validate[1],final.validate[0],`${context}: exact original request/revision`);
      else assert.equal(final.validate.length,1,`${context}: one explicit check`);
    } else assert(await check().isDisabled(),`${context}: disconnected check unavailable`);
    const usage = row.getByRole("button",{name:labels[language].usage,exact:true}); await usage.focus(); await usage.press("Enter");
    const navigation = await page.evaluate(() => { const f = window.__apiVerificationFixture; return {actual:f.counters.navigation[0], expected:{accountId:f.accountId,from:f.from,until:f.until}}; });
    assert.deepEqual(navigation.actual,navigation.expected,`${context}: exact account/range navigation`);
    const final = await fixture(); assert.equal(final.usage,1,`${context}: retained read`); assert.equal(final.edit,0,context); assert.equal(final.remove,0,context); assert.equal(final.manage,0,context);
    checks++;
  }
  assert.deepEqual(failures,[]);
  console.log(JSON.stringify({operation:"api-verification-browser",source,checks,result:"passed",evidence:"synthetic original-controller Chrome;720x450/480x320 are effective200% layouts; no packaged/native/account acceptance"}));
} finally { await browser?.close(); await new Promise(done => server ? server.close(done) : done()); await rm(directory,{recursive:true,force:true}); }
