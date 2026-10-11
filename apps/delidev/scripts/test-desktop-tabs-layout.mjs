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
const directory = await mkdtemp(join(tmpdir(), "delidev-tabs-layout-"));
let browser, server, cases = 0;
const errors = [];
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/desktop-tabs-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
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
  const sizes = [{width:1440,height:900},{width:960,height:640},{width:560,height:480},{width:320,height:480},{width:1120,height:960,zoom:2}];
  async function checkStrips() {
    const rows = await page.locator('.desktop-tab-strip:visible').evaluateAll(strips => strips.map(strip => {
      const style = getComputedStyle(strip), selected = strip.querySelector('.is-selected,[aria-selected="true"]')?.closest('.desktop-tab-item');
      const scale = parseFloat(getComputedStyle(document.body).zoom) || 1;
      return { name: strip.className, gap: style.gap, wrap: style.flexWrap, overflow: style.overflowX, baseline: style.borderBottomWidth,
        indicator: selected ? getComputedStyle(selected, '::after').height : undefined,
        indicatorColor: selected ? getComputedStyle(selected, '::after').backgroundColor : undefined,
        scale, items: [...strip.querySelectorAll('.desktop-tab-item')].map(item => {
          const itemStyle = getComputedStyle(item), label = item.matches('button') ? item : item.querySelector('.desktop-tab-label'), close = item.querySelector('.desktop-tab-close');
          const box = item.getBoundingClientRect(), text = label.getBoundingClientRect(), textStyle = getComputedStyle(label), closeBox = close?.getBoundingClientRect();
          return { radius: itemStyle.borderRadius, background: itemStyle.backgroundColor, labelRadius: textStyle.borderRadius,
            labelBackground: textStyle.backgroundColor, font: textStyle.fontSize, line: textStyle.lineHeight, padding: textStyle.paddingInlineStart,
            width: box.width, height: text.height, labelRight: text.right, closeLeft: closeBox?.left, closeWidth: closeBox?.width, closeHeight: closeBox?.height,
            closeRadius: close ? getComputedStyle(close).borderRadius : undefined, closeOpacity: close ? getComputedStyle(close).opacity : undefined,
            inactiveIndicator: item !== selected ? getComputedStyle(item, '::after').backgroundColor : undefined };
        }) };
    }));
    assert(rows.length > 0);
    for (const row of rows) {
      assert.equal(row.gap, '0px', row.name); assert.equal(row.wrap, 'nowrap', row.name); assert.equal(row.overflow, 'auto', row.name);
      assert.equal(row.baseline, '1px', row.name); assert.equal(row.indicator, '2px', row.name);
      assert.notEqual(row.indicatorColor, 'rgba(0, 0, 0, 0)', row.name);
      for (const item of row.items) {
        assert.equal(item.radius, '0px', row.name); assert.equal(item.labelRadius, '0px', row.name);
        assert.equal(item.background, 'rgba(0, 0, 0, 0)', row.name); assert.equal(item.labelBackground, 'rgba(0, 0, 0, 0)', row.name);
        assert.equal(item.font, '14px', row.name); assert.equal(item.line, '20px', row.name); assert.equal(item.padding, '16px', row.name);
        assert(item.height >= 40 * row.scale - 1, JSON.stringify({name:row.name,item}));
        if (row.name.includes('session-tabs')) assert(item.width <= 320 * row.scale + 1);
        if (item.closeWidth !== undefined) {
          assert(Math.abs(item.closeWidth - 40 * row.scale) <= 1); assert(item.closeHeight >= 40 * row.scale - 1);
          assert(item.labelRight <= item.closeLeft + 1); assert.equal(item.closeRadius, '0px'); assert(parseFloat(item.closeOpacity) > 0);
        }
        if (item.inactiveIndicator) assert.equal(item.inactiveIndicator, 'rgba(0, 0, 0, 0)');
      }
    }
    return rows.length;
  }
  for (const language of ['en','ko']) for (const theme of ['light','dark','custom']) for (const size of sizes) {
    await page.setViewportSize({width:size.width,height:size.height});
    await page.goto(`${origin}/?language=${language}&theme=${theme}&zoom=${size.zoom??1}`);
    await page.getByRole('heading',{name:'Desktop tab fixture'}).waitFor();
    const count = await checkStrips(); assert.equal(count, 7);
    const pinned = page.locator('.session-tab').first(); assert.equal(await pinned.locator('.desktop-tab-close').count(), 0);
    for (const strip of await page.locator('.desktop-tab-strip').all()) {
      const last = strip.locator('.desktop-tab-label').last(); await last.focus();
      const visibility = await last.evaluate(node => { const strip = node.closest('.desktop-tab-strip'), item = node.closest('.desktop-tab-item'), a = strip.getBoundingClientRect(), b = item.getBoundingClientRect(); return { left: b.left, right: b.right, viewportLeft: a.left, viewportRight: a.right, scroll: strip.scrollLeft, outline: getComputedStyle(node).outlineWidth }; });
      assert(parseFloat(visibility.outline) > 0, JSON.stringify(visibility));
      assert(visibility.right <= visibility.viewportRight + 1, JSON.stringify(visibility));
      assert(visibility.left >= visibility.viewportLeft - 1 || visibility.right - visibility.left > visibility.viewportRight - visibility.viewportLeft, JSON.stringify(visibility));
    }
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
    await page.getByRole('button',{name:'Open Project',exact:true}).click();
    const dialog = page.getByRole('dialog',{name:'Project',exact:true}); await dialog.waitFor();
    const project = dialog.locator('.desktop-tab-strip');
    const execution = project.getByRole('tab').nth(2); await execution.click();
    await checkStrips();
    const input = dialog.locator('input:visible'); await input.fill('Retained edited draft');
    await execution.press('ArrowRight'); assert.equal(await execution.getAttribute('aria-selected'), 'true');
    await page.keyboard.press('Enter'); await project.getByRole('tab').nth(2).click();
    assert.equal(await input.inputValue(), 'Retained edited draft');
    const ordinary = await dialog.getByRole('button',{name:'Save',exact:true}).evaluate(node => ({radius:getComputedStyle(node).borderRadius, shared:node.classList.contains('desktop-tab-label')}));
    assert.equal(ordinary.shared, false); assert.notEqual(ordinary.radius, '0px');
    process.stdout.write(JSON.stringify({operation:'desktop-tabs-case',language,theme,...size,strips:count,source})+'\n'); cases++;
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({operation:'desktop-tabs-layout',source,cases,result:'passed'})+'\n');
} finally { await browser?.close();await new Promise(done=>server?server.close(done):done());await rm(directory,{recursive:true,force:true}); }
