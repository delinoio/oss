// SPDX-License-Identifier: Apache-2.0
// Host-supplied headless Chromium validates the real App/Settings account-storage flow
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
const directory = await mkdtemp(join(tmpdir(), "delidev-account-storage-layout-"));
const screenshots = process.env.DELIDEV_ACCOUNT_STORAGE_SCREENSHOT_DIR;
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
  await page.addInitScript(() => { document.addEventListener("securitypolicyviolation", event => { if (event.violatedDirective === "style-src-elem" || event.violatedDirective === "style-src-attr") console.error("account_storage_style_csp_failure"); }); });
  page.on("console", message => { if (message.text() === "account_storage_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const catalogs = {};
  for (const language of ["en", "ko"]) catalogs[language] = JSON.parse(await readFile(join(app, `src/locales/${language}/doctor.json`), "utf8"));
  const select = async category => {
    if (await page.locator(".sidebar-context-trigger").isVisible()) await page.locator(".sidebar-context-trigger").click();
    await page.locator(`[data-settings-category="${category}"]`).click();
  };
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,1000], [960,640], [480,320]]) {
    const copy = key => catalogs[language][`account-storage.${key}`];
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?populated=true&apiUsage=true&accountStorage=true&theme=${theme}&language=${language}`);
    await page.getByRole("button", { name: language === "en" ? "Settings" : "설정", exact: true }).click();
    await page.getByText(copy("subscription"), { exact: true }).waitFor();
    assert.equal(await page.locator(".account-storage-summary[role=alert]").count(), 0, "Unsupported subscription inspection is informational");
    await select("api-accounts");
    const failure = page.getByText(copy("failed"), { exact: true }); await failure.waitFor();
    assert.equal(await page.locator(".account-storage-summary[role=alert]").count(), 1, "Only the exact failed row is an alert");
    assert.equal(await page.locator(".api-entry-row").filter({ has: failure }).count(), 1, "The alert belongs to its account row");
    await page.getByText(copy("keyless"), { exact: true }).first().waitFor();
    const details = page.locator(".account-storage-notice[data-failed] details"), summary = details.locator("summary");
    assert.equal(await details.evaluate(node => node.open), false);
    await summary.focus(); await summary.press("Enter"); assert.equal(await details.evaluate(node => node.open), true);
    const layout = await page.locator(".account-storage-notice[data-failed]").evaluate(node => ({ overflow: node.scrollWidth > node.clientWidth, right: node.getBoundingClientRect().right, background: getComputedStyle(node).backgroundColor, inline: Boolean(node.getAttribute("style")), focus: document.activeElement?.tagName }));
    assert(!layout.overflow && layout.right <= width + 1 && !layout.inline, JSON.stringify(layout));
    assert.equal(layout.focus, "SUMMARY"); assert.equal(layout.background, theme === "light" ? "rgb(255, 243, 244)" : "rgb(57, 33, 39)");
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), "Settings content reflows");
    assert(await page.getByRole("button", { name: copy("refresh"), exact: true }).evaluate(node => node.getBoundingClientRect().height >= 40), "Refresh control is accessible");
    await summary.press("Enter"); assert.equal(await details.evaluate(node => node.open), false);
    if (screenshots && width >= 960) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `account-storage-${language}-${theme}-${width}.png`) }); }
    await select("diagnostics"); await page.getByText(copy("destination"), { exact: true }).waitFor();
    assert.equal(await page.locator(".account-storage-notice").count(), 0, "Doctor does not duplicate account records");
    await page.getByRole("region", { name: copy("heading"), exact: true }).getByRole("button", { name: copy("apiKeys"), exact: true }).click();
    await page.getByText(copy("failed"), { exact: true }).waitFor();
    assert.equal(await page.locator(".account-storage-notice[data-failed] details").evaluate(node => node.open), false, "Category departure clears disclosures");
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(JSON.stringify({ fixture: "account-storage", checks, effectiveZoomViewport: "480x320 represents 960x640 at 200%", nativeAcceptance: false }));
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
