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
const directory = await mkdtemp(join(tmpdir(), "delidev-plan-mode-layout-"));
const screenshots = process.env.DELIDEV_PLAN_MODE_SCREENSHOT_DIR;
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/plan-mode-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const frame = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const records = async () => JSON.parse(await page.locator('output').textContent());
  const counts = async count => page.waitForFunction(expected => Number(document.querySelector('output').dataset.fixtureRequests) === expected, count);
  const choose = async (root, index) => {
    const control = root.getByRole('combobox').nth(index); await control.focus(); await page.keyboard.press('End'); await page.keyboard.press('Enter');
    await page.waitForFunction(node => Boolean(node.dataset.value), await control.elementHandle());
  };
  for (const language of ['en', 'ko']) for (const theme of ['light', 'dark', 'system']) for (const size of [{ width: 1600, height: 1000 }, { width: 1100, height: 800 }, { width: 960, height: 640 }, { width: 520, height: 640 }, { width: 480, height: 320 }]) {
    process.stdout.write(JSON.stringify({ operation: 'plan-mode-case', language, theme, ...size }) + '\n');
    await page.emulateMedia({ colorScheme: 'dark' }); await page.setViewportSize(size); await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    const label = language === 'en' ? 'Plan Mode' : '계획 모드';
    for (const surface of ['General Chat', 'New session', 'existing session']) {
      process.stdout.write(JSON.stringify({ operation: 'plan-mode-surface', surface, phase: 'open' }) + '\n');
      await page.getByRole('button', { name: `Fixture ${surface}`, exact: true }).click();
      const root = surface === 'existing session' ? page.locator('.plan-mode-session') : surface === 'General Chat' ? page.locator('.new-general-chat-page') : page.locator('.new-session-page:not(.new-general-chat-page)');
      const mode = root.getByRole('checkbox', { name: label, exact: true }); await mode.waitFor();
      assert.equal(await root.locator('select').count(), 0, 'Composer Mode dropdown is removed');
      assert.equal(await mode.isChecked(), false, 'Fresh independent composer starts unchecked');
      if (surface !== 'existing session') {
        if (size.width <= 1100) assert.equal(await root.locator('.new-session-toolbar').evaluate(node => getComputedStyle(node).flexDirection), 'column', '1100px toolbar wraps into rows');
        if (size.width <= 520) assert.equal(await root.locator('.new-session-selectors').evaluate(node => getComputedStyle(node).flexDirection), 'column', '520px selectors stack');
      }
      let count = (await records()).length;
      await mode.locator('..').click(); assert(await mode.isChecked());
      assert.equal((await records()).length, count, 'Label toggle sends no request');
      await mode.focus(); await page.keyboard.press('Space'); assert.equal(await mode.isChecked(), false);
      assert.equal(await mode.evaluate(node => getComputedStyle(node).outlineStyle), 'solid', 'Native checkbox keyboard focus is visible');
      assert(await mode.evaluate(node => node === document.activeElement));
      assert.equal((await records()).length, count, 'Space toggle sends no request');
      if (surface !== 'existing session') { const first = surface === 'New session' ? 1 : 0; await choose(root, first); await choose(root, first + 1); }
      const submit = root.locator(surface === 'existing session' ? '.composer-actions .primary' : '.new-session-submit');
      const prompt = root.locator('textarea');
      for (const [index, expected] of ['execute', 'plan', 'execute'].entries()) {
        if (index) await mode.click();
        await prompt.fill(`Synthetic ${surface} ${expected} ${index}`); await submit.click(); await counts(++count);
        const record = (await records()).at(-1); assert.equal(record.document.mode, expected);
        assert.equal(record.operation, surface === 'existing session' ? 'enqueue' : 'create');
        await page.waitForFunction(node => !node.matches(':disabled'), await mode.elementHandle());
      }
      process.stdout.write(JSON.stringify({ operation: 'plan-mode-surface', surface, phase: 'pending' }) + '\n');
      await mode.check(); await page.getByRole('button', { name: 'Fixture hold request', exact: true }).click();
      await prompt.fill('Synthetic pending Plan draft'); await submit.click(); await counts(++count);
      assert(await mode.isDisabled()); assert(await mode.isChecked()); assert(await prompt.isDisabled());
      assert.equal((await records()).at(-1).document.mode, 'plan');
      await page.getByRole('button', { name: 'Fixture accept request', exact: true }).click();
      await page.waitForFunction(node => !node.matches(':disabled'), await mode.elementHandle());
      assert(await mode.isChecked());
      process.stdout.write(JSON.stringify({ operation: 'plan-mode-surface', surface, phase: 'uncertain' }) + '\n');
      await page.getByRole('button', { name: 'Fixture fail request', exact: true }).click();
      await prompt.fill('Synthetic uncertain Plan draft'); await submit.click(); await counts(++count);
      const retry = root.getByRole('button', { name: surface === 'existing session' ? language === 'en' ? 'Retry the same message' : '같은 메시지 다시 시도' : language === 'en' ? 'Retry the same session creation' : '동일한 세션 생성 다시 시도', exact: true });
      const original = (await records()).at(-1);
      await page.waitForFunction(node => node.matches(':disabled'), await mode.elementHandle());
      assert(await mode.isChecked()); assert(await prompt.isDisabled());
      await page.getByRole('button', { name: 'Fixture accept request', exact: true }).click();
      await retry.click(); await counts(++count); assert.deepEqual((await records()).at(-1), original, 'Uncertain retry retains the exact request and Plan mode');
      await page.waitForFunction(node => !node.matches(':disabled'), await mode.elementHandle());
      await mode.uncheck(); await mode.focus(); await frame();
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'No horizontal page clipping');
      assert(await page.locator('main').evaluate(node => node.scrollWidth <= node.clientWidth + 1), 'No horizontal main clipping');
      await submit.scrollIntoViewIfNeeded(); assert(await submit.isVisible(), 'Submit stays reachable');
      if (screenshots && theme !== 'system' && size.width !== 480 && size.width !== 520) {
        await mkdir(screenshots, { recursive: true }); await page.screenshot({ path: join(screenshots, `${language}-${theme}-${size.width}-${surface.replaceAll(' ', '-')}.png`) });
      }
    }
    cases++;
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({ operation: 'plan-mode-layout', ...source, cases, composers: cases * 3, screenshots, effectiveZoom: 'half-viewport reflow', nativeAcceptance: 'not-performed' }) + '\n');
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
