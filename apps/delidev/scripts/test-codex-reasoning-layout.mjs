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
const directory = await mkdtemp(join(tmpdir(), "delidev-transcript-layout-"));
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
        entry: { index: join(app, "src/transcript-role-layout.fixture.tsx") },
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
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const workspace of ["worktree", "general-chat"])
        for (const availableWidth of [820, 360])
          for (const zoom of [1, 2]) {
            await page.setViewportSize({ width: 1920, height: 1400 });
            await page.goto(
              `${origin}/?language=${language}&theme=${theme}&workspace=${workspace}&zoom=${zoom}&reasoning=1`,
            );
            const transcript = page.locator(".transcript");
            await transcript.locator(".codex-reasoning").first().waitFor();
            await transcript.evaluate((node, width) => {
              node.style.width = `${width}px`;
              node.style.maxWidth = "100%";
            }, availableWidth);
            const reasoning = transcript.locator(".codex-reasoning").first();
            const trigger = reasoning.locator("summary");
            assert.equal(await reasoning.evaluate((node) => node.open), false);
            const metrics = await trigger.evaluate((node) => {
              const css = getComputedStyle(node),
                label = node.querySelector(".codex-reasoning-summary"),
                article = node.closest("article"),
                body = node.parentElement.querySelector(
                  ".codex-reasoning-body",
                );
              return {
                height: node.offsetHeight,
                background: css.backgroundColor,
                border: css.borderTopWidth,
                radius: css.borderRadius,
                shadow: css.boxShadow,
                textOverflow: getComputedStyle(label).textOverflow,
                nowrap: getComputedStyle(label).whiteSpace,
                clipped: label.scrollWidth > label.clientWidth,
                articleBorder: getComputedStyle(article).borderTopWidth,
                articlePadding: getComputedStyle(article).paddingTop,
                bodyBorder: getComputedStyle(body).borderLeftWidth,
                indentation: getComputedStyle(body).paddingLeft,
              };
            });
            assert.equal(metrics.height, 36);
            assert.equal(metrics.background, "rgba(0, 0, 0, 0)");
            assert.equal(metrics.border, "0px");
            assert.equal(metrics.radius, "0px");
            assert.equal(metrics.shadow, "none");
            assert.equal(metrics.articleBorder, "0px");
            assert.equal(metrics.articlePadding, "0px");
            assert.equal(metrics.textOverflow, "ellipsis");
            assert.equal(metrics.nowrap, "nowrap");
            assert.equal(metrics.clipped, true);
            assert.equal(metrics.bodyBorder, "1px");
            assert.equal(metrics.indentation, "20px");
            const fullSummary = await trigger.textContent();
            assert(
              fullSummary.includes("한글🙂\n"),
              "Accessible summary preserves exact multiline Unicode",
            );
            await trigger.focus();
            await page.keyboard.press("Enter");
            await page.waitForFunction(
              () => document.querySelector(".codex-reasoning").open,
            );
            const reads = await page.evaluate(
              () => window.__codexReasoningFixture.reads,
            );
            assert.equal(
              await reasoning.locator(".codex-reasoning-text").count(),
              1,
            );
            assert.equal(
              await reasoning
                .locator(".codex-reasoning-body section, .codex-reasoning-body h4, .codex-reasoning-body small, .codex-reasoning-body pre")
                .count(),
              0,
            );
            assert.equal(
              await reasoning.locator(".codex-reasoning-text").textContent(),
              "Streamed content",
            );
            const bodyMetrics = await reasoning
              .locator(".codex-reasoning-text")
              .evaluate((node) => {
                const css = getComputedStyle(node);
                return {
                  fontSize: parseFloat(css.fontSize),
                  lineHeight: parseFloat(css.lineHeight) / parseFloat(css.fontSize),
                  overflow: node.scrollWidth > node.clientWidth,
                };
              });
            assert(bodyMetrics.fontSize >= 13 && bodyMetrics.fontSize <= 14);
            assert(Math.abs(bodyMetrics.lineHeight - 1.45) < 0.02);
            assert.equal(bodyMetrics.overflow, false);
            assert.equal(
              await reasoning.locator("script, a, button").count(),
              0,
            );
            const focus = await trigger.evaluate((node) => ({
              outline: getComputedStyle(node).outlineStyle,
              active: node === document.activeElement,
            }));
            assert.equal(focus.active, true);
            assert.equal(focus.outline, "solid");
            const scroll = await transcript.evaluate((node) => node.scrollTop);
            await page.evaluate(() =>
              window.__codexReasoningFixture.complete(),
            );
            await page.waitForFunction(
              () =>
                document.querySelector(".codex-reasoning summary")
                  .textContent === "Final supplied summary",
            );
            assert.equal(await reasoning.evaluate((node) => node.open), true);
            assert.equal(
              await trigger.evaluate((node) => node === document.activeElement),
              true,
            );
            assert.equal(
              await transcript.evaluate((node) => node.scrollTop),
              scroll,
            );
            await page.waitForFunction(
              () =>
                document.querySelectorAll(".codex-reasoning-text").length ===
                  1 &&
                document.querySelector(".codex-reasoning-text").textContent ===
                  "Final original content",
            );
            const beforeToggleReads = await page.evaluate(
              () => window.__codexReasoningFixture.reads,
            );
            await page.keyboard.press("Space");
            await page.waitForFunction(
              () => !document.querySelector(".codex-reasoning").open,
            );
            await page.keyboard.press("Enter");
            await page.waitForFunction(
              () => document.querySelector(".codex-reasoning").open,
            );
            assert.equal(
              await page.evaluate(() => window.__codexReasoningFixture.reads),
              beforeToggleReads,
              "Disclosure cannot enumerate or reread history",
            );
            assert(
              reads <= beforeToggleReads,
              "Completion uses only its original event reader",
            );
            const empty = transcript.locator(".codex-reasoning").nth(1);
            await empty.locator("summary").click();
            assert.equal(
              await empty.locator("p").textContent(),
              language === "en"
                ? "No reasoning content was supplied."
                : "제공된 생각 과정이 없습니다.",
            );
            assert.equal(
              await transcript.locator("article > header strong").count(),
              0,
            );
            const overflow = await transcript.evaluate((node) => ({
              own: node.scrollWidth > node.clientWidth + 1,
              page: document.documentElement.scrollWidth > innerWidth + 1,
            }));
            assert.deepEqual(overflow, { own: false, page: false });
            cases++;
            process.stdout.write(
              JSON.stringify({
                event: "codex_reasoning_case",
                language,
                theme,
                workspace,
                availableWidth,
                zoom,
              }) + "\n",
            );
          }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      event: "codex_reasoning_layout_passed",
      cases,
      source,
      nativeAcceptance: "not-performed",
    }) + "\n",
  );
} finally {
  await browser?.close();
  await new Promise((done) => (server ? server.close(done) : done()));
  await rm(directory, { recursive: true, force: true });
}
