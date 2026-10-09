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
  const sizes = process.argv.includes("--short-height-baseline") ? [{width:560,height:480}] : [{ width: 1680, height: 1000 }, { width: 980, height: 640 }, { width: 979, height: 640 }, { width: 560, height: 640 }, { width: 1120, height: 960, zoom: 2 }];
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark", "system"]) for (const size of sizes) {
    await page.emulateMedia({ colorScheme: "dark" });
    process.stdout.write(JSON.stringify({ operation: "transcript-role-case", language, theme, ...size }) + "\n");
    await page.setViewportSize(size);
    await page.goto(`${origin}/?language=${language}&theme=${theme}&zoom=${size.zoom ?? 1}&workspace=${cases % 2 ? "worktree" : "general-chat"}`);
    await page.locator(".message-user").first().waitFor();
    const transcript = page.locator(".transcript");
    assert.equal(await transcript.locator(".message-user").count(), 2);
    assert.equal(await transcript.locator(".message-assistant").count(), 2);
    const measure = () => transcript.evaluate(root => {
      const rect = root.getBoundingClientRect(), style = getComputedStyle(root);
      const scale = parseFloat(getComputedStyle(document.body).zoom) || 1;
      const left = rect.left + parseFloat(style.paddingLeft) * scale, right = rect.left + (root.clientWidth - parseFloat(style.paddingRight)) * scale, width = right - left;
      const rows = [...root.querySelectorAll("article.message")];
      return { width, left, right, scale, overflow: root.scrollWidth > root.clientWidth + 1, rows: rows.map(node => {
        const rect = node.getBoundingClientRect(), style = getComputedStyle(node);
        return { left: rect.left, right: rect.right, width: rect.width, top: rect.top, bottom: rect.bottom, timingInset: node.previousElementSibling?.classList.contains("turn-time") ? rect.top - node.parentElement.getBoundingClientRect().top : 0, role: node.className, background: style.backgroundColor, border: style.borderTopWidth, padding: style.paddingTop, radius: style.borderTopLeftRadius, color: style.color };
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
          assert(row.width <= (m.width / m.scale < 600 ? m.width * .9 : Math.min(m.width * .75, 720 * m.scale)) + 1, "User width follows available transcript cap");
          assert.equal(row.radius, "12px"); assert.equal(row.padding, "12px");
          assert.equal(row.color, "rgb(255, 255, 255)");
          assert.notEqual(row.background, "rgba(0, 0, 0, 0)");
        } else if (row.role.includes("message-assistant") || row.role.includes("message-claude-assistant") && row.background === "rgba(0, 0, 0, 0)") {
          assert(Math.abs(row.left - m.left) <= 1, "Assistant is left-aligned");
          assert.equal(row.border, "0px"); assert.equal(row.padding, "0px"); assert.equal(row.radius, "0px");
          assert.equal(row.background, "rgba(0, 0, 0, 0)");
        } else { assert.equal(row.border, "1px", "Other roots retain card treatment"); }
      }
      for (let index = 1; index < m.rows.length; index++) assert(Math.abs(m.rows[index].top - m.rows[index - 1].bottom - 16 * m.scale - m.rows[index].timingInset) <= 1, "Historical and live item spacing is 16px");
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No horizontal page overflow");
    };
    await assertGeometry();
    const info = page.locator(".session-information");
    await info.waitFor();
    const cardGeometry = () => page.evaluate(() => {
      const box = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height }; };
      const scale = parseFloat(getComputedStyle(document.body).zoom) || 1;
      return { scale, workspace: box(".session-workspace"), available: box(".session-container").width / scale, info: box(".session-information"), transcript: box(".transcript"), tray: box(".session-input-tray"), composer: box(".composer"), region: box(".session-conversation-region"), borderRadius: getComputedStyle(document.querySelector(".session-information")).borderRadius };
    });
    const assertCard = async () => {
      const m = await cardGeometry();
      const intersects = (a, b) => a.left < b.right - 1 && a.right > b.left + 1 && a.top < b.bottom - 1 && a.bottom > b.top + 1;
      assert.equal(m.borderRadius, "20px");
      for (const name of ["transcript", "tray", "composer"]) assert.equal(intersects(m.info, m[name]), false, `Info excludes ${name}`);
      assert(m.composer.bottom <= m.workspace.bottom + 1, `Composer stays within the workspace ${JSON.stringify(m)}`);
      if (m.available >= 900) {
        assert(Math.abs(m.info.width / m.scale - 320) <= 1, "Wide card is 320 CSS px");
        assert(Math.abs((m.info.left - m.region.right) / m.scale - 44) <= 1, "Rail starts after 24px gap plus 20px padding");
      } else {
        assert(m.info.bottom <= m.region.top + 1, "Compact Info is an in-flow top band");
        assert(m.info.height <= Math.min(240 * m.scale, m.workspace.height * .25) + 1, "Compact card height is bounded");
      }
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No page overflow");
    };
    await assertCard();
    const retained = await page.evaluate(() => { globalThis.fixtureInfo = document.querySelector(".session-information"); globalThis.fixtureComposer = document.querySelector(".composer textarea"); return true; });
    assert(retained);
    const initialInfoTop = (await cardGeometry()).info.top;
    await transcript.evaluate(node => { node.scrollTop = node.scrollHeight; });
    assert.equal((await cardGeometry()).info.top, initialInfoTop, "Transcript scrolling keeps Info stationary");
    for (const section of await info.locator("details").all()) await section.evaluate(node => { node.open = true; });
    await info.evaluate(node => { node.scrollTop = node.scrollHeight; });
    await assertCard();
    assert(await page.evaluate(() => fixtureInfo === document.querySelector(".session-information") && fixtureComposer === document.querySelector(".composer textarea")), "Same Info and composer DOM after disclosure scroll");
    for (const section of await info.locator(".session-information-section").all()) await section.evaluate(node => { node.open = false; });
    await info.evaluate(node => { node.scrollTop = 0; });
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
    assert.equal(await transcript.locator(":scope > .turn-transcript-item > .message-assistant").count(), 1, "Streaming revision replaces one direct tail root");
    await transcript.locator(":scope > .turn-transcript-item > .message-assistant").scrollIntoViewIfNeeded();
    await assertGeometry();
    await assertGeometry();
    await assertCard();
    // Current tab-owned tools retain hidden roots; test visibility and original
    // focus rather than the obsolete Info action/aria-expanded contract.
    for (const name of language === "en" ? ["Files", "Diff", "Terminals", "Browser", "Diagnostics"] : ["파일", "변경 사항", "터미널", "브라우저", "진단"]) {
      const opener=page.getByRole("button",{name,exact:true}); await opener.click();
      const panel=page.locator(".session-app-panel:visible"); assert.equal(await panel.count(),1);
      const card=await panel.boundingBox(), m=await cardGeometry();
      assert(card.x+card.width<=m.region.right+1&&card.x>=m.region.left-1);
      for(const r of [m.info,m.tray,m.composer])assert(!(card.x<r.right-1&&card.x+card.width>r.left+1&&card.y<r.bottom-1&&card.y+card.height>r.top+1));
      await panel.evaluate(node=>{node.setAttribute("tabindex","-1");node.focus();}); await page.keyboard.press("Escape");
      assert.equal(await page.locator(".session-app-panel:visible").count(),0);
      await page.waitForFunction(()=>document.activeElement?.getAttribute("role")==="tab"&&document.activeElement?.getAttribute("aria-selected")==="true");
      assert(await page.evaluate(()=>fixtureInfo===document.querySelector(".session-information")&&fixtureComposer===document.querySelector(".composer textarea")));
    }
    await composer.scrollIntoViewIfNeeded();
    assert(await composer.isVisible(), "Composer remains reachable with tool panel open");
    assert.equal(await composer.inputValue(), "Retained synthetic draft");
    const previous = await cardGeometry();
    await page.setViewportSize({ width: (previous.available >= 900 ? 979 : 980) * previous.scale, height: size.height });
    await assertCard();
    assert(await page.evaluate(() => fixtureInfo === document.querySelector(".session-information") && fixtureComposer === document.querySelector(".composer textarea")), "Responsive rail/band reflow preserves DOM/controller owners");
    await page.setViewportSize(size);
    await assertCard();
    if (screenshots && size.width !== 480 && theme !== "system") {
      await mkdir(screenshots, { recursive: true });
        await transcript.evaluate(node => { node.scrollTop = 0; });
      // Allow content-visibility to paint the restored top range before capture.
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      await page.screenshot({ path: join(screenshots, `${language}-${theme}-${size.width}.png`) });
    }
    cases++;
  }
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width,height,zoom] of [[1440,900,1],[390,844,1],[1440,900,2]]) for (const workspace of ["general-chat","worktree"]) {
    await page.setViewportSize({width,height});
    await page.addInitScript(() => { Date.now = () => Date.parse("2026-10-09T10:01:23.123Z"); });
    await page.goto(`${origin}/?timing=active&language=${language}&theme=${theme}&workspace=${workspace}&zoom=${zoom}`);
    const line=page.locator(".turn-time"); await line.waitFor();
    await page.waitForFunction(ko => document.querySelector(".turn-time")?.textContent.includes(ko ? "1분 23초" : "1m 23s"),language==="ko");
    assert.equal(await line.count(),1); assert.equal(await page.locator(".message-user").count(),0);
    const composer=page.locator(".composer textarea"); await composer.focus(); const draft=await composer.inputValue();
    await page.evaluate(()=>window.__turnTimingFixture.parts()); await page.waitForFunction(()=>document.querySelectorAll(".message-user").length===3);
    assert.equal(await line.count(),1); assert(await line.evaluate(node=>node.nextElementSibling?.classList.contains("message-user")));
    assert.equal(await composer.evaluate(node=>node===document.activeElement),true);assert.equal(await composer.inputValue(),draft);
    const reads=await page.evaluate(()=>window.__turnTimingFixture.reads);
    await page.evaluate(()=>{Date.now=()=>Date.parse("2026-10-09T10:01:24.123Z");});
    await page.waitForFunction(ko=>document.querySelector(".turn-time")?.textContent.includes(ko?"1분 24초":"1m 24s"),language==="ko");
    assert.equal(await page.evaluate(()=>window.__turnTimingFixture.reads),reads,"ticks are read-only local display work");
    await page.evaluate(()=>window.__turnTimingFixture.fail());
    await page.waitForFunction(ko=>document.querySelector(".turn-time")?.textContent.includes(ko?"미확인":"Unconfirmed"),language==="ko");
    const frozen=await line.textContent(); await page.evaluate(()=>{Date.now=()=>Date.parse("2026-10-09T10:03:24.123Z");});
    await page.waitForTimeout(1100);assert.equal(await line.textContent(),frozen);
    await page.evaluate(()=>window.__turnTimingFixture.reconnect());
    // Original stream synchronization needs a validated event before Live;
    // opening an idle transport alone cannot confirm the retained observation.
    await page.getByRole("button",{name:"Fixture stream revision",exact:true}).evaluate(node=>node.click());
    await page.waitForFunction(ko=>document.querySelector(".turn-time")?.textContent.includes(ko?"3분 24초":"3m 24s"),language==="ko");
    await page.evaluate(()=>window.__turnTimingFixture.terminal("failed"));
    await page.waitForFunction(ko=>document.querySelector(".turn-time")?.textContent.includes(ko?"소요 시간 · 12초":"Elapsed · 12s"),language==="ko");
    await page.evaluate(()=>{Date.now=()=>Date.parse("2026-10-09T12:03:24.123Z");});await page.waitForTimeout(1100);
    assert((await line.textContent()).includes(language==="ko"?"12초":"12s"));assert.equal(await line.getAttribute("aria-live"),"off");assert.equal(await line.getAttribute("role"),null);
    assert(await line.evaluate(node=>node.scrollWidth<=node.clientWidth+1));assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);
    cases++;console.log(JSON.stringify({operation:"turn-timing-layout",language,theme,width,height,zoom,workspace,result:"passed"}));
  }
  for (const language of ["en","ko"]) for (const mode of ["legacy","invalid","zero","inherited"]) {
    await page.setViewportSize({width:390,height:844});await page.goto(`${origin}/?timing=${mode}&language=${language}`);await page.locator(".turn-time").first().waitFor();
    const text=await page.locator(".transcript").textContent();
    if(mode==="zero")assert(text.includes(language==="ko"?"소요 시간 · 0초":"Elapsed · 0s"));
    else if(mode==="inherited"){assert.equal(await page.locator(".turn-time").count(),2);assert(text.includes(language==="ko"?"상속된 턴":"Inherited turn"));assert(text.includes(language==="ko"?"1일 1시간":"1d 1h"));}
    else assert(text.includes(language==="ko"?"시간 정보 없음":"Time unavailable"));
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false);cases++;console.log(JSON.stringify({operation:"turn-timing-history-layout",language,mode,result:"passed"}));
  }
  assert.deepEqual(errors, []);
  process.stdout.write(JSON.stringify({ operation: "transcript-role-layout", ...source, cases, screenshots, effectiveZoom: "CSS zoom 2 plus narrow viewport reflow", nativeAcceptance: "not-performed" }) + "\n");
} finally {
  await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
