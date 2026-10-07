// SPDX-License-Identifier: Apache-2.0
// Host-supplied Chromium validates the real sidebar using synthetic Connect
// inventory. This does not establish native CEF, account or platform acceptance.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, realpath, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { basename, dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const playwright = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(playwright ? pathToFileURL(resolve(playwright)).href : "playwright");
async function screenshotDestination(value) {
  let ancestor = resolve(value);
  const missing = [];
  while (true) {
    try {
      const destination = resolve(await realpath(ancestor), ...missing.reverse());
      const path = relative(resolve(app, "../.."), destination);
      assert(path && (isAbsolute(path) || path === ".." || path.startsWith(`..${sep}`)), "Screenshot destination must resolve outside the checkout");
      return destination;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      const parent = dirname(ancestor);
      if (parent === ancestor) throw error;
      missing.push(basename(ancestor));
      ancestor = parent;
    }
  }
}
const screenshots = process.env.DELIDEV_HOVER_SCREENSHOT_DIR ? await screenshotDestination(process.env.DELIDEV_HOVER_SCREENSHOT_DIR) : undefined;
const directory = await mkdtemp(join(tmpdir(), "delidev-session-hover-layout-"));
let browser, server, page, scenario, checks = 0;
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
  await page.addInitScript(() => {
    window.sessionHoverTrace = [];
    for (const type of ["scroll", "focusin", "pointerover", "pointerout", "beforetoggle", "toggle"]) document.addEventListener(type, event => {
      const node = event.target instanceof Element ? event.target : null;
      const row = node?.closest(".sidebar-session-row");
      const zone = row ? "row" : node?.closest(".sidebar-session-tooltip") ? "card" : node?.classList.contains("sidebar-list") ? "list" : "other";
      if (zone === "other" && type !== "scroll") return;
      window.sessionHoverTrace.push({ type, zone, row: row ? [...document.querySelectorAll(".sidebar-session-row")].indexOf(row) : null, state: event.newState, time: Math.round(performance.now()) });
      window.sessionHoverTrace = window.sessionHoverTrace.slice(-32);
    }, true);
  });
  await page.addInitScript(() => document.addEventListener("securitypolicyviolation", event => {
    if (event.violatedDirective.startsWith("style-src")) console.error("session_hover_style_csp_failure");
  }));
  page.on("console", message => { if (message.text() === "session_hover_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const rows = page.locator(".sidebar-session-row");
  const card = page.getByRole("tooltip");
  const checkCard = async (theme, width, height) => {
    const geometry = await card.evaluate(node => {
      const box = node.getBoundingClientRect(), style = getComputedStyle(node), title = node.querySelector(".sidebar-session-card-title");
      return { left: box.left, top: box.top, right: box.right, bottom: box.bottom, width: box.width, height: box.height, padding: style.padding, radius: style.borderRadius, background: style.backgroundColor, titleSize: getComputedStyle(title).fontSize, horizontalOverflow: node.scrollWidth > node.clientWidth, topLayer: node.matches(":popover-open"), hit: node.contains(document.elementFromPoint(box.left + 20, box.top + 20)), controls: Boolean(node.querySelector("button, a, input, [tabindex]")) };
    });
    const context = `${theme}/${width}x${height}: ${JSON.stringify(geometry)}`;
    assert(geometry.left >= 7.5 && geometry.top >= 7.5 && geometry.right <= width - 7.5 && geometry.bottom <= height - 7.5, context);
    assert.equal(geometry.width, 320, context);
    assert.equal(geometry.padding, "16px", context);
    assert.equal(geometry.radius, "8px", context);
    assert.equal(geometry.titleSize, "15px", context);
    assert.equal(geometry.background, theme === "light" ? "rgb(255, 255, 255)" : "rgb(27, 33, 43)", context);
    assert(!geometry.horizontalOverflow && geometry.topLayer && geometry.hit && !geometry.controls, context);
    assert.equal(await card.count(), 1, context);
    checks++;
    return geometry;
  };
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [960,640], [480,320]]) {
    scenario = { language, theme, width, height };
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?sessionHover=true&language=${language}&theme=${theme}`);
    const opener = page.locator(".sidebar-context-trigger");
    if (await opener.isVisible()) await opener.click();
    await page.locator(".sidebar-project-row").filter({ hasText: "oss" }).click();
    await rows.first().waitFor();
    await page.mouse.move(width - 5, height - 5);
    await rows.first().scrollIntoViewIfNeeded();
    await page.evaluate(() => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done))));
    await rows.first().focus();
    await card.waitFor();
    const source = await rows.first().boundingBox();
    const geometry = await checkCard(theme, width, height);
    assert.equal(await card.locator(".sidebar-session-card-title").textContent(), "New session");
    if (width >= 960) {
      assert(Math.abs(geometry.left - source.x - source.width - 8) < 0.5, "Card is 8px right of its source");
      assert(Math.abs(geometry.top - source.y) < 0.5, "Card aligns to the source top");
    } else {
      assert.equal(await card.evaluate(node => node.closest("dialog")?.matches(":modal")), true, "Compact card remains inside the owning modal");
    }
    if (width === 1440 && screenshots) {
      await mkdir(resolve(screenshots), { recursive: true });
      await page.screenshot({ path: join(resolve(screenshots), `session-hover-${language}-${theme}.png`) });
    }
    await rows.first().press("Escape");
    assert.equal(await card.count(), 0, "Escape dismisses without changing focus");
    assert(await rows.first().evaluate(node => node === document.activeElement), "Focus stays on the row");
    if (width < 760) assert.equal(await page.locator("dialog:modal").count(), 1, "Escape dismisses the card before the drawer");
    await rows.nth(1).scrollIntoViewIfNeeded();
    await page.evaluate(() => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done))));
    await rows.nth(1).focus();
    await card.waitFor();
    await checkCard(theme, width, height);
    assert.equal(await card.locator(".sidebar-session-card-title").textContent(), "Complete-session-name-".repeat(12), "Full long name is preserved");
    assert.equal(await card.locator(".sidebar-session-card-title-state p").count(), 2, "Safe title reason is separate and complete");
    if (width === 1440 && screenshots) {
      await mkdir(resolve(screenshots), { recursive: true });
      await page.screenshot({ path: join(resolve(screenshots), `session-hover-long-${language}-${theme}.png`) });
    }
    await card.evaluate(node => { node.scrollTop = 20; node.dispatchEvent(new Event("scroll")); });
    assert.equal(await card.count(), 1, "Card scroll retains presentation");
    await page.locator(".sidebar-list").evaluate(node => { node.scrollTop += 100; node.dispatchEvent(new Event("scroll")); });
    assert.equal(await card.count(), 0, "Source scrolling clears presentation");
    // Scrolling is an explicit dismissal boundary. Settle setup scrolling
    // before testing focus so a queued scroll cannot dismiss the new card.
    await rows.last().scrollIntoViewIfNeeded();
    await page.evaluate(() => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done))));
    await rows.last().focus();
    await card.waitFor();
    await checkCard(theme, width, height);
    await rows.last().press("Escape");
    await page.locator(".sidebar-project-row").filter({ hasText: "oss" }).focus();
    await page.locator(".sidebar-list").evaluate(node => { node.scrollTop = 0; });
    await rows.first().scrollIntoViewIfNeeded();
    await page.evaluate(() => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done))));
    await page.mouse.move(width - 5, height - 5);
    await rows.first().hover();
    await card.waitFor();
    const box = await card.boundingBox();
    await page.mouse.move(box.x + 24, box.y + 24);
    await page.waitForTimeout(400);
    assert.equal(await card.count(), 1, "Pointer crossing the 8px gap retains the card");
    await page.mouse.move(width - 5, height - 5);
    await card.waitFor({ state: "detached" });
  }
  assert.deepEqual(failures, [], "No page errors or style CSP violations");
  console.log(JSON.stringify({ operation: "session_hover_layout", result: "passed", sourceRevision: process.env.DELIDEV_VALIDATION_REVISION ?? "working-tree", layoutChecks: checks, languages: 2, themes: 2, viewports: 3, effectiveZoom: "half-size CSS viewport", keyboard: "passed", pointerGap: "passed", modalClipping: "passed", csp: "style-src self", nativeAcceptance: "not-performed" }));
} catch (error) {
  console.error(JSON.stringify({ operation: "session_hover_layout", result: "failed", scenario, checks, failures, presentation: page && await page.evaluate(() => ({ focused: document.activeElement?.className, modals: document.querySelectorAll("dialog:modal").length, cards: document.querySelectorAll(".sidebar-session-tooltip").length, viewport: [innerWidth, innerHeight], trace: window.sessionHoverTrace, rows: [...document.querySelectorAll(".sidebar-session-row")].slice(0, 2).map(node => ({ box: node.getBoundingClientRect().toJSON(), hidden: Boolean(node.closest("[hidden], [inert]")) })) })).catch(() => null) }));
  throw error;
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
