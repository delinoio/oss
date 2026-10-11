// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, test, vi } from "vitest";
import { applyAppearanceColors, palettes } from "./appearance-preferences";

const originalSheets = Object.getOwnPropertyDescriptor(document, "adoptedStyleSheets");
afterEach(() => {
  vi.unstubAllGlobals();
  if (originalSheets) Object.defineProperty(document, "adoptedStyleSheets", originalSheets);
  else Reflect.deleteProperty(document, "adoptedStyleSheets");
});

test("root palettes match dark specificity and dispose only their owned CSSOM sheet", () => {
  class Sheet {
    cssRules: { selectorText: string; style: CSSStyleDeclaration }[] = [];
    replaceSync(css: string) {
      this.cssRules = [{ selectorText: css.split(" {")[0], style: document.createElement("div").style }];
    }
  }
  vi.stubGlobal("CSSStyleSheet", Sheet);
  const unrelated = new Sheet();
  Object.defineProperty(document, "adoptedStyleSheets", { configurable: true, writable: true, value: [unrelated] });
  const disposeDark = applyAppearanceColors(palettes.dracula.dark);
  const dark = document.adoptedStyleSheets[1] as unknown as Sheet;
  expect(dark.cssRules[0].selectorText).toBe(":root:root");
  for (const [token, value] of Object.entries(palettes.dracula.dark)) {
    expect(dark.cssRules[0].style.getPropertyValue(`--${token}`)).toBe(value);
  }
  const disposePreview = applyAppearanceColors(palettes.default.light, ".custom-preview");
  const preview = document.adoptedStyleSheets[2] as unknown as Sheet;
  expect(preview.cssRules[0].selectorText).toBe(".custom-preview");
  disposeDark(); disposeDark();
  expect(document.adoptedStyleSheets).toEqual([unrelated, preview]);
  disposePreview();
  expect(document.adoptedStyleSheets).toEqual([unrelated]);
});
