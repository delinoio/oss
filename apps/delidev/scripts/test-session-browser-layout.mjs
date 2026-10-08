// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-browser-layout-"));
const screenshots = process.env.DELIDEV_BROWSER_SCREENSHOT_DIR;
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/session-browser-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const sizes = [{ width: 1904, height: 1000, conversation: 1440 }, { width: 1424, height: 700, conversation: 960 }, { width: 1423, height: 700, conversation: 959 }, { width: 560, height: 640, conversation: 480 }, { width: 1904, height: 480, conversation: 1440 }, { width: 1120, height: 960, conversation: 480, zoom: 2 }];
  const labels = language => language === "en" ? { browser: "Browser", address: "Address", open: "Open account browser", expand: "Expand", restore: "Restore", information: "Browser information", closeInfo: "Close Browser information", close: "Close browser", splitter: "Browser width", files: "Files" } : { browser: "브라우저", address: "주소", open: "계정 브라우저 열기", expand: "확장", restore: "복원", information: "브라우저 정보", closeInfo: "브라우저 정보 닫기", close: "브라우저 닫기", splitter: "브라우저 너비", files: "파일" };
  const evidence = () => page.evaluate(() => ({ registrations: browserFixture.registrations.length, opens: browserFixture.native.filter(call => call.operation === "open_browser").length, hides: browserFixture.native.filter(call => call.action === "hide").length, actions: browserFixture.native.filter(call => call.action && !["resize", "hide"].includes(call.action)).length }));
  const geometry = () => page.evaluate(() => {
    const box = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height }; };
    const scale = parseFloat(getComputedStyle(document.body).zoom) || 1;
    const splitter = document.querySelector(".browser-splitter");
    return { scale, region: box(".session-conversation-region"), panel: box(".session-app-panel"), info: box(".session-information"), composer: box(".composer"), tray: box(".session-input-tray"), workspace: box(".session-workspace"), transcript: box(".transcript"), splitter: splitter ? { value: Number(splitter.getAttribute("aria-valuenow")), minimum: Number(splitter.getAttribute("aria-valuemin")), maximum: Number(splitter.getAttribute("aria-valuemax")) } : null, viewport: document.querySelector(".browser-viewport") ? box(".browser-viewport") : null };
  });
  const assertGeometry = async () => {
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const m = await geometry();
    const intersects = (a, b) => a.left < b.right - 1 && a.right > b.left + 1 && a.top < b.bottom - 1 && a.bottom > b.top + 1;
    for (const owner of [m.info, m.composer, m.tray]) assert.equal(intersects(m.panel, owner), false, `Browser excludes Info/composer/tray ${JSON.stringify(m)}`);
    if (m.region.width / m.scale >= 960) {
      assert(m.splitter, "Wide splitter is present");
      assert(m.panel.width / m.scale >= 480 - 1); assert(m.transcript.width / m.scale >= 360 - 1);
      assert(Math.abs(m.panel.width / m.scale - m.splitter.value) <= 1, "Accessible width matches real pane");
    } else { assert.equal(m.splitter, null); assert(m.panel.width / m.scale <= 400 + 1); }
    assert(m.composer.bottom <= m.workspace.bottom + 1, "Composer remains bounded");
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No horizontal page overflow");
    if (m.viewport) {
      assert(m.viewport.height > 0, "Remaining viewport height is positive");
      await page.waitForFunction(() => { const r = document.querySelector(".browser-viewport").getBoundingClientRect(), b = browserFixture.lastBounds; return b && Math.abs(b.x - r.left) < 2 && Math.abs(b.y - r.top) < 2 && Math.abs(b.width - r.width) < 2 && Math.abs(b.height - r.height) < 2; });
      assert.equal(await page.locator(".browser-viewport iframe,.browser-viewport webview,.browser-viewport > *").count(), 0, "Native content stays outside DOM");
    }
    return m;
  };
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark", "system"]) for (const size of sizes) {
    const l = labels(language);
    await page.emulateMedia({ colorScheme: "dark" }); await page.setViewportSize(size);
    process.stdout.write(JSON.stringify({ operation: "browser-split-case", language, theme, ...size }) + "\n");
    await page.goto(`${origin}/?language=${language}&theme=${theme}&zoom=${size.zoom ?? 1}`);
    await page.getByRole("heading", { name: "Synthetic Browser session" }).waitFor();
    const composer = page.locator(".composer textarea");
    await composer.evaluate(node => { globalThis.originalComposer = node; });
    await page.getByRole("button", { name: l.browser, exact: true }).click();
    const opening = page.getByRole("button", { name: l.open, exact: true });
    await opening.waitFor(); assert.equal(await page.locator(".browser-viewport").count(), 0, "Initial card has no empty viewport");
    for (const invalid of ["file:///synthetic", "https://user:password@example.com/"]) { await page.getByRole("textbox", { name: l.address }).fill(invalid); assert(await opening.isDisabled(), "Invalid initial URL cannot register"); }
    await page.getByRole("textbox", { name: l.address }).fill("https://example.com/");
    await page.waitForFunction(() => !document.querySelector(".browser-opening button").disabled);
    let m = await assertGeometry();
    assert(Math.abs(m.region.width / m.scale - size.conversation) <= 1, "Threshold uses conversation width excluding Info");
    if (m.splitter) assert(Math.abs(m.panel.width / m.scale - (size.conversation - 8) * .55) <= 1, "Default is 55 percent after splitter");
    assert.equal((await evidence()).registrations, 0);
    await opening.click(); await page.locator(".browser-tabs li").first().waitFor(); await assertGeometry();
    assert.equal((await evidence()).registrations, 1); assert.equal(await page.locator(".browser-tabs li").count(), 2);
    if (screenshots && language === "en" && theme === "light" && size.width === 1904 && size.height === 1000) { await mkdir(screenshots, { recursive: true }); await page.screenshot({ path: join(screenshots, "browser-open-wide.png") }); }
    const full = await page.locator(".browser-tabs li").last().locator("button").first().getAttribute("title");
    assert(full.length > 300); assert((await page.locator(".browser-tabs li").last().locator("button").first().getAttribute("aria-label")).includes(full), "Complete local URL stays accessible");
    if (m.splitter) {
      const splitter = page.getByRole("separator", { name: l.splitter });
      await splitter.focus(); await page.keyboard.press("Home"); assert.equal((await geometry()).splitter.value, 480);
      await page.keyboard.press("ArrowLeft"); assert.equal((await geometry()).splitter.value, 496);
      await page.keyboard.press("ArrowRight"); assert.equal((await geometry()).splitter.value, 480);
      await page.keyboard.press("End"); assert.equal((await geometry()).splitter.value, size.conversation - 368);
      const r = await splitter.boundingBox(); await page.mouse.move(r.x + r.width / 2, r.y + Math.min(100, r.height / 2)); await page.mouse.down(); await page.mouse.move((await geometry()).region.right + 100, r.y + 50); await page.mouse.up(); assert.equal((await geometry()).splitter.value, 480);
      const r2 = await splitter.boundingBox(); await page.mouse.move(r2.x + 4, r2.y + 50); await page.mouse.down(); await page.mouse.move((await geometry()).region.left - 100, r2.y + 50); await page.mouse.up(); assert.equal((await geometry()).splitter.value, size.conversation - 368);
      await splitter.focus(); await page.keyboard.press("Home"); await page.keyboard.press("ArrowLeft");
      await page.getByRole("button", { name: l.expand, exact: true }).click(); assert.equal((await geometry()).splitter.value, size.conversation - 368);
      await page.setViewportSize({ width: 1424, height: size.height }); await assertGeometry();
      await page.getByRole("button", { name: l.restore, exact: true }).click(); assert.equal((await geometry()).splitter.value, 496);
      await page.setViewportSize({ width: 1423, height: size.height }); await assertGeometry(); assert(await page.getByRole("button", { name: l.expand, exact: true }).isDisabled());
      await page.setViewportSize(size); await assertGeometry(); assert.equal((await geometry()).splitter.value, 496);
    } else assert(await page.getByRole("button", { name: l.expand, exact: true }).isDisabled());
    assert.equal((await evidence()).actions, 0, "Layout sends no native navigation"); assert.equal((await evidence()).registrations, 1);
    const before = await evidence(); await page.getByRole("button", { name: l.information, exact: true }).click(); await page.getByRole("dialog", { name: l.information }).waitFor(); await page.waitForFunction(n => browserFixture.native.filter(call => call.action === "hide").length > n, before.hides);
    await page.getByRole("button", { name: l.closeInfo, exact: true }).click(); await page.waitForFunction(n => browserFixture.native.filter(call => call.operation === "open_browser").length > n, before.opens); await assertGeometry();
    assert.equal((await evidence()).registrations, 1); assert.equal((await evidence()).actions, 0);
    await page.getByRole("button", { name: l.close, exact: true }).click(); assert(await page.getByRole("button", { name: l.browser, exact: true }).evaluate(node => node === document.activeElement));
    assert(await composer.evaluate(node => node === originalComposer)); assert.equal(await composer.inputValue(), "Retained synthetic Browser draft");
    await page.getByRole("button", { name: l.browser, exact: true }).click(); await opening.waitFor();
    if (m.splitter) assert.equal((await geometry()).splitter.value, 496, "Split survives Browser unmount/tool reopening");
    await page.getByRole("button", { name: l.files, exact: true }).click(); await page.keyboard.press("Escape");
    assert(await page.getByRole("button", { name: l.files, exact: true }).evaluate(node => node === document.activeElement)); assert(await composer.evaluate(node => node === originalComposer));
    cases++;
  }
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const state of ["host=unsupported", "capabilities=loading", "capabilities=unsupported", "capabilities=failure", "registration=uncertain", "native=failure", "removal=pending", "tabs=16"]) {
    const l = labels(language); await page.setViewportSize({ width: 1424, height: 640 });
    process.stdout.write(JSON.stringify({ operation: "browser-state-case", language, theme, state }) + "\n");
    await page.goto(`${origin}/?language=${language}&theme=${theme}&${state}`); await page.getByRole("heading", { name: "Synthetic Browser session" }).waitFor(); await page.getByRole("button", { name: l.browser, exact: true }).click();
    if (state === "host=unsupported") { assert.equal(await page.locator(".browser-opening").count(), 0); assert.equal((await evidence()).registrations, 0); assert.equal((await evidence()).opens, 0); cases++; continue; }
    const opening = page.getByRole("button", { name: l.open, exact: true }); await opening.waitFor(); await page.getByRole("textbox", { name: l.address }).fill("https://example.com/");
    if (state.startsWith("capabilities=")) { assert(await opening.isDisabled()); assert.equal((await evidence()).registrations, 0); assert.equal((await evidence()).opens, 0); await assertGeometry(); cases++; continue; }
    await page.waitForFunction(() => !document.querySelector(".browser-opening button").disabled); await opening.click();
    if (state === "registration=uncertain") {
      const retry = page.getByRole("button", { name: language === "en" ? "Retry the same registration" : "같은 등록 다시 시도", exact: true }); await retry.waitFor(); assert.equal((await evidence()).registrations, 1); assert.equal((await evidence()).opens, 0); await retry.click(); await page.locator(".browser-tabs li").first().waitFor();
      assert(await page.evaluate(() => JSON.stringify(browserFixture.registrations[0]) === JSON.stringify(browserFixture.registrations[1])), "Explicit retry retains original request");
    } else if (state === "native=failure") { await page.locator(".browser-chrome [role=alert]").waitFor(); assert.equal((await evidence()).opens, 1); assert.equal((await evidence()).actions, 0); }
    else { await page.locator(".browser-tabs li").first().waitFor(); if (state === "tabs=16") assert(await page.getByRole("button", { name: language === "en" ? "New tab" : "새 탭", exact: true }).isDisabled()); else assert(await page.getByRole("button", { name: language === "en" ? "Back" : "뒤로", exact: true }).isDisabled()); await assertGeometry(); }
    assert.equal((await evidence()).actions, 0); cases++;
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({ operation: "browser-split-layout", ...source, cases, screenshots, effectiveZoom: "CSS zoom 2 plus narrow viewport reflow", nativeAcceptance: "not-performed" }) + "\n");
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
