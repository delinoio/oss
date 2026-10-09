// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks; no native account, login or execution authority.
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-routing-layout-"));
const screenshots = process.env.DELIDEV_ROUTING_SCREENSHOTS === "1" ? await mkdtemp(join(tmpdir(), "delidev-routing-preview-")) : null;
const catalogs = { en: {}, ko: {} };
for (const file of await readdir(join(app, "src/locales/en"))) {
  if (!file.endsWith(".json")) continue;
  for (const language of ["en", "ko"]) Object.assign(catalogs[language], JSON.parse(await readFile(join(app, "src/locales", language, file))));
}
let browser, server, cases = 0;
const errors = [];
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
  page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const open = async (language, theme, width, height, mode = "true", longNames = false) => {
    const c = key => catalogs[language][key];
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?populated=true&routingFixture=${mode}&theme=${theme}&language=${language}&longNames=${longNames}&routingHold=true`);
    await page.getByRole("button", { name: c("settings.settings_74a883"), exact: true }).click();
    if (await page.locator(".sidebar-context-trigger").isVisible()) await page.locator(".sidebar-context-trigger").click();
    await page.getByRole("button", { name: c("settings.agentWorkers_e60c23"), exact: true }).click();
    const opener = page.getByRole("button", { name: longNames ? new RegExp(language === "en" ? "^Preview routing for Example AGENT" : "Example AGENT.*라우팅 미리보기") : c("settings.previewRoutingFor_ee49d7").replace("{{v0}}", "Luna MAX"), exact: !longNames }).first();
    await opener.click();
    const dialog = page.getByRole("dialog");
    await dialog.waitFor();
    return { c, dialog, opener };
  };
  const checkControlBand = async dialog => {
    const geometry = await dialog.locator(".routing-project-controls").evaluate(node => {
      const center = selector => { const rect = node.querySelector(selector).getBoundingClientRect(); return rect.top + rect.height / 2; };
      const label = node.querySelector(".scroll-picker > span").getBoundingClientRect();
      const picker = node.querySelector("[role=combobox]").getBoundingClientRect();
      return { wide: getComputedStyle(node).display === "grid", badge: center(".routing-read-only"), picker: center("[role=combobox]"), refresh: center(":scope > button"), labelBottom: label.bottom, pickerTop: picker.top };
    });
    assert(geometry.labelBottom <= geometry.pickerTop, "Project label remains above control");
    if (geometry.wide) {
      assert(Math.abs(geometry.badge - geometry.picker) <= 1, JSON.stringify(geometry));
      assert(Math.abs(geometry.refresh - geometry.picker) <= 1, JSON.stringify(geometry));
    }
  };
  const sizes = [[1440, 900], [1280, 820], [960, 640], [640, 480]];
  // Half-sized CSS viewports verify effective 200% layout, not native browser chrome zoom.
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [baseWidth, baseHeight] of sizes) for (const zoom of [1, 2]) {
    const width = baseWidth / zoom, height = baseHeight / zoom;
    const { c, dialog, opener } = await open(language, theme, width, height, "true", true);
    await checkControlBand(dialog);
    assert.equal(await dialog.locator(".routing-details").evaluate(node => node.open), false);
    await dialog.locator(".routing-details > summary").click();
    await dialog.getByText("Complete-account-alias-".repeat(10), { exact: true }).waitFor();
    await dialog.getByText("ChatGPT", { exact: true }).waitFor();
    assert(await dialog.getByText(c("routing-preview.noEligible"), { exact: true }).isVisible());
    assert.equal(await dialog.evaluate(node => Math.round(node.getBoundingClientRect().width)), Math.min(768, width - 32));
    const geometry = await dialog.evaluate(node => {
      const rect = node.getBoundingClientRect(), body = node.querySelector(".settings-task-body"), footer = node.querySelector(".settings-task-footer").getBoundingClientRect();
      return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, viewportWidth: innerWidth, viewportHeight: innerHeight, bodyScrollWidth: body.scrollWidth, bodyWidth: body.clientWidth, footerBottom: footer.bottom };
    });
    const withinBounds = geometry.left >= 15 && geometry.right <= width - 15 && geometry.top >= 23 && geometry.bottom <= height - 23 && geometry.bodyScrollWidth <= geometry.bodyWidth + 1 && geometry.footerBottom <= geometry.bottom;
    if (!withinBounds) console.error(JSON.stringify({ operation: "routing_geometry", language, theme, width, height, geometry }));
    assert(withinBounds, `${language}/${theme}/${width}: bounds and body overflow`);
    assert.equal(await dialog.locator(".routing-note").evaluate(node => getComputedStyle(node).fontSize), "12px");
    assert.equal(await dialog.locator(".routing-account-id").evaluate(node => node.open), false);
    const disclosure = dialog.locator(".routing-account-id summary");
    await disclosure.focus(); await page.keyboard.press("Enter");
    assert.equal(await dialog.locator(".routing-account-id").evaluate(node => node.open), true);
    assert(await dialog.locator(".routing-account-id code").isVisible());
    const close = dialog.locator(".settings-task-close");
    await close.focus(); await page.keyboard.press("Tab");
    assert(await dialog.evaluate(node => node.contains(document.activeElement)));
    await page.keyboard.press("Shift+Tab");
    assert(await close.evaluate(node => node === document.activeElement));
    await dialog.locator(".settings-task-body").evaluate(node => { node.scrollTop = node.scrollHeight; });
    assert(await close.isVisible(), "Header dismissal stays visible while evidence scrolls");
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
    assert(await opener.evaluate(node => node === document.activeElement), "Escape restores the original row opener");
    await opener.click(); await dialog.waitFor();
    await dialog.locator(".routing-details").waitFor();
    assert.equal(await dialog.locator(".routing-details").evaluate(node => node.open), false, "A fresh task collapses details");
    await close.click(); await dialog.waitFor({ state: "hidden" });
    assert(await opener.evaluate(node => node === document.activeElement), "Header dismissal restores opener");
    cases++;
  }
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const mode of ["true", "selected", "sources", "empty", "invalid", "denied"]) {
    const { c, dialog } = await open(language, theme, 1440, 900, mode);
    if (mode === "true" || mode === "empty") await dialog.locator(".routing-details > summary").click();
    if (mode === "true") await dialog.getByText("ChatGPT Personal", { exact: true }).waitFor();
    if (mode === "selected" || mode === "sources") {
      await dialog.getByText("Work API", { exact: true }).first().waitFor();
      await dialog.getByText("Fixture provider", { exact: true }).first().waitFor();
      assert(await dialog.locator(".routing-result").getByText(c("routing-preview.selectedAccount"), { exact: true }).isVisible());
      if (mode === "sources") assert.equal(await dialog.locator(".routing-source").count(), 2);
    }
    if (mode === "empty") await dialog.getByText(c("routing-preview.noCandidates"), { exact: true }).waitFor();
    if (mode === "invalid") await dialog.getByText(c("configuration-actions.routingEvidenceIsUnavailable_3f3ff4"), { exact: true }).waitFor();
    if (mode === "denied") await dialog.getByRole("alert").waitFor();
    if (screenshots && mode === "true") {
      await dialog.getByRole("combobox", { name: c("configuration-actions.project_985959") }).click();
      await page.getByRole("option", { name: "oss", exact: true }).click();
      await dialog.getByText(c("configuration-actions.usingTheSelectedProjectSRestrictions_f4c29e"), { exact: true }).waitFor();
      await dialog.getByText("ChatGPT Personal", { exact: true }).waitFor();
      await page.screenshot({ path: join(screenshots, `${language}-${theme}-1440x900.png`) });
    }
    await dialog.locator(".settings-task-close").click(); await dialog.waitFor({ state: "hidden" });
    cases++;
  }
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) {
    const { c, dialog, opener } = await open(language, theme, 1440, 900, "compact");
    await dialog.locator(".routing-result").getByText("Personal", { exact: true }).waitFor();
    assert.equal(await dialog.getByText("Personal", { exact: true }).evaluateAll(nodes => nodes.filter(node => node.checkVisibility()).length), 1);
    await checkControlBand(dialog);
    assert.equal(await dialog.locator(".routing-details").evaluate(node => node.open), false);
    assert(await dialog.locator(".routing-details-count").getByText(c("routing-preview.candidateCount_one").replace("{{count}}", "1"), { exact: true }).isVisible());
    const compact = await dialog.evaluate(node => {
      const body = node.querySelector(".settings-task-body");
      return { scrollHeight: body.scrollHeight, height: body.clientHeight, note: node.querySelector(".routing-note").getBoundingClientRect().bottom, bodyBottom: body.getBoundingClientRect().bottom };
    });
    assert(compact.scrollHeight <= compact.height + 1 && compact.note <= compact.bodyBottom, "Collapsed summary and note fit without scrolling");
    const reads = await page.locator("html").getAttribute("data-fixture-routing-reads");
    await dialog.locator(".routing-details > summary").focus(); await page.keyboard.press("Enter");
    assert.equal(await dialog.locator(".routing-details").evaluate(node => node.open), true);
    assert.equal(await page.locator("html").getAttribute("data-fixture-routing-reads"), reads, "Disclosure performs no routing read");
    assert.equal(await dialog.locator(".routing-candidates").count(), 1);
    assert(await dialog.locator(".routing-candidate").getByText("Personal", { exact: true }).isVisible());
    assert.equal(await dialog.locator(".routing-source-id").evaluate(node => node.open), false);
    await dialog.locator(".routing-source-id summary").focus(); await page.keyboard.press("Enter");
    assert(await dialog.locator(".routing-source-id code").first().isVisible());
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
    assert(await opener.evaluate(node => node === document.activeElement));
    cases++;
  }
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) {
    const { c, dialog } = await open(language, theme, 1440, 900, "compact");
    await dialog.locator(".routing-result").waitFor(); await checkControlBand(dialog);
    const before = Number(await page.locator("html").getAttribute("data-fixture-routing-reads"));
    await page.evaluate(() => window.fixtureHoldRouting());
    const refresh = dialog.getByRole("button", { name: c("configuration-actions.refreshRoutingPreview_3b8c83"), exact: true });
    await refresh.click(); await page.waitForFunction(() => document.documentElement.dataset.routingPending === "true");
    await page.waitForFunction(() => document.querySelector(".routing-project-controls > button").disabled); assert(await refresh.isDisabled()); await checkControlBand(dialog);
    assert(await refresh.getByText(c("routing-preview.refreshing"), { exact: true }).isVisible());
    await page.evaluate(() => window.fixtureReleaseRouting());
    await page.waitForFunction(previous => Number(document.documentElement.dataset.fixtureRoutingReads) === previous + 1, before);
    await page.waitForFunction(() => !document.querySelector(".routing-project-controls > button").disabled); await checkControlBand(dialog);
    await dialog.getByRole("combobox", { name: c("configuration-actions.project_985959") }).click();
    await page.getByRole("option", { name: "oss", exact: true }).click();
    await dialog.getByText(c("configuration-actions.usingTheSelectedProjectSRestrictions_f4c29e"), { exact: true }).waitFor();
    await page.waitForFunction(previous => Number(document.documentElement.dataset.fixtureRoutingReads) === previous + 2, before); await checkControlBand(dialog);
    await page.setViewportSize({ width: 1280, height: 820 }); await checkControlBand(dialog);
    assert.equal(Number(await page.locator("html").getAttribute("data-fixture-routing-reads")), before + 2, "Reflow adds no requests");
    await dialog.locator(".settings-task-close").click(); cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: "routing_preview_layout", result: "passed", cases, languages: 2, themes: 2, ordinaryViewports: sizes.length, effectiveZoom: "200%", nativeAcceptance: "not-performed", screenshots }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
