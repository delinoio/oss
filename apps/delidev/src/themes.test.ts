// SPDX-License-Identifier: Apache-2.0
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

const directory = join(process.cwd(), "src");
const source = readFileSync(join(directory, "themes.css"), "utf8");
const quotaColors = readFileSync(join(directory, "quota-color.ts"), "utf8");
const palettes = [...source.matchAll(/color-scheme: (?:light|dark);([^}]+)/g)].map(match => new Map([...match[1].matchAll(/--([\w-]+):\s*(#[\da-f]+);/g)].map(item => [item[1], item[2]])));

function luminance(hex: string) {
  const values = [1, 3, 5].map(offset => parseInt(hex.slice(offset, offset + 2), 16) / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
  return values[0] * .2126 + values[1] * .7152 + values[2] * .0722;
}
function contrast(first: string, second: string) {
  const a = luminance(first), b = luminance(second);
  return (Math.max(a, b) + .05) / (Math.min(a, b) + .05);
}
test("every existing surface uses shared tokens with complete light/dark and first-paint palettes", () => {
  expect(palettes).toHaveLength(3);
  expect([...palettes[1].keys()]).toEqual([...palettes[0].keys()]);
  expect(palettes[2]).toEqual(palettes[1]);
  for (const filename of readdirSync(directory).filter(name => name.endsWith(".css") && name !== "themes.css")) {
    const css = readFileSync(join(directory, filename), "utf8");
    expect(css, filename).not.toMatch(/#[\da-fA-F]{3,8}\b|:\s*white\b|:\s*black\b|rgba?\(/);
    expect(css, filename).not.toMatch(/(--[\w-]+):\s*var\(\1\)/);
    for (const match of css.matchAll(/var\(--([\w-]+)\)/g)) {
      // Quota fills are component-local interpolation of audited semantic tokens.
      // Keep the exception bounded to their two consumers and validate its inputs.
      if (match[1] === "quota-fill") {
        expect(["styles.css", "api-account.css"]).toContain(filename);
        expect(quotaColors).toContain('"--quota-fill": quotaColor(percent)');
        for (const token of quotaColors.matchAll(/var\(--([\w-]+)\)/g)) expect(palettes[0].has(token[1]), `quota-color: ${token[1]}`).toBe(true);
      } else expect(palettes[0].has(match[1]), `${filename}: ${match[1]}`).toBe(true);
    }
  }
  expect(source).toContain(':root[data-theme="dark"]');
  expect(source).toContain("@media (prefers-color-scheme: dark)");
});

test("semantic text, states, primary controls and focus retain accessible contrast in both themes", () => {
  for (const palette of palettes.slice(0, 2)) {
    for (const [foreground, background] of [["text", "background"], ["text", "surface"], ["muted", "surface"], ["text-subtle", "surface"], ["link", "surface"], ["selected-text", "selected-background"], ["warning-text", "warning-background"], ["danger-text", "danger-background"], ["success-text", "surface"], ["on-accent", "accent"], ["on-inverse", "inverse-surface"], ["on-inverse-muted", "inverse-surface"], ["conversation-text", "conversation-background"], ["terminal-foreground", "terminal-background"], ["terminal-muted", "terminal-background"], ["terminal-foreground", "terminal-selection"]]) {
      expect(contrast(palette.get(foreground)!, palette.get(background)!), `${foreground} / ${background}`).toBeGreaterThanOrEqual(4.5);
    }
    for (const background of ["surface", "background", "selected-background"]) expect(contrast(palette.get("focus")!, palette.get(background)!), `focus / ${background}`).toBeGreaterThanOrEqual(3);
    for (const background of ["terminal-background", "terminal-selection"]) expect(contrast(palette.get("terminal-border")!, palette.get(background)!), `terminal-border / ${background}`).toBeGreaterThanOrEqual(3);
    // Terminal focus is drawn outside selected buttons against the dock background.
    expect(contrast(palette.get("focus")!, palette.get("terminal-background")!)).toBeGreaterThanOrEqual(3);
    expect(contrast(palette.get("control-border")!, palette.get("surface")!)).toBeGreaterThanOrEqual(3);
  }
});
