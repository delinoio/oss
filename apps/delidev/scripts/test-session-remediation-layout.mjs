// SPDX-License-Identifier: Apache-2.0
// Host-supplied Chromium validates session recovery using synthetic Connect
// observations. This does not establish native CEF, account or platform acceptance.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-session-remediation-layout-"));
let browser, server, page, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/session-remediation-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [960,640], [480,320]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    assert.deepEqual(await page.locator(".session-toolbar-actions button").allTextContents(), language === "en" ? ["Diff", "Files", "Terminals", "Browser", "Diagnostics"] : ["변경 사항", "파일", "터미널", "브라우저", "진단"]);
    assert.equal(await page.locator(".session-information").count(), 1, "Persistent inspector remains mounted without an Info action");
    // The localization is sourced from the current catalog, not fixed English.
    await page.locator(".session-notices .actions button").last().waitFor();
    assert.equal(await page.getByRole("textbox").first().inputValue(), "Retained draft");
    assert.equal(await page.locator(".session-tools").count(), 1);
    assert.equal(await page.locator(".session-controls > button").nth(1).isDisabled(), true);
    assert.equal(await page.evaluate(() => document.documentElement.dataset.fixtureWrites ?? "0"), "0");
    // Short Session layouts own bounded conversation scrolling. Verify that
    // workspace and input access rather than requiring all content before scrolling.
    assert(await page.locator(".session-workspace").evaluate(node => node.getBoundingClientRect().height <= innerHeight + 1), "Original Session workspace stays bounded");
    const composer = page.locator(".composer textarea"); await composer.focus();
    assert(await composer.evaluate(node => { const bounds = node.getBoundingClientRect(); return bounds.top >= 0 && bounds.bottom <= innerHeight + 1; }), "Original composer remains reachable through its scroll owner");
    const controls = page.locator(".session-notices .actions button");
    await controls.last().focus(); await page.keyboard.press("Enter");
    const confirm = page.locator(".session-tools .notice button").first();
    await confirm.waitFor();
    assert.equal(await page.evaluate(() => document.documentElement.dataset.fixtureWrites ?? "0"), "0");
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1);
    assert.equal(overflow, false, "Failure and confirmation stay within the viewport");
    assert.equal(await page.locator(".session-tools .notice").count(), 1);
    if (process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR && width === 480) await page.screenshot({ path: join(process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR, `session-remediation-${language}-${theme}-${width}.png`) });
    // Cancel retains its original details-summary focus owner, independently of toolbar actions.
    const recoverySummary = page.locator(".session-tools > summary");
    await page.locator(".session-tools .notice button").last().click();
    assert.equal(await page.getByRole("textbox").first().inputValue(), "Retained draft");
    await page.keyboard.press("Escape");
    assert.equal(await recoverySummary.evaluate(node => document.activeElement === node), true, "Cancellation retains the original recovery summary focus owner");
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ checks, evidence: "Synthetic Chromium inline session uncertainty, single recovery controller and bilingual narrow layout checks; no native CEF acceptance" }));

} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
