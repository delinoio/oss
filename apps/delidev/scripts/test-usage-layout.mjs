// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF or native acceptance.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";


async function wheelChaining(page, context) {
 const table=page.locator('.usage-detail > .usage-table'), owner=page.locator('#main');
 const before=await page.evaluate(()=>({reads:window.__usageFixture.summary, tabs:[...document.querySelectorAll('.usage-tabs [role=tab]')].map(n=>n.getAttribute('aria-selected')),details:[...document.querySelectorAll('.usage-page details')].map(n=>n.open), filters:[...document.querySelectorAll('.usage-sidebar input,.usage-sidebar select')].map(n=>n.value)}));
 assert.deepEqual(await table.evaluate(n=>[getComputedStyle(n).overscrollBehaviorX,getComputedStyle(n).overscrollBehaviorY]),['contain','auto'],context);
 const prepare=async()=>{
  await table.evaluate(n=>{const detail=n.parentElement;detail.style.marginTop='500px';detail.style.marginBottom='1500px';const owner=document.querySelector('#main');owner.scrollTop=n.getBoundingClientRect().top-owner.getBoundingClientRect().top+owner.scrollTop-100;});
  await page.waitForTimeout(100);
  const point=await table.evaluate(n=>{const r=n.getBoundingClientRect(),m=document.querySelector('#main').getBoundingClientRect();return{x:Math.max(r.left,m.left)+Math.min(40,r.width/2),y:Math.max(r.top,m.top)+Math.min(40,r.height/2)};});await page.mouse.move(point.x,point.y);
 };
 const outward=async delta=>{await prepare();const start=await owner.evaluate(n=>n.scrollTop);assert(await owner.evaluate((n,delta)=>delta<0?n.scrollTop>=150:n.scrollHeight-n.clientHeight-n.scrollTop>=150,delta),`${context}: parent has outward range`);await page.mouse.wheel(0,delta);try {await page.waitForFunction(({start,delta})=>delta<0?document.querySelector('#main').scrollTop<start:document.querySelector('#main').scrollTop>start,{start,delta});} catch(error) {console.log(JSON.stringify({operation:'usage-wheel-failure',context,delta,start,geometry:await table.evaluate(n=>({top:n.scrollTop,height:n.clientHeight,scroll:n.scrollHeight,parent:document.querySelector('#main').scrollTop}))}));throw error;}await page.waitForTimeout(300);};
 assert(await table.evaluate(n=>n.scrollHeight===n.clientHeight),`${context}: no vertical table overflow`);
 // Counterfactual verifies the original CSS boundary consumes the same native input.
 await table.evaluate(n=>n.style.overscrollBehaviorY='contain');await prepare();const blocked=await owner.evaluate(n=>n.scrollTop);await page.mouse.wheel(0,-150);await page.waitForTimeout(150);assert.equal(await owner.evaluate(n=>n.scrollTop),blocked,`${context}: original containment blocks chaining`);await table.evaluate(n=>n.style.overscrollBehaviorY='');
 await outward(-150);await outward(150);
 if(await table.evaluate(n=>n.scrollWidth>n.clientWidth)) {
  await prepare();await page.mouse.wheel(150,0);await page.waitForFunction(()=>document.querySelector('.usage-detail > .usage-table').scrollLeft>0);await page.waitForTimeout(300);
  await table.evaluate(n=>n.scrollLeft=0);await table.focus();await page.keyboard.press('ArrowRight');await page.waitForFunction(()=>document.querySelector('.usage-detail > .usage-table').scrollLeft>0);assert(await table.evaluate(n=>n===document.activeElement),context);await table.evaluate(n=>n.scrollLeft=0);
 }
 await table.evaluate(n=>{n.style.maxHeight='120px';const body=n.querySelector('tbody');for(let i=0;i<5;i++){const row=body.firstElementChild.cloneNode(true);row.dataset.wheelFixture='true';body.append(row);}});
 assert(await table.evaluate(n=>n.scrollHeight>n.clientHeight),`${context}: fixture-only inner vertical overflow`);
 for(const delta of [-60,60]) {
  await prepare();await table.evaluate(n=>n.scrollTop=(n.scrollHeight-n.clientHeight)/2);const start=await table.evaluate(n=>n.scrollTop),parent=await owner.evaluate(n=>n.scrollTop);await page.mouse.wheel(0,delta);await page.waitForFunction(({start,delta})=>{const top=document.querySelector('.usage-detail > .usage-table').scrollTop;return delta<0?top<start:top>start;},{start,delta});await page.waitForTimeout(300);assert.equal(await owner.evaluate(n=>n.scrollTop),parent,`${context}: inner scroll precedes parent`);
 }
 // Let the previous native wheel animation settle before freezing the boundary.
 await page.waitForTimeout(300);await table.evaluate(n=>n.scrollTop=0);await outward(-150);await table.evaluate(n=>n.scrollTop=n.scrollHeight);await outward(150);
 await table.evaluate(n=>{n.style.maxHeight='';n.querySelectorAll('[data-wheel-fixture]').forEach(row=>row.remove());n.scrollTop=0;n.parentElement.style.marginTop='';n.parentElement.style.marginBottom='';});
 assert.deepEqual(await page.evaluate(()=>({reads:window.__usageFixture.summary,tabs:[...document.querySelectorAll('.usage-tabs [role=tab]')].map(n=>n.getAttribute('aria-selected')),details:[...document.querySelectorAll('.usage-page details')].map(n=>n.open),filters:[...document.querySelectorAll('.usage-sidebar input,.usage-sidebar select')].map(n=>n.value)})),before,`${context}: wheel input preserves data/filters/tabs/disclosures and reads`);
}

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = { revision: execFileSync("git", ["rev-parse", "HEAD"], { cwd: app, encoding: "utf8" }).trim(), dirty: Boolean(execFileSync("git", ["status", "--porcelain"], { cwd: app, encoding: "utf8" }).trim()) };
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-usage-layout-"));
const screenshots = process.env.DELIDEV_USAGE_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/usage-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  // 720x450 is the effective content viewport of 1440x900 at 200% zoom.
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1024,768], [390,844], [720,450]]) for (const empty of [false, true]) {
    const context = `${language}/${theme}/${width}x${height}/empty=${empty}`;
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?theme=${theme}&language=${language}&empty=${empty}`);
    await page.getByRole("button", { name: language === "ko" ? "사용량" : "Usage", exact: true }).click();
    const main = page.locator(".usage-page"); await main.locator(".usage-summary").waitFor();
    assert.equal(await main.getByRole("tab").count(), 3, context);
    assert.equal(await main.getByRole("tabpanel").count(), 1, context);
    assert.equal(await main.locator(".usage-trends").evaluate(node => node.tagName), "SECTION", context);
    assert(await main.locator(".usage-trends").isVisible(), context);
    assert(await main.locator(".usage-trends").evaluate(node => Boolean(node.compareDocumentPosition(document.querySelector(".usage-detail")) & Node.DOCUMENT_POSITION_FOLLOWING)), context);
    const initialReads = await page.evaluate(() => window.__usageFixture.summary);
    const geometry = await main.evaluate(node => ({ width: node.getBoundingClientRect().width, overflow: node.scrollWidth > node.clientWidth + 1, documentOverflow: document.documentElement.scrollWidth > innerWidth + 1, primary: node.querySelector(".usage-metrics-primary").children.length, values: [...node.querySelectorAll(".usage-summary [data-token-measure] dd")].map(value => value.textContent), table: node.querySelector(".usage-table") ? { width: node.querySelector(".usage-table").clientWidth, scroll: node.querySelector(".usage-table").scrollWidth } : null }));
    assert(geometry.width <= 1280 && !geometry.overflow && !geometry.documentOverflow && geometry.primary === 4, `${context}: ${JSON.stringify(geometry)}`);
    if (empty) assert.deepEqual(geometry.values, Array(6).fill("0"), context);
    else {
      const details = main.locator(".usage-row-detail summary").first(); await details.focus(); await details.press("Enter");
      assert(await main.locator(".usage-row-detail details").first().evaluate(node => node.open), context);
      for (const exact of ["0195c9c0-7b13-7000-8000-000000000001", "9,007,199,254,740,993", "USD", "EUR"]) assert((await main.locator(".usage-row-detail").first().textContent()).includes(exact), context);
      await details.press("Enter");
      assert.equal(await main.locator(".usage-table table thead th").count(),9,context);
      await wheelChaining(page, context);
    }
    const daily = main.locator(".usage-daily-svg"); await daily.focus(); await page.keyboard.press("End"); await page.keyboard.press("Escape");
    const viewData = main.locator(".usage-chart-panel .usage-data-toggle").first(); await viewData.focus(); await viewData.press("Enter");
    assert(await main.locator("#usage-daily-data").isVisible(),context); await viewData.press("Enter");
    assert.equal(await page.evaluate(() => window.__usageFixture.summary), initialReads, `${context}: row and chart inspection do not query`);
    await page.locator("#main").evaluate(node => node.scrollTop=0); await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    if (width === 1440) assert(await main.locator(".usage-daily-svg").evaluate(node => node.getBoundingClientRect().top < innerHeight && node.getBoundingClientRect().bottom > 0),`${context}: daily chart is visible before session records`);
    if (screenshots && [1440, 390].includes(width)) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `responses-${width}-${language}-${theme}-${empty ? "empty" : "records"}.png`) }); }
    const tabs = main.getByRole("tab"); await tabs.nth(0).focus(); await page.keyboard.press("ArrowRight");
    assert.equal(await tabs.nth(1).getAttribute("aria-selected"), "true", context);
    assert.equal(await page.evaluate(() => window.__usageFixture.summary), initialReads, `${context}: tabs do not query`);
    const sources = main.locator(".usage-accounting-source"); assert.equal(await sources.count(), 3, context);
    const state = await sources.evaluateAll(nodes => nodes.map(node => node.querySelector("details").open));
    assert.deepEqual(state, [false, false, !empty], context);
    if (screenshots && [1440, 390].includes(width)) await page.screenshot({ path: join(resolve(screenshots), `native-${width}-${language}-${theme}-${empty ? "empty" : "records"}.png`) });
    await sources.nth(0).locator("summary").first().focus(); await page.keyboard.press("Enter");
    assert.equal(await sources.nth(0).locator("details").first().evaluate(node => node.open), true, context);
    await tabs.nth(1).focus(); await page.keyboard.press("End");
    assert.equal(await tabs.nth(2).getAttribute("aria-selected"), "true", context);
    assert.equal(await main.getByRole("tabpanel").count(), 1, context);
    await tabs.nth(2).press("ArrowLeft");
    assert.equal(await sources.nth(0).locator("details").first().evaluate(node => node.open), true, context);
    const opener = page.locator(".sidebar-context-trigger"); if (await opener.isVisible()) { await page.locator("#main").evaluate(node=>node.scrollTop=0); await opener.focus(); await opener.press("Enter"); }
    const pane = page.locator(".usage-sidebar");
    assert.equal(await pane.getByRole("button", { name: /Apply filters|필터 적용/ }).count(), 0, context);
    const presets = pane.locator(".usage-range-presets button");
    assert.deepEqual(await presets.allTextContents(), language === "ko" ? ["24시간", "7일", "30일"] : ["24 hours", "7 days", "30 days"], context);
    assert.deepEqual(await presets.evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-pressed"))), ["false", "false", "false"], context);
    assert.equal(await pane.locator(".usage-range-helper").textContent(), language === "ko" ? "선택 시점 기준 최근 기간입니다." : "Rolling range ending when selected.", context);
    const presetGeometry = await presets.evaluateAll(nodes => nodes.map(node => ({ width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height, radius: getComputedStyle(node).borderRadius })));
    assert(presetGeometry.every(value => value.height >= 40 && value.radius === "8px" && Math.abs(value.width - presetGeometry[0].width) < 1), `${context}: equal accessible preset geometry ${JSON.stringify(presetGeometry)}`);
    await page.evaluate(() => { Date.now = () => Date.parse("2026-10-09T03:00:00.123Z"); });
    await presets.nth(0).focus(); await presets.nth(0).press("Enter");
    assert.equal(await presets.nth(0).evaluate(node => node === document.activeElement), true, context);
    await page.waitForFunction(() => JSON.parse(window.__usageFixture.selections.at(-1)).untilUnixMs === String(Date.parse("2026-10-09T03:00:00.123Z")));
    assert.equal(await presets.nth(0).getAttribute("aria-pressed"), "true", context);
    assert(await presets.nth(0).evaluate(node => parseFloat(getComputedStyle(node).outlineWidth) > 0 && getComputedStyle(node).outlineStyle !== "none"), `${context}: visible keyboard focus`);
    assert.equal(await page.evaluate(() => { const request = JSON.parse(window.__usageFixture.selections.at(-1)); return String(BigInt(request.untilUnixMs) - BigInt(request.fromUnixMs)); }), "86400000", context);
    assert.equal(await pane.locator(".usage-range-presets").evaluate(node => getComputedStyle(node).gap), "8px", context);
    await page.keyboard.press("Tab"); assert.equal(await presets.nth(1).evaluate(node => node === document.activeElement), true, context);
    await presets.nth(1).press("Space");
    await page.waitForFunction(() => { const request = JSON.parse(window.__usageFixture.selections.at(-1)); return BigInt(request.untilUnixMs) - BigInt(request.fromUnixMs) === 7n * 86400000n; });
    await page.keyboard.press("Tab"); assert.equal(await presets.nth(2).evaluate(node => node === document.activeElement), true, context);
    await presets.nth(2).press("Enter");
    await page.waitForFunction(() => { const request = JSON.parse(window.__usageFixture.selections.at(-1)); return BigInt(request.untilUnixMs) - BigInt(request.fromUnixMs) === 30n * 86400000n; });
    assert.deepEqual(await presets.evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-pressed"))), ["false", "false", "true"], context);
    assert(await pane.isVisible(), context);
    const captured = await page.evaluate(() => window.__usageFixture.selections.at(-1));
    const readCount = await page.evaluate(() => window.__usageFixture.summary);
    await page.evaluate(() => { Date.now = () => Date.parse("2026-10-10T03:00:00.456Z"); });
    // Close only the test drawer to reach the separate page action; selection never closes it.
    if (width < 760) await page.keyboard.press("Escape");
    await main.locator(".usage-header-actions button").click();
    await page.waitForFunction(count => window.__usageFixture.summary > count, readCount);
    assert.equal(await page.evaluate(() => window.__usageFixture.selections.at(-1)), captured, `${context}: Refresh retains captured bounds`);
    if (await opener.isVisible()) { await opener.focus(); await opener.press("Enter"); }
    await presets.nth(2).click();
    await page.waitForFunction(() => JSON.parse(window.__usageFixture.selections.at(-1)).untilUnixMs === String(Date.parse("2026-10-10T03:00:00.456Z")));
    const date = pane.locator('input[type="datetime-local"]').first(); await date.focus(); await date.fill("2026-09-01T10:00");
    assert.deepEqual(await presets.evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-pressed"))), ["false", "false", "false"], context);
    assert.equal(await date.evaluate(node => node === document.activeElement), true, context);
    assert.equal(await pane.isVisible(), true, context);
    const reset = pane.locator(".actions button"); await reset.focus();
    const footer = await reset.boundingBox(); assert(footer && footer.y >= 0 && footer.y + footer.height <= height, `${context}: reset stays reachable`);
    await reset.press("Enter");
    assert.equal(await date.inputValue(), "", context);
    if (width < 760) assert.equal(await page.locator(".sidebar-pane-dialog").getAttribute("open"), null, context);
    assert.equal(await page.evaluate(() => window.__usageFixture.writes), 0, context);
    checks++;
    console.log(JSON.stringify({ operation: "usage-layout-case", context, result: "passed" }));
  }
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [390,844]]) {
    const context = `${language}/${theme}/${width}x${height}/failed-read`;
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?theme=${theme}&language=${language}&failure=true`);
    await page.getByRole("button", { name: language === "ko" ? "사용량" : "Usage", exact: true }).click();
    const main = page.locator(".usage-page"), alert = main.getByRole("alert"); await alert.waitFor();
    assert((await alert.textContent()).includes(language === "ko" ? "읽기 실패" : "failed read does not establish zero"), context);
    assert.equal(await alert.locator("details").evaluate(node => node.open), false, context);
    const refresh = main.getByRole("button", { name: language === "ko" ? "새로고침" : "Refresh", exact: true });
    await refresh.focus(); await refresh.press("Enter"); await main.locator(".usage-summary").waitFor();
    const requests = await page.evaluate(() => window.__usageFixture);
    assert.equal(requests.summary, 2, context); assert.equal(requests.selections[0], requests.selections[1], context); assert.equal(requests.writes, 0, context);
    await main.getByRole("tab").nth(1).click();
    assert((await main.textContent()).includes(language === "ko" ? "불완전" : "unsupported or incomplete"), context);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, context);
    if (screenshots) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `remediation-${width}-${language}-${theme}.png`) }); }
    checks++; console.log(JSON.stringify({ operation: "usage-inline-remediation-case", context, result: "passed" }));
  }
  for (const scenario of ["loading", "unsupported", "refreshFailure", "newQueryFailure"]) for (const language of ["en","ko"]) for (const [width,height] of [[1440,900],[390,844]]) {
    await page.setViewportSize({width,height}); await page.goto(`${origin}/?${scenario}=true&language=${language}`); await page.getByRole("button",{name:language==="ko" ? "사용량" : "Usage",exact:true}).click();
    const main=page.locator(".usage-page");
    if(scenario==="loading") { await main.locator(".usage-skeletons").waitFor(); assert.equal(await main.locator(".usage-summary").count(),0); await page.evaluate(()=>window.releaseUsage()); }
    await main.locator(".usage-summary").waitFor();
    if(scenario==="unsupported") assert(await main.locator(".usage-charts-unavailable").isVisible());
    if(scenario==="refreshFailure") { await main.locator(".usage-row-detail summary").first().click(); await main.locator(".usage-header-actions button").click(); await main.locator(".usage-stale-indicator").waitFor(); assert(await main.locator(".usage-row-detail details").first().evaluate(node=>node.open)); assert(await main.locator(".usage-summary").isVisible()); }
    if(scenario==="newQueryFailure") { const opener=page.locator(".sidebar-context-trigger"); if(await opener.isVisible()) { await page.locator("#main").evaluate(node=>node.scrollTop=0); await opener.focus(); await opener.press("Enter"); } await page.locator('.usage-sidebar input[type="datetime-local"]').first().fill("2026-09-02T10:00"); if(width<760) await page.keyboard.press("Escape"); await main.getByRole("alert").waitFor(); assert.equal(await main.locator(".usage-summary").count(),0); }
    assert.equal(await page.evaluate(()=>window.__usageFixture.writes),0); checks++; console.log(JSON.stringify({operation:"usage-state-case",scenario,language,width,result:"passed"}));
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ operation: "usage-layout-browser", source, checks, result: "passed", evidence: "synthetic App browser; effective zoom viewport only; no packaged/native acceptance" }));
} finally {
  await browser?.close(); await new Promise(done => server ? server.close(done) : done()); await rm(directory, { recursive: true, force: true });
}
