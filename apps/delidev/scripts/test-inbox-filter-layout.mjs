// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF or native acceptance.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-inbox-filter-layout-"));
const screenshots = process.env.DELIDEV_INBOX_FILTER_SCREENSHOT_DIR;
let browser, server, checks = 0;
const failures = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/inbox-filter-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [960,768], [390,844], [720,450]]) {
    const context = `${language}/${theme}/${width}x${height}`;
    const label = (en, ko) => language === "ko" ? ko : en;
    await page.setViewportSize({ width, height }); await page.goto(`${origin}/?theme=${theme}&language=${language}`);
    const opener = page.locator(".sidebar-context-trigger"); if (await opener.isVisible()) await opener.click();
    await page.getByRole("button", { name: label("Inbox", "받은 요청"), exact: true }).click();
    if (await opener.isVisible()) await opener.click();
    const pane = page.locator(".sidebar-surface-content").filter({ has: page.getByRole("button", { name: label("Reset", "초기화"), exact: true }) });
    await pane.waitFor();
    assert.equal(await pane.getByRole("button", { name: /Apply filters|필터 적용/ }).count(), 0, context);
    const unread = pane.getByRole("button", { name: label("Unread", "읽지 않음"), exact: true });
    await unread.focus(); await unread.press("Enter");
    await page.waitForFunction(() => window.__inboxFilterFixture.requests.at(-1)?.readState === 1);
    assert.equal(await unread.evaluate(node => node === document.activeElement), true, context);
    assert.equal(await pane.isVisible(), true, context);
    const source = pane.getByRole("combobox", { name: label("Source", "출처"), exact: true });
    await source.selectOption("1");
    await page.waitForFunction(() => window.__inboxFilterFixture.requests.at(-1)?.source === 1);
    await pane.getByRole("combobox", { name: label("Project", "프로젝트"), exact: true }).click();
    await page.getByRole("option", { name: "Synthetic project", exact: true }).click();
    await page.waitForFunction(() => window.__inboxFilterFixture.requests.at(-1)?.projectId.endsWith("000002"));
    const reset = pane.getByRole("button", { name: label("Reset", "초기화"), exact: true });
    await reset.focus(); await reset.press("Enter");
    await page.waitForFunction(() => { const value = window.__inboxFilterFixture.requests.at(-1); return value?.readState === 0 && value.source === 0 && value.projectId === "" && value.sessionId === "" && value.pageToken === ""; });
    assert.equal(await reset.evaluate(node => node === document.activeElement), true, context);
    assert.equal(await pane.isVisible(), true, context);
    if (width < 760) assert.equal(await page.locator(".sidebar-pane-dialog").evaluate(node => node.open), true, context);
    assert.equal(await page.evaluate(() => window.__inboxFilterFixture.writes), 0, context);
    if (screenshots) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `filters-${width}-${language}-${theme}.png`) }); }
    checks++; console.log(JSON.stringify({ operation: "inbox-filter-layout-case", context, result: "passed" }));
  }
  assert.deepEqual(failures, [], "No browser errors");
  console.log(JSON.stringify({ operation: "inbox-filter-layout", source, result: "passed", checks, screenshots: screenshots ?? null }));
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
