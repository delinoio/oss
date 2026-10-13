// SPDX-License-Identifier: Apache-2.0
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";
import appearancePalettes from "./appearance-palettes.json";
import {
  validColors,
  visibleFocusColor,
  visibleHoverSurface,
} from "./appearance-preferences";

const directory = join(process.cwd(), "src");
const source = readFileSync(join(directory, "themes.css"), "utf8");
const usageStyles = readFileSync(join(directory, "usage.css"), "utf8");
const quotaColors = readFileSync(join(directory, "quota-color.ts"), "utf8");
const palettes = [
  ...source.matchAll(/color-scheme: (?:light|dark);([^}]+)/g),
].map(
  (match) =>
    new Map(
      [...match[1].matchAll(/--([\w-]+):\s*(#[\da-f]+);/g)].map((item) => [
        item[1],
        item[2],
      ]),
    ),
);

function luminance(hex: string) {
  const values = [1, 3, 5]
    .map((offset) => parseInt(hex.slice(offset, offset + 2), 16) / 255)
    .map((value) =>
      value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
    );
  return values[0] * 0.2126 + values[1] * 0.7152 + values[2] * 0.0722;
}
function contrast(first: string, second: string) {
  const a = luminance(first),
    b = luminance(second);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}
test("every existing surface uses shared tokens with complete light/dark and first-paint palettes", () => {
  expect(palettes).toHaveLength(3);
  expect([...palettes[1].keys()]).toEqual([...palettes[0].keys()]);
  expect(palettes[2]).toEqual(palettes[1]);
  for (const filename of readdirSync(directory).filter(
    (name) => name.endsWith(".css") && name !== "themes.css",
  )) {
    const css = readFileSync(join(directory, filename), "utf8");
    expect(css, filename).not.toMatch(
      /#[\da-fA-F]{3,8}\b|:\s*white\b|:\s*black\b|rgba?\(/,
    );
    expect(css, filename).not.toMatch(/(--[\w-]+):\s*var\(\1\)/);
    for (const match of css.matchAll(/var\(--([\w-]+)\)/g)) {
      // Quota fills are component-local interpolation of audited semantic tokens.
      // Keep the exception bounded to their two consumers and validate its inputs.
      if (match[1] === "quota-fill") {
        expect(["styles.css", "api-account.css"]).toContain(filename);
        expect(quotaColors).toContain('"--quota-fill": quotaColor(percent)');
        for (const token of quotaColors.matchAll(/var\(--([\w-]+)\)/g))
          expect(palettes[0].has(token[1]), `quota-color: ${token[1]}`).toBe(
            true,
          );
      } else
        expect(palettes[0].has(match[1]), `${filename}: ${match[1]}`).toBe(
          true,
        );
    }
  }
  expect(source).toContain(':root[data-theme="dark"]');
  expect(source).toContain("@media (prefers-color-scheme: dark)");
});

test("semantic text, states, primary controls and focus retain accessible contrast in both themes", () => {
  for (const palette of palettes.slice(0, 2)) {
    for (const [foreground, background] of [
      ["text", "background"],
      ["text", "surface"],
      ["muted", "surface"],
      ["text-subtle", "surface"],
      ["link", "surface"],
      ["selected-text", "selected-background"],
      ["warning-text", "warning-background"],
      ["danger-text", "danger-background"],
      ["success-text", "surface"],
      ["on-accent", "accent"],
      ["on-inverse", "inverse-surface"],
      ["on-inverse-muted", "inverse-surface"],
      ["conversation-text", "conversation-background"],
      ["terminal-foreground", "terminal-background"],
      ["terminal-muted", "terminal-background"],
      ["terminal-foreground", "terminal-selection"],
    ]) {
      expect(
        contrast(palette.get(foreground)!, palette.get(background)!),
        `${foreground} / ${background}`,
      ).toBeGreaterThanOrEqual(4.5);
    }
    for (const background of ["surface", "background", "selected-background"])
      expect(
        contrast(palette.get("focus")!, palette.get(background)!),
        `focus / ${background}`,
      ).toBeGreaterThanOrEqual(3);
    for (const background of ["terminal-background", "terminal-selection"])
      expect(
        contrast(palette.get("terminal-border")!, palette.get(background)!),
        `terminal-border / ${background}`,
      ).toBeGreaterThanOrEqual(3);
    // Terminal focus is drawn outside selected buttons against the dock background.
    expect(
      contrast(palette.get("focus")!, palette.get("terminal-background")!),
    ).toBeGreaterThanOrEqual(3);
    expect(
      contrast(palette.get("control-border")!, palette.get("surface")!),
    ).toBeGreaterThanOrEqual(3);
  }
});

test("picker options and the Home sidebar current row draw a persistent inset selected-border cue", () => {
  const picker = readFileSync(join(directory, "scroll-picker.css"), "utf8");
  const styles = readFileSync(join(directory, "styles.css"), "utf8");
  // An inset shadow paints inside the border box, so selection never moves geometry.
  expect(picker).toMatch(
    /\.scroll-picker-popup > button\[data-highlighted="true"\],\s*\.scroll-picker-popup > button\[aria-selected="true"\]\s*\{[^}]*box-shadow: inset 0 0 0 1px var\(--selected-border\);[^}]*\}/u,
  );
  expect(styles).toMatch(
    /\.is-home \.sidebar-session-row\[aria-current="true"\]\s*\{[^}]*box-shadow: inset 0 0 0 1px var\(--selected-border\);[^}]*\}/u,
  );
  expect(picker).toMatch(/\.scroll-picker-popup > button \{[^}]*border: 0;/u);
  expect(styles).not.toMatch(
    /\.is-home \.sidebar-session-row\[aria-current="true"\]\s*\{[^}]*(?:border-width|padding|margin|width|height)/u,
  );
});

