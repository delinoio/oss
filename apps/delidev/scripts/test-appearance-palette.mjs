// SPDX-License-Identifier: Apache-2.0
// Synthetic browser cascade evidence; no native window or account is used.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import ts from "typescript";

const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright"
);
const [source, palettes, themes, styles] = await Promise.all(
  [
    "appearance-preferences.ts",
    "appearance-palettes.json",
    "themes.css",
    "styles.css",
  ].map((name) => readFile(new URL(`../src/${name}`, import.meta.url), "utf8")),
);
// Execute the actual helper, with its sole JSON import inlined for this page.
const paletteImport = 'import palettes from "./appearance-palettes.json";';
assert(source.includes(paletteImport));
const compiled = ts.transpileModule(
  source.replace(paletteImport, `const palettes = ${palettes};`),
  {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
    },
  },
).outputText;
const browser = await chromium.launch({
  headless: true,
  ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL
    ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL }
    : {}),
});
let cases = 0;
try {
  const page = await browser.newPage();
  await page.setContent(
    `<html data-theme="light"><head><style>${styles}</style><style>${themes}</style></head><body>
      <div id="probe" style="background:var(--background);color:var(--text);border:1px solid var(--accent)">Palette</div>
      <aside id="sidebar" class="sidebar"><button id="selected" class="sidebar-session-row" aria-current="true">Demo</button></aside>
      <div class="appearance-preview"><div id="card">Demo card <a id="link" href="#demo" style="color:var(--link)">Link</a></div></div>
      <div id="conversation" class="usage-page">Synthetic conversation surface</div>
      <button id="primary" class="primary">Send</button>
      <textarea id="draft">unsent synthetic draft</textarea>
    </body></html>`,
  );
  await page.addScriptTag({
    type: "module",
    content: `${compiled}\nwindow.paletteFixture = { applyAppearanceColors, defaultPreferences, parseThemeFile, selectedColors, palettes };`,
  });
  await page.waitForFunction(() => !!window.paletteFixture);
  await page.evaluate(() => {
    const fixture = window.paletteFixture;
    fixture.unrelated = new CSSStyleSheet();
    fixture.unrelated.replaceSync(".unrelated { --unrelated: preserved; }");
    document.adoptedStyleSheets = [fixture.unrelated];
    const custom = fixture.parseThemeFile(
      JSON.stringify({
        version: 1,
        name: "Cascade fixture",
        light: { ...fixture.palettes.default.light, background: "#F2F3F4" },
        dark: {
          ...fixture.palettes.default.dark,
          background: "#151719",
          accent: "#315DA8",
        },
      }),
    );
    fixture.preferences = {
      ...fixture.defaultPreferences(),
      light_palette: "nord",
      dark_palette: "dracula",
      custom_themes: [{ ...custom, id: "cascade-fixture" }],
    };
  });
  await page.evaluate(() => {
    const fixture = window.paletteFixture;
    fixture.draft = document.querySelector("#draft");
    fixture.customBytes = JSON.stringify(fixture.preferences.custom_themes);
  });
  const familyCases = Object.keys(JSON.parse(palettes))
    .filter((id) => id !== "default")
    .flatMap((id) => [
      ["light", "dark", id, id],
      ["dark", "light", id, id],
    ]);
  const scenarios = [
    ...familyCases,
    ["dark", "light", "nord", "dracula"],
    ["dark", "dark", "nord", "cascade-fixture"],
    ["system", "light", "cascade-fixture", "dracula"],
    ["system", "dark", "nord", "cascade-fixture"],
    ["system", "light", "nord", "dracula"],
    ["system", "dark", "nord", "dracula"],
    ["system", "light", "nord", "dracula"],
    ["light", "dark", "default", "default"],
    ["dark", "light", "default", "default"],
  ];
  // Halved viewport dimensions exercise effective 200% reflow without
  // claiming native zoom or installed-platform acceptance.
  for (const viewport of [
    { width: 960, height: 640 },
    { width: 480, height: 320 },
  ]) {
    await page.setViewportSize(viewport);
    await page.evaluate(() => {
      const geometry = () =>
        ["#sidebar", "#selected", "#card", "#primary", "#draft"].map(
          (selector) => {
            const rect = document
              .querySelector(selector)
              .getBoundingClientRect();
            return [rect.x, rect.y, rect.width, rect.height];
          },
        );
      window.paletteFixture.geometry = geometry;
      window.paletteFixture.originalGeometry = geometry();
    });
    for (const [mode, scheme, lightPalette, darkPalette] of scenarios) {
      await page.emulateMedia({ colorScheme: scheme });
      const result = await page.evaluate(
        ({ mode, lightPalette, darkPalette }) => {
          const fixture = window.paletteFixture;
          const old = document.adoptedStyleSheets.at(-1);
          fixture.retire?.();
          const dark =
            mode === "dark" ||
            (mode === "system" &&
              matchMedia("(prefers-color-scheme: dark)").matches);
          document.documentElement.dataset.theme = dark ? "dark" : "light";
          fixture.preferences.dark_palette = darkPalette;
          fixture.preferences.light_palette = lightPalette;
          const colors = fixture.selectedColors(fixture.preferences, dark);
          fixture.retire = fixture.applyAppearanceColors(colors);
          const actual = getComputedStyle(document.querySelector("#probe"));
          const expected = document.createElement("div");
          expected.hidden = true;
          expected.style.cssText = `background:${colors.background};color:${colors.text};border:1px solid ${colors.accent}`;
          document.body.append(expected);
          const expectedStyle = getComputedStyle(expected);
          const properties = ["backgroundColor", "color", "borderTopColor"];
          const result = {
            actual: properties.map((key) => actual[key]),
            expected: properties.map((key) => expectedStyle[key]),
            replaced:
              old === fixture.unrelated ||
              !document.adoptedStyleSheets.includes(old),
            sheets: document.adoptedStyleSheets.length,
            unrelated: document.adoptedStyleSheets.includes(fixture.unrelated),
            tokensMatch: Object.entries(colors).every(
              ([token, value]) =>
                getComputedStyle(document.documentElement)
                  .getPropertyValue(`--${token}`)
                  .trim()
                  .toUpperCase() === value.toUpperCase(),
            ),
            surfaces: [
              ["#sidebar", "backgroundColor", "surface-muted"],
              ["#selected", "backgroundColor", "surface-selected"],
              ["#selected", "color", "text"],
              ["#card", "backgroundColor", "surface"],
              ["#card", "color", "text"],
              ["#card", "borderTopColor", "control-border"],
              ["#link", "color", "link"],
              ["#conversation", "backgroundColor", "conversation-background"],
              ["#conversation", "color", "text"],
              ["#primary", "backgroundColor", "accent"],
              ["#primary", "color", "on-accent"],
            ].every(([selector, property, token]) => {
              const target = document.querySelector(selector);
              expected.style.cssText =
                property === "color"
                  ? `color:${colors[token]}`
                  : property === "backgroundColor"
                    ? `background:${colors[token]}`
                    : `border:1px solid ${colors[token]}`;
              return (
                getComputedStyle(target)[property] ===
                getComputedStyle(expected)[property]
              );
            }),
            draftRetained:
              document.querySelector("#draft") === fixture.draft &&
              fixture.draft.value === "unsent synthetic draft",
            customRetained:
              JSON.stringify(fixture.preferences.custom_themes) ===
              fixture.customBytes,
            geometryRetained:
              JSON.stringify(fixture.geometry()) ===
              JSON.stringify(fixture.originalGeometry),
            excluded: ["--terminal-background", "--backdrop", "--shadow"].every(
              (token) =>
                !Object.hasOwn(colors, token.slice(2)) &&
                getComputedStyle(document.documentElement)
                  .getPropertyValue(token)
                  .trim(),
            ),
          };
          expected.remove();
          return result;
        },
        { mode, lightPalette, darkPalette },
      );
      assert.deepEqual(
        result.actual,
        result.expected,
        JSON.stringify({ mode, scheme, lightPalette, darkPalette }),
      );
      assert(result.replaced && result.unrelated);
      assert(result.tokensMatch && result.surfaces);
      assert(
        result.draftRetained &&
          result.customRetained &&
          result.excluded &&
          result.geometryRetained,
      );
      assert.equal(result.sheets, 2);
      cases++;
    }
  }
  assert(
    await page.evaluate(() => {
      const fixture = window.paletteFixture;
      fixture.retire();
      fixture.retire();
      const root = document.documentElement;
      const fallback = getComputedStyle(root)
        .getPropertyValue("--background")
        .trim()
        .toLowerCase();
      return (
        document.adoptedStyleSheets.length === 1 &&
        document.adoptedStyleSheets[0] === fixture.unrelated &&
        fallback === fixture.palettes.default.dark.background.toLowerCase()
      );
    }),
  );
  console.log(
    JSON.stringify({
      operation: "appearance-palette",
      cases,
      nativeAcceptance: "not-performed",
    }),
  );
} finally {
  await browser.close();
}
