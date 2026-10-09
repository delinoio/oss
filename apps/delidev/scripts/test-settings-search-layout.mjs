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
const directory = await mkdtemp(join(tmpdir(), "delidev-settings-search-"));
let browser, server, checks = 0;
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
  page.on("pageerror", error => console.error("fixture_page_error", error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const catalogs = Object.fromEntries(await Promise.all(["en","ko"].map(async language=>[language, Object.assign(...await Promise.all(["settings","remediation-policy","network-settings","ssh-setup","appearance","language","date-format","notification-settings"].map(async name=>JSON.parse(await readFile(join(app,`src/locales/${language}/${name}.json`),"utf8")))))])));
  for (const language of ["en","ko"]) for(const theme of ["light","dark","system"]) for(const [width,height] of [[1440,900],[1280,820],[960,640],[640,480],[720,450],[480,320],[320,240],[320,280],[480,321],[759,320],[760,320]]) {
    console.log(JSON.stringify({operation:"settings-search-case",language,theme,width,height}));
    await page.setViewportSize({width,height}); await page.goto(`${origin}/?language=${language}&theme=${theme}`);
    const t=key=>catalogs[language][key];
    await page.getByRole("button",{name:t("settings.settings_74a883"),exact:true}).click();
    const drawer=page.locator('.sidebar-context-trigger');
    const open=async()=>{if(width<760 && !(await page.getByRole('searchbox').isVisible()))await drawer.click();};
    await open(); const input=page.getByRole('searchbox'); await input.waitFor();
    assert.equal(await page.locator('[data-settings-category]').count(),18);
    const originalCategories=await page.locator('[data-settings-category]').evaluateAll(nodes=>nodes.map(node=>node.dataset.settingsCategory));
    assert.equal(await input.getAttribute('placeholder'),language==='en'?'Search Settings':'설정 검색');
    assert.equal(await input.getAttribute('aria-label'),t('settings.search.label'));
    assert.equal(await page.getByRole('button',{name:t('settings.search.clear'),exact:true}).count(),0);
    assert.equal(await page.locator('.settings-search-header label').count(),0);
    // Actual native pointer sequence that previously lost its click when focus
    // revealed a category between pointerdown and the browser's click target.
    if(width>=760){
    const retainedInput=await input.elementHandle();
    for(const category of originalCategories){await open();await page.locator(`[data-settings-category="${category}"]`).click();}
    await open();const pointerRow=page.locator('[data-settings-category="agent-workers"]');
    await pointerRow.scrollIntoViewIfNeeded();
    await pointerRow.evaluate(node=>{
      window.settingsPointerProbe=[];
      for(const type of ['pointerdown','focusin','pointerup','click'])node.addEventListener(type,()=>{const rect=node.getBoundingClientRect();window.settingsPointerProbe.push({type,top:rect.top,scroll:node.closest('.sidebar-list').scrollTop});},{once:true});
    });
    await pointerRow.click();
    await page.waitForFunction(()=>document.querySelector('[data-settings-category="agent-workers"]')?.getAttribute('aria-current')==='page');
    const probe=await page.evaluate(()=>window.settingsPointerProbe);
    const down=probe.find(value=>value.type==='pointerdown'),up=probe.find(value=>value.type==='pointerup');
    assert(down&&up&&probe.some(value=>value.type==='click'),'Native pointer sequence activates its original row');
    assert.equal(up.top,down.top,'Pointer focus cannot move its target before activation');
    assert.equal(up.scroll,down.scroll,'Pointer focus cannot change sidebar scroll before activation');
    await open();assert(await input.evaluate((node,original)=>node===original,retainedInput));await retainedInput.dispose();
    }
    const assertPinned = async () => {
      const geometry=await page.locator('.settings-search-header').evaluate(header=>{
        const scroller=header.closest('.sidebar-list'),field=header.querySelector('input'),icon=header.querySelector('svg');
        const box=header.getBoundingClientRect(),bounds=scroller.getBoundingClientRect(),input=field.getBoundingClientRect(),glyph=icon.getBoundingClientRect();
        return {top:box.top,bottom:box.bottom,scrollTop:bounds.top,scrollBottom:bounds.bottom,overlap:glyph.right>=input.left+parseFloat(getComputedStyle(field).paddingLeft),overflow:header.scrollWidth>header.clientWidth};
      });
      assert(geometry.top>=geometry.scrollTop-1 && geometry.bottom<=geometry.scrollBottom+1,'Sticky controls stay in scroller');
      assert.equal(geometry.overlap,false,'Search text clears icon');assert.equal(geometry.overflow,false);
    };
    const assertFocusedRow = async row => {
      await row.focus();
      assert(await row.evaluate(node=>{const box=node.getBoundingClientRect(),header=node.closest('.settings-search').querySelector('.settings-search-header').getBoundingClientRect(),bounds=node.closest('.sidebar-list').getBoundingClientRect();return box.top>=header.bottom+4 && box.bottom<=bounds.bottom-4 && node.closest('.sidebar-list').scrollWidth<=node.closest('.sidebar-list').clientWidth && parseFloat(getComputedStyle(node).outlineOffset)<=0;}),'Focused row clears pinned controls');
    };
    await page.locator('.sidebar-list').evaluate(node=>{node.scrollTop=node.scrollHeight;});
    await assertPinned();await assertFocusedRow(page.locator('[data-settings-category]').last());
    await assertFocusedRow(page.locator('[data-settings-category]').first());await assertPinned();
    if(width<760 && height<=320){
      const retained=await input.elementHandle(),scrollOwner=await page.locator(".sidebar-list").elementHandle();
      // Pointer scrolling and programmatic focus must both clear the header.
      for(const category of ['projects','subscription-accounts','backups']){
        await page.locator('.sidebar-list').evaluate(node=>{node.scrollTop=node.scrollHeight;});
        const row=page.locator(`[data-settings-category="${category}"]`);
        await row.click(); await open();
        await assertFocusedRow(row);
        assert(await row.evaluate(node=>{const box=node.getBoundingClientRect();return node.contains(document.elementFromPoint(box.left+box.width/2,box.top+box.height/2));}));
        await row.click(); await open();assert(await input.evaluate((node,original)=>node===original,retained));
      }
      await input.focus();await input.press('Tab');await assertFocusedRow(page.locator('[data-settings-category]').first());
      const footer=page.locator('.sidebar-footer'),original=await footer.textContent();
      // Synthetic long status proves the same footer scroller retains all text.
      await footer.locator('p').evaluate(node=>{node.textContent='Retained connection status '.repeat(80);});
      assert(await footer.evaluate(node=>node.scrollHeight>node.clientHeight));
      await footer.evaluate(node=>{node.scrollTop=node.scrollHeight;});
      assert(await footer.evaluate(node=>node.scrollTop+node.clientHeight>=node.scrollHeight-1));
      await assertFocusedRow(page.locator('[data-settings-category]').last());await assertPinned();
      await footer.locator('p').evaluate((node,text)=>{node.textContent=text;},original);
      assert(await page.locator('.sidebar-drawer-close').evaluate(node=>node.getBoundingClientRect().height>=40));
      await input.fill('retained resize draft');await page.setViewportSize({width,height:321});
      assert.equal(await input.inputValue(),'retained resize draft');assert(await input.evaluate((node,original)=>node===original,retained));
      assert(await page.locator('.sidebar-list').evaluate((node,original)=>node===original,scrollOwner));
      await page.setViewportSize({width,height});assert.equal(await input.inputValue(),'retained resize draft');
      assert(await input.evaluate((node,original)=>node===original && node===document.activeElement,retained));await input.fill('');
      await retained.dispose();await scrollOwner.dispose();
    }

    // Common letters in bundled metadata produce an overflowing result list.
    await input.fill(language==='en'?'a':'이');
    const results=page.locator('[data-settings-search-result]');
    assert(await results.count()>3);
    await page.locator('.sidebar-list').evaluate(node=>{node.scrollTop=node.scrollHeight;});
    await assertPinned();await assertFocusedRow(results.last());await assertFocusedRow(results.first());
    await input.fill('');
    assert.deepEqual(await page.locator('[data-settings-category]').evaluateAll(nodes=>nodes.map(node=>node.dataset.settingsCategory)),originalCategories);
    if(width<760){await page.keyboard.press('Escape');assert(await drawer.evaluate(node=>node===document.activeElement));await open();}
    const search=async key=>{await open();await input.fill(t(key));};
    const select=async id=>{const result=page.locator(`[data-settings-search-result="${id}"]`);await result.focus();await result.press('Enter');await page.waitForFunction(id=>document.activeElement?.getAttribute('data-settings-search-target')===id,id);};
    await search('appearance.theme_efb52e');
    assert.equal(await page.locator('[data-settings-groups]').isVisible(),false);
    await input.dispatchEvent('compositionstart');await input.press('Enter');assert.equal(await page.locator('.appearance-settings').count(),0);await input.dispatchEvent('compositionend');
    await select('theme');await search('language.title');await select('language');await search('date-format.title');await select('date-format');assert.equal(await page.locator('dialog[open]:not([role="region"])').count(),0);
    await search('remediation-policy.consecutiveAutomaticAttemptLimit_844605');await select('remediation-attempts');
    const attempts=page.locator('[data-settings-search-target="remediation-attempts"] input');await attempts.fill('8');
    await search('remediation-policy.consecutiveAutomaticAttemptLimit_844605');await select('remediation-attempts');assert.equal(await attempts.inputValue(),'8');
    assert.equal(await page.locator('.server-remediation-details').getAttribute('open'),'');
    await search('network-settings.networkSettings_600f22');await select('network');assert.equal(await page.locator('.network-inline > div').count(),1);assert.equal(await page.locator('.network-workspace').count(),0);
    await search('notification-settings.aboutNotificationDelivery_e8b4e9');await select('notification-delivery');
    const delivery=page.locator('[data-settings-search-target="notification-delivery"]');assert.equal(await delivery.evaluate(node=>node.tabIndex),0);assert.equal(await delivery.getAttribute('tabindex'),null);await page.locator('.notification-preferences button').first().focus();await page.keyboard.press('Tab');assert(await delivery.evaluate(node=>node===document.activeElement));
    await search('notification-settings.questionsAndApprovalRequests_e6c1b4');await select('notification-questions');assert.equal(await page.locator('dialog[open]:not([role="region"])').count(),0);
    await search('ssh-setup.setUpAWorkerOverSsh_9a1626');await select('ssh');assert.equal(await page.locator('dialog[open]:not([role="region"])').count(),0);
    await open();await input.fill('private-resource-name-no-match');assert.equal(await page.locator('[data-settings-search-result]').count(),0);
    await input.fill('');assert.equal(await input.inputValue(),'');assert(await input.evaluate(node=>node===document.activeElement));assert(await page.locator('[data-settings-groups]').isVisible());
    await input.fill('   ');assert(await page.locator('[data-settings-groups]').isVisible());
    await input.fill('');
    await page.locator('[data-settings-category="instructions"]').click();
    await page.locator('.settings-toolbar button.primary').click();
    await page.locator('dialog[open]:not([role="region"])').waitFor();
    await page.locator(".settings-search input").evaluate(node=>node.focus());assert.equal(await page.locator(".settings-search input").evaluate(node=>node===document.activeElement),false);
    await page.keyboard.press('Escape');assert.equal(await page.locator('dialog[open]:not([role="region"])').count(),0);
    await search('appearance.theme_efb52e');const themeResult=page.locator('[data-settings-search-result="theme"]');await themeResult.focus();await themeResult.press('Space');await page.waitForFunction(()=>document.activeElement?.getAttribute('data-settings-search-target')==='theme');
    await search('appearance.theme_efb52e');await input.focus();await input.press('Tab');assert.equal(await page.locator('[data-settings-search-result]').first().evaluate(node=>node===document.activeElement),true);
    assert(await page.locator('.settings-search').evaluate(node=>node.scrollWidth<=node.clientWidth+1));
    for(const box of await page.locator('.settings-search button:visible').evaluateAll(nodes=>nodes.map(node=>({height:node.getBoundingClientRect().height,width:node.getBoundingClientRect().width}))))assert(box.height>=40);
    if(process.env.DELIDEV_SEARCH_SCREENSHOT_DIR && width===1440 && language==='en' && theme==='light')await page.screenshot({path:resolve(process.env.DELIDEV_SEARCH_SCREENSHOT_DIR,'settings-search.png'),fullPage:true});
    // Departure closes the visit; entry restores the ordinary subscription category.
    await input.fill('');
    await page.locator('[data-settings-category="appearance"]').click();
    await page.getByRole('button',{name:language==='ko'?'사용량':'Usage',exact:true}).click();
    assert.equal(await page.locator('.sidebar-pane').evaluate(node=>getComputedStyle(node).display),'flex','Other sidebar surfaces retain flex geometry');
    await page.getByRole('button',{name:t('settings.settings_74a883'),exact:true}).click();await open();assert.equal(await page.getByRole('searchbox').inputValue(),'');
    checks++;
  }
  console.log(JSON.stringify({operation:'settings_search_layout',result:'passed',checks,languages:2,themes:3,viewports:11,effectiveZoom:[1,2],nativeAcceptance:'not-performed'}));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
