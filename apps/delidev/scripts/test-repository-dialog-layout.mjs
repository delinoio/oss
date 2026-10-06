// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-repository-dialog-"));
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
  const errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  // Half-sized CSS viewports model effective 200% layout. Native/browser chrome
  // zoom and packaged CEF behavior require independent platform acceptance.
  for (const theme of ["light", "dark"]) for (const [width, height] of [[1440, 900], [960, 640], [640, 480], [480, 320]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?theme=${theme}&repository-pat=true&populated=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const categories = page.getByRole("button", { name: "Open settings categories", exact: true });
    if (await categories.isVisible()) await categories.click();
    await page.getByRole("button", { name: "Repositories", exact: true }).click();
    const opener = page.getByRole("button", { name: "Add repository", exact: true });
    await opener.click();
    const dialog = page.getByRole("dialog", { name: "Add repository", exact: true });
    await dialog.waitFor();
    assert(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).evaluate(node => node === document.activeElement));
    assert(await page.locator(".settings-content h1").filter({ hasText: /^Repositories$/ }).isVisible());
    assert(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).isVisible());
    assert.equal(await dialog.getByRole("textbox", { name: "Clone to", exact: true }).count(), 0);
    await dialog.getByRole("button", { name: "Choose from GitHub", exact: true }).waitFor();
    const layout = await dialog.evaluate(node => {
      const rect = node.getBoundingClientRect(), style = getComputedStyle(node);
      return { width: rect.width, height: rect.height, top: rect.top, padding: style.paddingLeft, overflow: node.scrollWidth > node.clientWidth, scroll: node.querySelector(".settings-task-body").scrollHeight > node.querySelector(".settings-task-body").clientHeight, title: getComputedStyle(node.querySelector("header h2")).fontSize, controls: [...node.querySelectorAll("button,input")].every(control => control.getBoundingClientRect().height >= 39.5) };
    });
    assert(layout.width <= 640.5 && layout.width <= width - 31 && layout.height <= height - 31 && layout.top >= 15 && !layout.overflow && layout.controls, JSON.stringify(layout));
    assert.equal(layout.padding, "0px"); assert.equal(layout.title, "22px");
    if (height <= 480) assert(layout.scroll, "Small viewports require vertical dialog scrolling");
    for (const key of ["Tab", "Shift+Tab"]) for (let step = 0; step < 14; step++) { await page.keyboard.press(key); assert(await dialog.evaluate(node => node.contains(document.activeElement)), "Focus escaped the native dialog"); }
    await dialog.getByRole("textbox", { name: "Git URL", exact: true }).fill("https://github.com/owner/repo.git");
    assert(await dialog.getByRole("button", { name: "Add repository", exact: true }).isEnabled());
    await dialog.getByRole("button", { name: "Clone to this computer (optional)", exact: true }).click();
    await dialog.getByRole("textbox", { name: "Clone to", exact: true }).fill("/parent");
    await dialog.getByText("Final path: /parent/repo", { exact: true }).waitFor();
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "detached" });
    assert(await opener.evaluate(node => node === document.activeElement), "Opener focus was not restored");
    await opener.click();
    assert.equal(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).inputValue(), "");
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: "repository_dialog_layout", result: "passed", cases, themes: 2, viewports: 4, nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
