// SPDX-License-Identifier: Apache-2.0
// Automated synthetic browser evidence, without app/native launch or screenshots.
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
const directory = await mkdtemp(join(tmpdir(), "delidev-reset-credit-check-"));
let browser, server;
try {
  const build = await createRsbuild({
    cwd: app,
    rsbuildConfig: {
      plugins: [pluginReact()],
      source: {
        entry: { index: join(app, "src/reset-credit-layout.fixture.tsx") },
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
      const pathname = new URL(request.url, "http://127.0.0.1").pathname,
        file = resolve(
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
  const page = await browser.newPage(),
    errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  let checks = 0;
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const width of [640, 320])
        for (const zoom of [1, 2])
          for (const state of ["management", "consent", "uncertain"]) {
            await page.setViewportSize({
              width: width * zoom,
              height: 800 * zoom,
            });
            await page.goto(
              `http://127.0.0.1:${server.address().port}/?language=${language}&theme=${theme}&zoom=${zoom}&state=${state}`,
            );
            const use = page.getByRole("button", {
              name: language === "en" ? "Use" : "사용하기",
              exact: true,
            });
            await use.waitFor();
            if (state === "uncertain") {
              const results = page.getByRole("region", {
                name:
                  language === "en" ? "Results and recovery" : "사용 결과 및 복구",
              });
              await results.waitFor();
              await results
                .getByRole("heading", {
                  name:
                    language === "en"
                      ? "The consumption result could not be confirmed"
                      : "사용 결과를 확인하지 못했습니다",
                })
                .waitFor();
              const reconcile = results.getByRole("button", {
                name:
                  language === "en"
                    ? "Check original operation result"
                    : "기존 작업 결과 확인",
              });
              assert(await reconcile.isDisabled());
              assert((await reconcile.boundingBox()).height >= 40 * zoom);
              assert(await page.locator(".reset-credit-rows").isHidden());
              assert(
                await results.evaluate(
                  (node) => node.scrollWidth <= node.clientWidth,
                ),
              );
            } else if (state === "consent") {
              const automatic = page.getByRole("checkbox", {
                name: language === "en" ? "Automatic use" : "자동 사용",
                exact: true,
              });
              await page.waitForFunction(
                () =>
                  !document.querySelector(".reset-credit-automatic input").disabled,
              );
              await automatic.click();
              const dialog = page.getByRole("dialog");
              await dialog.waitFor();
              assert.equal(await dialog.locator(".reset-credit-rules li").count(), 3);
              assert(
                await dialog
                  .locator(".reset-credit-consent")
                  .evaluate((node) => node.scrollWidth <= node.clientWidth),
              );
              const enable = dialog.getByRole("button", {
                name: language === "en" ? "Enable automatic use" : "자동 사용 켜기",
                exact: true,
              });
              assert((await enable.boundingBox()).height >= 40 * zoom);
              assert(
                await dialog
                  .locator(".settings-task-footer")
                  .getByRole("button", {
                    name: language === "en" ? "Cancel" : "취소",
                    exact: true,
                  })
                  .isVisible(),
              );
              await page.keyboard.press("Escape");
              await dialog.waitFor({ state: "hidden" });
            } else {
              await page.waitForFunction(
                () =>
                  !document.querySelector(".reset-credit-summary button").disabled,
              );
              await use.focus();
              await page.keyboard.press("Enter");
              await page.waitForFunction(
                () => document.activeElement?.tagName === "H4",
              );
              assert(
                await page
                  .getByRole("heading", {
                    name: language === "en" ? "Credit details" : "리셋권 상세",
                    exact: true,
                  })
                  .evaluate((node) => node === document.activeElement),
              );
              const rows = page.locator(".reset-credit-row");
              assert.equal(await rows.count(), 3);
              assert.equal(await rows.locator("button").count(), 2);
              assert(
                (await page.locator(".reset-credit-summary").textContent()).includes(
                  language === "en" ? "3 available reset credits" : "사용 가능 3개",
                ),
              );
              assert.deepEqual(
                await rows.locator("strong").allTextContents(),
                language === "en"
                  ? ["Reset credit 1", "Reset credit 2", "Reset credit 3"]
                  : ["리셋권 1", "리셋권 2", "리셋권 3"],
              );
              for (const row of await rows.all()) {
                assert(
                  await row.evaluate(
                    (node) => node.scrollWidth <= node.clientWidth,
                  ),
                );
                if (await row.locator("button").count())
                  assert(
                  (await row.locator("button").boundingBox()).height >= 40 * zoom,
                );
                assert.equal(
                  await row.locator("time").getAttribute("datetime"),
                  "2099-01-01T00:00:00Z",
                );
              }
              const select = page.getByRole("button", {
                name: language === "en" ? "Select Reset credit 2" : "리셋권 2 선택",
                exact: true,
              });
              await select.focus();
              await page.keyboard.press("Enter");
              await page.waitForFunction(
                () =>
                  document.activeElement ===
                  document.querySelector(".reset-credit-confirmation h4"),
              );
              assert(
                await page
                  .locator(".reset-credit-confirmation h4")
                  .evaluate((node) => node === document.activeElement),
              );
              assert(
                (
                  await page.locator(".reset-credit-confirmation").textContent()
                ).includes(
                  language === "en" ? "Reset credit 2" : "리셋권 2",
                ),
              );
              assert(
                (
                  await page.locator(".reset-credit-confirmation").textContent()
                ).includes("long_original_identity_".repeat(4) + "b"),
              );
              assert(
                await page
                  .locator(".reset-credit-confirmation")
                  .evaluate((node) => node.scrollWidth <= node.clientWidth),
              );
              await page
                .getByRole("button", {
                  name: language === "en" ? "Cancel" : "취소",
                  exact: true,
                })
                .click();
              assert(
                await select.evaluate((node) => node === document.activeElement),
              );
            }
            assert(
              await page.evaluate(
                () => document.documentElement.scrollWidth <= innerWidth,
              ),
            );
            checks++;
          }
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      fixture: "reset-credit-presentation",
      checks,
      screenshots: false,
      nativeAcceptance: false,
    }),
  );
} finally {
  await browser?.close();
  if (server) await new Promise((done) => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
