// SPDX-License-Identifier: Apache-2.0
// Actual synthetic component DOM plus product styles; no app/native build.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, writeFile, rm } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(import.meta.url);
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright"
);
const directory = await mkdtemp(join(tmpdir(), "delidev-shared-tabs-"));
const revision = execFileSync("git", ["rev-parse", "HEAD"], {
  cwd: app,
  encoding: "utf8",
}).trim();
const dirty = Boolean(
  execFileSync("git", ["status", "--porcelain"], {
    cwd: app,
    encoding: "utf8",
  }).trim(),
);
let browser;
let cases = 0;
try {
  // Source aliases avoid building the API client solely for these fixtures.
  const config = join(directory, "config.ts");
  await writeFile(
    config,
    `import { mergeConfig } from ${JSON.stringify(require.resolve("vitest/config"))};
import config from ${JSON.stringify(join(app, "vitest.config.ts"))};
export default mergeConfig(config, {
  cacheDir: ${JSON.stringify(join(directory, "cache"))},
  resolve: { dedupe: ["react", "react-dom"], alias: {
    "@delinoio/delidev-api-client": ${JSON.stringify(resolve(app, "../../packages/delidev-api-client/src/index.ts"))}
  } },
});`,
  );
  execFileSync(
    process.execPath,
    [
      join(dirname(require.resolve("vitest/package.json")), "vitest.mjs"),
      "run",
      "--config",
      config,
      "--maxWorkers=1",
      "--reporter=dot",
      "src/settings-task.test.tsx",
      "src/usage.test.tsx",
      "src/backups.test.tsx",
      "src/session-tab-bar.test.tsx",
      "src/session-terminals.test.tsx",
      "--testNamePattern",
      "constrains the saved Project|defaults to Overview|manually activates stable|keeps complete names|attaches to the accepted creation beyond",
    ],
    {
      cwd: app,
      stdio: "inherit",
      env: { ...process.env, DELIDEV_SHARED_TAB_FIXTURES: directory },
    },
  );
  const css = (
    await Promise.all(
      [
        "themes.css",
        "styles.css",
        "session.css",
        "terminal-dock.css",
        "project-edit-tabs.css",
        "usage.css",
        "backups.css",
        "shared-tabs.css",
        "settings-task.css",
      ].map((name) => readFile(join(app, "src", name), "utf8")),
    )
  ).join("\n");
  const typescript = require("typescript");
  const revealSource = typescript
    .transpileModule(await readFile(join(app, "src/shared-tabs.ts"), "utf8"), {
      compilerOptions: {
        target: typescript.ScriptTarget.ES2022,
        module: typescript.ModuleKind.ESNext,
      },
    })
    .outputText.replace(/^import .*;\n/gm, "")
    .replace(/^export /gm, "");
  browser = await chromium.launch({
    headless: true,
    ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL
      ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL }
      : {}),
  });
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const sizes = [
    { width: 1440, height: 900 },
    { width: 960, height: 640 },
    { width: 560, height: 480 },
    { width: 320, height: 480 },
    { width: 640, height: 960, zoom: 2 },
  ];
  for (const surface of [
    "project",
    "usage",
    "backups",
    "workspace",
    "terminal",
  ])
    for (const language of ["en", "ko"])
      for (const theme of ["light", "dark", "custom"])
        for (const size of sizes) {
          await page.setViewportSize({
            width: Math.round(size.width / (size.zoom ?? 1)),
            height: Math.round(size.height / (size.zoom ?? 1)),
          });
          const html = await readFile(
            join(directory, `${surface}-${language}.html`),
            "utf8",
          );
          await page.setContent(`<!doctype html><html lang="${language}" data-theme="${theme}"><head>
            <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">
            <style>${css}</style><style>.session-workspace{height:auto}
            :root[data-theme="custom"]{--selected-text:#693b9c;--selected-border:#955dc4;--focus:#3d8073;--border:#8b8295;--muted:#655b6e}
            </style></head><body>${html}</body></html>`);
          await page.evaluate(
            revealSource + "\nwindow.revealSharedTab = revealSharedTab;",
          );
          const reveal = await page.evaluate(() => {
            const strip = document.querySelector(".shared-tab-strip");
            const selected = strip.querySelector('[aria-selected="true"]');
            const before = [
              document.documentElement.scrollLeft,
              document.documentElement.scrollTop,
            ];
            window.revealSharedTab(selected);
            const item = selected
              .closest(".shared-tab-item")
              .getBoundingClientRect();
            const bounds = strip.getBoundingClientRect();
            return {
              reachable:
                item.left >= bounds.left - 1 && item.right <= bounds.right + 1,
              before,
              after: [
                document.documentElement.scrollLeft,
                document.documentElement.scrollTop,
              ],
            };
          });
          assert.equal(
            reveal.reachable,
            true,
            `${surface} reveals its selected item`,
          );
          assert.deepEqual(reveal.before, reveal.after);
          const matrix = await page.evaluate(() => {
            const strip = document.querySelector(".shared-tab-strip");
            if (!strip) throw new Error("Missing actual component strip");
            const zoom = Number(getComputedStyle(document.body).zoom) || 1;
            const control = [...strip.querySelectorAll(".shared-tab-control")];
            const items = [...strip.querySelectorAll(".shared-tab-item")];
            return {
              wrap: getComputedStyle(strip).flexWrap,
              gap: getComputedStyle(strip).gap,
              overflow: getComputedStyle(strip).overflowX,
              controls: control.map((node) => {
                const style = getComputedStyle(node),
                  rect = node.getBoundingClientRect();
                return {
                  height: rect.height / zoom,
                  width: rect.width / zoom,
                  radius: style.borderRadius,
                  background: style.backgroundColor,
                  font: style.fontSize,
                  line: style.lineHeight,
                  padding: style.paddingLeft,
                  close: node.classList.contains("shared-tab-close"),
                };
              }),
              overlap: items.some((item) => {
                const label = item.querySelector(".shared-tab-label"),
                  close = item.querySelector(".shared-tab-close");
                return (
                  label &&
                  close &&
                  label.getBoundingClientRect().right >
                    close.getBoundingClientRect().left + 1
                );
              }),
              selected: items
                .filter((item) =>
                  item.matches('[aria-selected="true"], .is-selected'),
                )
                .map((item) => {
                  const style = getComputedStyle(
                    item,
                    item.classList.contains("shared-tab-item--closable")
                      ? "::after"
                      : null,
                  );
                  return {
                    underline: style.borderBottomWidth,
                    color: style.borderBottomColor,
                  };
                }),
              documentOverflow:
                document.documentElement.scrollWidth -
                document.documentElement.clientWidth,
            };
          });
          assert.equal(matrix.wrap, "nowrap");
          assert.equal(matrix.gap, "0px");
          assert.equal(matrix.overflow, "auto");
          assert.equal(matrix.overlap, false);
          assert.ok(
            matrix.documentOverflow <= 1,
            `${surface} keeps overflow inside its strip`,
          );
          assert.equal(
            matrix.selected.length,
            1,
            `${surface} has one original selection`,
          );
          assert.ok(matrix.selected.every((item) => item.underline === "2px"));
          for (const control of matrix.controls) {
            assert.ok(control.height >= 40, `${surface} has 40px targets`);
            assert.equal(control.radius, "0px");
            assert.equal(control.background, "rgba(0, 0, 0, 0)");
            assert.equal(control.font, "14px");
            assert.equal(control.line, "20px");
            if (control.close) assert.equal(control.width, 40);
            else assert.equal(control.padding, "16px");
          }
          // Keyboard focus must stay inside the owning strip and retain an inset ring.
          await page.keyboard.press("Tab");
          await page.locator(".shared-tab-control").first().focus();
          const focus = await page.evaluate(() => {
            const node = document.activeElement,
              style = getComputedStyle(node);
            return {
              width: style.outlineWidth,
              offset: style.outlineOffset,
              visible: node.matches(":focus-visible"),
            };
          });
          assert.equal(focus.visible, true);
          assert.equal(focus.width, "2px");
          assert.equal(focus.offset, "-3px");
          if (surface === "project") {
            const ordinary = await page
              .locator("input, .settings-task-footer button")
              .evaluateAll((nodes) =>
                nodes.map((node) => getComputedStyle(node).borderRadius),
              );
            assert.ok(
              ordinary.includes("8px"),
              "ordinary Settings controls retain corners",
            );
          }
          process.stdout.write(
            JSON.stringify({
              operation: "shared-tabs-geometry",
              revision,
              dirty,
              surface,
              language,
              theme,
              ...size,
              matrix,
            }) + "\n",
          );
          cases++;
        }
  assert.deepEqual(errors, []);
  process.stdout.write(
    JSON.stringify({
      operation: "shared-tabs-layout",
      revision,
      dirty,
      cases,
      result: "passed",
      limits:
        "Synthetic component DOM geometry; no installed CEF, live React browser lifecycle, account, native zoom or platform acceptance",
    }) + "\n",
  );
} finally {
  await browser?.close();
  await rm(directory, { recursive: true, force: true });
}