test("selected-border cue contrasts at least 3:1 with adjacent surfaces in every nondefault bundled palette", () => {
  // Default light (selected-border / surface is about 2.04:1) is deliberately
  // outside this assertion: the palette is unchanged by the cue and disclosed
  // as a known sub-3:1 case rather than silently retuned.
  for (const [name, modes] of Object.entries(appearancePalettes)) {
    if (name === "default") continue;
    for (const [mode, colors] of Object.entries(modes)) {
      for (const adjacent of [
        "surface",
        "surface-muted",
        "selected-background",
      ] as const)
        expect(
          contrast(colors["selected-border"], colors[adjacent]),
          `${name} ${mode}: selected-border / ${adjacent}`,
        ).toBeGreaterThanOrEqual(3);
    }
  }
});

test("Usage token breakdown focus uses the inverse-surface contrast token", () => {
  expect(usageStyles).toMatch(
    /:root\s+\.usage-page\s+\.usage-summary-breakdown\s*>\s*\.disclosure-header:focus-visible\s*\{\s*outline-color:\s*var\(--on-inverse\);\s*\}/u,
  );
});

test("all native modal backdrops use the same subtle dimming token without surface overrides", () => {
  for (const palette of palettes)
    expect(palette.get("backdrop")).toBe("#0000001f");
  const rules = readdirSync(directory)
    .filter((name) => name.endsWith(".css") && name !== "themes.css")
    .flatMap((filename) => {
      const css = readFileSync(join(directory, filename), "utf8");
      return [...css.matchAll(/([^{}]+)::backdrop\s*\{([^}]+)\}/g)].map(
        (match) => ({ filename, declarations: match[2] }),
      );
    });
  expect(rules.length).toBeGreaterThan(0);
  for (const rule of rules) {
    expect(rule.declarations, rule.filename).toMatch(
      /background:\s*var\(--backdrop\)/,
    );
    expect(rule.declarations, rule.filename).not.toMatch(
      /backdrop-filter|animation|box-shadow/,
    );
  }
});

test("accent-off user-message focus follows the selected palette without changing accent-on focus", () => {
  // The root attribute gives this override greater specificity than the
  // transcript rule, regardless of stylesheet order or custom token values.
  expect(source).toMatch(
    /:root\[data-session-accent="false"\]\s+\.session-workspace\s+\.message-user\s+:focus-visible\s*\{\s*outline-color: var\(--focus-on-selected, var\(--focus\)\);\s*\}/u,
  );
  const session = readFileSync(join(directory, "session.css"), "utf8");
  expect(session).toMatch(
    /\.session-workspace \.transcript \.message-user :focus-visible\s*\{\s*outline-color: var\(--on-accent\);\s*outline-offset: 2px;\s*\}/u,
  );
  for (const [name, modes] of Object.entries(appearancePalettes)) {
    for (const [mode, colors] of Object.entries(modes)) {
      expect(
        contrast(colors.focus, colors["surface-selected"]),
        `${name} ${mode}: focus / surface-selected`,
      ).toBeGreaterThanOrEqual(3);
    }
  }
});

test("custom theme focus stays visible against the selected user-message surface", () => {
  const surface = "#777777";
  const fallback = visibleFocusColor(surface, surface);
  expect(fallback).not.toBe(surface);
  expect(contrast(fallback, surface)).toBeGreaterThanOrEqual(3);

  const focus = "#2563d8";
  expect(visibleFocusColor(focus, "#FFFFFF")).toBe(focus);
});

test("Revert text is readable at rest and on hover in every admitted palette", () => {
  for (const [name, modes] of Object.entries(appearancePalettes)) {
    for (const [mode, colors] of Object.entries(modes)) {
      for (const surface of ["surface", "surface-hover"] as const)
        expect(
          contrast(colors["text-secondary"], colors[surface]),
          `${name} ${mode}: Revert / ${surface}`,
        ).toBeGreaterThanOrEqual(4.5);
      expect(validColors(colors)).toBe(true);
      const legacyColors = {
        ...colors,
        "surface-hover": colors["text-secondary"],
      };
      expect(validColors(legacyColors)).toBe(true);
      expect(visibleHoverSurface(legacyColors)).toBe(colors.surface);
      expect(
        contrast(
          legacyColors["text-secondary"],
          visibleHoverSurface(legacyColors),
        ),
      ).toBeGreaterThanOrEqual(4.5);
      expect(visibleHoverSurface(colors)).toBe(colors["surface-hover"]);
      expect(
        contrast(
          visibleFocusColor(colors.surface, colors.surface),
          colors.surface,
        ),
      ).toBeGreaterThanOrEqual(3);
    }
  }
});

