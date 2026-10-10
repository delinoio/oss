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
const directory = await mkdtemp(join(tmpdir(), "delidev-general-chat-layout-"));
const screenshots = undefined;
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/general-chat-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const frame = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  let overlayCases = 0;
  const surrounding = root => root.evaluate(node => {
    const selectors = ['.new-session-header', '.new-session-content', 'fieldset', '.new-session-composer', '.composer', 'textarea', '.new-session-toolbar', '.new-session-hints', '.actions'];
    return selectors.flatMap(selector => [...node.querySelectorAll(selector)].map(element => {
      const box = element.getBoundingClientRect(); return { selector, x: box.x, y: box.y, width: box.width, height: box.height, scrollTop: element.scrollTop };
    }));
  });
  const unchanged = (before, after) => { assert.equal(after.length, before.length); before.forEach((rect, index) => { for (const field of ['x', 'y', 'width', 'height', 'scrollTop']) assert(Math.abs(rect[field] - after[index][field]) <= 1, JSON.stringify({ rect, after: after[index], field })); }); };
  const bounded = async (root, input) => {
    const placement = await input.evaluate(node => {
      const rect = node.getBoundingClientRect(), panel = node.closest('fieldset, form, [data-shared]').querySelector('.skill-completion'), box = panel.getBoundingClientRect();
      return { anchor: { left: rect.left, top: rect.top, bottom: rect.bottom, width: rect.width }, box: { left: box.left, top: box.top, right: box.right, bottom: box.bottom, width: box.width, height: box.height }, open: panel.matches(':popover-open'), viewport: { width: innerWidth, height: innerHeight } };
    });
    assert(placement.open); assert(placement.box.height <= 220.1);
    assert(placement.box.left >= 7 && placement.box.top >= 7 && placement.box.right <= placement.viewport.width - 7 && placement.box.bottom <= placement.viewport.height - 7, JSON.stringify(placement));
    assert(placement.box.width <= placement.anchor.width + 1);
  };
  const stableStates = async (root, input) => {
    await input.fill('$'); await input.press('Escape'); await frame(); const before = await surrounding(root);
    const reopen = async value => { await input.fill(''); await input.pressSequentially(value); await root.locator('.skill-completion').waitFor(); await frame(); unchanged(before, await surrounding(root)); await bounded(root, input); };
    await reopen('$a'); await reopen('$'); await reopen('$no-match');
    await input.press('Escape'); await frame(); unchanged(before, await surrounding(root));
    await reopen('$');
    await page.evaluate(() => window.__skillInventory.mode('loading')); await root.locator('.skill-completion p').waitFor(); await frame(); unchanged(before, await surrounding(root));
    await page.evaluate(() => window.__skillInventory.mode('failed')); await root.locator('.skill-completion button').waitFor(); await frame(); unchanged(before, await surrounding(root));
    await root.locator('.skill-completion button').click(); await frame(); unchanged(before, await surrounding(root));
    await page.evaluate(() => window.__skillInventory.mode('ready')); await root.locator('.skill-completion').getByRole('option').first().waitFor(); await frame(); unchanged(before, await surrounding(root));
    assert(await input.evaluate(node => node === document.activeElement));
    const viewport = page.viewportSize(); await page.setViewportSize({ width: viewport.width, height: Math.max(240, viewport.height - 40) }); await frame(); await bounded(root, input); await page.setViewportSize(viewport); await frame();
    await page.evaluate(() => window.visualViewport?.dispatchEvent(new Event('resize'))); await frame(); await bounded(root, input);
    // Deliberately move only the anchor in the fixture to prove both fallback sides.
    if (viewport.height >= 400) {
      const originalTransform = await input.evaluate(node => node.style.transform);
      for (const side of ['above', 'below']) {
        await input.evaluate((node, side) => { node.style.transform = ''; const rect = node.getBoundingClientRect(); const target = side === 'above' ? innerHeight - 100 : 20; node.style.transform = `translateY(${target - rect.top}px)`; }, side);
        await frame(); await bounded(root, input);
        const gap = await root.locator('.skill-completion').evaluate((panel, side) => { const input = panel.parentElement.querySelector('textarea'), anchor = input.getBoundingClientRect(), box = panel.getBoundingClientRect(); return side === 'above' ? anchor.top - box.bottom : box.top - anchor.bottom; }, side);
        assert(Math.abs(gap - 8) <= 1, JSON.stringify({ side, gap }));
      }
      await input.evaluate((node, transform) => { node.style.transform = transform; }, originalTransform); await frame();
    }
    const dimensions = await input.evaluate(node => ({ width: node.style.width, height: node.style.height }));
    await input.evaluate(node => { node.style.width = '75%'; node.style.height = '80px'; }); await frame(); await bounded(root, input);
    await page.locator('main').evaluate(node => { node.scrollTop += 25; node.dispatchEvent(new Event('scroll')); }); await frame(); await bounded(root, input);
    await input.evaluate((node, size) => { node.style.width = size.width; node.style.height = size.height; }, dimensions);
    await input.press('Escape');
  };
  const checkOverlay = async (root, input) => {
    const draft = '한글 Native selection $add-issue $removed-skill\n' + 'wrap words $removed-skill '.repeat(30);
    await input.fill(draft); await input.press('Escape');
    await root.locator('.skill-token-unavailable').first().waitFor(); await frame();
    assert.equal(await input.inputValue(), draft);
    await input.evaluate(node => { node.parentElement.style.zoom='2'; }); await frame();
    const scaled = await input.evaluate(node => ({ input: node.getBoundingClientRect().width - (node.offsetWidth-node.clientWidth)*2, overlay: node.parentElement.querySelector('.skill-text-overlay').getBoundingClientRect().width }));
    assert(Math.abs(scaled.input-scaled.overlay) <= 1, JSON.stringify(scaled));
    await input.evaluate(node => { node.parentElement.style.zoom=''; }); await frame();
    assert.equal(await root.locator('.skill-token-unavailable').count(), 31);
    const geometry = await input.evaluate(node => {
      const overlay = node.parentElement.querySelector('.skill-text-overlay'), text = overlay.firstElementChild;
      const style = getComputedStyle(node), mirrored = getComputedStyle(text);
      return { width: node.clientWidth, overlayWidth: overlay.clientWidth, textWidth: text.clientWidth, nativeHeight: node.scrollHeight, textHeight: text.scrollHeight,
        fonts: [style.fontFamily,style.fontSize,style.lineHeight,style.letterSpacing,style.padding].join('|'), mirrored: [mirrored.fontFamily,mirrored.fontSize,mirrored.lineHeight,mirrored.letterSpacing,mirrored.padding].join('|'),
        pointer: getComputedStyle(overlay).pointerEvents, hidden: overlay.getAttribute('aria-hidden'), muted: getComputedStyle(overlay.querySelector('.skill-token-unavailable')).color, semantic: getComputedStyle(node).getPropertyValue('--muted').trim(),
        described: Boolean(document.getElementById(node.getAttribute('aria-describedby'))) };
    });
    assert.equal(geometry.width, geometry.overlayWidth); assert.equal(geometry.width, geometry.textWidth); assert.equal(geometry.fonts, geometry.mirrored);
    assert.notEqual(await root.locator('.skill-token-unavailable').first().evaluate(node => getComputedStyle(node).color), await root.locator('.skill-text-overlay').evaluate(node => getComputedStyle(node).color));
    assert(Math.abs(geometry.nativeHeight - geometry.textHeight) <= 1, JSON.stringify(geometry)); assert.equal(geometry.pointer, 'none'); assert.equal(geometry.hidden, 'true'); assert.equal(geometry.described, true);
    await input.evaluate(node => { node.scrollTop=100; node.dispatchEvent(new Event('scroll')); node.setSelectionRange(3,17); }); await frame();
    const scroll = await input.evaluate(node => ({ top: node.scrollTop, transform: node.parentElement.querySelector('.skill-text-overlay > div').style.transform, start: node.selectionStart, end: node.selectionEnd }));
    assert(scroll.top > 0); assert.equal(scroll.transform, `translate(0px, ${-scroll.top}px)`); assert.equal(scroll.start,3); assert.equal(scroll.end,17);
    await input.evaluate(node => { node.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true })); });
    await page.waitForFunction(node => !node.parentElement.querySelector('.skill-text-overlay'), await input.elementHandle());
    assert.equal(await input.inputValue(), draft);
    await input.evaluate(node => { node.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true })); }); await root.locator('.skill-text-overlay').waitFor();
    await input.press('End'); await input.pressSequentially('!'); await input.press('ControlOrMeta+z'); assert.equal(await input.inputValue(), draft);
    assert.equal(await page.locator('output').getAttribute('data-fixture-creates'), '0'); overlayCases++;
  };
  const selectResource = async (root, index) => {
    const control = root.getByRole('combobox').nth(index);
    await control.focus(); await page.keyboard.press('End'); await page.keyboard.press('Enter');
    await page.waitForFunction(node => Boolean(node.dataset.value), await control.elementHandle());
  };
  for (const language of ['en', 'ko']) for (const theme of ['light', 'dark', 'system']) for (const size of [{ width: 1440, height: 900 }, { width: 1280, height: 820 }, { width: 960, height: 640 }, { width: 640, height: 480 }, { width: 720, height: 450 }, { width: 320, height: 240 }]) for (const kind of ['General Chat', 'New session']) {
    process.stdout.write(JSON.stringify({ operation: 'creation-skill-case', language, theme, kind, ...size }) + '\n');
    await page.emulateMedia({ colorScheme: 'dark' }); await page.setViewportSize(size);
    await page.goto(`${origin}/?language=${language}&theme=${theme}&skills=true`);
    await page.getByRole('button', { name: `Fixture ${kind}`, exact: true }).click();
    const root = page.locator(kind === 'General Chat' ? '.new-general-chat-page' : '.new-session-page:not(.new-general-chat-page)');
    const input = root.locator('textarea'); await input.waitFor();
    if (kind === 'New session') await selectResource(root, 0);
    await selectResource(root, kind === 'New session' ? 1 : 0); await selectResource(root, kind === 'New session' ? 2 : 1);
    const open = async token => { await input.fill(''); await input.pressSequentially(token); await root.locator('.skill-completion').getByRole('option').first().waitFor(); await frame(); };
    const geometry = async () => root.evaluate(node => {
      const content = node.querySelector('.new-session-content'), fieldset = node.querySelector('fieldset'), list = node.querySelector('.skill-completion');
      const rows = [...list.querySelectorAll('[role=option]')];
      return { content: content.getBoundingClientRect().width, fieldset: fieldset.getBoundingClientRect().width, contentMin: getComputedStyle(content).minWidth, fieldsetMin: getComputedStyle(fieldset).minWidth, listHeight: list.getBoundingClientRect().height, listClient: list.clientHeight, listScroll: list.scrollHeight,
        rows: rows.map(row => { const name = row.querySelector('strong'), description = row.querySelector('.skill-completion-description'), badge = row.querySelector('.skill-completion-provenance'); return { width: row.clientWidth, scroll: row.scrollWidth, height: row.getBoundingClientRect().height, name: name.getBoundingClientRect().width, description: description.getBoundingClientRect().width, badge: getComputedStyle(badge).flexShrink, provenance: badge.textContent, nameText: name.textContent, original: description.textContent }; }),
        pageOverflow: document.documentElement.scrollWidth > innerWidth + 1, mainOverflow: node.closest('main').scrollWidth > node.closest('main').clientWidth + 1 };
    });
    await stableStates(root, input);
    await open('$a'); const one = await geometry();
    assert.equal(one.contentMin, '0px'); assert.equal(one.fieldsetMin, '0px');
    assert(one.content <= 820 && one.fieldset <= one.content + 1, JSON.stringify(one));
    assert.equal(one.pageOverflow, false); assert.equal(one.mainOverflow, false);
    assert.equal(one.rows.length, 1); assert.equal(one.rows[0].original, 'Evidence-driven GitHub issue creation '.repeat(20));
    const assertRows = data => { for (const row of data.rows) { assert(row.scroll <= row.width + 1, JSON.stringify(row)); assert(row.height >= 40 && row.height < 42); assert.equal(row.badge, '0'); assert(row.name <= row.width * 0.4 + 1); assert([language === 'en' ? 'User' : '사용자', language === 'en' ? 'Project' : '프로젝트'].includes(row.provenance)); } };
    assertRows(one);
    await input.press('Escape'); assert.equal(await root.locator('.skill-completion').count(), 0); assert(await input.evaluate(node => node === document.activeElement));
    await open('$'); const many = await geometry(); assertRows(many); assert(many.listHeight <= 220); assert(many.listScroll > many.listClient); assert(many.rows.some(row => row.original === '' && row.nameText === 'long-name-'.repeat(30))); 
    const scroll = await page.locator('main').evaluate(node => node.scrollTop);
    for (let index = 0; index < 10; index++) await input.press('ArrowDown');
    assert(await root.locator('.skill-completion').evaluate(node => node.scrollTop > 0));
    assert.equal(await page.locator('main').evaluate(node => node.scrollTop), scroll); assert(await input.evaluate(node => node === document.activeElement));
    await input.press('ArrowUp'); await input.press('Tab'); assert.equal(await root.locator('.skill-completion').count(), 0); assert(await input.evaluate(node => node === document.activeElement));
    await open('$a'); await input.press('Enter'); assert.equal(await input.inputValue(), '$add-issue');
    await open('$a'); await root.locator('.skill-completion').getByRole('option').click(); assert.equal(await input.inputValue(), '$add-issue'); assert(await input.evaluate(node => node === document.activeElement));
    assert.equal(await page.locator('output').getAttribute('data-fixture-creates'), '0');
    await open('$a');
    await page.evaluate(() => window.__skillInventory.remove());
    const removed = root.locator('.skill-completion').getByRole('option'); await page.waitForFunction(node => node.getAttribute('aria-disabled') === 'true', await removed.elementHandle());
    await removed.click({ force: true }); await input.press('ArrowDown'); await input.press('Enter'); await input.press('Tab');
    assert.equal(await input.inputValue(), '$a'); assert.equal(await input.getAttribute('aria-activedescendant'), null);
    assert.equal(await removed.evaluate(node => getComputedStyle(node).color === getComputedStyle(node.querySelector('.skill-completion-description')).color), true);
    assert.equal(await removed.evaluate(node => node.scrollWidth <= node.clientWidth + 1), true);
    assert.equal(await removed.getAttribute('data-availability'), 'unavailable'); assert(await input.evaluate(node => node === document.activeElement));
    await input.press('Escape'); await input.fill(''); await input.pressSequentially('$a');
    await frame(); assert.equal(await root.locator('.skill-completion').getByRole('option').count(), 0);
    await page.evaluate(() => window.__skillInventory.restore());
    await checkOverlay(root, input);
    cases++;
  }
  // Nearest shared placements: real queued editor and follow-up controller/CSS.
  for (const language of ['en', 'ko']) for (const theme of ['light', 'dark', 'system']) for (const width of [960, 480, 320]) {
    await page.setViewportSize({ width, height: 640 }); await page.goto(`${origin}/?language=${language}&theme=${theme}&skills=true&shared=true`);
    await page.getByRole('button', { name: 'Fixture Settings', exact: true }).click();
    await page.locator('[data-shared=queue] .actions button').first().click();
    for (const placement of ['follow-up', 'queue']) {
      const root = page.locator(`[data-shared="${placement}"]`), input = root.locator('textarea');
      await stableStates(root, input);
      await input.fill(''); await input.pressSequentially('$a'); await root.locator('.skill-completion').getByRole('option').waitFor();
      assert(await root.locator('.skill-completion').evaluate(node => node.scrollWidth <= node.clientWidth + 1));
      await input.press('Enter'); assert.equal(await input.inputValue(), '$add-issue'); assert(await input.evaluate(node => node === document.activeElement));
      assert.equal(await page.locator('output').getAttribute('data-fixture-creates'), '0');
      await checkOverlay(root, input);
    }
    cases++;
  }
  // Unsupported creation uses the same original overlay, with no inventory choices.
  for (const language of ['en', 'ko']) for (const theme of ['light', 'dark']) for (const kind of ['General Chat', 'New session']) {
    await page.setViewportSize({ width: 640, height: 480 });
    await page.goto(`${origin}/?language=${language}&theme=${theme}&skills=true&unsupported=true`);
    await page.getByRole('button', { name: `Fixture ${kind}`, exact: true }).click();
    const root = page.locator(kind === 'General Chat' ? '.new-general-chat-page' : '.new-session-page:not(.new-general-chat-page)'), input = root.locator('textarea');
    if (kind === 'New session') await selectResource(root, 0);
    await selectResource(root, kind === 'New session' ? 1 : 0); await selectResource(root, kind === 'New session' ? 2 : 1);
    await input.fill('$'); await root.locator('.skill-completion p').waitFor(); await input.press('Escape'); await frame(); const before = await surrounding(root);
    await input.fill(''); await input.pressSequentially('$'); await root.locator('.skill-completion p').waitFor(); await frame(); unchanged(before, await surrounding(root)); await bounded(root, input);
    assert.equal(await root.locator('.skill-completion [role=option]').count(), 0); await input.press('Escape'); await frame(); unchanged(before, await surrounding(root)); cases++;
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({ operation: 'creation-skill-layout', ...source, cases, overlayCases, screenshots, effectiveZoom: 'half-viewport reflow', nativeAcceptance: 'not-performed' }) + '\n');
} finally {
  await browser?.close(); if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
