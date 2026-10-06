// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation; Playwright is supplied by the validation host,
// without adding a product/workspace dependency. All data is synthetic.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, realpath, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { basename, dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const playwright = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const screenshots = process.env.DELIDEV_LAYOUT_SCREENSHOT_DIR;
const screenshot = process.env.DELIDEV_LAYOUT_SCREENSHOT;
const checkout = resolve(app, "..", "..");

const resolveDestination = async value => {
  let candidate = resolve(value);
  const missingParts = [];
  while (true) {
    try {
      const existing = await realpath(candidate);
      return resolve(existing, ...missingParts.reverse());
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      const parent = dirname(candidate);
      if (parent === candidate) throw error;
      missingParts.push(basename(candidate));
      candidate = parent;
    }
  }
};

// Resolve existing ancestors before browser work so symlinked destinations cannot redirect optional screenshots into the checkout.
const ensureOutsideCheckout = async value => {
  const destination = await resolveDestination(value);
  const relativePath = relative(checkout, destination);
  const insideCheckout = relativePath === "" || (!isAbsolute(relativePath) && relativePath !== ".." && !relativePath.startsWith(`..${sep}`));
  if (insideCheckout) throw new Error("Optional screenshot destinations must resolve outside the repository checkout");
  return destination;
};

const screenshotDirectory = screenshots ? await ensureOutsideCheckout(screenshots) : null;
const screenshotPath = screenshot ? await ensureOutsideCheckout(screenshot) : null;

