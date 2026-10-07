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
    assert.equal(await main.locator(".usage-trends").evaluate(node => node.open), false, context);
    const initialReads = await page.evaluate(() => window.__usageFixture.summary);
    const geometry = await main.evaluate(node => ({ width: node.getBoundingClientRect().width, overflow: node.scrollWidth > node.clientWidth + 1, documentOverflow: document.documentElement.scrollWidth > innerWidth + 1, primary: node.querySelector(".usage-metrics-primary").children.length, values: [...node.querySelectorAll(".usage-summary [data-token-measure] dd")].map(value => value.textContent), table: node.querySelector(".usage-table") ? { width: node.querySelector(".usage-table").clientWidth, scroll: node.querySelector(".usage-table").scrollWidth } : null }));
    assert(geometry.width <= 1280 && !geometry.overflow && !geometry.documentOverflow && geometry.primary === 4, `${context}: ${JSON.stringify(geometry)}`);
    if (empty) assert.deepEqual(geometry.values, Array(6).fill("0"), context);
    else assert((await main.textContent()).includes("0195c9c0-7b13-7000-8000-000000000001"), context);
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
    const opener = page.locator(".sidebar-context-trigger"); if (await opener.isVisible()) await opener.click();
    const pane = page.locator(".usage-sidebar");
    assert.equal(await pane.getByRole("button", { name: /Apply filters|필터 적용/ }).count(), 0, context);
    const date = pane.locator('input[type="datetime-local"]').first(); await date.focus(); await date.fill("2026-09-01T10:00");
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
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ operation: "usage-layout-browser", source, checks, result: "passed", evidence: "synthetic App browser; effective zoom viewport only; no packaged/native acceptance" }));
} finally {
  await browser?.close(); await new Promise(done => server ? server.close(done) : done()); await rm(directory, { recursive: true, force: true });
}
