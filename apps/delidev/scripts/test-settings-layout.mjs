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
const categories = ["AI Subscription", "AI API Keys", "API Providers", "Agent Workers", "Instructions", "Projects", "Repositories", "Git Profiles", "Git", "Runner Devices", "Paired devices", "Appearance", "Server preferences", "Connection & diagnostics", "Notifications", "Import / Export", "Backups"];
const viewports = [[1920,1080], [1440,1000], [1440,900], [1280,820], [960,640], [640,480]];
let checked = 0, formsChecked = 0, keyboardChecks = 0;
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
  const checkWizard = async () => {
    const form = page.locator(".worker-wizard");
    assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), "Wizard form cap");
    assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), "Wizard content overflow");
    const next = form.getByRole("button", { name: /^(Next|Save Agent Worker)$/ });
    await next.scrollIntoViewIfNeeded();
    const footer = await next.boundingBox();
    assert(footer && footer.y >= 0 && footer.y + footer.height <= page.viewportSize().height + 0.5, "Wizard footer remains visible in document flow");
  };
  if (!githubOnly) {
  for (const theme of ["light", "dark", "system"]) for (const populated of [false, true]) for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport[0], height: viewport[1] });
    await page.emulateMedia({ colorScheme: theme === "system" ? "dark" : theme });
    await page.goto(`${origin}/?theme=${theme}&populated=${populated}`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    assert.deepEqual(await page.locator(".settings-nav-group h2").allTextContents(), ["AI", "Coding", "Device management", "System"]);
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
    if (populated) {
      await select("Agent Workers");
      await page.getByRole("button", { name: "New Agent Worker", exact: true }).click();
      await checkWizard();
      await page.getByRole("button", { name: "Next", exact: true }).click();
      await page.getByRole("combobox", { name: "Account source", exact: true }).selectOption({ label: "Fixture provider" });
      await page.getByRole("checkbox", { name: /^Personal API/ }).check();
      await page.getByRole("checkbox", { name: /^Team API/ }).check();
      await checkWizard();
      await page.getByRole("button", { name: "Next", exact: true }).click();
      const input = page.getByRole("combobox", { name: "Model", exact: true });
      await input.fill("example-model");
      await page.getByRole("option", { name: /^Fixture model/ }).waitFor();
      await input.press("ArrowDown"); await input.press("Enter");
      await checkWizard();
      await page.getByRole("button", { name: "Next", exact: true }).click();
      const heading = page.getByRole("heading", { name: "Configure", exact: true });
      assert(await heading.evaluate(node => node === document.activeElement), "Wizard step heading receives focus");
      await checkWizard();
      await page.getByRole("button", { name: "Back", exact: true }).click();
      assert.equal((await input.inputValue()).startsWith("example-model-native-"), true, "Model selection survives Back");
      await page.getByRole("button", { name: "Back", exact: true }).click();
      assert(await page.getByRole("checkbox", { name: /^Personal API/ }).isChecked());
      assert(await page.getByRole("checkbox", { name: /^Team API/ }).isChecked());
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      formsChecked += 4;
    }
    if (!populated) {
      for (const [category, action] of [["Agent Workers", "New Agent Worker"], ["Projects", "New Project"], ["Instructions", "New Instructions"], ["Repositories", "Add repository"], ["Server preferences", "New Server preferences"], ["Git", "New Git workflow"], ["Git Profiles", "New GitHub profile"], ["Notifications", "Edit notification preferences"], ["AI API Keys", "Add AI API key"]]) {
        await select(category); await page.getByRole("button", { name: action, exact: true }).click();
        if (category === "Repositories") {
          // Registration first inspects a folder before exposing saved fields.
          // Exercise its manual entry without inventing native folder authority.
          await page.getByRole("button", { name: "Enter a path…", exact: true }).click();
          await page.getByRole("textbox", { name: "Absolute checkout path", exact: true }).waitFor();
          assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), "Repository registration overflow");
          assert.equal(await page.locator(".settings-content h1:visible").count(), 1);
          formsChecked++;
          await page.getByRole("button", { name: "Back to repositories", exact: true }).click();
          continue;
        }
        if (category === "AI API Keys") await page.getByRole("button", { name: /^Fixture provider/ }).click();
        const dialog = page.getByRole("dialog"); await dialog.waitFor();
        const form = dialog.locator("form:visible");
        if (category !== "Repositories") await form.waitFor();
        const layout = await dialog.evaluate(node => {
          const box = node.getBoundingClientRect(), header = node.querySelector(".settings-task-header"), footer = node.querySelector(".settings-task-footer"), body = node.querySelector(".settings-task-body");
          return { width: box.width, left: box.left, right: innerWidth - box.right, height: box.height, overflow: body.scrollWidth > body.clientWidth, title: getComputedStyle(header.querySelector("h2")).fontSize, radius: getComputedStyle(node).borderRadius, footerInside: !footer.getClientRects().length || footer.getBoundingClientRect().bottom <= box.bottom, bodyScroll: getComputedStyle(body).overflowY };
        });
        assert(layout.left >= 15.5 && layout.right >= 15.5 && layout.height <= viewport[1] - 47.5 && !layout.overflow && layout.footerInside, `${category} dialog bounds: ${JSON.stringify(layout)}`);
        assert.equal(layout.title, "20px"); assert.equal(layout.radius, "16px"); assert.equal(layout.bodyScroll, "auto");
        assert.equal(await page.locator(".settings-content h1:visible").count(), 1, "Mounted background category title");
        assert.equal(await page.locator("dialog[open]:not([role=region])").count(), 1, "One native dialog");
        if (category !== "Repositories") {
          assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline controls preserve whitespace`);
          assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), `${category} form cap`);
        }
        const close = dialog.locator(".settings-task-close"); await close.focus();
        for (const key of ["Shift+Tab", "Tab", "Tab"]) {
          await page.keyboard.press(key);
          assert(await dialog.evaluate(node => node.contains(document.activeElement)), `${category} ${key} stays in dialog`); keyboardChecks++;
        }
        await page.mouse.click(2, 2); assert(await dialog.isVisible(), "Backdrop preserves task");
        formsChecked++;
        await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
        await page.waitForFunction(() => document.querySelector(".settings-content")?.contains(document.activeElement), { timeout: 2000 }); keyboardChecks++;
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
    await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Git Profiles");
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
  if (!githubOnly) {
  for (const theme of ["light", "dark"]) for (const [width, height] of [[1440,900], [1280,820], [960,640], [640,480]]) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    await page.goto(`${origin}/?theme=${theme}`); await page.getByRole("button", { name: "Settings", exact: true }).click();
    await select("Projects"); await page.getByRole("button", { name: "New Project", exact: true }).click();
    const dialog = page.getByRole("dialog");
    assert(await dialog.evaluate(node => { const box = node.getBoundingClientRect(), body = node.querySelector(".settings-task-body"); return box.width <= innerWidth - 31 && box.height <= innerHeight - 47 && body.scrollWidth <= body.clientWidth; }), `${theme} Project dialog effective 200% ${width}`);
    await page.keyboard.press("Escape"); formsChecked++;
  }
  for (const theme of ["light", "dark"]) {
    await page.setViewportSize({ width: 1280, height: 820 }); await page.goto(`${origin}/?theme=${theme}&populated=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Projects");
    await page.getByRole("button", { name: /^Delete Example PROJECT/ }).click();
    const dialog = page.getByRole("dialog"); await dialog.waitFor();
    await page.waitForFunction(() => document.activeElement?.textContent === "Keep configuration");
    assert.equal(await dialog.evaluate(node => Math.round(node.getBoundingClientRect().width)), 480);
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
    await select("Backups");
    const main = page.locator("#main");
    const position = await main.evaluate(node => { node.scrollTop = 80; return node.scrollTop; });
    await page.getByRole("button", { name: /^Inspect backup/ }).click(); await dialog.waitFor();
    await page.getByText("Database integrity and original server identity verified.", { exact: true }).waitFor();
    assert.equal(await main.evaluate(node => node.scrollTop), position, "Opening preserves list scroll");
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
    assert.equal(await main.evaluate(node => node.scrollTop), position, "Closing preserves list scroll");
    await select("Agent Workers");
    await page.getByRole("button", { name: /^Preview routing for Example AGENT/ }).click(); await dialog.waitFor();
    assert.equal(await dialog.evaluate(node => Math.round(node.getBoundingClientRect().width)), 768, "Routing uses the ordinary form width");
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "hidden" });
    keyboardChecks += 4;
  }
  if (process.env.DELIDEV_LAYOUT_SCREENSHOT) {
    await page.setViewportSize({ width: 1440, height: 900 }); await page.goto(`${origin}/?theme=light&populated=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Projects");
    await page.getByRole("button", { name: /^Edit Example PROJECT/ }).click(); await page.getByRole("dialog").waitFor();
    await page.screenshot({ path: process.env.DELIDEV_LAYOUT_SCREENSHOT });
  }
  }
  console.log(JSON.stringify({ operation: "settings_layout", result: "passed", categoryChecks: checked, childFormChecks: formsChecked, themes: githubOnly ? 1 : 3, inventories: githubOnly ? 1 : 2, viewports: githubOnly ? 3 : 6, effectiveZoomChecks: githubOnly ? 0 : categories.length * viewports.length, keyboardChecks, onboardingChecks, nativeAcceptance: "not-performed", githubAccountAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
