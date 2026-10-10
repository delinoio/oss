// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, test, vi } from "vitest";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { applyAppearanceColors, contrast, defaultPreferences, palettes, selectedColors, validColors } from "./appearance-preferences";

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

// Approved encoded-sRGB fixture rules belong only to this test. Runtime and
// native consumers both read the committed opaque maps without generating them.
const approvedCores = {
  titanium: { light: ["#F4F4F5", "#FFFFFF", "#E4E4E7", "#27272A", "#52525B"], dark: ["#18181B", "#27272A", "#09090B", "#FAFAFA", "#52525B"] },
  nord: { light: ["#ECEFF4", "#E5E9F0", "#D8DEE9", "#2E3440", "#3A5877"], dark: ["#2E3440", "#3B4252", "#242933", "#ECEFF4", "#3A5877"] },
  dracula: { light: ["#F7F3FF", "#F0E9FA", "#E6DCF3", "#382C4A", "#7143A5"], dark: ["#282A36", "#343746", "#21222C", "#F8F8F2", "#7143A5"] },
  solarized: { light: ["#FDF6E3", "#EEE8D5", "#E3DCC8", "#4B6068", "#006B82"], dark: ["#002B36", "#073642", "#001F27", "#D6D3C4", "#006B82"] },
} as const;
function mix(x: string, y: string, proportion: number) {
  return `#${[1, 3, 5].map(offset => Math.round(proportion * parseInt(x.slice(offset, offset + 2), 16) + (1 - proportion) * parseInt(y.slice(offset, offset + 2), 16)).toString(16).padStart(2, "0")).join("").toUpperCase()}`;
}
function approvedMap(family: keyof typeof approvedCores, mode: "light" | "dark") {
  const [b, s, i, t, a] = approvedCores[family][mode], q = mix(a, s, .08), c = mix(t, s, .8), pole = mode === "light" ? "#FFFFFF" : "#000000";
  return {
    ...palettes.default[mode], background: b, surface: s, "surface-inset": i, text: t, accent: a,
    "surface-subtle": mix(b, s, .5), "surface-muted": mix(i, s, .25), "surface-hover": mix(t, s, .08),
    "surface-selected": q, "selected-background": q, "selected-text": t, "selected-border": c,
    "text-secondary": mix(t, s, .98), muted: mix(t, s, .97), "text-subtle": mix(t, s, .96),
    border: mix(t, s, .25), "control-border": c, "border-subtle": mix(t, s, .15),
    "accent-hover": mix("#000000", a, .15), "on-accent": "#FFFFFF", link: mode === "light" ? a : mix("#FFFFFF", a, .65), focus: mode === "light" ? a : mix("#FFFFFF", a, .65),
    "inverse-surface": t, "inverse-hover": mix(pole, t, .1), "inverse-border": mix(pole, t, .65), "on-inverse": pole, "on-inverse-muted": mix(pole, t, .9),
    "conversation-background": q, "conversation-text": t, "conversation-border": c,
    "execution-running": mode === "light" ? mix("#000000", "#087B78", .15) : palettes.default.dark["execution-running"],
  };
}

test("all eight bundled family maps retain the exact approved 38 opaque tokens", () => {
  expect(Object.keys(palettes)).toEqual(["default", "titanium", "nord", "dracula", "solarized"]);
  for (const family of Object.keys(approvedCores) as (keyof typeof approvedCores)[]) {
    for (const mode of ["light", "dark"] as const) {
      const map = palettes[family][mode];
      expect(map, `${family} ${mode}`).toEqual(approvedMap(family, mode));
      expect(Object.keys(map)).toEqual(Object.keys(palettes.default[mode]));
      expect(Object.keys(map)).toHaveLength(38);
      expect(Object.values(map).every(value => /^#[0-9A-F]{6}$/.test(value))).toBe(true);
      expect(validColors(map)).toBe(true);
    }
  }
});

test("family text, status, selection, controls and focus retain their actual surface contrast", () => {
  const backgrounds = ["background", "surface", "surface-subtle", "surface-muted", "surface-inset", "surface-hover", "surface-selected", "selected-background", "conversation-background"] as const;
  for (const family of Object.keys(approvedCores) as (keyof typeof approvedCores)[]) {
    for (const mode of ["light", "dark"] as const) {
      const map = palettes[family][mode];
      const pair = (foreground: keyof typeof map, background: keyof typeof map, minimum: number) => expect(contrast(map[foreground], map[background]), `${family} ${mode}: ${foreground}/${background}`).toBeGreaterThanOrEqual(minimum);
      for (const background of backgrounds) {
        for (const foreground of ["text", "text-secondary", "muted", "success-text", "execution-running"] as const) pair(foreground, background, 4.5);
        for (const foreground of ["control-border", "selected-border", "focus"] as const) pair(foreground, background, 3);
      }
      // Plain helper/placeholder text and normal links use the content surfaces.
      // Tinted row disclosure/link glyphs are indicators, with a separate 3:1 gate.
      for (const background of ["background", "surface", "surface-subtle", "surface-muted"] as const) {
        pair("text-subtle", background, 4.5); pair("link", background, 4.5);
      }
      for (const background of ["surface-inset", "surface-hover", "surface-selected", "selected-background"] as const) {
        pair("text-subtle", background, 3); pair("link", background, 3);
      }
      for (const [foreground, background] of [["selected-text", "selected-background"], ["conversation-text", "conversation-background"], ["warning-text", "warning-background"], ["danger-text", "danger-background"], ["on-accent", "accent"], ["on-accent", "accent-hover"], ["on-inverse", "inverse-surface"], ["on-inverse", "inverse-hover"], ["on-inverse-muted", "inverse-surface"]] as const) pair(foreground, background, 4.5);
    }
  }
});

test("Default map bytes and previously saved custom duplicates remain original", () => {
  const serialized = readFileSync("src/appearance-palettes.json", "utf8");
  expect(createHash("sha256").update(serialized.split('  "titanium"')[0]).digest("hex")).toBe("71a9d638db79217a5a1c5cb3b008b6853b30518134ba86aef8f621fbb7c89e61");
  const oldLight = { ...palettes.default.light, background: "#F5F5F5", accent: "#414D5E" }, oldDark = { ...palettes.default.dark, background: "#191919", accent: "#435169" };
  const custom = { id: "019b0000-0000-7000-8000-000000000009", version: 1 as const, name: "Earlier bundled duplicate", light: oldLight, dark: oldDark };
  const preferences = { ...defaultPreferences(), light_palette: custom.id, dark_palette: custom.id, custom_themes: [custom] }, before = JSON.stringify(preferences);
  expect(selectedColors(preferences, false)).toBe(oldLight);
  expect(selectedColors(preferences, true)).toBe(oldDark);
  expect(JSON.stringify(preferences)).toBe(before);
  expect(selectedColors({ ...preferences, light_palette: "nord", dark_palette: "solarized" }, false)).toBe(palettes.nord.light);
  expect(selectedColors({ ...preferences, light_palette: "nord", dark_palette: "solarized" }, true)).toBe(palettes.solarized.dark);
});
