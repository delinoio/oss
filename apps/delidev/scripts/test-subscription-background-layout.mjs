// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no login, logout or account deletion is run.
import assert from "node:assert/strict";
import { mkdtemp, readFile, realpath, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const screenshots = process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR ? await realpath(process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR) : null;
if (screenshots) {
  const location = relative(resolve(app, "../.."), screenshots);
  assert(isAbsolute(location) || location === ".." || location.startsWith(`..${sep}`), "Screenshots must be outside the checkout");
}
const directory = await mkdtemp(join(tmpdir(), "delidev-subscription-background-"));
let browser, server;
let cases = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage();
  page.setDefaultTimeout(10000);
  const errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  // Half-sized CSS viewports model effective 200% layout. Actual browser chrome
  // zoom and packaged CEF behavior require independent platform acceptance.
  for (const theme of ["light", "dark"]) for (const [width, height] of [[1440, 900], [960, 640], [640, 480], [480, 320]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?theme=${theme}&subscriptionBackground=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const inventory = page.locator(".subscription-row");
    await inventory.waitFor();
    const original = await inventory.elementHandle();
    // The modal intentionally removes background controls from the accessibility
    // tree. Keep this locator usable when checking their disabled state.
    const opener = inventory.locator(".subscription-more > button");
    await opener.click();
    await page.getByRole("button", { name: "Account details", exact: true }).click();
    await page.locator(".subscription-advanced summary").click();
    for (const action of ["Delete account", "Edit preferences"]) for (const dismissal of ["X", "Escape", "Cancel"]) {
      await opener.click();
      const taskAction = page.getByRole("button", { name: action, exact: true });
      await taskAction.scrollIntoViewIfNeeded();
      const before = await inventory.evaluate(node => ({ height: node.getBoundingClientRect().height, scroll: document.querySelector("main").scrollTop }));
      await taskAction.click();
      const dialog = page.getByRole("dialog");
      await dialog.waitFor();
      assert(await inventory.isVisible(), "The subscription row disappeared behind the modal");
      assert(await page.locator(".subscription-provider-cards").isVisible(), "The service choices disappeared behind the modal");
      const state = await inventory.evaluate(node => {
        const background = node.closest("fieldset"), dialog = document.querySelector(".settings-task-dialog[open]");
        return { height: node.getBoundingClientRect().height, scroll: document.querySelector("main").scrollTop, hidden: Boolean(node.closest("[hidden]")), disabled: background.disabled, inert: background.inert, ariaHidden: background.getAttribute("aria-hidden"), modalOutside: !background.contains(dialog), details: Boolean(node.querySelector(".subscription-details")), advanced: document.querySelector(".subscription-advanced").open };
      });
      assert(!state.hidden && state.disabled && state.inert && state.ariaHidden === "true" && state.modalOutside && state.details && state.advanced, JSON.stringify(state));
      assert.equal(state.height, before.height, "Opening the task changed the list layout");
      assert.equal(state.scroll, before.scroll, "Opening the task moved the background scroll position");
      assert(await opener.isDisabled(), "The background allows a replacement operation");
      await opener.evaluate(node => node.focus());
      assert(await dialog.evaluate(node => node.contains(document.activeElement)), "Background focus escaped the modal");
      for (const key of ["Tab", "Shift+Tab"]) for (let step = 0; step < 6; step++) {
        await page.keyboard.press(key);
        assert(await dialog.evaluate(node => node.contains(document.activeElement)), "Keyboard focus escaped the modal");
      }
      await page.mouse.click(20, 80);
      assert(await dialog.isVisible(), "A backdrop click dismissed the task");
      const geometry = await dialog.evaluate(node => ({ width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height, overflow: node.scrollWidth > node.clientWidth }));
      assert(geometry.width <= width - 31 && geometry.height <= height - 47 && !geometry.overflow, JSON.stringify(geometry));
      if (screenshots && width === 1440 && action === "Delete account" && dismissal === "X") await page.screenshot({ path: join(screenshots, `subscription-background-${theme}.png`) });
      if (dismissal === "X") await dialog.getByRole("button", { name: /^Close / }).click();
      else if (dismissal === "Escape") await page.keyboard.press("Escape");
      else await dialog.getByRole("button", { name: action === "Delete account" ? "Keep account" : "Cancel edit", exact: true }).click();
      await dialog.waitFor({ state: "detached" });
      assert(await original.evaluate(node => node.isConnected && node === document.querySelector(".subscription-row")), "Dismissal replaced the inventory controller");
      assert(await inventory.locator(".subscription-details").isVisible(), "Dismissal lost the expanded details");
      assert(await page.locator(".subscription-advanced").evaluate(node => node.open), "Dismissal lost the Advanced disclosure");
      await page.waitForFunction(() => document.activeElement?.getAttribute("aria-label") === "More actions for ChatGPT fixture");
      assert.equal(await page.locator("main").evaluate(node => node.scrollTop), before.scroll, "Dismissal moved the background scroll position");
      assert(await opener.isEnabled(), "Idle dismissal kept the inventory locked");
      cases++;
    }
    await original.dispose();
    console.log(JSON.stringify({ operation: "subscription_background_viewport", result: "passed", theme, width, height, cases }));
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: "subscription_background_layout", result: "passed", cases, themes: 2, viewports: 4, nativeAcceptance: "not-performed", accountAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
