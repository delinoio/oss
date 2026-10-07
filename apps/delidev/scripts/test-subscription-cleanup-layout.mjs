// SPDX-License-Identifier: Apache-2.0
// Explicit browser fixture QA; the validation host supplies Playwright.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, realpath, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const checkout = await realpath(resolve(app, "../.."));
const directory = await mkdtemp(join(tmpdir(), "delidev-cleanup-layout-"));
let screenshots;
if (process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR) {
  // Resolve the existing parent before creating a screenshot directory, so a
  // symlink cannot redirect validation output into repository-owned assets.
  const requested = resolve(process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR);
  const parent = await realpath(dirname(requested));
  screenshots = resolve(parent, requested.slice(dirname(requested).length + 1));
  const path = relative(checkout, screenshots);
  assert(path && (path === ".." || path.startsWith(`..${sep}`) || isAbsolute(path)), "Screenshots must remain outside checkout");
  await mkdir(screenshots, { recursive: true });
  const actual = await realpath(screenshots), distance = relative(checkout, actual);
  assert(distance && (distance === ".." || distance.startsWith(`..${sep}`) || isAbsolute(distance)), "Screenshot symlink points into checkout");
}
const { chromium } = await import(process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE ? pathToFileURL(resolve(process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE)).href : "playwright");
let server, browser;
let checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const path = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${path === "/" ? "/index.html" : path}`);
      assert(file.startsWith(`${directory}${sep}`));
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage();
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440, 900], [640, 480]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&language=${language}&subscriptionBackground=true&cleanupFixture=true`);
    await page.getByRole("button", { name: language === "ko" ? "설정" : "Settings", exact: true }).click();
    const cleanup = page.getByRole("button", { name: language === "ko" ? "자동 정리" : "Auto cleanup", exact: true });
    await cleanup.waitFor(); await page.waitForFunction(() => !document.querySelector(".subscription-header-actions button")?.disabled);
    const refresh = page.locator(".subscription-header-actions button").nth(1);
    const box = await cleanup.boundingBox(), refreshBox = await refresh.boundingBox();
    assert.equal(box.height, 40);
    if (box.y === refreshBox.y) assert(Math.abs(refreshBox.x - box.x - box.width - 8) <= 1, "Header action gap");
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), "Narrow content overflow");
    await cleanup.focus(); await cleanup.press("Enter");
    const busy = page.getByRole("button", { name: language === "ko" ? "정리 중…" : "Cleaning up…", exact: true });
    await busy.waitFor(); assert(await busy.isDisabled());
    await page.locator(".subscription-cleanup-status").filter({ hasText: language === "ko" ? "3개 삭제 · 2개 유지" : "3 deleted · 2 retained" }).waitFor();
    assert(await cleanup.evaluate(node => document.activeElement === node), "Completion moved focus");
    const summary = page.locator(".subscription-cleanup-results summary");
    await summary.focus(); await summary.press("Enter");
    assert(await page.locator(".subscription-cleanup-results").evaluate(node => node.open));
    assert(await page.locator(".subscription-cleanup-results li").count() === 2);
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), "Result overflow");
    if (screenshots) await page.screenshot({ path: join(screenshots, `cleanup-${language}-${theme}-${width}x${height}.png`) });
    checks++;
  }
  console.log(JSON.stringify({ operation: "subscription_cleanup_layout", result: "passed", checks, languages: 2, themes: 2, viewports: 2, keyboard: "Enter activation/disclosure, focus preserved", nativeAcceptance: "not-performed", accountAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
