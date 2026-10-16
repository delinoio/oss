// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { expect, test } from "vitest";
import {
  contrast,
  defaultPreferences,
  palettes,
  parsePreferences,
  selectedColors,
  validColors,
  type ColorMap,
} from "./appearance-preferences";

const cores = {
  titanium: {
    light: ["#F4F4F5", "#FFFFFF", "#E4E4E7", "#27272A", "#52525B"],
    dark: ["#18181B", "#27272A", "#09090B", "#FAFAFA", "#52525B"],
  },
  nord: {
    light: ["#ECEFF4", "#E5E9F0", "#D8DEE9", "#2E3440", "#3A5877"],
    dark: ["#2E3440", "#3B4252", "#242933", "#ECEFF4", "#3A5877"],
  },
  dracula: {
    light: ["#F7F3FF", "#F0E9FA", "#E6DCF3", "#382C4A", "#7143A5"],
    dark: ["#282A36", "#343746", "#21222C", "#F8F8F2", "#7143A5"],
  },
  solarized: {
    light: ["#FDF6E3", "#EEE8D5", "#E3DCC8", "#4B6068", "#006B82"],
    dark: ["#002B36", "#073642", "#001F27", "#D6D3C4", "#006B82"],
  },
} as const;

// The approved derivation blends encoded sRGB, with rounding per channel.
// This generator is a fixture only; the application consumes stored JSON.
function mix(first: string, second: string, weight: number) {
  const channels = [1, 3, 5].map((offset) =>
    Math.round(
      weight * parseInt(first.slice(offset, offset + 2), 16) +
        (1 - weight) * parseInt(second.slice(offset, offset + 2), 16),
    )
      .toString(16)
      .padStart(2, "0"),
  );
  return `#${channels.join("").toUpperCase()}`;
}

function derived(core: readonly string[], mode: "light" | "dark"): ColorMap {
  const [b, s, i, t, a] = core;
  const q = mix(a, s, 0.08),
    c = mix(t, s, 0.8);
  const p = mode === "light" ? "#FFFFFF" : "#000000";
  return {
    ...palettes.default[mode],
    background: b,
    surface: s,
    "surface-inset": i,
    text: t,
    accent: a,
    "surface-subtle": mix(b, s, 0.5),
    "surface-muted": mix(i, s, 0.25),
    "surface-hover": mix(t, s, 0.08),
    "surface-selected": q,
    "selected-background": q,
    "selected-text": t,
    "selected-border": c,
    "text-secondary": mix(t, s, 0.98),
    muted: mix(t, s, 0.97),
    "text-subtle": mix(t, s, 0.96),
    border: mix(t, s, 0.25),
    "control-border": c,
    "border-subtle": mix(t, s, 0.15),
    "accent-hover": mix("#000000", a, 0.15),
    "on-accent": "#FFFFFF",
    link: mode === "light" ? a : mix("#FFFFFF", a, 0.65),
    focus: mode === "light" ? a : mix("#FFFFFF", a, 0.65),
    "inverse-surface": t,
    "inverse-hover": mix(p, t, 0.1),
    "inverse-border": mix(p, t, 0.65),
    "on-inverse": p,
    "on-inverse-muted": mix(p, t, 0.9),
    "conversation-background": q,
    "conversation-text": t,
    "conversation-border": c,
    "execution-running":
      mode === "light"
        ? mix("#000000", "#087B78", 0.15)
        : palettes.default.dark["execution-running"],
  };
}

test("Default retains its complete original maps and five stable palette IDs", () => {
  expect(Object.keys(palettes)).toEqual(["default", ...Object.keys(cores)]);
  expect(
    createHash("sha256").update(JSON.stringify(palettes.default)).digest("hex"),
  ).toBe("08952cf9a976148b4192c87a19273f717e06686aa104a81936c62c2082e064ab");
});

