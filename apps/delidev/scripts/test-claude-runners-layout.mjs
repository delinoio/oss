// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence, with no native command, account write or external read.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-claude-runners-layout-"));
const screenshots = process.env.DELIDEV_CLAUDE_RUNNERS_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/claude-runners-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage(); page.setDefaultTimeout(8000); page.on("pageerror", error => failures.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[960,640], [640,480], [480,320]]) for (const scenario of ["failure", "mixed", "partial", "empty", "stale", "malformed"]) {
    const context = `${language}/${theme}/${width}x${height}/${scenario}`;
    console.log(JSON.stringify({ operation: "claude-runners-case", context }));
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?language=${language}&theme=${theme}&scenario=${scenario}`);
    await page.locator("[data-fixture-open]").click();
    const dialog = page.getByRole("dialog"), start = dialog.getByRole("button", { name: language === "ko" ? "로그인 시작" : "Start sign-in", exact: true });
    await dialog.locator('[role="combobox"]').waitFor();
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const read = dialog.getByRole("button", { name: language === "ko" ? "실행 장치 새로고침" : "Refresh Runner Devices", exact: true });
    const picker = dialog.getByRole("combobox");
    const inspect = async stage => {
      const geometry = await dialog.evaluate(node => {
      const box = node.getBoundingClientRect(), body = node.querySelector(".settings-task-body");
      const texts = [...node.querySelectorAll("p,h4,.problem,button")].filter(element => element.getClientRects().length && !element.closest("details:not([open])"));
      return { box: { x: box.x, y: box.y, width: box.width, height: box.height }, overflow: node.scrollWidth > node.clientWidth + 1 || body.scrollWidth > body.clientWidth + 1, clipped: texts.filter(element => element.scrollWidth > element.clientWidth + 2).map(element => element.tagName + ":" + element.textContent.slice(0, 70)) };
    });
      assert(geometry.box.x >= -1 && geometry.box.y >= -1 && geometry.box.width <= width + 1 && geometry.box.height <= height + 1 && !geometry.overflow && !geometry.clipped.length, `${context}: ${JSON.stringify(geometry)}`);
      if (screenshots && width !== 640) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `${scenario}-${language}-${theme}-${width}x${height}-${stage}.png`) }); }
    };
    await inspect("initial");
    // At the effective 200% viewport, diagnostics remain inside the original
    // bounded body. Capture both ends and verify keyboard-reachable controls.
    const diagnostic = dialog.locator(".problem").first();
    if (width === 480 && await diagnostic.count()) {
      await diagnostic.scrollIntoViewIfNeeded();
      await inspect("diagnostic-top");
      const diagnosticEnd = diagnostic.locator("p,button").last();
      if (await diagnosticEnd.count()) {
        await diagnosticEnd.scrollIntoViewIfNeeded();
        const reachable = await diagnosticEnd.evaluate(element => {
          const body = element.closest(".settings-task-body").getBoundingClientRect();
          const box = element.getBoundingClientRect();
          return box.bottom > body.top && box.top < body.bottom;
        });
        assert(reachable, `${context}: diagnostic end remains reachable through bounded scrolling`);
        await inspect("diagnostic-end");
      }
    }
    if (scenario === "failure") {
      await dialog.locator(".problem").waitFor();
      assert((await dialog.textContent()).includes("0195c9c0-7b13-7000-8000-000000000001"), `${context}: opaque correlation`);
      assert(await start.isDisabled(), context);
      await read.focus(); await read.press("Enter");
      await page.waitForFunction(() => window.__claudeRunnersFixture.list === 2);
      await picker.focus(); await picker.press("ArrowDown"); await picker.press("Enter");
      await page.waitForFunction(() => document.querySelector('[role="combobox"]').getAttribute("data-value") !== "");
    } else if (scenario === "partial") {
      await page.waitForFunction(() => window.__claudeRunnersFixture.list === 1);
      assert(await start.isDisabled(), context);
      const more = dialog.getByRole("button", { name: language === "ko" ? "더 불러오기" : "Load more", exact: true });
      await more.focus(); await more.press("Enter");
      await page.waitForFunction(() => window.__claudeRunnersFixture.list === 2);
      await picker.focus(); await picker.press("ArrowDown"); await picker.press("Enter");
      await page.waitForFunction(() => document.querySelector('[role="combobox"]').getAttribute("data-value") !== "");
    } else if (scenario === "mixed" || scenario === "stale") {
      await page.waitForFunction(() => window.__claudeRunnersFixture.list === 1);
      await picker.focus(); await picker.press("ArrowDown");
      if (scenario === "mixed") {
        const options = dialog.getByRole("option");  await options.first().waitFor();
        assert.equal(await options.count(), 11, `${context}: all observed choices`);
        assert.equal(await dialog.locator("[role=option]:disabled").count(), 10, `${context}: excluded choices stay disabled`);
        await picker.press("End");
        const highlighted = await picker.getAttribute("aria-activedescendant");
        assert(highlighted?.endsWith("option-0"), `${context}: keyboard skips excluded choices`);
      }
      await picker.press("Enter");
      await page.waitForFunction(() => document.querySelector('[role="combobox"]').getAttribute("data-value") !== "");
      if (scenario === "stale") {
        const selected = await picker.getAttribute("data-value");
        await read.click(); await dialog.locator(".problem").waitFor();
        assert(await start.isDisabled(), `${context}: failed refresh blocks admission`);
        await inspect("stale");
        assert.equal(await picker.getAttribute("data-value"), selected, `${context}: selection retained`);
        await read.focus(); await read.press("Enter");
        assert(await start.isDisabled(), `${context}: pending retry grants no readiness`);
        await page.waitForFunction(() => window.__claudeRunnersFixture.list === 3);
        await page.waitForTimeout(220);
        assert.equal(await picker.getAttribute("data-value"), selected, `${context}: retry preserves selection`);
      }
    } else {
      await page.waitForFunction(() => window.__claudeRunnersFixture.list === 1);
      assert(await start.isDisabled(), context);
      if (scenario === "malformed") {
        await picker.focus(); await picker.press("ArrowDown");
        const option = dialog.getByRole("option"); await option.waitFor(); assert(await option.isDisabled(), context);
        await picker.press("Enter"); assert.equal(await picker.getAttribute("data-value"), "", `${context}: malformed selection blocked`);
        await picker.press("Escape");
      }
    }
    if (["failure", "mixed", "partial", "stale"].includes(scenario)) { await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))); assert(await start.isEnabled(), `${context}: current eligible Runner is usable`); }
    assert(!(await dialog.textContent()).includes("native-secret"), `${context}: native content stays private`);
    await inspect("final");
    const counters = await page.evaluate(() => window.__claudeRunnersFixture);
    assert.equal(counters.save, 0, context); assert.equal(counters.login, 0, context); assert.equal(counters.native, 0, context);
    const close = dialog.locator(".settings-task-close"); await close.focus(); await close.press("Enter");
    await dialog.waitFor({ state: "detached" });
    assert.equal(await page.locator("[data-fixture-open]").evaluate(node => document.activeElement === node), true, `${context}: return focus`);
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ operation: "claude-runners-browser", source, checks, result: "passed", evidence: "synthetic original-controller Chrome browser; effective480x320 viewport is200%zoom equivalent only; no packaged/native/OS/account acceptance" }));
} finally { await browser?.close(); await new Promise(done => server ? server.close(done) : done()); await rm(directory, { recursive: true, force: true }); }
