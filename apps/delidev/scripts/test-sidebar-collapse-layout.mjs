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
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/pull-requests-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const width of [1440, 960, 760]) {
    await page.setViewportSize({ width, height: width === 1440 ? 900 : 640 });
    await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    const primary = await page.evaluate(() => /mac/i.test(navigator.platform) ? "Meta" : "Control");
    const toggle = page.locator(".sidebar-wide-toggle"), pane = page.locator(".sidebar-pane-dialog"), rail = page.locator(".sidebar-rail");
    await toggle.waitFor(); await page.waitForFunction(() => document.querySelector(".sidebar-wide-toggle")?.getAttribute("aria-disabled") === "false");
    const originalWidth = width > 1100 ? 288 : 256;
    assert.equal(Math.round((await pane.boundingBox()).width), originalWidth);
    const paneHandle = await pane.elementHandle();
    await toggle.click(); await page.waitForFunction(() => document.querySelector(".sidebar-pane-dialog").hidden);
    assert.equal(Math.round((await rail.boundingBox()).width), 52);
    assert.equal(Math.round((await page.locator("#main").boundingBox()).x), 53);
    assert.equal(await pane.evaluate(node => node.inert), true);
    await page.keyboard.press(`${primary}+b`); await page.waitForFunction(() => !document.querySelector(".sidebar-pane-dialog").hidden);
    assert.equal(Math.round((await pane.boundingBox()).width), originalWidth);
    assert.equal(await pane.evaluate((node, retained) => node === retained, paneHandle), true);
    // A focused pane control must return to the persistent toggle before hiding.
    const control = pane.locator("button").first(); await control.focus(); await page.keyboard.press(`${primary}+b`);
    await page.waitForFunction(() => document.querySelector(".sidebar-pane-dialog").hidden);
    assert.equal(await toggle.evaluate(node => node === document.activeElement), true);
    // Compact opening is independent of the committed wide choice.
    await page.setViewportSize({ width: 759, height: 640 });
    assert.equal(await toggle.isVisible(), false);
    await page.locator(".sidebar-context-trigger").click();
    assert.equal(await pane.evaluate(node => node.matches(":modal")), true);
    await page.keyboard.press(`${primary}+b`); assert.equal(await pane.evaluate(node => node.matches(":modal")), true);
    await page.keyboard.press("Escape"); await page.setViewportSize({ width, height: 640 });
    await page.waitForFunction(() => document.querySelector(".sidebar-pane-dialog").hidden);
    assert.equal(await toggle.getAttribute("aria-expanded"), "false");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    // Supporting-surface portals retain selected resources and authoring state.
    await page.getByRole("button", { name: language === "ko" ? "풀 리퀘스트" : "Pull requests", exact: true }).click();
    assert.equal(await pane.evaluate(node => node.hidden), true);
    await toggle.click(); await page.waitForFunction(() => !document.querySelector(".sidebar-pane-dialog").hidden);
    const repository = pane.locator(".sidebar-repository-row").first(); await repository.click();
    const draft = pane.locator(".pr-search-input input"); await draft.fill("Retained query draft");
    const draftHandle = await draft.elementHandle();
    await draft.evaluate(node => node.setSelectionRange(2, 8));
    await toggle.focus();
    const scroller = page.locator(".sidebar-list");
    await scroller.evaluate(node => { node.style.maxHeight = "140px"; node.scrollTop = 90; });
    const retainedScroll = await scroller.evaluate(node => node.scrollTop);
    assert.ok(retainedScroll > 0, "supporting surface has a measurable retained scroll owner");
    await page.keyboard.press(`${primary}+b`); await page.waitForFunction(() => document.querySelector(".sidebar-pane-dialog").hidden);
    assert.equal(await toggle.evaluate(node => node === document.activeElement), true);
    await page.getByRole("button", { name: language === "ko" ? "세션" : "Sessions", exact: true }).click();
    await page.getByRole("button", { name: language === "ko" ? "풀 리퀘스트" : "Pull requests", exact: true }).click();
    await toggle.click(); await page.waitForFunction(() => !document.querySelector(".sidebar-pane-dialog").hidden);
    assert.equal(await scroller.evaluate(node => node.scrollTop), retainedScroll, "hidden navigation cannot overwrite the original scroll snapshot");
    assert.equal(await draft.inputValue(), "Retained query draft");
    assert.equal(await draft.evaluate((node, original) => node === original, draftHandle), true);
    assert.equal(await repository.getAttribute("aria-pressed"), "true");
    assert.equal(await page.evaluate(() => window.__prSidebarFixture.github), 0);
    checks++;
  }
  // Effective 200% reflow uses half the physical viewport, without claiming CEF zoom.
  for (const language of ["en", "ko"]) for (const width of [720, 480, 320]) {
    await page.setViewportSize({ width, height: 450 }); await page.goto(`${origin}/?language=${language}`);
    assert.equal(await page.locator(".sidebar-wide-toggle").isVisible(), false);
    await page.locator(".sidebar-context-trigger").click();
    assert.equal(await page.locator(".sidebar-pane-dialog").evaluate(node => node.matches(":modal")), true);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true); checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ operation: "sidebar-collapse-layout", source, checks, result: "passed", evidence: "synthetic Chromium geometry and effective reflow; no screenshots or installed CEF acceptance" }));
} finally {
  await browser?.close();
  await new Promise(done => server ? server.close(done) : done());
  await rm(directory, { recursive: true, force: true });
}
