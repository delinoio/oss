// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, test, vi } from "vitest";
import { readFileSync } from "node:fs";
import { applyAppearanceColors, defaultPreferences, palettes, selectedColors } from "./appearance-preferences";

const originalSheets = document.adoptedStyleSheets;
const NativeSheet = CSSStyleSheet;

function constructedSheets() {
  // jsdom provides CSSOM parsing but not constructed-sheet replacement/adoption.
  // This seam inspects the real parsed rules; it does not emulate browser cascade.
  vi.stubGlobal("CSSStyleSheet", class extends NativeSheet {
    replaceSync(css: string) { this.insertRule(css, 0); }
  });
  document.adoptedStyleSheets = [];
}
afterEach(() => {
  document.adoptedStyleSheets = originalSheets;
  delete document.documentElement.dataset.theme;
  vi.unstubAllGlobals();
});

test("selected light and dark rules outrank the static theme selectors", () => {
  constructedSheets();
  for (const dark of [false, true]) {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    const preferences = { ...defaultPreferences(), light_palette: "nord", dark_palette: "dracula" };
    const colors = selectedColors(preferences, dark);
    const dispose = applyAppearanceColors(colors);
    const rule = document.adoptedStyleSheets[0].cssRules[0] as CSSStyleRule;
    expect(document.documentElement.matches(rule.selectorText)).toBe(true);
    // Compare against the actual static selectors, including the OS fallback.
    // :not contributes its argument's specificity, not an extra pseudo-class.
    const specificity = (selector: string) => (selector.replace(/:not\(/g, "(").match(/:[a-z-]+|\[[^\]]+\]/g) ?? []).length;
    const staticRootSelectors = [...readFileSync("src/themes.css", "utf8").matchAll(/(:root[^{}]*)\{[^}]*--background:/g)].map(match => match[1].trim());
    expect(staticRootSelectors).toHaveLength(3);
    for (const selector of staticRootSelectors) expect(specificity(rule.selectorText)).toBeGreaterThan(specificity(selector));
    for (const [token, value] of Object.entries(colors)) expect(rule.style.getPropertyValue(`--${token}`)).toBe(value);
    dispose();
    expect(document.adoptedStyleSheets).toEqual([]);
  }
});

test("independent System maps and custom replacement keep only the current owned sheet", () => {
  constructedSheets();
  const unrelated = new CSSStyleSheet();
  document.adoptedStyleSheets = [unrelated];
  const custom = { id: "019b0000-0000-7000-8000-000000000001", version: 1 as const, name: "Fixture", light: { ...palettes.nord.light }, dark: { ...palettes.dracula.dark, background: "#101820", accent: "#917BCB" } };
  const preferences = { ...defaultPreferences(), light_palette: "nord", dark_palette: custom.id, custom_themes: [custom] };
  let dispose = () => {};
  for (const dark of [true, false, true]) {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    dispose();
    const colors = selectedColors(preferences, dark);
    dispose = applyAppearanceColors(colors);
    expect(document.adoptedStyleSheets).toHaveLength(2);
    expect(document.adoptedStyleSheets[0]).toBe(unrelated);
    const rule = document.adoptedStyleSheets[1].cssRules[0] as CSSStyleRule;
    expect(rule.style.getPropertyValue("--background")).toBe(dark ? custom.dark.background : palettes.nord.light.background);
    expect(rule.style.getPropertyValue("--accent")).toBe(dark ? custom.dark.accent : palettes.nord.light.accent);
  }
  dispose();
  const defaults = selectedColors(defaultPreferences(), true);
  const removeDefault = applyAppearanceColors(defaults);
  const defaultRule = document.adoptedStyleSheets[1].cssRules[0] as CSSStyleRule;
  for (const [token, value] of Object.entries(palettes.default.dark)) expect(defaultRule.style.getPropertyValue(`--${token}`)).toBe(value);
  removeDefault();
  expect(document.adoptedStyleSheets).toEqual([unrelated]);
});
