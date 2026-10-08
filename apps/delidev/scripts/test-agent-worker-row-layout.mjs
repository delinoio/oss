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
const directory = await mkdtemp(join(tmpdir(), "delidev-worker-rows-"));
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/agent-worker-row-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [960,640], [640,480], [720,450], [480,320]]) {
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    await page.locator(".agent-model-native").first().waitFor();
    assert.equal(await page.locator("html").getAttribute("data-model-reads"), "1");
    assert.equal(await page.locator(".worker-harness-mark").count(), 4);
    const geometry = await page.locator(".settings-agent-row").evaluateAll(rows => rows.map(row => ({ width: row.clientWidth, overflow: row.scrollWidth > row.clientWidth + 1, mark: [...row.querySelectorAll(".worker-harness-mark")].map(mark => ({ width: mark.getBoundingClientRect().width, image: getComputedStyle(mark).backgroundImage, hidden: mark.getAttribute("aria-hidden") })), buttons: [...row.querySelectorAll("button")].map(button => ({ height: button.getBoundingClientRect().height, radius: getComputedStyle(button).borderRadius })) })));
    for (const row of geometry) { assert(!row.overflow, JSON.stringify(row)); for (const mark of row.mark) { assert.equal(mark.width, 32); assert.notEqual(mark.image, "none"); assert.equal(mark.hidden, "true"); } for (const button of row.buttons) { assert(button.height >= 40); assert.equal(button.radius, "8px"); } }
    const toggle = page.getByRole("button", { name: language === "ko" ? "+2개 더 보기" : "+2 more" });
    await page.keyboard.press("Tab"); await toggle.focus(); assert(await toggle.evaluate(node => node.matches(":focus-visible") && parseFloat(getComputedStyle(node).outlineWidth) > 0));
    await toggle.press("Enter"); assert.equal(await toggle.getAttribute("aria-expanded"), "true");
    await page.waitForFunction(() => document.documentElement.dataset.modelReads === "2");
    const native = await page.locator(".agent-model-routes code").allTextContents(); assert.equal(native.length, 3); assert.equal(native[0], native[2]); assert.equal(native[1], "second-native");
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth));
    await toggle.press("Space"); assert.equal(await toggle.getAttribute("aria-expanded"), "false"); assert.equal(await page.locator("html").getAttribute("data-model-reads"), "2"); checks++;
  }
  console.log(JSON.stringify({ operation: "agent_worker_rows_layout", result: "passed", checks, languages: 2, themes: 2, viewports: 5, effectiveZoom: [1,2], nativeAcceptance: "not-performed" }));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
