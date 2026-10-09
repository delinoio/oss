// SPDX-License-Identifier: Apache-2.0
// Synthetic stylesheet evidence; no native desktop or session execution is run.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const [themes, styles] = await Promise.all(["themes.css", "styles.css"].map(name => readFile(join(app, "src", name), "utf8")));
const browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
let cases = 0;
try {
  const page = await browser.newPage();
  for (const theme of ["light", "dark", "system"]) for (const colorScheme of ["light", "dark"]) for (const width of [1440, 960, 480, 320]) {
    await page.setViewportSize({ width, height: 640 });
    await page.emulateMedia({ colorScheme });
    await page.setContent(`<html${theme === "system" ? "" : ` data-theme="${theme}"`}><head><style>${themes}\n${styles}</style></head><body><main class="app"><aside class="sidebar"><div class="sidebar-rail"></div><dialog class="sidebar-pane-dialog"><div class="sidebar-pane is-home"><button class="sidebar-new-session">New session</button><button class="sidebar-new-general-chat">New general chat</button></div></dialog></aside></main></body></html>`);
    await page.evaluate(() => {
      const dialog = document.querySelector("dialog");
      if (matchMedia("(max-width: 759px)").matches) dialog.showModal();
      else dialog.setAttribute("open", "");
    });
    for (const current of ["sidebar-new-general-chat", "sidebar-new-session", null]) {
      await page.evaluate(current => {
        for (const button of document.querySelectorAll(".is-home > button")) {
          if (button.className === current) button.setAttribute("aria-current", "page");
          else button.removeAttribute("aria-current");
          button.blur();
        }
      }, current);
      await page.mouse.move(width - 1, 639);
      const result = await page.evaluate(() => {
        const token = name => {
          const probe = document.createElement("span");
          probe.style.color = `var(--${name})`;
          document.body.append(probe);
          const value = getComputedStyle(probe).color;
          probe.remove();
          return value;
        };
        return [...document.querySelectorAll(".is-home > button")].map(button => {
          const css = getComputedStyle(button);
          const selected = button.getAttribute("aria-current") === "page";
          return { selected, actual: [css.backgroundColor, css.color, css.borderTopColor], expected: selected ? [token("selected-background"), token("selected-text"), token("selected-border")] : [token("surface"), token("text"), token("on-inverse-muted")] };
        });
      });
      assert.equal(result.filter(value => value.selected).length, current ? 1 : 0);
      for (const value of result) assert.deepEqual(value.actual, value.expected, JSON.stringify({ theme, colorScheme, width, current }));
      cases++;
    }
    // Keyboard focus has its own outline and cannot confer current-page state.
    await page.locator(".sidebar-new-session").focus();
    await page.keyboard.press("Tab");
    const focus = await page.locator(".sidebar-new-general-chat").evaluate(button => {
      const css = getComputedStyle(button);
      return { active: button === document.activeElement, current: button.getAttribute("aria-current"), outline: css.outlineStyle, width: css.outlineWidth };
    });
    assert.deepEqual(focus, { active: true, current: null, outline: "solid", width: "3px" });
    await page.locator(".sidebar-new-general-chat").hover();
    assert.equal(await page.locator(".sidebar-new-general-chat").getAttribute("aria-current"), null);
  }
  console.log(JSON.stringify({ operation: "sidebar-selection", cases, themes: ["light", "dark", "system"], widths: [1440, 960, 480, 320], nativeAcceptance: "not-performed" }));
} finally {
  await browser.close();
}