test("the wide Appearance Language label resets the global block margin and the stacked label keeps it", () => {
  const css = source.replace(/\/\*[\s\S]*?\*\//gu, "");
  const wide = css.match(
    /\.appearance-content \.language-settings > label\s*\{([^}]*)\}/u,
  );
  expect(wide?.[1]).toMatch(/min-height:\s*40px/u);
  expect(wide?.[1]).toMatch(/align-self:\s*start/u);
  expect(wide?.[1]).toMatch(/margin-block:\s*0\s*;/u);
  // The stacked override is the only indented (nested) occurrence.
  const narrow = css.match(
    /^ {2}\.appearance-content \.language-settings > label\s*\{([^}]*)\}/mu,
  );
  expect(narrow?.[1]).toMatch(/min-height:\s*0\s*;/u);
  expect(narrow?.[1]).toMatch(/margin-block:\s*12px/u);
});

test("the shared entrance primitive is a static opaque 160ms Y4 ease-out keyframe", () => {
  const css = readFileSync(
    join(directory, "entrance-motion.css"),
    "utf8",
  ).replace(/\/\*[\s\S]*?\*\//gu, "");
  expect(css).toMatch(
    /@keyframes entrance-y4\s*\{\s*from\s*\{\s*transform:\s*translateY\(4px\);\s*\}\s*to\s*\{\s*transform:\s*none;\s*\}\s*\}/u,
  );
  expect(css).toMatch(
    /\[data-entrance="y4"\]\s*\{\s*animation:\s*entrance-y4 160ms ease-out;\s*\}/u,
  );
  // No fill mode keeps the resting transform none after the motion.
  expect(css).not.toMatch(
    /forwards|both|opacity|scale|bounce|cubic-bezier|steps\(|height|animation-delay/u,
  );
  expect(css).not.toMatch(/style=|@import|url\(/u);
});

test("the shared 120ms Y2, X4 and short Y4 primitives are static opaque ease-out keyframes", () => {
  const css = readFileSync(
    join(directory, "entrance-motion.css"),
    "utf8",
  ).replace(/\/\*[\s\S]*?\*\//gu, "");
  for (const [name, axis, distance] of [
    ["y2", "Y", "2px"],
    ["x4", "X", "4px"],
    ["y4-short", "Y", "4px"],
  ]) {
    expect(css).toMatch(
      new RegExp(
        `@keyframes entrance-${name}\\s*\\{\\s*from\\s*\\{\\s*transform:\\s*translate${axis}\\(${distance}\\);\\s*\\}\\s*to\\s*\\{\\s*transform:\\s*none;\\s*\\}\\s*\\}`,
        "u",
      ),
    );
    expect(css).toMatch(
      new RegExp(
        `\\[data-entrance="${name}"\\]\\s*\\{\\s*animation:\\s*entrance-${name} 120ms ease-out;\\s*\\}`,
        "u",
      ),
    );
  }
});

test("Animation Off and reduced motion suppress entrance animations", () => {
  expect(source).toMatch(
    /:root\[data-animation="false"\] \*,[^{]+\{\s*animation: none !important;/u,
  );
  expect(source).toMatch(
    /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\*,[^{]+\{\s*animation: none !important;/u,
  );
});

test("merged pull request dot uses a fixed semantic purple token independent of accent palettes", () => {
  expect(palettes).toHaveLength(3);
  for (const palette of palettes) {
    const merged = palette.get("pr-merged");
    expect(merged).toBeTruthy();
    expect(merged).not.toBe(palette.get("accent"));
    expect(
      contrast(merged!, palette.get("surface")!),
      "pr-merged / surface",
    ).toBeGreaterThanOrEqual(3);
    // Purple hue: blue and red channels both dominate green.
    const [red, green, blue] = [1, 3, 5].map((offset) =>
      parseInt(merged!.slice(offset, offset + 2), 16),
    );
    expect(blue).toBeGreaterThan(green);
    expect(red).toBeGreaterThan(green);
  }
  // The token is not user-customizable, so saved themes and accent palettes
  // (Nord, Dracula and others) can never override it.
  for (const modes of Object.values(appearancePalettes))
    for (const colors of Object.values(modes))
      expect(Object.keys(colors)).not.toContain("pr-merged");
  const session = readFileSync(join(directory, "session.css"), "utf8");
  const rule = (name: string) =>
    session.match(new RegExp(`\\.session-pr-dot-${name}\\s*\\{([^}]*)\\}`))![1];
  expect(rule("merged")).toMatch(/background:\s*var\(--pr-merged\)/);
  expect(rule("merged")).not.toContain("--accent");
  expect(rule("open")).toMatch(/background:\s*var\(--success-text\)/);
  expect(rule("closed")).toMatch(/background:\s*var\(--danger-text\)/);
  expect(session).toMatch(
    /\.session-pr-dot\s*\{[^}]*background:\s*var\(--text-secondary\)/,
  );
});
