// SPDX-License-Identifier: Apache-2.0
// Explicit browser validation of the queued-message More menu; Playwright is supplied by the validation host through
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
const directory = await mkdtemp(join(tmpdir(), "delidev-queue-layout-"));
let browser,
  server,
  cases = 0,
  checks = 0;
const errors = [];
const places = ["top", "bottom"];
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
          index: join(app, "src/queue-layout.fixture.tsx"),
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
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const place of places)
        for (const [width, height] of viewports) {
          await page.setViewportSize({ width, height });
          await page.emulateMedia({ colorScheme: theme });
          await page.goto(
            `${origin}/?language=${language}&theme=${theme}&place=${place}`,
          );
          await page.locator(".queue-drop-row").nth(3).waitFor();
          const rows = page.locator(".queue-drop-row");
          // Row 0 is the first row (Move up unavailable), row 3 the last row,
          // clipped by the scrolling list and, at the bottom place, by the
          // viewport edge (Move down unavailable).
          for (const rowIndex of [0, 3]) {
            const state = `${language}/${theme}/${place}/${width}x${height}/row${rowIndex}`;
            const row = rows.nth(rowIndex);
            const more = row.locator("button[aria-haspopup=menu]");
            const popup = row.locator(".queue-more-popup");
            const items = popup.locator("[role=menuitem]");
            await row.scrollIntoViewIfNeeded();
            // The trigger is fully visible before the closed geometry is
            // recorded, so pointer actions never scroll an owner themselves.
            await more.scrollIntoViewIfNeeded();
            await frames(page);
            // Rectangles are compared per retained node, not by selector, so a
            // remounted or reflowed element cannot hide a displacement. Scroll
            // owners are every ancestor of the trigger plus the document.
            const snapshot = (capture) =>
              more.evaluate((node, capture) => {
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
              await more.evaluate((node, metrics) => {
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
              more.evaluate((node) => {
                const popup = node
                    .closest(".queue-more")
                    .querySelector(".queue-more-popup"),
                  box = popup.getBoundingClientRect(),
                  anchor = node.getBoundingClientRect(),
                  hits = [...popup.querySelectorAll("[role=menuitem]")].map(
                    (item) => {
                      // A capped popup scrolls inside itself; an item that is
                      // out of view must become reachable by that scroll alone.
                      if (popup.scrollHeight > popup.clientHeight + 1)
                        item.scrollIntoView({ block: "nearest" });
                      const rect = item.getBoundingClientRect(),
                        hit = document.elementFromPoint(
                          rect.left + rect.width / 2,
                          rect.top + rect.height / 2,
                        );
                      return {
                        // Reachable: inside the popup box and hit-testable
                        // above the clipping list and the composer.
                        inside:
                          rect.top >= box.top - 1 &&
                          rect.bottom <= box.bottom + 1 &&
                          rect.left >= box.left - 1 &&
                          rect.right <= box.right + 1,
                        onTop: item.contains(hit),
                      };
                    },
                  ),
                  list = node
                    .closest(".queue-compact-list")
                    .getBoundingClientRect();
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
                  anchorRight: anchor.right,
                  anchorTop: anchor.top,
                  anchorBottom: anchor.bottom,
                  boundRight: innerWidth,
                  boundBottom: innerHeight,
                  listTop: list.top,
                  listBottom: list.bottom,
                  hits,
                  scrollable: popup.scrollHeight > popup.clientHeight + 1,
                  focusedItem:
                    document.activeElement?.getAttribute("role") ===
                      "menuitem" &&
                    popup.contains(document.activeElement) &&
                    document.activeElement ===
                      popup.querySelector("[role=menuitem]"),
                  expanded: node.getAttribute("aria-expanded"),
                };
              });
            const assertPlacement = async (label, { focused = true } = {}) => {
              const box = await geometry();
              const detail = `${state} ${label}: ${JSON.stringify(box)}`;
              assert(box.open, detail);
              assert.equal(box.popover, "manual", detail);
              assert.equal(box.position, "fixed", detail);
              assert(box.height <= 280.5, detail);
              // 8px inset from the viewport (the owning surface of the tray).
              assert(box.left >= 7.5, detail);
              assert(box.right <= box.boundRight - 7.5, detail);
              assert(box.top >= 7.5, detail);
              assert(box.bottom <= box.boundBottom - 7.5, detail);
              // End-aligned with the trigger unless clamped by the inset.
              assert(
                Math.abs(box.right - box.anchorRight) <= 0.5 ||
                  Math.abs(box.left - 8) <= 0.5 ||
                  Math.abs(box.right - (box.boundRight - 8)) <= 0.5,
                detail,
              );
              // 4px gap below the trigger, or above when it flips.
              const below = box.top - box.anchorBottom,
                above = box.anchorTop - box.bottom;
              assert(
                Math.abs(below - 4) <= 0.5 || Math.abs(above - 4) <= 0.5,
                detail,
              );
              // All three items are reachable and above the clipping list.
              assert.equal(box.hits.length, 3, detail);
              for (const hit of box.hits) {
                assert(hit.inside, detail);
                assert(hit.onTop, detail);
              }
              assert.equal(box.expanded, "true", detail);
              if (focused) assert(box.focusedItem, detail);
              checks++;
              return box;
            };
            const assertClosed = async (label) => {
              assert.equal(await popup.count(), 0, `${state} ${label}`);
              assert.equal(
                await more.getAttribute("aria-expanded"),
                "false",
                `${state} ${label}`,
              );
            };
            const open = async () => {
              await more.click();
              await popup.waitFor();
            };

            // Open from a click: nothing moves and nothing is written, the
            // first item takes focus and every item stays reachable.
            await open();
            assert.equal(await items.count(), 3, state);
            await assertStable("open");
            const placed = await assertPlacement("open");
            // Rows inside the scrolling list overflow it: the clipped list
            // never clips the menu.
            if (rowIndex === 3)
              assert(
                placed.bottom > placed.listBottom ||
                  placed.top < placed.listTop ||
                  placed.height <= placed.listBottom - placed.listTop,
                `${state}: popup is not confined to the list`,
              );
            // Tab moves to the next enabled item (disabled movement items are
            // skipped) while the menu stays open.
            await page.keyboard.press("Tab");
            assert.equal(
              await popup.evaluate(
                (node) =>
                  node.contains(document.activeElement) &&
                  document.activeElement !==
                    node.querySelector("[role=menuitem]"),
              ),
              true,
              state,
            );
            await assertPlacement("tab inside", { focused: false });
            await restoreScroll("tab inside");
            await assertStable("tab inside");

            // Escape closes without an action and returns focus to More.
            await page.keyboard.press("Escape");
            await assertClosed("escape");
            assert.equal(
              await more.evaluate((node) => node === document.activeElement),
              true,
              `${state}: Escape restores focus to More`,
            );
            await assertStable("escape");

            // The trigger toggles the same menu.
            await open();
            await more.click();
            await assertClosed("toggle");
            await restoreScroll("toggle");
            await assertStable("toggle");

            // An outside pointer closes it without an action.
            await open();
            await page
              .locator("[data-outside]")
              .click({ position: { x: 4, y: 4 } });
            await assertClosed("outside pointer");
            await restoreScroll("outside pointer");
            await assertStable("outside pointer");

            // Tabbing past the last item closes the menu.
            await open();
            for (let step = 0; step < 6 && (await popup.count()); step++)
              await page.keyboard.press("Tab");
            await assertClosed("tab out");
            await restoreScroll("tab out");
            await assertStable("tab out");

            // Repositions with the nearest scroll owner and never scrolls it.
            await open();
            const moved = await more.evaluate((node) => {
              for (
                let owner = node.parentElement;
                owner;
                owner = owner.parentElement
              ) {
                const style = getComputedStyle(owner);
                if (
                  /(auto|scroll)/.test(style.overflowY) &&
                  owner.scrollHeight - owner.clientHeight > 16
                ) {
                  owner.scrollTop += 12;
                  return owner.scrollTop;
                }
              }
              return false;
            });
            assert(moved, `${state}: a scroll owner with room exists`);
            await frames(page);
            await assertPlacement("owner scrolled", { focused: false });
            await restoreScroll("owner restored");
            await assertPlacement("owner restored", { focused: false });
            await page.keyboard.press("Escape");
            await assertClosed("owner escape");
            await assertStable("owner escape");

            // A row scrolled completely out of the clipping list dismisses its
            // menu instead of leaving the top-layer popup beside another row.
            await open();
            await more.evaluate((node, last) => {
              const list = node.closest(".queue-compact-list");
              list.scrollTop = last ? 0 : list.scrollHeight;
            }, rowIndex === 3);
            await frames(page);
            const hidden = await more.evaluate((node) => {
              const list = node
                  .closest(".queue-compact-list")
                  .getBoundingClientRect(),
                anchor = node.getBoundingClientRect();
              return anchor.bottom <= list.top || anchor.top >= list.bottom;
            });
            assert(hidden, `${state}: the trigger is scrolled out of the list`);
            await assertClosed("trigger scrolled out of the list");
            await restoreScroll("trigger scrolled out of the list");
            await assertStable("trigger scrolled out of the list");

            // Movement actions run once from the menu and close it.
            if (rowIndex === 3) {
              await open();
              await items.nth(1).click();
              await assertClosed("move up");
              await restoreScroll("move up");
              await assertStable("move up");
              assert.deepEqual(
                await page.evaluate(() => window.__queueMoves),
                ["up:3"],
                state,
              );
            }
            // Edit opens the retained editor in the row; nothing is saved.
            if (rowIndex === 0) {
              await open();
              await items.nth(0).click();
              await assertClosed("edit");
              assert.equal(await row.locator("textarea").count(), 1, state);
            }
            assert.deepEqual(
              await page.evaluate(() => window.__queueMutations),
              [],
              `${state}: no queue mutation from open, close or placement`,
            );
            cases++;
            process.stdout.write(
              JSON.stringify({
                event: "queue_more_layout_case",
                language,
                theme,
                place,
                width,
                height,
                row: rowIndex,
              }) + "\n",
            );
          }
        }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      event: "queue_more_layout_passed",
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
