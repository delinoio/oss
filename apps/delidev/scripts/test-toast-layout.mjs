// SPDX-License-Identifier: Apache-2.0
// Host-supplied headless Chromium validates the real App/Settings toast flow
// against synthetic Connect handlers. No native or real account acceptance.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const playwright = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(playwright ? pathToFileURL(resolve(playwright)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-toast-layout-"));
const screenshots = process.env.DELIDEV_TOAST_SCREENSHOT_DIR;
let browser, server;
let checks = 0;
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
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage();
  page.on("pageerror", error => failures.push(error.message));
  await page.addInitScript(() => { document.addEventListener("securitypolicyviolation", event => { if (event.violatedDirective === "style-src-elem" || event.violatedDirective === "style-src-attr") console.error("toast_style_csp_failure"); }); });
  page.on("console", message => { if (message.text() === "toast_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const saved = page.getByRole("button", { name: "Dismiss notification: Notification preferences saved.", exact: true });
  const createToast = async () => {
    await page.getByRole("button", { name: "Edit notification preferences", exact: true }).click();
    await page.getByRole("checkbox", { name: "Execution completion, failure and interruption", exact: true }).click();
    await page.getByRole("button", { name: "Save notification preferences", exact: true }).click();
    await saved.waitFor();
    // Finish the existing editor's async refresh/focus handoff before explicitly
    // entering the toast. Entering it earlier deliberately cancels that handoff.
    await page.waitForFunction(() => [...document.querySelectorAll("button")].some(node => node.textContent === "Edit notification preferences" && !node.disabled));
    await page.getByRole("button", { name: "Edit notification preferences", exact: true }).focus();
    await saved.focus();
    await page.locator(".toast-notification").evaluate(node => Promise.all(node.getAnimations().map(animation => animation.finished)));
  };
  const enterNotifications = async () => {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const opener = page.getByRole("button", { name: "Open settings categories", exact: true });
    if (await opener.isVisible()) await opener.click();
    await page.getByRole("button", { name: "Notifications", exact: true }).click();
    await page.getByRole("button", { name: "Edit notification preferences", exact: true }).waitFor();
  };
  for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [960,640], [640,480], [720,450], [480,320]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?theme=${theme}`);
    await enterNotifications();
    await createToast();
    const layout = await page.locator(".toast-notification").evaluate(node => {
      const viewport = node.parentElement, box = node.getBoundingClientRect(), parent = viewport.getBoundingClientRect();
      return { top: parent.top, center: parent.left + parent.width / 2, width: box.width, left: box.left, right: innerWidth - box.right, overflow: node.scrollWidth > node.clientWidth, color: getComputedStyle(node).color, background: getComputedStyle(node).backgroundColor, radius: getComputedStyle(node).borderRadius, close: node.querySelector("button").getBoundingClientRect().width, inline: Boolean(viewport.getAttribute("style") || node.getAttribute("style")) };
    });
    const context = `${theme}/${width}x${height}: ${JSON.stringify(layout)}`;
    assert.equal(layout.top, 80, context); assert(Math.abs(layout.center - width / 2) < 0.5, context);
    assert(layout.width <= 420 && layout.left >= 16 && layout.right >= 16 && !layout.overflow && !layout.inline, context);
    assert.equal(layout.radius, "10px", context); assert.equal(layout.close, 32, context);
    assert.equal(layout.background, theme === "light" ? "rgb(255, 255, 255)" : "rgb(27, 33, 43)", context);
    if (screenshots && width === 1440) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `toast-${theme}.png`) }); }
    await saved.press("Escape");
    assert.equal(await saved.count(), 0, "Focused Escape dismisses the toast");
    assert.equal(await page.getByRole("button", { name: "Edit notification preferences", exact: true }).evaluate(node => node === document.activeElement), true, "Dismissal returns focus to the current editor action");
    checks++;
  }
  await page.setViewportSize({ width: 640, height: 480 });
  await createToast();
  await page.getByRole("button", { name: "Open settings categories", exact: true }).click();
  assert.equal(await page.locator(".toast-viewport").isVisible(), false, "Compact modal drawer hides toasts");
  await page.waitForTimeout(5500);
  await page.getByRole("button", { name: "Close navigation", exact: true }).click();
  await saved.waitFor(); await saved.focus();
  assert.equal(await saved.count(), 1, "Modal time does not expire the retained toast");
  await page.emulateMedia({ reducedMotion: "reduce" });
  assert.equal(await page.locator(".toast-notification").evaluate(node => getComputedStyle(node).animationName), "none", "Reduced motion disables toast animation");
  await saved.press("Escape");
  await page.setViewportSize({ width: 480, height: 320 });
  await page.goto(`${origin}/?theme=dark&toast-controls=true`);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Open settings categories", exact: true }).click();
  await page.getByRole("button", { name: "Connection & diagnostics", exact: true }).click();
  for (const kind of ["success", "info", "warning", "error"]) await page.getByRole("button", { name: `Show ${kind} toast`, exact: true }).click();
  assert.deepEqual(await page.locator(".toast-notification").evaluateAll(nodes => nodes.map(node => node.dataset.kind)), ["success", "info", "warning"], "Three visible arrivals preserve FIFO order");
  assert.equal(await page.locator(".toast-notification").evaluateAll(nodes => nodes.every(node => node.scrollWidth <= node.clientWidth && node.querySelector("p").scrollWidth <= node.querySelector("p").clientWidth)), true, "Long words wrap without horizontal clipping");
  assert.equal(await page.locator(".toast-viewport").evaluate(node => node.getBoundingClientRect().bottom <= innerHeight - 15), true, "Tall stacks remain within the effective zoom viewport");
  await page.getByRole("button", { name: "Dismiss notification: Fixture save completed.", exact: true }).click();
  assert.equal(await page.locator('.toast-notification[data-kind="error"] [role="alert"]').count(), 1, "Queued errors retain assertive announcement semantics");
  assert.deepEqual(failures, [], "No fixture errors or toast style CSP violations");
  console.log(JSON.stringify({ operation: "toast_layout", result: "passed", sourceRevision: process.env.DELIDEV_VALIDATION_REVISION ?? "working-tree", layoutChecks: checks, themes: 2, viewports: 5, effectiveZoom: "half-size CSS viewports", csp: "style-src self", modalTimer: "passed", keyboard: "passed", longContentAndQueue: "passed", nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
