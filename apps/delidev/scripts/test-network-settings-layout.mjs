// SPDX-License-Identifier: Apache-2.0
// Host-supplied headless Chromium validates the real App/Settings inline Server preferences network flow
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
const directory = await mkdtemp(join(tmpdir(), "delidev-network-settings-layout-"));
const screenshots = process.env.DELIDEV_NETWORK_SETTINGS_SCREENSHOT_DIR;
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
  await page.addInitScript(() => { document.addEventListener("securitypolicyviolation", event => { if (event.violatedDirective === "style-src-elem" || event.violatedDirective === "style-src-attr") console.error("network_settings_style_csp_failure"); }); });
  page.on("console", message => { if (message.text() === "network_settings_style_csp_failure") failures.push("style CSP violation"); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const catalogs = {};
  for (const language of ["en", "ko"]) catalogs[language] = JSON.parse(await readFile(join(app, `src/locales/${language}/network-settings.json`), "utf8"));
  const select = async category => {
    if (await page.locator(".sidebar-context-trigger").isVisible()) await page.locator(".sidebar-context-trigger").click();
    await page.locator(`[data-settings-category="${category}"]`).click();
  };
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1280,820], [960,640], [640,480], [480,320]]) {
    const copy = key => catalogs[language][`network-settings.${key}`];
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?populated=true&networkFixture=populated&theme=${theme}&language=${language}`);
    await page.getByRole("button", { name: language === "en" ? "Settings" : "설정", exact: true }).click();
    await select("server-preferences");
    const opener = page.getByRole("button", { name: copy("networkSettings_600f22"), exact: true });
    await opener.waitFor().catch(async error => { console.error(await page.locator("body").innerText()); console.error(failures); throw error; });
    assert.deepEqual(await page.evaluate(() => window.networkFixtureReads), { route: 0, profiles: 0, writes: 0 });
    await opener.focus(); await opener.press("Enter");
    await page.getByRole("heading", { name: copy("currentRoute"), exact: true }).waitFor();
    await page.locator(".network-profile-row").waitFor();
    assert.equal(await page.getByRole("dialog").count(), 0, "Network expands without an outer modal");
    const picker = page.getByRole("combobox", { name: copy("profileToSelect_7e2a31"), exact: true });
    assert.equal(await picker.evaluate(node => node.tagName), "SELECT");
    assert.equal(await page.locator(".network-transfer").evaluate(node => node.open), false);
    const mountedRecipient = page.locator(".network-transfer input[type=file]");
    assert.equal(await mountedRecipient.count(), 1, "Collapsed transfer keeps the workflow mounted");
    const transfer = page.locator(".network-transfer summary");
    await transfer.focus(); await transfer.press("Enter");
    assert(await mountedRecipient.isVisible());
    await transfer.press("Enter");
    assert.equal(await mountedRecipient.count(), 1);
    await picker.selectOption({ index: 1 });
    await page.waitForFunction(() => !document.querySelector(".network-selection fieldset").disabled);
    assert.equal((await page.evaluate(() => window.networkFixtureReads)).writes, 0, "Choosing a revision reads but does not mutate");
    await page.getByRole("button", { name: copy("newNetworkProfile_100d40"), exact: true }).click();
    const dialog = page.getByRole("dialog"); await dialog.waitFor();
    assert.equal(await dialog.count(), 1, "The profile editor owns one independent modal");
    await dialog.press("Escape"); await dialog.waitFor({ state: "hidden" });
    assert(await page.getByRole("heading", { name: copy("currentRoute"), exact: true }).isVisible());
    assert(await page.getByRole("button", { name: copy("newNetworkProfile_100d40"), exact: true }).evaluate(node => document.activeElement === node));
    await page.keyboard.press("Escape"); assert(await opener.isVisible(), "Page Escape retains Settings");
    const geometry = await page.locator(".settings-content").evaluate(node => ({ width: node.clientWidth, scroll: node.scrollWidth }));
    assert(geometry.scroll <= geometry.width + 1, JSON.stringify({ width, geometry }));
    assert(await page.locator(".network-profile-row").evaluate(node => node.scrollWidth <= node.clientWidth + 1), "Full names wrap without horizontal clipping");
    assert(await picker.evaluate(node => node.getBoundingClientRect().height >= 40));
    const selectionLayout = await picker.evaluate(node => ({ width: node.closest("form").clientWidth, bottom: node.getBoundingClientRect().bottom, buttonTop: node.closest("fieldset").querySelector("button").getBoundingClientRect().top }));
    if (selectionLayout.width < 640) assert(selectionLayout.buttonTop >= selectionLayout.bottom, "Narrow available form width stacks explicit selection");
    if (screenshots && width >= 640) { await mkdir(resolve(screenshots), { recursive: true }); await page.screenshot({ path: join(resolve(screenshots), `network-${language}-${theme}-${width}.png`) }); }
    await opener.click(); assert.equal(await page.locator(".network-workspace").count(), 0);
    await opener.click(); await page.locator(".network-profile-row").waitFor();
    assert.equal(await picker.inputValue(), "", "Reopening discards selected revisions without replay");
    assert.equal((await page.evaluate(() => window.networkFixtureReads)).writes, 0);
    checks++;
  }
  for (const language of ["en", "ko"]) {
    const copy = key => catalogs[language][`network-settings.${key}`];
    await page.setViewportSize({ width: 1280, height: 820 });
    await page.goto(`${origin}/?networkFixture=empty&language=${language}`);
    await page.getByRole("button", { name: language === "en" ? "Settings" : "설정", exact: true }).click(); await select("server-preferences");
    await page.getByRole("button", { name: copy("networkSettings_600f22"), exact: true }).click();
    await page.getByText(copy("noProfiles"), { exact: true }).waitFor();
    assert.equal(await page.locator(".network-profile-row").count(), 0);
    assert.equal(await page.locator(".network-workspace button").filter({ hasText: /^First$|^Next$/ }).count(), 0);
    checks++;
  }
  assert.deepEqual(failures, []);
  console.log(`Inline network layout: ${checks} bilingual/theme/viewport/empty scenarios passed; packaged CEF acceptance was not performed.`);
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
