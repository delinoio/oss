// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import { test } from "node:test";

const palettes = JSON.parse(await readFile(new URL("../src/appearance-palettes.json", import.meta.url), "utf8"));
const source = await readFile(new URL("../src/appearance-preferences.ts", import.meta.url), "utf8");
// Load the production color application and contrast helpers without frontend
// dependencies. These functions contain only erasable TypeScript annotations.
const colorFunctions = source.slice(source.indexOf("const luminance="), source.indexOf("export function validColors")) + source.slice(source.indexOf("export function applyAppearanceColors"));
const moduleSource = stripTypeScriptTypes(colorFunctions);
const { applyAppearanceColors } = await import(`data:text/javascript;base64,${Buffer.from(moduleSource).toString("base64")}`);
const themes = await readFile(new URL("../src/themes.css", import.meta.url), "utf8");

// This fixture records the actual CSSOM writes. It does not claim browser paint
// or CSP acceptance; those checks belong to the browser/platform CI fixtures.
class Sheet {
  cssRules = [];
  replaceSync(css) {
    const selectorText = css.slice(0, css.indexOf("{")).trim();
    const properties = new Map();
    this.cssRules = [{ selectorText, style: { setProperty: (name, value) => properties.set(name, value), getPropertyValue: name => properties.get(name) } }];
  }
}
// Owning root selectors use only :root, :not() and attribute conditions.
// :not() itself adds no specificity; its argument still contributes.
function rootSpecificity(selector) {
  assert.match(selector, /^:root/);
  assert.doesNotMatch(selector, /[#>,+~]/);
  return (selector.match(/:root/g) ?? []).length + (selector.match(/\[/g) ?? []).length;
}
const staticSelectors = [...themes.matchAll(/(:root[^{}]*)\{([^{}]*)\}/g)].filter(match => match[2].includes("--background:")).map(match => match[1].trim());
const originalSheet = globalThis.CSSStyleSheet, originalDocument = globalThis.document;

test("bundled and custom maps outrank every static root palette, and replacement retires only its sheet", () => {
  const unrelated = {};
  globalThis.CSSStyleSheet = Sheet;
  globalThis.document = { adoptedStyleSheets: [unrelated] };
  let retire = () => {};
  try {
    const custom = { id: "fixture", version: 1, name: "Fixture", light: { ...palettes.default.light }, dark: { ...palettes.default.dark, background: "#101010", text: "#FFFFFF", accent: "#345678" } };
    for (const colors of [palettes.dracula.dark, custom.dark, palettes.default.light, palettes.default.dark, palettes.default.light]) {
      retire();
      retire = applyAppearanceColors(colors);
      assert.equal(document.adoptedStyleSheets.length, 2);
      assert.equal(document.adoptedStyleSheets[0], unrelated);
      const rule = document.adoptedStyleSheets[1].cssRules[0];
      assert.ok(staticSelectors.length >= 3);
      for (const selector of staticSelectors) assert.ok(rootSpecificity(rule.selectorText) > rootSpecificity(selector), `${rule.selectorText} must outrank ${selector}`);
      for (const [token, value] of Object.entries(colors)) assert.equal(rule.style.getPropertyValue(`--${token}`), value);
    }
    retire();
    retire();
    assert.deepEqual(document.adoptedStyleSheets, [unrelated]);
  } finally {
    retire();
    if (originalSheet === undefined) delete globalThis.CSSStyleSheet;
    else globalThis.CSSStyleSheet = originalSheet;
    if (originalDocument === undefined) delete globalThis.document;
    else globalThis.document = originalDocument;
  }
});

test("missing constructed stylesheet support leaves the existing fallback untouched", () => {
  const unrelated = {};
  globalThis.CSSStyleSheet = class {};
  globalThis.document = { adoptedStyleSheets: [unrelated] };
  try {
    applyAppearanceColors(palettes.default.dark)();
    assert.deepEqual(document.adoptedStyleSheets, [unrelated]);
  } finally {
    if (originalSheet === undefined) delete globalThis.CSSStyleSheet;
    else globalThis.CSSStyleSheet = originalSheet;
    if (originalDocument === undefined) delete globalThis.document;
    else globalThis.document = originalDocument;
  }
});
