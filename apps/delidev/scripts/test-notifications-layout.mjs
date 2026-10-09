// SPDX-License-Identifier: Apache-2.0
// Automated synthetic Notifications geometry; no native/account launch or screenshots.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-notifications-check-"));
let browser, server;
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try { const pathname = new URL(request.url, "http://127.0.0.1").pathname, file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`); if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path"); response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css" }[extname(file)] ?? "application/octet-stream"); response.end(await readFile(file)); }
    catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage(), errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  let checks = 0;
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const terminals of [true, false]) for (const [width, height, zoom] of [[1920,1080,1],[1440,900,1],[960,640,1],[640,480,1],[320,640,1],[1280,960,2]]) {
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?notificationLayout=true&language=${language}&theme=${theme}&terminals=${terminals}`);
    await page.locator(".notification-value").first().waitFor();
    await page.evaluate(zoom => { document.body.style.zoom = String(zoom); }, zoom);
    const result = await page.evaluate(zoom => {
      const rect = node => { const r = node.getBoundingClientRect(); return { left:r.left/zoom, right:r.right/zoom, top:r.top/zoom, bottom:r.bottom/zoom, width:r.width/zoom, height:r.height/zoom }; };
      const column = document.querySelector(".settings-content-column"), native = document.querySelector(".notification-os"), summary = document.querySelector("section.notification-preferences"), guidance = document.querySelector(".notification-inbox-guidance"), header = document.querySelector(".settings-category-heading");
      const sections = [native,summary,guidance], rows = [...summary.querySelectorAll(".notification-row")], buttons = sections.slice(0,2).map(section => section.querySelector(".notification-section-heading button"));
      const edges = [...sections,...rows,...rows.map(row => row.querySelector(".notification-value"))].map(rect);
      return { column:rect(column), edges, buttons:buttons.map(rect), stacked:sections.slice(0,2).every(section => rect(section.querySelector("button")).top >= rect(section.querySelector("h2")).bottom - 1), borders:[header,native,summary].map(node => ({ top:getComputedStyle(node).borderTopWidth,bottom:getComputedStyle(node).borderBottomWidth,paddingTop:getComputedStyle(node).paddingTop })), gap:rect(summary).top-rect(native).bottom,rowPadding:rows.map(row => getComputedStyle(row).paddingTop), summaryInputs:summary.querySelectorAll("input").length,summaryTag:summary.tagName, summaryLabel:summary.getAttribute("aria-labelledby") === summary.querySelector("h2").id, delivery:document.querySelector(".notification-delivery").open, overflow:document.documentElement.scrollWidth > innerWidth, values:rows.map(row=>row.querySelector(".notification-value").textContent), controls:buttons.every(button=>rect(button).height>=39.5), counts:window.notificationFixtureCounts };
    }, zoom);
    assert.equal(result.summaryInputs,0); assert.equal(result.values[0]===result.values[1],terminals); assert.equal(result.summaryTag,"SECTION"); assert(result.summaryLabel); assert.equal(result.delivery,false); assert.equal(result.overflow,false,JSON.stringify({language,theme,width,zoom,result})); assert(result.controls);
    assert(result.column.width <= 1040.5);
    for (const edge of result.edges) assert(Math.abs(edge.right-result.column.right)<=1,"Section/row/value trailing edge");
    for (const edge of result.edges.slice(0,5)) assert(Math.abs(edge.left-result.column.left)<=1,"Section/row leading edge");
    if (result.column.width > 600) for (const button of result.buttons) assert(Math.abs(button.right-result.column.right)<=1,"Wide actions share trailing edge"); else assert(result.stacked,"Actions stack at body600 or less");
    assert.deepEqual(result.borders.map(border=>[border.top,border.bottom]),[["0px","1px"],["0px","1px"],["0px","0px"]]); assert.equal(result.borders[1].paddingTop,"0px"); assert.equal(result.borders[2].paddingTop,"0px"); assert(Math.abs(result.gap-24)<=1); assert.deepEqual(result.rowPadding,["16px","16px"]);
    assert.equal(result.counts.permissionRequests,0); assert.equal(result.counts.preferenceWrites,0); assert.equal(result.values.length,2);
    const opener = page.locator("section.notification-preferences .notification-section-heading button"); await opener.click();
    const editor = page.locator("dialog form.notification-preferences"); await editor.waitFor();
    assert(await editor.evaluate((node,zoom)=>node.getBoundingClientRect().width/zoom<=720.5,zoom)); assert(await editor.locator("input[type=checkbox]").first().evaluate(node=>node===document.activeElement));
    await page.keyboard.press("Escape"); await editor.waitFor({state:"hidden"}); assert(await opener.evaluate(node=>node===document.activeElement)); assert.equal(await page.evaluate(()=>window.notificationFixtureCounts.preferenceWrites),0);
    checks++;
  }
  for (const permission of ["not-determined","denied","granted","service-available","unavailable","read-failure"]) {
    await page.setViewportSize({width:640,height:480}); await page.goto(`${origin}/?notificationLayout=true&permission=${permission}`);
    await page.locator(".notification-value").first().waitFor(); await page.waitForFunction(()=>window.notificationFixtureCounts.nativeReads>0);
    const status = page.locator(".notification-os [role=status]"); await status.waitFor();
    const expected = { "not-determined":"Notification permission has not been requested.", denied:"Notifications are disabled.", granted:"Notifications allowed", "service-available":"The desktop notification service", unavailable:"Native notification service is unavailable.", "read-failure":"Native notification permission could not be confirmed." }[permission];
    await page.waitForFunction(expected=>document.querySelector(".notification-os [role=status]").textContent.includes(expected),expected);
    assert.equal(await page.evaluate(()=>window.notificationFixtureCounts.permissionRequests),0);
    const before = await page.evaluate(()=>window.notificationFixtureCounts.nativeReads); await page.locator(".notification-os .notification-section-heading button").click(); await page.waitForFunction(before=>window.notificationFixtureCounts.nativeReads>before,before);
    const opener=page.locator("section.notification-preferences .notification-section-heading button"); await opener.click(); await page.locator("dialog input[type=checkbox]").first().waitFor(); await page.keyboard.press("Escape");
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false); assert.equal(await page.evaluate(()=>window.notificationFixtureCounts.permissionRequests),0);
    if (permission === "not-determined") { await page.locator(".notification-os > button").click(); await page.waitForFunction(()=>window.notificationFixtureCounts.permissionRequests===1); await page.waitForFunction(()=>document.querySelector(".notification-os [role=status]").textContent.includes("Notifications allowed")); }
  }
  // The default branch still renders the actual App and ordinary Settings host.
  for (const [width,height] of [[1920,1080],[640,480]]) {
    await page.setViewportSize({width,height}); await page.goto(`${origin}/?populated=true&theme=light`);
    await page.getByRole("button",{name:"Settings",exact:true}).click(); await page.locator(".subscription-settings").waitFor();
    const siblingGeometry = await page.locator(".settings-content").evaluate(node=>({overflow:node.scrollWidth>node.clientWidth,column:node.querySelector(".settings-content-column").getBoundingClientRect().width,forms:[...node.querySelectorAll("form")].every(form=>form.getBoundingClientRect().width<=720.5)}));
    assert.equal(siblingGeometry.overflow,false); assert(siblingGeometry.column<=1040.5); assert(siblingGeometry.forms);
    if (await page.locator(".sidebar-context-trigger").isVisible()) await page.locator(".sidebar-context-trigger").click();
    await page.getByRole("button",{name:"Projects",exact:true}).click(); const newProject=page.getByRole("button",{name:"New Project",exact:true}); await newProject.click();
    const project=page.locator("dialog .project-creation"); await project.waitFor(); assert(await project.evaluate(node=>node.getBoundingClientRect().width<=720.5));
    await page.keyboard.press("Escape"); await project.waitFor({state:"hidden"}); assert(await newProject.evaluate(node=>node===document.activeElement)); assert(await page.locator(".settings-content").evaluate(node=>node.scrollWidth<=node.clientWidth));
  }
  assert.deepEqual(errors,[]); console.log(`Notifications layout passed: ${checks} language/theme/value/viewport/zoom cases; 6 permission states; AI Subscription/Projects regressions; aligned edges, single separators, spacing, editor focus/cap, explicit reads, no saves or implicit permission.`);
} finally { await browser?.close(); if (server) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
