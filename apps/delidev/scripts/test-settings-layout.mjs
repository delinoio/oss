// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation; Playwright is supplied by the validation host,
// without adding a product/workspace dependency. All data is synthetic.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-settings-layout-"));
let browser, server;
const categories = ["AI Subscription", "AI API Keys", "API Providers", "Models", "Agent Workers", "Instructions", "Projects", "Repositories", "Runner Devices", "Appearance", "Paired devices", "Server preferences", "Integrations", "Connection & diagnostics", "Notifications", "Import / Export", "Backups"];
const viewports = [[1920,1080], [1440,1000], [1440,900], [1280,820], [960,640], [640,480]];
let checked = 0, formsChecked = 0, keyboardChecks = 0;
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
  page.on("pageerror", error => console.error("fixture_page_error", error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const select = async category => {
    if (await page.getByRole("button", { name: "Open settings categories", exact: true }).isVisible()) await page.getByRole("button", { name: "Open settings categories", exact: true }).click();
    await page.getByRole("button", { name: category, exact: true }).click();
    await page.locator(".settings-content h1:visible").filter({ hasText: category }).waitFor();
    // Allow asynchronous synthetic reads to settle; no external RPC/account work.
    await page.waitForFunction(() => ![...document.querySelectorAll(".settings-content [role=status]")].some(node => node.getClientRects().length && /^(Loading |Reading server diagnostics)/.test(node.textContent ?? "")));
    if (category === "Notifications") await page.getByRole("button", { name: "Edit notification preferences", exact: true }).waitFor();
  };
  for (const theme of ["light", "dark", "system"]) for (const populated of [false, true]) for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport[0], height: viewport[1] });
    await page.emulateMedia({ colorScheme: theme === "system" ? "dark" : theme });
    await page.goto(`${origin}/?theme=${theme}&populated=${populated}`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    for (const category of categories) {
      await select(category);
      const layout = await page.locator(".settings-content").evaluate(root => {
        const column = root.querySelector(".settings-content-column"), h1s = [...root.querySelectorAll("h1")].filter(node => node.getClientRects().length);
        const style = getComputedStyle(root), box = column.getBoundingClientRect();
        const controls = [...root.querySelectorAll("button, select, input:not([type=checkbox]):not([type=radio])")].filter(node => node.getClientRects().length);
        const textareas = [...root.querySelectorAll("textarea")].filter(node => node.getClientRects().length);
        const empty = [...root.querySelectorAll(".settings-empty")].filter(node => node.getClientRects().length);
        const forms = [...root.querySelectorAll("form")].filter(node => node.getClientRects().length);
        return { titles: h1s.length, titleSize: getComputedStyle(h1s[0]).fontSize, padding: style.paddingLeft, anchor: box.left - root.getBoundingClientRect().left, width: box.width, overflow: root.scrollWidth > root.clientWidth, controls: controls.every(node => node.getBoundingClientRect().height >= 39.5), multiline: textareas.every(node => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(node).whiteSpace)), empty: empty.every(node => node.getBoundingClientRect().height >= 159.5), forms: forms.every(node => node.getBoundingClientRect().width <= 720.5) };
      });
      const context = `${theme}/${populated}/${viewport}/${category}: ${JSON.stringify(layout)}`;
      assert.equal(layout.titles, 1, context); assert.equal(layout.titleSize, "26px", context);
      assert.equal(layout.padding, viewport[0] >= 1100 ? "32px" : viewport[0] >= 760 ? "24px" : "16px", context);
      assert.equal(layout.anchor, Number.parseInt(layout.padding), context);
      assert(layout.width <= 1040.5 && !layout.overflow && layout.controls && layout.multiline && layout.empty && layout.forms, context);
      checked++;
    }
    if (!populated) {
      for (const [category, action] of [["Agent Workers", "New Agent Worker"], ["Projects", "New Project"], ["Instructions", "New Instructions"], ["Repositories", "New Repository"], ["Server preferences", "New Server preferences"], ["Models", "New Model"], ["Integrations", "New GitHub profile"], ["Notifications", "Edit notification preferences"], ["AI API Keys", "Add AI API key"]]) {
        await select(category); await page.getByRole("button", { name: action, exact: true }).click();
        if (category === "AI API Keys") await page.getByRole("button", { name: /^Fixture provider/ }).click();
        const dialog = page.getByRole("dialog"); await dialog.waitFor();
        const form = dialog.locator("form:visible");
        if (category !== "Repositories") await form.waitFor();
        const layout = await dialog.evaluate(node => {
          const box = node.getBoundingClientRect(), header = node.querySelector(".settings-task-header"), footer = node.querySelector(".settings-task-footer"), body = node.querySelector(".settings-task-body");
          return { width: box.width, left: box.left, right: innerWidth - box.right, height: box.height, overflow: body.scrollWidth > body.clientWidth, title: getComputedStyle(header.querySelector("h2")).fontSize, radius: getComputedStyle(node).borderRadius, footerInside: !footer.getClientRects().length || footer.getBoundingClientRect().bottom <= box.bottom, bodyScroll: getComputedStyle(body).overflowY };
        });
        assert(layout.left >= 15.5 && layout.right >= 15.5 && layout.height <= viewport[1] - 47.5 && !layout.overflow && layout.footerInside, `${category} dialog bounds: ${JSON.stringify(layout)}`);
        assert.equal(layout.title, "20px"); assert.equal(layout.radius, "16px"); assert.equal(layout.bodyScroll, "auto");
        assert.equal(await page.locator(".settings-content h1:visible").count(), 1, "Mounted background category title");
        assert.equal(await page.locator("dialog[open]:not([role=region])").count(), 1, "One native dialog");
        if (category !== "Repositories") {
          assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline controls preserve whitespace`);
          assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), `${category} form cap`);
        }
        const close = dialog.locator(".settings-task-close"); await close.focus();
        for (const key of ["Shift+Tab", "Tab", "Tab"]) {
          await page.keyboard.press(key);
          assert(await dialog.evaluate(node => node.contains(document.activeElement)), `${category} ${key} stays in dialog`); keyboardChecks++;
        }
        await page.mouse.click(2, 2); assert(await dialog.isVisible(), "Backdrop preserves task");
        formsChecked++;
        await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
        assert(await page.locator(".settings-content").evaluate(node => node.contains(document.activeElement)), `${category} returns focus to its category`); keyboardChecks++;

      }
    }
  }
  // 200% effective-layout coverage uses half-size CSS viewports. Actual browser
  // chrome zoom and packaged CEF keyboard/platform acceptance remain separate.
  await page.goto(`${origin}/?theme=dark`); await page.getByRole("button", { name: "Settings", exact: true }).click();
  for (const [width,height] of viewports) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    for (const category of categories) { await select(category); assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} effective 200% ${width}`); checked++; }
  }
  for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1280,820], [960,640], [640,480]]) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    await page.goto(`${origin}/?theme=${theme}`); await page.getByRole("button", { name: "Settings", exact: true }).click();
    await select("Projects"); await page.getByRole("button", { name: "New Project", exact: true }).click();
    const dialog = page.getByRole("dialog");
    assert(await dialog.evaluate(node => { const box = node.getBoundingClientRect(), body = node.querySelector(".settings-task-body"); return box.width <= innerWidth - 31 && box.height <= innerHeight - 47 && body.scrollWidth <= body.clientWidth; }), `${theme} Project dialog effective 200% ${width}`);
    await page.keyboard.press("Escape"); formsChecked++;
  }
  if (process.env.DELIDEV_LAYOUT_SCREENSHOT) {
    await page.setViewportSize({ width: 1440, height: 900 }); await page.goto(`${origin}/?theme=light&populated=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Projects");
    await page.getByRole("button", { name: /^Edit Example PROJECT/ }).click(); await page.getByRole("dialog").waitFor();
    await page.screenshot({ path: process.env.DELIDEV_LAYOUT_SCREENSHOT });
  }
  console.log(JSON.stringify({ operation: "settings_layout", result: "passed", categoryChecks: checked, childFormChecks: formsChecked, themes: 3, inventories: 2, viewports: 6, effectiveZoomChecks: 102, keyboardChecks, nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
