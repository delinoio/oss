// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation of the Reasoning effort / Subagent effort
// suggestions; Playwright is supplied by the validation host through
// DELIDEV_LAYOUT_PLAYWRIGHT_MODULE or DELIDEV_QA_PLAYWRIGHT_MODULE. All data is
// synthetic. Synthetic browser checks do not establish packaged CEF or native
// acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath =
  process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE ??
  process.env.DELIDEV_QA_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright"
);
const directory = await mkdtemp(join(tmpdir(), "delidev-reasoning-layout-"));
let browser,
  server,
  cases = 0,
  checks = 0;
const errors = [];
// The short page leaves the scrolling content pane taller than the content
// column, so a popup bounded by the pane instead of the column would extend
// below the column.
const hosts = [
  ["page", "tall"],
  ["page", "short"],
  ["dialog", "tall"],
];
const fields = ["primary", "subagent"];
const viewports = [
  [1280, 800],
  [640, 480],
  // Half-size CSS viewports model effective 200% zoom.
  [320, 240],
];
const frames = (page) =>
  page.evaluate(
    () =>
      new Promise((done) =>
        requestAnimationFrame(() => requestAnimationFrame(done)),
      ),
  );
try {
  const build = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      plugins: [pluginReact()],
      source: {
        entry: {
          index: join(app, "src/reasoning-effort-layout.fixture.tsx"),
        },
      },
      html: { template: join(app, "index.html") },
      output: {
        distPath: { root: directory },
        assetPrefix: "/",
        sourceMap: false,
        cleanDistPath: true,
      },
    },
  });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(
        directory,
        `.${pathname === "/" ? "/index.html" : pathname}`,
      );
      if (!file.startsWith(`${directory}${sep}`))
        throw new Error("Invalid fixture path");
      response.setHeader(
        "Content-Security-Policy",
        "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'none'; base-uri 'none'",
      );
      response.setHeader(
        "Content-Type",
        {
          ".html": "text/html",
          ".js": "application/javascript",
          ".css": "text/css",
          ".svg": "image/svg+xml",
        }[extname(file)] ?? "application/octet-stream",
      );
      response.end(await readFile(file));
    } catch {
      response.writeHead(404);
      response.end();
    }
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({
    headless: true,
    ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL
      ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL }
      : {}),
  });
  const page = await browser.newPage();
  // Real-Chromium actions can stall on a heavily loaded validation host.
  page.setDefaultTimeout(120_000);
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const [host, layout] of hosts)
    for (const language of ["en", "ko"])
      for (const theme of ["light", "dark"])
        for (const [width, height] of viewports) {
          await page.setViewportSize({ width, height });
          await page.emulateMedia({ colorScheme: theme });
          await page.goto(
            `${origin}/?host=${host}&layout=${layout}&language=${language}&theme=${theme}`,
          );
          await page.locator(".reasoning-effort-field").nth(1).waitFor();
          // The subagent field is enabled once the capability query resolves.
          await page
            .locator(".reasoning-effort-field")
            .nth(1)
            .locator("input:enabled")
            .waitFor();
          for (const [fieldIndex, field] of fields.entries()) {
            const state = `${host}-${layout}/${language}/${theme}/${width}x${height}/${field}`;
            const root = page
              .locator(".reasoning-effort-field")
              .nth(fieldIndex);
            const input = root.locator("input[role=combobox]");
            const toggle = root.locator("button.reasoning-effort-toggle");
            const popup = root.locator(".reasoning-effort-popup");
            await input.fill("");
            await input.evaluate((node) => node.blur());
            await input.scrollIntoViewIfNeeded();
            // Rectangles are compared per retained node, not by selector, so a
            // remounted or reflowed element cannot hide a displacement. Scroll
            // owners are every ancestor of the input plus the document.
            const snapshot = (capture) =>
              input.evaluate((node, capture) => {
                if (capture)
                  window.__layoutNodes = [
                    ...document.querySelectorAll(
                      "[data-scroll-owner], [data-scroll-owner] *",
                    ),
                  ];
                const owners = [];
                for (
                  let owner = node.parentElement;
                  owner;
                  owner = owner.parentElement
                )
                  owners.push(owner);
                owners.push(document.scrollingElement);
                return {
                  rects: window.__layoutNodes.map((element) => {
                    const rect = element.getBoundingClientRect();
                    return [rect.left, rect.top, rect.width, rect.height];
                  }),
                  metrics: owners.map((owner) => [
                    owner.scrollWidth,
                    owner.scrollHeight,
                    owner.scrollTop,
                    owner.scrollLeft,
                  ]),
                };
              }, capture);
            let closed = await snapshot(true);
            const assertStable = async (label) => {
              const now = await snapshot(false);
              assert.equal(now.rects.length, closed.rects.length, label);
              now.rects.forEach((rect, index) =>
                rect.forEach((value, edge) =>
                  assert(
                    Math.abs(value - closed.rects[index][edge]) <= 1,
                    `${state} ${label}: node ${index} edge ${edge} moved ${closed.rects[index][edge]} -> ${value}`,
                  ),
                ),
              );
              assert.deepEqual(
                now.metrics,
                closed.metrics,
                `${state} ${label}`,
              );
              checks++;
            };
            // Tab and pointer focus may legitimately scroll an owner. Scroll
            // sizes must still match; offsets return to the recorded resting
            // position before rectangles are compared again.
            const restoreScroll = async (label) => {
              const now = await snapshot(false);
              now.metrics.forEach(([w, h], index) => {
                assert.equal(w, closed.metrics[index][0], `${state} ${label}`);
                assert.equal(h, closed.metrics[index][1], `${state} ${label}`);
              });
              await input.evaluate((node, metrics) => {
                const owners = [];
                for (
                  let owner = node.parentElement;
                  owner;
                  owner = owner.parentElement
                )
                  owners.push(owner);
                owners.push(document.scrollingElement);
                owners.forEach((owner, index) => {
                  owner.scrollTop = metrics[index][2];
                  owner.scrollLeft = metrics[index][3];
                });
              }, closed.metrics);
              await frames(page);
            };
            const geometry = () =>
              input.evaluate((node) => {
                const field = node.closest(".reasoning-effort-field"),
                  popup = field.querySelector(".reasoning-effort-popup"),
                  box = popup.getBoundingClientRect(),
                  anchor = node.getBoundingClientRect(),
                  boundary = node.closest(
                    ".settings-task-body, .settings-content-column",
                  ),
                  owner = boundary.getBoundingClientRect(),
                  pane = node
                    .closest(".settings-content")
                    .getBoundingClientRect(),
                  center = document.elementFromPoint(
                    box.left + box.width / 2,
                    box.top + Math.min(box.height, 20),
                  );
                return {
                  open: popup.matches(":popover-open"),
                  popover: popup.getAttribute("popover"),
                  position: getComputedStyle(popup).position,
                  left: box.left,
                  right: box.right,
                  top: box.top,
                  bottom: box.bottom,
                  width: box.width,
                  height: box.height,
                  anchorLeft: anchor.left,
                  anchorTop: anchor.top,
                  anchorBottom: anchor.bottom,
                  anchorWidth: anchor.width,
                  boundLeft: Math.max(0, owner.left),
                  boundTop: Math.max(0, owner.top),
                  boundRight: Math.min(innerWidth, owner.right),
                  boundBottom: Math.min(innerHeight, owner.bottom),
                  paneBottom: Math.min(innerHeight, pane.bottom),
                  boundaryClass: boundary.className,
                  onTop: popup.contains(center),
                  inDialog: Boolean(field.closest("dialog")),
                  focused: node === document.activeElement,
                  expanded: node.getAttribute("aria-expanded"),
                  controls:
                    node.getAttribute("aria-controls") ===
                    popup.querySelector("[role=listbox]").id,
                };
              });
            const assertPlacement = async (label) => {
              const popupBox = await geometry();
              const detail = `${state} ${label}: ${JSON.stringify(popupBox)}`;
              assert(popupBox.open, detail);
              assert.equal(popupBox.popover, "manual", detail);
              assert.equal(popupBox.position, "fixed", detail);
              assert(popupBox.height <= 280.5, detail);
              // Anchored to the input at its width with an 8px inset.
              assert(
                Math.abs(
                  popupBox.width -
                    Math.min(
                      popupBox.anchorWidth,
                      popupBox.boundRight - popupBox.boundLeft - 16,
                    ),
                ) <= 0.5,
                detail,
              );
              assert(popupBox.left >= popupBox.boundLeft + 7.5, detail);
              assert(popupBox.right <= popupBox.boundRight - 7.5, detail);
              assert(popupBox.top >= popupBox.boundTop + 7.5, detail);
              assert(popupBox.bottom <= popupBox.boundBottom - 7.5, detail);
              // The boundary is the dialog's task body or the page's content
              // column, never the wider scrolling pane around it.
              assert(
                host === "dialog"
                  ? popupBox.boundaryClass.includes("settings-task-body")
                  : popupBox.boundaryClass.includes("settings-content-column"),
                detail,
              );
              if (layout === "short" && height >= 800)
                assert(
                  popupBox.paneBottom - popupBox.boundBottom > 40,
                  `${detail}: the short page must leave pane space below the column`,
                );
              const below = popupBox.top - popupBox.anchorBottom,
                above = popupBox.anchorTop - popupBox.bottom;
              assert(below >= 3.5 || above >= 3.5, detail);
              assert(
                Math.abs(below - 4) <= 0.5 ||
                  Math.abs(above - 4) <= 0.5 ||
                  popupBox.height <= 280.5,
                detail,
              );
              // Top layer: the popup is hit-testable above a modal dialog, and
              // focus plus the combobox contract stay on the input.
              assert(popupBox.onTop, detail);
              assert.equal(popupBox.inDialog, host === "dialog", detail);
              assert(popupBox.focused, detail);
              assert.equal(popupBox.expanded, "true", detail);
              assert(popupBox.controls, detail);
              checks++;
              return popupBox;
            };
            const assertClosed = async (label) =>
              assert.equal(await popup.count(), 0, `${state} ${label}`);
            const optionTexts = () =>
              popup.locator("[role=option]").allTextContents();
            const outsidePointer = async () => {
              const outside = page.locator(
                "[data-outside], .settings-task-header h2",
              );
              const count = await outside.count();
              let clicked = false;
              for (let index = 0; index < count && !clicked; index++) {
                const candidate = outside.nth(index);
                await candidate.scrollIntoViewIfNeeded();
                await frames(page);
                const box = await candidate.boundingBox();
                const free = await candidate.evaluate((node) => {
                  const rect = node.getBoundingClientRect();
                  const hit = document.elementFromPoint(
                    rect.left + 4,
                    rect.top + rect.height / 2,
                  );
                  return hit === node;
                });
                if (!box || !free) continue;
                await page.mouse.click(box.x + 4, box.y + box.height / 2);
                clicked = true;
              }
              assert(clicked, `${state}: an outside pointer target exists`);
            };

            // Open from a click: nothing moves and nothing is written.
            await input.click();
            const texts = await optionTexts();
            // The native default row plus the nine Codex suggestions.
            assert.equal(texts.length, 10, state);
            assert.deepEqual(texts.slice(1).slice(-3), [
              "max",
              "ultra",
              "persistent",
            ]);
            const nativeDefault = texts[0];
            await assertStable("open");
            await assertPlacement("open");
            assert.equal(await input.inputValue(), "");
            // Every option stays reachable at narrow widths and 200% zoom:
            // the last option scrolls into the capped popup without moving any
            // scroll owner, and the page never scrolls to reveal it.
            await input.press("ArrowUp");
            const last = popup.locator("[role=option]").last();
            assert.equal(
              await input.getAttribute("aria-activedescendant"),
              await last.getAttribute("id"),
              state,
            );
            const reachable = await last.evaluate((node) => {
              const popupBox = node
                  .closest(".reasoning-effort-popup")
                  .getBoundingClientRect(),
                rect = node.getBoundingClientRect();
              return (
                rect.top >= popupBox.top - 1 &&
                rect.bottom <= popupBox.bottom + 1
              );
            });
            assert(reachable, `${state}: last option is reachable`);
            await assertStable("last option active");
            await input.press("ArrowDown");

            // Filtering and the empty status live inside the popup.
            await input.fill("hi");
            assert.deepEqual(
              await optionTexts(),
              [nativeDefault, "high"],
              state,
            );
            await assertStable("filtered");
            await assertPlacement("filtered");
            await input.fill("zzz");
            const status = popup.locator("[role=status]");
            assert.equal(await status.count(), 1, state);
            assert.equal(
              await status.evaluate(
                (node) =>
                  node.closest(".reasoning-effort-popup") !== null &&
                  node.closest(".reasoning-effort-field") !== null,
              ),
              true,
              state,
            );
            await assertStable("empty");
            await assertPlacement("empty");
            // Escape closes without selecting; the typed draft is retained.
            await input.press("Escape");
            assert.equal(await input.inputValue(), "zzz");
            await assertClosed("escape");
            await assertStable("escape");

            // Tab and an outside pointer also close without changing the draft.
            await input.click();
            await assertPlacement("reopened");
            await input.press("Tab");
            assert.equal(await input.inputValue(), "zzz");
            await assertClosed("tab");
            await restoreScroll("tab");
            await assertStable("tab");
            await input.click();
            await input.fill("zzz");
            await outsidePointer();
            assert.equal(await input.inputValue(), "zzz");
            await assertClosed("outside");
            await restoreScroll("outside");
            await assertStable("outside");

            // The list button opens and closes the same overlay.
            await input.fill("");
            await input.evaluate((node) => node.blur());
            await assertClosed("blur");
            await toggle.click();
            await assertPlacement("toggle open");
            await assertStable("toggle open");
            await toggle.click();
            await assertClosed("toggle close");
            await assertStable("toggle close");

            // Repositions with the nearest scroll owner and never scrolls it.
            await input.click();
            const moved = await input.evaluate((node) => {
              for (
                let owner = node.parentElement;
                owner;
                owner = owner.parentElement
              ) {
                const style = getComputedStyle(owner);
                if (
                  /(auto|scroll)/.test(style.overflowY) &&
                  owner.scrollHeight - owner.clientHeight > 80
                ) {
                  owner.scrollTop += 40;
                  return true;
                }
              }
              return false;
            });
            // The short page has no scrolling room by design.
            assert(
              moved || layout === "short",
              `${state}: a scroll owner with room exists`,
            );
            await frames(page);
            await assertPlacement("owner scrolled");
            await restoreScroll("owner restored");
            await assertPlacement("owner restored");

            // Near the bottom edge the popup flips above and stays clamped.
            await input.press("Escape");
            await input.evaluate((node) => {
              for (
                let owner = node.parentElement;
                owner;
                owner = owner.parentElement
              ) {
                const style = getComputedStyle(owner);
                if (
                  /(auto|scroll)/.test(style.overflowY) &&
                  owner.scrollHeight > owner.clientHeight
                ) {
                  const ownerBox = owner.getBoundingClientRect(),
                    box = node.getBoundingClientRect();
                  owner.scrollTop += box.bottom - (ownerBox.bottom - 56);
                  return;
                }
              }
            });
            await frames(page);
            closed = await snapshot(true);
            await input.click();
            const edge = await assertPlacement("bottom edge");
            await assertStable("bottom edge");
            if (height >= 480 && edge.anchorTop - 8 >= 280 + 4)
              assert(
                edge.bottom <= edge.anchorTop - 3.5,
                `${state}: popup flips above near the bottom edge`,
              );
            await input.press("Escape");
            await assertClosed("edge escape");

            // Selecting a suggestion by pointer writes it once and closes.
            await input.fill("");
            await input.click();
            await popup.locator("[role=option]", { hasText: /^high$/ }).click();
            assert.equal(await input.inputValue(), "high");
            await assertClosed("selected");
            await assertStable("selected");
            cases++;
            process.stdout.write(
              JSON.stringify({
                event: "reasoning_effort_layout_case",
                host,
                layout,
                language,
                theme,
                width,
                height,
                field,
              }) + "\n",
            );
          }
        }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      event: "reasoning_effort_layout_passed",
      cases,
      checks,
      nativeAcceptance: "not-performed",
    }) + "\n",
  );
} finally {
  await browser?.close();
  await new Promise((done) => (server ? server.close(done) : done()));
  await rm(directory, { recursive: true, force: true });
}
