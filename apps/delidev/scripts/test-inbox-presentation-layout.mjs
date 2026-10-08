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

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = { revision: execFileSync("git", ["rev-parse", "HEAD"], { cwd: app, encoding: "utf8" }).trim(), dirty: Boolean(execFileSync("git", ["status", "--porcelain"], { cwd: app, encoding: "utf8" }).trim()) };
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-inbox-presentation-layout-"));
const screenshots = process.env.DELIDEV_INBOX_PRESENTATION_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/inbox-filter-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1100,768], [960,640], [759,640], [720,450], [480,320]]) {
    const context = `${language}/${theme}/${width}x${height}`;
    const label = (en, ko) => language === "ko" ? ko : en;
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?presentation=1&theme=${theme}&language=${language}`);
    const opener = page.locator(".sidebar-context-trigger");
    if (await opener.isVisible()) await opener.click();
    await page.getByRole("button", { name: label("Inbox", "받은 요청"), exact: true }).click();
    if (await page.locator(".sidebar-pane-dialog").count() && await page.locator(".sidebar-pane-dialog").evaluate(node => node.open)) await page.keyboard.press("Escape");
    const list = page.locator(".inbox-list-scroll");
    await page.locator(".inbox-row").first().waitFor();
    assert.equal(await page.getByRole("button", { name: /Apply filters|필터 적용/ }).count(), 0, context);
    for (const row of await page.locator(".inbox-row").all()) assert.ok((await row.boundingBox()).height >= 72, `${context} rows grow above72`);
    if(width>=1120) {
      assert.equal(await list.evaluate(node=>node.scrollHeight>node.clientHeight),true, `${context} independently scrollable list`);
      const detailScroll=await page.locator(".inbox-detail-pane").evaluate(node=>node.scrollTop);
      await list.evaluate(node=>{node.scrollTop=80;});
      assert.equal(await page.locator(".inbox-detail-pane").evaluate(node=>node.scrollTop),detailScroll, `${context} list scroll retains detail`);
      await list.evaluate(node=>{node.scrollTop=0;});
    }
    const first = page.locator('.inbox-row[data-inbox-id="0195c9c0-7b13-7000-8000-000000000003"]');
    await first.focus(); await first.press("Enter");
    const detail = page.locator(".inbox-detail-pane"), heading = detail.locator(".inbox-detail-top h3");
    await heading.waitFor();
    assert.equal(await heading.innerText(), "Synthetic original conversation", context);
    assert.equal(await detail.locator("h3").count(), 1, context);
    assert.equal(await detail.locator(".inbox-source-badge").innerText(), label("Execution succeeded", "실행 성공"), context);
    assert.equal(await detail.locator(".inbox-state-badge").innerText(), label("Unread", "읽지 않음"), context);
    assert.equal(await detail.getByRole("button", { name: label("Open session", "세션 열기"), exact: true }).evaluate(node => node.classList.contains("primary")), true, context);
    const metadata = detail.locator(".inbox-execution-metadata"), summary = metadata.locator("summary");
    assert.equal(await metadata.evaluate(node => node.open), false, context);
    const before = await page.evaluate(() => ({ reads: window.__inboxFilterFixture.reads, writes: window.__inboxFilterFixture.writes, answers: window.__inboxFilterFixture.answers, lists: window.__inboxFilterFixture.requests.length }));
    await summary.focus(); await summary.press("Enter");
    assert.equal(await metadata.evaluate(node => node.open), true, context);
    assert.equal(await summary.evaluate(node => document.activeElement === node), true, context);
    assert.notEqual(await summary.evaluate(node=>getComputedStyle(node).outlineStyle),"none", `${context} visible keyboard focus`);
    const original = JSON.parse(await metadata.locator("pre").innerText());
    assert.equal(original.native_thread_id, "original-native-thread", context); assert.equal(original.native_turn_id, "original-native-turn", context); assert.equal(original.sequence, 17, context); assert.equal(original.outcome, "succeeded", context); assert.ok(original.job_id && original.input_id && original.execution_id, context);
    assert.deepEqual(await page.evaluate(() => ({ reads: window.__inboxFilterFixture.reads, writes: window.__inboxFilterFixture.writes, answers: window.__inboxFilterFixture.answers, lists: window.__inboxFilterFixture.requests.length })), before, `${context} disclosure zeroRPC`);
    const nodes = await page.evaluate(() => {
      const pane = document.querySelector(".inbox-detail-pane"), title = pane.querySelector(".inbox-detail-top h3"), body = pane.querySelector(".inbox-detail-content"), header = document.querySelector(".inbox-header"), rows = document.querySelector(".inbox-list-pane");
      return { inset: title.getBoundingClientRect().left-pane.getBoundingClientRect().left, aligned: title.getBoundingClientRect().left-body.getBoundingClientRect().left, bodyWidth: body.getBoundingClientRect().width, headerHeight: header.getBoundingClientRect().height, listWidth: rows.getBoundingClientRect().width, pageOverflow: document.documentElement.scrollWidth>innerWidth+1, controls: [...pane.querySelectorAll("button, summary")].filter(node => node.getBoundingClientRect().height>0).map(node => node.getBoundingClientRect().height) };
    });
    assert.ok(Math.abs(nodes.inset-24)<=1 && Math.abs(nodes.aligned)<=1, `${context} detail anchor ${JSON.stringify(nodes)}`);
    assert.ok(nodes.bodyWidth<=900.5 && nodes.headerHeight>=72, `${context} bounded content/header`);
    if(width>=1120) assert.ok(Math.abs(nodes.listWidth-320)<=1, `${context}320list`);
    assert.equal(nodes.pageOverflow,false, `${context} page wrapping`); assert.ok(nodes.controls.every(height => height>=39.5), `${context} minimum targets`);
    const retained = await metadata.elementHandle();
    await page.getByRole("button", { name: label("Refresh", "새로고침"), exact: true }).click();
    await page.waitForFunction(reads => window.__inboxFilterFixture.reads>reads, before.reads);
    assert.equal(await retained.evaluate(node=>node.isConnected && node.open),true, `${context} same-entry expansion`);
    if(width<1120) {
      await detail.getByRole("button", { name: label("Back to inbox", "받은 요청으로 돌아가기"), exact: true }).click();
      assert.equal(await first.evaluate(node=>node===document.activeElement),true, `${context} Back focus`);
    }
    await page.locator('.inbox-row[data-inbox-id="0195c9c0-7b13-7000-8000-000000000020"]').click();
    await page.waitForFunction(() => document.querySelector(".inbox-detail-top h3")?.textContent?.startsWith("Long original"));
    assert.equal(await metadata.evaluate(node=>node.open),false, `${context} new selection collapsed`);
    assert.equal(await detail.locator(".inbox-source-badge").innerText(),label("Execution failed","실행 실패"),context);
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false, `${context} long title wraps`);
    if(screenshots) { await mkdir(resolve(screenshots),{recursive:true}); await page.screenshot({path:join(resolve(screenshots),`inbox-${width}-${language}-${theme}.png`),fullPage:true}); }
    if(width<1120) await detail.getByRole("button", { name: label("Back to inbox", "받은 요청으로 돌아가기"), exact: true }).click();
    await page.locator('.inbox-row[data-inbox-id="0195c9c0-7b13-7000-8000-000000000063"]').click();
    const answer = detail.locator("textarea").first(); await answer.fill("Retained original answer draft");
    if(await opener.isVisible()) await opener.click();
    const pane = page.locator(".sidebar-surface-content").filter({has: page.getByRole("button",{name:label("Reset","초기화"),exact:true})});
    await pane.getByRole("combobox",{name:label("Source","출처"),exact:true}).selectOption("2");
    await page.waitForFunction(()=>window.__inboxFilterFixture.requests.at(-1)?.source===2);
    assert.equal(await pane.isVisible(),true, `${context} filters retain drawer`);
    const reset=pane.getByRole("button",{name:label("Reset","초기화"),exact:true}); await reset.focus();await reset.press("Enter");
    await page.waitForFunction(()=>window.__inboxFilterFixture.requests.at(-1)?.source===0);
    assert.equal(await reset.evaluate(node=>node===document.activeElement),true, `${context} Resetfocus`);
    if(width<760) { assert.equal(await page.locator(".sidebar-pane-dialog").evaluate(node=>node.open),true,context); await page.keyboard.press("Escape"); }
    assert.equal(await answer.inputValue(),"Retained original answer draft", `${context} filtering preserves draft`);
    if(width<1120) await detail.getByRole("button",{name:label("Back to inbox","받은 요청으로 돌아가기"),exact:true}).click();
    await page.locator('.inbox-row[data-inbox-id="0195c9c0-7b13-7000-8000-000000000071"]').click();
    await page.waitForFunction(()=>document.querySelector(".inbox-detail-top h3")?.textContent==="Original quota account");
    assert.equal(await metadata.count(),0, `${context} quota has no terminal disclosure`);
    assert.equal(await detail.getByRole("button",{name:label("Open session","세션 열기"),exact:true}).isDisabled(),true,context);
    assert.deepEqual(await page.evaluate(()=>({writes:window.__inboxFilterFixture.writes,answers:window.__inboxFilterFixture.answers})),{writes:0,answers:0},`${context} presentation no mutations`);
    checks++; console.log(JSON.stringify({operation:"inbox-presentation-layout-case",context,result:"passed"}));
  }
  assert.deepEqual(failures, [], "No browser errors");
  console.log(JSON.stringify({ operation: "inbox-presentation-layout", source, result: "passed", checks, screenshots: screenshots ?? null }));
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
