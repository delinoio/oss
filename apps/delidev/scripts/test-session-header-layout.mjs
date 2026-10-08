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
const directory = await mkdtemp(join(tmpdir(), "delidev-session-header-layout-"));
let browser, server, page, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/session-header-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, dataUriLimit: 0, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`);
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
      response.setHeader("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'none'; base-uri 'none'");
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".webp": "image/webp" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  page = await browser.newPage();
  page.on("pageerror", error => failures.push(error.message));
  await page.addInitScript(() => document.addEventListener("securitypolicyviolation", event => {
    if (event.violatedDirective.startsWith("style-src")) console.error("session_header_style_csp_failure");
  }));
  page.on("console", message => { if (message.text() === "session_header_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark", "system"]) for (const [width, height] of [[1440, 900], [520, 640], [960, 320], [480, 320]]) for (const [harness, name] of [["codex", "Codex"], ["claude-code", "Claude Code"], ["opencode", "OpenCode"], ["grok-build", "Grok Build"], ["unknown", language === "ko" ? "하네스 사용 불가" : "Harness unavailable"]]) {
    await page.setViewportSize({ width, height });
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto(`${origin}/?language=${language}&theme=${theme}&harness=${harness}`);
    await page.locator(".session-harness-name").filter({ hasText: name }).waitFor();
    const mark = page.locator(".session-harness-mark");
    assert.equal(await mark.count(), harness === "unknown" ? 0 : 1);
    if (harness !== "unknown") {
      const image = await mark.evaluate(node => ({ width: node.clientWidth, height: node.clientHeight, image: getComputedStyle(node).backgroundImage, hidden: node.getAttribute("aria-hidden"), focus: node.hasAttribute("tabindex") }));
      assert.equal(image.width, 32); assert.equal(image.height, 32); assert.equal(image.hidden, "true"); assert.equal(image.focus, false);
      const variant = theme === "light" ? "light" : "dark";
      assert(image.image.includes(harness === "opencode" ? `opencode-${variant}` : harness === "grok-build" ? `grok-${variant}` : harness));
      assert(await mark.evaluate(async node => { const image = new Image(); image.src = getComputedStyle(node).backgroundImage.slice(5, -2); await image.decode(); return image.naturalWidth > 0 && image.naturalHeight > 0; }), "Licensed local marks decode successfully");
    }
    const geometry = await page.evaluate(() => {
      const header = document.querySelector(".session-header"), composer = document.querySelector(".composer"), controls = document.querySelector(".session-controls"), heading = document.querySelector(".session-heading");
      return { header: header.getBoundingClientRect().toJSON(), composer: composer.getBoundingClientRect().toJSON(), controls: controls.getBoundingClientRect().toJSON(), heading: heading.getBoundingClientRect().toJSON(), overflow: header.scrollWidth > header.clientWidth, buttons: [...controls.querySelectorAll("button")].map(button => button.getBoundingClientRect().height).filter(height => height > 0) };
    });
    assert.equal(geometry.overflow, false); assert(geometry.header.bottom <= geometry.composer.top); if (geometry.composer.bottom > height + 1) assert.equal(await page.locator(".session-conversation-region").evaluate(node => getComputedStyle(node).overflowY), "auto", "Short layouts retain the original bounded conversation scroll owner");
    assert(geometry.buttons.every(value => value >= 40));
    if (width <= 520) assert(geometry.controls.top >= geometry.heading.bottom);
    assert(await page.locator(".session-title-status").textContent());
    if (process.env.DELIDEV_HEADER_SCREENSHOTS && harness === "codex") await page.screenshot({ path: join(process.env.DELIDEV_HEADER_SCREENSHOTS, `${language}-${theme}-${width}x${height}.png`) });
    const more = page.locator(".session-actions-popup > button");
    await more.focus(); await page.keyboard.press("Enter");
    assert(await page.locator(".session-actions-list").evaluate(node => node.contains(document.activeElement)));
    await page.keyboard.press("Escape");
    assert(await more.evaluate(node => node === document.activeElement));
    assert.equal(await more.getAttribute("aria-expanded"), "false");
    const input = page.locator(".composer textarea"); await input.fill("Retained draft");
    await page.locator(".plan-mode input").check();
    await page.evaluate(() => { window.__sessionHeaderFixture.input = document.querySelector(".composer textarea"); window.__sessionHeaderFixture.info = document.querySelector(".session-information"); window.__sessionHeaderFixture.before = { ...window.__sessionHeaderFixture.counts }; });
    await page.evaluate(async language => { document.documentElement.dataset.theme = "dark"; await window.__sessionHeaderFixture.language(language === "en" ? "ko" : "en"); }, language);
    assert.equal(await input.inputValue(), "Retained draft"); assert(await page.locator(".plan-mode input").isChecked());
    assert(await page.evaluate(() => window.__sessionHeaderFixture.input === document.querySelector(".composer textarea") && window.__sessionHeaderFixture.info === document.querySelector(".session-information")));
    assert.deepEqual(await page.evaluate(() => window.__sessionHeaderFixture.counts), await page.evaluate(() => window.__sessionHeaderFixture.before));
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ checks, result: "passed", effectiveZoom: "200% at 480x320", evidence: "Synthetic Chromium full Session workspace; native/account/platform acceptance not performed" }));

} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
