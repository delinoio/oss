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
const directory = await mkdtemp(join(tmpdir(), "delidev-service-tier-"));
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/service-tier-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[960,640], [480,320], [320,480]]) {
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    const selector = page.getByRole("combobox"); await selector.waitFor();
    assert.equal(await selector.inputValue(), "default");
    await page.keyboard.press("Tab"); assert(await selector.evaluate(node => node === document.activeElement)); await selector.selectOption("fast");
    assert.equal(await selector.inputValue(), "fast"); assert.equal(await page.locator("output").textContent(), "fast");
    await selector.selectOption("custom"); await page.getByRole("textbox").fill(" exact tier ");
    assert.equal(await page.locator("output").textContent(), " exact tier ");
    const geometry = await page.locator("select,input,a").evaluateAll(nodes => nodes.map(node => ({ left: node.getBoundingClientRect().left, right: node.getBoundingClientRect().right, width: node.getBoundingClientRect().width, described: node.tagName !== "SELECT" || Boolean(document.getElementById(node.getAttribute("aria-describedby"))) })));
    for (const node of geometry) { assert(node.left >= 0 && node.right <= width + 1 && node.width > 0, JSON.stringify(node)); assert(node.described); }
    assert(await page.locator("body").evaluate(node => node.scrollWidth <= innerWidth));
    assert.equal(await page.getByRole("link").getAttribute("href"), "https://learn.chatgpt.com/docs/agent-configuration/speed");
    await selector.selectOption("default"); assert.equal(await page.locator("output").textContent(), "omitted"); checks++;
  }
  console.log(JSON.stringify({ operation: "service_tier_layout", result: "passed", checks, languages: 2, themes: 2, widths: [960,480,320], equivalentReflowOnly: true, nativeAcceptance: "not-performed" }));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
