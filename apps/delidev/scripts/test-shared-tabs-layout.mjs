// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = {
  revision: execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: app,
    encoding: "utf8",
  }).trim(),
  dirty: Boolean(
    execFileSync("git", ["status", "--porcelain"], {
      cwd: app,
      encoding: "utf8",
    }).trim(),
  ),
};
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright"
);
const directory = await mkdtemp(join(tmpdir(), "delidev-browser-layout-"));
let browser,
  server,
  cases = 0;
const errors = [];
try {
  const build = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      plugins: [pluginReact()],
      source: {
        entry: { index: join(app, "src/shared-tabs-layout.fixture.tsx") },
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
  page.setDefaultTimeout(5000);
  page.on("pageerror", (error) => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const sizes = [
    { width: 1440, height: 900 },
    { width: 960, height: 640 },
    { width: 560, height: 480 },
    { width: 320, height: 480 },
    { width: 640, height: 960, zoom: 2 },
  ];
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark", "custom"])
      for (const size of sizes) {
        await page.setViewportSize({ width: size.width, height: size.height });
        await page.goto(
          `${origin}/?language=${language}&theme=${theme}&zoom=${size.zoom ?? 1}`,
        );
        const dialog = page.getByRole("dialog", { name: "Edit project" });
        await dialog.waitFor();
        const tabs = dialog.getByRole("tab");
        await tabs.nth(0).click();
        const draft = dialog.getByRole("textbox", { name: "Project draft" });
        await draft.fill("Retained project draft");
        await tabs.nth(0).focus();
        await page.keyboard.press("End");
        assert.equal(
          await tabs.nth(0).getAttribute("aria-selected"),
          "true",
          "Project arrows are manual",
        );
        await page.keyboard.press("Space");
        assert.equal(await tabs.nth(3).getAttribute("aria-selected"), "true");
        await tabs.nth(1).click();
        await dialog
          .getByRole("tabpanel")
          .evaluate((node) => (node.scrollTop = 37));
        await tabs.nth(2).click();
        const project = await tabs.evaluateAll((nodes) =>
          nodes.map((node) => {
            const style = getComputedStyle(node),
              strip = node.parentElement,
              box = node.getBoundingClientRect();
            return {
              radius: style.borderRadius,
              background: style.backgroundColor,
              underline: style.borderBottomWidth,
              color: style.borderBottomColor,
              selected: node.getAttribute("aria-selected"),
              height: box.height,
              font: style.fontSize,
              line: style.lineHeight,
              padding: style.paddingInlineStart,
              baseline: getComputedStyle(strip).borderBottomWidth,
              gap: getComputedStyle(strip).gap,
            };
          }),
        );
        assert(
          project.every(
            (row) =>
              row.radius === "0px" &&
              row.background === "rgba(0, 0, 0, 0)" &&
              row.underline === "2px" &&
              row.font === "14px" &&
              row.line === "20px" &&
              row.padding === "16px" &&
              row.baseline === "1px" &&
              row.gap === "0px" &&
              row.height >= 40 * (size.zoom ?? 1) - 1,
          ),
          JSON.stringify(project),
        );
        assert(
          project
            .filter((row) => row.selected === "false")
            .every((row) => row.color === "rgba(0, 0, 0, 0)"),
          JSON.stringify(project),
        );
        const expected = await page.evaluate(() => {
          const probe = document.createElement("span");
          probe.style.color = "var(--selected-border)";
          document.body.append(probe);
          const color = getComputedStyle(probe).color;
          probe.remove();
          return color;
        });
        assert.equal(
          project[2].color,
          expected,
          "Settings task cannot override selected underline",
        );
        assert.equal(
          await dialog
            .getByRole("button", { name: "Save Project" })
            .evaluate((node) => getComputedStyle(node).borderRadius),
          "8px",
        );
        assert.equal(
          await dialog
            .locator('input[aria-label="Project draft"]')
            .evaluate((node) => getComputedStyle(node).borderRadius),
          "8px",
        );
        await tabs.nth(1).click();
        assert.equal(
          await dialog.getByRole("tabpanel").evaluate((node) => node.scrollTop),
          37,
        );
        await tabs.nth(0).click();
        assert.equal(await draft.inputValue(), "Retained project draft");
        await tabs.nth(3).click();
        await dialog.locator("form").evaluate((node) => node.reportValidity());
        await dialog
          .getByRole("textbox", { name: "Required repository" })
          .waitFor();
        assert.equal(await tabs.nth(1).getAttribute("aria-selected"), "true");
        await page.waitForFunction(
          () =>
            document.activeElement?.getAttribute("aria-label") ===
            "Required repository",
        );
        assert(
          await dialog
            .getByRole("textbox", { name: "Required repository" })
            .evaluate((node) => node === document.activeElement),
        );
        await dialog.locator(".settings-task-close").click();
        const workspace = page.locator(".session-tabs");
        await workspace.locator("[role=tab]").last().focus();
        await page.keyboard.press("Home");
        await page.keyboard.press("End");
        const geometry = await workspace.evaluate((strip) => {
          const selected = strip.querySelector(".is-selected"),
            label = selected.querySelector("[role=tab]"),
            close = selected.querySelector(".shared-tab-close"),
            box = label.getBoundingClientRect(),
            target = close.getBoundingClientRect(),
            viewport = strip.getBoundingClientRect();
          return {
            radius: getComputedStyle(selected).borderRadius,
            background: getComputedStyle(selected).backgroundColor,
            underline: getComputedStyle(selected, "::after").borderBottomWidth,
            closeWidth: target.width,
            closeHeight: target.height,
            overlap: box.right > target.left + 1,
            visible:
              box.left >= viewport.left - 1 &&
              target.right <= viewport.right + 1,
            page: document.documentElement.scrollWidth,
            viewport: innerWidth,
          };
        });
        assert.equal(geometry.radius, "0px");
        assert.equal(geometry.background, "rgba(0, 0, 0, 0)");
        assert.equal(geometry.underline, "2px");
        assert.equal(geometry.overlap, false);
        assert.equal(geometry.visible, true);
        assert(
          geometry.closeWidth >= 40 * (size.zoom ?? 1) - 1 &&
            geometry.closeHeight >= 40 * (size.zoom ?? 1) - 1,
        );
        assert(
          geometry.page <= geometry.viewport + 1,
          JSON.stringify(geometry),
        );
        const controls = await page
          .locator(".fixture-secondary .shared-tab-control")
          .evaluateAll((nodes) =>
            nodes.map((node) => ({
              radius: getComputedStyle(node).borderRadius,
              underline: getComputedStyle(node).borderBottomWidth,
              height: node.getBoundingClientRect().height,
              wrap: getComputedStyle(node.parentElement).flexWrap,
            })),
          );
        assert(
          controls.every(
            (row) =>
              row.radius === "0px" &&
              row.underline === "2px" &&
              row.height >= 40 * (size.zoom ?? 1) - 1 &&
              row.wrap === "nowrap",
          ),
        );
        process.stdout.write(
          JSON.stringify({
            operation: "shared-tabs-case",
            language,
            theme,
            ...size,
            geometry,
          }) + "\n",
        );
        cases++;
      }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      operation: "shared-tabs-layout",
      source,
      cases,
      result: "passed",
    }) + "\n",
  );
} finally {
  await browser?.close();
  await new Promise((done) => (server ? server.close(done) : done()));
  await rm(directory, { recursive: true, force: true });
}