const splitSelectorList = (selectors: string) => {
  const result: string[] = [];
  let start = 0;
  let parentheses = 0;
  let brackets = 0;
  let quote: string | undefined;
  let escaped = false;

  for (let index = 0; index < selectors.length; index += 1) {
    const character = selectors[index];
    if (escaped) {
      escaped = false;
    } else if (character === "\\") {
      escaped = true;
    } else if (quote) {
      if (character === quote) quote = undefined;
    } else if (character === '"' || character === "'") {
      quote = character;
    } else if (character === "(") {
      parentheses += 1;
    } else if (character === ")") {
      parentheses -= 1;
    } else if (character === "[") {
      brackets += 1;
    } else if (character === "]") {
      brackets -= 1;
    } else if (character === "," && parentheses === 0 && brackets === 0) {
      result.push(selectors.slice(start, index).trim());
      start = index + 1;
    }
  }

  result.push(selectors.slice(start).trim());
  return result;
};

test("ordinary controls use semantic accent and contrast tokens", async () => {
  const stylesheet = async (name: string) =>
    readFile(resolve(process.cwd(), `src/${name}`), "utf8");
  const declarations = async (
    name: string,
    checks: [selector: string, property: string, value: string][],
  ) => {
    const styles = await stylesheet(name);
    const rules = [...styles.matchAll(/([^{}]+)\{([^{}]*)\}/gu)];
    for (const [selector, property, value] of checks) {
      const rule = rules.find(
        ([, selectors, declarations]) =>
          splitSelectorList(selectors).includes(selector) &&
          declarations.split(";").some((candidate) => {
            const separator = candidate.indexOf(":");
            return (
              separator >= 0 &&
              candidate.slice(0, separator).trim() === property &&
              candidate.slice(separator + 1).includes(`var(--${value})`)
            );
          }),
      );
      expect(rule?.[2], `${name} ${selector} ${property}`).toBeDefined();
    }
  };

  await declarations("styles.css", [
    [".new-session-submit", "color", "on-accent"],
    [".sidebar-new-session", "border-color", "control-border"],
    [".usage-row-measures .usage-metric", "border-color", "control-border"],
    [".inbox-row[aria-current]", "border-color", "control-border"],
    [".inbox-row[aria-current]", "border-left-color", "selected-border"],
    [".integration-empty-mark", "color", "selected-border"],
    [".settings-runner-column:focus-visible", "outline-color", "focus"],
    [".usage-line", "stroke", "selected-border"],
    [".usage-point", "fill", "selected-border"],
    [".usage-model-bar", "fill", "selected-border"],
    [".usage-model-labels li", "border-left", "selected-border"],
  ]);
  await declarations("session.css", [
    [".queue-insertion", "border-top", "selected-border"],
    [".context-capacity > div", "background", "selected-border"],
  ]);
  await declarations("schedule-creation.css", [
    ['.schedule-creation-progress li[aria-current="step"]', "color", "link"],
    [
      ".schedule-creation-radio-cards label:has(:checked)",
      "border-color",
      "selected-border",
    ],
  ]);
  await declarations("api-account.css", [
    [".account-oauth-card .account-oauth-fallback", "color", "link"],
  ]);
  await declarations("agent-configuration.css", [
    [".agent-configuration .agent-required", "color", "link"],
    [
      ".agent-configuration:is(button, summary):focus-visible",
      "outline",
      "focus",
    ],
  ]);
  await declarations("agent-worker-wizard.css", [
    [".worker-account-add", "color", "link"],
    [
      '.worker-source-group[data-expanded="true"]',
      "border-color",
      "selected-border",
    ],
  ]);
  await declarations("credential-access.css", [
    [".credential-access-brand", "color", "link"],
  ]);
  await declarations("tab-strip.css", [
    [".tab-strip .tab-item.is-selected", "color", "selected-text"],
    [
      '.tab-strip .tab-item[aria-selected="true"]',
      "border-bottom-color",
      "selected-border",
    ],
    [".tab-strip .tab-label:focus-visible", "outline", "focus"],
    [".tab-strip .tab-close:focus-visible", "outline", "focus"],
  ]);
});

