// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
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
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-appearance-colors-"));
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/appearance-colors.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  page.on("pageerror", error => errors.push(error.message));
  page.setDefaultTimeout(15000);
  const origin = `http://127.0.0.1:${server.address().port}`;
  const palettes = JSON.parse(await readFile(join(app, "src/appearance-palettes.json"), "utf8"));
  const custom = { light: { ...palettes.nord.light, background: "#EEF2F6", accent: "#2244AA" }, dark: { ...palettes.dracula.dark, background: "#161B26", text: "#F6F8FC", accent: "#6644BB" } };
  const values = () => page.evaluate(() => {
    const style = getComputedStyle(document.documentElement);
    return { theme: document.documentElement.dataset.theme, colors: Object.fromEntries(["background", "text", "accent"].map(token => [token, style.getPropertyValue(`--${token}`).trim().toUpperCase()])), sheets: document.adoptedStyleSheets.length, unrelated: style.getPropertyValue("--fixture-unrelated").trim(), preview: getComputedStyle(document.querySelector('[data-preview="owned"]')).getPropertyValue("--background").trim().toUpperCase() };
  });
  async function check(mode, kind, dark) {
    const expected = (kind === "custom" ? custom : kind === "default" ? palettes.default : dark ? palettes.dracula : palettes.nord)[dark ? "dark" : "light"];
    await page.waitForFunction(({ theme, background }) => document.documentElement.dataset.theme === theme && getComputedStyle(document.documentElement).getPropertyValue("--background").trim().toUpperCase() === background.toUpperCase(), { theme: dark ? "dark" : "light", background: expected.background });
    const actual = await values();
    assert.deepEqual(actual.colors, Object.fromEntries(["background", "text", "accent"].map(token => [token, expected[token].toUpperCase()])));
    assert.equal(actual.sheets, 3, "Replacement removes only the original owner sheet");
    assert.equal(actual.unrelated, "retained");
    assert.equal(actual.preview, palettes.solarized.light.background.toUpperCase(), "Preview stays scoped");
    cases++; process.stdout.write(JSON.stringify({ operation: "appearance-colors-case", mode, kind, dark }) + "\n");
  }
  for (const owner of ["main", "tray"]) {
    await page.emulateMedia({ colorScheme: "dark" }); await page.goto(`${origin}/${owner === "tray" ? "?tray" : ""}`);
    await check("dark", "bundled", true);
    for (const kind of ["bundled", "custom", "default"]) {
      for (const mode of ["dark", "light", "system"]) {
        await page.getByRole("button", { name: `${mode}-${kind}`, exact: true }).click();
        await check(mode, kind, mode !== "light");
        if (mode === "system") {
          await page.emulateMedia({ colorScheme: "light" }); await check(mode, kind, false);
          await page.emulateMedia({ colorScheme: "dark" }); await check(mode, kind, true);
        }
      }
    }
    await page.getByRole("button", { name: "Dispose owner", exact: true }).click();
    await page.waitForFunction(() => document.adoptedStyleSheets.length === 2);
    assert.equal((await values()).colors.background, palettes.default.dark.background.toUpperCase(), "Disposal restores static dark colors");
    assert.equal((await values()).unrelated, "retained");
    await page.getByRole("button", { name: "Dispose preview", exact: true }).click();
    assert.equal((await values()).sheets, 1);
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({ operation: "appearance-colors", ...source, cases, nativeAcceptance: "not-performed" }) + "\n");
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
