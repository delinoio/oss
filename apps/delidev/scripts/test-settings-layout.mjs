// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation; Playwright is supplied by the validation host,
// without adding a product/workspace dependency. All data is synthetic.
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const playwright = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(playwright ? pathToFileURL(resolve(playwright)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-settings-layout-"));
let browser, server;
const categories = ["AI Subscription", "AI API Keys", "API Providers", "Models", "Agent Workers", "Instructions", "Projects", "Repositories", "Runner Devices", "Appearance", "Paired devices", "Server preferences", "Integrations", "Connection & diagnostics", "Notifications", "Import / Export", "Backups"];
let language = "en";
const messages = new Map();
for (const file of await readdir(join(app, "src/locales/en"))) {
  if (!file.endsWith(".json")) continue;
  const en = JSON.parse(await readFile(join(app, "src/locales/en", file))), ko = JSON.parse(await readFile(join(app, "src/locales/ko", file)));
  for (const [key, value] of Object.entries(en)) if (!messages.has(value)) messages.set(value, ko[key]);
}
const l = value => {
  if (language !== "ko") return value;
  if (messages.has(value)) return messages.get(value);
  if (value.startsWith("New ")) return messages.get("New {{v0}}").replace("{{v0}}", messages.get(value.slice(4)) ?? value.slice(4));
  return value;
};
const viewports = [[1920,1080], [1440,1000], [1440,900], [1280,800], [960,640], [640,480]];
let checked = 0, formsChecked = 0;
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
  const select = async category => {
    if (await page.locator(".sidebar-context-trigger").isVisible()) await page.locator(".sidebar-context-trigger").click();
    await page.getByRole("button", { name: l(category), exact: true }).click();
    await page.locator(".settings-content h1:visible").filter({ hasText: l(category) }).waitFor();
    // Allow asynchronous synthetic reads to settle; no external RPC/account work.
    await page.waitForFunction(() => ![...document.querySelectorAll(".settings-content [role=status]")].some(node => node.getClientRects().length && /(Loading |Reading server diagnostics|불러오는 중|읽는 중)/.test(node.textContent ?? "")));
    if (category === "Notifications") await page.getByRole("button", { name: l("Edit notification preferences"), exact: true }).waitFor();
  };
  for (language of ["en", "ko"]) for (const theme of ["light", "dark", "system"]) for (const populated of [false, true]) for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport[0], height: viewport[1] });
    await page.emulateMedia({ colorScheme: theme === "system" ? "dark" : theme });
    await page.goto(`${origin}/?theme=${theme}&populated=${populated}&language=${language}`);
    await page.getByRole("button", { name: l("Settings"), exact: true }).click();
    for (const category of categories) {
      await select(category);
      const layout = await page.locator(".settings-content").evaluate(root => {
        const column = root.querySelector(".settings-content-column"), h1s = [...root.querySelectorAll("h1")].filter(node => node.getClientRects().length);
        const style = getComputedStyle(root), box = column.getBoundingClientRect();
        const controls = [...root.querySelectorAll("button, select, input:not([type=checkbox]):not([type=radio])")].filter(node => node.getClientRects().length);
        const textareas = [...root.querySelectorAll("textarea")].filter(node => node.getClientRects().length);
        const empty = [...root.querySelectorAll(".settings-empty")].filter(node => node.getClientRects().length);
        const forms = [...root.querySelectorAll("form")].filter(node => node.getClientRects().length);
        return { titles: h1s.length, titleSize: getComputedStyle(h1s[0]).fontSize, padding: style.paddingLeft, anchor: box.left - root.getBoundingClientRect().left, width: box.width, overflow: root.scrollWidth > root.clientWidth, controls: controls.every(node => node.getBoundingClientRect().height >= 39.5), multiline: textareas.every(node => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(node).whiteSpace)), empty: empty.every(node => node.getBoundingClientRect().height >= 159.5), forms: forms.every(node => node.getBoundingClientRect().width <= 720.5) };
      });
      const context = `${language}/${theme}/${populated}/${viewport}/${category}: ${JSON.stringify(layout)}`;
      assert.equal(layout.titles, 1, context); assert.equal(layout.titleSize, "26px", context);
      assert.equal(layout.padding, viewport[0] >= 1100 ? "32px" : viewport[0] >= 760 ? "24px" : "16px", context);
      assert.equal(layout.anchor, Number.parseInt(layout.padding), context);
      assert(layout.width <= 1040.5 && !layout.overflow && layout.controls && layout.multiline && layout.empty && layout.forms, context);
      if (category === "Appearance") assert(await page.locator(".appearance-choice-label").evaluateAll(labels => labels.every(node => node.getBoundingClientRect().width >= node.parentElement.clientWidth - 32 && node.scrollWidth <= node.clientWidth)), `${context} theme labels retain available width`);
      checked++;
    }
    if (!populated) {
      for (const [category, action] of [["Agent Workers", "New Agent Worker"], ["Projects", "New Project"], ["Instructions", "New Instructions"], ["Repositories", "Add repository"], ["Server preferences", "New Server preferences"], ["Models", "New Model"], ["Integrations", "New GitHub profile"], ["Notifications", "Edit notification preferences"], ["AI API Keys", "Add AI API key"]]) {
        await select(category); await page.getByRole("button", { name: l(action), exact: true }).click();
        if (category === "AI API Keys") await page.getByRole("button", { name: /^Fixture provider/ }).click();
        const form = page.locator(category === "Repositories" ? ".settings-content .repository-registration:visible" : ".settings-content form:visible"); await form.waitFor();
        assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline form controls preserve whitespace`);
        assert(await form.evaluate(node => node.getBoundingClientRect().width <= (node.classList.contains("repository-registration") ? 1040.5 : 720.5)), `${category} form cap`);
        assert.equal(await page.locator(".settings-content h1:visible").count(), 1);
        assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} form overflow`);
        if (category === "Agent Workers") assert(await form.evaluate(node => node.getBoundingClientRect().width >= 640 || getComputedStyle(node.querySelector(".agent-core-columns")).gridTemplateColumns.split(" ").length === 1), "Narrow Agent fields stack");
        formsChecked++;
        await page.getByRole("button", { name: l(category === "Repositories" ? "Back to repositories" : category === "AI API Keys" ? "Back to AI API Keys" : category === "Notifications" ? "Cancel notification edit" : "Cancel edit"), exact: true }).click();
        if (category === "AI API Keys" && await page.getByRole("button", { name: l("Back to AI API Keys"), exact: true }).isVisible()) await page.getByRole("button", { name: l("Back to AI API Keys"), exact: true }).click();
      }
    }
  }
  // 200% effective-layout coverage uses half-size CSS viewports. Actual browser
  // chrome zoom and packaged CEF keyboard/platform acceptance remain separate.
  for (language of ["en", "ko"]) {
  await page.goto(`${origin}/?theme=dark&language=${language}`); await page.getByRole("button", { name: l("Settings"), exact: true }).click();
  for (const [width,height] of viewports) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    for (const category of categories) { await select(category); assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} effective 200% ${width}`); checked++; }
  }
  }
  // Inspect all primary empty-state surfaces in both languages. Their retained
  // business queries use only the synthetic transport above.
  for (language of ["en", "ko"]) {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(`${origin}/?theme=light&language=${language}`);
    for (const surface of ["Sessions", "New session", "Pull requests", "Usage", "Schedules", "Activity", "Inbox", "Search"]) {
      if (surface === "Inbox" || surface === "Search") await page.getByRole("button", { name: l("Sessions"), exact: true }).click();
      await page.getByRole("button", { name: l(surface), exact: true }).click();
      assert.equal(await page.locator("main").evaluate(node => node.scrollWidth <= node.clientWidth), true, `${language}/${surface} overflow`);
      assert.equal(await page.locator("html").getAttribute("lang"), language);
    }
  }
  // The approved appearance geometry and a real fixture save transition.
  await page.goto(`${origin}/?theme=light&language=en`); language = "en";
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Appearance");
  const languageSelect = page.locator(".language-settings select");
  await languageSelect.selectOption("ko"); language = "ko";
  await page.locator(".language-settings [role=status]").filter({ hasText: "언어를 저장했습니다." }).waitFor();
  const languageBounds = await languageSelect.boundingBox();
  assert(languageBounds.width <= 320 && languageBounds.height >= 40);
  assert.equal(await page.locator(".language-settings select").inputValue(), "ko");
  assert.equal(await page.locator(".language-settings").getByText("기본값은 시스템 언어입니다. 변경하면 모든 DeliDev 창에 바로 적용됩니다.").count(), 1);
  if (process.env.DELIDEV_LAYOUT_SCREENSHOT) await page.screenshot({ path: process.env.DELIDEV_LAYOUT_SCREENSHOT });
  console.log(JSON.stringify({ operation: "settings_layout", result: "passed", categoryChecks: checked, childFormChecks: formsChecked, languages: 2, themes: 3, inventories: 2, viewports: 6, effectiveZoomChecks: 204, primarySurfaceChecks: 16, nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
