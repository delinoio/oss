// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-transcript-layout-"));
const screenshots = process.env.DELIDEV_TRANSCRIPT_SCREENSHOT_DIR;
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/transcript-role-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const origin = `http://127.0.0.1:${server.address().port}`;
  const sizes = [{ width: 1600, height: 1000 }, { width: 960, height: 640 }, { width: 480, height: 320 }];
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark", "system"]) for (const size of sizes) {
    await page.emulateMedia({ colorScheme: "dark" });
    process.stdout.write(JSON.stringify({ operation: "transcript-role-case", language, theme, ...size }) + "\n");
    await page.setViewportSize(size);
    await page.goto(`${origin}/?language=${language}&theme=${theme}&workspace=${cases % 2 ? "worktree" : "general-chat"}`);
    await page.locator(".message-user").first().waitFor();
    const transcript = page.locator(".transcript");
    assert.equal(await transcript.locator(".message-user").count(), 2);
    assert.equal(await transcript.locator(".message-assistant").count(), 2);
    const measure = () => transcript.evaluate(root => {
      const rect = root.getBoundingClientRect(), style = getComputedStyle(root);
      const left = rect.left + parseFloat(style.paddingLeft), right = rect.left + root.clientWidth - parseFloat(style.paddingRight), width = right - left;
      const rows = [...root.querySelectorAll("article.message")];
      return { width, left, right, overflow: root.scrollWidth > root.clientWidth + 1, rows: rows.map(node => {
        const rect = node.getBoundingClientRect(), style = getComputedStyle(node);
        return { left: rect.left, right: rect.right, width: rect.width, top: rect.top, bottom: rect.bottom, role: node.className, background: style.backgroundColor, border: style.borderTopWidth, padding: style.paddingTop, radius: style.borderTopLeftRadius, color: style.color };
      }) };
    });
    // Visit all content-visibility roots before taking the complete geometry.
    for (const row of await transcript.locator("article.message").all()) await row.scrollIntoViewIfNeeded();
    const assertGeometry = async () => {
      const m = await measure();
      assert.equal(m.overflow, false, "Transcript content wraps within its scroll owner");
      for (const row of m.rows) {
        if (row.role.includes("message-user")) {
          assert(Math.abs(row.right - m.right) <= 1, "User is right-aligned");
          assert(row.width <= (m.width < 600 ? m.width * .9 : Math.min(m.width * .75, 720)) + 1, "User width follows available transcript cap");
          assert.equal(row.radius, "12px"); assert.equal(row.padding, "12px");
          assert.equal(row.color, "rgb(255, 255, 255)");
          assert.notEqual(row.background, "rgba(0, 0, 0, 0)");
        } else if (row.role.includes("message-assistant") || row.role.includes("message-claude-assistant") && row.background === "rgba(0, 0, 0, 0)") {
          assert(Math.abs(row.left - m.left) <= 1, "Assistant is left-aligned");
          assert.equal(row.border, "0px"); assert.equal(row.padding, "0px"); assert.equal(row.radius, "0px");
          assert.equal(row.background, "rgba(0, 0, 0, 0)");
        } else { assert.equal(row.border, "1px", "Other roots retain card treatment"); }
      }
      for (let index = 1; index < m.rows.length; index++) assert(Math.abs(m.rows[index].top - m.rows[index - 1].bottom - 16) <= 1, "Historical and live item spacing is 16px");
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No horizontal page overflow");
    };
    await assertGeometry();
    const summary = transcript.locator(".message-user details > summary").first();
    await summary.focus(); await page.keyboard.press("Enter");
    assert.equal(await summary.evaluate(node => node.parentElement.open), true, "Keyboard exposes original native details");
    assert.equal(await summary.evaluate(node => getComputedStyle(node).outlineStyle), "solid", "Keyboard focus is visible inside accent bubble");
    await page.keyboard.press("Enter");
    const composer = page.locator(".composer textarea"); await composer.focus();
    await page.getByRole("button", { name: "Fixture stream revision", exact: true }).evaluate(node => node.click());
    await page.getByText("Synthetic streaming text", { exact: true }).waitFor({ state: "attached" });
    assert(await composer.evaluate(node => node === document.activeElement), "Live append preserves composer focus");
    await page.getByRole("button", { name: "Fixture stream revision", exact: true }).evaluate(node => node.click());
    await page.getByText("Synthetic complete text", { exact: true }).waitFor({ state: "attached" });
    assert.equal(await page.getByText("Synthetic streaming text", { exact: true }).count(), 0);
    assert.equal(await transcript.locator(":scope > .message-assistant").count(), 1, "Streaming revision replaces one direct tail root");
    await transcript.locator(":scope > .message-assistant").scrollIntoViewIfNeeded();
    await assertGeometry();
    await page.getByRole("button", { name: language === "en" ? "Info" : "정보", exact: true }).click();
    await assertGeometry();
    await composer.scrollIntoViewIfNeeded();
    assert(await composer.isVisible(), "Composer remains reachable with tool panel open");
    assert.equal(await composer.inputValue(), "Retained synthetic draft");
    if (screenshots && size.width !== 480 && theme !== "system") {
      await mkdir(screenshots, { recursive: true });
      await page.getByRole("button", { name: language === "en" ? "Info" : "정보", exact: true }).click();
      await transcript.evaluate(node => { node.scrollTop = 0; });
      // Allow content-visibility to paint the restored top range before capture.
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      await page.screenshot({ path: join(screenshots, `${language}-${theme}-${size.width}.png`) });
    }
    cases++;
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({ operation: "transcript-role-layout", ...source, cases, screenshots, effectiveZoom: "half-viewport reflow", nativeAcceptance: "not-performed" }) + "\n");
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
