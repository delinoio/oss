// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { resolve, dirname, join, extname, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app = resolve(dirname(fileURLToPath(import.meta.url)), ".."),
  temp = await mkdtemp(join(tmpdir(), "delidev-mobile-layout-"));
let browser,
  server,
  cases = 0;
try {
  const build = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      plugins: [pluginReact()],
      source: { entry: { index: join(app, "src/layout.fixture.tsx") } },
      html: { template: join(app, "index.html") },
      output: {
        distPath: { root: temp },
        assetPrefix: "/",
        cleanDistPath: true,
      },
    },
  });
  await build.build();
  server = createServer(async (req, res) => {
    try {
      const path = resolve(
        temp,
        "." +
          new URL(req.url, "http://127.0.0.1").pathname.replace(
            /^\/$/,
            "/index.html",
          ),
      );
      if (!path.startsWith(temp + sep)) throw new Error();
      res.setHeader(
        "Content-Type",
        {
          ".html": "text/html",
          ".js": "application/javascript",
          ".css": "text/css",
        }[extname(path)] ?? "application/octet-stream",
      );
      res.end(await readFile(path));
    } catch {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const module = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
  const { chromium } = await import(
    module ? pathToFileURL(resolve(module)).href : "playwright"
  );
  browser = await chromium.launch({
    headless: true,
    ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL
      ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL }
      : {}),
  });
  const page = await browser.newPage(),
    errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const notifications of ["legacy", "granular"])
      for (const [width, height] of [
        [360, 780],
        [390, 844],
        [430, 932],
        [768, 1024],
        [180, 390],
      ]) {
        await page.setViewportSize({ width, height });
        await page.goto(
          `http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&notifications=${notifications}`,
        );
        await page.locator(".connection button").waitFor();
        await page.waitForFunction(
          () =>
            document.documentElement.lang ===
            new URLSearchParams(location.search).get("language"),
        );
        assert.equal(await page.locator("nav button").count(), 3);
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth + 1,
          ),
          "initial horizontal reflow",
        );
        await page.locator("nav button").nth(2).click();
        await page.locator("select").first().waitFor();
        const notificationGroup = page.locator("fieldset").filter({ has: page.locator("legend") });
        await notificationGroup.locator("input[type=checkbox]").first().waitFor();
        await page.waitForFunction(() => [...document.querySelectorAll("fieldset input[type=checkbox]")].every(node => !node.matches(":disabled")));
        assert.equal(await notificationGroup.locator("input[type=checkbox]").count(), notifications === "granular" ? 12 : 2);
        assert.equal(await notificationGroup.locator("h3").count(), notifications === "granular" ? 4 : 0);
        const notificationControls = await notificationGroup.locator("label.check").evaluateAll(nodes => nodes.map(node => ({ height: node.getBoundingClientRect().height, width: node.getBoundingClientRect().width, scroll: node.scrollWidth })));
        assert(notificationControls.every(control => control.height >= 47 && control.scroll <= control.width + 1), "notification targets and text reflow");
        const lastPreference = notificationGroup.locator("input[type=checkbox]").last();
        await lastPreference.focus();
        assert(await lastPreference.evaluate(node => document.activeElement === node), "last preference keyboard focus");
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth + 1,
          ),
          "settings horizontal reflow",
        );
        await page.locator("nav button").first().click();
        await page.locator("section > header button").first().click();
        await page.locator("dialog[open]").waitFor();
        const dialog = page.locator("dialog"),
          textarea = dialog.locator("textarea");
        await textarea.fill("Retained keyboard draft");
        await page.setViewportSize({
          width,
          height: Math.max(260, Math.floor(height * 0.6)),
        });
        await textarea.scrollIntoViewIfNeeded();
        assert.equal(await textarea.inputValue(), "Retained keyboard draft");
        assert(
          await dialog.evaluate(
            (node) => node.scrollWidth <= node.clientWidth + 1,
          ),
          "dialog reflow",
        );
        const controls = await dialog
          .locator("button,input,select,summary")
          .evaluateAll((nodes) =>
            nodes.map((node) => node.getBoundingClientRect().height),
          );
        assert(
          controls.every((height) => height >= 47),
          "48px controls",
        );
        await page.keyboard.press("Escape");
        await page.locator("dialog").waitFor({ state: "detached" });
        assert(
          await page.evaluate(
            () => document.activeElement?.tagName === "BUTTON",
          ),
          "focus returns to opener",
        );
        assert.equal(
          await page.evaluate(() => document.documentElement.dataset.mutations),
          undefined,
          "layout never creates a session",
        );
        cases++;
      }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      operation: "mobile-layout",
      outcome: "passed",
      cases,
      screenshots: "not-performed",
    }) + "\n",
  );
} finally {
  await browser?.close();
  if (server) await new Promise((r) => server.close(r));
  await rm(temp, { recursive: true, force: true });
}
