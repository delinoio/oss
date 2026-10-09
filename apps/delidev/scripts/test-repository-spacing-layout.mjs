// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-repository-spacing-"));
let browser, server, page;
let cases = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/scroll-pagination-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  page = await browser.newPage();
  const errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;

  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height, zoom] of [[1440, 900, 1], [960, 640, 1], [600, 800, 1], [480, 320, 1]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?repositories=1&language=${language}&theme=${theme}&zoom=${zoom}`);
    await page.getByRole("button", { name: "Fixture Load", exact: true }).click();
    const root = page.locator("[data-repository-scroll]");
    const cards = root.locator(".repository-row");
    await cards.nth(2).waitFor();
    const geometry = async () => root.evaluate(node => {
      const scale = document.body.style.zoom === "2" ? 2 : 1;
      const rows = [...node.querySelectorAll(".repository-row")];
      return { gaps: rows.slice(1).map((row, index) => (row.getBoundingClientRect().top - rows[index].getBoundingClientRect().bottom) / scale), overflow: node.scrollWidth > node.clientWidth, pages: [...node.querySelectorAll("[data-payload-page]")].filter(page => page.querySelector(".repository-row")).map(page => ({ token: page.dataset.payloadPage, height: page.getBoundingClientRect().height, sum: [...page.querySelectorAll(".repository-row")].reduce((sum, row) => sum + row.getBoundingClientRect().height, 0) + 32 * scale })), padding: getComputedStyle(rows[0]).padding };
    });
    const first = await geometry();
    assert(first.gaps.every(gap => Math.abs(gap - 16) < .1), JSON.stringify(first));
    assert.equal(first.padding, "16px");
    assert(!first.overflow);
    assert(first.pages.every(item => Math.abs(item.height - item.sum) < .1), "Wrapper measures both internal gaps");
    await page.getByRole("button", { name: "Fixture Next", exact: true }).click();
    await cards.nth(5).waitFor();
    const two = await geometry();
    assert(two.gaps.every(gap => Math.abs(gap - 16) < .1), "Both within-page and cross-page gaps are exactly 16 CSS pixels");
    assert.equal(await page.locator('.settings-projects [data-payload-page]').evaluate(node => getComputedStyle(node).display), "block", "Repository rule cannot change other categories");
    const firstHeight = first.pages[0].height;
    // Reach enough actual pagination pages to evict the original payload.
    for (let index = 2; index <= 4; index++) {
      await root.evaluate(node => { node.scrollTop = node.scrollHeight; });
      await page.getByRole("button", { name: "Fixture Next", exact: true }).click();
      await page.waitForFunction(count => JSON.parse(document.querySelector('[data-repository-state]').textContent).pages.length === count, index + 1);
    }
    await page.waitForFunction(() => !document.querySelector('[data-payload-page=""] .repository-row'));
    const placeholder = root.locator('[data-payload-page=""]');
    assert(Math.abs((await placeholder.boundingBox()).height - firstHeight) < 1, `Evicted wrapper retains measured card heights and gaps: ${JSON.stringify({ language, theme, width, height, firstHeight, placeholder: await placeholder.boundingBox() })}`);
    const focused = root.locator('[data-payload-page="4"] .repository-row').first().getByRole("button").first();
    await focused.focus();
    await root.evaluate(node => { node.scrollTop = 0; });
    await root.locator('[data-payload-page=""] .repository-row').first().waitFor();
    assert(await focused.evaluate(node => node.isConnected && node === document.activeElement), "Restoration retains the connected focused action and its protected payload");
    assert(Math.abs(await root.evaluate(node => node.scrollTop)) < 1, "Restoration retains the top scroll anchor");
    const restored = await geometry();
    assert(!restored.overflow);
    assert(restored.pages.every(item => Math.abs(item.height - item.sum) < .1), "Restored wrapper includes internal gaps");
    assert.equal(await root.locator('[data-payload-page="4"] .repository-row').last().locator("button:disabled").count(), 2, "Unsupported schema actions stay disabled");
    cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: "repository-spacing-layout", cases, nativeAcceptance: "not-performed", accountAcceptance: "not-performed", zoom: "equivalent-200-percent-reflow-viewport" }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
