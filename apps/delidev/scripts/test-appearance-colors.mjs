// SPDX-License-Identifier: Apache-2.0
// Run on a browser-equipped CI host. Synthetic data grants no native acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const module = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
if (!module) throw new Error("DELIDEV_LAYOUT_PLAYWRIGHT_MODULE must name the CI Playwright module");
const { chromium } = await import(pathToFileURL(resolve(module)).href);
const palettes = JSON.parse(await readFile(join(app, "src/appearance-palettes.json"), "utf8"));
const directory = await mkdtemp(join(tmpdir(), "delidev-appearance-colors-"));
let server, browser;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/appearance-colors.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`);
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.waitForFunction(() => document.adoptedStyleSheets.length === 1 && document.documentElement.dataset.theme === "dark");
  const check = async colors => {
    const expected = Object.fromEntries(["background", "text", "accent"].map(token => [token, colors[token].toLowerCase()]));
    await page.waitForFunction(expected => Object.entries(expected).every(([token, value]) => getComputedStyle(document.documentElement).getPropertyValue(`--${token}`).trim().toLowerCase() === value), expected);
    assert.equal(await page.evaluate(() => document.adoptedStyleSheets.length), 1, "Palette replacement must dispose its old sheet");
  };
  await check(palettes.dracula.dark);
  await page.evaluate(() => window.appearanceFixture.select("dark", true));
  await check({ ...palettes.default.dark, background: "#111111", accent: "#000000" });
  await page.evaluate(() => window.appearanceFixture.select("system", false));
  await check(palettes.dracula.dark);
  await page.emulateMedia({ colorScheme: "light" });
  await check(palettes.nord.light);
  await page.emulateMedia({ colorScheme: "dark" });
  await check(palettes.dracula.dark);
  await page.evaluate(() => window.appearanceFixture.select("light", false));
  await check(palettes.nord.light);
  await page.evaluate(() => window.appearanceFixture.dispose());
  assert.equal(await page.evaluate(() => document.adoptedStyleSheets.length), 0);
  assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--background").trim().toLowerCase()), palettes.default.light.background.toLowerCase());
  console.log(JSON.stringify({ operation: "appearance_colors", result: "passed", nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
