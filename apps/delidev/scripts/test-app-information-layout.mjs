// SPDX-License-Identifier: Apache-2.0
// Synthetic browser cascade evidence; no native window or browser opener is used.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright"
);
const source = (name) => new URL(`../src/${name}`, import.meta.url);
const [styles, themes, settings, information, english, korean] =
  await Promise.all([
    readFile(source("styles.css"), "utf8"),
    readFile(source("themes.css"), "utf8"),
    readFile(source("settings-presentation.css"), "utf8"),
    readFile(
      process.env.DELIDEV_APP_INFORMATION_CSS
        ? resolve(process.env.DELIDEV_APP_INFORMATION_CSS)
        : source("app-information.css"),
      "utf8",
    ),
    readFile(source("locales/en/settings.json"), "utf8").then(JSON.parse),
    readFile(source("locales/ko/settings.json"), "utf8").then(JSON.parse),
  ]);
const copies = { en: english, ko: korean };
const linkKeys = ["releases", "license", "notices"];
const escapeHtml = (value) =>
  value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll('"', "&quot;");
const expectedRadii = [
  ["7px", "7px", "0px", "0px"],
  ["0px", "0px", "0px", "0px"],
  ["0px", "0px", "7px", "7px"],
];
const browser = await chromium.launch({
  headless: true,
  ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL
    ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL }
    : {}),
});
let cases = 0;
try {
  const page = await browser.newPage();
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const width of [960, 240])
        for (const order of ["shared-first", "local-first"]) {
          const copy = copies[language];
          const related = (key) => copy[`settings.appInformation.${key}`];
          const css =
            order === "shared-first"
              ? [styles, themes, settings, information]
              : [styles, themes, information, settings];
          await page.setViewportSize({ width, height: 600 });
          await page.setContent(`<html data-theme="${theme}"><head>
            ${css.map((sheet) => `<style>${sheet}</style>`).join("\n")}
            </head><body><section class="settings-content" aria-label="Settings">
              <div class="settings-content-column"><div class="app-information">
                <section class="app-information-card">
                  <h2>${escapeHtml(related("related"))}</h2>
                  <div class="app-information-links">
                    ${linkKeys
                      .map(
                        (
                          key,
                          index,
                        ) => `<button id="row-${index}" class="app-information-link" type="button" data-settings-search-target="app-${key}">
                          <svg class="app-information-link-icon" aria-hidden="true" focusable="false"></svg>
                          <span class="app-information-link-label">${escapeHtml(related(key))}</span>
                          <svg class="app-information-link-external" aria-hidden="true" focusable="false"></svg>
                        </button>`,
                      )
                      .join("\n")}
                  </div>
                  <p id="notice" class="app-information-help">${escapeHtml(related("sourceNotices"))}</p>
                  <p id="ordinary">Ordinary Settings paragraph</p>
                  <button id="ordinary-button">Ordinary Settings button</button>
                </section>
              </div></div></section></body></html>`);
          const initial = await page.evaluate(() => {
            const style = (selector) =>
              getComputedStyle(document.querySelector(selector));
            const group = document.querySelector(".app-information-links");
            const notice = document.querySelector("#notice");
            return {
              labels: [
                ...group.querySelectorAll(".app-information-link-label"),
              ].map((node) => node.textContent),
              targets: [...group.querySelectorAll("button")].map((node) =>
                node.getAttribute("data-settings-search-target"),
              ),
              groupRadius: style(".app-information-links").borderRadius,
              groupBorder: style(".app-information-links").borderTopWidth,
              groupOverflow: style(".app-information-links").overflow,
              rowRadii: [...group.querySelectorAll("button")].map((node) => {
                const computed = getComputedStyle(node);
                return [
                  computed.borderTopLeftRadius,
                  computed.borderTopRightRadius,
                  computed.borderBottomRightRadius,
                  computed.borderBottomLeftRadius,
                ];
              }),
              rowHeights: [...group.querySelectorAll("button")].map(
                (node) => node.getBoundingClientRect().height,
              ),
              divider: style("#row-1").borderTopWidth,
              groupBorderColor: style(".app-information-links").borderTopColor,
              dividerColor: style("#row-1").borderTopColor,
              noticeFont: style("#notice").fontSize,
              ordinaryFont: style("#ordinary").fontSize,
              labelFonts: [
                ...group.querySelectorAll(".app-information-link-label"),
              ].map((node) => getComputedStyle(node).fontSize),
              ordinaryRadius: style("#ordinary-button").borderRadius,
              gap:
                notice.getBoundingClientRect().top -
                group.getBoundingClientRect().bottom,
              groupRight: group.getBoundingClientRect().right,
              overflowWidth: document.documentElement.scrollWidth,
            };
          });
          assert.deepEqual(
            initial.labels,
            linkKeys.map((key) => related(key)),
          );
          assert.deepEqual(
            initial.targets,
            linkKeys.map((key) => `app-${key}`),
          );
          assert.equal(initial.groupRadius, "8px");
          assert.equal(initial.groupBorder, "1px");
          assert.equal(initial.groupOverflow, "visible");
          assert.deepEqual(initial.rowRadii, expectedRadii);
          assert(initial.rowHeights.every((height) => height >= 56));
          assert.equal(initial.divider, "1px");
          assert.equal(initial.dividerColor, initial.groupBorderColor);
          assert.equal(initial.noticeFont, "12px");
          assert.equal(initial.ordinaryFont, "14px");
          assert(initial.labelFonts.every((font) => font === "14px"));
          assert.equal(initial.ordinaryRadius, "8px");
          assert.equal(initial.gap, 16);
          assert(initial.groupRight <= width);
          assert(initial.overflowWidth <= width);

          for (let index = 0; index < 3; index++) {
            const row = page.locator(`#row-${index}`);
            await row.hover();
            const hovered = await row.evaluate((node) => {
              const computed = getComputedStyle(node);
              const probe = document.createElement("span");
              probe.style.background = "var(--surface-subtle)";
              document.body.append(probe);
              const subtle = getComputedStyle(probe).backgroundColor;
              probe.remove();
              return {
                background: computed.backgroundColor,
                subtle,
                radii: [
                  computed.borderTopLeftRadius,
                  computed.borderTopRightRadius,
                  computed.borderBottomRightRadius,
                  computed.borderBottomLeftRadius,
                ],
              };
            });
            assert.equal(hovered.background, hovered.subtle);
            assert.deepEqual(hovered.radii, expectedRadii[index]);
          }

          await page.mouse.move(0, 0);
          await page.keyboard.press("Tab");
          const focus = await page.locator("#row-0").evaluate((node) => ({
            active: document.activeElement === node,
            outlineWidth: getComputedStyle(node).outlineWidth,
            outlineOffset: getComputedStyle(node).outlineOffset,
          }));
          assert.equal(focus.active, true);
          assert.equal(focus.outlineWidth, "2px");
          assert.equal(focus.outlineOffset, "3px");

          for (const reason of ["unavailable", "inactive", "pending"]) {
            await page.evaluate(() => {
              for (const row of document.querySelectorAll(
                ".app-information-link",
              ))
                row.disabled = true;
            });
            for (let index = 0; index < 3; index++) {
              const row = page.locator(`#row-${index}`);
              const before = await row.evaluate(
                (node) => getComputedStyle(node).backgroundColor,
              );
              await row.hover();
              const after = await row.evaluate(
                (node) => getComputedStyle(node).backgroundColor,
              );
              assert.equal(
                after,
                before,
                `${reason} hover changed row ${index}`,
              );
              assert.equal(await row.isDisabled(), true);
            }
            await page.evaluate(() => {
              for (const row of document.querySelectorAll(
                ".app-information-link",
              ))
                row.disabled = false;
            });
          }
          cases++;
        }
  console.log(JSON.stringify({ operation: "app-information-layout", cases }));
} finally {
  await browser.close();
}
