// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { colorTokens, contrast, defaultPreferences, palettes, parsePreferences, selectedColors, validColors, type ColorMap, type CustomTheme } from "./appearance-preferences";

// Approved encoded-sRGB cores. Derivation belongs only to regression fixtures;
// production ships complete static maps, with no generator or preference rewrite.
const cores = [
  ["titanium", "light", "#F4F4F5", "#FFFFFF", "#E4E4E7", "#27272A", "#52525B"],
  ["titanium", "dark", "#18181B", "#27272A", "#09090B", "#FAFAFA", "#52525B"],
  ["nord", "light", "#ECEFF4", "#E5E9F0", "#D8DEE9", "#2E3440", "#3A5877"],
  ["nord", "dark", "#2E3440", "#3B4252", "#242933", "#ECEFF4", "#3A5877"],
  ["dracula", "light", "#F7F3FF", "#F0E9FA", "#E6DCF3", "#382C4A", "#7143A5"],
  ["dracula", "dark", "#282A36", "#343746", "#21222C", "#F8F8F2", "#7143A5"],
  ["solarized", "light", "#FDF6E3", "#EEE8D5", "#E3DCC8", "#4B6068", "#006B82"],
  ["solarized", "dark", "#002B36", "#073642", "#001F27", "#D6D3C4", "#006B82"],
] as const;
function mix(x: string, y: string, percent: number) {
  return "#" + [1, 3, 5].map(index => Math.round((percent * parseInt(x.slice(index, index + 2), 16) + (100 - percent) * parseInt(y.slice(index, index + 2), 16)) / 100).toString(16).padStart(2, "0")).join("").toUpperCase();
}
function expected(mode: "light" | "dark", B: string, S: string, I: string, T: string, A: string): ColorMap {
  const Q = mix(A, S, 8), C = mix(T, S, 80), P = mode === "light" ? "#FFFFFF" : "#000000";
  return { ...palettes.default[mode], background: B, surface: S, "surface-inset": I, text: T, accent: A,
    "surface-subtle": mix(B, S, 50), "surface-muted": mix(I, S, 25), "surface-hover": mix(T, S, 8),
    "surface-selected": Q, "selected-background": Q, "selected-text": T, "selected-border": C,
    "text-secondary": mix(T, S, 98), muted: mix(T, S, 97), "text-subtle": mix(T, S, 96),
    border: mix(T, S, 25), "control-border": C, "border-subtle": mix(T, S, 15),
    "accent-hover": mix("#000000", A, 15), "on-accent": "#FFFFFF", link: mode === "light" ? A : mix("#FFFFFF", A, 65), focus: mode === "light" ? A : mix("#FFFFFF", A, 65),
    "inverse-surface": T, "inverse-hover": mix(P, T, 10), "inverse-border": mix(P, T, 65), "on-inverse": P, "on-inverse-muted": mix(P, T, 90),
    "conversation-background": Q, "conversation-text": T, "conversation-border": C,
    "execution-running": mode === "light" ? mix("#000000", "#087B78", 15) : palettes.default.dark["execution-running"] };
}

test("bundled Default JSON bytes and five stable palette IDs remain unchanged", () => {
  const source = readFileSync(new URL("./appearance-palettes.json", import.meta.url), "utf8");
  expect(createHash("sha256").update(source.slice(0, source.indexOf('  "titanium"'))).digest("hex")).toBe("71a9d638db79217a5a1c5cb3b008b6853b30518134ba86aef8f621fbb7c89e61");
  expect(Object.keys(palettes)).toEqual(["default", "titanium", "nord", "dracula", "solarized"]);
});

test.each(cores)("bundled %s %s uses exact approved opaque semantic colors", (family, mode, B, S, I, T, A) => {
  const map = palettes[family][mode];
  expect(map).toEqual(expected(mode, B, S, I, T, A));
  expect(Object.keys(map).sort()).toEqual([...colorTokens].sort());
  expect(Object.keys(map)).toHaveLength(38);
  expect(Object.values(map).every(value => /^#[0-9A-F]{6}$/.test(value))).toBe(true);
  expect(validColors(map)).toBe(true);
  // Adjacent application surfaces include sidebar/card hover, selected and
  // conversation treatments, plus the existing semantic status backgrounds.
  const surfaces = ["background", "surface", "surface-subtle", "surface-muted", "surface-inset", "surface-hover", "surface-selected", "selected-background", "conversation-background"] as const;
  for (const surface of surfaces) expect(contrast(map.text, map[surface]), `${family} ${mode}: text/${surface}`).toBeGreaterThanOrEqual(4.5);
  for (const foreground of ["text-secondary", "muted", "text-subtle"] as const)
    for (const surface of ["background", "surface", "surface-muted", "conversation-background"] as const) expect(contrast(map[foreground], map[surface]), `${family} ${mode}: ${foreground}/${surface}`).toBeGreaterThanOrEqual(4.5);
  for (const foreground of ["link", "success-text", "execution-running"] as const)
    for (const surface of ["background", "surface", "conversation-background"] as const) expect(contrast(map[foreground], map[surface]), `${family} ${mode}: ${foreground}/${surface}`).toBeGreaterThanOrEqual(4.5);
  for (const [foreground, surface] of [["selected-text", "selected-background"], ["conversation-text", "conversation-background"], ["on-accent", "accent"], ["on-accent", "accent-hover"], ["on-inverse", "inverse-surface"], ["on-inverse-muted", "inverse-hover"], ["warning-text", "warning-background"], ["danger-text", "danger-background"]] as const)
    expect(contrast(map[foreground], map[surface]), `${family} ${mode}: ${foreground}/${surface}`).toBeGreaterThanOrEqual(4.5);
  for (const foreground of ["focus", "control-border", "selected-border", "conversation-border"] as const)
    for (const surface of surfaces) expect(contrast(map[foreground], map[surface]), `${family} ${mode}: ${foreground}/${surface}`).toBeGreaterThanOrEqual(3);
});

test("saved custom maps and earlier bundled duplicates retain their original independent bytes", () => {
  const legacy: CustomTheme = { version: 1, id: "01900000-0000-7000-8000-000000000001", name: "Earlier Titanium duplicate", light: { ...palettes.default.light, background: "#F5F5F5", accent: "#414D5E" }, dark: { ...palettes.default.dark, background: "#191919", accent: "#435169" } };
  const custom: CustomTheme = { ...structuredClone(legacy), id: "01900000-0000-7000-8000-000000000002", name: "Saved custom", light: { ...palettes.default.light, background: "#F2F5FA" } };
  for (const original of [legacy, custom]) {
    const preferences = { ...defaultPreferences(), light_palette: original.id, dark_palette: original.id, custom_themes: [original] };
    const bytes = JSON.stringify(preferences);
    const restored = parsePreferences(JSON.parse(bytes));
    expect(JSON.stringify(restored)).toBe(bytes);
    expect(selectedColors(restored, false)).toEqual(original.light);
    expect(selectedColors(restored, true)).toEqual(original.dark);
    expect(selectedColors(restored, false)).not.toEqual(palettes.titanium.light);
  }
});
