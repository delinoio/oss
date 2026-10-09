// SPDX-License-Identifier: Apache-2.0
// Synthetic browser geometry only; no native, account or execution acceptance.
import { createHash } from "node:crypto";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const { chromium } = await import(pathToFileURL(resolve(process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE)).href);
const directory = await mkdtemp(join(tmpdir(), "delidev-worker-rows-"));
const assetHashes = Object.fromEntries(await Promise.all(["codex.webp", "claude-code.png", "opencode-light.svg", "opencode-dark.svg", "grok-light.svg", "grok-dark.svg"].map(async file => [file, createHash("sha256").update(await readFile(join(app, "public/harness-marks", file))).digest("hex")])));
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/agent-worker-row-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  async function decodedMarks(locator, size, count, theme) {
    const marks = await locator.evaluateAll(async nodes => Promise.all(nodes.map(async node => {
      const css = getComputedStyle(node), url = css.backgroundImage.slice(4, -1).replace(/^['"]|['"]$/g, "");
      const image = new Image(); image.src = url; await image.decode();
      const digest = await crypto.subtle.digest("SHA-256", await (await fetch(url)).arrayBuffer());
      return { harness: node.dataset.harness, width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height, gap: getComputedStyle(node.parentElement.parentElement).columnGap, contain: css.backgroundSize, hidden: node.getAttribute("aria-hidden"), decoded: image.naturalWidth > 0 && image.naturalHeight > 0, digest: [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join("") };
    })));
    assert.equal(marks.length, count);
    for (const mark of marks) {
      assert.equal(mark.width, size); assert.equal(mark.height, size); assert.equal(mark.gap, "8px"); assert.equal(mark.contain, "contain"); assert.equal(mark.hidden, "true"); assert(mark.decoded);
      const file = { codex: "codex.webp", "claude-code": "claude-code.png", opencode: `opencode-${theme}.svg`, "grok-build": `grok-${theme}.svg` }[mark.harness];
      assert.equal(mark.digest, assetHashes[file], JSON.stringify({ theme, mark }));
    }
  }
  for (const language of ["en", "ko"]) for (const [theme, systemTheme] of [["light", "light"], ["dark", "dark"], ["system", "light"], ["system", "dark"]]) for (const [width, height, availableWidth] of [[1440,900], [960,640], [640,480], [720,450], [480,320], [1100,900], [1099,900], [1440,900,640], [1440,900,639]]) {
    await page.setViewportSize({ width, height }); await page.emulateMedia({ colorScheme: systemTheme }); await page.goto(`${origin}/?language=${language}&theme=${theme}${availableWidth ? `&availableWidth=${availableWidth}` : ""}`);
    await page.locator(".agent-model-native").first().waitFor();
    assert.equal(await page.locator("html").getAttribute("data-model-reads"), "1");
    await decodedMarks(page.locator(".settings-agent-mark .worker-harness-mark"), 32, 4, theme === "system" ? systemTheme : theme);
    const picker = page.getByRole("region", { name: "Agent picker" }).getByRole("combobox");
    await picker.locator(".worker-harness-mark").waitFor();
    await decodedMarks(picker.locator(".worker-harness-mark"), 16, 1, theme === "system" ? systemTheme : theme);
    for (const harness of ["codex", "claude-code", "opencode", "grok-build"]) {
      await picker.click();
      const popup = page.getByRole("listbox");
      await decodedMarks(popup.locator(".worker-harness-mark"), 16, 4, theme === "system" ? systemTheme : theme);
      assert.equal(await popup.locator(".scroll-picker-decoration").count(), 7);
      await popup.locator(`button:has([data-harness="${harness}"])`).click();
      await page.waitForFunction(harness => document.querySelector('[role="combobox"] .worker-harness-mark')?.getAttribute("data-harness") === harness, harness);
      await decodedMarks(picker.locator(".worker-harness-mark"), 16, 1, theme === "system" ? systemTheme : theme);
      assert.equal(await page.locator("html").getAttribute("data-model-reads"), "1", "Decorations cannot add model reads");
    }
    await picker.click(); await page.getByRole("option", { name: "Worker missing harness" }).click();
    assert.equal(await picker.locator(".worker-harness-mark").count(), 0);
    assert.equal(await picker.locator(".scroll-picker-decoration").evaluate(node => node.getBoundingClientRect().width), 16);
    const geometry = await page.locator(".settings-agent-row").evaluateAll(rows => rows.map(row => ({ width: row.clientWidth, overflow: row.scrollWidth > row.clientWidth + 1, mark: [...row.querySelectorAll(".worker-harness-mark")].map(mark => ({ width: mark.getBoundingClientRect().width, image: getComputedStyle(mark).backgroundImage, hidden: mark.getAttribute("aria-hidden") })), buttons: [...row.querySelectorAll("button")].map(button => ({ height: button.getBoundingClientRect().height, radius: getComputedStyle(button).borderRadius })) })));
    for (const row of geometry) { assert(!row.overflow, JSON.stringify(row)); for (const mark of row.mark) { assert.equal(mark.width, 32); assert.notEqual(mark.image, "none"); assert.equal(mark.hidden, "true"); } for (const button of row.buttons) { assert(button.height >= 40); assert.equal(button.radius, "8px"); } }
    const anchors = await page.locator(".settings-agent-row").evaluateAll(rows => rows.map(row => {
      const details = row.querySelector(".settings-agent-details").getBoundingClientRect();
      const actions = row.querySelector(".settings-agent-actions").getBoundingClientRect();
      const heading = row.querySelector("h3").getBoundingClientRect();
      const identity = row.querySelector(".settings-agent-text > small").getBoundingClientRect();
      const summary = row.querySelector(".agent-model-summary > span")?.getBoundingClientRect();
      return { headingX: heading.x, identityX: identity.x, summaryX: summary?.x, detailsTop: details.top, actionsTop: actions.top, sideBySide: actions.left >= details.right - 1 };
    }));
    for (const row of anchors) {
      assert(Math.abs(row.headingX - row.identityX) <= 1, JSON.stringify(row));
      if (row.summaryX !== undefined) assert(Math.abs(row.headingX - row.summaryX) <= 1, JSON.stringify(row));
      if (row.sideBySide) assert(Math.abs(row.detailsTop - row.actionsTop) <= 1, JSON.stringify(row));
    }
    const toggle = page.getByRole("button", { name: language === "ko" ? "+2개 더 보기" : "+2 more" });
    await page.keyboard.press("Tab"); await toggle.focus(); assert(await toggle.evaluate(node => node.matches(":focus-visible") && parseFloat(getComputedStyle(node).outlineWidth) > 0));
    const actionsTop = await page.locator(".settings-agent-actions").first().evaluate(node => node.getBoundingClientRect().top);
    await toggle.press("Enter"); assert.equal(await toggle.getAttribute("aria-expanded"), "true");
    await page.waitForFunction(() => document.documentElement.dataset.modelReads === "2");
    if (anchors[0].sideBySide) assert(Math.abs(actionsTop - await page.locator(".settings-agent-actions").first().evaluate(node => node.getBoundingClientRect().top)) <= 1);
    const native = await page.locator(".agent-model-routes code").allTextContents(); assert.equal(native.length, 3); assert.equal(native[0], native[2]); assert.equal(native[1], "second-native");
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth));
    await toggle.press("Space"); assert.equal(await toggle.getAttribute("aria-expanded"), "false"); assert.equal(await page.locator("html").getAttribute("data-model-reads"), "2"); if (anchors[0].sideBySide) assert(Math.abs(actionsTop - await page.locator(".settings-agent-actions").first().evaluate(node => node.getBoundingClientRect().top)) <= 1); checks++;
  }
  // A failed local asset leaves its fixed slot, readable name and original controls.
  const failedPage = await browser.newPage();
  await failedPage.route(/codex[^/]*\.webp/, route => route.abort());
  await failedPage.goto(`${origin}/?language=en&theme=light`);
  await failedPage.locator(".agent-model-native").first().waitFor();
  const failedMark = failedPage.locator(".settings-agent-mark .worker-harness-mark").first();
  assert.equal(await failedMark.evaluate(async node => { const image = new Image(); image.src = getComputedStyle(node).backgroundImage.slice(4, -1).replace(/^['"]|['"]$/g, ""); try { await image.decode(); return true; } catch { return false; } }), false);
  assert.equal(await failedMark.evaluate(node => node.getBoundingClientRect().width), 32);
  assert(await failedPage.locator(".settings-agent-row h3").first().isVisible());
  assert(await failedPage.locator(".settings-agent-actions button").first().isEnabled());
  const failedPicker = failedPage.getByRole("region", { name: "Agent picker" }).getByRole("combobox");
  await failedPicker.locator(".worker-harness-mark").waitFor();
  assert.equal(await failedPicker.locator(".scroll-picker-decoration").evaluate(node => node.getBoundingClientRect().width), 16);
  assert(await failedPicker.isEnabled());
  assert.equal(await failedPage.locator("html").getAttribute("data-model-reads"), "1");
  assert.equal(await failedPage.locator("html").getAttribute("data-agent-reads"), "1");
  await failedPage.close();
  console.log(JSON.stringify({ operation: "agent_worker_rows_layout", result: "passed", checks, languages: 2, themes: 3, systemPreferences: 2, decodedAssets: true, missingAssetFallback: true, viewports: 7, availableRowBoundaries: [640,639], effectiveZoom: [1,2], nativeAcceptance: "not-performed" }));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
