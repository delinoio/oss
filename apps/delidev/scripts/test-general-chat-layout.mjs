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
const directory = await mkdtemp(join(tmpdir(), "delidev-general-chat-layout-"));
const screenshots = process.env.DELIDEV_GENERAL_CHAT_SCREENSHOT_DIR;
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
        entry: { index: join(app, "src/general-chat-layout.fixture.tsx") },
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
  const frame = () =>
    page.evaluate(
      () =>
        new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        ),
    );
  const chat = page.locator(".new-general-chat-page");
  const geometry = () =>
    chat.evaluate((root) => {
      const header = root
          .querySelector(".new-session-header")
          .getBoundingClientRect(),
        composer = root
          .querySelector(".new-session-composer")
          .getBoundingClientRect();
      return {
        heading: header.top,
        composer: composer.top,
        width: composer.width,
      };
    });
  const normalize = async () => {
    await frame();
    await page.locator("main").evaluate((node) => {
      node.scrollTop = 0;
      window.scrollTo(0, 0);
    });
    await frame();
    await page.locator("main").evaluate((node) => {
      node.scrollTop = 0;
      window.scrollTo(0, 0);
    });
  };
  const anchored = async (before) => {
    await normalize();
    const after = await geometry();
    for (const key of ["heading", "composer", "width"])
      assert(
        Math.abs(before[key] - after[key]) <= 1,
        `${key} stays within 1 CSS pixel`,
      );
  };
  const selectResource = async (root, index) => {
    const control = root.getByRole("combobox").nth(index);
    await control.focus();
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await page.waitForFunction(
      (node) => Boolean(node.dataset.value),
      await control.elementHandle(),
    );
  };
  // Use the real shared creation forms; Compact must not impose a fixed height.
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark"])
      for (const width of [1440, 1200, 1100, 960, 640, 480]) {
        await page.setViewportSize({ width, height: 640 });
        await page.goto(`${origin}/?language=${language}&theme=${theme}`);
        for (const kind of ["General Chat", "New session"]) {
          await page
            .getByRole("button", { name: `Fixture ${kind}`, exact: true })
            .click();
          const root = page.locator(
            kind === "General Chat"
              ? ".new-general-chat-page"
              : ".new-session-page:not(.new-general-chat-page)",
          );
          const input = root.locator("textarea");
          await input.waitFor();
          if (kind === "New session") await selectResource(root, 0);
          const pickerOffset = kind === "New session" ? 1 : 0;
          await selectResource(root, pickerOffset);
          await selectResource(root, pickerOffset + 1);
          await root
            .getByRole("checkbox", {
              name: language === "en" ? "Plan Mode" : "계획 모드",
              exact: true,
            })
            .check();
          await input.fill("Retained multiline draft\nSecond line");
          const originalInput = await input.elementHandle();
          const snapshot = () =>
            originalInput.evaluate((node) => {
              const root = node.closest(".new-session-page");
              return {
                connected: node.isConnected,
                sameInput: root.querySelector("textarea") === node,
                value: node.value,
                rows: node.rows,
                selection: [node.selectionStart, node.selectionEnd],
                focused: document.activeElement === node,
                choices: Array.from(
                  root.querySelectorAll("[role=combobox]"),
                  (control) => control.dataset.value ?? "",
                ),
                mode: root.querySelector(".plan-mode input").checked,
              };
            });
          const measure = () =>
            root.locator(".new-session-composer").evaluate((node) => {
              const input = node.querySelector("textarea");
              const rect = node.getBoundingClientRect();
              return {
                height: rect.height,
                minimum: getComputedStyle(node).minHeight,
                inputHeight: input.getBoundingClientRect().height,
                inputMinimum: getComputedStyle(input).minHeight,
                resize: getComputedStyle(input).resize,
                controlsContained: Array.from(
                  node.querySelectorAll("button, [role=combobox], .plan-mode"),
                  (control) => control.getBoundingClientRect(),
                ).every(
                  (control) =>
                    control.left >= rect.left - 1 &&
                    control.right <= rect.right + 1 &&
                    control.top >= rect.top - 1 &&
                    control.bottom <= rect.bottom + 1,
                ),
              };
            });
          for (const textSize of ["default", "12", "14", "16", "18"]) {
            await page.evaluate((textSize) => {
              document.documentElement.dataset.composerSize = textSize;
              document.documentElement.dataset.composerLayout = "regular";
            }, textSize);
            await input.focus();
            await input.evaluate((node) => node.setSelectionRange(3, 14));
            await frame();
            const retained = await snapshot();
            assert.equal(retained.rows, 5);
            const regular = await measure();
            assert.equal(regular.minimum, "210px");
            assert(regular.height >= 210);
            await page.evaluate(() => {
              document.documentElement.dataset.composerLayout = "compact";
            });
            await frame();
            const compact = await measure();
            assert.equal(compact.minimum, "0px");
            assert.equal(compact.inputMinimum, "64px");
            assert(compact.inputHeight >= 64);
            assert.equal(compact.resize, "vertical");
            assert(compact.controlsContained, "Wrapping controls fit the card");
            if (width >= 1200 && ["12", "14"].includes(textSize))
              assert(
                compact.height < 210,
                `Compact uses natural height: ${JSON.stringify({ language, theme, width, kind, textSize, compact })}`,
              );
            assert.deepEqual(await snapshot(), retained);
            // Inline height models a native manual resize without changing rows.
            await input.evaluate((node, height) => {
              node.style.height = `${height + 32}px`;
            }, compact.inputHeight);
            await frame();
            assert((await measure()).inputHeight >= compact.inputHeight + 31);
            await input.evaluate((node) => {
              node.style.height = "";
            });
            await page.evaluate(() => {
              document.documentElement.dataset.composerLayout = "regular";
            });
            await frame();
            assert(
              Math.abs((await measure()).height - regular.height) <= 1,
              "Regular restores its original geometry",
            );
            assert.deepEqual(await snapshot(), retained);
            assert.equal(
              await page.locator("output").getAttribute("data-fixture-creates"),
              "0",
              "Appearance changes never submit",
            );
            assert(
              await page.evaluate(
                () => document.documentElement.scrollWidth <= innerWidth + 1,
              ),
              "Narrow and effective 200% reflow have no horizontal overflow",
            );
            cases++;
          }
          await originalInput.dispose();
        }
      }
  await page.evaluate(() => {
    delete document.documentElement.dataset.composerSize;
    delete document.documentElement.dataset.composerLayout;
  });
  for (const language of ["en", "ko"])
    for (const theme of ["light", "dark", "system"])
      for (const size of [
        { width: 1600, height: 1000 },
        { width: 960, height: 640 },
        { width: 480, height: 320 },
      ]) {
        process.stdout.write(
          JSON.stringify({
            operation: "general-chat-case",
            language,
            theme,
            ...size,
          }) + "\n",
        );
        await page.emulateMedia({ colorScheme: "dark" });
        await page.setViewportSize(size);
        await page.goto(`${origin}/?language=${language}&theme=${theme}`);
        await chat.locator("textarea").waitFor();
        await frame();
        assert.equal(await chat.locator(".new-session-options").count(), 0);
        await chat.locator("textarea").fill("Retained synthetic draft");
        await selectResource(chat, 0);
        await selectResource(chat, 1);
        await chat
          .getByRole("checkbox", {
            name: language === "en" ? "Plan Mode" : "계획 모드",
            exact: true,
          })
          .check();
        await normalize();
        const before = await geometry();
        if (screenshots && theme !== "system" && size.width !== 480) {
          await mkdir(screenshots, { recursive: true });
          await page.screenshot({
            path: join(
              screenshots,
              `${language}-${theme}-${size.width}-closed.png`,
            ),
          });
        }
        const toggle = chat.locator(".new-session-options-toggle");
        await toggle.focus();
        await page.keyboard.press("Enter");
        assert.equal(await toggle.getAttribute("aria-expanded"), "true");
        assert(
          await toggle.evaluate((node) => node === document.activeElement),
          "Options retains keyboard focus",
        );
        await anchored(before);
        assert.equal(await chat.locator(".new-session-options h3").count(), 1);
        assert.equal(
          await chat.locator(".new-session-options details").count(),
          0,
          "Budget fields have no second disclosure",
        );
        const enabled = chat.locator(
          ".new-session-options input[type=checkbox]",
        );
        await enabled.check();
        const fields = chat.locator(
          ".new-session-options input:not([type=checkbox])",
        );
        await fields.nth(0).fill("USD");
        await fields.nth(1).fill("2");
        await anchored(before);
        // A user-resized composer changes the primary range while Options remains open.
        const originalHeight = await chat
          .locator("textarea")
          .evaluate((node) => node.getBoundingClientRect().height);
        await chat.locator("textarea").evaluate((node, height) => {
          node.style.height = `${height + 32}px`;
        }, originalHeight);
        await normalize();
        const resized = await geometry();
        const newHeight = await chat
          .locator("textarea")
          .evaluate((node) => node.getBoundingClientRect().height);
        assert(
          Math.abs(
            resized.heading -
              before.heading -
              (size.height > 760 ? -(newHeight - originalHeight) / 2 : 0),
          ) <= 1,
          `Primary ResizeObserver recomputes centering with minimum padding: ${JSON.stringify({ before, resized, originalHeight, newHeight })}`,
        );
        await chat.locator("textarea").evaluate((node) => {
          node.style.height = "";
        });
        await anchored(before);
        await page.setViewportSize({ ...size, height: size.height + 40 });
        await normalize();
        const taller = await geometry();
        assert(
          Math.abs(
            taller.heading - before.heading - (size.height > 760 ? 20 : 0),
          ) <= 1,
          `Visible viewport height recomputes primary placement: ${JSON.stringify({ before, taller, size, scroll: await page.locator("main").evaluate((node) => node.scrollTop) })}`,
        );
        await page.setViewportSize(size);
        await anchored(before);
        await fields.nth(1).scrollIntoViewIfNeeded();
        assert(await fields.nth(1).isVisible(), "Expanded budget is reachable");
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth + 1,
          ),
          "No horizontal page overflow",
        );
        assert(
          await page
            .locator("main")
            .evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
          "No horizontal main overflow",
        );
        await toggle.click();
        await anchored(before);
        await toggle.click();
        await anchored(before);
        assert(await enabled.isChecked());
        assert.equal(await fields.nth(1).inputValue(), "2");
        for (const destination of ["Fixture Settings", "Fixture reconnect"]) {
          await page
            .getByRole("button", { name: destination, exact: true })
            .click();
          if (destination === "Fixture Settings")
            await page
              .getByRole("button", {
                name: "Fixture General Chat",
                exact: true,
              })
              .click();
          await chat.locator("textarea").waitFor();
          await frame();
          assert.equal(
            await chat.locator("textarea").inputValue(),
            "Retained synthetic draft",
          );
          assert(
            await chat
              .getByRole("checkbox", {
                name: language === "en" ? "Plan Mode" : "계획 모드",
                exact: true,
              })
              .isChecked(),
          );
          assert(await enabled.isChecked());
          assert.equal(await fields.nth(0).inputValue(), "USD");
          assert.equal(await fields.nth(1).inputValue(), "2");
          await anchored(before);
        }
        if (screenshots && theme !== "system" && size.width !== 480) {
          await mkdir(screenshots, { recursive: true });
          await normalize();
          await page.screenshot({
            path: join(screenshots, `${language}-${theme}-${size.width}.png`),
          });
        }
        // Invalid budgets cannot reach the create RPC. A valid budget retains its exact draft.
        await fields.nth(0).fill("usd");
        await chat.locator(".new-session-submit").click();
        assert.equal(
          await page.locator("output").getAttribute("data-fixture-creates"),
          "0",
        );
        await fields.nth(0).fill("USD");
        await fields.nth(1).fill("-1");
        await chat.locator(".new-session-submit").click();
        assert.equal(
          await page.locator("output").getAttribute("data-fixture-creates"),
          "0",
        );
        await fields.nth(1).fill("2");
        await chat.locator(".new-session-submit").click();
        await page.waitForFunction(
          () => document.querySelector("output").dataset.fixtureCreates === "1",
        );
        assert.deepEqual(
          JSON.parse(await page.locator("output").textContent())
            .estimated_cost_budget,
          { currency: "USD", threshold: "2" },
        );
        // A fresh identity remounts both draft controllers; navigation alone above does not.
        await page
          .getByRole("button", { name: "Fixture identity", exact: true })
          .click();
        await frame();
        assert.equal(await chat.locator("textarea").inputValue(), "");
        assert.equal(
          await chat
            .getByRole("checkbox", {
              name: language === "en" ? "Plan Mode" : "계획 모드",
              exact: true,
            })
            .isChecked(),
          false,
        );
        assert.equal(await toggle.getAttribute("aria-expanded"), "false");
        await chat.locator("textarea").fill("No budget draft");
        await selectResource(chat, 0);
        await selectResource(chat, 1);
        await chat.locator(".new-session-submit").click();
        await page.waitForFunction(
          () => document.querySelector("output").dataset.fixtureCreates === "2",
        );
        assert.equal(
          "estimated_cost_budget" in
            JSON.parse(await page.locator("output").textContent()),
          false,
        );
        await page
          .getByRole("button", { name: "Fixture New session", exact: true })
          .click();
        const ordinary = page.locator(
          ".new-session-page:not(.new-general-chat-page)",
        );
        await ordinary.locator("textarea").waitFor();
        await ordinary.locator(".new-session-options-toggle").click();
        assert.equal(
          await ordinary
            .locator(".new-session-options details > summary")
            .count(),
          1,
          "Ordinary creation retains its budget disclosure",
        );
        await selectResource(ordinary, 0);
        assert.equal(
          await ordinary.locator(".workspace-mode input[type=radio]").count(),
          2,
          "Project workspace controls remain",
        );
        cases++;
      }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      operation: "general-chat-layout",
      ...source,
      cases,
      screenshots,
      effectiveZoom: "half-viewport reflow",
      nativeAcceptance: "not-performed",
    }) + "\n",
  );
} finally {
  await browser?.close();
  if (server) await new Promise((done) => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
