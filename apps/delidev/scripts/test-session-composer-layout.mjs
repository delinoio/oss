// SPDX-License-Identifier: Apache-2.0
// Portable retained-session fixtures do not prove packaged/native acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, readdir } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-composer-layout-"));
const screenshots = process.env.DELIDEV_COMPOSER_SCREENSHOTS === "1" ? await mkdtemp(join(tmpdir(), "delidev-composer-preview-")) : null;
const catalogs = { en: {}, ko: {} };
for (const file of await readdir(join(app, "src/locales/en"))) if (file.endsWith(".json")) for (const language of ["en", "ko"]) Object.assign(catalogs[language], JSON.parse(await readFile(join(app, "src/locales", language, file))));
let browser, server, cases = 0;
const errors = [];
try {
 const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/session-composer-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
 await build.build();
 server = createServer(async (request, response) => {
  try { const pathname = new URL(request.url, "http://127.0.0.1").pathname; const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`); if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path"); response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[extname(file)] ?? "application/octet-stream"); response.end(await readFile(file)); } catch { response.writeHead(404); response.end(); }
 });
 await new Promise(done => server.listen(0, "127.0.0.1", done));
 browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
 const page = await browser.newPage(); page.on("pageerror", error => errors.push(error.message));
 const open = async (language, theme, width, height, state = "ready", workspace = "general-chat") => { await page.setViewportSize({ width, height }); await page.goto(`http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&state=${state}&workspace=${workspace}`); await page.locator(".composer textarea").waitFor(); await page.waitForFunction(() => document.querySelector(".composer-attach") && !document.querySelector(".composer-attach").disabled); };
 const image = async () => { const bytes = await page.evaluate(async () => { const canvas = document.createElement("canvas"); canvas.width = 3; canvas.height = 2; canvas.getContext("2d").fillRect(0, 0, 3, 2); const blob = await new Promise(done => canvas.toBlob(done, "image/png")); return Array.from(new Uint8Array(await blob.arrayBuffer())); }); return { name: "synthetic.png", mimeType: "image/png", buffer: Buffer.from(bytes) }; };
 const geometry = async () => {
  const value = await page.locator(".composer").evaluate(node => { const rect = node.getBoundingClientRect(), textarea = node.querySelector("textarea"), submit = node.querySelector(".composer-submit").getBoundingClientRect(), controls = [...node.querySelectorAll(".composer-toolbar > button, .composer-toolbar > label")].map(control => control.getBoundingClientRect()); return { left: rect.left, right: rect.right, bottom: rect.bottom, width: node.clientWidth, scrollWidth: node.scrollWidth, input: textarea.clientHeight, max: Number.parseFloat(getComputedStyle(textarea).maxHeight), inputScroll: textarea.scrollHeight, submitBottom: submit.bottom, controls: controls.map(rect => ({ width: rect.width, height: rect.height })), viewportWidth: innerWidth, viewportHeight: innerHeight }; });
  assert(value.left >= 0 && value.right <= value.viewportWidth + 1 && value.bottom <= value.viewportHeight + 1 && value.submitBottom <= value.viewportHeight + 1 && value.scrollWidth <= value.width + 1, JSON.stringify({ operation: "composer_bounds", value }));
  assert(value.controls.every(control => control.width >= 39 && control.height >= 39));
  return value;
 };
 // Reproduce the actual nested size-container geometry after compact Info reflow:
 // 320px original workspace, 72px conversation, and the same retained body/input.
 // A pointer click must succeed naturally; forced clicks would hide interception.
 for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const zoom of [1, 2]) {
  await open(language, theme, 480 * zoom, 320 * zoom);
  await page.evaluate(zoom => {
   document.body.style.zoom = String(zoom);
   const workspace = document.querySelector(".session-workspace"), body = workspace.querySelector(".session-body");
   const region = document.createElement("div"); region.className = "session-conversation-region"; region.dataset.nestedComposerFixture = "true";
   body.before(region); region.append(body);
   workspace.style.gridTemplateRows = "auto auto minmax(0, 1fr)";
   Object.assign(region.style, { gridArea: "3 / 1", alignSelf: "end", height: "72px", minHeight: "0", container: "conversation / size" });
   Object.assign(body.style, { display: "grid", height: "100%", gridTemplateColumns: "minmax(0, 1fr)", gridTemplateRows: "minmax(0, auto) minmax(0, 1fr) minmax(0, auto) minmax(0, auto)" });
   for (const [selector, row] of [[".session-notices", 1], [".transcript", 2], [".session-input-tray", 3], [".composer", 4]]) body.querySelector(selector).style.gridArea = `${row} / 1`;
  }, zoom);
  const input = page.locator(".composer textarea"), c = key => catalogs[language][key];
  await input.fill("Retained short workspace draft");
  await page.locator('input[type="file"]').setInputFiles(await image());
  await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 1);
  assert.equal(await page.locator(".image-preview-list img").evaluate(node => node.getBoundingClientRect().width / (parseFloat(getComputedStyle(document.body).zoom) || 1)), 64);
  await page.locator(".image-preview-list button").click({ timeout: 5000 });
  await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 0);
  assert.equal(await input.inputValue(), "Retained short workspace draft");
  await page.locator('input[type="file"]').setInputFiles(await image());
  await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 1);
  await page.locator(".image-preview-list button").focus(); await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 0);
  await input.click({ timeout: 5000 }); await input.press("End"); await input.press("!");
  assert.equal(await input.inputValue(), "Retained short workspace draft!");
  const plus = page.getByRole("button", { name: c("image-input.attach"), exact: true });
  assert.equal(await page.locator(".composer-attachment-help").count(), 0);
  await plus.focus();
  const tooltip = page.getByRole("tooltip"); await tooltip.waitFor();
  assert.equal(await tooltip.textContent(), c("image-input.help"));
  const tooltipBounds = await tooltip.boundingBox();
  assert(tooltipBounds.x >= 0 && tooltipBounds.y >= 0 && tooltipBounds.x + tooltipBounds.width <= 480 * zoom + 1 && tooltipBounds.y + tooltipBounds.height <= 320 * zoom + 1);
  await page.keyboard.press("Escape"); assert(await plus.evaluate(node => document.activeElement === node));
  assert.equal(await tooltip.count(), 0);
  await page.getByRole("button", { name: c("session.queueMessage_891d4e"), exact: true }).click({ timeout: 5000 });
  await page.waitForFunction(() => window.__sessionComposerFixture.events.length === 1);
  const bounds = await page.evaluate(() => { const workspace = document.querySelector(".session-workspace"), region = document.querySelector("[data-nested-composer-fixture]"); return { workspace: workspace.clientHeight, region: region.clientHeight, contents: region.scrollHeight, overflow: getComputedStyle(region).overflowY }; });
  assert.equal(bounds.workspace, 320); assert.equal(bounds.region, 72); assert(bounds.contents > bounds.region); assert.equal(bounds.overflow, "auto");
  // The fixture records the send before the retained receipt callback clears it.
  await page.waitForFunction(() => document.querySelector(".composer textarea").value === "");
  assert.equal(await input.inputValue(), "");
  console.log(JSON.stringify({ operation: "composer_nested_short", language, theme, zoom, result: "passed" })); cases++;
 }
 for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440, 900], [960, 640], [960, 480], [480, 320]]) {
  console.log(JSON.stringify({ operation: "composer_case", language, theme, width, height }));
  await open(language, theme, width, height);
  const c = key => catalogs[language][key];
  const input = page.locator(".composer textarea"), submit = page.getByRole("button", { name: c("session.queueMessage_891d4e"), exact: true });
  assert(await submit.isDisabled());
  assert.equal(await page.locator(".composer").evaluate(node => getComputedStyle(node).borderRadius), "24px");
  assert.equal(await input.evaluate(node => getComputedStyle(node).resize), "none");
  assert.equal((await geometry()).input, 48);
  if (screenshots && width === 1440) await page.screenshot({ path: join(screenshots, `${language}-${theme}-empty.png`) });
  assert.equal(await page.locator(".composer-attachment-help").count(), 0);
  const plus = page.getByRole("button", { name: c("image-input.attach"), exact: true });
  const before = await input.boundingBox();
  await plus.hover();
  const tooltip = page.getByRole("tooltip"); await tooltip.waitFor();
  assert.equal(await tooltip.textContent(), c("image-input.help"));
  assert.deepEqual(await input.boundingBox(), before, "guidance cannot displace input");
  await tooltip.hover(); await page.waitForTimeout(150); assert(await tooltip.isVisible());
  await input.hover(); await tooltip.waitFor({ state: "detached" });
  await plus.focus(); await tooltip.waitFor();
  const bounds = await tooltip.boundingBox();
  assert(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width + 1 && bounds.y + bounds.height <= height + 1);
  await page.keyboard.press("Escape"); assert.equal(await tooltip.count(), 0); assert(await plus.evaluate(node => node === document.activeElement));
  await input.focus(); await plus.focus(); await tooltip.waitFor();
  await input.focus(); await tooltip.waitFor({state: "detached"});
  await input.fill("First line"); await input.press("End"); await input.press("Enter"); assert.equal(await input.inputValue(), "First line\n");
  await input.fill("A long multiline message\n".repeat(30)); const growing = await geometry(); assert(growing.input <= growing.max && growing.inputScroll > growing.input);
  await input.fill(""); assert.equal((await geometry()).input, 48);
  const file = await image(); await page.locator('input[type="file"]').setInputFiles(file); await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 1);
  assert.equal(await page.locator(".image-preview-list img").evaluate(node => node.getBoundingClientRect().width), 64); await geometry();
  if (screenshots && width === 1440) await page.screenshot({ path: join(screenshots, `${language}-${theme}-image.png`) });
  await page.locator('input[type="file"]').setInputFiles(Array.from({ length: 7 }, () => file)); await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 8);
  assert.deepEqual(await page.locator(".image-preview-list img").evaluateAll(nodes => nodes.map(node => node.alt)), Array.from({ length: 8 }, (_, index) => c("image-input.image").replace("{{number}}", String(index + 1))));
  await page.locator(".image-preview-list button").nth(3).click(); assert.equal(await page.locator(".image-preview-list img").count(), 7); await geometry();
  await page.locator('input[type="file"]').setInputFiles([file, file]); await page.getByText(c("image-input.limits"), { exact: true }).waitFor(); assert.equal(await page.locator(".image-preview-list img").count(), 7);
  assert.equal(await page.locator(".session-toolbar-actions button").count(), 5);
  for (let index = 0; index < 5; index++) { const button = page.locator(".session-toolbar-actions button").nth(index); await button.click(); assert.equal(await page.locator(".image-preview-list img").count(), 7); await geometry(); await button.click(); }

  await page.waitForFunction(() => !document.querySelector(".composer-submit").disabled);
  await input.evaluate(node => node.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, ctrlKey: true, isComposing: true, bubbles: true, cancelable: true })));
  assert.equal(await page.evaluate(() => window.__sessionComposerFixture.events.length), 0, "IME cannot submit");
  await input.press(await page.evaluate(() => /Mac/.test(navigator.platform)) ? "Meta+Enter" : "Control+Enter"); await page.waitForFunction(() => window.__sessionComposerFixture.events.length === 1); assert.equal(await page.locator(".image-preview-list img").count(), 0); assert.equal(await input.inputValue(), "");
  cases++;
 }
 for (const language of ["en", "ko"]) for (const state of ["unsupported", "pending", "uncertain"]) {
  await open(language, "light", 960, 640, state, "local"); const c = key => catalogs[language][key], input = page.locator(".composer textarea"), submit = page.getByRole("button", { name: c("session.queueMessage_891d4e"), exact: true });
  if (state === "unsupported") { await page.locator('input[type="file"]').setInputFiles(await image()); await page.getByText(c("image-input.unsupported"), { exact: true }).waitFor(); assert(await submit.isDisabled()); await geometry(); }
  else { await page.locator('input[type="file"]').setInputFiles(await image()); await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 1); await input.fill("Original bound message"); await submit.click(); await page.waitForFunction(() => window.__sessionComposerFixture.events.length === 1); assert(await input.isDisabled()); assert(await page.locator(".image-preview-list button").isDisabled()); assert(await page.locator(".composer-attach").isDisabled()); assert(await page.locator(".composer-toolbar input[type=checkbox]").isDisabled()); if (state === "uncertain") { await page.getByRole("button", { name: c("session.retryTheSameMessage_5656d9"), exact: true }).click(); await page.waitForFunction(() => window.__sessionComposerFixture.events.length === 2); const events = await page.evaluate(() => window.__sessionComposerFixture.events); assert.deepEqual(events[0], events[1]); await page.waitForFunction(() => document.querySelectorAll(".image-preview-list img").length === 0); assert.equal(await input.inputValue(), ""); } }
  cases++;
 }
 assert.deepEqual(errors, []);
 console.log(JSON.stringify({ operation: "session_composer_layout", result: "passed", cases, languages: 2, themes: 2, effectiveZoom: "200% at480x320", nativeAcceptance: "not-performed", screenshots }));
} finally { await browser?.close(); if (server?.listening) await new Promise(done => server.close(done)); await rm(directory, { recursive: true, force: true }); }