const { chromium } = await import(playwright ? pathToFileURL(resolve(playwright)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-settings-layout-"));
let browser, server;
const categories = ["AI Subscription", "AI API Keys", "API Providers", "Agent Workers", "Instructions", "Projects", "Repositories", "Git Profiles", "Git", "Runner Devices", "Paired devices", "Appearance", "Server preferences", "Connection & diagnostics", "Notifications", "Import / Export", "Backups"];
const viewports = [[1920,1080], [1440,1000], [1440,900], [1280,820], [1280,800], [960,640], [640,480]];
let checked = 0, formsChecked = 0, harnessChecks = 0, keyboardChecks = 0, hiddenChoicesChecked = 0;
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
    const group = form.getByRole("radiogroup", { name: "Harness", exact: true });
    if (await group.isVisible()) {
      // The picker is visible while its independent server-support read settles.
      // Exercise keyboard input only after the fixture grants that capability.
      await page.waitForFunction(() => document.querySelector(".worker-harness-card")?.matches(":enabled"));
      const cards = group.getByRole("radio");
      assert.deepEqual(await cards.evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-label"))), ["Codex", "Claude Code", "OpenCode", "Grok Build"]);
      const layout = await group.evaluate(node => {
        const style = getComputedStyle(node), form = node.closest("form");
        return { columns: style.gridTemplateColumns.split(" ").length, width: form.getBoundingClientRect().width, gap: style.gap, cards: [...node.children].map(card => ({ height: card.getBoundingClientRect().height, padding: getComputedStyle(card).padding, radius: getComputedStyle(card).borderRadius, mark: getComputedStyle(card.querySelector(".worker-harness-mark")).width, ink: getComputedStyle(card.querySelector(".worker-harness-mark")).backgroundColor, hasMask: getComputedStyle(card.querySelector(".worker-harness-mark")).maskImage !== "none", overflow: card.scrollWidth > card.clientWidth, inline: Boolean(card.getAttribute("style")) })) };
      });
      assert.equal(layout.columns, layout.width >= 640 ? 2 : 1, JSON.stringify(layout));
      assert.equal(layout.gap, "16px");
      for (const card of layout.cards) {
        assert(card.height >= 176 && !card.overflow && !card.inline, JSON.stringify(card));
        assert.equal(card.padding, "24px"); assert.equal(card.radius, "8px"); assert.equal(card.mark, "48px");
        assert(card.hasMask); assert.notEqual(card.ink, "rgba(0, 0, 0, 0)");
      }
      const codex = cards.filter({ hasText: "Codex" }), claude = cards.filter({ hasText: "Claude Code" });
      await codex.focus(); await codex.press("ArrowLeft");
      assert.equal(await group.getByRole("radio", { checked: true }).getAttribute("aria-label"), "Grok Build");
      await page.keyboard.press("Home");
      assert(await codex.evaluate(node => node === document.activeElement && node.tabIndex === 0));
      assert.equal(await codex.evaluate(node => getComputedStyle(node).outlineWidth), "3px");
      await claude.focus(); await claude.press("Space");
      assert.equal(await claude.getAttribute("aria-checked"), "true");
      await codex.focus(); await codex.press("Enter");
      assert.equal(await codex.getAttribute("aria-checked"), "true");
      assert(await group.isVisible(), "Native button activation cannot advance the wizard");
      assert.equal(await cards.evaluateAll(nodes => nodes.filter(node => node.tabIndex === 0).length), 1);
      await codex.hover();
      const selection = await codex.evaluate(node => {
        const style = getComputedStyle(node);
        return { background: style.backgroundColor, border: style.borderTopColor, selected: getComputedStyle(document.querySelector(".settings-category-button[aria-pressed=true]")).backgroundColor, accent: getComputedStyle(node.closest("form").querySelector("button.primary")).backgroundColor };
      });
      assert.equal(selection.background, selection.selected, "Selected card retains its semantic fill on hover");
      assert.equal(selection.border, selection.accent, "Selected card retains its accent border on hover");
      if (screenshotDirectory) {
        await mkdir(screenshotDirectory, { recursive: true });
        await codex.click();
        await form.scrollIntoViewIfNeeded();
        const viewport = page.viewportSize(), theme = await page.locator("html").getAttribute("data-theme");
        await page.screenshot({ path: join(screenshotDirectory, `harness-${theme}-${viewport.width}x${viewport.height}.png`) });
      }
      harnessChecks++;
    }
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
      for (const [category, action] of [["Agent Workers", "New Agent Worker"], ["Projects", "New Project"], ["Instructions", "New Instructions"], ["Repositories", "Add repository"], ["Server preferences", null], ["Git", "New Git workflow"], ["Git Profiles", "New GitHub profile"], ["Notifications", "Edit notification preferences"], ["AI API Keys", "Add AI API key"]]) {
        await select(category); if (action) await page.getByRole("button", { name: action, exact: true }).click();
        if (category === "Server preferences") {
          const form = page.getByRole("form", { name: "Server preferences form", exact: true }); await form.waitFor();
          assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline form controls preserve whitespace`);
          assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), `${category} form cap`);
          assert.equal(await page.locator(".settings-content h1:visible").count(), 1);
          assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} form overflow`);
          assert(await page.getByRole("button", { name: "Save changes", exact: true }).isDisabled());
          formsChecked++;
          continue;
        }
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
        const dialogLayout = await dialog.evaluate(node => {
          const box = node.getBoundingClientRect(), header = node.querySelector(".settings-task-header"), footer = node.querySelector(".settings-task-footer"), body = node.querySelector(".settings-task-body");
          return { width: box.width, left: box.left, right: innerWidth - box.right, height: box.height, overflow: body.scrollWidth > body.clientWidth, title: getComputedStyle(header.querySelector("h2")).fontSize, radius: getComputedStyle(node).borderRadius, footerInside: !footer.getClientRects().length || footer.getBoundingClientRect().bottom <= box.bottom, bodyScroll: getComputedStyle(body).overflowY };
        });
        assert(dialogLayout.left >= 15.5 && dialogLayout.right >= 15.5 && dialogLayout.height <= viewport[1] - 47.5 && !dialogLayout.overflow && dialogLayout.footerInside, `${category} dialog bounds: ${JSON.stringify(dialogLayout)}`);
        assert.equal(dialogLayout.title, "20px"); assert.equal(dialogLayout.radius, "16px"); assert.equal(dialogLayout.bodyScroll, "auto");
        assert.equal(await page.locator(".settings-content h1:visible").count(), 1, "Mounted background category title");
        assert.equal(await page.locator("dialog[open]:not([role=region])").count(), 1, "One native dialog");
        if (category !== "Repositories") {
          assert(await form.evaluate(node => [...node.querySelectorAll("textarea")].filter(control => control.getClientRects().length).every(control => ["pre", "pre-wrap", "break-spaces"].includes(getComputedStyle(control).whiteSpace))), `${category} multiline controls preserve whitespace`);
          assert(await form.evaluate(node => node.getBoundingClientRect().width <= 720.5), `${category} form cap`);
        }
        if (category === "Agent Workers") {
          assert.equal(await form.locator(".worker-steps li").count(), 4);
          assert.equal(await form.getByRole("heading", { name: "Harness", exact: true }).count(), 1);
          assert.equal(await form.getByRole("button", { name: "Save Agent Worker", exact: true }).count(), 0);
          await checkWizard();
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
  for (const [width,height] of viewports) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    await page.goto(`${origin}/?theme=dark`); await page.getByRole("button", { name: "Settings", exact: true }).click();
    for (const category of categories) { await select(category); assert(await page.locator(".settings-content").evaluate(node => node.scrollWidth <= node.clientWidth), `${category} effective 200% ${width}`); checked++; }
    await select("Agent Workers"); await page.getByRole("button", { name: "New Agent Worker", exact: true }).click();
    await checkWizard(); await page.getByRole("button", { name: "Cancel", exact: true }).click();
  }
  for (const [width,height] of viewports) {
    await page.setViewportSize({ width: width / 2, height: height / 2 });
    await page.goto(`${origin}/?theme=dark&populated=true&hiddenWorkerChoices=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await checkHiddenAccountChoices();
  }
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
  if (screenshotPath) {
    await page.setViewportSize({ width: 1440, height: 900 }); await page.goto(`${origin}/?theme=light&populated=true`);
    await page.getByRole("button", { name: "Settings", exact: true }).click(); await select("Projects");
    await page.getByRole("button", { name: /^Edit Example PROJECT/ }).click(); await page.getByRole("dialog").waitFor();
    await page.screenshot({ path: screenshotPath });
  }
  console.log(JSON.stringify({ operation: "settings_layout", result: "passed", categoryChecks: checked, childFormChecks: formsChecked, harnessChecks, hiddenAccountChoiceChecks: hiddenChoicesChecked, themes: 3, inventories: 2, viewports: viewports.length, effectiveZoomChecks: categories.length * viewports.length, keyboardChecks, nativeAcceptance: "not-performed" }));
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
