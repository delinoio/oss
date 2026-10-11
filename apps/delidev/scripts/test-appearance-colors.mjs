// SPDX-License-Identifier: Apache-2.0
// CI-only synthetic Chromium checks; no native/account acceptance or screenshots.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = { revision: execFileSync("git", ["rev-parse", "HEAD"], { cwd: app, encoding: "utf8" }).trim(), dirty: Boolean(execFileSync("git", ["status", "--porcelain"], { cwd: app, encoding: "utf8" }).trim()) };
const palettes = JSON.parse(await readFile(join(app, "src/appearance-palettes.json"), "utf8"));
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-appearance-colors-"));
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/appearance-colors.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.waitForFunction(() => document.adoptedStyleSheets.length === 1 && window.appearanceColorsFixture);
  await page.locator("textarea").evaluate(node => { window.originalAppearanceDraft = node; });
  // Foreign sheets must survive replacement and disposal of this owner.
  await page.evaluate(() => { const sheet = new CSSStyleSheet(); sheet.replaceSync(":root { --unrelated-appearance-test: retained; }"); window.foreignAppearanceSheet = sheet; document.adoptedStyleSheets = [sheet, ...document.adoptedStyleSheets]; });
  const rgb = hex => `rgb(${[1, 3, 5].map(index => parseInt(hex.slice(index, index + 2), 16)).join(", ")})`;
  async function assertMap(expected, mode, mounted = true) {
    await page.waitForFunction(({ background, mode, mounted }) => document.documentElement.dataset.theme === mode && getComputedStyle(document.documentElement).getPropertyValue("--background").trim().toUpperCase() === background.toUpperCase() && document.adoptedStyleSheets.length === (mounted ? 2 : 1), { background: expected.background, mode, mounted });
    const actual = await page.evaluate(() => {
      const root = getComputedStyle(document.documentElement), probe = document.getElementById("appearance-probe"), style = probe && getComputedStyle(probe);
      return { colors: Object.fromEntries(["background", "surface", "text", "accent", "conversation-background"].map(token => [token, root.getPropertyValue(`--${token}`).trim().toUpperCase()])), foreign: document.adoptedStyleSheets.includes(window.foreignAppearanceSheet), background: style?.backgroundColor, text: style?.color, accent: style?.borderTopColor, retained: !probe || document.querySelector("textarea") === window.originalAppearanceDraft };
    });
    for (const [token, value] of Object.entries(actual.colors)) assert.equal(value, expected[token].toUpperCase(), `${mode}: ${token}`);
    assert(actual.foreign, "Unrelated adopted sheet survives");
    assert(actual.retained, "Mode/palette changes preserve mounted draft");
    if (mounted) { assert.equal(actual.background, rgb(expected.background)); assert.equal(actual.text, rgb(expected.text)); assert.equal(actual.accent, rgb(expected.accent)); }
    checks++;
  }
  const commit = (theme, custom = false, defaults = false) => page.evaluate(({ theme, custom, defaults }) => window.appearanceColorsFixture.commit(theme, custom, defaults), { theme, custom, defaults });
  await assertMap(palettes.dracula.dark, "dark");
  await commit("dark", true);
  const custom = { ...palettes.default.dark, background: "#201B2D", text: "#E8EDF6", accent: "#6B21A8" };
  await assertMap(custom, "dark");
  await page.emulateMedia({ colorScheme: "light" });
  await assertMap(custom, "dark"); // Explicit Dark ignores OS changes.
  await commit("system");
  await assertMap(palettes.nord.light, "light");
  await page.emulateMedia({ colorScheme: "dark" });
  await assertMap(palettes.dracula.dark, "dark");
  await page.emulateMedia({ colorScheme: "light" });
  await assertMap(palettes.nord.light, "light");
  await commit("system", true);
  await page.emulateMedia({ colorScheme: "dark" });
  await assertMap(custom, "dark");
  await commit("light");
  await assertMap(palettes.nord.light, "light");
  await commit("dark", false, true);
  await assertMap(palettes.default.dark, "dark");
  await commit("light", false, true);
  await assertMap(palettes.default.light, "light");
  await commit("dark", true);
  await assertMap(custom, "dark");
  await page.evaluate(() => window.appearanceColorsFixture.unmount());
  await assertMap(palettes.default.dark, "dark", false);
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: "appearance-color-cascade", source, checks, result: "passed", evidence: "synthetic Chromium computed styles with production themes.css and AppearanceProvider; no native acceptance or screenshots" }));
} finally {
  await browser?.close();
  await new Promise(done => server ? server.close(done) : done());
  await rm(directory, { recursive: true, force: true });
}
