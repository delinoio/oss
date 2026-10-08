// SPDX-License-Identifier: Apache-2.0
// Synthetic browser geometry only; no native, account or execution acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const { chromium } = await import(pathToFileURL(resolve(process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE)).href);
const directory = await mkdtemp(join(tmpdir(), "delidev-connections-"));
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/connections-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage();
  page.on("pageerror", error => console.error("fixture_page_error", error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1100,768], [960,640], [640,480], [720,450], [480,320]]) {
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    await page.getByRole("button", { name: language === "ko" ? "Studio server 열기" : "Open Studio server" }).waitFor();
    assert.equal(await page.locator(".connections-saved-row").count(), 2);
    assert.equal(await page.locator(".connections-advanced").getAttribute("open"), null);
    assert.equal(await page.locator(".connections-attention").count(), 1);
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth + 1));
    const overflow = await page.locator(".connections-page").evaluate(node => node.scrollWidth > node.clientWidth + 1);
    assert(!overflow);
    for (const height of await page.locator(".connections-page button:visible").evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height))) assert(height >= 40);
    const menu = page.locator(".connections-row-menu summary").first();
    await menu.focus(); await menu.press("Enter");
    assert.equal(await menu.evaluate(node => node.parentElement.open), true);
    await menu.press("Escape");
    assert.equal(await menu.evaluate(node => node.parentElement.open), false);
    assert(await menu.evaluate(node => node === document.activeElement));
    if (process.env.DELIDEV_CONNECTIONS_SCREENSHOTS && width === 1440 && theme === "light" && language === "en") await page.screenshot({ path: resolve(process.env.DELIDEV_CONNECTIONS_SCREENSHOTS, "connections.png"), fullPage: true });
    const advanced = page.locator(".connections-advanced > summary");
    await advanced.focus(); await advanced.press("Space");
    assert.equal(await advanced.evaluate(node => node.parentElement.open), true);
    await menu.press("Enter");
    await page.locator(".connections-row-menu button").first().click();
    await page.locator("dialog[open]").waitFor(); await page.keyboard.press("Escape");
    assert.equal(await page.locator("dialog[open]").count(), 0);
    assert(await menu.evaluate(node => node === document.activeElement));
    await page.locator(".connections-row-menu button").nth(1).click();
    await page.locator("dialog[open]").waitFor(); await page.keyboard.press("Escape");
    assert.equal(await page.locator("dialog[open]").count(), 0);
    assert(await menu.evaluate(node => node === document.activeElement));
    const add = page.getByRole("button", { name: language === "ko" ? "서버 추가" : "Add server" });
    await add.click();
        await page.locator("dialog[open]").waitFor();
    assert.equal(await page.locator('dialog[open] input[type="password"]').count(), 1);
    await page.keyboard.press("Escape");
    assert.equal(await page.locator("dialog[open]").count(), 0);
    assert(await add.evaluate(node => node === document.activeElement));
    assert.equal(await page.locator("html").getAttribute("data-actions"), null);
    checks++;
  }
  console.log(JSON.stringify({ operation: "connections_layout", result: "passed", checks, languages: 2, themes: 2, viewports: 6, effectiveZoom: [1,2], nativeAcceptance: "not-performed" }));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