for (const [name, modes] of Object.entries(cores)) {
  for (const mode of ["light", "dark"] as const) {
    test(`${name} ${mode} matches the approved complete semantic derivation`, () => {
      const colors = palettes[name as keyof typeof cores][mode];
      expect(colors).toEqual(derived(modes[mode], mode));
      expect(Object.keys(colors)).toEqual(Object.keys(palettes.default[mode]));
      expect(Object.keys(colors)).toHaveLength(38);
      expect(
        Object.values(colors).every((value) => /^#[0-9A-F]{6}$/.test(value)),
      ).toBe(true);
      expect(validColors(colors)).toBe(true);
    });

    test(`${name} ${mode} retains contrast for application text and adjacent controls`, () => {
      const colors = palettes[name as keyof typeof cores][mode];
      const check = (fg: keyof ColorMap, bg: keyof ColorMap, minimum = 4.5) =>
        expect(
          contrast(colors[fg], colors[bg]),
          `${fg} / ${bg}`,
        ).toBeGreaterThanOrEqual(minimum);
      const surfaces: (keyof ColorMap)[] = [
        "background",
        "surface",
        "surface-subtle",
        "surface-muted",
        "surface-inset",
        "surface-hover",
        "surface-selected",
        "selected-background",
        "conversation-background",
      ];
      for (const bg of surfaces) {
        for (const fg of [
          "text",
          "text-secondary",
          "muted",
          "success-text",
          "execution-running",
        ] as const)
          check(fg, bg);
        for (const fg of [
          "focus",
          "control-border",
          "selected-border",
          "conversation-border",
        ] as const)
          check(fg, bg, 3);
      }
      // Subtle text and links use ordinary surfaces. Their selected-row uses
      // are 14px decorative/status glyphs, rather than normal body text.
      for (const bg of [
        "background",
        "surface",
        "surface-subtle",
        "surface-muted",
      ] as const) {
        check("text-subtle", bg);
        check("link", bg);
      }
      for (const bg of ["surface-hover", "surface-selected"] as const) {
        check("text-subtle", bg, 3);
        check("link", bg, 3);
      }
      for (const [fg, bg] of [
        ["on-accent", "accent"],
        ["on-accent", "accent-hover"],
        ["selected-text", "selected-background"],
        ["conversation-text", "conversation-background"],
        ["warning-text", "warning-background"],
        ["danger-text", "danger-background"],
        ["on-inverse", "inverse-surface"],
        ["on-inverse", "inverse-hover"],
        ["on-inverse-muted", "inverse-surface"],
      ] as [keyof ColorMap, keyof ColorMap][])
        check(fg, bg);
      check("inverse-border", "inverse-surface", 3);
    });
  }
}

test("saved bundled references adopt full maps while existing duplicates retain their bytes", () => {
  // Earlier duplicates copied Default's auxiliary values, with their own
  // background and accent. They remain complete independent custom maps.
  const legacy = {
    version: 1 as const,
    id: "01900000-0000-7000-8000-000000000001",
    name: "Earlier duplicate",
    light: {
      ...palettes.default.light,
      background: "#ECEFF4",
      accent: "#365D84",
    },
    dark: {
      ...palettes.default.dark,
      background: "#282A36",
      accent: "#644591",
    },
  };
  const bytes = JSON.stringify(legacy);
  const preferences = parsePreferences({
    ...defaultPreferences(),
    light_palette: "nord",
    dark_palette: "dracula",
    custom_themes: [legacy],
  });
  expect(selectedColors(preferences, false)).toEqual(palettes.nord.light);
  expect(selectedColors(preferences, true)).toEqual(palettes.dracula.dark);
  preferences.light_palette = legacy.id;
  preferences.dark_palette = legacy.id;
  expect(selectedColors(preferences, false)).toEqual(legacy.light);
  expect(selectedColors(preferences, true)).toEqual(legacy.dark);
  expect(JSON.stringify(preferences.custom_themes[0])).toBe(bytes);
});
