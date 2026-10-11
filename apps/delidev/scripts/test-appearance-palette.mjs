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
const [source, palettes, themes] = await Promise.all(
  ["appearance-preferences.ts", "appearance-palettes.json", "themes.css"].map(
    (name) => readFile(new URL(`../src/${name}`, import.meta.url), "utf8"),
  ),
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
    `<html data-theme="light"><head><style>${themes}</style></head><body><div id="probe" style="background:var(--background);color:var(--text);border:1px solid var(--accent)">Palette</div></body></html>`,
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
  for (const [mode, scheme, palette] of [
    ["dark", "light", "dracula"],
    ["dark", "dark", "cascade-fixture"],
    ["system", "light", "cascade-fixture"],
    ["system", "dark", "cascade-fixture"],
    ["system", "light", "dracula"],
    ["light", "dark", "default"],
    ["dark", "light", "default"],
  ]) {
    await page.emulateMedia({ colorScheme: scheme });
    const result = await page.evaluate(({ mode, palette }) => {
      const fixture = window.paletteFixture;
      const old = document.adoptedStyleSheets.at(-1);
      fixture.retire?.();
      const dark =
        mode === "dark" ||
        (mode === "system" &&
          matchMedia("(prefers-color-scheme: dark)").matches);
      document.documentElement.dataset.theme = dark ? "dark" : "light";
      fixture.preferences.dark_palette = palette;
      fixture.preferences.light_palette =
        palette === "default" ? "default" : "nord";
      const colors = fixture.selectedColors(fixture.preferences, dark);
      fixture.retire = fixture.applyAppearanceColors(colors);
      const actual = getComputedStyle(document.querySelector("#probe"));
      const expected = document.createElement("div");
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
      };
      expected.remove();
      return result;
    }, { mode, palette });
    assert.deepEqual(
      result.actual,
      result.expected,
      JSON.stringify({ mode, scheme, palette }),
    );
    assert(result.replaced && result.unrelated);
    assert.equal(result.sheets, 2);
    cases++;
  }
  assert(await page.evaluate(() => {
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
  }));
  console.log(JSON.stringify({
    operation: "appearance-palette",
    cases,
    nativeAcceptance: "not-performed",
  }));
} finally {
  await browser.close();
}
