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
const geometryFailures = [];
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
              dlRowGap: getComputedStyle(first.querySelector("dl")).rowGap,
              labelMargins: labels.map((e) => {
                const style = getComputedStyle(e);
                return { top: style.marginTop, bottom: style.marginBottom };
              }),
              valueMargins: values.map((e) => {
                const style = getComputedStyle(e);
                return { top: style.marginTop, bottom: style.marginBottom };
              }),
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
          const scenario = `${viewport.width}x${viewport.height}/panel-${panelWidth}/${language}/${theme}`;
          const checkGeometry = (name, callback) => {
            try {
              callback();
            } catch (error) {
              geometryFailures.push({
                scenario,
                name,
                error: error.message,
                geometry,
              });
            }
          };
          const geometryChecks = [
            ["inset", () => assert.equal(geometry.inset, narrow ? 16 : 32)],
            ["body size", () => assert.equal(geometry.bodySize, "14px")],
            ["line height", () => assert.equal(geometry.lineHeight, "21px")],
            ["heading size", () => assert.equal(geometry.headingSize, "16px")],
            ["heading gap", () => assert.equal(geometry.headingGap, 16)],
            [
              "section rhythm",
              () => assert(Math.abs(geometry.sectionGap - 32) <= 1),
            ],
            [
              "all portal items receive section rhythm",
              () =>
                assert(
                  Math.abs(
                    geometry.portalItemGap + geometry.portalItemInset - 33,
                  ) <= 1,
                ),
            ],
            ["content cap", () => assert(geometry.contentWidth <= 1040)],
            [
              "shared inset",
              () => assert.equal(geometry.firstX, geometry.requestX),
            ],
            [
              "long reference overflow",
              () => assert(!geometry.pageOverflow && !geometry.panelOverflow),
            ],
            [
              "minimum control height",
              () => assert(geometry.controls.every((r) => r.height >= 40)),
            ],
          ];
          for (const [name, callback] of geometryChecks)
            checkGeometry(name, callback);
          for (let i = 0; i < geometry.labels.length; i++) {
            const label = geometry.labels[i],
              value = geometry.values[i];
            if (narrow) {
              checkGeometry(`label/value horizontal alignment ${i}`, () =>
                assert.equal(value.x, label.x),
              );
              checkGeometry(`label/value gap ${i}`, () =>
                assert.equal(value.y - label.bottom, 4),
              );
            } else {
              checkGeometry(`label/value horizontal gap ${i}`, () =>
                assert.equal(value.x - label.x, 224),
              );
              checkGeometry(`label/value vertical alignment ${i}`, () =>
                assert.equal(value.y, label.y),
              );
            }
          }
          checkGeometry("second label/value gap", () =>
            assert.equal(
              geometry.values[1].y -
                (narrow
                  ? geometry.labels[1].bottom
                  : geometry.values[0].bottom),
              narrow ? 4 : 12,
            ),
          );
          if (narrow)
            checkGeometry("between-pair spacing", () =>
              assert.equal(
                geometry.labels[1].y - geometry.values[0].bottom,
                12,
              ),
            );
          checkGeometry("other surface grid unchanged", () =>
            assert.equal(geometry.otherDisplay, "block"),
          );
          checkGeometry("other surface indentation", () =>
            assert.notEqual(geometry.otherIndent, "0px"),
          );
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
  // Flat dt/dd request facts (as rendered by RequestDiagnostics) and the
  // request panel separator with an empty versus populated portal host.
  let flatChecks = 0;
  for (const viewport of [
    { width: 320, height: 240 },
    { width: 500, height: 640 },
    { width: 639, height: 640 },
    { width: 640, height: 640 },
    { width: 1000, height: 640 },
  ]) {
    for (const language of ["en", "ko"])
      for (const theme of ["light", "dark"])
        for (const populated of [true, false]) {
          const page = await browser.newPage({ viewport });
          const labels =
            language === "en"
              ? ["Request", "Revision", "Execution", "Account at request time"]
              : ["요청", "리비전", "실행", "요청 시점 계정"];
          const pairs = labels
            .map(
              (label, i) =>
                `<dt>${label}</dt><dd>${i === 2 ? reference : `value-${i}`}</dd>`,
            )
            .join("");
          const hostContent = populated
            ? `<section class="session-information-section"><h3>Session details</h3><dl><div><dt>${labels[0]}</dt><dd>wrapped</dd></div></dl></section>`
            : "";
          await page.setContent(
            `<html lang="${language}" data-theme="${theme}"><style>${styles.join("\n")}</style><body><main class="session-workspace session-tabbed" style="display:block;height:100vh;width:${viewport.width}px"><div class="session-app-panel session-diagnostics-panel"><div class="session-diagnostics"><div id="host">${hostContent}</div><aside class="session-files"><header><h2>Model request diagnostics</h2><button>Close diagnostics</button></header><article class="result"><h3>Native input</h3><dl id="flat">${pairs}</dl><button id="after-facts">Load more</button></article></aside></div></div></main></body></html>`,
          );
          const measure = () =>
            page.evaluate(() => {
              const content = document.querySelector(".session-diagnostics"),
                host = document.querySelector("#host"),
                request = document.querySelector("aside"),
                flat = document.querySelector("#flat");
              const rect = (e) => e.getBoundingClientRect();
              const style = getComputedStyle(request);
              const flatStyle = getComputedStyle(flat);
              const dts = [...flat.querySelectorAll("dt")].map(rect),
                dds = [...flat.querySelectorAll("dd")].map(rect);
              return {
                hostChildCount: host.childNodes.length,
                inset: parseFloat(getComputedStyle(content).paddingLeft),
                borderTop: style.borderTopWidth,
                marginTop: style.marginTop,
                paddingTop: style.paddingTop,
                requestX: rect(request).x,
                hostX: rect(host).x,
                requestTop: rect(request).y,
                hostBottom: rect(host).bottom,
                contentTop: rect(content).y,
                contentPaddingTop: parseFloat(
                  getComputedStyle(content).paddingTop,
                ),
                columns: flatStyle.gridTemplateColumns.split(" ").length,
                columnGap: flatStyle.columnGap,
                rowGap: flatStyle.rowGap,
                dts,
                dds,
                ddMarginBottoms: [...flat.querySelectorAll("dd")].map(
                  (e) => getComputedStyle(e).marginBottom,
                ),
                flatBottom: rect(flat).bottom,
                afterFactsTop: rect(document.querySelector("#after-facts")).y,
                pageOverflow:
                  document.documentElement.scrollWidth > window.innerWidth,
              };
            });
          const scenario = `flat/${viewport.width}/${language}/${theme}/${populated ? "populated" : "empty"}`;
          const narrow = viewport.width < 640;
          const verify = (geometry, state) => {
            const fail = (name, callback) => {
              try {
                callback();
              } catch (error) {
                geometryFailures.push({
                  scenario: `${scenario}/${state}`,
                  name,
                  error: error.message,
                  geometry,
                });
              }
            };
            const hostPopulated = state === "populated";
            fail("shared outer inset", () => {
              assert.equal(geometry.inset, narrow ? 16 : 32);
              assert.equal(geometry.requestX, geometry.hostX);
            });
            fail("request separator", () => {
              if (hostPopulated) {
                assert.equal(geometry.borderTop, "1px");
                assert.equal(geometry.marginTop, "16px");
                assert.equal(geometry.paddingTop, "16px");
              } else {
                assert.equal(geometry.borderTop, "0px");
                assert.equal(geometry.marginTop, "0px");
                assert.equal(geometry.paddingTop, "0px");
              }
            });
            fail("request panel starts at the inset when host is empty", () => {
              if (!hostPopulated)
                assert.equal(
                  geometry.requestTop,
                  geometry.contentTop + geometry.contentPaddingTop,
                );
              else assert.equal(geometry.requestTop - geometry.hostBottom, 16);
            });
            fail("flat pair count", () => {
              assert.equal(geometry.dts.length, 4);
              assert.equal(geometry.dds.length, 4);
            });
            for (let i = 0; i < geometry.dts.length; i++) {
              const label = geometry.dts[i],
                value = geometry.dds[i];
              if (narrow) {
                fail(`flat label/value alignment ${i}`, () =>
                  assert.equal(value.x, label.x),
                );
                fail(`flat label/value gap ${i}`, () =>
                  assert.equal(value.y - label.bottom, 4),
                );
                if (i + 1 < geometry.dts.length)
                  fail(`flat between-pair gap ${i}`, () =>
                    assert.equal(geometry.dts[i + 1].y - value.bottom, 12),
                  );
              } else {
                fail(`flat wide column offset ${i}`, () => {
                  assert.equal(value.x - label.x, 224);
                  assert.equal(label.width, 200);
                  assert.equal(value.y, label.y);
                });
                if (i + 1 < geometry.dts.length)
                  fail(`flat wide row gap ${i}`, () =>
                    assert.equal(
                      geometry.dts[i + 1].y -
                        Math.max(label.bottom, value.bottom),
                      12,
                    ),
                  );
              }
            }
            fail("no trailing compensating gap", () => {
              assert.equal(geometry.ddMarginBottoms.at(-1), "0px");
              assert.equal(geometry.flatBottom, geometry.dds.at(-1).bottom);
            });
            fail("flat grid tracks", () => {
              assert.equal(geometry.columns, narrow ? 1 : 2);
              assert.equal(geometry.columnGap, "24px");
              assert.equal(geometry.rowGap, narrow ? "4px" : "12px");
            });
            fail("long reference overflow", () =>
              assert(!geometry.pageOverflow),
            );
          };
          verify(await measure(), populated ? "populated" : "empty");
          // Empty -> populated -> empty transitions reuse the same request
          // panel and portal host nodes. Only the host content changes.
          await page.evaluate(() => {
            window.__nodes = {
              host: document.querySelector("#host"),
              request: document.querySelector("aside"),
              button: document.querySelector("#after-facts"),
            };
            document.querySelector("#after-facts").focus();
          });
          const toggled = async (html) => {
            await page.evaluate((markup) => {
              document.querySelector("#host").innerHTML = markup;
            }, html);
            return measure();
          };
          verify(
            await toggled(
              `<section class="session-information-section"><h3>Session details</h3><dl><div><dt>${labels[0]}</dt><dd>wrapped</dd></div></dl></section>`,
            ),
            "populated",
          );
          verify(await toggled(""), "empty");
          assert.equal((await measure()).hostChildCount, 0);
          assert(
            await page.evaluate(
              () =>
                window.__nodes.host === document.querySelector("#host") &&
                window.__nodes.request === document.querySelector("aside") &&
                window.__nodes.button === document.activeElement,
            ),
            "host and request nodes and focus are retained across transitions",
          );
          flatChecks++;
          await page.close();
        }
  }
  console.log(
    JSON.stringify({
      checks,
      flatChecks,
      fixture: "synthetic-session-diagnostics",
      nativeAcceptance: false,
      geometryFailures,
    }),
  );
  assert.equal(geometryFailures.length, 0, "layout geometry assertions");
} finally {
  await browser.close();
}
