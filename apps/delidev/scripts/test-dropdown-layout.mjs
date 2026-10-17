// SPDX-License-Identifier: Apache-2.0
// Explicit cross-family browser validation of the dropdown layout invariance
// contract for the Session Diff options popup, the saved-server row menu, the
// API key row More actions and the Subscription row More actions. For each
// first and last row it records every retained node rectangle and the scroll
// metrics of every owner, opens the popup (pointer and keyboard), drafts,
// toggles, tabs, resizes, scrolls and dismisses it, and asserts that nothing
// moved, nothing was written and the popup is placed and hit-testable.
// Playwright is supplied by the validation host through
// DELIDEV_LAYOUT_PLAYWRIGHT_MODULE or DELIDEV_QA_PLAYWRIGHT_MODULE. All data is
// synthetic. Synthetic browser checks do not establish packaged CEF or native
// acceptance. DELIDEV_DROPDOWN_FAMILY limits the run to one family id and
// DELIDEV_DROPDOWN_TIMEOUT_MS overrides the Playwright action timeout.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-dropdown-layout-"));
let browser,
  server,
  cases = 0,
  checks = 0,
  ownerScrolls = 0;
const scrolledFamilies = new Set();
const errors = [];
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
const label = (language, english, korean) =>
  language === "ko" ? korean : english;

