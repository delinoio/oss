// SPDX-License-Identifier: Apache-2.0
// Host-supplied Chromium validates the real sidebar using synthetic Connect
// inventory. This does not establish native CEF, account or platform acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const playwright = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(playwright ? pathToFileURL(resolve(playwright)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-session-actions-layout-"));
let browser, server, page, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`);
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
      response.setHeader("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'none'; base-uri 'none'");
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  page = await browser.newPage();
  page.on("pageerror", error => failures.push(error.message));
  await page.addInitScript(() => document.addEventListener("securitypolicyviolation", event => {
    if (event.violatedDirective.startsWith("style-src")) console.error("session_hover_style_csp_failure");
  }));
  page.on("console", message => { if (message.text() === "session_hover_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const rows = page.locator(".sidebar-session-row");
  const card = page.getByRole("tooltip");
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [960,640], [480,320]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?sessionActions=true&language=${language}&theme=${theme}`);
    const drawer = page.locator(".sidebar-context-trigger");
    if (await drawer.isVisible()) await drawer.click();
    await page.locator(".sidebar-project-row").filter({ hasText: "oss" }).click();
    await rows.first().waitFor();
    const titleWidths = await page.locator(".sidebar-session-title").evaluateAll(nodes => nodes.slice(0, 6).map(node => node.clientWidth));
    assert(titleWidths.every(width => width >= 48), "Names retain visible space beside title hints and actions");
    const states = await page.locator(".sidebar-session-row .sidebar-statuses").evaluateAll(nodes => nodes.slice(0, 6).map(node => ({ outcome: node.dataset.outcome, color: getComputedStyle(node.firstElementChild).color })));
    assert.deepEqual(states.map(state => state.outcome), ["not-started", "running", "succeeded", "failed", "stopped", "unknown"]);
    assert.equal(new Set(states.slice(0, 4).map(state => state.color)).size, 4);
    assert.equal(states[0].color, states[4].color);
    const selected = await page.locator('[aria-current="true"]').count();
    const more = page.locator(".sidebar-session-more").first();
    await more.focus(); await page.keyboard.press("Enter");
    const menu = page.getByRole("menu"); await menu.waitFor();
    assert.equal(await page.locator('[aria-current="true"]').count(), selected);
    await page.getByRole("menuitem").first().waitFor();
    await page.waitForFunction(() => document.querySelector(".sidebar-session-menu button[role=menuitem]")?.disabled === false);
    const bounds = await menu.boundingBox();
    assert(bounds.x >= 7 && bounds.y >= 7 && bounds.x + bounds.width <= width - 7 && bounds.y + bounds.height <= height - 7);
    const openerBounds = await more.boundingBox();
    assert(bounds.x >= openerBounds.x + openerBounds.width || bounds.x + bounds.width <= openerBounds.x || bounds.y >= openerBounds.y + openerBounds.height || bounds.y + bounds.height <= openerBounds.y, "Menu must not cover its opener");
    assert.equal(await card.count(), 0);
    await page.keyboard.press("ArrowDown");
    assert(await menu.evaluate(node => node.contains(document.activeElement)));
    await page.keyboard.press("Escape");
    assert(await more.evaluate(node => document.activeElement === node));
    assert.equal(await menu.count(), 0);
    checks += 1;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ checks, evidence: "Synthetic Chromium session status/action geometry and keyboard checks; no native CEF acceptance" }));

} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
