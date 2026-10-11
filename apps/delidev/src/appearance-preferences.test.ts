// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, test, vi } from "vitest";
import { applyAppearanceColors, palettes } from "./appearance-preferences";

const originalSheets = document.adoptedStyleSheets;
afterEach(() => { vi.unstubAllGlobals(); document.adoptedStyleSheets = originalSheets; });
function cssom() {
  class Sheet {
    cssRules: { selectorText: string; style: CSSStyleDeclaration }[] = [];
    replaceSync(text: string) { this.cssRules = [{ selectorText: text.split(" {")[0], style: document.createElement("div").style }]; }
  }
  vi.stubGlobal("CSSStyleSheet", Sheet);
  document.adoptedStyleSheets = [];
}
test("selected maps use resolved-root specificity and preserve unrelated sheet ownership", () => {
  cssom();
  const unrelated = new CSSStyleSheet(); document.adoptedStyleSheets = [unrelated];
  const remove = applyAppearanceColors(palettes.dracula.dark);
  const selected = document.adoptedStyleSheets[1];
  const rule = selected.cssRules[0] as CSSStyleRule;
  expect(rule.selectorText).toBe(":root[data-theme]");
  for (const [token, color] of Object.entries(palettes.dracula.dark)) expect(rule.style.getPropertyValue(`--${token}`)).toBe(color);
  remove(); expect(document.adoptedStyleSheets).toEqual([unrelated]);
  const restore = applyAppearanceColors(palettes.default.light);
  expect((document.adoptedStyleSheets[1].cssRules[0] as CSSStyleRule).style.getPropertyValue("--background")).toBe(palettes.default.light.background);
  remove(); expect(document.adoptedStyleSheets).toHaveLength(2);
  restore(); expect(document.adoptedStyleSheets).toEqual([unrelated]);
});
test("preview selectors remain scoped and disposal preserves root ownership", () => {
  cssom(); const root = applyAppearanceColors(palettes.dracula.dark);
  const preview = applyAppearanceColors(palettes.nord.light, '[data-preview="owned"]');
  expect((document.adoptedStyleSheets[1].cssRules[0] as CSSStyleRule).selectorText).toBe('[data-preview="owned"]');
  preview(); expect(document.adoptedStyleSheets).toHaveLength(1); root();
  expect(document.adoptedStyleSheets).toHaveLength(0);
});
test("missing replaceSync keeps static fallback and unrelated sheets", () => {
  cssom(); const unrelated = new CSSStyleSheet(); document.adoptedStyleSheets = [unrelated];
  vi.stubGlobal("CSSStyleSheet", class {});
  applyAppearanceColors(palettes.dracula.dark)();
  expect(document.adoptedStyleSheets).toEqual([unrelated]);
});
