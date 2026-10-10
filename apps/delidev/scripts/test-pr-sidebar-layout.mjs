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
const screenshots = process.env.DELIDEV_PR_SIDEBAR_SCREENSHOT_DIR;
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
  const enter = async language => {
    await page.getByRole("button", { name: language === "ko" ? "풀 리퀘스트" : "Pull requests", exact: true }).click();
    const opener = page.locator(".sidebar-context-trigger");
    if (await opener.isVisible()) await opener.click();
    return page.getByRole("region", { name: language === "ko" ? "풀 리퀘스트 탐색 및 필터" : "Pull requests navigation and filters", exact: true });
  };
  const assertLayout = async (pane, context) => {
    const geometry = await pane.evaluate(node => {
      const box = node.getBoundingClientRect();
      return { overflow: node.scrollWidth > node.clientWidth, outside: [...node.querySelectorAll("button, input:not([type=radio]), select")].filter(item => item.getClientRects().length).some(item => { const rect = item.getBoundingClientRect(); return rect.left < box.left - 1 || rect.right > box.right + 1 || item.scrollWidth > item.clientWidth + 1; }), targets: [...node.querySelectorAll("button, .pr-state-choice")].filter(item => item.getClientRects().length).every(item => item.getBoundingClientRect().height >= 40) };
    });
    assert(!geometry.overflow && !geometry.outside && geometry.targets, `${context}: ${JSON.stringify(geometry)}`);
    const titleRow = pane.locator(".pr-sidebar-title");
    assert.equal(await pane.locator("h2").count(), 1, `${context}: one sidebar title`);
    assert.equal(await pane.locator(".sidebar-list-heading").count(), 0, `${context}: no former repository heading row`);
    const title = await titleRow.evaluate(node => {
      const heading = node.querySelector("h2"), action = node.querySelector("button");
      const row = node.getBoundingClientRect(), text = heading.getBoundingClientRect(), button = action.getBoundingClientRect();
      const style = getComputedStyle(action), name = action.querySelector(".settings-action-name");
      const repository = node.nextElementSibling.querySelector(".pr-repository-heading")?.getBoundingClientRect();
      return { actions: node.querySelectorAll("button").length, width: button.width, height: button.height, aligned: Math.abs(button.right - row.right) < 1 && text.right <= button.left, transparent: style.backgroundColor === "rgba(0, 0, 0, 0)", hiddenName: getComputedStyle(name).clipPath === "inset(50%)", glyph: action.querySelector("svg").getAttribute("aria-hidden"), gap: repository ? repository.top - row.bottom : 12 };
    });
    assert(title.actions === 1 && title.width >= 40 && title.height >= 40 && title.aligned && title.transparent && title.hiddenName && title.glyph === "true" && Math.abs(title.gap - 12) < 1, `${context}: ${JSON.stringify(title)}`);
    const disclosures = await pane.locator(".pr-repository-details-toggle").evaluateAll(nodes => nodes.map(node => {
      const style = getComputedStyle(node), box = node.getBoundingClientRect(), glyph = node.querySelector(".disclosure-chevron").getBoundingClientRect(), row = node.parentElement.getBoundingClientRect();
      return { border: style.borderWidth, background: style.backgroundColor, shadow: style.boxShadow, width: box.width, height: box.height, glyphWidth: glyph.width, glyphHeight: glyph.height, centered: Math.abs(glyph.x + glyph.width / 2 - box.x - box.width / 2) < 1 && Math.abs(glyph.y + glyph.height / 2 - box.y - box.height / 2) < 1, contained: box.left >= row.left && box.right <= row.right && box.top >= row.top && box.bottom <= row.bottom };
    }));
    assert(disclosures.every(item => item.border === "0px" && item.background === "rgba(0, 0, 0, 0)" && item.shadow === "none" && item.width >= 40 && item.height >= 40 && item.glyphWidth === 14 && item.glyphHeight === 14 && item.centered && item.contained), `${context}: ${JSON.stringify(disclosures)}`);

    assert.deepEqual(await page.evaluate(() => window.__prSidebarFixture), { github: 0, repository: 0 }, context);
  };
  const layoutSnapshot = pane => pane.evaluate(node => ({
    scrolling: [node, node.closest(".sidebar-list")].filter(Boolean).map(item => ({ scrollHeight: item.scrollHeight, clientHeight: item.clientHeight, scrollTop: item.scrollTop })),
    siblings: [...node.querySelectorAll(".pr-repository-heading, .sidebar-query-options, .pr-state-options, .pr-sidebar-title")].map(item => {
      const { x, y, width, height } = item.getBoundingClientRect(); return { x, y, width, height };
    }),
  }));
  const assertPopup = async (pane, details, before, context) => {
    const popup = pane.getByRole("region", { name: await details.getAttribute("aria-label"), exact: true });
    const geometry = await popup.evaluate(node => {
      const box = node.getBoundingClientRect(), style = getComputedStyle(node);
      return { native: node.matches(":popover-open") && node.getAttribute("popover") === "manual", local: !!node.closest(".sidebar-pane"),
        fixed: style.position === "fixed", contained: box.left >= 7 && box.top >= 7 && box.right <= innerWidth - 7 && box.bottom <= innerHeight - 7,
        scrollable: style.overflowY === "auto", ids: /0195c9c0/.test(node.textContent) };
    });
    assert(geometry.native && geometry.local && geometry.fixed && geometry.contained && geometry.scrollable && !geometry.ids, `${context}: ${JSON.stringify(geometry)}`);
    assert.deepEqual(await layoutSnapshot(pane), before, `${context}: popup must not move siblings or sidebar scrolling`);
  };
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1100,768], [960,640], [640,480], [720,450], [480,320]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?theme=${theme}&language=${language}`);
    const pane = await enter(language);
    const row = pane.getByRole("button", { name: "oss", exact: true });
    await row.waitFor();
    assert.equal(await row.textContent(), "oss");
    await assertLayout(pane, `${language}/${theme}/${width}: unselected`);
    const refresh = pane.getByRole("button", { name: language === "ko" ? "새로고침" : "Refresh", exact: true });
    const beforeTooltip = await page.evaluate(() => window.__prSidebarCatalogReads);
    await refresh.hover();
    assert.equal(await page.getByRole("tooltip").textContent(), language === "ko" ? "새로고침" : "Refresh");
    await refresh.focus();
    assert.equal(await page.evaluate(() => window.__prSidebarCatalogReads), beforeTooltip, "Tooltip interaction adds no catalog reads");
    await refresh.press("Escape");
    await refresh.blur();
    await page.mouse.move(0, 0);

    if (screenshots && width === 1440) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `pr-${language}-${theme}-unselected.png`) }); }
    const details = pane.getByRole("button", { name: language === "ko" ? "oss 상세" : "Details for oss", exact: true });
    await details.hover(); await assertLayout(pane, `${language}/${theme}/${width}: hover`);
    const beforeDetails = await layoutSnapshot(pane);
    await details.focus(); await details.press("Enter");
    await pane.getByRole("region", { name: await details.getAttribute("aria-label"), exact: true }).waitFor();
    assert.equal(await row.getAttribute("aria-pressed"), "false");
    await assertLayout(pane, `${language}/${theme}/${width}: details`);
    await assertPopup(pane, details, beforeDetails, `${language}/${theme}/${width}: details`);
    await details.press("Space");
    await row.click();
    const open = pane.getByRole("radio", { name: language === "ko" ? "열림" : "Open", exact: true });
    const closed = pane.getByRole("radio", { name: language === "ko" ? "닫힘" : "Closed", exact: true });
    await open.focus(); await open.press("ArrowRight");
    assert(await closed.isChecked(), "Native arrows select the next draft state");
    assert.equal(await closed.evaluate(node => getComputedStyle(node.nextElementSibling).backgroundColor), theme === "light" ? "rgb(255, 255, 255)" : "rgb(27, 33, 43)");
    await assertLayout(pane, `${language}/${theme}/${width}: selected`);
    await details.click();
    const detailsRegion = pane.getByRole("region", { name: await details.getAttribute("aria-label"), exact: true });
    for (const activation of ["mouse", "Enter", "Space"]) {
      const beforeRefresh = await page.evaluate(() => window.__prSidebarCatalogReads);
      if (activation === "mouse") await refresh.click();
      else { await refresh.focus(); await refresh.press(activation); }
      await page.waitForFunction(before => window.__prSidebarCatalogReads === before + 1, beforeRefresh);
      await page.waitForFunction(() => !document.querySelector(".pr-sidebar-refresh").disabled);
      assert.equal(await row.getAttribute("aria-pressed"), "true", "Refresh retains selection");
      assert(await closed.isChecked(), "Refresh retains filter state");
      assert.equal(await detailsRegion.isVisible(), false, "Outside activation dismisses the popup without altering retained expansion memory");
      if (!await detailsRegion.isVisible()) await details.click();
    }
    await refresh.blur();
    await page.mouse.move(0, 0);

    if (screenshots && width === 1440) await page.screenshot({ path: join(resolve(screenshots), `pr-${language}-${theme}-selected.png`) });
    if (width < 760) {
      await page.keyboard.press("Escape");
      assert(await pane.isVisible(), "The first Escape dismisses only Details");
      assert(await details.evaluate(node => document.activeElement === node), "Escape restores the opener");
      await page.keyboard.press("Escape");
      assert.equal(await pane.isVisible(), false, "The next Escape dismisses the original compact drawer");
      await page.locator(".sidebar-context-trigger").click();
      assert(await closed.isChecked(), "Drawer reopening retains draft state");
    }
    checks++;
  }
  // Half-sized CSS viewports exercise the effective layout at 200% zoom.
  for (const language of ["en", "ko"]) {
    await page.setViewportSize({ width: 480, height: 320 });
    await page.goto(`${origin}/?theme=dark&language=${language}&long=true`);
    const pane = await enter(language);
    // Fixture-only long title proves that localization can wrap beside the fixed action.
    await pane.locator(".pr-sidebar-title h2").evaluate(node => { node.textContent = node.textContent.repeat(8); });
    const last = pane.locator(".sidebar-repository-row").last();
    await last.click();
    const details = pane.locator(".pr-repository-details-toggle").last();
    const beforeLong = await layoutSnapshot(pane);
    await details.click();
    await assertPopup(pane, details, beforeLong, `${language}: long popup`);
    await assertLayout(pane, `${language}: long name/details at effective 200%`);
    const popup = pane.getByRole("region", { name: await details.getAttribute("aria-label"), exact: true });
    assert.equal(await popup.locator("dd").last().textContent(), await last.textContent(), "Details shows the safe repository name");
    await popup.focus(); await page.keyboard.press("Escape");
    assert.equal(await popup.isVisible(), false);
    assert(await pane.isVisible());
    checks++;
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${origin}/?empty=true`);
  const emptyPane = await enter("en");
  await emptyPane.getByText("No repositories on this page.", { exact: true }).waitFor();
  await assertLayout(emptyPane, "successful empty page");
  await emptyPane.getByRole("button", { name: "Repository settings", exact: true }).click();
  assert.equal(await page.getByRole("button", { name: "Repositories", exact: true }).getAttribute("aria-current"), "page");
  assert.equal(await page.locator(".sidebar-pull-requests").count(), 0, "Settings leaves the PR style scope");
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ operation: "pr-sidebar-layout", source, command: "node scripts/test-pr-sidebar-layout.mjs", checks: checks + 1, result: "passed", evidence: "synthetic Chromium; packaged CEF/native acceptance unperformed" }));
} finally {
  await browser?.close();
  await new Promise(done => server ? server.close(done) : done());
  await rm(directory, { recursive: true, force: true });
}