// Each family names its fixture entry, how to reach the rows, the trigger and
// the popup. `anchored` families are top-layer popups placed by the shared
// hook, so their placement (4px gap, 8px inset, 280px cap, flip and clamp) is
// asserted as well as the invariance of every unrelated rectangle and scroll
// metric. A non-anchored family would be checked for invariance and clipping
// only.
const families = [
  {
    id: "session-diff-options",
    // Whether moving keyboard focus out of the popup dismisses it. The
    // desktop contract documents this only for the owners that close on blur;
    // the others stay open until Escape, an outside pointer press, the
    // trigger or an action dismisses them.
    focusOutDismisses: false,
    entry: "diff",
    anchored: true,
    ready: (page) => page.getByText("synthetic diff line 0").waitFor(),
    triggers: (page) =>
      page.locator(".diff-navbar button[aria-haspopup=dialog]"),
    popup: ".diff-options",
    popupOf: (page) => page.locator(".diff-options"),
    items: "button, input",
    afterOpen: async (page) => {
      // Typing a path is a draft only; it never reads or reflows.
      const before = await page.evaluate(() => window.__diffReads.length);
      await page.locator(".diff-options input").fill("src/deep/nested/path");
      assert.equal(
        await page.evaluate(() => window.__diffReads.length),
        before,
      );
    },
    mutations: (page) => page.evaluate(() => window.__diffReads.join(",")),
  },
  {
    id: "saved-connection-row-menu",
    // Whether moving keyboard focus out of the popup dismisses it. The
    // desktop contract documents this only for the owners that close on blur;
    // the others stay open until Escape, an outside pointer press, the
    // trigger or an action dismisses them.
    focusOutDismisses: true,
    entry: "connections",
    anchored: true,
    ready: async (page) => {
      await page.locator(".connections-saved-row").nth(1).waitFor();
      assert.equal(await page.locator(".connections-saved-row").count(), 2);
    },
    triggers: (page) => page.locator(".connections-row-menu summary"),
    popup: ".connections-row-menu-popup",
    popupOf: (page) => page.locator(".connections-row-menu-popup"),
    items: "button",
    mutations: (page) =>
      page
        .locator("html")
        .getAttribute("data-actions")
        .then((value) => value ?? ""),
  },
  {
    id: "api-entry-more-panel",
    // Whether moving keyboard focus out of the popup dismisses it. The
    // desktop contract documents this only for the owners that close on blur;
    // the others stay open until Escape, an outside pointer press, the
    // trigger or an action dismisses them.
    focusOutDismisses: false,
    entry: "api",
    anchored: true,
    ready: async (page) => {
      await page.locator(".api-entry-row").nth(2).waitFor();
      await page.waitForFunction(
        () => window.__apiDetailsFixture.reads.length === 2,
      );
    },
    triggers: (page) => page.locator(".api-entry-row .api-entry-more > button"),
    popup: ".api-entry-more-panel",
    popupOf: (page) => page.locator(".api-entry-more-panel"),
    items: "button",
    mutations: (page) =>
      page.evaluate(() => window.__apiDetailsFixture.effects.join(",")),
  },
  {
    id: "subscription-more-panel",
    // Whether moving keyboard focus out of the popup dismisses it. The
    // desktop contract documents this only for the owners that close on blur;
    // the others stay open until Escape, an outside pointer press, the
    // trigger or an action dismisses them.
    focusOutDismisses: true,
    entry: "settings",
    anchored: true,
    query: "&subscriptionDetails=true",
    ready: async (page, language) => {
      await page
        .getByRole("button", {
          name: label(language, "Settings", "설정"),
          exact: true,
        })
        .click();
      await page.locator(".subscription-row").first().waitFor();
    },
    triggers: (page) =>
      page.locator(".subscription-row .subscription-more > button"),
    popup: ".subscription-more-panel",
    popupOf: (page) => page.locator(".subscription-more-panel"),
    items: "button",
    // Every non-read RPC the fixture transport receives (see
    // `__subscriptionFixture` in settings-layout.fixture.tsx).
    mutations: (page) =>
      page.evaluate(() => window.__subscriptionFixture.writes.join(",")),
    // The counter is only evidence if it can fail: one real write RPC must
    // change it.
    selfCheck: async (page) => {
      const before = await page.evaluate(
        () => window.__subscriptionFixture.writes.length,
      );
      await page.evaluate(() => window.__subscriptionFixture.probe());
      assert.equal(
        await page.evaluate(() => window.__subscriptionFixture.writes.length),
        before + 1,
        "the subscription write counter observes a write RPC",
      );
    },
  },
].filter(
  (family) =>
    !process.env.DELIDEV_DROPDOWN_FAMILY ||
    process.env.DELIDEV_DROPDOWN_FAMILY === family.id,
);
assert(families.length > 0, "unknown DELIDEV_DROPDOWN_FAMILY");
const entries = {
  diff: "src/session-diff-layout.fixture.tsx",
  connections: "src/connections-layout.fixture.tsx",
  api: "src/api-details-layout.fixture.tsx",
  settings: "src/settings-layout.fixture.tsx",
};
const needed = [...new Set(families.map((family) => family.entry))];
try {
  const build = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      plugins: [pluginReact()],
      source: {
        entry: Object.fromEntries(
          needed.map((name) => [name, join(app, entries[name])]),
        ),
      },
      // The Settings fixture imports the terminal emulator's WebAssembly URL.
      tools: {
        rspack: {
          module: {
            rules: [
              {
                test: /\.wasm$/,
                resourceQuery: /url/,
                type: "asset/resource",
                generator: {
                  filename: "static/wasm/[name].[contenthash:8][ext]",
                },
              },
            ],
          },
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
  page.setDefaultTimeout(
    Number(process.env.DELIDEV_DROPDOWN_TIMEOUT_MS ?? 120_000),
  );
  page.on("pageerror", (error) => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  for (const family of families)
    for (const language of ["en", "ko"])
      for (const theme of ["light", "dark"])
        for (const [width, height] of viewports) {
          await page.setViewportSize({ width, height });
          await page.emulateMedia({ colorScheme: theme });
          await page.goto(
            `${origin}/${family.entry}.html?language=${language}&theme=${theme}${family.query ?? ""}`,
          );
          await family.ready(page, language);
          if (family.selfCheck) await family.selfCheck(page);
          const count = await family.triggers(page).count();
          assert(count > 0, `${family.id}: no triggers`);
          // First and last row: the last one sits closest to the lower edge.
          for (const index of new Set([0, count - 1])) {
            const state = `${family.id}/${language}/${theme}/${width}x${height}/row${index}`;
            const trigger = family.triggers(page).nth(index);
            const popup = family.popupOf(page);
            await trigger.scrollIntoViewIfNeeded();
            await frames(page);
            const owners = (node) => {
              const list = [];
              for (
                let owner = node.parentElement;
                owner;
                owner = owner.parentElement
              )
                list.push(owner);
              list.push(document.scrollingElement);
              return list;
            };
            // Rectangles are compared per retained node, not by selector, so a
            // remounted or reflowed element cannot hide a displacement. The
            // popup subtree is excluded: it is the thing being measured.
            const snapshot = (capture) =>
              trigger.evaluate(
                (node, { capture, popup, owners: ownersSource }) => {
                  const ownersOf = new Function(`return (${ownersSource})`)();
                  if (capture)
                    window.__layoutNodes = [
                      ...document.querySelectorAll("#root *"),
                    ].filter((element) => !element.closest(popup));
                  return {
                    rects: window.__layoutNodes.map((element) => {
                      const rect = element.getBoundingClientRect();
                      return [rect.left, rect.top, rect.width, rect.height];
                    }),
                    metrics: ownersOf(node).map((owner) => [
                      owner.scrollWidth,
                      owner.scrollHeight,
                      owner.scrollTop,
                      owner.scrollLeft,
                    ]),
                  };
                },
                { capture, popup: family.popup, owners: owners.toString() },
              );
            const closed = await snapshot(true);
            const writesBefore = await family.mutations(page);
            const assertStable = async (step) => {
              const now = await snapshot(false);
              assert.equal(now.rects.length, closed.rects.length, step);
              now.rects.forEach((rect, node) =>
                rect.forEach((value, edge) =>
                  assert(
                    Math.abs(value - closed.rects[node][edge]) <= 1,
                    `${state} ${step}: node ${node} edge ${edge} moved ${closed.rects[node][edge]} -> ${value}`,
                  ),
                ),
              );
              assert.deepEqual(now.metrics, closed.metrics, `${state} ${step}`);
              checks++;
            };
            // Focus movement may legitimately scroll an owner. Scroll sizes
            // must still match; offsets return to the recorded resting
            // position before rectangles are compared again.
            const restoreScroll = async (step) => {
              const now = await snapshot(false);
              now.metrics.forEach(([w, h], index) => {
                assert.equal(w, closed.metrics[index][0], `${state} ${step}`);
                assert.equal(h, closed.metrics[index][1], `${state} ${step}`);
              });
              await trigger.evaluate(
                (node, { metrics, owners: ownersSource }) => {
                  const ownersOf = new Function(`return (${ownersSource})`)();
                  ownersOf(node).forEach((owner, index) => {
                    owner.scrollTop = metrics[index][2];
                    owner.scrollLeft = metrics[index][3];
                  });
                },
                { metrics: closed.metrics, owners: owners.toString() },
              );
              await frames(page);
            };
            const placement = async (step) => {
              const box = await popup.evaluate(
                (node, { selector, anchored }) => {
                  const anchor = document.querySelector(
                    "[data-dropdown-anchor]",
                  );
                  const rect = node.getBoundingClientRect(),
                    from = anchor.getBoundingClientRect();
                  if (node.scrollHeight > node.clientHeight + 1)
                    node.scrollTop = 0;
                  const hits = [...node.querySelectorAll(selector)].map(
                    (item) => {
                      if (node.scrollHeight > node.clientHeight + 1)
                        item.scrollIntoView({ block: "nearest" });
                      const box = item.getBoundingClientRect(),
                        hit = document.elementFromPoint(
                          box.left + box.width / 2,
                          box.top + box.height / 2,
                        );
                      return {
                        inView:
                          box.top >= 0 &&
                          box.left >= 0 &&
                          box.bottom <= innerHeight &&
                          box.right <= innerWidth,
                        // The tested item itself (or one of its descendants such as a
                        // label or icon) must own the hit-test result; a sibling,
                        // the panel or a pointer-events:none item must fail.
                        onTop: item.contains(hit),
                      };
                    },
                  );
                  return {
                    open: node.matches(":popover-open"),
                    popover: node.getAttribute("popover"),
                    position: getComputedStyle(node).position,
                    left: rect.left,
                    right: rect.right,
                    top: rect.top,
                    bottom: rect.bottom,
                    height: rect.height,
                    anchorBottom: from.bottom,
                    anchorTop: from.top,
                    vw: innerWidth,
                    vh: innerHeight,
                    hits,
                    anchored,
                  };
                },
                { selector: family.items, anchored: family.anchored },
              );
              const detail = `${state} ${step}: ${JSON.stringify(box)}`;
              assert(box.hits.length > 0, detail);
              for (const hit of box.hits) {
                assert(hit.inView, detail);
                assert(hit.onTop, detail);
              }
              if (family.anchored) {
                assert(box.open, detail);
                assert.equal(box.popover, "manual", detail);
                assert.equal(box.position, "fixed", detail);
                assert(box.height <= 280.5, detail);
                assert(box.left >= 7.5 && box.top >= 7.5, detail);
                assert(box.right <= box.vw - 7.5, detail);
                assert(box.bottom <= box.vh - 7.5, detail);
                // 4px gap below the trigger, or above when it flips, whenever
                // the viewport leaves room for the preferred placement.
                const below = box.top - box.anchorBottom,
                  above = box.anchorTop - box.bottom;
                if (width >= 640)
                  assert(
                    Math.abs(below - 4) <= 0.5 || Math.abs(above - 4) <= 0.5,
                    detail,
                  );
              }
              checks++;
            };
            await trigger.evaluate((node) =>
              node.setAttribute("data-dropdown-anchor", ""),
            );
            const open = async () => {
              await trigger.click();
              await popup.waitFor();
              await frames(page);
            };
            const assertClosed = async (step) => {
              assert.equal(await popup.count(), 0, `${state} ${step}`);
            };
            // Open from a pointer click: nothing moves, nothing is written and
            // every item is reachable and hit-testable above other content.
            await open();
            await assertStable("open");
            await placement("open");
            if (family.afterOpen) {
              await family.afterOpen(page);
              await assertStable("draft");
            }
            // Escape dismisses and keeps the layout where it started.
            await page.keyboard.press("Escape");
            await assertClosed("escape");
            await assertStable("escape");
            assert(
              await trigger.evaluate((node) => node === document.activeElement),
              `${state} escape focus`,
            );
            // Toggle closed with the trigger itself.
            await open();
            await trigger.click();
            await assertClosed("toggle");
            await assertStable("toggle");
            // Keyboard open, Tab through every item and back out.
            await trigger.focus();
            await page.keyboard.press("Enter");
            await popup.waitFor();
            await frames(page);
            await assertStable("keyboard open");
            await placement("keyboard open");
            // Escape from inside the popup closes it and keeps the layout.
            await page.keyboard.press("Tab");
            await restoreScroll("tab");
            await assertStable("tab");
            await page.keyboard.press("Escape");
            await assertClosed("tab escape");
            await restoreScroll("tab escape");
            await assertStable("tab escape");
            // Tab through every enabled item until focus leaves the popup.
            // Each focused control is recorded, so the sequence provably
            // reaches the whole popup; the layout stays put on every step.
            await trigger.focus();
            await page.keyboard.press("Enter");
            await popup.waitFor();
            await frames(page);
            const tabbable = () =>
              popup.evaluate(
                (node, selector) =>
                  [...node.querySelectorAll(selector)].filter(
                    (item) => !item.disabled && item.tabIndex >= 0,
                  ).length,
                family.items,
              );
            const focusInside = () =>
              popup.evaluate((node) => {
                const active = document.activeElement;
                if (!active || !node.contains(active)) return null;
                // Identify a control by its position inside the popup.
                return [...node.querySelectorAll("*")].indexOf(active);
              });
            const expected = await tabbable();
            assert(expected > 0, `${state} tab sequence has enabled items`);
            const reached = new Set();
            const initial = await focusInside();
            if (initial !== null) reached.add(initial);
            let left = false;
            for (let step = 0; step < expected + 4 && !left; step++) {
              await page.keyboard.press("Tab");
              await frames(page);
              if ((await popup.count()) === 0) {
                left = true;
                break;
              }
              const position = await focusInside();
              if (position === null) left = true;
              else reached.add(position);
              await restoreScroll(`tab step ${step}`);
              await assertStable(`tab step ${step}`);
            }
            assert(left, `${state} focus leaves the popup after its last item`);
            assert.equal(
              reached.size,
              expected,
              `${state} Tab reached ${reached.size} of ${expected} enabled items`,
            );
            if (family.focusOutDismisses) {
              await assertClosed("tab out");
            } else {
              // No focus-out dismissal is documented for this owner: the popup
              // stays open and unmoved until the documented outside press.
              assert.equal(await popup.count(), 1, `${state} stays open`);
              await assertStable("tab out");
              await page.mouse.click(1, height - 1);
              await frames(page);
              await assertClosed("tab out outside pointer");
            }
            await restoreScroll("tab out");
            await assertStable("tab out");
            // Resize and owner scroll while open keep the popup reachable.
            await open();
            await page.setViewportSize({ width: width + 40, height });
            await frames(page);
            await placement("resized");
            await page.setViewportSize({ width, height });
            await frames(page);
            // Scroll the real overflowing owner (when the fixture has one) by
            // 48px toward keeping the trigger visible: the popup must follow
            // the resulting anchor geometry, not stay behind.
            const ownerScroll = await trigger.evaluate((node) => {
              let owner = node.parentElement;
              for (; owner; owner = owner.parentElement) {
                const overflow = getComputedStyle(owner).overflowY;
                if (
                  (overflow === "auto" || overflow === "scroll") &&
                  owner.scrollHeight > owner.clientHeight
                )
                  break;
              }
              // Fixtures without an overflowing ancestor scroll the page.
              if (
                !owner &&
                document.scrollingElement.scrollHeight > innerHeight
              )
                owner = document.scrollingElement;
              if (!owner) return null;
              const frame =
                  owner === document.scrollingElement
                    ? { top: 0, height: innerHeight }
                    : owner.getBoundingClientRect(),
                anchor = node.getBoundingClientRect(),
                before = owner.scrollTop,
                toward =
                  anchor.top + anchor.height / 2 < frame.top + frame.height / 2
                    ? -48
                    : 48;
              owner.scrollTop = before + toward;
              if (owner.scrollTop === before) owner.scrollTop = before - toward;
              return {
                moved: owner.scrollTop !== before,
                anchorBefore: anchor.top,
              };
            });
            if (ownerScroll) {
              assert(ownerScroll.moved, `${state} owner scroll moved`);
              await frames(page);
              await placement("scrolled");
              const after = await trigger.evaluate(
                (node) => node.getBoundingClientRect().top,
              );
              assert(
                Math.abs(after - ownerScroll.anchorBefore) > 1,
                `${state} anchor moved with the owner`,
              );
              ownerScrolls++;
              scrolledFamilies.add(family.id);
            }
            // Outside pointer dismisses top-layer menus that own dismissal.
            // The press alone must close them; no second dismissal path.
            if (family.anchored) {
              await page.mouse.click(1, height - 1);
              await frames(page);
              await assertClosed("outside pointer");
            } else await page.keyboard.press("Escape");
            await restoreScroll("outside");
            await assertClosed("outside");
            await assertStable("outside");
            assert.equal(
              await family.mutations(page),
              writesBefore,
              `${state} no mutation`,
            );
            await trigger.evaluate((node) =>
              node.removeAttribute("data-dropdown-anchor"),
            );
            cases++;
          }
        }
  assert.deepEqual(errors, []);
  for (const family of families)
    assert(
      scrolledFamilies.has(family.id),
      `${family.id}: no case scrolled a real owner`,
    );
  console.log(
    JSON.stringify({
      operation: "dropdown_layout",
      families: families.map((family) => family.id),
      cases,
      checks,
      ownerScrolls,
    }),
  );
} finally {
  await browser?.close();
  await new Promise((done) => (server ? server.close(done) : done()));
  await rm(directory, { recursive: true, force: true });
}
