// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation; Playwright is supplied by the validation host,
// without adding a product/workspace dependency. All data is synthetic.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
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
const viewports = [[1920,1080], [1440,1000], [1440,900], [1280,820], [1280,800], [960,640], [640,480]];
let checked = 0, formsChecked = 0, hiddenChoicesChecked = 0;
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
  const checkHiddenAccountChoices = async () => {
    await select("Agent Workers");
    await page.getByRole("button", { name: "New Agent Worker", exact: true }).click();
    await page.getByRole("button", { name: "Next", exact: true }).click();
    await page.getByRole("combobox", { name: "Account source", exact: true }).selectOption({ label: "Fixture provider" });
    const form = page.locator(".worker-wizard");
    await form.getByText("No accounts to select on this page.", { exact: true }).waitFor();
    assert.equal(await form.locator(".worker-account-row").count(), 0, "Hidden accounts have no DOM/focusable rows");
    assert.equal(await form.getByRole("checkbox").count(), 0, "Hidden accounts have no accessible checkbox");
    assert(await form.getByText("0 accounts selected", { exact: true }).isVisible());
    assert(await form.getByText("Connect an account in AI Subscription or AI API Keys, then refresh.", { exact: true }).isVisible());
    await form.getByRole("button", { name: "Refresh accounts", exact: true }).click();
    await form.getByText("No accounts to select on this page.", { exact: true }).waitFor();
    await checkWizard();
    await form.getByRole("button", { name: "Cancel", exact: true }).click();
    hiddenChoicesChecked++;
  };
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
      await page.goto(`${origin}/?theme=${theme}&populated=true&hiddenWorkerChoices=true`);
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await checkHiddenAccountChoices();
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
        const form = page.locator(".settings-content form:visible"); await form.waitFor();
        assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline form controls preserve whitespace`);
        assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), `${category} form cap`);
        assert.equal(await page.locator(".settings-content h1:visible").count(), 1);
        assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} form overflow`);
        if (category === "Agent Workers") {
          assert.equal(await form.locator(".worker-steps li").count(), 4);
          assert.equal(await form.getByRole("heading", { name: "Harness", exact: true }).count(), 1);
          assert.equal(await form.getByRole("button", { name: "Save Agent Worker", exact: true }).count(), 0);
        }
        formsChecked++;
        await page.getByRole("button", { name: category === "AI API Keys" ? "Back to AI API Keys" : category === "Notifications" ? "Cancel notification edit" : category === "Agent Workers" ? "Cancel" : "Cancel edit", exact: true }).click();
        if (category === "AI API Keys" && await page.getByRole("button", { name: "Back to AI API Keys", exact: true }).isVisible()) await page.getByRole("button", { name: "Back to AI API Keys", exact: true }).click();
      }
    }
  }
  // 200% effective-layout coverage uses half-size CSS viewports. Actual browser
  // chrome zoom and packaged CEF keyboard/platform acceptance remain separate.
  for (const [width,height] of viewports) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    await page.goto(`${origin}/?theme=dark`); await page.getByRole("button", { name: "Settings", exact: true }).click();
    for (const category of categories) { await select(category); assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} effective 200% ${width}`); checked++; }
    await page.goto(`${origin}/?theme=dark&populated=true&hiddenWorkerChoices=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await checkHiddenAccountChoices();
  }
  console.log(JSON.stringify({ operation: "settings_layout", result: "passed", categoryChecks: checked, childFormChecks: formsChecked, hiddenAccountChoiceChecks: hiddenChoicesChecked, themes: 3, inventories: 2, viewports: viewports.length, effectiveZoomChecks: categories.length * viewports.length, nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
