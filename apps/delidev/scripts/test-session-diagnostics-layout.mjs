// SPDX-License-Identifier: Apache-2.0
// Synthetic geometry checks use committed styles. They do not establish native
// acceptance or exercise authenticated request/recovery controllers.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
const module = process.env.DELIDEV_QA_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  module ? pathToFileURL(module).href : "playwright"
);
const styles = await Promise.all(
  ["themes", "styles", "session", "disclosure"].map((name) =>
    readFile(new URL(`../src/${name}.css`, import.meta.url), "utf8"),
  ),
);
const browser = await chromium.launch({
  headless: true,
  channel: process.env.DELIDEV_QA_BROWSER_CHANNEL ?? "chrome",
});
let checks = 0;
const reference = "01900000-0000-7000-8000-000000000001".repeat(6);
try {
  // Half-size CSS viewports cover effective 200% reflow, not native browser zoom.
  for (const viewport of [
    { width: 1920, height: 1080 },
    { width: 1440, height: 900 },
    { width: 960, height: 640 },
    { width: 640, height: 480 },
    { width: 320, height: 240 },
  ]) {
    for (const panelWidth of [viewport.width, Math.min(600, viewport.width)]) {
      for (const language of ["en", "ko"])
        for (const theme of ["light", "dark"]) {
          const page = await browser.newPage({ viewport });
          const label =
            language === "en"
              ? "Selected execution account"
              : "선택된 실행 계정";
          await page.setContent(
            `<html lang="${language}" data-theme="${theme}"><style>${styles.join("\n")}</style><body><main class="session-workspace session-tabbed" style="display:block;height:100vh;width:${panelWidth}px"><div class="session-app-panel session-diagnostics-panel"><div class="session-diagnostics"><div><section class="session-information-section"><h3>Session details</h3><dl><div data-token-observation><dt>${label}</dt><dd>${reference}<p>Original Worker guidance</p></dd></div><div><dt>Recovery</dt><dd>none</dd></div></dl></section><section><h3>Snapshot references and account order</h3><dl><div><dt>${label}</dt><dd>${reference}</dd></div></dl><ol aria-label="Saved account order"><li>first-account · weight 2</li><li>second-account · weight 1</li></ol></section><p id="portal-notice" role="status">Saved execution evidence is unavailable</p></div><aside class="session-files"><header><h2>Model request diagnostics</h2><button>Close diagnostics</button></header><p role="status">Retained observations may be stale</p><form><label>${label}<input value="${reference}" /></label><button>Apply execution filter</button></form><article class="result"><h3>Native input</h3><dl><div><dt>Request</dt><dd>${reference}</dd></div></dl><details class="disclosure" data-disclosure-density="compact"><summary class="disclosure-header">Technical details</summary><p>${reference}</p></details></article><button>Load more</button></aside></div></div></main><section id="other"><dl><dt>${label}</dt><dd>Other surface</dd></dl><input id="ordinary-input" /></section></body></html>`,
          );
          const geometry = await page.evaluate(() => {
            const rect = (e) => {
              const r = e.getBoundingClientRect();
              return {
                x: r.x,
                y: r.y,
                width: r.width,
                height: r.height,
                bottom: r.bottom,
              };
            };
            const content = document.querySelector(".session-diagnostics"),
              host = content.firstElementChild,
              first = host.firstElementChild,
              next = host.children[1],
              portalNotice = host.lastElementChild;
            const labels = [...content.querySelectorAll("dt")],
              values = [...content.querySelectorAll("dd")];
            const portalNoticeStyle = getComputedStyle(portalNotice);
            const heading = first.querySelector("h3"),
              facts = first.querySelector("dl");
            return {
              inset: parseFloat(getComputedStyle(content).paddingLeft),
              bodySize: getComputedStyle(content).fontSize,
              lineHeight: getComputedStyle(content).lineHeight,
              headingSize: getComputedStyle(heading).fontSize,
              headingGap: rect(facts).y - rect(heading).bottom,
              sectionGap: rect(next.querySelector("h3")).y - rect(first).bottom,
              portalItemGap: rect(portalNotice).y - rect(next).bottom,
              portalItemInset:
                parseFloat(portalNoticeStyle.paddingTop) +
                parseFloat(portalNoticeStyle.borderTopWidth),
              contentWidth: rect(host).width,
              firstX: rect(first).x,
              requestX: rect(content.querySelector("aside")).x,
              labels: labels.map(rect),
              values: values.map(rect),
              pageOverflow:
                document.documentElement.scrollWidth > window.innerWidth,
              panelOverflow: content.scrollWidth > content.clientWidth,
              controls: [
                ...content.querySelectorAll("button,input,summary"),
              ].map(rect),
              otherDisplay: getComputedStyle(
                document.querySelector("#other dl"),
              ).display,
              otherIndent: getComputedStyle(document.querySelector("#other dd"))
                .marginLeft,
            };
          });
          const narrow = panelWidth < 640;
          assert.equal(geometry.inset, narrow ? 16 : 32);
          assert.equal(geometry.bodySize, "14px");
          assert.equal(geometry.lineHeight, "21px");
          assert.equal(geometry.headingSize, "16px");
          assert.equal(geometry.headingGap, 16);
          assert(Math.abs(geometry.sectionGap - 32) <= 1, "section rhythm");
          assert(
            Math.abs(geometry.portalItemGap + geometry.portalItemInset - 33) <=
              1,
            "all portal items receive section rhythm",
          );
          assert(geometry.contentWidth <= 1040, "content cap");
          assert.equal(geometry.firstX, geometry.requestX, "shared inset");
          assert(
            !geometry.pageOverflow && !geometry.panelOverflow,
            "long reference overflow",
          );
          assert(
            geometry.controls.every((r) => r.height >= 40),
            "minimum control height",
          );
          for (let i = 0; i < geometry.labels.length; i++) {
            const label = geometry.labels[i],
              value = geometry.values[i];
            if (narrow) {
              assert.equal(value.x, label.x);
              assert.equal(value.y - label.bottom, 4);
            } else {
              assert.equal(value.x - label.x, 224);
              assert.equal(value.y, label.y);
            }
          }
          assert.equal(
            geometry.values[1].y -
              (narrow ? geometry.labels[1].bottom : geometry.values[0].bottom),
            narrow ? 4 : 12,
          );
          if (narrow)
            assert.equal(
              geometry.labels[1].y - geometry.values[0].bottom,
              12,
              "between-pair spacing",
            );
          assert.equal(
            geometry.otherDisplay,
            "block",
            "other surface grid unchanged",
          );
          assert.notEqual(geometry.otherIndent, "0px");
          assert.deepEqual(
            await page
              .locator('ol[aria-label="Saved account order"] li')
              .allTextContents(),
            ["first-account · weight 2", "second-account · weight 1"],
          );
          assert.equal(
            await page.locator(".session-diagnostics dd").first().textContent(),
            reference + "Original Worker guidance",
          );
          await page.keyboard.press("Tab");
          const focusState = await page
            .locator("button")
            .first()
            .evaluate((e) => {
              const style = getComputedStyle(e);
              const probe = document.createElement("span");
              probe.style.color = "var(--focus)";
              document.body.append(probe);
              const focusColor = getComputedStyle(probe).color;
              probe.remove();
              return {
                active: e === document.activeElement,
                outlineWidth: style.outlineWidth,
                outlineColor: style.outlineColor,
                focusColor,
              };
            });
          assert.deepEqual(
            focusState,
            {
              active: true,
              outlineWidth: "2px",
              outlineColor: focusState.focusColor,
              focusColor: focusState.focusColor,
            },
            "keyboard focus uses the semantic focus token",
          );
          const inputOutline = async (selector) =>
            page.locator(selector).evaluate((e) => {
              const style = getComputedStyle(e);
              return {
                width: style.outlineWidth,
                style: style.outlineStyle,
                color: style.outlineColor,
                offset: style.outlineOffset,
              };
            });
          await page.locator(".session-diagnostics input").focus();
          const diagnosticInputOutline = await inputOutline(
            ".session-diagnostics input",
          );
          await page.locator("#ordinary-input").focus();
          assert.deepEqual(
            diagnosticInputOutline,
            await inputOutline("#ordinary-input"),
            "form controls retain their ordinary focus appearance",
          );
          await page.locator("summary").focus();
          await page.keyboard.press("Enter");
          assert(
            await page.locator("details").evaluate((e) => e.open),
            "disclosure keyboard open",
          );
          await page.keyboard.press("Enter");
          assert(
            await page.locator("details").evaluate((e) => !e.open),
            "disclosure keyboard close",
          );
          await page.locator(".session-diagnostics button").last().focus();
          assert(
            await page
              .locator(".session-diagnostics button")
              .last()
              .evaluate(
                (e) => e.getBoundingClientRect().bottom <= window.innerHeight,
              ),
            "scroll-reachable final control",
          );
          checks++;
          await page.close();
        }
    }
  }
  console.log(
    JSON.stringify({
      checks,
      fixture: "synthetic-session-diagnostics",
      nativeAcceptance: false,
    }),
  );
} finally {
  await browser.close();
}
