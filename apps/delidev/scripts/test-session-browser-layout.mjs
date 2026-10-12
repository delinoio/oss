// SPDX-License-Identifier: Apache-2.0
// Synthetic browser checks do not establish packaged CEF/native acceptance.
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
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
const screenshots = process.env.DELIDEV_BROWSER_SCREENSHOT_DIR;
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
        entry: { index: join(app, "src/session-browser-layout.fixture.tsx") },
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
  page.on("pageerror", (error) => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const sizes = [
    { width: 1904, height: 1000, conversation: 1440 },
    { width: 1424, height: 700, conversation: 960 },
    { width: 1423, height: 700, conversation: 959 },
    { width: 560, height: 640, conversation: 480 },
    { width: 1904, height: 480, conversation: 1440 },
    { width: 1120, height: 960, conversation: 480, zoom: 2 },
  ];
  const labels = (language) =>
    language === "en"
      ? {
          newPage: "New browser page",
          address: "Address",
          open: "Open page",
          cancel: "Cancel",
          tool: "Open tool",
          browser: "Browser",
          conversation: "Conversation",
          information: "Browser information",
          closeInfo: "Close Browser information",
        }
      : {
          newPage: "새 브라우저 페이지",
          address: "주소",
          open: "페이지 열기",
          cancel: "취소",
          tool: "도구 열기",
          browser: "브라우저",
          conversation: "대화",
          information: "브라우저 정보",
          closeInfo: "브라우저 정보 닫기",
        };
  const evidence = () =>
    page.evaluate(() => ({
      registrations: browserFixture.registrations.length,
      creates: browserFixture.native.filter(
        (call) =>
          call.operation === "browser_pages" && call.action === "create",
      ).length,
      opens: browserFixture.native.filter(
        (call) => call.operation === "open_browser",
      ).length,
      hides: browserFixture.native.filter((call) => call.action === "hide")
        .length,
      closes: browserFixture.native.filter(
        (call) => call.operation === "browser_pages" && call.action === "close",
      ).length,
    }));
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark", "custom"])
      for (const size of sizes) {
        const l = labels(language);
        await page.setViewportSize(size);
        await page.goto(
          `${origin}/?language=${language}&theme=${theme}&zoom=${size.zoom ?? 1}`,
        );
        await page.getByRole("tab").nth(2).waitFor();
        assert.equal(await page.getByRole("tablist").count(), 1);
        assert.equal(
          await page.getByRole("tab", { name: l.browser, exact: true }).count(),
          0,
        );
        assert.equal(
          await page
            .locator(".browser-tab-bar,.browser-pages,.browser-tabs")
            .count(),
          0,
        );
        assert.equal((await evidence()).creates, 0);
        assert.equal((await evidence()).registrations, 0);
        const composer = page.getByRole("textbox", {
          name: language === "en" ? "Message" : "메시지",
        });
        await composer.fill("Synthetic retained draft");
        await composer.evaluate((node) => {
          window.originalComposer = node;
        });
        const plus = page.getByRole("button", { name: l.newPage, exact: true });
        assert.equal(await plus.getAttribute("role"), null);
        assert.equal(await plus.getAttribute("aria-keyshortcuts"), null);
        await plus.click();
        let dialog = page.getByRole("dialog", { name: l.newPage, exact: true });
        await dialog.waitFor();
        assert(
          await dialog
            .getByRole("textbox", { name: l.address })
            .evaluate((node) => node === document.activeElement),
        );
        assert(
          await dialog
            .getByRole("button", { name: l.open, exact: true })
            .isDisabled(),
        );
        const box = await dialog.boundingBox();
        assert(box.width <= 480 * (size.zoom ?? 1) + 1);
        assert(box.x >= 0 && box.x + box.width <= size.width + 1);
        const controls = await dialog
          .locator("button,input")
          .evaluateAll((nodes) =>
            nodes.map((node) => node.getBoundingClientRect().height),
          );
        assert(controls.every((height) => height >= 40 * (size.zoom ?? 1) - 1));
        await page.keyboard.press("Shift+Tab");
        assert(
          await dialog.evaluate((node) =>
            node.contains(document.activeElement),
          ),
        );
        await page.keyboard.press("Escape");
        await dialog.waitFor({ state: "detached" });
        assert(await plus.evaluate((node) => node === document.activeElement));
        assert.equal((await evidence()).creates, 0);
        await page.getByRole("button", { name: l.tool, exact: true }).click();
        await page
          .getByRole("menuitem", { name: l.browser, exact: true })
          .click();
        dialog = page.getByRole("dialog", { name: l.newPage, exact: true });
        await dialog.waitFor();
        await dialog
          .getByRole("textbox", { name: l.address })
          .fill("https://explicit.example/new");
        await dialog.getByRole("button", { name: l.open, exact: true }).click();
        await dialog.waitFor({ state: "detached" });
        const newTab = page.getByRole("tab", {
          name: "https://explicit.example/new",
          selected: true,
        });
        await newTab.waitFor();
        assert.equal((await evidence()).creates, 1);
        assert.equal((await evidence()).registrations, 1);
        await page
          .getByRole("button", { name: l.information, exact: true })
          .click();
        await page.waitForFunction(() =>
          browserFixture.native.some((call) => call.action === "hide"),
        );
        await page
          .getByRole("button", { name: l.closeInfo, exact: true })
          .click();
        const geometry = await page.evaluate(() => {
          const panel = document
            .querySelector(".session-browser:not([hidden])")
            .getBoundingClientRect();
          const info = document
            .querySelector(".session-information")
            .getBoundingClientRect();
          const intersect =
            panel.left < info.right - 1 &&
            panel.right > info.left + 1 &&
            panel.top < info.bottom - 1 &&
            panel.bottom > info.top + 1;
          const bar = document.querySelector(".session-tabs");
          return {
            intersect,
            scroll: bar.scrollWidth >= bar.clientWidth,
            viewport: document
              .querySelector(".session-browser:not([hidden]) .browser-viewport")
              .getBoundingClientRect().height,
          };
        });
        assert.equal(geometry.intersect, false);
        assert(geometry.scroll && geometry.viewport > 0);
        await page
          .getByRole("button", {
            name:
              language === "en"
                ? "Close explicit.example/new tab"
                : "explicit.example/new 탭 닫기",
            exact: true,
          })
          .click();
        await newTab.waitFor({ state: "detached" });
        assert.equal((await evidence()).closes, 1);
        await page
          .getByRole("tab", { name: l.conversation, exact: true })
          .click();
        assert(await composer.evaluate((node) => node === originalComposer));
        assert.equal(await composer.inputValue(), "Synthetic retained draft");
        cases++;
      }
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const state of [
        "host=unsupported",
        "capabilities=loading",
        "capabilities=unsupported",
        "capabilities=failure",
        "registration=uncertain",
        "native=failure",
        "removal=pending",
        "tabs=16",
      ]) {
        const l = labels(language);
        await page.setViewportSize({ width: 1424, height: 640 });
        await page.goto(
          `${origin}/?language=${language}&theme=${theme}&${state}`,
        );
        await page
          .getByRole("button", { name: l.newPage, exact: true })
          .click();
        const dialog = page.getByRole("dialog", {
          name: l.newPage,
          exact: true,
        });
        await dialog.waitFor();
        if (state === "host=unsupported") {
          assert.equal((await evidence()).registrations, 0);
          cases++;
          continue;
        }
        await dialog
          .getByRole("textbox", { name: l.address })
          .fill("https://explicit.example/new");
        const open = dialog.getByRole("button", { name: l.open, exact: true });
        if (
          state.startsWith("capabilities=") ||
          state === "removal=pending" ||
          state === "tabs=16"
        ) {
          await page.waitForTimeout(100);
          assert(await open.isDisabled());
          assert.equal((await evidence()).registrations, 0);
          assert.equal((await evidence()).creates, 0);
          cases++;
          continue;
        }
        await open.click();
        if (state === "registration=uncertain") {
          const retry = dialog.getByRole("button", {
            name:
              language === "en"
                ? "Retry the same registration"
                : "같은 등록 다시 시도",
            exact: true,
          });
          await retry.waitFor();
          assert.equal((await evidence()).creates, 0);
          await retry.click();
          await dialog.waitFor({ state: "detached" });
          assert(
            await page.evaluate(
              () =>
                JSON.stringify(browserFixture.registrations[0]) ===
                JSON.stringify(browserFixture.registrations[1]),
            ),
          );
          assert.equal((await evidence()).creates, 1);
        } else {
          await dialog.waitFor({ state: "detached" });
          await page
            .locator('.session-browser:not([hidden]) [role="alert"]')
            .waitFor();
          assert.equal((await evidence()).creates, 1);
        }
        cases++;
      }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      operation: "browser-workspace-layout",
      source,
      cases,
      nativeAcceptance: false,
    }) + "\n",
  );
} finally {
  await browser?.close();
  if (server) await new Promise((done) => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
