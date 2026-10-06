// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation; Playwright is supplied by the validation host,
// without adding a product/workspace dependency. All data is synthetic.
import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
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
const viewports = [[1920,1080], [1440,1000], [1440,900], [1280,820], [960,640], [640,480]];
let checked = 0, formsChecked = 0;
const githubOnly = process.env.DELIDEV_LAYOUT_GITHUB_ONLY === "1";
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
    if (await page.getByRole("button", { name: "Open settings categories", exact: true }).isVisible()) await page.getByRole("button", { name: "Open settings categories", exact: true }).click();
    await page.getByRole("button", { name: category, exact: true }).click();
    await page.locator(".settings-content h1:visible").filter({ hasText: category }).waitFor();
    // Allow asynchronous synthetic reads to settle; no external RPC/account work.
    await page.waitForFunction(() => ![...document.querySelectorAll(".settings-content [role=status]")].some(node => node.getClientRects().length && /^(Loading |Reading server diagnostics)/.test(node.textContent ?? "")));
    if (category === "Notifications") await page.getByRole("button", { name: "Edit notification preferences", exact: true }).waitFor();
  };
  if (!githubOnly) {
  for (const theme of ["light", "dark", "system"]) for (const populated of [false, true]) for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport[0], height: viewport[1] });
    await page.emulateMedia({ colorScheme: theme === "system" ? "dark" : theme });
    await page.goto(`${origin}/?theme=${theme}&populated=${populated}`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
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
      const context = `${theme}/${populated}/${viewport}/${category}: ${JSON.stringify(layout)}`;
      assert.equal(layout.titles, 1, context); assert.equal(layout.titleSize, "26px", context);
      assert.equal(layout.padding, viewport[0] >= 1100 ? "32px" : viewport[0] >= 760 ? "24px" : "16px", context);
      assert.equal(layout.anchor, Number.parseInt(layout.padding), context);
      assert(layout.width <= 1040.5 && !layout.overflow && layout.controls && layout.multiline && layout.empty && layout.forms, context);
      checked++;
    }
    if (!populated) {
      for (const [category, action] of [["Agent Workers", "New Agent Worker"], ["Projects", "New Project"], ["Instructions", "New Instructions"], ["Repositories", "New Repository"], ["Server preferences", "New Server preferences"], ["Models", "New Model"], ["Integrations", "New GitHub profile"], ["Notifications", "Edit notification preferences"], ["AI API Keys", "Add AI API key"]]) {
        await select(category); await page.getByRole("button", { name: action, exact: true }).click();
        if (category === "AI API Keys") await page.getByRole("button", { name: /^Fixture provider/ }).click();
        const form = page.locator(".settings-content form:visible"); await form.waitFor();
        assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline form controls preserve whitespace`);
        assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), `${category} form cap`);
        assert.equal(await page.locator(".settings-content h1:visible").count(), 1);
        assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} form overflow`);
        if (category === "Agent Workers") assert(await form.evaluate(node => node.getBoundingClientRect().width >= 640 || getComputedStyle(node.querySelector(".agent-core-columns")).gridTemplateColumns.split(" ").length === 1), "Narrow Agent fields stack");
        formsChecked++;
        await page.getByRole("button", { name: category === "AI API Keys" ? "Back to AI API Keys" : category === "Notifications" ? "Cancel notification edit" : "Cancel edit", exact: true }).click();
        if (category === "AI API Keys" && await page.getByRole("button", { name: "Back to AI API Keys", exact: true }).isVisible()) await page.getByRole("button", { name: "Back to AI API Keys", exact: true }).click();
      }
    }
  }
  // 200% effective-layout coverage uses half-size CSS viewports. Actual browser
  // chrome zoom and packaged CEF keyboard/platform acceptance remain separate.
  await page.goto(`${origin}/?theme=dark`); await page.getByRole("button", { name: "Settings", exact: true }).click();
  for (const [width,height] of viewports) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    for (const category of categories) { await select(category); assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} effective 200% ${width}`); checked++; }
  }
  }
  // Explicit token-first fixture: no real GitHub, credentials or native opener.
  let onboardingChecks = 0;
  const screenshots = process.env.DELIDEV_LAYOUT_SCREENSHOTS;
  if (screenshots) await mkdir(resolve(screenshots), { recursive: true });
  for (const [width, height] of [[1440,900], [960,640], [640,480]]) {
    await page.setViewportSize({ width, height }); await page.emulateMedia({ colorScheme: "light" });
    await page.goto(`${origin}/?theme=light&github-onboarding=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Integrations");
    await page.getByRole("button", { name: "New GitHub profile", exact: true }).click();
    const token = page.getByLabel("GitHub personal access token", { exact: true }); await token.waitFor();
    assert(await token.evaluate(node => node === document.activeElement), "Password initial focus");
    assert(await page.locator(".integration-draft-guidance").evaluate(node => node.open), "Initial token-form disclosure");
    const check = async stage => {
      assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `Onboarding ${stage} ${width} overflow`);
      assert(await page.locator(".integration-onboarding form").evaluateAll(nodes => nodes.every(node => node.getBoundingClientRect().width <= 720.5)), `Onboarding ${stage} form cap`);
      if (screenshots) await page.screenshot({ path: join(resolve(screenshots), `github-${stage}-${width}x${height}.png`) });
      onboardingChecks++;
    };
    await check("token");
    // Native keyboard entry/order; no fill of a real secret or live API request.
    await token.pressSequentially("fixture-pat"); await page.keyboard.press("Tab");
    assert.equal(await page.evaluate(() => document.activeElement?.textContent), "Verify token");
    await page.keyboard.press("Enter");
    const name = page.getByLabel("Profile name", { exact: true }); await name.waitFor();
    assert(await name.evaluate(node => node === document.activeElement && node.value === "fixture-user"), "Verified name focus and prefill");
    assert.equal(await page.getByLabel("Resource owner", { exact: true }).inputValue(), "", "Never infer resource owner");
    assert(await page.getByRole("button", { name: "Save and connect", exact: true }).isDisabled(), "Explicit fine-grained owner required");
    await check("confirm");
    await page.getByRole("button", { name: "Back", exact: true }).click();
    assert.equal(await token.inputValue(), "", "Back clears token");
    assert(await token.evaluate(node => node === document.activeElement), "Back restores password focus");
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    assert(await page.getByRole("button", { name: "New GitHub profile", exact: true }).evaluate(node => node === document.activeElement), "Cancel restores opener focus");
  }
  console.log(JSON.stringify({ operation: "settings_layout", result: "passed", categoryChecks: checked, childFormChecks: formsChecked, themes: githubOnly ? 1 : 3, inventories: githubOnly ? 1 : 2, viewports: githubOnly ? 3 : 6, effectiveZoomChecks: githubOnly ? 0 : 102, onboardingChecks, nativeAcceptance: "not-performed", githubAccountAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
