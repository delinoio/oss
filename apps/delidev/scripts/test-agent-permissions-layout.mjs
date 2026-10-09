// SPDX-License-Identifier: Apache-2.0
// Synthetic browser geometry only; no native, account or execution acceptance.
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
let browser, server, checks = 0;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/agent-permissions-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const page = await browser.newPage(); const errors = [], requests = []; page.on('pageerror', error => errors.push(error.message)); page.on('request', request => requests.push(request.url()));
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const language of ['en', 'ko']) for (const theme of ['light', 'dark', 'system']) for (const editing of [false, true]) for (const harness of ['codex', 'claude-code', 'opencode', 'grok-build']) for (const size of [{ width: 1440, height: 1000 }, { width: 960, height: 640 }, { width: 480, height: 320 }]) {
    process.stdout.write(JSON.stringify({ operation: 'permission-case', language, theme, editing, harness, ...size }) + '\n');
    await page.emulateMedia({ colorScheme: 'dark' }); await page.setViewportSize(size); await page.goto(`${origin}/?language=${language}&theme=${theme}&editing=${editing}&harness=${harness}&unsupported=${editing}`);
    const source = page.getByRole('combobox', { name: 'Account source', exact: true }); await source.waitFor();
    const permission = page.locator('.agent-permissions [role=combobox]');
    const original = JSON.parse(await page.locator('output').getAttribute('data-options')); if (editing) assert.equal(await permission.getAttribute('data-value'), 'future-mode');
    const style = async () => page.locator('.scroll-picker-popup').evaluate(node => { const css = getComputedStyle(node); return { background: css.backgroundColor, border: css.borderColor, radius: css.borderRadius }; });
    await source.click(); const treatment = await style(); await source.press('Escape');
    await permission.focus(); await page.keyboard.press('Tab'); await permission.focus(); assert(await permission.evaluate(node => node.matches(':focus-visible') && parseFloat(getComputedStyle(node).outlineWidth) > 0));
    const beforeReads = requests.length; await permission.press('ArrowDown'); assert.deepEqual(await style(), treatment);
    const ids = await page.locator('.agent-permissions [role=option]').evaluateAll(rows => rows.map(row => row.dataset.pickerId));
    const expected = harness === 'codex' ? ['default', 'read-only', 'workspace-write', 'full-access'] : harness === 'claude-code' ? ['default', 'plan', 'acceptEdits', 'dontAsk', 'bypassPermissions', 'auto'] : ['default'];
    assert.deepEqual(ids, [...(editing ? ['future-mode'] : []), ...expected]);
    await permission.press('End'); await permission.press('Home'); assert.deepEqual(JSON.parse(await page.locator('output').getAttribute('data-options')), original);
    const geometry = await page.locator('.scroll-picker-popup').evaluate(node => { const b = node.getBoundingClientRect(); return { left: b.left, right: b.right, top: b.top, bottom: b.bottom, width: innerWidth, height: innerHeight, overflow: node.scrollWidth > node.clientWidth + 1 }; });
    assert(geometry.left >= 0 && geometry.right <= geometry.width + 1 && geometry.top >= 0 && geometry.bottom <= geometry.height + 1 && !geometry.overflow, JSON.stringify(geometry));
    await permission.press('Escape'); assert.equal(await permission.getAttribute('aria-expanded'), 'false'); assert(await permission.evaluate(node => node === document.activeElement));
    for (const id of expected) { await permission.click(); await page.locator(`.agent-permissions [data-picker-id="${id}"]`).click(); assert.equal(await permission.getAttribute('data-value'), id); assert(await permission.evaluate(node => node === document.activeElement)); }
    assert.equal(requests.length, beforeReads); const saved = JSON.parse(await page.locator('output').getAttribute('data-options')); assert.equal(saved.future_option, 'keep');
    await permission.click(); await page.getByRole('button', { name: 'Fixture pending', exact: true }).click(); assert(await permission.isDisabled()); assert.equal(await page.locator('.agent-permissions [role=listbox]').count(), 0);
    assert(await page.locator('main').evaluate(node => node.scrollWidth <= node.clientWidth + 1)); checks++;
  }
  assert.deepEqual(errors, []); console.log(JSON.stringify({ operation: 'agent_permission_layout', result: 'passed', checks, effectiveZoom: 'half-viewport reflow', nativeAcceptance: 'not-performed' }));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
