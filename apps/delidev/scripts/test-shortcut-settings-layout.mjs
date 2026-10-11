// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright"
);
const directory = await mkdtemp(join(tmpdir(), "delidev-shortcut-settings-"));
let browser, server, page;
let cases = 0;
try {
  const build = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      plugins: [pluginReact()],
      source: {
        entry: { index: join(app, "src/shortcut-settings-layout.fixture.tsx") },
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
  page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;

  const catalogs = {};
  for (const language of ["en", "ko"])
    catalogs[language] = JSON.parse(
      await readFile(join(app, `src/locales/${language}/shortcuts.json`)),
    );
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const [width, height] of [
        [1440, 900],
        [960, 640],
        [640, 480],
        [480, 320],
        [320, 240],
      ]) {
        const t = (key) => catalogs[language][key];
        await page.setViewportSize({ width, height });
        await page.goto(`${origin}/?language=${language}&theme=${theme}`);
        await page
          .getByText(t("shortcut-settings.saved"), { exact: true })
          .waitFor();
        assert.equal(
          await page
            .getByText(t("shortcut-settings.scope"), { exact: true })
            .count(),
          1,
        );
        const catalog = page.getByRole("region", {
          name: t("shortcut-settings.catalog"),
          exact: true,
        });
        assert.equal(await catalog.getAttribute("tabindex"), "0");
        assert.equal(
          await catalog.locator(".shortcut-settings-row").count(),
          7,
        );
        assert.equal(
          await catalog
            .locator('[data-settings-action-presentation="icon"]')
            .count(),
          21,
        );
        const footer = page.locator(".shortcut-settings-footer");
        assert.equal(
          await footer
            .locator('[data-settings-action-presentation="icon"]')
            .count(),
          0,
        );
        assert.equal(
          await catalog
            .locator(".shortcut-settings-guidance, .shortcut-settings-footer")
            .count(),
          0,
        );
        for (const key of [
          "shortcut-settings.save",
          "shortcut-settings.discard",
        ])
          assert(
            await page
              .getByRole("button", { name: t(key), exact: true })
              .isDisabled(),
          );
        assert(
          await page
            .getByRole("button", {
              name: t("shortcut-settings.restoreAll"),
              exact: true,
            })
            .isEnabled(),
        );
        const bounds = async () =>
          page.evaluate(() => {
            const main = document.querySelector("main"),
              list = document.querySelector(".shortcut-settings-catalog"),
              heading = document.querySelector(".settings-category-heading"),
              footer = document.querySelector(".shortcut-settings-footer");
            return {
              mainScroll: main.scrollTop,
              fits: main.scrollHeight <= main.clientHeight + 2,
              listScroll: list.scrollTop,
              headingTop: heading.getBoundingClientRect().top,
              footerTop: footer.getBoundingClientRect().top,
            };
          });
        const before = await bounds();
        await catalog.hover();
        await page.mouse.wheel(0, 400);
        await page.waitForFunction(
          () =>
            document.querySelector(".shortcut-settings-catalog").scrollTop > 0,
        );
        await catalog.evaluate((node) => {
          node.scrollTop = 0;
        });
        await catalog.focus();
        await page.keyboard.press("PageDown");
        await page.waitForFunction(
          () =>
            document.querySelector(".shortcut-settings-catalog").scrollTop > 0,
        );
        await catalog.evaluate((node) => {
          node.scrollTop = node.scrollHeight;
        });
        const after = await bounds();
        if (before.fits) {
          assert.equal(
            after.mainScroll,
            before.mainScroll,
            "Catalog keyboard/wheel scrolling must retain the outer owner",
          );
          assert.equal(
            after.headingTop,
            before.headingTop,
            "Heading remains stationary",
          );
          assert.equal(
            after.footerTop,
            before.footerTop,
            "Footer remains stationary",
          );
        }
        const finalEntry = catalog.locator("dl > div").last();
        const finalBounds = await finalEntry.boundingBox(),
          listBounds = await catalog.boundingBox();
        assert(
          finalBounds.y >= listBounds.y &&
            finalBounds.y + finalBounds.height <=
              listBounds.y + listBounds.height + 1,
          "Final native shortcut must be reachable inside the catalog",
        );
        await catalog.evaluate((node) => {
          node.scrollTop = 0;
        });
        const lastCapture = catalog
          .locator('[data-settings-action="edit"]')
          .last();
        await catalog.focus();
        const tabStops = await catalog.locator("button:not(:disabled)").count();
        for (let step = 0; step <= tabStops; step++) {
          await page.keyboard.press("Tab");
          if (
            await lastCapture.evaluate(
              (node) => node === document.activeElement,
            )
          )
            break;
        }
        assert(
          await lastCapture.evaluate((node) => node === document.activeElement),
        );
        assert(
          (await bounds()).listScroll > 0,
          "Tab reveals an offscreen editable row through catalog scrolling",
        );
        if (before.fits)
          assert.equal((await bounds()).mainScroll, before.mainScroll);
        const action = page.locator("[data-shortcut-action]");
        const original = await action.getAttribute("aria-keyshortcuts");
        const capture = page.getByRole("button", {
          name: t("shortcut-settings.captureAction").replace(
            "{{name}}",
            t("shortcuts.newSession"),
          ),
          exact: true,
        });
        await capture.click();
        const primary = await page.evaluate(() =>
          /mac/i.test(navigator.platform) ? "Meta" : "Control",
        );
        await page.keyboard.press(`${primary}+K`);
        await page
          .getByText(t("shortcut-settings.invalid"), { exact: true })
          .waitFor();
        assert.equal(
          await page.locator("[data-shortcut-state]").textContent(),
          "0:0",
        );
        const captureNode = await capture.elementHandle();
        await page.setViewportSize({
          width: Math.max(240, width - 16),
          height,
        });
        await page.setViewportSize({ width, height });
        assert(
          await captureNode.evaluate((node) => node.isConnected),
          "Responsive reflow must retain the active capture opener",
        );
        await page.keyboard.press("Escape");
        assert(
          await capture.evaluate((node) => node === document.activeElement),
          "Capture Escape restores its enabled opener",
        );
        await capture.click();
        await page.keyboard.press(`${primary}+Shift+J`);
        await page
          .getByText(t("shortcut-settings.unsaved"), { exact: true })
          .waitFor();
        await page.setViewportSize({
          width: Math.max(240, width - 16),
          height,
        });
        await page.setViewportSize({ width, height });
        assert(
          await captureNode.evaluate((node) => node.isConnected),
          "Responsive reflow must retain the dirty draft owner",
        );
        assert.equal(
          await action.getAttribute("aria-keyshortcuts"),
          original,
          "Draft cannot update ARIA or dispatch",
        );
        await page
          .getByRole("button", {
            name: t("shortcut-settings.save"),
            exact: true,
          })
          .click();
        await page
          .getByText(t("shortcut-settings.saved"), { exact: true })
          .waitFor();
        assert.equal(
          await action.getAttribute("aria-keyshortcuts"),
          `${primary}+Shift+J`,
        );
        await action.focus();
        await page.keyboard.press(`${primary}+Shift+J`);
        assert.equal(
          await page.locator("[data-shortcut-state]").textContent(),
          "1:1",
        );
        await page
          .getByRole("button", {
            name: t("shortcut-settings.restoreAll"),
            exact: true,
          })
          .click();
        assert.equal(
          await action.getAttribute("aria-keyshortcuts"),
          `${primary}+Shift+J`,
        );
        await page
          .getByRole("button", {
            name: t("shortcut-settings.discard"),
            exact: true,
          })
          .click();
        assert.equal(
          await page.locator("[data-shortcut-state]").textContent(),
          "1:1",
        );
        const geometry = await page.evaluate(() => ({
          overflow: document.documentElement.scrollWidth > innerWidth,
          rows: [...document.querySelectorAll(".shortcut-settings-row")].map(
            (node) => ({
              overflow: node.scrollWidth > node.clientWidth,
              buttons: [...node.querySelectorAll("button")].map(
                (button) => button.getBoundingClientRect().height,
              ),
            }),
          ),
        }));
        assert(
          !geometry.overflow &&
            geometry.rows.every(
              (row) =>
                !row.overflow && row.buttons.every((height) => height >= 40),
            ),
          JSON.stringify(geometry),
        );
        assert.equal(geometry.rows.length, 7);
        for (const button of await footer.getByRole("button").all()) {
          await button.scrollIntoViewIfNeeded();
          assert(
            await button.isVisible(),
            "Every footer action remains reachable in fallback layouts",
          );
        }
        cases++;
      }
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      operation: "shortcut-settings-layout",
      cases,
      nativeAcceptance: "not-performed",
      actualZoomAcceptance: "not-performed",
      reflow: "equivalent-200-percent-viewport",
    }),
  );
} finally {
  await browser?.close();
  if (server) await new Promise((done) => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
