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
const directory = await mkdtemp(join(tmpdir(), "delidev-pr-sidebar-layout-"));
const screenshots = process.env.DELIDEV_PR_CARDS_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/pr-list-cards-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const id = "0195c9c0-7b13-7000-8000-000000000001";
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1100,768], [960,640], [640,480], [720,450]]) {
    const context = `${language}/${theme}/${width}x${height}`;
    console.log(JSON.stringify({ operation: "pr-list-cards-case", context }));
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?theme=${theme}&language=${language}`);
    await page.getByRole("button", { name: language === "ko" ? "풀 리퀘스트" : "Pull requests", exact: true }).click();
    const opener = page.locator(".sidebar-context-trigger"); if (await opener.isVisible()) await opener.click();
    const pane = page.getByRole("region", { name: language === "ko" ? "풀 리퀘스트 탐색 및 필터" : "Pull requests navigation and filters", exact: true });
    await pane.getByRole("button", { name: language === "ko" ? `oss. 저장소 ID: ${id}` : `oss. Repository ID: ${id}`, exact: true }).click();
    assert.equal(await page.evaluate(() => window.__prSidebarFixture.github), 0, `${context}: explicit initial Load`);
    const load = pane.getByRole("button", { name: language === "ko" ? "풀 리퀘스트 불러오기" : "Load pull requests", exact: true }); await load.click();
    await page.locator(".pr-list-card").first().waitFor();
    assert.equal(await page.locator(".pr-list-card").count(), 4, context);
    const geometry = await page.locator(".pull-requests-page").evaluate(node => {
      const style = getComputedStyle(node), header = node.querySelector(".pr-list-header"), cards = [...node.querySelectorAll(".pr-list-card")];
      return { width: node.getBoundingClientRect().width, padding: style.paddingLeft, margin: style.marginLeft, overflow: node.scrollWidth > node.clientWidth + 1, cards: cards.map(card => { const s = getComputedStyle(card), title = card.querySelector("h3"), action = card.querySelector("button"); return { radius: s.borderRadius, padding: s.paddingLeft, title: getComputedStyle(title).fontSize, titleFull: title.scrollHeight <= title.clientHeight + 1, overflow: card.scrollWidth > card.clientWidth + 1, actionHeight: action.getBoundingClientRect().height }; }), refresh: header.querySelectorAll("button").length, pendingEmpty: node.querySelector(".pending-pr-actions").dataset.empty, timestamps: [...node.querySelectorAll("time")].map(time => [time.textContent, time.dateTime]) };
    });
    assert(geometry.width <= 1120 && geometry.margin === "0px" && geometry.padding === (width < 760 ? "16px" : "32px") && !geometry.overflow, `${context}: ${JSON.stringify(geometry)}`);
    assert(geometry.cards.every(card => card.radius === "12px" && card.padding === "20px" && card.title === "16px" && card.titleFull && !card.overflow && card.actionHeight >= 40), context);
    assert.equal(geometry.refresh, 1, context); assert.equal(geometry.pendingEmpty, "true", context);
    assert(geometry.timestamps.every(([value, datetime]) => value === datetime && /Z$/.test(value)), context);
    assert(geometry.timestamps.some(([value]) => value === "2026-10-07T08:07:22.123456789Z"), context);
    const before = await page.evaluate(() => window.__prSidebarFixture.github);
    if (await opener.isVisible()) await opener.click();
    const state = pane.getByRole("radio", { name: language === "ko" ? "열림" : "Open", exact: true }); await state.focus(); await state.press("ArrowRight");
    if (width < 760) await page.keyboard.press("Escape");
    assert.equal(await page.evaluate(() => window.__prSidebarFixture.github), before, `${context}: draft filters do not read`);
    assert(await page.locator(".pr-list-applied").first().textContent().then(value => value.includes(language === "ko" ? "열림" : "Open")), context);
    const read = page.locator(".pr-list-card").first().getByRole("button"); await read.focus(); await read.press("Enter");
    await page.getByRole("button", { name: language === "ko" ? "결과로 돌아가기" : "Back to results", exact: true }).waitFor();
    assert.equal(await page.locator(".pr-list-card").count(), 0, `${context}: detail uses the original renderer`);
    await page.getByRole("button", { name: language === "ko" ? "결과로 돌아가기" : "Back to results", exact: true }).click(); await page.locator(".pr-list-card").first().waitFor();
    const more = page.locator('[data-continuation="'+(language === "ko" ? "GitHub 조회 결과" : "GitHub query results")+'"] button');
    if (await more.count()) { await more.click(); await page.getByText(language === "ko" ? "2페이지에서 반환된 pull request가 없습니다." : "No pull requests were returned on page 2.", { exact: true }).waitFor(); }
    assert.equal(await page.locator(".pr-list-card").count(), 4, `${context}: empty append retains accepted cards`);
    if (screenshots && width === 1440) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `cards-${language}-${theme}.png`) }); }
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ operation: "pr-list-cards-browser", source, checks, result: "passed", evidence: "synthetic App browser; effective viewport zoom only; no packaged CEF acceptance" }));
} finally {
  await browser?.close(); await new Promise(done => server ? server.close(done) : done()); await rm(directory, { recursive: true, force: true });
}
